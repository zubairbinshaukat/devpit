<p align="center">
  <a href="https://devpit.zubyr.dev"><img src="web/logo.png" alt="Devpit logo" width="120"></a>
</p>

<h1 align="center">Devpit</h1>

<p align="center">
  <b>A pit stop for your dev machine.</b><br>
  Free disk space, fix stuck ports, and keep your tools up to date, from one terminal menu.
</p>

<p align="center">
  <a href="https://github.com/zubairbinshaukat/devpit/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/zubairbinshaukat/devpit/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/zubairbinshaukat/devpit/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/zubairbinshaukat/devpit?include_prereleases&label=release&color=7FDBCA"></a>
  <a href="LICENSE"><img alt="MIT License" src="https://img.shields.io/badge/license-MIT-7FDBCA.svg"></a>
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white">
  <img alt="Windows" src="https://img.shields.io/badge/Windows-10%20%7C%2011-0078D4?logo=windows&logoColor=white">
  <img alt="Bubble Tea" src="https://img.shields.io/badge/TUI-Bubble%20Tea%20v2-FF75B5">
  <img alt="No cgo" src="https://img.shields.io/badge/cgo-none-2ea44f">
</p>

<p align="center">
  <a href="https://devpit.zubyr.dev">Website</a> ·
  <a href="#install">Install</a> ·
  <a href="#what-it-does">Features</a> ·
  <a href="#safety">Safety</a> ·
  <a href="docs/safety.md">Safety rules</a> ·
  <a href="PRIVACY.md">Privacy</a> ·
  <a href="#author">Author</a>
</p>

---

Devpit (or "Devpit CLI") is a free, open-source terminal app for Windows. One
menu finds `node_modules`, build folders, package caches, Docker leftovers and
old app versions, shows you what is safe, and clears gigabytes in one keystroke.
It also frees busy ports, installs and updates developer tools, and sets up Git
and SSH on a fresh machine.

Windows first. Go, Bubble Tea, single executable, no runtime to install.

> **Beta.** Every section works end to end and the delete engine is covered by
> safety tests, but there has been no tagged release yet. Nothing is removed
> without a preview and a confirmation, Safe items are renamed before a byte is
> removed so an interrupted delete can be finished rather than half-done, and
> riskier items go to the Recycle Bin. Keep your backups anyway.

## Install

One line in PowerShell. It downloads the latest release from GitHub, verifies
the SHA256 checksum and adds Devpit to your user PATH. No admin needed.

```powershell
irm https://devpit.zubyr.dev/install | iex
```

Then type `devpit`.

Other ways:

- Download `devpit.exe` from the [latest release](https://github.com/zubairbinshaukat/devpit/releases/latest) (x64 and ARM64).
- Build from source with Go 1.26 or newer, no C toolchain needed:

  ```powershell
  git clone https://github.com/zubairbinshaukat/devpit
  cd devpit
  go build -o devpit.exe .
  .\devpit.exe
  ```

The one-liner and the release download start working with the first tagged
release. Until then, build from source.

## What it does

| Section | What it does |
| --- | --- |
| Free Up Disk Space | One scan finds `node_modules`, build folders, package caches, Docker leftovers, old Scoop versions and Windows temp, labels each by risk, and deletes only what you tick |
| Fix Stuck Ports & Apps | Frees a busy port in two keystrokes; lists busy dev ports and stuck Node processes, with a kill-tree option for npm, pnpm, yarn and node |
| Install Developer Apps | Installs from a catalog through Scoop, winget or Chocolatey |
| Update Everything | Updates through every package manager it finds, in one pass |
| Network Tools | Local and public IP, ping, DNS flush |
| Git & SSH Setup | Git identity and SSH key generation, copy the public key |
| Devpit Settings | Theme, icon tier, Nerd Font install, never-touch list, dev port list |

## Safety

These rules are not preferences. They are enforced in the code and every one
is pinned by a test. The full table is in [docs/safety.md](docs/safety.md).

- Nothing is deleted without a preview and an explicit confirmation.
- The default answer on every confirmation is No. The confirm dialog has no
  way to be built with a Yes default.
- Careful items need you to type `DELETE`.
- Projects you touched in the last 7 days are never pre-ticked.
- A folder is only junk when its marker file sits beside it (`package.json`
  for `node_modules`, `Cargo.toml` for `target`), and the marker is checked
  again right before deleting.
- Junctions, symlinks and other reparse points are treated as leaves and never
  followed. This is the bug that made other cleaners delete real source code.
- Drive roots, the Windows directory, network paths and your never-touch list
  are refused by both the scanner and the deleter.
- Safe items are renamed to a tombstone before removal, so an interrupted
  delete leaves a folder that a later run finishes, never a half-emptied one.
- Docker volumes are never touched.
- Every confirmation tells you how to get the thing back (`npm install`).

## Privacy

Nothing leaves your machine unless you turn usage stats on, and they are off
until you do. `DEVPIT_NO_TELEMETRY=1` and `DO_NOT_TRACK=1` always win. See
[PRIVACY.md](PRIVACY.md) for exactly what would be sent.

## Usage

```
devpit                 open the app
devpit version         print the version
devpit --ascii         force the plain ASCII icon tier
```

The other subcommands (`clean`, `ports`, `update`, `font`, `settings`) are
reserved and tell you which milestone fills them in.

### Icons and themes

Devpit has three icon tiers and picks one automatically. Windows Terminal ships
a font with no Nerd Font glyphs, and no program can ask a terminal what fonts it
has, so the rich tier is opt-in: Settings can install the icon font for you and
then asks whether the glyphs render before turning them on. Themes: auto, dark,
light, aqua, blue, rose and mono. See [docs/icons.md](docs/icons.md).

## Development

```powershell
task check        # build + vet + lint + test
task hooks        # run `task check` automatically before every push
go run .          # run from source
```

Go 1.26+, `golangci-lint`, `gofumpt` and `task` (all available through Scoop).
Engine packages never import Bubble Tea; screens bridge them with channels.
See [docs/architecture.md](docs/architecture.md) and
[CONTRIBUTING.md](CONTRIBUTING.md).

## Author

<a href="https://zubyr.dev"><img src="web/author-avatar.webp" alt="Zubair bin Shaukat" width="88" align="left" style="border-radius:50%"></a>

**Zubair bin Shaukat** (zubyr), software engineer from Lahore, Pakistan.
Devpit is the cleanup he wanted for his own Windows machine, so he built it and
gave it away.

[Portfolio](https://zubyr.dev) · [GitHub](https://github.com/zubairbinshaukat) · [LinkedIn](https://www.linkedin.com/in/zubairbinshaukat) · [X](https://x.com/zubairbinshaukt)

<br clear="left">

## License

[MIT](LICENSE). Icon tables are vendored from lazygit (MIT), see [NOTICE](NOTICE).
