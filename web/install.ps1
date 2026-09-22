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

function Write-Step([string]$Text) { Write-Host "  $Text" -ForegroundColor Cyan }
function Write-Ok([string]$Text) { Write-Host "  $Text" -ForegroundColor Green }

Write-Host ''
Write-Host '  Devpit installer' -ForegroundColor White

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
      Write-Ok 'Devpit removed. Your settings in %APPDATA%\devpit were left alone.'
      return
    }
    'U' { }
    default { Write-Host '  Cancelled.'; return }
  }
}

Write-Step 'Looking up the latest release...'
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
Write-Step "Latest release: v$version (windows-$arch)"

$tmp = Join-Path ([IO.Path]::GetTempPath()) "devpit-install-$([IO.Path]::GetRandomFileName())"
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  $zip = Join-Path $tmp $zipName
  Write-Step "Downloading $zipName..."
  Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $zip -Headers @{ 'User-Agent' = 'devpit-installer' }
  $sumsText = (Invoke-WebRequest -Uri $sums.browser_download_url -Headers @{ 'User-Agent' = 'devpit-installer' }).Content

  Write-Step 'Verifying SHA256 checksum...'
  $expected = ($sumsText -split "`n" | Where-Object { $_ -match [regex]::Escape($zipName) } | Select-Object -First 1)
  if (-not $expected) { throw "checksums.txt has no entry for $zipName." }
  $expected = ($expected -split '\s+')[0].ToLowerInvariant()
  $actual = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLowerInvariant()
  if ($expected -ne $actual) { throw "Checksum mismatch for $zipName. Expected $expected, got $actual. Nothing was installed." }
  Write-Ok 'Checksum OK'

  Write-Step "Installing to $Dir..."
  New-Item -ItemType Directory -Force -Path $Dir | Out-Null
  Expand-Archive -Path $zip -DestinationPath $tmp -Force
  $built = Get-ChildItem -Path $tmp -Recurse -Filter 'devpit.exe' | Select-Object -First 1
  if (-not $built) { throw 'devpit.exe was not inside the archive.' }
  Copy-Item -Path $built.FullName -Destination $Exe -Force
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $Dir) {
  [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ';' + $Dir), 'User')
  $env:Path = "$env:Path;$Dir"
  Write-Step 'Added Devpit to your user PATH (new terminals pick it up).'
}

Write-Host ''
Write-Ok "Devpit v$version installed."
Write-Host '  Type  devpit  to start. Settings > Icon font installs the optional Nerd Font icons.' -ForegroundColor White
Write-Host ''
