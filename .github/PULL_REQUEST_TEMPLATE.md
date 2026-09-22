## What this changes

<!-- One or two sentences. What is different after this is merged? -->

## Why

<!-- Link the issue, or explain the problem this solves. -->

Closes #

## How to check it

<!-- The steps a reviewer follows to see it work. -->

## Checklist

- [ ] `task check` passes (format, vet on Windows and Linux, lint, tests)
- [ ] New behaviour has a test; a bug fix has a test that failed before it
- [ ] No work added to the startup path (no exec, no network, no tool detection)
- [ ] Any new Windows syscall is in a `_windows.go` file with an `_other.go` stub
- [ ] No new `init()` and no new global mutable state outside `internal/config`
- [ ] Any new glyph exists in all three icon tiers and is one cell wide
- [ ] Golden files regenerated and the diff read, if the UI changed
- [ ] `docs/safety.md` still accurate, if this touches scanning or deleting
