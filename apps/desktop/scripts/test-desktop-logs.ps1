[CmdletBinding()]
param([Parameter(Mandatory = $true)][string]$PortableZip)

$ErrorActionPreference = 'Stop'
$PortableZip = (Resolve-Path -LiteralPath $PortableZip).Path
$root = Join-Path ([IO.Path]::GetTempPath()) ('matchscope-desktop-logs-test-' + [guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($root) | Out-Null
$testFailed = $false

Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
public static class MatchScopeLogTestWindows {
    private delegate bool EnumProc(IntPtr window, IntPtr parameter);
    [DllImport("user32.dll")] private static extern bool EnumWindows(EnumProc callback, IntPtr parameter);
    [DllImport("user32.dll")] private static extern bool EnumChildWindows(IntPtr parent, EnumProc callback, IntPtr parameter);
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr window, out uint processId);
    [DllImport("user32.dll")] private static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] private static extern bool PostMessage(IntPtr window, uint message, IntPtr wParam, IntPtr lParam);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] private static extern int GetWindowText(IntPtr window, StringBuilder text, int capacity);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] private static extern int GetClassName(IntPtr window, StringBuilder text, int capacity);
    private static string Text(IntPtr window) {
        var text = new StringBuilder(32768);
        GetWindowText(window, text, text.Capacity);
        return text.ToString();
    }
    public static IntPtr FindVisible(uint processId, string title) {
        IntPtr found = IntPtr.Zero;
        EnumWindows(delegate(IntPtr window, IntPtr parameter) {
            uint owner;
            GetWindowThreadProcessId(window, out owner);
            if (owner == processId && IsWindowVisible(window) && Text(window) == title) found = window;
            return true;
        }, IntPtr.Zero);
        return found;
    }
    public static string Describe(uint processId) {
        var result = new List<string>();
        EnumWindows(delegate(IntPtr window, IntPtr parameter) {
            uint owner;
            GetWindowThreadProcessId(window, out owner);
            if (owner == processId) {
                var name = new StringBuilder(256);
                GetClassName(window, name, name.Capacity);
                result.Add(string.Format("handle={0} visible={1} class={2} title={3}", window, IsWindowVisible(window), name, Text(window)));
            }
            return true;
        }, IntPtr.Zero);
        return result.Count == 0 ? "no top-level windows" : string.Join("; ", result.ToArray());
    }
    public static string ChildText(IntPtr parent) {
        var result = new List<string>();
        EnumChildWindows(parent, delegate(IntPtr child, IntPtr parameter) {
            string text = Text(child);
            if (text.Length > 0) result.Add(text);
            return true;
        }, IntPtr.Zero);
        return string.Join("\n", result.ToArray());
    }
    public static bool Close(IntPtr window) { return PostMessage(window, 0x0010, IntPtr.Zero, IntPtr.Zero); }
}
'@

function Assert($Condition, $Message) { if (-not $Condition) { throw $Message } }

function Wait-Window($Process, $Title, $Seconds = 30) {
    $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
    do {
        $Process.Refresh()
        if ($Process.HasExited) { throw "Client exited before window '$Title' appeared (code $($Process.ExitCode))." }
        $window = [MatchScopeLogTestWindows]::FindVisible($Process.Id, $Title)
        if ($window -ne [IntPtr]::Zero) { return $window }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Timed out waiting for window '$Title'. Windows: $([MatchScopeLogTestWindows]::Describe($Process.Id))"
}

function Start-TestApp($Directory, $WorkingDirectory) {
    $process = Start-Process -FilePath (Join-Path $Directory 'MatchScope.exe') -WorkingDirectory $WorkingDirectory -WindowStyle Hidden -PassThru
    return $process
}

function Stop-TestApp($Process, $Window) {
    Assert ([MatchScopeLogTestWindows]::Close($Window)) 'Could not close the test window.'
    Assert ($Process.WaitForExit(10000)) 'Test application did not exit after its window closed.'
}

function Post-Json($Uri, $Value) {
    $json = ConvertTo-Json -InputObject $Value -Depth 20 -Compress
    $bytes = [Text.Encoding]::UTF8.GetBytes($json)
    return Invoke-RestMethod -Uri $Uri -Method Post -Body $bytes -ContentType 'application/json; charset=utf-8' -UseBasicParsing
}

function Read-SharedUtf8($Path) {
    $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite)
    try {
        $reader = [IO.StreamReader]::new($stream, [Text.Encoding]::UTF8, $true)
        try { return $reader.ReadToEnd() }
        finally { $reader.Dispose() }
    } finally { $stream.Dispose() }
}

try {
    $archive = Join-Path $root 'archive'
    Expand-Archive -LiteralPath $PortableZip -DestinationPath $archive
    $source = if (Test-Path -LiteralPath (Join-Path $archive 'portable/MatchScope.exe')) { Join-Path $archive 'portable' } else { $archive }
    foreach ($name in @('MatchScope.exe', 'simulator-api.exe', 'Updater.exe')) {
        Assert (Test-Path -LiteralPath (Join-Path $source $name) -PathType Leaf) "Built ZIP is missing $name"
    }
    $install = Join-Path $root '安装目录 with spaces'
    $working = Join-Path $root '独立工作目录'
    $broken = Join-Path $root '日志失败目录'
    foreach ($dir in @($install, $working, $broken)) { [IO.Directory]::CreateDirectory($dir) | Out-Null }
    foreach ($name in @('MatchScope.exe', 'simulator-api.exe', 'Updater.exe')) {
        Copy-Item -LiteralPath (Join-Path $source $name) -Destination (Join-Path $install $name)
        Copy-Item -LiteralPath (Join-Path $source $name) -Destination (Join-Path $broken $name)
    }

    # 从不同工作目录启动两次，验证真实程序目录中的独立日志文件。
    $logs = Join-Path $install 'Logs'
    for ($run = 1; $run -le 2; $run++) {
        $process = Start-TestApp $install $working
        $handle = Wait-Window $process 'MatchScope'
        $files = @(Get-ChildItem -LiteralPath $logs -Filter 'matchscope-*.log' -File -ErrorAction SilentlyContinue)
        Assert ($files.Count -eq $run) "Expected $run independent log file(s) in $logs; found $($files.Count)."
        Assert (-not (Test-Path -LiteralPath (Join-Path $working 'Logs'))) 'Logs were created under the working directory.'
        $children = @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$($process.Id)" | Where-Object { $_.Name -eq 'simulator-api.exe' })
        Assert ($children.Count -eq 1) 'Expected exactly one simulator-api child.'
        Assert ($children[0].CommandLine.Contains('--log-dir')) 'Sidecar command line is missing --log-dir.'
        Assert ($children[0].CommandLine.Contains($logs)) 'Sidecar command line is missing the executable directory Logs path.'
        if ($run -eq 1) {
            # 从该子进程的监听端口执行轮次，并从实际磁盘日志提取实例时间线。
            $listeners = @(Get-NetTCPConnection -State Listen -OwningProcess $children[0].ProcessId | Where-Object { $_.LocalAddress -eq '127.0.0.1' })
            Assert ($listeners.Count -eq 1) 'Expected exactly one loopback listener for the test sidecar.'
            $api = 'http://127.0.0.1:' + $listeners[0].LocalPort + '/api/v1'
            $null = Post-Json "$api/rounds" @{ now = 1000; maxMatches = 1 }
            $recording = Read-SharedUtf8 $files[0].FullName
            $replayLines = @([regex]::Matches($recording, '(?m)^\[match_replay\] ([^\r\n]+)\r?$'))
            Assert ($replayLines.Count -ge 1) 'Disk recording did not contain a line beginning with the replay marker.'
            $firstRecord = $replayLines[0].Groups[1].Value | ConvertFrom-Json
            Assert ($null -ne $firstRecord.timeline) 'The first replay line did not contain a timeline JSON object.'
            $listing = Post-Json "$api/replay/extract" @{ logs = $recording }
            $instances = @($listing.instances | Where-Object { $_.replayable })
            Assert ($instances.Count -ge 1) 'Disk recording did not contain a replayable instance.'
            $extracted = Post-Json "$api/replay/extract" @{ logs = $recording; instanceId = $instances[0].instanceId }
            $timeline = $extracted.timelineJson | ConvertFrom-Json
            Assert (@($timeline.events | Where-Object { $_.type -eq 'round_begin' }).Count -ge 1) 'Extracted disk timeline did not contain round_begin.'
        }
        Stop-TestApp $process $handle
    }

    # 用同名普通文件阻止创建 Logs，验证无控制台启动时也能看到后端诊断。
    [IO.File]::WriteAllText((Join-Path $broken 'Logs'), 'not a directory')
    $process = Start-TestApp $broken $working
    $handle = Wait-Window $process 'MatchScope 启动失败'
    $diagnostic = [MatchScopeLogTestWindows]::ChildText($handle)
    Assert ($diagnostic.Contains((Join-Path $broken 'Logs'))) 'Startup dialog did not show the log directory.'
    Assert ($diagnostic.Contains('后端诊断')) 'Startup dialog did not show the backend stderr diagnostic.'
    Stop-TestApp $process $handle
    Assert ($process.ExitCode -ne 0) 'A failed startup must return a nonzero exit code.'

    Write-Host 'PASS: independent logs, disk timeline extraction, Unicode/spaced paths, different working directory and visible startup diagnostics.'
} catch {
    $testFailed = $true
    throw
} finally {
    try {
        $full = [IO.Path]::GetFullPath($root)
        if ($full.StartsWith([IO.Path]::GetTempPath(), [StringComparison]::OrdinalIgnoreCase) -and [IO.Path]::GetFileName($full).StartsWith('matchscope-desktop-logs-test-')) {
            # 重新核对进程路径，容忍进程在枚举后自行退出。
            $ownedProcesses = @(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith($full + '\', [StringComparison]::OrdinalIgnoreCase) })
            foreach ($entry in $ownedProcesses) {
                try {
                    $running = Get-Process -Id $entry.ProcessId -ErrorAction SilentlyContinue
                    if ($running -and $running.Path -and $running.Path.StartsWith($full + '\', [StringComparison]::OrdinalIgnoreCase)) {
                        $running.Kill()
                    }
                } catch {
                    $remaining = Get-CimInstance Win32_Process -Filter "ProcessId=$($entry.ProcessId)" -ErrorAction SilentlyContinue
                    if ($remaining -and $remaining.ExecutablePath -and $remaining.ExecutablePath.StartsWith($full + '\', [StringComparison]::OrdinalIgnoreCase)) { throw }
                }
            }
            for ($attempt = 0; ; $attempt++) {
                try { Remove-Item -LiteralPath $full -Recurse -Force; break }
                catch { if ($attempt -ge 20) { throw }; Start-Sleep -Milliseconds 100 }
            }
        }
    } catch {
        if ($testFailed) { Write-Warning "测试目录清理失败：$_" }
        else { throw }
    }
}
