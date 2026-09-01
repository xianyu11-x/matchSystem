# Shared path-safety helpers for build-client.ps1 and its controlled tests.
# This file only declares functions when dot-sourced; it never changes the
# caller's environment or performs filesystem cleanup by itself.

function Get-AbsolutePath {
    param([Parameter(Mandatory = $true)][string]$Path)

    return [IO.Path]::GetFullPath($Path)
}

function Get-NormalizedAbsolutePath {
    param([Parameter(Mandatory = $true)][string]$Path)

    $fullPath = Get-AbsolutePath $Path
    if ($fullPath.Length -gt 3) {
        return $fullPath.TrimEnd([char]'\', [char]'/')
    }
    return $fullPath
}

function Test-ReparsePoint {
    param([Parameter(Mandatory = $true)][string]$Path)

    if (-not (Test-Path -LiteralPath $Path)) {
        return $false
    }
    $item = Get-Item -LiteralPath $Path -Force
    return (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)
}

function Assert-NoReparsePathComponents {
    param(
        [Parameter(Mandatory = $true)][string]$BasePath,
        [Parameter(Mandatory = $true)][string]$CandidatePath
    )

    $base = Get-NormalizedAbsolutePath $BasePath
    $candidate = Get-NormalizedAbsolutePath $CandidatePath
    $basePrefix = $base + [IO.Path]::DirectorySeparatorChar
    if (-not $candidate.StartsWith($basePrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "路径不在允许的基准目录内：$candidate"
    }

    $current = $base
    if (Test-ReparsePoint $current) {
        throw "路径组件是符号链接或 junction，拒绝继续：$current"
    }

    $relative = $candidate.Substring($basePrefix.Length)
    foreach ($component in ($relative -split '[\\/]')) {
        if ([string]::IsNullOrWhiteSpace($component)) {
            continue
        }
        $current = Join-Path $current $component
        if (-not (Test-Path -LiteralPath $current)) {
            # Once a component does not exist, later components cannot point
            # at an existing object that the cleanup could accidentally follow.
            break
        }
        $item = Get-Item -LiteralPath $current -Force
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "路径组件是符号链接或 junction，拒绝清理：$current"
        }
        if (-not $item.PSIsContainer -and $current -ne $candidate) {
            throw "输出路径的中间组件不是目录：$current"
        }
    }
}

function Resolve-SafeOutputDirectory {
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$RequestedPath
    )

    $candidate = if ([IO.Path]::IsPathRooted($RequestedPath)) {
        Get-NormalizedAbsolutePath $RequestedPath
    }
    else {
        Get-NormalizedAbsolutePath (Join-Path $RepositoryRoot $RequestedPath)
    }

    $repo = Get-NormalizedAbsolutePath $RepositoryRoot
    $distRoot = Get-NormalizedAbsolutePath (Join-Path $RepositoryRoot "dist")
    $repoPrefix = $repo + [IO.Path]::DirectorySeparatorChar
    $distPrefix = $distRoot + [IO.Path]::DirectorySeparatorChar

    if ($candidate -eq $repo -or $candidate -eq $distRoot) {
        throw "输出目录必须是仓库 dist 下的专用子目录，不能是仓库根目录或 dist 本身：$candidate"
    }
    if (-not $candidate.StartsWith($repoPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "输出目录必须位于仓库根目录内：$candidate"
    }
    if (-not $candidate.StartsWith($distPrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "输出目录必须位于仓库 dist 目录内，以避免清理源码目录：$candidate"
    }

    # Walk every existing component from dist through candidate.  This also
    # covers an existing intermediate junction when the candidate itself does
    # not exist yet (for example dist\link\release).
    Assert-NoReparsePathComponents -BasePath $distRoot -CandidatePath $candidate

    if (Test-Path -LiteralPath $candidate) {
        $candidateItem = Get-Item -LiteralPath $candidate -Force
        if (-not $candidateItem.PSIsContainer) {
            throw "输出路径不是目录：$candidate"
        }
    }

    return $candidate
}
