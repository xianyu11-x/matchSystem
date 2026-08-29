[CmdletBinding()]
param(
    [string]$TargetTriple = "x86_64-pc-windows-msvc",
    [string]$GoCommand = "go"
)

$ErrorActionPreference = "Stop"

$desktopRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$repositoryRoot = (Resolve-Path (Join-Path $desktopRoot "../..")).Path
$binaryDirectory = Join-Path $desktopRoot "src-tauri/binaries"
$outputPath = Join-Path $binaryDirectory "simulator-api-$TargetTriple.exe"

if ($TargetTriple -notmatch "^[A-Za-z0-9][A-Za-z0-9._-]*$") {
    throw "Invalid target triple '$TargetTriple'."
}
if (-not (Get-Command $GoCommand -ErrorAction SilentlyContinue)) {
    throw "Go executable '$GoCommand' was not found. Install Go or pass -GoCommand with its path."
}

New-Item -ItemType Directory -Force -Path $binaryDirectory | Out-Null

# Tauri selects this exact target-triple suffix when it packages an
# externalBin entry. The helper deliberately builds only the requested
# Windows sidecar and does not touch any other binaries.
$oldGoOs = $env:GOOS
$oldGoArch = $env:GOARCH
$oldCgoEnabled = $env:CGO_ENABLED
$locationPushed = $false
try {
    $env:GOOS = "windows"
    switch -Regex ($TargetTriple) {
        "^x86_64-" { $env:GOARCH = "amd64"; break }
        "^i686-" { $env:GOARCH = "386"; break }
        "^aarch64-" { $env:GOARCH = "arm64"; break }
        default { throw "Unsupported Windows target triple '$TargetTriple'." }
    }
    $env:CGO_ENABLED = "0"

    Push-Location $repositoryRoot
    $locationPushed = $true
    & $GoCommand build -trimpath -ldflags "-s -w" -o $outputPath ./cmd/simulator-api
    if ($LASTEXITCODE -ne 0) {
        throw "Go sidecar build failed with exit code $LASTEXITCODE."
    }
}
finally {
    if ($locationPushed) { Pop-Location }
    if ($null -eq $oldGoOs) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $oldGoOs }
    if ($null -eq $oldGoArch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $oldGoArch }
    if ($null -eq $oldCgoEnabled) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue } else { $env:CGO_ENABLED = $oldCgoEnabled }
}

Write-Host "Built Tauri sidecar: $outputPath"
