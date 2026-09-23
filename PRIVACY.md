# Privacy

Short version: Devpit sends nothing anywhere unless you turn usage stats on,
and they are off until you do.

## What runs on your machine

Everything. The scan, the sizes, the deletions, the port list and the settings
all stay local. Devpit reads and writes exactly two directories:

| What | Where | Override |
| --- | --- | --- |
| Settings | `%APPDATA%\devpit\config.toml` | `DEVPIT_CONFIG_DIR` |
| Caches and logs | `%LOCALAPPDATA%\devpit\` | `DEVPIT_CACHE_DIR` |

Nothing in either directory is uploaded.

## Usage stats are opt-in

You are asked once, on first run, with the answer already on No:

> Help show how much space Devpit saves? Only totals are sent — no file names
> or paths.

Leaving it as No is the default. You can change your mind in
**Settings → Usage stats**.

These always disable stats, whatever the setting says:

- `DEVPIT_NO_TELEMETRY=1`
- `DO_NOT_TRACK=1`

Devpit never asks in a non-interactive run, and never enables stats on its own.

## What is sent, if you turn it on

After a cleanup finishes, one small report. Numbers and short labels only:

- Bytes freed
- Number of items removed
- Item type, from a fixed list such as `node_modules`, `npm-cache`,
  `docker-build-cache`
- Operating system name and version
- Devpit version
- A random install ID, generated on your machine, that identifies nothing else

## What is never sent

- File, folder or project names
- Any path, absolute or relative
- Your username, machine name or domain
- Your IP address as a location; the server does not store it
- The contents of any file
- What tools or apps you have installed
- Anything at all when a scan finds nothing, or when you cancel

## How it behaves

Reports go out in the background and never block you. If you are offline, or
the server is down, or it takes too long, the report is dropped silently and
nothing is retried or queued to disk. A failed report never shows an error and
never delays a cleanup.

The receiving server rejects impossible values, so the public totals cannot be
inflated by a forged report. It keeps a hashed form of your IP address for at
most one hour, only to limit how many reports one connection can send, and
that hash is never stored beyond the hour or used for anything else.

## What the totals are for

Aggregated numbers on the project's landing page: total space freed, items
removed by type, how many people are using it. Nothing per-user is published,
and nothing per-user is stored beyond the aggregate counters.

## Update checks

Separate from usage stats. Devpit can check GitHub's public releases API at
most once every 24 hours to see if a newer version exists. That request sends
nothing but the HTTP request itself, and its result is cached locally. If you
are offline it is skipped silently. When a newer release exists you see a
small "update" pill in the header and the upgrade command under
**Settings → About Devpit**; nothing is ever downloaded or replaced for you.

Turn the check off in **Settings → Update check**, or set
`DEVPIT_NO_UPDATE_CHECK=1`, which wins over the setting. A build made from
source never checks.

## Changes to this policy

If what is collected ever changes, this file changes in the same release, the
change is called out in the release notes, and anyone who had opted in is asked
again.
