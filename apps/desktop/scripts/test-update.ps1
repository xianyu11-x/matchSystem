[CmdletBinding()]
param([string]$UpdaterPath, [string]$PortableZip, [switch]$SkipBuild)
$ErrorActionPreference = 'Stop'
$updaterRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../updater'))
if (-not $UpdaterPath) {
    if (-not $SkipBuild) {
        & cargo build --manifest-path (Join-Path $updaterRoot 'Cargo.toml') --bin Updater --locked
        if ($LASTEXITCODE -ne 0) { throw 'Native updater build failed.' }
    }
    $UpdaterPath = Join-Path $updaterRoot 'target/debug/Updater.exe'
}
$UpdaterPath = (Resolve-Path -LiteralPath $UpdaterPath).Path
$root = Join-Path ([IO.Path]::GetTempPath()) ('matchscope-update-test-' + [guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($root) | Out-Null
function Assert($Condition, $Message) { if (-not $Condition) { throw $Message } }
function Write-Json($Path, $Value) { [IO.File]::WriteAllText($Path, ($Value | ConvertTo-Json -Depth 8), [Text.UTF8Encoding]::new($false)) }
function File-Hash($Path) {
    $hash = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::OpenRead($Path)
    try { return [BitConverter]::ToString($hash.ComputeHash($stream)) }
    finally { $stream.Dispose(); $hash.Dispose() }
}
function Wait-File($Path, $Process) {
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    while (-not (Test-Path -LiteralPath $Path)) {
        if ($Process.HasExited -or [DateTime]::UtcNow -ge $deadline) { throw "Updater did not create $Path" }
        Start-Sleep -Milliseconds 50
    }
}
try {
    if ($PortableZip) {
        $portableSource = Join-Path $root 'portable-source'
        Expand-Archive -LiteralPath $PortableZip -DestinationPath $portableSource
        if (Test-Path -LiteralPath (Join-Path $portableSource 'portable/MatchScope.exe')) { $portableSource = Join-Path $portableSource 'portable' }
        foreach ($required in @('MatchScope.exe', 'simulator-api.exe', 'Updater.exe')) {
            Assert (Test-Path -LiteralPath (Join-Path $portableSource $required)) "Built ZIP is missing $required"
        }
    }
    # These .NET fixtures are only test applications. Production updates never invoke PowerShell.
    $healthy = Join-Path $root 'healthy.exe'
    Add-Type -TypeDefinition 'using System; using System.IO; public class Healthy { public static void Main() { var p=Environment.GetEnvironmentVariable("MATCHSCOPE_UPDATE_ACK"); if(p!=null) File.WriteAllText(p,"healthy"); System.Threading.Thread.Sleep(500); } }' -OutputAssembly $healthy -OutputType WindowsApplication
    $broken = Join-Path $root 'broken.exe'
    Add-Type -TypeDefinition 'using System; using System.IO; public class Broken { public static void Main() { var p=Environment.GetEnvironmentVariable("MATCHSCOPE_UPDATE_ACK"); var marker=Path.Combine(Path.GetDirectoryName(p),"child-pid"); System.Diagnostics.Process.Start(Path.Combine(AppDomain.CurrentDomain.BaseDirectory,"child.exe")); for(var i=0;i<100 && !File.Exists(marker);i++) System.Threading.Thread.Sleep(20); Environment.Exit(1); } }' -OutputAssembly $broken -OutputType WindowsApplication
    $worker = Join-Path $root 'worker.exe'
    Add-Type -TypeDefinition 'using System; using System.IO; public class Worker { public static void Main() { var p=Environment.GetEnvironmentVariable("MATCHSCOPE_UPDATE_ACK"); File.WriteAllText(Path.Combine(Path.GetDirectoryName(p),"child-pid"),System.Diagnostics.Process.GetCurrentProcess().Id.ToString()); System.Threading.Thread.Sleep(30000); } }' -OutputAssembly $worker -OutputType WindowsApplication
    $sleeper = Join-Path $root 'owner.exe'
    Add-Type -TypeDefinition 'public class Owner { public static void Main() { System.Threading.Thread.Sleep(3000); } }' -OutputAssembly $sleeper -OutputType WindowsApplication
    foreach ($success in @($true, $false)) {
        $base = Join-Path $root ("transaction-$success")
        [IO.Directory]::CreateDirectory($base) | Out-Null
        $work = Join-Path $base ('.matchscope-update-' + [guid]::NewGuid().ToString('N'))
        [IO.Directory]::CreateDirectory($work) | Out-Null
        $c = @{ protocol_version=1; install=(Join-Path $base '安装目录 with spaces'); stage="$work-stage"; backup="$work-backup"; failed="$work-failed"; ack=(Join-Path $work 'healthy'); armed=(Join-Path $work 'armed'); pid=0 }
        [IO.Directory]::CreateDirectory($c.install) | Out-Null
        [IO.Directory]::CreateDirectory($c.stage) | Out-Null
        Copy-Item -LiteralPath $healthy -Destination (Join-Path $c.install 'MatchScope.exe')
        Copy-Item -LiteralPath $UpdaterPath -Destination (Join-Path $c.install 'Updater.exe')
        [IO.File]::WriteAllText((Join-Path $c.install 'simulator-api.exe'), 'old-sidecar')
        [IO.File]::WriteAllText((Join-Path $c.install 'old-extra.txt'), 'user backup')
        Copy-Item -LiteralPath $(if ($success) { $healthy } else { $broken }) -Destination (Join-Path $c.stage 'MatchScope.exe')
        Copy-Item -LiteralPath $UpdaterPath -Destination (Join-Path $c.stage 'Updater.exe')
        Copy-Item -LiteralPath $worker -Destination (Join-Path $c.stage 'child.exe')
        [IO.File]::WriteAllText((Join-Path $c.stage 'simulator-api.exe'), 'new-sidecar')
        if ($success -and $PortableZip) {
            Get-ChildItem -LiteralPath $portableSource | Copy-Item -Destination $c.stage -Recurse -Force
        }
        $expectedSidecarHash = File-Hash (Join-Path $(if ($success) { $c.stage } else { $c.install }) 'simulator-api.exe')
        # The running helper is copied from the old installation, outside the directory being moved.
        $helperPath = Join-Path $work 'Updater.exe'
        Copy-Item -LiteralPath (Join-Path $c.install 'Updater.exe') -Destination $helperPath
        $owner = Start-Process -FilePath $sleeper -WindowStyle Hidden -PassThru
        $c.pid = $owner.Id
        $contextFile = Join-Path $work 'transaction.json'
        Write-Json $contextFile $c
        $helper = Start-Process -FilePath $helperPath -ArgumentList @('--context', ('"' + $contextFile + '"')) -WorkingDirectory $work -WindowStyle Hidden -PassThru
        Wait-File $c.armed $helper
        Assert (-not $owner.HasExited) 'Owner fixture exited before ready handshake.'
        Assert ((Get-Content -Raw -LiteralPath (Join-Path $c.install 'simulator-api.exe')) -eq 'old-sidecar') 'Updater replaced files before the owner exited.'
        if (-not $helper.WaitForExit(60000)) { throw 'Native helper timed out.' }
        $status = Get-Content -Raw -LiteralPath (Join-Path $c.install '.matchscope-update-status.json') | ConvertFrom-Json
        Assert ($status.ok -eq $success) 'Unexpected native transaction result.'
        Assert ((File-Hash (Join-Path $c.install 'simulator-api.exe')) -eq $expectedSidecarHash) 'Desktop and sidecar versions differ after transaction.'
        Assert (Test-Path -LiteralPath (Join-Path $(if ($success) { $c.backup } else { $c.install }) 'old-extra.txt')) 'User files were lost.'
        Assert (Test-Path -LiteralPath (Join-Path $c.install 'Updater.exe')) 'Updater was not included in the resulting installation.'
        if (-not $success) {
            Assert (Test-Path -LiteralPath $c.failed) 'Failed new version was not retained.'
            $descendantPid = [int](Get-Content -Raw -LiteralPath (Join-Path $work 'child-pid'))
            Assert ($null -eq (Get-Process -Id $descendantPid -ErrorAction SilentlyContinue)) 'New version descendant survived rollback.'
        }
        if ($success -and $PortableZip) {
            # Only close the newly created test installation, never an existing user instance.
            $appPath = Join-Path $c.install 'MatchScope.exe'
            $testApps = @(Get-CimInstance Win32_Process -Filter "Name='MatchScope.exe'" | Where-Object ExecutablePath -EQ $appPath)
            Assert ($testApps.Count -eq 1) 'Built Tauri client was not running after acknowledgement.'
            $testApp = Get-Process -Id $testApps[0].ProcessId
            $windowDeadline = [DateTime]::UtcNow.AddSeconds(10)
            while ($testApp.MainWindowHandle -eq 0 -and [DateTime]::UtcNow -lt $windowDeadline) { Start-Sleep -Milliseconds 100; $testApp.Refresh() }
            Assert ($testApp.MainWindowHandle -ne 0) 'Built Tauri client did not expose its main window.'
            Assert ($testApp.CloseMainWindow()) 'Could not close the test client normally.'
            Assert ($testApp.WaitForExit(10000)) 'Test client did not exit after normal close.'
            Write-Host 'PASS: built Tauri client and real Go sidecar acknowledged startup; main window appeared and closed normally.'
        }
    }
    Write-Host 'PASS: native helper, Unicode/spaced paths, ready handshake, owner exit, complete directory update, native helper replacement, startup rollback and descendant termination.'
} finally {
    $full = [IO.Path]::GetFullPath($root)
    if ($full.StartsWith([IO.Path]::GetTempPath(), [StringComparison]::OrdinalIgnoreCase) -and [IO.Path]::GetFileName($full).StartsWith('matchscope-update-test-')) {
        # Test failures must not leave an application running from a disposable fixture directory.
        Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($full + '\', [StringComparison]::OrdinalIgnoreCase) } | ForEach-Object {
            & taskkill.exe /PID $_.ProcessId /T /F 2>&1 | Out-Null
        }
        for ($attempt=0; ; $attempt++) {
            try { Remove-Item -LiteralPath $full -Recurse -Force; break }
            catch { if ($attempt -ge 20) { throw }; Start-Sleep -Milliseconds 100 }
        }
    }
}
