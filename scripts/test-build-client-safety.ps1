[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
$safetyScript = Join-Path $PSScriptRoot "build-client-safety.ps1"
if (-not (Test-Path -LiteralPath $safetyScript)) {
    throw "Missing safety helper: $safetyScript"
}
. $safetyScript

function Assert-Throws {
    param(
        [Parameter(Mandatory = $true)][scriptblock]$Action,
        [Parameter(Mandatory = $true)][string]$Name
    )

    $threw = $false
    try {
        & $Action
    }
    catch {
        $threw = $true
    }
    if (-not $threw) {
        throw "Safety assertion did not reject: $Name"
    }
}

$tempRoot = Join-Path ([IO.Path]::GetTempPath()) ("matchscope-build-safety-" + [guid]::NewGuid().ToString("N"))
$distRoot = Join-Path $tempRoot "dist"
$outsideRoot = Join-Path $tempRoot "outside"
$junctionPath = Join-Path $distRoot "junction"
$junctionCreated = $false

try {
    New-Item -ItemType Directory -Path $distRoot -Force | Out-Null
    New-Item -ItemType Directory -Path $outsideRoot -Force | Out-Null

    $resolved = Resolve-SafeOutputDirectory -RepositoryRoot $tempRoot -RequestedPath "dist/release"
    $expected = Get-NormalizedAbsolutePath (Join-Path $tempRoot "dist/release")
    if ($resolved -ne $expected) {
        throw "Safe output path resolved unexpectedly: $resolved"
    }

    Assert-Throws -Name "repository root" -Action {
        Resolve-SafeOutputDirectory -RepositoryRoot $tempRoot -RequestedPath "." | Out-Null
    }
    Assert-Throws -Name "dist root" -Action {
        Resolve-SafeOutputDirectory -RepositoryRoot $tempRoot -RequestedPath "dist" | Out-Null
    }
    Assert-Throws -Name "path outside repository" -Action {
        Resolve-SafeOutputDirectory -RepositoryRoot $tempRoot -RequestedPath "..\outside" | Out-Null
    }

    try {
        New-Item -ItemType Junction -Path $junctionPath -Target $outsideRoot -ErrorAction Stop | Out-Null
        $junctionCreated = $true
    }
    catch {
        Write-Warning "Skipping junction case because this account cannot create a junction: $($_.Exception.Message)"
    }

    if ($junctionCreated) {
        Assert-Throws -Name "intermediate junction" -Action {
            Resolve-SafeOutputDirectory -RepositoryRoot $tempRoot -RequestedPath "dist/junction/release" | Out-Null
        }
    }

    Write-Host "Build-client output path safety checks passed." -ForegroundColor Green
}
finally {
    if ($junctionCreated -and (Test-Path -LiteralPath $junctionPath)) {
        Remove-Item -LiteralPath $junctionPath -Force -ErrorAction SilentlyContinue
    }
    if (Test-Path -LiteralPath $tempRoot) {
        Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
