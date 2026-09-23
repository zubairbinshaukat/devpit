# Architecture

Devpit is one Go binary. No runtime, no cgo, no DLLs to ship.

## Layout

```
main.go              calls cmd/devpit.Execute
cmd/devpit/          cobra command tree; no args opens the TUI; worker.go is
                      the hidden --elevated-worker entry point
internal/app/         root tea.Model, screen router, global keys
internal/ui/
  theme/              palette and package-level Lip Gloss styles
  icons/              the three glyph tiers and the rule that picks one
  uictx/              render context and navigation messages
  components/         header, footer, menu, confirm, summary, restable,
                      progress, pathpicker
  screens/            clean, firstrun, gitssh, home, install, network,
                      ports, settings, update
internal/config/       TOML settings, atomic save, corrupt-file recovery
internal/winapi/       thin Win32 wrappers, one _windows.go per stub
internal/version/      build identity, set by ldflags
internal/scan/         the read-only engine: walk, size, rule-match, verify,
                      cache, active-project detection
internal/clean/        the delete engine: pre-flight, tombstone, sweep,
                      Recycle Bin, locked-file reporting, command cleanups
internal/ports/        TCP/UDP enumeration, protected-process rules, kill
internal/tools/        manager/app detection and catalog (scoop, winget,
                      choco, npm)
internal/fonts/        Nerd Font download, checksum, install, Terminal patch
internal/wt/           Windows Terminal settings.json patch/restore
internal/network/      ping, DNS flush, local/public IP
internal/gitssh/       SSH keygen, git config, clipboard (OSC 52)
internal/elevate/      the elevated worker: protocol, client, server, whitelist
internal/telemetry/    the opt-in usage-stats client: one POST after a
                      cleanup, counts and rule names only
internal/selfupdate/   once-a-day look at GitHub's latest release, cached on
                      disk; names the upgrade command for this install
internal/about/        author, links and licence, read by every place that
                      prints them so the byline cannot drift
```

`internal/` because none of it is meant to be imported by anyone else. No
`pkg/`.

## The engine/UI split

Every package above `internal/ui` and `internal/app` — `scan`, `clean`,
`ports`, `tools`, `fonts`, `wt`, `network`, `gitssh`, `elevate` — is an
**engine package**: it does not import `github.com/charmbracelet/bubbletea`,
does not know what a screen is, and can be unit-tested with nothing but the
filesystem and (on Windows) real Win32 calls. `internal/elevate` is the
extreme case of this rule: it is a whole separate process, and the one line
its package doc leads with is that it has no `internal/ui` or `internal/app`
import anywhere in it (see [safety.md](safety.md) rule 18).

A screen is the only thing allowed to know about both a `tea.Cmd` and an
engine type. The shape repeats across `scan`, `clean`, `ports` and the
manager steps in `tools`: the engine exposes a plain Go function that takes a
channel or a callback, and the screen wraps it in a `tea.Cmd` that reads from
that channel and turns what it sees into messages. `scan.Batcher` /
`scan.BatcherFunc` is the canonical example: it drains an `Item` channel,
groups what arrives into a `Batch` no more often than every 150 ms, and calls
a plain `emit func(Batch)` — the `clean` screen's `engine.go` is what turns
that into `tea.Cmd`s producing `scanBatchMsg`. The same channel → batch →
`tea.Cmd` shape carries scan progress, delete progress and port-kill
confirmation, so a screen's engine.go file is always the shortest path to
understanding what it streams.

## The render loop

Bubble Tea v2's Elm architecture, with one addition: a screen stack.

```
tea.Program
  └── app.Model                     root tea.Model
        ├── Router                  stack of uictx.Screen
        ├── header.Model            app name, versions, free space
        ├── footer.Model            key hints from bubbles/help
        └── GlobalKeyMap            ctrl+c, esc, ?, q
```

`app.Model.Update` handles the messages nobody else should: window size,
background colour, the global keys, and the navigation messages. Everything
else falls through to the screen on top of the stack.

Screens implement `uictx.Screen`. They are **values**, not pointers:
`Update` returns the next screen rather than mutating the receiver. That is
what makes the stack honest — pushing a screen and popping back to it restores
exactly the state it had, cursor included, with no bookkeeping.

Screens never import `internal/app`. They share `uictx.Context` (theme, icons,
config, size) and navigate by returning commands that emit
`uictx.PushScreenMsg`, `PopScreenMsg` or `ReplaceScreenMsg`, which the router
acts on. That keeps the dependency arrow pointing one way and leaves no cycle
to break.

`app.Model.View` returns a `tea.View` with `AltScreen` and the window title
set, and a body of exactly `height - header - footer` rows, so the footer never
floats. Below 80x24 the whole frame is replaced by a resize notice.

## Startup

The rule: **the first frame costs nothing.**

`app.New` builds the model and does no I/O at all. `Init` returns exactly two
commands, and Bubble Tea runs both on their own goroutines after the frame is
already on screen:

- `tea.RequestBackgroundColor`, which comes back as `tea.BackgroundColorMsg`
  and decides light or dark;
- `header.DetectDiskCmd`, the free-space probe.

The only file read on the way in is `config.toml`, done by `cmd/devpit`
before the program starts. No process is executed, no socket opened and no
tool detected anywhere on that path. When tool detection arrives in milestone 1
it follows the same shape: a command, memoized, never synchronous.

## Theme and icons

`internal/ui/theme` builds two complete `Theme` values at program load, one for
a light background and one for a dark one, and hands out a pointer to the right
one. Nothing rebuilds a `lipgloss.Style` during a render; a style is a value and
copying one is free.

`internal/ui/icons` has three tiers and one resolution rule. A `Set` is a plain
struct of glyphs, so a screen writes `ctx.Icons.Trash` and gets the empty string
in tiers that have no such glyph. See [icons.md](icons.md).

## Configuration

One TOML file, `%APPDATA%\devpit\config.toml`, schema version 1, forward-only
migrations. Saves are atomic: temp file in the same directory, then a rename
over the target, so a half-written file is never observable. A file that will
not parse is renamed to `config.toml.broken-<unix>` and the user is told once.

`DEVPIT_CONFIG_DIR` and `DEVPIT_CACHE_DIR` relocate both directories; tests
use them with `t.TempDir()` so no test can touch a real machine's settings.

Config is the only mutable state in the program. Screens do not write it: they
emit `uictx.ConfigChangedMsg`, and the router stores it, rebuilds the theme and
icon set from it, and saves it.

## Platform code

Every Win32 call lives in a `*_windows.go` file with a matching `*_other.go`
stub returning `winapi.ErrUnsupported`. `GOOS=linux go vet ./...` passes, the
pure-logic tests run on the Linux CI runner, and the day Linux support is worth
doing the stubs are the list of what has to be written.

`golang.org/x/sys/windows`, never cgo.

## The engine packages

| Package | Purpose | Entry points | Windows-only files |
| --- | --- | --- | --- |
| `scan` | Walk projects, size junk, match rules, cache results, verify before delete | `Run`, `Batcher`/`BatcherFunc`, `Verify`, `Rule.HasMarker`, `LoadCache`/`SaveCache`, `IsActive` | `attrs_windows.go` (reparse/cloud attributes), `junction_windows_test.go`, `names_windows_test.go` |
| `clean` | Pre-flight, tombstone rename, bounded parallel delete, Recycle Bin, command-based cleanups, sweep | `Preflight`, `Run`, `RunCommand`, `FindTombstones`, `IsTombstone` | `recyclebin_windows.go` (`SHFileOperationW`), `clean_windows_test.go`, `sys_windows.go` |
| `ports` | Enumerate TCP/UDP owners, decide what may never be killed, kill | `Busy`, `Protected`, `Kill`, `KillTree`, `OffersTree` | `process_windows.go`, `table_windows.go`, `kill_windows.go` |
| `tools` | Detect installed dev tools and package managers, run manager steps | `Get`, `All`, `RunStep` | none — shells out via `os/exec` on every OS |
| `tools/catalog` | The app catalog (`apps.toml`) embedded and parsed | `Load`, `ByCategory` | none |
| `tools/managers` | One file per manager (scoop, winget, choco, npm): command building and output parsing | `Commands`, `ParseList`, `ParseOutdated` | none — the managers themselves are Windows tools, but parsing recorded output needs no Win32 calls |
| `fonts` | Download, verify, install the Nerd Font, patch it into Windows Terminal | `Install`, `Remove` | `install_windows.go` (per-user font registration via GDI) |
| `wt` | Read/patch/restore Windows Terminal's `settings.json` | `Patch`, `Restore`, `FindSettings` | none — pure JSONC editing, the file itself is Windows-only in practice |
| `network` | Ping, DNS flush, local/public IP | `Ping`, `FlushDNS`, `LocalIPs`, `PublicIP` | `dns_test.go`'s Windows path (`ipconfig /flushdns`) |
| `gitssh` | SSH keygen with overwrite protection, git config read/write, OSC 52 clipboard | `Keygen`, `GitConfig`, `SetGitConfig`, `Copy` | `clipboard_windows.go` |
| `elevate` | The elevated worker: wire protocol, client, server loop, remove-path whitelist | `Launch`, `Serve`, `Client.Exec`, `Client.Remove` | `launch_windows.go` (`ShellExecuteExW`), `pipe_windows.go`, `serve_windows.go`, `shellexecute_windows.go`, `iselevated_windows.go` |

## The elevated worker protocol

`internal/elevate` is how Devpit ever touches something that needs
administrator rights (Chocolatey, some `%WINDIR%\Temp` cleanup) without the
TUI itself running elevated — see [safety.md](safety.md) rule 18. The shape:

1. The TUI creates a named pipe and launches `devpit --elevated-worker --pipe
   <name>` through `ShellExecuteExW`'s `"runas"` verb. That is the one UAC
   prompt. `cmd/devpit/worker.go` defines the two hidden flags
   (`--elevated-worker`, `--pipe`) and is the process's entire job: parse
   them and call `elevate.Serve`, nothing else.
2. Worker and TUI exchange **one JSON object per line**, in each direction,
   over the pipe (`internal/elevate/codec.go`'s `lineWriter`/`lineReader`).
3. The worker sends `hello` first (its PID and whether its token is really
   elevated), then reads `Request`s until `shutdown` or the pipe closes.
   `Request.Kind` is one of `exec` (run a command, stream `stdout`/`stderr`
   back as `line` events, finish with `done` and an exit code), `remove`
   (delete a path), or `shutdown`.
4. `remove` is checked against a whitelist inside `serveConn` itself
   (`internal/elevate/whitelist.go`'s `validateRemovePath`), independent of
   whatever pre-flight the TUI side already ran: only
   `%WINDIR%\Temp`, `%LOCALAPPDATA%\CrashDumps` and
   `%PROGRAMDATA%\Microsoft\Windows\WER` are reachable through it, because the
   worker has no way to know the TUI's own checks actually ran before the
   request was sent.
5. `serveConn` is the transport-agnostic core (`internal/elevate/server.go`):
   it takes any `io.ReadWriteCloser`, so tests drive the whole protocol over
   `net.Pipe()` with no real named pipe or elevation involved
   (`server_test.go`); `Serve` (Windows-only) is the thin wrapper that dials
   the real pipe.

## The delete flow: tombstone, sweep, Recycle Bin

Deleting a Safe or Unverified item (`internal/clean`) never removes it in
place. `clean.Preflight` runs first — never-touch list, own-executable check,
drive-root/`%WINDIR%`/UNC refusal, reparse-point refusal, and a fresh
`scan.Verify` of the marker — and only then does the target get renamed to
`<name>.devpit-<8 random>` in the same directory (`tombstone` in
`internal/clean/tombstone.go`). That rename is what actually "frees" the
space from the UI's point of view: it is atomic, so a crash mid-delete can
never leave a half-emptied folder, and a rename that fails with a sharing
violation is the cheapest way to discover the item is locked before anything
was removed.

Once tombstoned, `removeTree` lists the tombstone's top-level children and
removes each with a bounded worker pool (12), then removes the now-empty
root — that child count is the delete progress bar's denominator. If the
rename itself failed because the item was locked, Restart Manager
(`internal/winapi/restartmgr_windows.go`) is queried for the holder's name so
the summary can say "open in Code.exe — close it and press R to retry"
instead of "failed".

A tombstone that never got cleaned up — the process was killed, the holder
never closed — is found by `clean.FindTombstones`, which walks the recent
folders and never descends into a tombstone it finds (there is nothing valid
left below it to protect). The clean screen already resumes tombstones under
its recent folders; running that same sweep automatically on program startup
is being wired into `internal/app` now.

Review and Careful items skip the tombstone step entirely and go straight to
the Recycle Bin, as a whole directory in one `SHFileOperationW` call
(`internal/clean/recyclebin_windows.go`), so an accidental delete of
something the user marked as needing a second look is still reversible.

## Testing

- Unit tests for config, icons, the confirm dialog's default and the router.
- Golden frames in `testdata/golden/`, rendered at a fixed size with escape
  sequences stripped, regenerated with
  `go test ./internal/app -run Golden -update`.
- The golden tests drive the real program through `teatest` first, so the
  Bubble Tea loop is exercised, then compare a deterministic frame rather than
  the raw byte stream, which carries cursor moves and altscreen teardown that
  would make the files churn.
- `TestNoColorProducesNoColour` checks the byte stream for the one thing it is
  authoritative about.
