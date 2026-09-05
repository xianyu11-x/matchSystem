param(
    [ValidateSet('Check', 'Prepare', 'Apply', 'Library')][string]$Mode,
    [string]$Repository, [string]$CurrentVersion, [string]$Architecture,
    [string]$InstallDirectory, [string]$ExpectedVersion, [string]$ContextPath,
    [int]$OwnerPid
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

function Write-JsonFile($Path, $Value) {
    [IO.File]::WriteAllText($Path, ($Value | ConvertTo-Json -Depth 8), [Text.UTF8Encoding]::new($false))
}
function Get-StableVersion([string]$Tag) {
    if ($Tag -cnotmatch '^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') { throw 'Release tag must be a stable vMAJOR.MINOR.PATCH version.' }
    return [version]$Tag.TrimStart('v')
}
function Assert-DownloadUrl([string]$Url, [string]$Repo) {
    $uri = [uri]$Url
    if ($uri.Scheme -ne 'https' -or $uri.Host -ne 'github.com' -or
        -not $uri.AbsolutePath.StartsWith("/$Repo/releases/download/", [StringComparison]::Ordinal) -or $uri.UserInfo) {
        throw 'Release asset URL is outside the configured GitHub repository.'
    }
}
function Get-Release {
    if ($Repository -notmatch '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$') { throw 'Invalid GitHub repository.' }
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repository/releases/latest" -Headers @{ 'User-Agent'='MatchScope-Updater'; Accept='application/vnd.github+json' } -TimeoutSec 30
    $latest = Get-StableVersion $release.tag_name
    if ($release.draft -or $release.prerelease) { throw 'Only published stable releases are supported.' }
    $asset = $null
    foreach ($name in @("MatchScope-$latest-windows-$Architecture-portable.zip", "MatchScope-$latest-windows-$Architecture.zip")) {
        $matches = @($release.assets | Where-Object { $_.name -ceq $name -and $_.state -eq 'uploaded' })
        if ($matches.Count -eq 1) { $asset = $matches[0]; break }
    }
    return @{ currentVersion=$CurrentVersion; version=$latest.ToString(); available=($latest -gt (Get-StableVersion $CurrentVersion)); repository=$Repository; notes=[string]$release.body; asset=$asset; assets=$release.assets }
}
function Get-Sha256($Path) {
    $hash = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::OpenRead($Path)
    try { return ([BitConverter]::ToString($hash.ComputeHash($stream))).Replace('-', '').ToLowerInvariant() }
    finally { $stream.Dispose(); $hash.Dispose() }
}
function Expand-Portable($Zip, $Destination) {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [IO.Compression.ZipFile]::OpenRead($Zip)
    try {
        if ($archive.Entries.Count -gt 4096) { throw 'Too many ZIP entries.' }
        $names = @($archive.Entries | ForEach-Object { $_.FullName.Replace('\','/') })
        $prefix = if ($names -ccontains 'portable/MatchScope.exe') { 'portable/' } else { '' }
        $seen = @{}
        [long]$total = 0
        foreach ($entry in $archive.Entries) {
            $name = $entry.FullName.Replace('\','/')
            # Reject dangerous entries even outside portable/.
            if ($name -match '(^/|:|(^|/)\.\.?(/|$))' -or $name -match '[ .](/|$)' -or $name -match '(^|/)(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])(\.|/|$)' -or (($entry.ExternalAttributes -shr 16) -band 0xF000) -eq 0xA000) { throw "Unsafe ZIP entry: $name" }
            if (-not $name.StartsWith($prefix, [StringComparison]::Ordinal)) { continue }
            $relative = $name.Substring($prefix.Length)
            if (-not $relative -or $relative.EndsWith('/')) { continue }
            if ($seen.ContainsKey($relative)) { throw 'Duplicate ZIP path.' }
            $seen[$relative] = $true
            $total += $entry.Length
            if ($total -gt 2GB) { throw 'Expanded ZIP exceeds 2 GiB.' }
            $target = [IO.Path]::GetFullPath((Join-Path $Destination $relative))
            if (-not $target.StartsWith($Destination.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'ZIP path escapes staging directory.' }
            [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target)) | Out-Null
            [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $target, $false)
        }
        foreach ($required in @('MatchScope.exe','simulator-api.exe')) {
            $file = Join-Path $Destination $required
            if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { throw "Missing $required in portable archive." }
            $stream = [IO.File]::OpenRead($file)
            try { if ($stream.ReadByte() -ne 77 -or $stream.ReadByte() -ne 90) { throw "Invalid executable: $required" } } finally { $stream.Dispose() }
        }
    } finally { $archive.Dispose() }
}
function Assert-Sibling($Path, $Parent) {
    $full = [IO.Path]::GetFullPath($Path)
    if ([IO.Path]::GetDirectoryName($full) -ne $Parent) { throw 'Update directory must be an immediate sibling of the installation.' }
    if ((Test-Path -LiteralPath $full) -and ((Get-Item -LiteralPath $full).Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Update paths cannot be links or junctions.' }
}
function Invoke-Apply($c) {
    $parent = [IO.Path]::GetDirectoryName($c.install)
    foreach ($path in @($c.install, $c.stage, $c.backup, $c.failed)) { Assert-Sibling $path $parent }
    # Reject simultaneous transactions across different app instances as well.
    $lockPath = Join-Path $parent ('.' + [IO.Path]::GetFileName($c.install) + '.matchscope-update.lock')
    $transactionLock = [IO.File]::Open($lockPath, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
    $oldMoved = $false; $newMoved = $false; $child = $null
    [IO.File]::WriteAllText($c.armed, 'ready')
    try {
        $owner = Get-Process -Id $c.pid -ErrorAction SilentlyContinue
        if ($owner -and -not $owner.WaitForExit(60000)) { throw 'Current application did not exit within 60 seconds.' }
        # Retry directory moves while Windows releases the owned sidecar handles.
        for ($attempt=0; ; $attempt++) {
            try { Move-Item -LiteralPath $c.install -Destination $c.backup; $oldMoved=$true; break }
            catch { if ($attempt -ge 39) { throw }; Start-Sleep -Milliseconds 250 }
        }
        Move-Item -LiteralPath $c.stage -Destination $c.install; $newMoved=$true
        Write-JsonFile (Join-Path $c.install '.matchscope-update-status.json') @{ ok=$null; pending=$true; message='New version is starting; waiting for its health acknowledgement.' }
        $env:MATCHSCOPE_UPDATE_ACK = $c.ack
        $child = Start-Process -FilePath (Join-Path $c.install 'MatchScope.exe') -WorkingDirectory $c.install -WindowStyle Hidden -PassThru
        $deadline = [DateTime]::UtcNow.AddSeconds(45)
        while (-not (Test-Path -LiteralPath $c.ack)) {
            if ($child.HasExited -or [DateTime]::UtcNow -ge $deadline) { throw 'Updated application failed its startup health check.' }
            Start-Sleep -Milliseconds 250
        }
        Write-JsonFile (Join-Path $c.install '.matchscope-update-status.json') @{ ok=$true; message="Updated successfully. Previous files: $($c.backup)" }
    } catch {
        $failure = $_.Exception.Message
        if ($child -and -not $child.HasExited) { & taskkill.exe /PID $child.Id /T /F 2>&1 | Out-Null }
        if ($newMoved) { Move-Item -LiteralPath $c.install -Destination $c.failed }
        if ($oldMoved) { Move-Item -LiteralPath $c.backup -Destination $c.install }
        Write-JsonFile (Join-Path $c.install '.matchscope-update-status.json') @{ ok=$false; message="Update failed; previous version restored. $failure" }
        if ($oldMoved) {
            Remove-Item Env:MATCHSCOPE_UPDATE_ACK -ErrorAction SilentlyContinue
            Start-Process -FilePath (Join-Path $c.install 'MatchScope.exe') -WorkingDirectory $c.install -WindowStyle Hidden
        }
    } finally { $transactionLock.Dispose() }
}
if ($Mode -eq 'Library') { return }
try {
    if ($Mode -eq 'Apply') {
        try { Invoke-Apply (Get-Content -Raw -LiteralPath $ContextPath | ConvertFrom-Json) }
        catch {
            Write-JsonFile (Join-Path ([IO.Path]::GetDirectoryName($ContextPath)) 'recovery-error.json') @{ ok=$false; message=$_.Exception.Message; context=$ContextPath }
            throw
        }
        exit 0
    }
    $release = Get-Release
    if ($Mode -eq 'Check') {
        $release.Remove('assets'); $release.assetName = if ($release.asset) { $release.asset.name } else { $null }; $release.Remove('asset')
        $release | ConvertTo-Json -Depth 8 -Compress; exit 0
    }
    if (-not $release.available -or $release.version -ne $ExpectedVersion -or -not $release.asset) { throw 'Release changed or has no compatible Windows ZIP. Check again.' }
    $install = [IO.Path]::GetFullPath($InstallDirectory).TrimEnd('\')
    $parent = [IO.Path]::GetDirectoryName($install)
    Assert-Sibling $install $parent
    if (-not (Test-Path -LiteralPath (Join-Path $install 'MatchScope.exe'))) { throw 'Automatic update requires a portable MatchScope.exe installation.' }
    $work = Join-Path $parent ('.matchscope-update-' + [guid]::NewGuid().ToString('N'))
    [IO.Directory]::CreateDirectory($work) | Out-Null
    $asset = $release.asset
    Assert-DownloadUrl $asset.browser_download_url $Repository
    if ($asset.size -le 0 -or $asset.size -gt 1GB) { throw 'Invalid ZIP size (maximum 1 GiB).' }
    $zip = Join-Path $work 'release.zip'
    Invoke-WebRequest -UseBasicParsing -Uri $asset.browser_download_url -OutFile $zip -TimeoutSec 600
    if ((Get-Item -LiteralPath $zip).Length -ne $asset.size) { throw 'Incomplete ZIP download.' }
    $expected = ''
    if ([string]$asset.digest -match '^sha256:([0-9a-fA-F]{64})$') { $expected = $Matches[1] }
    else {
        $checksum = @($release.assets | Where-Object { $_.name -ceq ($asset.name + '.sha256') -or $_.name -ceq 'SHA256SUMS.txt' } | Select-Object -First 1)
        if ($checksum.Count -ne 1) { throw 'Release requires a GitHub SHA-256 digest or uploaded checksum asset.' }
        Assert-DownloadUrl $checksum[0].browser_download_url $Repository
        $body = (Invoke-WebRequest -UseBasicParsing -Uri $checksum[0].browser_download_url -TimeoutSec 30).Content
        foreach ($line in ($body -split "`n")) {
            if ($line -match '^([0-9a-fA-F]{64})\s+\*?(.+?)\s*$' -and $Matches[2] -ceq $asset.name) { $expected=$Matches[1]; break }
        }
    }
    if (-not $expected -or (Get-Sha256 $zip) -ne $expected.ToLowerInvariant()) { throw 'ZIP SHA-256 verification failed.' }
    $stage = "$work-stage"
    [IO.Directory]::CreateDirectory($stage) | Out-Null
    Expand-Portable $zip $stage
    # Keep transaction metadata outside both directories that will be renamed.
    $context = @{ install=$install; stage=$stage; backup="$work-backup"; failed="$work-failed"; pid=$OwnerPid; ack=(Join-Path $work 'healthy'); armed=(Join-Path $work 'armed') }
    $contextFile = Join-Path $work 'transaction.json'
    Write-JsonFile $contextFile $context
    @{ contextPath=$contextFile; armed=$context.armed } | ConvertTo-Json -Compress
} catch { [Console]::Error.WriteLine($_.Exception.Message); exit 1 }
