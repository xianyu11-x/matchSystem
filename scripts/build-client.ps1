[CmdletBinding()]
param(
    [string]$TargetTriple = "x86_64-pc-windows-msvc",
    [string]$OutputDirectory = "dist/release",
    [switch]$SkipDependencyInstall
)

# This is the single Windows release entry point.  It intentionally keeps all
# generated files below the repository's dist directory and only removes the
# exact release directory selected by -OutputDirectory.
$ErrorActionPreference = "Stop"
$script:BuildStage = "初始化"
$stagingDirectory = $null
$originalProcessPath = $env:PATH
$cargoPathWasAdded = $false

function Enter-BuildStage {
    param([Parameter(Mandatory = $true)][string]$Name)

    $script:BuildStage = $Name
    Write-Host ""
    Write-Host "=== $Name ===" -ForegroundColor Cyan
}

function Resolve-Executable {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Label
    )

    $command = Get-Command -Name $Name -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -eq $command) {
        throw "$Label 未找到（需要 '$Name'）。请先安装并将其加入 PATH。"
    }

    $path = [string]$command.Path
    if ([string]::IsNullOrWhiteSpace($path)) {
        $path = [string]$command.Source
    }
    if ([string]::IsNullOrWhiteSpace($path)) {
        throw "$Label 的可执行路径无法解析。"
    }
    return $path
}

function Resolve-RustExecutable {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$Label
    )

    try {
        return Resolve-Executable -Name $Name -Label $Label
    }
    catch {
        $firstError = $_.Exception.Message
        $profileRoot = [string]$env:USERPROFILE
        if ([string]::IsNullOrWhiteSpace($profileRoot)) {
            $profileRoot = [Environment]::GetFolderPath([Environment+SpecialFolder]::UserProfile)
        }
        $cargoBin = if ([string]::IsNullOrWhiteSpace($profileRoot)) {
            $null
        }
        else {
            Join-Path $profileRoot ".cargo\bin"
        }

        if ($null -eq $cargoBin -or -not (Test-Path -LiteralPath $cargoBin -PathType Container)) {
            throw "$firstError；未找到 Cargo 默认目录 $cargoBin，无法自动重试。请确认 Rust/Cargo 已安装并加入 PATH。"
        }

        $pathSeparator = [IO.Path]::PathSeparator
        $pathEntries = if ([string]::IsNullOrWhiteSpace($env:PATH)) {
            @()
        }
        else {
            @($env:PATH -split [regex]::Escape([string]$pathSeparator))
        }
        $alreadyPresent = $pathEntries | Where-Object {
            $_.TrimEnd([char]'\', [char]'/') -ieq $cargoBin.TrimEnd([char]'\', [char]'/')
        }
        if (-not $alreadyPresent) {
            $env:PATH = if ([string]::IsNullOrWhiteSpace($env:PATH)) {
                $cargoBin
            }
            else {
                "$cargoBin$pathSeparator$env:PATH"
            }
            $script:CargoPathWasAdded = $true
            Write-Warning "未在 PATH 中找到 $Label；已仅在当前 PowerShell 进程临时追加 $cargoBin 并重试。"
        }

        try {
            return Resolve-Executable -Name $Name -Label $Label
        }
        catch {
            throw "$firstError；已重试当前进程 PATH（$cargoBin），仍未找到 $Label。请确认 Rust/Cargo 安装完整。"
        }
    }
}

function Invoke-CommandStage {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$CommandPath,
        [string[]]$ArgumentList = @()
    )

    Enter-BuildStage $Name
    $argumentText = if ($ArgumentList.Count -gt 0) { $ArgumentList -join " " } else { "" }
    Write-Host ("> {0} {1}" -f $CommandPath, $argumentText)
    & $CommandPath @ArgumentList
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        throw "阶段 '$Name' 失败，退出码 $exitCode。"
    }
}

function Invoke-ScriptStage {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$ScriptPath,
        [string[]]$ArgumentList = @()
    )

    Enter-BuildStage $Name
    $argumentText = if ($ArgumentList.Count -gt 0) { $ArgumentList -join " " } else { "" }
    $powershell = Get-Command -Name "powershell.exe" -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -eq $powershell) {
        throw "阶段 '$Name' 失败：未找到 powershell.exe，无法安全调用 sidecar 脚本。"
    }

    # Keep every argument as its own array element.  In particular, a Go path
    # such as 'C:\Program Files\Go\bin\go.exe' must not be flattened into a
    # command string or PowerShell 5.1 will bind the trailing tokens wrongly.
    $scriptArguments = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $ScriptPath) + $ArgumentList
    Write-Host ("> powershell -NoProfile -ExecutionPolicy Bypass -File `"{0}`" {1}" -f $ScriptPath, $argumentText)
    & $powershell.Path @scriptArguments
    $exitCode = $LASTEXITCODE
    if ($null -ne $exitCode -and $exitCode -ne 0) {
        throw "阶段 '$Name' 失败，退出码 $exitCode。"
    }
}

function Get-FileSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)

    $algorithm = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::OpenRead($Path)
    try {
        $bytes = $algorithm.ComputeHash($stream)
    }
    finally {
        $stream.Dispose()
        $algorithm.Dispose()
    }
    return (([BitConverter]::ToString($bytes) -replace "-", "").ToLowerInvariant())
}

function Write-Utf8File {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Content
    )

    [IO.File]::WriteAllText($Path, $Content, [Text.UTF8Encoding]::new($false))
}

function Get-RelativeArchivePath {
    param(
        [Parameter(Mandatory = $true)][string]$RootPath,
        [Parameter(Mandatory = $true)][string]$FilePath
    )

    $root = Get-NormalizedAbsolutePath $RootPath
    $file = Get-NormalizedAbsolutePath $FilePath
    $prefix = $root + [IO.Path]::DirectorySeparatorChar
    if (-not $file.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "文件不在指定目录内，无法计算相对路径：$FilePath"
    }
    return $file.Substring($prefix.Length).Replace([IO.Path]::DirectorySeparatorChar, "/")
}

function Write-ChecksumsFile {
    param(
        [Parameter(Mandatory = $true)][string]$RootPath,
        [Parameter(Mandatory = $true)][string]$DestinationPath
    )

    $destination = Get-NormalizedAbsolutePath $DestinationPath
    $files = @(Get-ChildItem -LiteralPath $RootPath -File -Recurse |
        Where-Object { (Get-NormalizedAbsolutePath $_.FullName) -ne $destination } |
        Sort-Object FullName)
    $lines = foreach ($file in $files) {
        $relativePath = Get-RelativeArchivePath -RootPath $RootPath -FilePath $file.FullName
        "$(Get-FileSha256 $file.FullName)  $relativePath"
    }
    $content = if ($lines.Count -gt 0) { ($lines -join "`r`n") + "`r`n" } else { "" }
    Write-Utf8File -Path $DestinationPath -Content $content
}

function Get-NewestFile {
    param(
        [Parameter(Mandatory = $true)][string]$Directory,
        [Parameter(Mandatory = $true)][scriptblock]$Predicate
    )

    if (-not (Test-Path -LiteralPath $Directory -PathType Container)) {
        return $null
    }
    # A predicate passed as a scriptblock with param($file) does not receive
    # the pipeline object when handed directly to Where-Object in Windows
    # PowerShell 5.1.  Invoke it explicitly with the current file so both
    # Windows PowerShell 5.1 and PowerShell 7 evaluate it consistently.
    $files = @(Get-ChildItem -LiteralPath $Directory -File | Where-Object {
        & $Predicate $_
    })
    if ($files.Count -eq 0) {
        return $null
    }
    return ($files | Sort-Object LastWriteTimeUtc -Descending | Select-Object -First 1)
}

function Add-ManifestArtifact {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][object[]]$List,
        [Parameter(Mandatory = $true)][string]$RootPath,
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string]$Type,
        [Parameter(Mandatory = $true)][bool]$Required
    )

    $item = Get-Item -LiteralPath $FilePath -Force
    $List += [ordered]@{
        name      = Get-RelativeArchivePath -RootPath $RootPath -FilePath $FilePath
        type      = $Type
        required  = $Required
        sizeBytes = [int64]$item.Length
        sha256    = Get-FileSha256 $FilePath
    }
    return $List
}

$safetyScript = Join-Path $PSScriptRoot "build-client-safety.ps1"
if (-not (Test-Path -LiteralPath $safetyScript)) {
    throw "未找到输出路径安全检查脚本：$safetyScript"
}
. $safetyScript

$repositoryRoot = Get-NormalizedAbsolutePath (Join-Path $PSScriptRoot "..")
$webRoot = Get-NormalizedAbsolutePath (Join-Path $repositoryRoot "apps/web")
$desktopRoot = Get-NormalizedAbsolutePath (Join-Path $repositoryRoot "apps/desktop")
$desktopPackagePath = Join-Path $desktopRoot "package.json"
$webPackagePath = Join-Path $webRoot "package.json"
$webLockPath = Join-Path $webRoot "package-lock.json"
$desktopLockPath = Join-Path $desktopRoot "package-lock.json"
$tauriConfigPath = Join-Path $desktopRoot "src-tauri/tauri.conf.json"
$cargoManifestPath = Join-Path $desktopRoot "src-tauri/Cargo.toml"

try {
    if (-not (Test-Path -LiteralPath $desktopPackagePath)) {
        throw "未找到桌面端 package.json，脚本位置或仓库结构不正确：$desktopPackagePath"
    }
    if (-not (Test-Path -LiteralPath $webPackagePath)) {
        throw "未找到 Web package.json，脚本位置或仓库结构不正确：$webPackagePath"
    }
    if (-not (Test-Path -LiteralPath $webLockPath) -or -not (Test-Path -LiteralPath $desktopLockPath)) {
        throw "npm ci 需要 apps/web/package-lock.json 和 apps/desktop/package-lock.json。"
    }

    $desktopPackage = Get-Content -Raw -LiteralPath $desktopPackagePath | ConvertFrom-Json
    $version = [string]$desktopPackage.version
    if ($version -notmatch "^[0-9A-Za-z][0-9A-Za-z.+-]*$") {
        throw "桌面端 package.json 的版本号无效：$version"
    }

    $platformLabels = @{
        "x86_64-pc-windows-msvc" = "windows-x64"
        "aarch64-pc-windows-msvc" = "windows-arm64"
    }
    if (-not $platformLabels.ContainsKey($TargetTriple)) {
        throw "暂不支持的 Windows target triple：$TargetTriple。仅支持 x86_64-pc-windows-msvc 和 aarch64-pc-windows-msvc。"
    }
    $platformLabel = [string]$platformLabels[$TargetTriple]
    $bundleArchitecture = if ($platformLabel -eq "windows-x64") { "x64" } else { "arm64" }

    if (-not (Test-Path -LiteralPath $tauriConfigPath)) {
        throw "未找到 Tauri 配置：$tauriConfigPath"
    }
    if (-not (Test-Path -LiteralPath $cargoManifestPath)) {
        throw "未找到 Cargo manifest：$cargoManifestPath"
    }
    $tauriConfig = Get-Content -Raw -LiteralPath $tauriConfigPath | ConvertFrom-Json
    $tauriVersion = [string]$tauriConfig.version
    $cargoManifest = Get-Content -Raw -LiteralPath $cargoManifestPath
    $cargoVersionMatch = [regex]::Match($cargoManifest, '(?ms)^\[package\].*?^\s*version\s*=\s*"([^"]+)"\s*$')
    if (-not $cargoVersionMatch.Success) {
        throw "无法从 Cargo.toml 的 [package] 区域读取 version。"
    }
    $cargoVersion = $cargoVersionMatch.Groups[1].Value
    Enter-BuildStage "校验客户端版本与目标平台"
    Write-Host "package.json version: $version"
    Write-Host "tauri.conf.json version: $tauriVersion"
    Write-Host "Cargo.toml version: $cargoVersion"
    if ($tauriVersion -ne $version -or $cargoVersion -ne $version) {
        throw "apps/desktop/package.json、src-tauri/tauri.conf.json 和 src-tauri/Cargo.toml 的版本必须一致。"
    }
    Write-Host "TargetTriple: $TargetTriple ($platformLabel)"

    Enter-BuildStage "检查构建工具"
    # npm.cmd is intentional: it bypasses the npm.ps1 execution-policy issue
    # commonly seen when this script is launched from PowerShell on Windows.
    $nodeCommand = Resolve-Executable -Name "node.exe" -Label "Node.js"
    $npmCommand = Resolve-Executable -Name "npm.cmd" -Label "npm"
    $goCommand = Resolve-Executable -Name "go.exe" -Label "Go"
    $rustcCommand = Resolve-RustExecutable -Name "rustc.exe" -Label "rustc"
    $cargoCommand = Resolve-RustExecutable -Name "cargo.exe" -Label "Cargo"

    Write-Host "node:  $nodeCommand"
    & $nodeCommand --version
    if ($LASTEXITCODE -ne 0) { throw "无法读取 Node.js 版本。" }
    Write-Host "npm:   $npmCommand"
    & $npmCommand --version
    if ($LASTEXITCODE -ne 0) { throw "无法读取 npm 版本。" }
    Write-Host "go:    $goCommand"
    & $goCommand version
    if ($LASTEXITCODE -ne 0) { throw "无法读取 Go 版本。" }
    Write-Host "rustc: $rustcCommand"
    & $rustcCommand --version
    if ($LASTEXITCODE -ne 0) { throw "无法读取 rustc 版本。" }
    Write-Host "cargo: $cargoCommand"
    & $cargoCommand --version
    if ($LASTEXITCODE -ne 0) { throw "无法读取 Cargo 版本。" }

    $rustInfo = @(& $rustcCommand -vV 2>&1)
    if ($LASTEXITCODE -ne 0) {
        throw "无法读取 rustc host target。"
    }
    $hostLine = $rustInfo | ForEach-Object { [string]$_ } |
        Where-Object { $_ -match "^host:\s*" } | Select-Object -First 1
    $rustHost = if ($hostLine) { ($hostLine -replace "^host:\s*", "").Trim() } else { "" }
    if ($rustHost) {
        Write-Host "Rust host target: $rustHost"
    }

    if ($SkipDependencyInstall) {
        Enter-BuildStage "跳过依赖安装（按参数）"
        Write-Warning "已跳过 apps/web 和 apps/desktop 的 npm ci；后续仍会执行类型检查、测试、配置校验、sidecar、Tauri 构建和 ZIP 打包。"
        Write-Warning "请确认两个 node_modules 已就绪；正式干净构建不建议使用 -SkipDependencyInstall。"
    }
    else {
        Invoke-CommandStage -Name "安装 Web 依赖（npm ci）" -CommandPath $npmCommand -ArgumentList @(
            "--prefix", $webRoot, "ci", "--no-audit", "--no-fund"
        )

        Invoke-CommandStage -Name "安装桌面端依赖（npm ci）" -CommandPath $npmCommand -ArgumentList @(
            "--prefix", $desktopRoot, "ci", "--no-audit", "--no-fund"
        )
    }

    Invoke-CommandStage -Name "Web 类型检查" -CommandPath $npmCommand -ArgumentList @(
        "--prefix", $webRoot, "run", "typecheck"
    )
    Invoke-CommandStage -Name "Web 测试" -CommandPath $npmCommand -ArgumentList @(
        "--prefix", $webRoot, "run", "test"
    )

    Invoke-CommandStage -Name "校验桌面端配置" -CommandPath $npmCommand -ArgumentList @(
        "--prefix", $desktopRoot, "run", "check:config"
    )

    $sidecarScript = Join-Path $desktopRoot "scripts/build-sidecar.ps1"
    if (-not (Test-Path -LiteralPath $sidecarScript)) {
        throw "未找到现有 sidecar 构建脚本：$sidecarScript"
    }
    Invoke-ScriptStage -Name "构建 Go sidecar" -ScriptPath $sidecarScript -ArgumentList @(
        "-TargetTriple", $TargetTriple, "-GoCommand", $goCommand
    )

    $tauriArguments = @("--prefix", $desktopRoot, "run", "build", "--", "--ci")
    if ($rustHost -and $rustHost -ne $TargetTriple) {
        # Cross-target builds are opt-in.  Cargo/Rust must already have the
        # target installed; this script never installs toolchains or targets.
        $rustup = Get-Command -Name "rustup.exe" -CommandType Application -ErrorAction SilentlyContinue |
            Select-Object -First 1
        if ($null -ne $rustup) {
            $installedTargets = @(& $rustup.Path target list --installed 2>$null)
            if ($LASTEXITCODE -ne 0 -or $installedTargets -notcontains $TargetTriple) {
                throw "Rust 未安装目标 $TargetTriple。请先执行 rustup target add $TargetTriple（脚本不会自动安装 Rust 或 target）。"
            }
        }
        else {
            Write-Warning "未找到 rustup.exe，无法预检 $TargetTriple；Tauri/Cargo 将在构建阶段报告 target 是否可用。"
        }
        $tauriArguments += @("--target", $TargetTriple)
    }
    Write-Host "Tauri beforeBuildCommand 将负责唯一一次 Web 生产构建，避免重复执行。"
    Invoke-CommandStage -Name "Tauri Release 构建（含 NSIS/MSI）" -CommandPath $npmCommand -ArgumentList $tauriArguments

    Enter-BuildStage "查找构建产物"
    # build-sidecar.ps1 writes the authoritative sidecar into Tauri's
    # binaries directory.  Tauri embeds that file during its release build;
    # it is not guaranteed to be copied to target/release as a standalone
    # executable, so portable packaging must use this source directly.
    $sidecarBinaryPath = Join-Path (Join-Path $desktopRoot "src-tauri/binaries") ("simulator-api-" + $TargetTriple + ".exe")
    $sidecarExists = Test-Path -LiteralPath $sidecarBinaryPath -PathType Leaf
    if ($sidecarExists) {
        Write-Host "权威 sidecar：$sidecarBinaryPath"
    }

    $releaseCandidates = if ($rustHost -and $rustHost -eq $TargetTriple) {
        @(
            (Join-Path $desktopRoot "src-tauri/target/release"),
            (Join-Path $desktopRoot "src-tauri/target/$TargetTriple/release")
        )
    }
    else {
        @(
            (Join-Path $desktopRoot "src-tauri/target/$TargetTriple/release"),
            (Join-Path $desktopRoot "src-tauri/target/release")
        )
    }

    $releaseDirectory = $null
    $nsisArtifact = $null
    $msiArtifact = $null
    $releaseDiagnostics = @()
    foreach ($candidate in $releaseCandidates) {
        $desktopExecutable = Join-Path $candidate "matchscope-desktop.exe"
        $nsisDirectory = Join-Path $candidate "bundle/nsis"
        $desktopExists = Test-Path -LiteralPath $desktopExecutable -PathType Leaf
        $nsisCandidate = Get-NewestFile -Directory $nsisDirectory -Predicate {
            param($file) $file.Name -match ("_" + $bundleArchitecture + "-setup\.exe$")
        }
        $nsisExists = $null -ne $nsisCandidate
        $releaseDiagnostics += [ordered]@{
            candidate     = $candidate
            appPath       = $desktopExecutable
            appExists     = $desktopExists
            sidecarPath   = $sidecarBinaryPath
            sidecarExists = $sidecarExists
            nsisPath      = if ($nsisExists) { $nsisCandidate.FullName } else { $nsisDirectory }
            nsisExists    = $nsisExists
        }
        if (-not $desktopExists -or -not $nsisExists) {
            continue
        }

        $releaseDirectory = $candidate
        $nsisArtifact = $nsisCandidate
        $msiArtifact = Get-NewestFile -Directory (Join-Path $candidate "bundle/msi") -Predicate {
            param($file) $file.Name -match ("_" + $bundleArchitecture + "(?:_|-)") -and $file.Extension -ieq ".msi"
        }
        break
    }

    if ($null -eq $releaseDirectory -or -not $sidecarExists) {
        Write-Host "产物路径诊断：" -ForegroundColor Yellow
        foreach ($diagnostic in $releaseDiagnostics) {
            Write-Host ("  app:     {0} (Exists={1})" -f $diagnostic.appPath, $diagnostic.appExists) -ForegroundColor Yellow
            Write-Host ("  sidecar: {0} (Exists={1})" -f $diagnostic.sidecarPath, $diagnostic.sidecarExists) -ForegroundColor Yellow
            Write-Host ("  nsis:    {0} (Exists={1})" -f $diagnostic.nsisPath, $diagnostic.nsisExists) -ForegroundColor Yellow
        }
        if ($releaseDiagnostics.Count -eq 0) {
            Write-Host ("  sidecar: {0} (Exists={1})" -f $sidecarBinaryPath, $sidecarExists) -ForegroundColor Yellow
        }
        throw "未找到完整 Tauri Release 产物。需要 matchscope-desktop.exe、权威 sidecar $sidecarBinaryPath 和 bundle/nsis/*-setup.exe。MSI 为可选产物。"
    }
    Write-Host "Release 目录：$releaseDirectory"
    Write-Host "NSIS 安装包：$($nsisArtifact.FullName)"
    if ($null -ne $msiArtifact) {
        Write-Host "MSI 安装包：$($msiArtifact.FullName)"
    }
    else {
        Write-Warning "未找到 MSI 安装包，将继续发布 NSIS 安装包和便携版文件。"
    }

    $resolvedOutputDirectory = Resolve-SafeOutputDirectory -RepositoryRoot $repositoryRoot -RequestedPath $OutputDirectory
    Enter-BuildStage "准备发布目录"
    # The safety checks above make this the only recursive cleanup performed
    # by the release script.  Previous releases are deliberately replaced.
    if (Test-Path -LiteralPath $resolvedOutputDirectory) {
        Remove-Item -LiteralPath $resolvedOutputDirectory -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $resolvedOutputDirectory | Out-Null

    $packageBaseName = "MatchScope-$version-$platformLabel"
    $stagingRoot = Join-Path $resolvedOutputDirectory ".staging"
    $stagingDirectory = Join-Path $stagingRoot ($packageBaseName + "-" + [guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $stagingDirectory | Out-Null
    $portableDirectory = Join-Path $stagingDirectory "portable"
    New-Item -ItemType Directory -Force -Path $portableDirectory | Out-Null

    $nsisDestination = Join-Path $stagingDirectory $nsisArtifact.Name
    Copy-Item -LiteralPath $nsisArtifact.FullName -Destination $nsisDestination
    $manifestArtifacts = @()
    $manifestArtifacts = Add-ManifestArtifact -List $manifestArtifacts -RootPath $stagingDirectory -FilePath $nsisDestination -Type "nsis-installer" -Required $true

    if ($null -ne $msiArtifact) {
        $msiDestination = Join-Path $stagingDirectory $msiArtifact.Name
        Copy-Item -LiteralPath $msiArtifact.FullName -Destination $msiDestination
        $manifestArtifacts = Add-ManifestArtifact -List $manifestArtifacts -RootPath $stagingDirectory -FilePath $msiDestination -Type "msi-installer" -Required $false
    }

    $desktopExecutableSource = Join-Path $releaseDirectory "matchscope-desktop.exe"
    $sidecarExecutableSource = $sidecarBinaryPath
    $desktopExecutableDestination = Join-Path $portableDirectory "MatchScope.exe"
    $sidecarExecutableDestination = Join-Path $portableDirectory "simulator-api.exe"
    Copy-Item -LiteralPath $desktopExecutableSource -Destination $desktopExecutableDestination
    Copy-Item -LiteralPath $sidecarExecutableSource -Destination $sidecarExecutableDestination
    $manifestArtifacts = Add-ManifestArtifact -List $manifestArtifacts -RootPath $stagingDirectory -FilePath $desktopExecutableDestination -Type "portable-client" -Required $true
    $manifestArtifacts = Add-ManifestArtifact -List $manifestArtifacts -RootPath $stagingDirectory -FilePath $sidecarExecutableDestination -Type "portable-sidecar" -Required $true

    $portableReadmeSource = Join-Path $desktopRoot "portable/README.txt"
    $portableReadmeDestination = Join-Path $portableDirectory "README.txt"
    if (Test-Path -LiteralPath $portableReadmeSource) {
        Copy-Item -LiteralPath $portableReadmeSource -Destination $portableReadmeDestination
    }
    else {
        Write-Utf8File -Path $portableReadmeDestination -Content "解压后请保持 MatchScope.exe 与 simulator-api.exe 在同一目录，然后运行 MatchScope.exe。`r`n"
    }

    $readmePath = Join-Path $stagingDirectory "README.txt"
    $readmeContent = @"
MatchScope $version Windows 客户端
================================

本目录包含可分发的 Windows 安装包和便携版文件。
目标架构：$TargetTriple（文件名标签：$bundleArchitecture；ZIP：$packageBaseName.zip）

安装包
------
- $($nsisArtifact.Name)：NSIS 安装包，推荐普通用户使用。
$(if ($null -ne $msiArtifact) { "- $($msiArtifact.Name)：MSI 安装包，可用于企业部署。" } else { "- 本次构建未生成 MSI；如需 MSI，请检查 Windows Installer/WiX 环境后重新构建。" })

便携版
------
进入 portable 目录，保持其中两个 EXE 在同一目录并运行 MatchScope.exe。
便携版不需要 Node.js、Go 或 Rust，但目标 Windows 需要 WebView2 Runtime。

校验
----
MANIFEST.json 记录构建目标和文件 SHA-256；SHA256SUMS.txt 可用于校验发布目录中的文件。
本客户端未签名，首次运行可能显示 Windows SmartScreen 提示。
"@
    Write-Utf8File -Path $readmePath -Content $readmeContent.TrimStart()

    $manifestPath = Join-Path $stagingDirectory "MANIFEST.json"
    $manifest = [ordered]@{
        schemaVersion = "matchscope-release/v1"
        productName = "MatchScope"
        version = $version
        platform = "windows"
        architecture = $platformLabel -replace "^windows-", ""
        targetTriple = $TargetTriple
        packageName = $packageBaseName
        generatedAtUtc = [DateTime]::UtcNow.ToString("o")
        artifacts = @($manifestArtifacts)
    }
    Write-Utf8File -Path $manifestPath -Content (($manifest | ConvertTo-Json -Depth 6) + "`r`n")

    $stagingChecksumsPath = Join-Path $stagingDirectory "SHA256SUMS.txt"
    Write-ChecksumsFile -RootPath $stagingDirectory -DestinationPath $stagingChecksumsPath

    # Publish the individual files next to the ZIP for users who do not need
    # an archive, while the archive itself contains exactly the same payload.
    Get-ChildItem -LiteralPath $stagingDirectory -Force | ForEach-Object {
        Copy-Item -LiteralPath $_.FullName -Destination $resolvedOutputDirectory -Recurse -Force
    }

    $zipPath = Join-Path $resolvedOutputDirectory ($packageBaseName + ".zip")
    Compress-Archive -Path (Join-Path $stagingDirectory "*") -DestinationPath $zipPath -CompressionLevel Optimal -Force

    Enter-BuildStage "校验 ZIP 与生成校验和"
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [IO.Compression.ZipFile]::OpenRead($zipPath)
    try {
        $entryNames = @($archive.Entries | ForEach-Object { $_.FullName.Replace("\", "/") })
        foreach ($requiredEntry in @($nsisArtifact.Name, "README.txt", "MANIFEST.json", "SHA256SUMS.txt", "portable/MatchScope.exe", "portable/simulator-api.exe")) {
            if ($entryNames -notcontains $requiredEntry) {
                throw "ZIP 缺少必需文件 '$requiredEntry'。"
            }
        }
        if ($null -ne $msiArtifact -and $entryNames -notcontains $msiArtifact.Name) {
            throw "ZIP 缺少已发现的 MSI 文件 '$($msiArtifact.Name)'。"
        }
    }
    finally {
        $archive.Dispose()
    }

    # Remove the private staging tree before hashing the published directory;
    # otherwise SHA256SUMS.txt would contain paths that are deleted below.
    if (Test-Path -LiteralPath $stagingDirectory) {
        Remove-Item -LiteralPath $stagingDirectory -Recurse -Force
    }
    $stagingDirectory = $null
    if (Test-Path -LiteralPath $stagingRoot) {
        $remaining = @(Get-ChildItem -LiteralPath $stagingRoot -Force -ErrorAction SilentlyContinue)
        if ($remaining.Count -eq 0) {
            Remove-Item -LiteralPath $stagingRoot -Force
        }
    }

    $publishedChecksumsPath = Join-Path $resolvedOutputDirectory "SHA256SUMS.txt"
    Write-ChecksumsFile -RootPath $resolvedOutputDirectory -DestinationPath $publishedChecksumsPath
    $zipHash = Get-FileSha256 $zipPath

    Write-Host ""
    Write-Host "客户端构建完成。" -ForegroundColor Green
    Write-Host "发布目录：$resolvedOutputDirectory"
    Write-Host "ZIP：      $zipPath"
    Write-Host "SHA-256：  $zipHash"
}
catch {
    Write-Error "[失败][$script:BuildStage] $($_.Exception.Message)"
    exit 1
}
finally {
    if ($null -ne $stagingDirectory -and (Test-Path -LiteralPath $stagingDirectory)) {
        Remove-Item -LiteralPath $stagingDirectory -Recurse -Force -ErrorAction SilentlyContinue
    }
    if ($null -ne $stagingDirectory) {
        $stagingRoot = Split-Path -Parent $stagingDirectory
        if (Test-Path -LiteralPath $stagingRoot) {
            $remaining = @(Get-ChildItem -LiteralPath $stagingRoot -Force -ErrorAction SilentlyContinue)
            if ($remaining.Count -eq 0) {
                Remove-Item -LiteralPath $stagingRoot -Force -ErrorAction SilentlyContinue
            }
        }
    }
    if ($cargoPathWasAdded) {
        if ($null -eq $originalProcessPath) {
            Remove-Item Env:PATH -ErrorAction SilentlyContinue
        }
        else {
            $env:PATH = $originalProcessPath
        }
    }
}
