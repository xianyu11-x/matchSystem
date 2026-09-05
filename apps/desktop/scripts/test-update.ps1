$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '../src-tauri/src/update.ps1') -Mode Library
$root = Join-Path ([IO.Path]::GetTempPath()) ('matchscope-update-test-' + [guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($root) | Out-Null
function Assert($Condition, $Message) { if (-not $Condition) { throw $Message } }
function Reject($Action, $Message) { $rejected=$false; try { & $Action } catch { $rejected=$true }; Assert $rejected $Message }
function Make-Zip($Path, $Names) {
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::Open($Path, [IO.Compression.ZipArchiveMode]::Create)
    try { foreach ($name in $Names) { $entry=$zip.CreateEntry($name); $stream=$entry.Open(); try { $stream.WriteByte(77); $stream.WriteByte(90) } finally { $stream.Dispose() } } } finally { $zip.Dispose() }
}
try {
    Assert ((Get-StableVersion 'v0.1.10') -gt (Get-StableVersion '0.1.9')) 'Version comparison is lexical.'
    Reject { Get-StableVersion 'v1.0.0-beta.1' } 'Prerelease accepted.'
    Reject { Assert-DownloadUrl 'https://example.com/a.zip' 'owner/repo' } 'Foreign host accepted.'
    Reject { Assert-DownloadUrl 'https://github.com/other/repo/releases/download/v1/a.zip' 'owner/repo' } 'Foreign repository accepted.'
    $cases = @(
        @{ names=@('MatchScope.exe','simulator-api.exe','assets/theme.txt'); valid=$true },
        @{ names=@('portable/MatchScope.exe','portable/simulator-api.exe','setup.exe'); valid=$true },
        @{ names=@('MatchScope.exe','../escape.exe'); valid=$false },
        @{ names=@('MatchScope.exe','simulator-api.exe','C:/escape.exe'); valid=$false },
        @{ names=@('MatchScope.exe','simulator-api.exe','MATCHSCOPE.EXE'); valid=$false },
        @{ names=@('MatchScope.exe'); valid=$false }
    )
    $i=0
    foreach ($case in $cases) {
        $zip=Join-Path $root "$i.zip"; $dest=Join-Path $root "extract-$i"; $i++
        [IO.Directory]::CreateDirectory($dest) | Out-Null; Make-Zip $zip $case.names
        if ($case.valid) { Expand-Portable $zip $dest; Assert (Test-Path -LiteralPath (Join-Path $dest 'simulator-api.exe')) 'Sidecar not extracted.' }
        else { Reject { Expand-Portable $zip $dest } 'Unsafe/incomplete ZIP accepted.' }
    }
    Reject { Assert-Sibling (Join-Path $root 'nested/path') $root } 'Non-sibling accepted.'
    # Real Windows executable fixtures exercise relaunch acknowledgement and rollback.
    $healthy = Join-Path $root 'healthy.exe'
    Add-Type -TypeDefinition 'using System; using System.IO; public class Healthy { public static void Main() { var p=Environment.GetEnvironmentVariable("MATCHSCOPE_UPDATE_ACK"); if(p!=null) File.WriteAllText(p,"healthy"); } }' -OutputAssembly $healthy -OutputType WindowsApplication
    $broken = Join-Path $root 'broken.exe'
    Add-Type -TypeDefinition 'public class Broken { public static void Main() { System.Environment.Exit(1); } }' -OutputAssembly $broken -OutputType WindowsApplication
    foreach ($success in @($true,$false)) {
        $base=Join-Path $root ("transaction-$success"); [IO.Directory]::CreateDirectory($base) | Out-Null
        $c=@{ install=(Join-Path $base 'install'); stage=(Join-Path $base 'stage'); backup=(Join-Path $base 'backup'); failed=(Join-Path $base 'failed'); ack=(Join-Path $base 'ack'); armed=(Join-Path $base 'armed'); pid=2147483647 }
        [IO.Directory]::CreateDirectory($c.install) | Out-Null; [IO.Directory]::CreateDirectory($c.stage) | Out-Null
        Copy-Item -LiteralPath $healthy -Destination (Join-Path $c.install 'MatchScope.exe')
        [IO.File]::WriteAllText((Join-Path $c.install 'simulator-api.exe'), 'old-sidecar')
        [IO.File]::WriteAllText((Join-Path $c.install 'old-extra.txt'), 'user backup')
        Copy-Item -LiteralPath $(if ($success) { $healthy } else { $broken }) -Destination (Join-Path $c.stage 'MatchScope.exe')
        [IO.File]::WriteAllText((Join-Path $c.stage 'simulator-api.exe'), 'new-sidecar')
        $contextFile = Join-Path $base 'transaction.json'
        Write-JsonFile $contextFile $c
        $helperScript = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../src-tauri/src/update.ps1'))
        $helper = Start-Process powershell.exe -ArgumentList @('-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', ('"' + $helperScript + '"'), '-Mode', 'Apply', '-ContextPath', ('"' + $contextFile + '"')) -WorkingDirectory $base -WindowStyle Hidden -PassThru -Wait
        Assert ($helper.ExitCode -eq 0) 'External update helper failed.'
        $status = Get-Content -Raw -LiteralPath (Join-Path $c.install '.matchscope-update-status.json') | ConvertFrom-Json
        Assert ($status.ok -eq $success) 'Unexpected transaction result.'
        $sidecar = [IO.File]::ReadAllText((Join-Path $c.install 'simulator-api.exe'))
        Assert ($sidecar -eq $(if ($success) { 'new-sidecar' } else { 'old-sidecar' })) 'Desktop and sidecar were not changed atomically.'
        Assert (Test-Path -LiteralPath (Join-Path $(if ($success) {$c.backup} else {$c.install}) 'old-extra.txt')) 'User files were lost.'
    }
    Write-Host 'PASS: stable versions, repository URLs, both ZIP layouts, traversal/duplicates/missing sidecar, full update and startup rollback.'
} finally {
    Remove-Item Env:MATCHSCOPE_UPDATE_ACK -ErrorAction SilentlyContinue
    $full=[IO.Path]::GetFullPath($root)
    if ($full.StartsWith([IO.Path]::GetTempPath(), [StringComparison]::OrdinalIgnoreCase) -and [IO.Path]::GetFileName($full).StartsWith('matchscope-update-test-')) {
        for ($attempt=0; ; $attempt++) {
            try { Remove-Item -LiteralPath $full -Recurse -Force; break }
            catch { if ($attempt -ge 20) { throw }; Start-Sleep -Milliseconds 100 }
        }
    }
}
