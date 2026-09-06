[CmdletBinding()]
param(
    [string]$TargetTriple = "x86_64-pc-windows-msvc",
    [string]$CargoCommand = "cargo",
    [string]$TargetDirectory = ""
)

$ErrorActionPreference = "Stop"

$desktopRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$updaterRoot = (Resolve-Path (Join-Path $desktopRoot "updater")).Path
$manifestPath = Join-Path $updaterRoot "Cargo.toml"

$targetMachines = @{
    "x86_64-pc-windows-msvc" = [uint16]0x8664
    "aarch64-pc-windows-msvc" = [uint16]0xAA64
}
if (-not $targetMachines.ContainsKey($TargetTriple)) {
    throw "Unsupported Windows target triple '$TargetTriple'. Only x86_64-pc-windows-msvc and aarch64-pc-windows-msvc are supported."
}
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
    throw "Updater Cargo manifest was not found: $manifestPath"
}

function Resolve-CargoCommand {
    param([Parameter(Mandatory = $true)][string]$Command)

    if (Test-Path -LiteralPath $Command -PathType Leaf) {
        return (Resolve-Path -LiteralPath $Command).Path
    }
    $resolved = Get-Command -Name $Command -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -ne $resolved) {
        return [string]$resolved.Path
    }

    $profileRoot = [string]$env:USERPROFILE
    if (-not [string]::IsNullOrWhiteSpace($profileRoot)) {
        $defaultCargo = Join-Path $profileRoot ".cargo\bin\cargo.exe"
        if (Test-Path -LiteralPath $defaultCargo -PathType Leaf) {
            return $defaultCargo
        }
    }
    throw "Cargo executable '$Command' was not found. Install Rust or pass -CargoCommand with its path."
}

function Get-PeMachine {
    param([Parameter(Mandatory = $true)][string]$Path)

    $stream = [IO.File]::OpenRead($Path)
    $reader = [IO.BinaryReader]::new($stream)
    try {
        $stream.Position = 0x3C
        $peOffset = $reader.ReadInt32()
        if ($peOffset -lt 0 -or $peOffset -gt $stream.Length - 6) {
            throw "Invalid PE header offset."
        }
        $stream.Position = $peOffset
        $signature = [Text.Encoding]::ASCII.GetString($reader.ReadBytes(4))
        if ($signature -ne "PE`0`0") {
            throw "Missing PE signature."
        }
        return [uint16]$reader.ReadUInt16()
    }
    finally {
        $reader.Dispose()
        $stream.Dispose()
    }
}

$cargoPath = Resolve-CargoCommand $CargoCommand
$targetRoot = if ([string]::IsNullOrWhiteSpace($TargetDirectory)) {
    Join-Path $updaterRoot "target"
}
elseif ([IO.Path]::IsPathRooted($TargetDirectory)) {
    [IO.Path]::GetFullPath($TargetDirectory)
}
else {
    [IO.Path]::GetFullPath((Join-Path $updaterRoot $TargetDirectory))
}
$releaseDirectory = Join-Path $targetRoot "$TargetTriple\release"
$binaryPath = Join-Path $releaseDirectory "Updater.exe"
$lockPath = Join-Path $updaterRoot "Cargo.lock"

$cargoArguments = @(
    "build",
    "--manifest-path", $manifestPath,
    "--target-dir", $targetRoot,
    "--target", $TargetTriple,
    "--release"
)
if (Test-Path -LiteralPath $lockPath -PathType Leaf) {
    $cargoArguments += "--locked"
}

Write-Host "Building native updater for $TargetTriple..."
& $cargoPath @cargoArguments
if ($LASTEXITCODE -ne 0) {
    throw "Updater build failed with exit code $LASTEXITCODE."
}
if (-not (Test-Path -LiteralPath $binaryPath -PathType Leaf)) {
    throw "Updater build completed but the target-specific binary was not found: $binaryPath"
}

$expectedMachine = [uint16]$targetMachines[$TargetTriple]
try {
    $actualMachine = Get-PeMachine $binaryPath
}
catch {
    throw "Updater binary is not a valid Windows PE file: $binaryPath ($($_.Exception.Message))"
}
if ($actualMachine -ne $expectedMachine) {
    throw ("Updater binary architecture mismatch for {0}: expected PE machine 0x{1:X4}, found 0x{2:X4}. " +
        "The target-specific output was not used.") -f $TargetTriple, $expectedMachine, $actualMachine
}

Write-Host "Built native updater: $binaryPath"
