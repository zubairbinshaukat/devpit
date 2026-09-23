# Devpit installer  -  https://devpit.zubyr.dev
#
#   irm https://devpit.zubyr.dev/install | iex
#
# Downloads the latest release from GitHub, verifies its SHA256 checksum
# against the checksums.txt published with the release, unpacks devpit.exe
# into %LOCALAPPDATA%\Programs\devpit and adds that folder to the user PATH.
# Nothing needs admin. Read it all: it is short on purpose.

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = 'zubairbinshaukat/devpit'
$Dir = Join-Path $env:LOCALAPPDATA 'Programs\devpit'
$Exe = Join-Path $Dir 'devpit.exe'
$Steps = 4

# Output helpers. Every step is announced with ">" and confirmed with "+", so
# a failed run shows exactly which step it died on.
function Write-Step([int]$N, [string]$Text) {
  Write-Host '  > ' -ForegroundColor Cyan -NoNewline
  Write-Host "[$N/$Steps] $Text" -ForegroundColor Cyan
}
function Write-Ok([int]$N, [string]$Text) {
  Write-Host '  + ' -ForegroundColor Green -NoNewline
  Write-Host "[$N/$Steps] $Text" -ForegroundColor Green
}
function Write-Row([string]$Label, [string]$Value, [ConsoleColor]$Color = 'White') {
  Write-Host ('  - {0,-9}' -f $Label) -ForegroundColor DarkGray -NoNewline
  Write-Host $Value -ForegroundColor $Color
}

# The same wordmark the app draws on its home screen (assets/logo.txt). Every
# glyph is a block or box-drawing rune, which every console font can draw.
$Logo = @(
  '██████╗ ███████╗██╗   ██╗██████╗ ██╗████████╗',
  '██╔══██╗██╔════╝██║   ██║██╔══██╗██║╚══██╔══╝',
  '██║  ██║█████╗  ██║   ██║██████╔╝██║   ██║   ',
  '██║  ██║██╔══╝  ╚██╗ ██╔╝██╔═══╝ ██║   ██║   ',
  '██████╔╝███████╗ ╚████╔╝ ██║     ██║   ██║   ',
  '╚═════╝ ╚══════╝  ╚═══╝  ╚═╝     ╚═╝   ╚═╝   '
)
Write-Host ''
foreach ($line in $Logo) { Write-Host "  $line" -ForegroundColor Cyan }
Write-Host '  A project by Zubair Bin Shaukat  -  devpit.zubyr.dev' -ForegroundColor DarkGray
Write-Host ''

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'ARM64' { 'arm64' }
  'AMD64' { 'amd64' }
  default { throw "Unsupported processor architecture: $env:PROCESSOR_ARCHITECTURE" }
}

# Already installed? Offer update / uninstall / cancel.
if (Test-Path $Exe) {
  $have = (& $Exe version 2>$null | Select-Object -First 1)
  Write-Host "  Devpit is already installed ($have)." -ForegroundColor Yellow
  $choice = Read-Host '  [U]pdate, [R]emove or [C]ancel'
  switch ($choice.ToUpperInvariant()) {
    'R' {
      Remove-Item -Recurse -Force $Dir
      $path = [Environment]::GetEnvironmentVariable('Path', 'User')
      $clean = ($path -split ';' | Where-Object { $_ -and $_ -ne $Dir }) -join ';'
      [Environment]::SetEnvironmentVariable('Path', $clean, 'User')
      Write-Host '  + Devpit removed. Your settings in %APPDATA%\devpit were left alone.' -ForegroundColor Green
      return
    }
    'U' { }
    default { Write-Host '  Cancelled.'; return }
  }
}

Write-Step 1 'Checking environment and resolving the latest version...'
$headers = @{ 'User-Agent' = 'devpit-installer'; 'Accept' = 'application/vnd.github+json' }
try {
  $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $headers
} catch {
  throw "No release found yet at https://github.com/$Repo/releases. Build from source: git clone https://github.com/$Repo && cd devpit && go build -o devpit.exe ."
}
$version = $release.tag_name.TrimStart('v')
$zipName = "devpit_${version}_windows_${arch}.zip"
$asset = $release.assets | Where-Object { $_.name -eq $zipName } | Select-Object -First 1
$sums = $release.assets | Where-Object { $_.name -eq 'checksums.txt' } | Select-Object -First 1
if (-not $asset -or -not $sums) { throw "Release $($release.tag_name) has no $zipName or checksums.txt." }
Write-Ok 1 "Environment ready (Windows $arch) - v$version"

$tmp = Join-Path ([IO.Path]::GetTempPath()) "devpit-install-$([IO.Path]::GetRandomFileName())"
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  $zip = Join-Path $tmp $zipName
  Write-Step 2 "Downloading $zipName..."
  Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $zip -Headers @{ 'User-Agent' = 'devpit-installer' }
  # GitHub serves checksums.txt as application/octet-stream, so PowerShell 7
  # hands back a byte array rather than a string. Decode it ourselves.
  $sumsRaw = (Invoke-WebRequest -Uri $sums.browser_download_url -Headers @{ 'User-Agent' = 'devpit-installer' }).Content
  if ($sumsRaw -is [byte[]]) { $sumsText = [Text.Encoding]::UTF8.GetString($sumsRaw) } else { $sumsText = [string]$sumsRaw }
  $size = '{0:N1} MB' -f ((Get-Item $zip).Length / 1MB)
  Write-Ok 2 "Downloaded $zipName ($size)"

  Write-Step 3 'Verifying SHA256 checksum...'
  $expected = ($sumsText -split "`n" | Where-Object { $_ -match [regex]::Escape($zipName) } | Select-Object -First 1)
  if (-not $expected) { throw "checksums.txt has no entry for $zipName." }
  $expected = ($expected -split '\s+')[0].ToLowerInvariant()
  $actual = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLowerInvariant()
  if ($expected -ne $actual) { throw "Checksum mismatch for $zipName. Expected $expected, got $actual. Nothing was installed." }
  Write-Ok 3 'Checksum verified against the release manifest'

  Write-Step 4 "Installing devpit.exe to $Dir..."
  New-Item -ItemType Directory -Force -Path $Dir | Out-Null
  Expand-Archive -Path $zip -DestinationPath $tmp -Force
  $built = Get-ChildItem -Path $tmp -Recurse -Filter 'devpit.exe' | Select-Object -First 1
  if (-not $built) { throw 'devpit.exe was not inside the archive.' }
  Copy-Item -Path $built.FullName -Destination $Exe -Force
  Write-Ok 4 "Installed $Exe"
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

$pathNote = 'already on your user PATH'
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $Dir) {
  [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ';' + $Dir), 'User')
  $env:Path = "$env:Path;$Dir"
  $pathNote = 'added to your user PATH'
}

Write-Host ''
Write-Host "  + Devpit v$version installed." -ForegroundColor Green
Write-Host ''
Write-Row 'Binary:' $Exe
Write-Row 'Version:' "v$version"
Write-Row 'Shell:' $pathNote Magenta
Write-Row 'Author:' 'Zubair Bin Shaukat  -  https://zubyr.dev'
Write-Host ''
Write-Host '  To start:' -ForegroundColor White
Write-Host '    devpit' -ForegroundColor Magenta
Write-Host ''
Write-Host '  [i] Other terminal windows that were already open need a restart, or run:' -ForegroundColor Cyan
Write-Host "      & `"$Exe`"" -ForegroundColor White
Write-Host '  [i] Settings > Icon font installs the optional Nerd Font icons.' -ForegroundColor Cyan
Write-Host ''
