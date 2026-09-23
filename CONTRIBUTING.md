# Contributing to Devpit

Thanks for looking. Devpit is early: milestone 0 of 8. The shape of the code
is settled, most of the behaviour is not.

## Before you start

Open an issue first for anything bigger than a typo. The roadmap is in
`plans/plan.md` section 14, and a change that belongs to a later milestone is
usually better waited for than merged early.

## Setting up

You need Go 1.26 or newer. Nothing else is required to build; no cgo, no C
toolchain.

```powershell
git clone https://github.com/zubairbinshaukat/devpit
cd devpit
go build ./...
go test ./...
```

Optional but recommended:

```powershell
go install github.com/go-task/task/v3/cmd/task@latest
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
go install mvdan.cc/gofumpt@latest
```

Then `task check` runs the same things CI does: build, format check, vet on
Windows and Linux, lint, and tests.

Run `task hooks` once to install the pre-push hook in `.githooks/`. From then
on `git push` runs `task check` first and refuses to push if anything fails,
so a red CI run never starts from this machine. `git push --no-verify` skips
it in an emergency.

## House rules

These are not style preferences; they are why the app starts instantly and does
not corrupt anyone's machine.

**Startup does no work.** Building the model must not exec a process, open a
socket or detect a tool. Anything that asks the operating system a question is a
`tea.Cmd`, so Bubble Tea runs it on its own goroutine after the first frame is
already on screen. The config file read is the one exception.

**Windows syscalls are isolated.** Every one lives in a `*_windows.go` file with
a matching `*_other.go` stub, so `GOOS=linux go vet ./...` passes and the
pure-logic tests run on the Linux CI runner. Use `golang.org/x/sys/windows`.
Never cgo.

**No `init()`.** Nowhere. Package-level values computed by a function call are
fine; side effects at import time are not.

**No global mutable state outside `internal/config`.** The theme and icon sets
are package-level, built once, and read-only after that.

**Styles are values.** Build a `lipgloss.Style` once, at package level, and
render with it. Never construct one inside a `View`. Views build their output
with a `strings.Builder`.

**No dim and no bold-only cues.** The faint SGR attribute is unreadable on light
Windows Terminal themes; use the explicit muted grey from `internal/ui/theme`.
Windows Terminal renders bold as a brighter colour rather than a thicker glyph,
so a cue that is only bold is not a cue. Selection is accent colour plus reverse
video, which survives `NO_COLOR`.

**Every glyph is one cell.** Adding an icon means adding it to all three tiers
and to the width test. See `docs/icons.md`.

**Emoji only in free text.** Never in a table, a menu or the key-hint bar. They
are two cells wide and they break alignment. The allow-list in
`internal/ui/icons/emoji.go` is closed.

**Safety rules are tested, not commented.** If you touch anything in
`docs/safety.md`, the test that pins it has to still pass, or you have to
explain in the pull request why the rule changed.

## Code style

`gofumpt`, not just `gofmt`. Every package has a package comment. Every exported
identifier has a doc comment. Error messages say what happened, why, and what to
do next, in plain language, lower case, no trailing punctuation.

## Tests

- New behaviour needs a test. Bug fixes need a test that fails before the fix.
- UI changes: regenerate the golden files with
  `go test ./internal/app -run Golden -update` and read the diff before
  committing it.
- Tests must not touch the real config. Use `t.TempDir()` with
  `t.Setenv(config.EnvConfigDir, ...)`.
- Tests must not depend on the developer's machine: no real package managers,
  no network, no `%APPDATA%`.

## Commits and pull requests

Conventional commits (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`,
`chore:`), because the changelog is generated from them. One logical change per
pull request. Fill in the template; the checklist is short on purpose.

## Reporting a security issue

Do not open an issue. See [SECURITY.md](SECURITY.md).

## Code of conduct

By taking part you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
