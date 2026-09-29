# Screenshots

The screenshots of the real app live here as `<name>.webp`, one file per entry
in `web/docs-site/shots.json` and `web/docs-site/shots.content.json`. They are
committed, because the Vercel build refuses to deploy a page with a missing
screenshot.

They are rendered from the app itself, never captured by hand. From the repo
root:

```sh
task docs:shots
```

That runs two steps:

1. `go test -tags shots ./internal/shots -run TestRender` (with `DEVPIT_SHOTS=1`)
   drives the real screens over fakes to the state each manifest entry names and
   writes one ANSI frame per entry, plus `index.json`.
2. `node web/docs-site/scripts/shots-render.mjs` lays each frame out as a grid of
   terminal cells in a Windows Terminal style window, draws it in headless Edge or
   Chrome at 2x, and saves a lossless WebP here.

Needs: Go, Node 22, Edge or Chrome (`SHOTS_BROWSER`), Cascadia Mono (found in
the Windows Terminal package, or `SHOTS_FONT`) and Symbols Nerd Font Mono for the
icons (`devpit font install`, or `SHOTS_SYMBOLS`). Re-run it after any change to
a screen that a page shows, and commit the images with the change.

- The site turns each image into AVIF and WebP at several sizes during the
  build (`src/components/Shot.astro`).
- A page uses one with `<Shot name="clean-results" />`. The alt text comes from
  the manifest.
- Until a file exists, a local build shows a neutral placeholder and
  `npm run check` lists it. On Vercel, or with `DOCS_REQUIRE_SHOTS=1`, a missing
  file is a build error.
