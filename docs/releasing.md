# Releasing

A release is a git tag. CI does the rest: `.github/workflows/release.yml`
runs the tests, then GoReleaser builds `devpit.exe` for Windows amd64 and
arm64, zips them with the licence files, writes `checksums.txt` and an SBOM,
publishes the GitHub release with a changelog, and attests build provenance.

## Before the first release (once)

1. **Scoop bucket.** A bucket is just a git repository with one JSON manifest
   per app under `bucket/`, so one bucket serves every project you ship.
   GoReleaser writes `bucket/devpit.json` into `zubairbinshaukat/scoop-bucket`
   on each stable release. Set it up once:
   1. Create a public repository named `scoop-bucket` with a README.
   2. Add an empty `bucket/.gitkeep` so the directory exists.
   3. GitHub: Settings, Developer settings, Personal access tokens,
      Fine-grained tokens. Generate one scoped to only `scoop-bucket` with
      Repository permissions, Contents: Read and write. Set an expiry and
      note it; the release fails when it lapses.
   4. In the `devpit` repository: Settings, Secrets and variables, Actions,
      New repository secret, name `SCOOP_BUCKET_TOKEN`.

   GoReleaser fails the whole release if the secret is missing, so do this
   before the first tag or delete the `scoops:` section. Users install with:

   ```powershell
   scoop bucket add zubyr https://github.com/zubairbinshaukat/scoop-bucket
   scoop install devpit
   ```

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
- winget. See below; the first submission is manual.

## winget

winget has one central catalogue, `microsoft/winget-pkgs`, and every version
is a pull request there that Microsoft moderates (a few days the first time,
usually hours after that). Devpit ships as a zip with a portable exe inside,
which winget supports directly. The package id is `Zubyr.Devpit`.

**First version, by hand.** Wait until a stable release exists on GitHub,
then from a machine with the `wingetcreate` tool:

```powershell
winget install Microsoft.WingetCreate
wingetcreate new https://github.com/zubairbinshaukat/devpit/releases/download/v0.1.0/devpit_0.1.0_windows_amd64.zip https://github.com/zubairbinshaukat/devpit/releases/download/v0.1.0/devpit_0.1.0_windows_arm64.zip
```

The wizard asks for the id (`ZubairBinShaukat.Devpit`), name, publisher,
licence (MIT), description, and for each zip the installer type: choose
`zip`, nested installer type `portable`, nested file `devpit.exe`, command
alias `devpit`. It writes three YAML manifests, validates them, and can open
the pull request for you when you say yes at the end (it needs a GitHub
token with public repo access, which it prompts for). Answer any bot comments
on the pull request; once merged, `winget install ZubairBinShaukat.Devpit`
works.

**Every later version, automated.** The `winget` job in
`.github/workflows/release.yml` runs after GoReleaser on every stable tag. It
bumps the manifests in your fork of `microsoft/winget-pkgs` and opens the
pull request; the bot usually auto-approves updates to an existing package
within a day. It needs, once:

1. A fork of `microsoft/winget-pkgs` under `zubairbinshaukat` (wingetcreate
   made it during the first submission).
2. A classic personal access token with the `public_repo` scope (and
   `workflow`, so the action can keep the fork in sync), stored in this
   repository's Actions secrets as `WINGET_TOKEN`. Note its expiry.

The job fails harmlessly until the first manual submission is merged. If it
fails later, the GitHub release and Scoop are already done, so just run the
manual update instead: `wingetcreate update Zubyr.Devpit --version X.Y.Z
--urls <amd64 zip> <arm64 zip> --submit`.

**Rules that trip people up.** Pre-releases are not accepted. The zip URLs
must be release assets, not the `latest` redirect. The SHA256 of each zip is
computed by the tool, so never re-upload an asset after submitting.
