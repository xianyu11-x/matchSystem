$ErrorActionPreference = 'Stop'

$deps = go list -deps ./internal/matchsystem/expression
foreach ($forbidden in @(
    'matchSystem/internal/matchsystem/prefilter',
    'matchSystem/internal/matchsystem/evaluation',
    'github.com/RoaringBitmap/roaring/v2'
)) {
    if ($deps -contains $forbidden) {
        throw "expression dependency boundary violated: $forbidden"
    }
}

$evaluationDeps = go list -deps ./internal/matchsystem/evaluation
if ($evaluationDeps -contains 'matchSystem/internal/matchsystem/prefilter') {
    throw 'evaluation dependency boundary violated: evaluation must not depend on Prefilter'
}

$factDeps = go list -deps ./internal/matchsystem/fact
if ($factDeps -contains 'matchSystem/internal/matchsystem/expression') {
    throw 'fact dependency boundary violated: Fact providers must not depend on expression'
}

$expressionRoot = Join-Path $PSScriptRoot '..\internal\matchsystem\expression'
$productionFiles = @(Get-ChildItem -LiteralPath $expressionRoot -Filter '*.go' -File | Where-Object { $_.Name -notlike '*_test.go' })
if ($productionFiles.Count -gt 6) {
    throw "expression production file budget exceeded: $($productionFiles.Count) > 6"
}

# The scalar package has one public compilation boundary. These assertions
# prevent an old AST/IR or domain callback surface from being reintroduced by
# accident while the Prefilter-owned bitmap compiler evolves independently.
$forbiddenSymbols = @(
    'ResultBitmap', 'KindBitmap', 'BitmapState', 'BitmapProperties',
    'BitmapInstruction', 'EvaluateBitmap', 'BitmapInstructions',
    'DomainDescriptor', 'DomainField', 'DomainLeaf', 'LeafHandle',
    'DomainLeafCompiler', 'LeafEvaluator',
    'RootInstruction', 'InstructionID', 'EvaluateBoolAt',
    'EvaluateInt64At', 'EvaluateStringsAt', 'EvaluateUint64sAt',
    'NewArena', 'NewCompiler', 'ParseRoot', 'DecodeRootInto',
    'type Program struct', 'type Arena struct', 'type Node struct',
    'type NodeRef struct', 'type Root struct',
    'domain_call', 'bitmap_'
)
foreach ($file in $productionFiles) {
    $source = Get-Content -LiteralPath $file.FullName -Raw
    foreach ($symbol in $forbiddenSymbols) {
        if ($source.Contains($symbol)) {
            throw "expression scalar boundary contains forbidden symbol '$symbol': $($file.Name)"
        }
    }
}

# The production surface has no compatibility workflow for the removed
# scorer/Match-Fact patch model or the old Prefilter/Evaluation APIs. Keep the
# assertion repository-wide so aliases in the matchsystem facade cannot
# quietly resurrect those entry points.
$productionRoots = @(
    (Join-Path $PSScriptRoot '..\internal\matchsystem'),
    (Join-Path $PSScriptRoot '..\cmd')
)
$removedProductionSymbols = @(
    'mergeMatchFacts', 'MatchFactsConfig', 'ScorerRegistry',
    'EvaluationMatchFactsConfig',
    'func Compile\(', 'func CompileTyped\(', 'func CompileConfig\(',
    'patchFallback', 'PatchFallback'
)
foreach ($root in $productionRoots) {
    if (-not (Test-Path -LiteralPath $root)) { continue }
    $files = Get-ChildItem -LiteralPath $root -Recurse -Filter '*.go' -File | Where-Object { $_.Name -notlike '*_test.go' }
    foreach ($file in $files) {
        $source = Get-Content -LiteralPath $file.FullName -Raw
        foreach ($symbol in $removedProductionSymbols) {
            if ($source -match $symbol) {
                throw "removed production surface '$symbol' found in $($file.FullName)"
            }
        }
    }
}

# Every JSON envelope has one current schema, and all legacy envelope strings
# must stay absent from production Go and live documentation. The script is
# intentionally not part of this scan because it contains the legacy strings
# as negative assertions. doc/design-decisions/archive is historical material
# and is excluded by the generic archive path filter below.
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$legacySchemas = @(
    'logical-node-contract/v1', 'logical-node-contract/v2',
    'expression-scalar/v1', 'expression-scalar/v2',
    'prefilter/v1', 'prefilter/v2',
    'evaluation/v1', 'evaluation/v2'
)
$currentSchemas = @(
    'logical-node-contract/v3',
    'expression-scalar/v3',
    'prefilter/v3',
    'evaluation/v3'
)
$schemaFiles = @()
foreach ($root in $productionRoots) {
    if (-not (Test-Path -LiteralPath $root)) { continue }
    $schemaFiles += Get-ChildItem -LiteralPath $root -Recurse -Filter '*.go' -File |
        Where-Object { $_.Name -notlike '*_test.go' }
}
$rootReadme = Join-Path $repoRoot 'README.md'
if (Test-Path -LiteralPath $rootReadme) {
    $schemaFiles += Get-Item -LiteralPath $rootReadme
}
$liveDocRoot = Join-Path $repoRoot 'doc'
if (Test-Path -LiteralPath $liveDocRoot) {
    $schemaFiles += Get-ChildItem -LiteralPath $liveDocRoot -Recurse -Filter '*.md' -File |
        Where-Object { $_.FullName -notmatch '[\\/]archive[\\/]' }
}
foreach ($file in $schemaFiles) {
    $source = Get-Content -LiteralPath $file.FullName -Raw
    foreach ($schema in $legacySchemas) {
        if ($source.Contains($schema)) {
            throw "legacy JSON schema '$schema' found in $($file.FullName)"
        }
    }
}
$schemaText = ($schemaFiles | ForEach-Object {
    Get-Content -LiteralPath $_.FullName -Raw
}) -join "`n"
foreach ($schema in $currentSchemas) {
    if (-not $schemaText.Contains($schema)) {
        throw "current JSON schema '$schema' is missing from production/live sources"
    }
}

# Index declarations intentionally have one name only.  The name is both the
# declared Attribute name and the physical index/query identifier; reject a
# reintroduced member or index JSON key without scanning unrelated uses of the
# word field in validators and JSON helpers.
$contractSource = Get-Content -LiteralPath (Join-Path $repoRoot 'internal\matchsystem\contract\contract.go') -Raw
$indexSpecBlock = [regex]::Match($contractSource, '(?s)type IndexSpec struct \{.*?\}').Value
if ([string]::IsNullOrEmpty($indexSpecBlock) -or $indexSpecBlock -cmatch '\bField\b') {
    throw 'contract IndexSpec must not contain a Field member'
}
$parseIndexBlock = [regex]::Match($contractSource, '(?s)func parseIndex\(.*?\n\}\r?\n\r?\nfunc decodeStrict').Value
if ([string]::IsNullOrEmpty($parseIndexBlock) -or $parseIndexBlock -cmatch '"field"\s*:|`json:"field"`|\.field') {
    throw 'contract index parser must not accept an index field member'
}
$prefilterSource = Get-ChildItem -LiteralPath (Join-Path $repoRoot 'internal\matchsystem\prefilter') -Recurse -Filter '*.go' -File |
    Where-Object { $_.Name -notlike '*_test.go' } |
    ForEach-Object { Get-Content -LiteralPath $_.FullName -Raw }
if (($prefilterSource -join "`n") -cmatch '\.field\b|\bField\b') {
    throw 'prefilter index metadata must not contain a Field/field member'
}
$indexArrayPattern = '(?s)"indexes"\s*:\s*\[(.*?)\]'
foreach ($file in $schemaFiles) {
    $source = Get-Content -LiteralPath $file.FullName -Raw
    foreach ($array in [regex]::Matches($source, $indexArrayPattern)) {
        if ($array.Groups[1].Value -cmatch '"field"\s*:') {
            throw "index JSON must not contain field member: $($file.FullName)"
        }
    }
}

$compileEntry = Join-Path $expressionRoot 'json.go'
if (-not (Select-String -LiteralPath $compileEntry -Pattern 'func CompileScalarJSON\(' -Quiet)) {
    throw 'expression scalar boundary is missing CompileScalarJSON'
}
if (-not (Select-String -LiteralPath (Join-Path $expressionRoot 'program.go') -Pattern 'type ScalarProgram struct' -Quiet)) {
    throw 'expression scalar boundary is missing opaque ScalarProgram'
}

Write-Output 'expression dependency boundary: OK'
