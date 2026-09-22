# Security Policy

Devpit deletes files. That makes correctness a security property, not just a
quality one, and it is why this file exists before version 1.0 does.

## Supported versions

Only the latest release. Devpit is pre-1.0 and there are no maintenance
branches yet.

## Reporting a vulnerability

Do not open a public issue.

Use GitHub's private reporting:
**[Security → Report a vulnerability](https://github.com/zubairbinshaukat/devpit/security/advisories/new)**.

Please include what you found, how to reproduce it, and what an attacker gets
out of it. You will get a first reply within 7 days. If a fix is needed, the
advisory and the release go out together, and you get credit unless you would
rather not.

### What counts

Anything that makes Devpit remove, move or expose a file the user did not
choose, or run code it should not:

- Escaping the scan or delete rules: reparse-point traversal, path handling
  that walks outside the chosen root, a rule matching something it should not.
- Defeating a safety gate: reaching a delete without a confirmation, a
  confirmation that defaults to Yes, bypassing the typed word on the Careful
  tier.
- Privilege problems in the elevated worker (milestone 5): argument injection
  into the `runas` launch, an unauthenticated pipe client, work executed that
  the parent process never asked for.
- Supply chain: a way to get an artefact published that was not built from the
  public source by the release workflow.
- Leaking anything the privacy policy says is never sent.

### What does not count

- Devpit deleting something you ticked and confirmed. That is the feature.
- Antivirus false positives on an unsigned binary. Real, but not a
  vulnerability; see below.
- Anything that needs the attacker to already be running code as you.

## What is signed, and how to check it

Every release is built by GitHub Actions from a tagged commit in this
repository. Releases are not built on anyone's laptop.

Each release carries:

- `checksums.txt`, the SHA256 of every artefact.
- A GitHub build provenance attestation, produced by
  `actions/attest-build-provenance`.

Verify a download:

```powershell
# 1. The checksum matches the published list
Get-FileHash .\devpit_windows_amd64.zip -Algorithm SHA256
# compare with the matching line in checksums.txt

# 2. The artefact really was built by this repository's workflow
gh attestation verify .\devpit_windows_amd64.zip --repo zubairbinshaukat/devpit
```

If either check fails, do not run it, and report it.

## Code signing

Devpit is not Authenticode-signed yet, so Windows SmartScreen may warn about an
unknown publisher. The plan is to apply to the SignPath Foundation's free
open-source signing programme once there are a few releases. Until then the
provenance attestation above is the stronger check: it ties the file to the
exact commit and workflow run that produced it.

Devpit is never compressed with UPX, because packed executables trip antivirus
heuristics. If your scanner flags a release, please report it to your vendor as
a false positive and open an issue here so it can be tracked.

## How Devpit handles privilege

- The TUI never runs elevated. If you start it from an elevated prompt it still
  does not use the privilege for anything.
- Work that genuinely needs administrator rights runs in a separate hidden
  worker process, launched on demand with one UAC prompt, that executes a fixed
  job list and streams results back. It contains no UI code. It arrives in
  milestone 5; the flags are reserved and inert today.
- Devpit's own executable is always on the never-touch list.
