# Releasing

A release is a git tag. CI does the rest: `.github/workflows/release.yml`
runs the tests, then GoReleaser builds `devpit.exe` for Windows amd64 and
arm64, zips them with the licence files, writes `checksums.txt` and an SBOM,
publishes the GitHub release with a changelog, and attests build provenance.

## Before the first release (once)

1. **Scoop bucket.** `.goreleaser.yml` pushes a manifest to
   `zubairbinshaukat/devpit-bucket`. Create that repository with a `bucket/`
   directory, make a fine-grained token with write access to it, and add it
   to this repository's Actions secrets as `SCOOP_BUCKET_TOKEN`. GoReleaser
   fails the whole release if the secret is missing, so either do this or
   delete the `scoops:` section first.
2. **Dry run.** `task snapshot` builds everything locally into `dist/`
   without publishing. Run the built `dist/devpit_windows_amd64_v1/devpit.exe`
   and check `devpit version` shows a version, commit and date.

## Every release

1. Make sure `main` is green: `task check` locally and CI on GitHub.
2. Pick the version. Use [SemVer](https://semver.org): `v0.1.0` for the first
   cut, patch for fixes, minor for features. Anything with a suffix such as
   `v0.2.0-rc.1` is published as a pre-release and skips the Scoop bucket.
3. Tag and push:

   ```powershell
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

4. Watch the **Release** workflow under Actions. It takes a few minutes.
5. Check the release page: two zips, `checksums.txt`, the SBOM files, and a
   changelog grouped into Features, Fixes and Performance. The changelog is
   built from commit subjects, so `feat:`, `fix:` and `perf:` prefixes land in
   the right group and `docs:`, `test:`, `chore:`, `ci:` are left out.
6. Smoke-test the install path on a clean machine or VM:

   ```powershell
   irm https://devpit.zubyr.dev/install | iex
   devpit version
   ```

7. Update the "no tagged release yet" note in `README.md` after the first
   release.

## If it goes wrong

- **Workflow failed before publishing.** Fix on `main`, delete the tag locally
  and remotely, then tag the fixed commit with the same version:

  ```powershell
  git tag -d v0.1.0
  git push origin :refs/tags/v0.1.0
  ```

- **Release published but broken.** Never rewrite a published tag. Fix
  forward with a patch release, and mark the bad one as a pre-release on
  GitHub so `releases/latest` (which the installer and self-updater use) skips
  it.

## What is not automated yet

- Authenticode signing. SmartScreen warns on first run until then; see
  `SECURITY.md`.
- winget. Planned via `winget-releaser` after a few stable releases.
