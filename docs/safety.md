# Safety

Devpit deletes files. Everything in this file is a rule that the code enforces,
not advice it offers. Each row names where the rule lives and what test pins it.

Most of milestone 1's rules are now enforced with a passing test beside them.
A few rows are still marked **planned**: the rule is decided and the milestone
is named, but the code that enforces it does not exist yet, or (row 17) the
code exists but has no fixture pinning it. A row may not move from planned to
enforced without the test beside it, and it may not claim a test that does not
exist.

## The rules

| # | Rule | Enforced in | Pinned by |
| --- | --- | --- | --- |
| 1 | Nothing is deleted without a preview and an explicit confirmation | `ui/screens/clean`: `beginDelete` is only reachable from a `confirm.AnsweredMsg` carrying a non-empty selection | `TestCannotReachDeletingWithoutConfirm` |
| 2 | The default answer on every confirmation is No | `components/confirm` has no constructor that produces a Yes default, and `Reset` returns to No | `TestDefaultIsAlwaysNo`, `TestEnterOnAFreshDialogAnswersNo` |
| 3 | Careful items need a typed word | `confirm.Model.WithTypedWord("DELETE")`; `y` alone does not bypass it | `TestTypedWordGatesYes`, `TestTypedWordIsCaseSensitive`; pinned a second time where it matters, in `ui/screens/clean`, by `TestCarefulSelectionRequiresTypedWord` and `TestCarefulWordIsCaseSensitive` |
| 4 | Careful items are never pre-ticked | `components/restable.preselect` | `TestPreselectNeverTicksCareful` |
| 5 | Projects touched in the last `active_days` (7) are never pre-ticked | `components/restable.preselect`, using the newest mtime of the project's top-level files and `.git/index` | `TestPreselectNeverTicksActive`, plus `TestPreselectNeverTicksUnverified`, `TestPreselectNeverTicksCloud` and `TestSetItemsAndAppendPreserveTheRule` for the paths that feed the same table |
| 6 | The never-touch list is always skipped | both `scan/filters.go`'s `skipPath` (the walker's root filter) and `clean.Preflight`, independently; a tombstone being finished by `Sweep` or `clean.Retry` is checked the same way, against both its tombstone name and the original path the marker names, by `preflightTombstone` | `TestNeverTouchIsSkipped`, `TestNeverTouchEntriesAreMatchedByPrefix` in `scan`; `TestPreflightRefusesTheNeverTouchList`, `TestDryRunDeletesNothing`, `TestSweepRefusesATombstoneOnTheNeverTouchList`, `TestRetryRefusesWhenNeverTouchNowCoversTheItem` in `clean` |
| 7 | Devpit's own executable is always on the never-touch list | `clean.Preflight`, resolved via `os.Executable` | `TestPreflightRefusesDevpitsOwnExecutable`, `TestRunRefusesToDeleteItsOwnDirectory` |
| 8 | Reparse points are never followed | the `scan` walker and sizer treat `FILE_ATTRIBUTE_REPARSE_POINT`, `ModeSymlink` and `ModeIrregular` as leaves and never size through them; `scan.Verify` and `clean.Preflight` both refuse a path that resolves through one; `preflightTombstone` runs the same reparse check on a tombstone before `Sweep` or `clean.Retry` removes it | `TestJunctionIsNeverFollowedOrSizedThrough`, `TestReparsePointDetection`, `TestVerifyRefusesAPathBelowAJunction` in `scan`; `TestPreflightRefusesAPathReachedThroughAJunction`, `TestJunctionInsideTheTargetIsRemovedAsALinkAndTheTargetSurvives`, `TestSweepRefusesATombstoneThatIsAReparsePoint` in `clean` |
| 9 | A junk folder is only junk when its marker file sits beside it | `scan.Rule.HasMarker` and `matchDir`: `package.json` for `node_modules`, `Cargo.toml` for `target`, `.csproj`/`.sln` for `bin` and `obj` | `TestMarkerGating`, `TestHasMarker`, `TestHasMarkerWithAGlob`, `TestHasMarkerInside` |
| 10 | The marker is re-verified immediately before deleting | `scan.Verify`, called from `clean.Preflight` via `Item.Verify`; the clean screen sets that hook before a delete | `TestVerifyRefusesWhenTheMarkerIsGone`, `TestVerifyRefusesARenamedPath`, `TestPreflightRefusesWhenVerifyFails` |
| 11 | A drive root, anything under `%WINDIR%`, and UNC or mapped network paths are refused | `clean.Preflight` for the delete path; `scan.Verify`'s `protectedByEnvironment` and the `pathpicker` component both refuse the same paths before a scan even starts | `TestPreflightRefusesDriveRootsAndWindows`, `TestPreflightRefusesUNCPaths` in `clean`; `TestVerifyRefusesProtectedPaths` in `scan`; `TestValidationRefusesTheThreeBadAnswers` in `pathpicker` |
| 12 | Current versions of apps are never removed | `scan/rules/scoop.go` excludes whatever the `current` junction points at, and never touches `persist` | `TestScoopExcludesCurrentAndPersist`, `TestScoopSkipsAnAppWithoutAReadableCurrent` |
| 13 | Docker volumes are never in Full Scan, and `--volumes` is never passed, in any spelling | `scan/rules/docker.go`'s `ForbiddenArgs`, checked by `scan.Command.Valid` (catches `--volumes`, `-v` and `--all-volumes`); `clean.RunCommand` calls `scan.Command.Valid` on its own arguments before running anything, plus a literal `--volumes`/`--volumes=...` check that catches what an exact-match `Valid` cannot; `clean.RunScanCommand` is the entry point for a scan step's own command — it additionally checks that step's own `ForbiddenArgs` before delegating to `RunCommand`, so a step-specific forbidden flag is caught even if it is not in the fixed list `RunCommand` knows about | `TestDockerNeverPrunesVolumes`, `TestValidCatchesAForbiddenArgument` in `scan`; `TestRunCommandRefusesVolumes`, `TestRunCommandRefusesShortVolumesFlag`, `TestRunScanCommandRefusesAStepsOwnForbiddenArgs` in `clean` |
| 13a | The scan and clean tier enums can never drift apart, and a screen that maps between them fails safe | `scan.Tier` and `clean.Tier` are pinned numerically equal by a test that runs on every change; `internal/ui/screens/clean`'s tier mapping is an exhaustive switch whose `default` case is Careful, the strictest tier, never Safe | `TestScanTiersMatchCleanTiers` in `clean`; `TestTierMappingIsExhaustiveAndSafeByDefault` in `internal/ui/screens/clean` |
| 14 | Every rule has a non-empty restore hint, shown on the confirmation | `scan/rules`; a test walks the whole rule table | `TestEveryRuleHasARestoreHint` |
| 15 | An existing SSH key is never overwritten without a warning | `gitssh.Keygen` refuses without an explicit `force`; the gitssh screen requires the user to type `OVERWRITE` before it will pass one | `TestKeygen_RefusesToOverwriteExistingPrivateKey`, `TestKeygen_RefusesToOverwriteExistingPublicKey` in `gitssh`; `TestExistingKeyRequiresTypedOverwrite` in the gitssh screen |
| 16 | PID 0 and 4, every other binary under `%WINDIR%\System32`, and services are never killed, and Devpit says why | `ports.Protected`. `cmd.exe`, `powershell.exe` and `conhost.exe` under System32 are the one deliberate exception: they are interactive shell hosts, not services, and dev tools spawn them constantly — plan.md section 8 explicitly offers "kill process tree" when the parent is one of them. `svchost.exe`, `services.exe`, every other System32 binary, and anything running in session 0 stay protected | `TestProtectedRefusesPID0And4`, `TestProtectedRefusesSystem32Binaries`, `TestProtectedRefusesKnownServiceHosts`, `TestProtectedExemptsShellHostsUnderSystem32` in `ports`; `TestKillRefusesProtectedPIDs` in the integration suite; `TestProtectedProcessCannotBeKilled` in the ports screen |
| 17 | Cloud placeholder files are skipped, not downloaded | **enforced in code, not yet pinned by a fixture.** `scan.isCloudAttr` (`internal/scan/attrs_windows.go`) checks `FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS` and `FILE_ATTRIBUTE_RECALL_ON_OPEN` and the walker/sizer use it to count zero local bytes and label the item "cloud" | none yet — a real OneDrive/Dropbox placeholder cannot be fabricated in a fixture tree without the Cloud Filter API; pinning this needs either a recorded `Win32FileAttributeData` fixture fed through `isCloudAttr` directly, or a CI step that provisions a real placeholder via the Cloud Filter API and is skipped everywhere else |
| 18 | The TUI never runs elevated | admin-only work goes to a separate hidden worker process (`internal/elevate`) with no `internal/ui` or `internal/app` import anywhere in it; the worker's own `serveConn` also whitelists which paths a `remove` request may ever touch, so a compromised or buggy request from the TUI side still can't delete outside `%WINDIR%\Temp`, `%LOCALAPPDATA%\CrashDumps` and `%PROGRAMDATA%\Microsoft\Windows\WER`. `cmd/devpit/worker.go` is the entry point (`devpit --elevated-worker --pipe <name>`) | `TestServeRemoveWhitelist`, `TestServeRefusesUnknownKind`, `TestClientSurfacesWorkerDied` |
| 18a | A long-running install or update, and the elevated worker doing its work, both stop the instant the user asks, and the TUI is never left waiting on a goroutine that can't be cancelled | the `install` and `update` screens cancel the run's context and stop the elevated worker on Esc/Ctrl+C, rather than leaving the goroutine running to completion in the background; `tools.RunStep` runs each step's process inside a Windows Job Object, so a timeout kills the whole process tree it spawned, not just the direct child | `TestStopCancelsTheRunAndUnblocksTheGoroutine` in `internal/ui/screens/install` and `internal/ui/screens/update`; `TestRunStepTimeoutKillsTheTree` in `internal/tools` |
| 19 | Nothing leaves the machine unless usage stats are on | off by default; `DEVPIT_NO_TELEMETRY=1` and `DO_NOT_TRACK=1` always win | `TestDefaults`; the client is milestone 8 |
| 20 | A corrupt settings file never silently discards settings | `config.Load` renames it to `config.toml.broken-<unix>` and surfaces a warning | `TestCorruptFileIsQuarantined` |
| 21 | A settings file from a newer Devpit is refused, never partly applied | `config.Load` checks the schema version before using any field | `TestFutureSchemaIsRefusedNotDowngraded` |
| 22 | A half-written settings file is never observable | `config.Save` writes a temp file in the same directory and renames over the target | `TestSaveIsAtomicAndLeavesNoTempFiles` |

## Two rules that are about behaviour, not code paths

**Nothing fails silently.** A locked folder, an access-denied directory, a
cancelled scan: each is counted, named, and reported in the summary with what
to do about it. "Couldn't delete api\node_modules — it's open in Code.exe.
Close it and press R to retry" is the standard, not "3 items skipped". This is
`clean.reasonFor`, pinned by `TestReasonsAreSentencesAPersonCanAct` and
`TestLockedItemIsReportedWithItsHolder`.

**Interrupting is safe.** `Esc` during a delete finishes the item in flight and
reports what was done; completed deletions stay done — pinned by
`TestCancelFinishesTheItemInFlightAndReportsPartially` in `clean` and
`TestEscDuringDeleteFinishesItemAndShowsSummary` in the clean screen. The
rename-to-tombstone step means an interrupted delete leaves a
`.devpit-<random>` folder, never a half-emptied `node_modules`, and the
tombstone is finished later by `Sweep`, which runs at startup, from Settings →
Resume, and from `clean.Retry`.

A tombstone is never removed on the strength of its name alone. The instant
the rename succeeds, `writeTombstoneMarker` writes `.devpit-tombstone.json`
inside the renamed directory — `TombstoneMarker{OriginalPath, CreatedAt, PID,
Version}` — and that marker is what makes a tombstone self-authenticating: a
user's own folder that happens to be named `archive.devpit-abc12345` matches
the name pattern exactly but holds no marker, and `verifyTombstone` refuses it
on sight, so `Sweep` reports it as skipped and never opens it
(`TestSweepIgnoresALookAlikeWithoutAMarker`). `TestTombstoneMarkerIsWrittenOnRename`
pins that the marker is written the moment the rename happens, before anything
below the directory is touched.

`Sweep` (and `Resume`, its name for the startup and Settings → Resume paths,
and `clean.Retry` for a single item) only ever acts on a directory whose
marker names that exact directory — `findTombstoneCandidates` matches every
directory by the name pattern first, but `verifyTombstone` then checks the
marker's `OriginalPath` against the directory the pattern implies, and a
mismatch is refused, not repaired. Before removing a verified tombstone, both
`Sweep` and `Retry` run `preflightTombstone`, which re-runs the environment
check (drive roots, `%WINDIR%`, network paths), the never-touch list, and the
reparse-point check — against both the tombstone's current name and the
original path the marker claims, because the never-touch list can have grown
since the tombstone was made
(`TestSweepRefusesATombstoneOnTheNeverTouchList`,
`TestRetryRefusesWhenNeverTouchNowCoversTheItem`,
`TestSweepRefusesATombstoneThatIsAReparsePoint`). Everything that fails any of
these checks is reported in `Report.Skipped` and left exactly as it was;
`TestSweepFinishesATombstoneLeftByALockedRemoval` pins the success path, that
a sweep completes a tombstone a locked removal left behind.

## Why deleting is a rename first

Renaming the target to `<name>.devpit-<8 random>` in the same directory is
instant and atomic. It does three things at once: the space is reported freed
immediately, a crash mid-delete can never leave a partially emptied folder that
looks like a working one, and a rename that fails with a sharing violation is
the cheapest possible way to discover the folder is locked, before anything has
been removed.

The rename alone is not proof of anything: the moment it succeeds, Devpit
writes `.devpit-tombstone.json` inside the renamed directory, naming the exact
path it was renamed from. That marker is what makes a tombstone
self-authenticating, and it is the only thing a later sweep ever trusts — see
"Interrupting is safe" below for how `Sweep` uses it.

Review and Careful items skip the tombstone and go to the Recycle Bin instead,
as a whole directory in one `SHFileOperationW` call, so they are reversible.
That call lives in `internal/clean/recyclebin_windows.go` as Devpit's own
wrapper rather than a borrowed one, after a bug in an existing `go2trash`-style
helper was traced to the `SHFILEOPSTRUCTW` being garbage-collected out from
under the pending Win32 call; `runtime.KeepAlive` pins it for the duration.

## Changing any of this

A pull request that touches a row in the table has to say in its description
which rule changed and why, and the test that pins the rule has to still pass
or be replaced by a stricter one. `docs/safety.md` is in `CODEOWNERS` for that
reason.
