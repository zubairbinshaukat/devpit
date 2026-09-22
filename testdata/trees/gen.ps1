<#
.SYNOPSIS
    Generates the fixture tree Devpit's scanner is developed against.

.DESCRIPTION
    Every folder in this tree is here because it broke a real tool. A project
    vendored inside another project's node_modules, a node_modules with no
    package.json beside it, a junction pointing back at live source code, a
    folder called "build" that is somebody's own scripts: each one is a way a
    cleaner can delete something it should not have.

    The Go tests build their own small copy of this tree in t.TempDir(), so CI
    never runs this script. It exists for two things a test cannot do well:
    poking at the tree by hand while working on the walker, and generating the
    100,000-file tree the benchmarks want, which -Large builds.

    Nothing here needs administrator rights. Symbolic links do, or Developer
    Mode, so the symlink case is attempted and skipped with a message when it
    is not available. Junctions never need either.

.PARAMETER Root
    Where to build the tree. Created if missing.

.PARAMETER Large
    Also build a 100,000-file node_modules for benchmarking. This takes a
    minute or two and roughly 400 MB.

.PARAMETER Force
    Delete Root first. Without it, an existing Root is added to rather than
    replaced.

.EXAMPLE
    .\gen.ps1 -Root C:\temp\devpit-fixture

.EXAMPLE
    .\gen.ps1 -Root D:\bench\devpit -Large -Force
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string] $Root,

    [switch] $Large,

    [switch] $Force
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# ---------------------------------------------------------------- helpers --

function New-Dir {
    param([string] $Path)
    if (-not (Test-Path -LiteralPath $Path)) {
        [void] (New-Item -ItemType Directory -Path $Path -Force)
    }
}

function New-Blob {
    param(
        [string] $Path,
        [int] $Bytes = 64
    )
    New-Dir -Path (Split-Path -Parent $Path)
    $content = 'x' * $Bytes
    [System.IO.File]::WriteAllText($Path, $content)
}

# New-RawDir creates a directory through the .NET API with the \\?\ prefix,
# which is the only way to make a name Windows would otherwise normalise away:
# trailing dots and trailing spaces are stripped by every Win32 path API, but
# the filesystem itself stores them happily.
function New-RawDir {
    param([string] $Path)
    try {
        [void] [System.IO.Directory]::CreateDirectory("\\?\$Path")
        return $true
    } catch {
        Write-Warning "could not create '$Path': $($_.Exception.Message)"
        return $false
    }
}

function New-Junction {
    param([string] $Link, [string] $Target)
    try {
        [void] (New-Item -ItemType Junction -Path $Link -Target $Target)
        return $true
    } catch {
        Write-Warning "could not create the junction '$Link': $($_.Exception.Message)"
        return $false
    }
}

function New-Symlink {
    param([string] $Link, [string] $Target)
    try {
        [void] (New-Item -ItemType SymbolicLink -Path $Link -Target $Target)
        return $true
    } catch {
        Write-Warning "could not create the symbolic link '$Link'. Symbolic links need Developer Mode or an elevated shell; the rest of the tree is unaffected."
        return $false
    }
}

function Write-Step {
    param([string] $Message)
    Write-Host "  $Message"
}

# ------------------------------------------------------------------- build --

if ($Force -and (Test-Path -LiteralPath $Root)) {
    Write-Host "Removing $Root"
    Remove-Item -LiteralPath $Root -Recurse -Force
}
New-Dir -Path $Root
$Root = (Resolve-Path -LiteralPath $Root).Path
Write-Host "Building the Devpit fixture tree in $Root"

# api: a Node project with another project vendored inside its node_modules.
# The inner project must be pruned with the folder that contains it and never
# listed as a row of its own.
Write-Step 'api: a Node project with a project vendored inside node_modules'
New-Blob "$Root\api\package.json" 40
New-Blob "$Root\api\src\index.js" 200
New-Blob "$Root\api\node_modules\left-pad\index.js" 1000
New-Blob "$Root\api\node_modules\vendored\package.json" 40
New-Blob "$Root\api\node_modules\vendored\node_modules\dep\index.js" 500
New-Blob "$Root\api\node_modules\vendored\dist\bundle.js" 500

# A .git directory, which the walker must never descend into, with a decoy
# node_modules inside it.
New-Blob "$Root\api\.git\index" 64
New-Blob "$Root\api\.git\node_modules\decoy\index.js" 64

# web: node_modules and build output that a package.json vouches for.
Write-Step 'web: node_modules and a marker-gated dist'
New-Blob "$Root\web\package.json" 40
New-Blob "$Root\web\node_modules\react\index.js" 2000
New-Blob "$Root\web\dist\bundle.js" 300

# caseweb: the same build output under a differently cased name. Windows
# cannot hold "dist" and "Dist" in one folder, so the case duplicate needs a
# project of its own. Matching must be case-insensitive all the same.
Write-Step 'caseweb: Dist, to prove matching ignores case'
New-Blob "$Root\caseweb\package.json" 40
New-Blob "$Root\caseweb\Dist\bundle.js" 300

# orphan: node_modules with no package.json beside it. Listed as unverified,
# never pre-ticked.
Write-Step 'orphan: node_modules with no package.json beside it'
New-Blob "$Root\orphan\node_modules\whatever\index.js" 700

# Marker-gated build output for Rust and .NET.
Write-Step 'rust and dotnet: marker-gated build output'
New-Blob "$Root\rust\Cargo.toml" 40
New-Blob "$Root\rust\target\debug\app.exe" 1500
New-Blob "$Root\dotnet\App.csproj" 40
New-Blob "$Root\dotnet\bin\Debug\app.dll" 800
New-Blob "$Root\dotnet\obj\project.assets.json" 200

# notaproject: build, bin and target folders with no marker anywhere near
# them. This is somebody's source code. Nothing here may ever be listed.
Write-Step 'notaproject: junk names with no markers, which must be left alone'
New-Blob "$Root\notaproject\build\important.ps1" 120
New-Blob "$Root\notaproject\bin\run.cmd" 120
New-Blob "$Root\notaproject\target\notes.md" 120

# A project used as the never-touch subject in tests.
Write-Step 'secret: the never-touch subject'
New-Blob "$Root\secret\package.json" 40
New-Blob "$Root\secret\node_modules\dep\index.js" 900

# Unicode and emoji names.
Write-Step 'unicode and emoji project names'
New-Blob "$Root\проект-🚀\package.json" 40
New-Blob "$Root\проект-🚀\node_modules\dep\index.js" 600
New-Blob "$Root\日本語プロジェクト\package.json" 40
New-Blob "$Root\日本語プロジェクト\node_modules\dep\index.js" 600

# A junction inside node_modules pointing at live source, and a junction at
# the top of the tree pointing at a project. Neither may be followed, and the
# bytes behind them must not be counted.
Write-Step 'junctions: one inside node_modules, one at the top level'
New-Blob "$Root\linktarget\package.json" 40
New-Blob "$Root\linktarget\big.js" 5000
[void] (New-Junction "$Root\api\node_modules\linked" "$Root\linktarget")
[void] (New-Junction "$Root\mirror" "$Root\web")

# A symbolic link, where the account is allowed to make one.
Write-Step 'symbolic link (needs Developer Mode or elevation)'
[void] (New-Symlink "$Root\symlinked" "$Root\linktarget")

# A path well past 260 characters, with a real project at the end of it.
Write-Step 'a path over 260 characters'
$deep = $Root
while ($deep.Length -lt 300) {
    $deep = Join-Path $deep ('a' * 30)
}
New-Blob "$deep\package.json" 40
New-Blob "$deep\node_modules\dep\index.js" 123

# Names with trailing dots and trailing spaces, which only the \\?\ prefix can
# create. Every Win32 path API strips them; the filesystem does not.
Write-Step 'names with trailing dots and spaces'
[void] (New-RawDir "$Root\trailing-dot.")
[void] (New-RawDir "$Root\trailing-space ")

# A read-only file inside a node_modules, to prove the sizer counts it and
# that deleting is the clean engine's problem rather than the scanner's.
Write-Step 'a read-only file'
New-Blob "$Root\readonly\package.json" 40
New-Blob "$Root\readonly\node_modules\dep\index.js" 250
Set-ItemProperty -LiteralPath "$Root\readonly\node_modules\dep\index.js" -Name IsReadOnly -Value $true

# A Unity project, which is recognised by what sits beside Library rather than
# by a file inside it.
Write-Step 'unity: Library beside Assets and ProjectSettings'
New-Dir "$Root\unity\Assets"
New-Dir "$Root\unity\ProjectSettings"
New-Blob "$Root\unity\Library\ArtifactDB" 4000
New-Blob "$Root\unity\Temp\build.log" 500

# ------------------------------------------------------------------ large --

if ($Large) {
    Write-Host 'Building the 100,000-file benchmark tree. This takes a while.'
    $bench = "$Root\bench"
    New-Blob "$bench\package.json" 40

    $packages = 500
    $filesPerPackage = 200
    $payload = 'x' * 512

    for ($p = 0; $p -lt $packages; $p++) {
        $dir = "$bench\node_modules\pkg-$p\lib"
        [void] [System.IO.Directory]::CreateDirectory($dir)
        for ($f = 0; $f -lt $filesPerPackage; $f++) {
            [System.IO.File]::WriteAllText("$dir\mod-$f.js", $payload)
        }
        if ($p % 50 -eq 0) {
            Write-Step "  $($p * $filesPerPackage) files"
        }
    }
    Write-Step "$($packages * $filesPerPackage) files written to $bench\node_modules"
}

# ----------------------------------------------------------------- report --

Write-Host ''
Write-Host "Done. The tree is in $Root"
Write-Host 'What the scanner is expected to do with it:'
Write-Host '  list      api\node_modules, web\node_modules, web\dist, caseweb\Dist,'
Write-Host '            orphan\node_modules (unverified), rust\target, dotnet\bin,'
Write-Host '            dotnet\obj, secret\node_modules, unity\Library, unity\Temp'
Write-Host '  never     anything under notaproject, anything under .git,'
Write-Host '            anything reached through a junction or a symbolic link,'
Write-Host '            and the project vendored inside api\node_modules'
Write-Host '  measure   api\node_modules without the bytes behind its junction'
