$ErrorActionPreference = 'Stop'

$repoRoot = [IO.Path]::GetFullPath((Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).ProviderPath).TrimEnd([char]'\')
$consumerRoot = [IO.Path]::GetFullPath((Resolve-Path -LiteralPath (Join-Path $repoRoot 'examples\go-consumer')).ProviderPath).TrimEnd([char]'\')
$tempRoot = [IO.Path]::GetFullPath((Resolve-Path -LiteralPath ([IO.Path]::GetTempPath())).ProviderPath).TrimEnd([char]'\')
$tempDir = [IO.Path]::GetFullPath((Join-Path $tempRoot ('matchsystem-consumer-' + [Guid]::NewGuid().ToString('N'))))
if (-not $consumerRoot.StartsWith($repoRoot + '\', [StringComparison]::OrdinalIgnoreCase)) {
    throw '消费者目录不在仓库内'
}
if (-not $tempDir.StartsWith($tempRoot + '\', [StringComparison]::OrdinalIgnoreCase) -or
    $tempDir.StartsWith($repoRoot + '\', [StringComparison]::OrdinalIgnoreCase) -or
    $repoRoot.StartsWith($tempDir + '\', [StringComparison]::OrdinalIgnoreCase) -or
    (Test-Path -LiteralPath $tempDir)) {
    throw '临时目录无效或已存在'
}

$hadGoWork = Test-Path Env:GOWORK
$previousGoWork = $env:GOWORK
$created = $false
$pushed = $false
try {
    New-Item -ItemType Directory -Path $tempDir | Out-Null
    $created = $true
    if ((Resolve-Path -LiteralPath $tempDir).ProviderPath -ine $tempDir) {
        throw '临时目录的解析路径发生变化'
    }
    foreach ($name in @('go.mod', 'go.sum', 'main.go', 'main_test.go', 'README.md')) {
        $source = (Resolve-Path -LiteralPath (Join-Path $consumerRoot $name)).ProviderPath
        $destination = [IO.Path]::GetFullPath((Join-Path $tempDir $name))
        if (-not $source.StartsWith($consumerRoot + '\', [StringComparison]::OrdinalIgnoreCase) -or
            -not $destination.StartsWith($tempDir + '\', [StringComparison]::OrdinalIgnoreCase) -or
            ((Get-Item -LiteralPath $source -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw "消费者文件路径无效: $name"
        }
        Copy-Item -LiteralPath $source -Destination $destination
    }

    $env:GOWORK = 'off'
    Push-Location -LiteralPath $tempDir
    $pushed = $true
    $replaceRoot = $repoRoot.Replace('\', '/')
    & go mod edit "-replace=github.com/xianyu11-x/matchSystem=$replaceRoot"
    if ($LASTEXITCODE -ne 0) { throw "go mod edit 失败: $LASTEXITCODE" }
    $goWork = (& go env GOWORK)
    if ($LASTEXITCODE -ne 0 -or $goWork.Trim() -cne 'off') { throw 'Go workspace 未关闭' }
    $goMod = (& go env GOMOD)
    if ($LASTEXITCODE -ne 0 -or [IO.Path]::GetFullPath($goMod.Trim()) -ine (Join-Path $tempDir 'go.mod')) {
        throw "Go 未使用临时消费者模块: $goMod"
    }
    & go mod tidy
    if ($LASTEXITCODE -ne 0) { throw "go mod tidy 失败: $LASTEXITCODE" }
    $dependencies = @(& go list -deps ./...)
    if ($LASTEXITCODE -ne 0) { throw "go list -deps 失败: $LASTEXITCODE" }
    $forbidden = @($dependencies | Where-Object {
        $_ -match '^github\.com/xianyu11-x/matchSystem/internal/(simulator|simulatorapi)(/|$)'
    })
    if ($forbidden.Count -ne 0) { throw "消费者依赖了模拟器应用层: $($forbidden -join ', ')" }
    & go test ./... -count=1
    if ($LASTEXITCODE -ne 0) { throw "go test 失败: $LASTEXITCODE" }
    & go run .
    if ($LASTEXITCODE -ne 0) { throw "go run 失败: $LASTEXITCODE" }
    Write-Output "独立消费者验证通过: $goMod"
} finally {
    if ($pushed) { Pop-Location }
    if ($hadGoWork) { $env:GOWORK = $previousGoWork } else { Remove-Item Env:GOWORK -ErrorAction SilentlyContinue }
    if ($created -and (Test-Path -LiteralPath $tempDir)) {
        $resolved = (Resolve-Path -LiteralPath $tempDir).ProviderPath
        $entry = Get-Item -LiteralPath $tempDir -Force
        $links = @(Get-ChildItem -LiteralPath $tempDir -Recurse -Force | Where-Object {
            $_.Attributes -band [IO.FileAttributes]::ReparsePoint
        })
        if ($resolved -ine $tempDir -or -not $resolved.StartsWith($tempRoot + '\', [StringComparison]::OrdinalIgnoreCase) -or
            ($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) -or $links.Count -ne 0) {
            throw "拒绝清理不安全的临时目录: $tempDir"
        }
        Remove-Item -LiteralPath $tempDir -Recurse -Force
    }
}
