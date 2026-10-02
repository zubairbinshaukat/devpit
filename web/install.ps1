# Devpit installer  -  https://devpit.zubyr.dev
#
#   irm https://devpit.zubyr.dev/install | iex
#
# Downloads the latest release from GitHub, verifies its SHA256 checksum
# against the checksums.txt published with the release, unpacks devpit.exe
# and devpit-shim.exe into %LOCALAPPDATA%\Programs\devpit, installs the icon
# font for your user and adds that folder to the user PATH. The shim is only
# a file here: Devpit itself copies it as claude.exe, gh.exe... and puts its
# shims folder on PATH, later, and only for a tool that gets a folder rule. Nothing needs admin. Read it all: it
# is short on purpose.
#
# Flags go through a script block, since `iex` cannot pass any:
#
#   & ([scriptblock]::Create((irm https://devpit.zubyr.dev/install))) -NoFont
#
#   -NoFont   skip the icon font (Settings > Icon font can install it later)
#   -NoAgent  never ask about the AI agent skill, and leave it as it is
#
# If Claude Code is on this PC, a fresh install asks once whether AI agents
# may use Devpit (default No). Yes runs `devpit agent install --yes`, which
# writes a skill into Claude Code's skills folder. An update only refreshes
# that skill when Devpit wrote it before, and never adds one; a run with
# nobody at the keyboard never asks and never adds it.

param(
  [switch]$NoFont,
  [switch]$NoAgent
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = 'zubairbinshaukat/devpit'
$Dir = Join-Path $env:LOCALAPPDATA 'Programs\devpit'
$Exe = Join-Path $Dir 'devpit.exe'
$ShimExe = Join-Path $Dir 'devpit-shim.exe'
# Where Devpit puts the shims it makes for Accounts (internal/accounts/shims).
$ShimDir = Join-Path $Dir 'shims'
$Steps = 6
$IsUpdate = $false

# Output helpers. Every step is announced with ">" and confirmed with "+", so
# a failed run shows exactly which step it died on. A step that is allowed to
# come up short without failing the install (only the icon font) ends in "!"
# instead, or "-" when it was skipped.
function Write-Step([int]$N, [string]$Text) {
  Write-Host '  > ' -ForegroundColor Cyan -NoNewline
  Write-Host "[$N/$Steps] $Text" -ForegroundColor Cyan
}
function Write-Ok([int]$N, [string]$Text) {
  Write-Host '  + ' -ForegroundColor Green -NoNewline
  Write-Host "[$N/$Steps] $Text" -ForegroundColor Green
}
function Write-Warn([int]$N, [string]$Text) {
  Write-Host '  ! ' -ForegroundColor Yellow -NoNewline
  Write-Host "[$N/$Steps] $Text" -ForegroundColor Yellow
}
function Write-Skip([int]$N, [string]$Text) {
  Write-Host '  - ' -ForegroundColor DarkGray -NoNewline
  Write-Host "[$N/$Steps] $Text" -ForegroundColor DarkGray
}
function Write-Row([string]$Label, [string]$Value, [ConsoleColor]$Color = 'White') {
  Write-Host ('  - {0,-9}' -f $Label) -ForegroundColor DarkGray -NoNewline
  Write-Host $Value -ForegroundColor $Color
}

# Is a person at the keyboard? Not when input is redirected, the session is
# not interactive, or PowerShell was started with -NonInteractive.
function Test-Interactive {
  if (-not [Environment]::UserInteractive) { return $false }
  try { if ([Console]::IsInputRedirected) { return $false } } catch { return $false }
  foreach ($a in [Environment]::GetCommandLineArgs()) { if ($a -match '^-noni') { return $false } }
  return $true
}

# What to do about the AI agent skill, from `devpit agent status --json`:
# 'ask' (a fresh install with a person at the keyboard), 'refresh' (Devpit's
# own older skill is there, so the person said yes before), or 'skip:<why>'.
# Nothing here ever adds the skill without a yes.
function Get-AgentAction($Status, [bool]$IsUpdate, [bool]$NoAgent, [bool]$Interactive) {
  if ($NoAgent) { return 'skip:flag' }
  if (-not $Status -or -not $Status.claude_code) { return 'skip:no-claude' }
  $ours = @($Status.targets | Where-Object { @('update', 'up to date', 'newer') -contains $_.state }).Count -gt 0
  if ($ours) {
    if ($Status.can_install) { return 'refresh' }
    return 'skip:current'
  }
  if ($IsUpdate) { return 'skip:update' }
  if (-not $Interactive) { return 'skip:non-interactive' }
  if ($Status.state -eq 'a different devpit skill is in the way') { return 'skip:foreign' }
  if (-not $Status.can_install) { return 'skip:nothing' }
  return 'ask'
}

# Only "y" or "yes" is a yes; Enter, anything else and no answer are No.
function Test-Yes([string]$Answer) {
  if ($null -eq $Answer) { return $false }
  return ($Answer.Trim() -match '^(y|yes)$')
}

# Runs devpit.exe and returns its output lines and exit code, without letting
# Windows PowerShell 5.1 turn a line on stderr into a terminating error, and
# reading its UTF-8 output as UTF-8.
function Invoke-Devpit([string[]]$Arguments, [switch]$StdoutOnly) {
  $prevEap = $ErrorActionPreference
  $prevEnc = [Console]::OutputEncoding
  try {
    try { [Console]::OutputEncoding = New-Object Text.UTF8Encoding $false } catch { }
    $ErrorActionPreference = 'Continue'
    if ($StdoutOnly) {
      $out = @(& $Exe @Arguments 2>$null | ForEach-Object { "$_" })
    } else {
      $out = @(& $Exe @Arguments 2>&1 | ForEach-Object { "$_" })
    }
    return @{ Out = $out; Code = $LASTEXITCODE }
  } catch {
    return @{ Out = @($_.Exception.Message); Code = 1 }
  } finally {
    $ErrorActionPreference = $prevEap
    try { [Console]::OutputEncoding = $prevEnc } catch { }
  }
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
Write-Host '  A project by Zubair bin Shaukat  -  devpit.zubyr.dev' -ForegroundColor DarkGray
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
      # Take the icon font and its Windows Terminal fallback out first, while
      # devpit.exe is still here to do it. An older build without the font
      # command just says so, and the removal carries on regardless.
      try { & $Exe font remove 2>$null | Out-Null } catch { }
      # The same for the AI agent skill: `agent remove` takes out only the
      # SKILL.md files carrying Devpit's marker, never anyone else's skill.
      # An older build without the command just fails quietly here.
      $agentOut = Invoke-Devpit @('agent', 'remove', '--yes')
      if ($agentOut.Code -eq 0 -and ($agentOut.Out -match 'Removed').Count -gt 0) {
        Write-Host "  + Devpit's AI agent skill removed from Claude Code." -ForegroundColor Green
      }
      # This takes the Accounts shims with it (they live in $Dir\shims). A
      # shim that is running right now (a claude session, say) cannot be
      # deleted, so say what to close rather than leave half a folder.
      try {
        Remove-Item -Recurse -Force $Dir
      } catch {
        throw "Could not remove $Dir. Close anything started through Devpit's shims (claude, gh, vercel...), then run this again. $($_.Exception.Message)"
      }
      # Devpit's folder, and the shims folder Devpit itself put in front of
      # PATH; every other PATH entry stays as it is.
      $path = [Environment]::GetEnvironmentVariable('Path', 'User')
      $clean = ($path -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ne $Dir -and $_.TrimEnd('\') -ne $ShimDir }) -join ';'
      [Environment]::SetEnvironmentVariable('Path', $clean, 'User')
      Write-Host '  + Devpit and its icon font removed. Your settings in %APPDATA%\devpit were left alone.' -ForegroundColor Green
      # Account folders hold sign-ins: uninstall never removes them.
      Write-Host '  + Your account folders in %USERPROFILE%\.devpit were left alone: they hold your sign-ins.' -ForegroundColor Green
      Write-Host '    Without Devpit, every tool uses its own default sign-in in every folder again.' -ForegroundColor DarkGray
      return
    }
    'U' { $IsUpdate = $true }
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

  Write-Step 4 "Installing devpit.exe and devpit-shim.exe to $Dir..."
  New-Item -ItemType Directory -Force -Path $Dir | Out-Null
  Expand-Archive -Path $zip -DestinationPath $tmp -Force
  $built = Get-ChildItem -Path $tmp -Recurse -Filter 'devpit.exe' | Select-Object -First 1
  if (-not $built) { throw 'devpit.exe was not inside the archive.' }
  Copy-Item -Path $built.FullName -Destination $Exe -Force
  # The Accounts shim goes next to devpit.exe and nowhere else: no shims
  # are made and PATH is not touched for them here. Shims Devpit made
  # earlier are refreshed by Devpit itself, which knows which tools have
  # rules. A release from before Accounts has no shim; that is fine.
  $shimBuilt = Get-ChildItem -Path $tmp -Recurse -Filter 'devpit-shim.exe' | Select-Object -First 1
  if ($shimBuilt) { Copy-Item -Path $shimBuilt.FullName -Destination $ShimExe -Force }
  Write-Ok 4 "Installed $Exe"
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

# The icon font, so Devpit's icons work on first launch. It must never fail
# the install: devpit.exe is already in place. `devpit font install --quiet`
# exits 0 with a one-line note when offline or blocked by policy, and prints
# one line starting with a check mark on success; anything else (a non-zero
# exit, or devpit.exe not running at all) is caught and shown as a warning.
$fontOk = $false
if ($NoFont) {
  Write-Skip 5 'Skipped icon font (-NoFont)'
} else {
  Write-Step 5 'Installing icon font...'
  $prevEap = $ErrorActionPreference
  $prevEnc = [Console]::OutputEncoding
  try {
    # devpit writes UTF-8, but PowerShell decodes a native program's output
    # with [Console]::OutputEncoding, which on Windows PowerShell 5.1 is
    # usually the OEM code page. And with 'Stop', 5.1 turns the first line a
    # native program writes to stderr into a terminating error even when it
    # is redirected, so the real message would be lost.
    try { [Console]::OutputEncoding = New-Object Text.UTF8Encoding $false } catch { }
    $ErrorActionPreference = 'Continue'
    $fontOut = @(& $Exe font install --quiet 2>&1 | ForEach-Object { "$_" } | Where-Object { $_.Trim() })
    $fontCode = $LASTEXITCODE
    $ErrorActionPreference = $prevEap

    $line = ''
    if ($fontOut.Count -gt 0) { $line = $fontOut[-1].Trim() }
    # The first rune is devpit's marker; the rest is the message. The marker
    # is swapped for this script's own, and the one non-ASCII character in
    # the messages for ">", since a legacy console font may draw neither.
    $mark = ''
    $text = $line
    # Only split off a marker devpit actually prints; an error message from
    # a failed run starts with a letter, and must keep it.
    if ($line.Length -gt 1 -and ('!', [string][char]0x2714, [string][char]0x2022) -contains $line.Substring(0, 1)) {
      $mark = $line.Substring(0, 1)
      $text = $line.Substring(1).Trim()
    }
    $text = $text.Replace([string][char]0x203A, '>')
    if ($fontCode -ne 0) {
      if (-not $text) { $text = "devpit exited with code $fontCode" }
      Write-Warn 5 "Icon font not installed: $text. Settings > Icon font can try again."
    } elseif ($mark -eq [string][char]0x2714) {
      Write-Ok 5 $text
      $fontOk = $true
    } elseif ($mark -eq '!') {
      Write-Warn 5 $text
    } else {
      Write-Skip 5 $text
    }
  } catch {
    Write-Warn 5 "Icon font not installed: $($_.Exception.Message). Settings > Icon font can try again."
  } finally {
    $ErrorActionPreference = $prevEap
    try { [Console]::OutputEncoding = $prevEnc } catch { }
  }
}

# The AI agent skill. Asked only on a fresh install, only when Claude Code is
# here and the skill is missing, only with a person at the keyboard; the
# default is No. An update refreshes Devpit's own skill when it is already
# there and never adds one. Like the font, it never fails the install.
$agentOk = $false
$agentStatus = $null
$statusRun = Invoke-Devpit @('agent', 'status', '--json') -StdoutOnly
if ($statusRun.Code -eq 0) {
  try { $agentStatus = ($statusRun.Out -join "`n") | ConvertFrom-Json } catch { $agentStatus = $null }
}
$agentAction = Get-AgentAction $agentStatus $IsUpdate $NoAgent.IsPresent (Test-Interactive)
if ($agentAction -eq 'ask') {
  Write-Step 6 'AI agents...'
  Write-Host '    The skill tells Claude Code how to check which account a tool uses here, where' -ForegroundColor DarkGray
  Write-Host '    the docs are, and to ask you before changing anything. It never reads your logins.' -ForegroundColor DarkGray
  $answer = Read-Host '  Let AI agents (Claude Code) use Devpit? [y/N]'
  if (-not (Test-Yes $answer)) { $agentAction = 'skip:no' }
}
switch -Wildcard ($agentAction) {
  'ask' { $agentRun = Invoke-Devpit @('agent', 'install', '--yes') }
  'refresh' { $agentRun = Invoke-Devpit @('agent', 'install', '--yes') }
  default { $agentRun = $null }
}
if ($agentRun) {
  if ($agentRun.Code -eq 0) {
    $agentOk = $true
    if ($agentAction -eq 'refresh') { Write-Ok 6 "Updated Devpit's AI agent skill for Claude Code" } else { Write-Ok 6 'AI agent skill added to Claude Code' }
  } else {
    $why = ($agentRun.Out | Where-Object { $_.Trim() } | Select-Object -Last 1)
    Write-Warn 6 "AI agent skill not written: $why. Run devpit agent install to try again."
  }
} else {
  switch ($agentAction) {
    'skip:flag' { Write-Skip 6 'Skipped the AI agent skill (-NoAgent)' }
    'skip:no-claude' { Write-Skip 6 'Claude Code not found; no AI agent skill' }
    'skip:current' { Write-Ok 6 "Devpit's AI agent skill is up to date" }
    'skip:foreign' { Write-Skip 6 'A devpit skill Devpit did not write is in Claude Code; left alone' }
    default { Write-Skip 6 'No AI agent skill. Settings or devpit agent install can add it later.' }
  }
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
Write-Row 'Author:' 'Zubair bin Shaukat  -  https://zubyr.dev'
Write-Host ''
Write-Host '  To start:' -ForegroundColor White
Write-Host '    devpit' -ForegroundColor Magenta
Write-Host ''
Write-Host '  [i] Other terminal windows that were already open need a restart, or run:' -ForegroundColor Cyan
Write-Host "      & `"$Exe`"" -ForegroundColor White
if ($fontOk) {
  Write-Host "  [i] Reopen Windows Terminal to see Devpit's icons." -ForegroundColor Cyan
} else {
  Write-Host '  [i] Settings > Icon font installs the icons later.' -ForegroundColor Cyan
}
if ($agentOk) {
  Write-Host '  [i] New Claude Code sessions pick up the Devpit skill; devpit agent remove takes it out.' -ForegroundColor Cyan
}
Write-Host ''
