[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$sourceRoot = Split-Path -Parent $PSScriptRoot
$tokens = $null
$parseErrors = $null
$buildAst = [Management.Automation.Language.Parser]::ParseInput(
    (Get-Content -Raw -Encoding UTF8 (Join-Path $PSScriptRoot 'build-client.ps1')),
    [ref]$tokens, [ref]$parseErrors
)
if ($parseErrors.Count) { throw ($parseErrors | Out-String) }

# Load helpers and execute the actual packaging tail with fixture artifacts.
# No toolchain, dependency installation or application build is invoked.
foreach ($statement in $buildAst.EndBlock.Statements) {
    if ($statement -is [Management.Automation.Language.FunctionDefinitionAst]) {
        . ([scriptblock]::Create($statement.Extent.Text))
    }
}
. (Join-Path $PSScriptRoot 'build-client-safety.ps1')
$buildTry = $buildAst.EndBlock.Statements |
    Where-Object { $_ -is [Management.Automation.Language.TryStatementAst] } |
    Select-Object -First 1
$packagingStatements = @($buildTry.Body.Statements)
$start = 0
while ($start -lt $packagingStatements.Count -and
    $packagingStatements[$start].Extent.Text -notmatch '^\$resolvedOutputDirectory\s*=') {
    $start++
}
if ($start -eq $packagingStatements.Count) { throw 'Packaging entry point not found.' }
$packaging = [scriptblock]::Create(
    ($packagingStatements[$start..($packagingStatements.Count - 1)].Extent.Text -join "`n")
)

$workflow = Get-Content -Raw -Encoding UTF8 (Join-Path $sourceRoot '.github/workflows/weekly-windows-release.yml')
$runBlocks = [regex]::Matches($workflow, '(?m)^        run: \|\r?\n((?:          .*\r?\n|\r?\n)+)')
$locateAssets = $null
$publishRelease = $null
foreach ($block in $runBlocks) {
    $body = [regex]::Replace($block.Groups[1].Value, '(?m)^          ', '')
    $null = [Management.Automation.Language.Parser]::ParseInput($body, [ref]$tokens, [ref]$parseErrors)
    if ($parseErrors.Count) { throw ($parseErrors | Out-String) }
    if ($body.Contains('$assetsJson =')) { $locateAssets = [scriptblock]::Create($body) }
    if ($body.Contains('& gh release create')) { $publishRelease = [scriptblock]::Create($body) }
}
if ($null -eq $locateAssets -or $null -eq $publishRelease) { throw 'Release workflow steps not found.' }

function Assert-EqualSet {
    param([object[]]$Actual, [object[]]$Expected, [string]$Label)
    if (Compare-Object @($Actual | Sort-Object) @($Expected | Sort-Object)) {
        throw "$Label mismatch: $($Actual -join ', ')"
    }
}

# Replace GitHub CLI with an in-process recorder; this test never publishes.
function gh {
    $script:ghCalls += ,@($args)
    $global:LASTEXITCODE = if ($args[1] -eq 'view') { 1 } else { 0 }
}

$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('matchscope-package-test-' + [guid]::NewGuid().ToString('N'))
$savedOutput = $env:GITHUB_OUTPUT
$savedAssets = $env:RELEASE_ASSETS
$savedTag = $env:RELEASE_TAG
$savedRepository = $env:GITHUB_REPOSITORY
try {
    New-Item -ItemType Directory -Path $testRoot | Out-Null
    foreach ($architecture in @('x64', 'arm64')) {
        foreach ($withMsi in @($false, $true)) {
            $repositoryRoot = Join-Path $testRoot "$architecture-$withMsi"
            $releaseDirectory = Join-Path $repositoryRoot 'fixture binaries'
            $desktopRoot = Join-Path $repositoryRoot 'desktop'
            New-Item -ItemType Directory -Path $releaseDirectory -Force | Out-Null
            $version = '1.2.3'
            $platformLabel = "windows-$architecture"
            $bundleArchitecture = $architecture
            $TargetTriple = if ($architecture -eq 'x64') { 'x86_64-pc-windows-msvc' } else { 'aarch64-pc-windows-msvc' }
            $OutputDirectory = 'dist/release'
            $sidecarBinaryPath = Join-Path $releaseDirectory 'simulator-api.exe'
            $updaterBinaryPath = Join-Path $releaseDirectory 'Updater.exe'
            $nsisName = "MatchScope_${version}_${architecture}-setup.exe"
            $msiName = "MatchScope_${version}_${architecture}_en-US.msi"
            foreach ($name in @('matchscope-desktop.exe', 'simulator-api.exe', 'Updater.exe', $nsisName, $msiName)) {
                Write-Utf8File -Path (Join-Path $releaseDirectory $name) -Content "fixture $name"
            }
            $nsisArtifact = Get-Item -LiteralPath (Join-Path $releaseDirectory $nsisName)
            $msiArtifact = if ($withMsi) { Get-Item -LiteralPath (Join-Path $releaseDirectory $msiName) } else { $null }
            & $packaging

            $output = Join-Path $repositoryRoot $OutputDirectory
            $zipName = "MatchScope-$version-$platformLabel.zip"
            $archive = [IO.Compression.ZipFile]::OpenRead((Join-Path $output $zipName))
            try {
                $entries = @($archive.Entries | Where-Object { $_.Name } | ForEach-Object { $_.FullName.Replace('\', '/') })
                Assert-EqualSet $entries @('README.txt', 'MANIFEST.json', 'SHA256SUMS.txt', 'portable/README.txt', 'portable/MatchScope.exe', 'portable/simulator-api.exe', 'portable/Updater.exe') 'Portable ZIP'
                $manifestReader = [IO.StreamReader]::new($archive.GetEntry('MANIFEST.json').Open())
                try { $manifest = $manifestReader.ReadToEnd() | ConvertFrom-Json } finally { $manifestReader.Dispose() }
                Assert-EqualSet @($manifest.artifacts.name) @('portable/MatchScope.exe', 'portable/simulator-api.exe', 'portable/Updater.exe') 'ZIP manifest'
                $sumReader = [IO.StreamReader]::new($archive.GetEntry('SHA256SUMS.txt').Open())
                try { $internalSums = $sumReader.ReadToEnd() } finally { $sumReader.Dispose() }
                $internalNames = @($internalSums -split '\r?\n' | Where-Object { $_ } | ForEach-Object { ($_ -split '  ', 2)[1] })
                Assert-EqualSet $internalNames @($entries | Where-Object { $_ -ne 'SHA256SUMS.txt' }) 'ZIP checksums'
            }
            finally { $archive.Dispose() }

            foreach ($line in (Get-Content -LiteralPath (Join-Path $output 'SHA256SUMS.txt'))) {
                $parts = $line -split '  ', 2
                if ((Get-FileSha256 (Join-Path $output $parts[1])) -ne $parts[0]) { throw "Invalid checksum: $line" }
            }
            $expectedAssets = @($zipName, $nsisName, 'SHA256SUMS.txt')
            if ($withMsi) { $expectedAssets += $msiName }
            $externalSums = Get-Content -Raw -LiteralPath (Join-Path $output 'SHA256SUMS.txt')
            foreach ($name in @($zipName, $nsisName) + @($(if ($withMsi) { $msiName }))) {
                if ($name -and -not $externalSums.Contains("  $name")) { throw "Missing checksum: $name" }
            }
            $env:GITHUB_OUTPUT = Join-Path $repositoryRoot 'github-output.txt'
            Push-Location $repositoryRoot
            try { & $locateAssets } finally { Pop-Location }
            $assetLine = Get-Content -LiteralPath $env:GITHUB_OUTPUT | Where-Object { $_.StartsWith('assets=') }
            $env:RELEASE_ASSETS = $assetLine.Substring(7)
            [string[]]$assets = $env:RELEASE_ASSETS | ConvertFrom-Json
            Assert-EqualSet @($assets | ForEach-Object { Split-Path -Leaf $_ }) $expectedAssets 'Release assets'
            $env:RELEASE_TAG = 'v1.2.3'
            $env:GITHUB_REPOSITORY = 'fixture/repository'
            $script:ghCalls = @()
            & $publishRelease
            $create = @($script:ghCalls | Where-Object { $_[1] -eq 'create' })
            if ($create.Count -ne 1) { throw 'Expected one release create call.' }
            Assert-EqualSet @($create[0][3..(2 + $assets.Count)]) $assets 'Uploaded assets'
            Write-Host "PASS: $architecture, MSI=$withMsi"
        }
    }
}
finally {
    $env:GITHUB_OUTPUT = $savedOutput
    $env:RELEASE_ASSETS = $savedAssets
    $env:RELEASE_TAG = $savedTag
    $env:GITHUB_REPOSITORY = $savedRepository
    $resolvedTestRoot = [IO.Path]::GetFullPath($testRoot)
    $tempPrefix = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (-not $resolvedTestRoot.StartsWith($tempPrefix, [StringComparison]::OrdinalIgnoreCase) -or
        (Split-Path -Leaf $resolvedTestRoot) -notlike 'matchscope-package-test-*') {
        throw "Refusing unexpected cleanup path: $resolvedTestRoot"
    }
    if (Test-Path -LiteralPath $resolvedTestRoot) { Remove-Item -LiteralPath $resolvedTestRoot -Recurse -Force }
}
