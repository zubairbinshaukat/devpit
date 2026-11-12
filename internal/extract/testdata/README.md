# internal/extract test data

## `plans/*.txt`

`hatchzip check` output, one file per case. Each has a `.golden.json` beside it: the `Plan` that `ParsePlan` returns, checked by `TestRecordedPlansParse`. Regenerate the goldens with `go test ./internal/extract -run TestRecordedPlansParse -update` and read the diff before committing.

**Recorded** (hatchzip 0.1.10, Scoop build, Windows 11, NTFS, 2026-11-12, during the Phase 0 spikes in `plans/v0.5.4-spikes.md`). The archives were made in a scratch folder with throwaway content; only the archive name was changed to `archive.*` afterwards, and nothing holds a path of the PC they were made on.

| File | How |
| --- | --- |
| `zip_inplace_fits.txt` | `check archive.zip -o archive` on a 3 MB zip (7 entries) with plenty of space |
| `zip_keep_archive.txt` | the same with `--keep-archive` |
| `zip_existing_files.txt` | the same into a folder already holding 6 of its files (spike X4) |
| `zip_resuming.txt` | a 2 GB / 5,000-file zip stopped part way in place; note the entry count is the entries left (spike X3) |
| `zip_nofit.txt` | `--keep-free 200GB` (a spike-only flag; Devpit never passes it) to force exit 3 |
| `rar_parts.txt` | a RAR5 volume set made with `Rar.exe -v1m`, checked from `.part2.rar` |
| `7z_split.txt` | a 7z made with `7z -v1m`, checked from `.7z.002` |
| `targz.txt` | `tar -czf` (hatchzip reads it; Devpit does not offer tar in v0.5.4) |

**Hand-made** (`handmade_*.txt`): an all-done resume, unknown extra lines, a future rewording of every line (must parse to almost nothing and never fail), a header alone with "1 entry". CRLF line ends are tested in code from the recorded files.

## `encrypted.zip`

694 bytes: three small text files, AES-256, made once with `7z a -tzip -pfixture-pass -mem=AES256`. The password `fixture-pass` is in the test that uses it (`TestAPasswordArchiveWithoutAPasswordExitsFiveNotHangs`). It is not a secret.
