[CmdletBinding()]
param(
    [string]$TargetTriple = "x86_64-pc-windows-msvc",
    [string]$OutputDirectory = "dist",
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"

$desktopRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$packageJson = Get-Content -Raw -LiteralPath (Join-Path $desktopRoot "package.json") | ConvertFrom-Json
$version = [string]$packageJson.version

if ($TargetTriple -notmatch "^[A-Za-z0-9][A-Za-z0-9._-]*$") {
    throw "Invalid target triple '$TargetTriple'."
}
if ($version -notmatch "^[0-9A-Za-z][0-9A-Za-z.+-]*$") {
    throw "Invalid package version '$version'."
}

$platformLabel = switch -Regex ($TargetTriple) {
    "^x86_64-" { "windows-x64"; break }
    "^aarch64-" { "windows-arm64"; break }
    "^i686-" { "windows-x86"; break }
    default { throw "Unsupported Windows target triple '$TargetTriple'." }
}

$cargoBin = Join-Path $env:USERPROFILE ".cargo\bin"
if ((Test-Path -LiteralPath $cargoBin) -and -not (($env:PATH -split ";") -contains $cargoBin)) {
    $env:PATH = "$cargoBin;$env:PATH"
}

if (-not $SkipBuild) {
    & (Join-Path $PSScriptRoot "build-sidecar.ps1") -TargetTriple $TargetTriple

    if (-not (Get-Command "npm.cmd" -ErrorAction SilentlyContinue)) {
        throw "npm.cmd was not found. Install Node.js before building the portable package."
    }

    $tauriArguments = @("run", "tauri", "--", "build", "--no-bundle", "--ci")
    $rustHost = if (Get-Command "rustc" -ErrorAction SilentlyContinue) {
        (& rustc -vV | Select-String "^host: " | ForEach-Object { $_.Line.Substring(6) })
    }
    if ($rustHost -and $rustHost -ne $TargetTriple) {
        $tauriArguments += @("--target", $TargetTriple)
    }

    Push-Location $desktopRoot
    try {
        & npm.cmd @tauriArguments
        if ($LASTEXITCODE -ne 0) {
            throw "Tauri portable executable build failed with exit code $LASTEXITCODE."
        }
    }
    finally {
        Pop-Location
    }
}

$releaseCandidates = @(
    (Join-Path $desktopRoot "src-tauri/target/$TargetTriple/release"),
    (Join-Path $desktopRoot "src-tauri/target/release")
)
$releaseDirectory = $releaseCandidates |
    Where-Object {
        (Test-Path -LiteralPath (Join-Path $_ "matchscope-desktop.exe")) -and
        (Test-Path -LiteralPath (Join-Path $_ "simulator-api.exe"))
    } |
    Select-Object -First 1
if (-not $releaseDirectory) {
    throw "Release executables were not found. Run without -SkipBuild first."
}

$resolvedOutputDirectory = if ([IO.Path]::IsPathRooted($OutputDirectory)) {
    [IO.Path]::GetFullPath($OutputDirectory)
} else {
    [IO.Path]::GetFullPath((Join-Path $desktopRoot $OutputDirectory))
}
New-Item -ItemType Directory -Force -Path $resolvedOutputDirectory | Out-Null

$stagingRoot = Join-Path $desktopRoot ".portable"
New-Item -ItemType Directory -Force -Path $stagingRoot | Out-Null
$stagingRootFull = [IO.Path]::GetFullPath($stagingRoot).TrimEnd([IO.Path]::DirectorySeparatorChar)
$packageBaseName = "MatchScope-$version-$platformLabel-portable"
$stagingDirectory = Join-Path $stagingRoot ("$packageBaseName-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $stagingDirectory | Out-Null

try {
    Copy-Item -LiteralPath (Join-Path $releaseDirectory "matchscope-desktop.exe") -Destination (Join-Path $stagingDirectory "MatchScope.exe")
    Copy-Item -LiteralPath (Join-Path $releaseDirectory "simulator-api.exe") -Destination (Join-Path $stagingDirectory "simulator-api.exe")
    Copy-Item -LiteralPath (Join-Path $desktopRoot "portable/README.txt") -Destination (Join-Path $stagingDirectory "README.txt")

    $zipPath = Join-Path $resolvedOutputDirectory "$packageBaseName.zip"
    Compress-Archive -Path (Join-Path $stagingDirectory "*") -DestinationPath $zipPath -CompressionLevel Optimal -Force

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $archive = [IO.Compression.ZipFile]::OpenRead($zipPath)
    try {
        $entryNames = @($archive.Entries | ForEach-Object { $_.FullName })
        foreach ($requiredEntry in @("MatchScope.exe", "simulator-api.exe", "README.txt")) {
            if ($entryNames -notcontains $requiredEntry) {
                throw "Portable archive is missing required entry '$requiredEntry'."
            }
        }
    }
    finally {
        $archive.Dispose()
    }

    $hash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToLowerInvariant()
    $checksumPath = "$zipPath.sha256"
    [IO.File]::WriteAllText($checksumPath, "$hash  $([IO.Path]::GetFileName($zipPath))`n", [Text.UTF8Encoding]::new($false))

    Write-Host "Portable ZIP: $zipPath"
    Write-Host "SHA-256:     $hash"
}
finally {
    $stagingFull = [IO.Path]::GetFullPath($stagingDirectory)
    $expectedPrefix = $stagingRootFull + [IO.Path]::DirectorySeparatorChar
    if ($stagingFull.StartsWith($expectedPrefix, [StringComparison]::OrdinalIgnoreCase) -and (Test-Path -LiteralPath $stagingFull)) {
        Remove-Item -LiteralPath $stagingFull -Recurse -Force
    }
}
