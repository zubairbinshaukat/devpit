# Screenshots

The screenshots of the real app live here as `<name>.webp`, one file per entry
in `web/docs-site/shots.json` and `web/docs-site/shots.content.json`.

They are rendered from the app itself by the screenshot tool and delivered as a
separate zip. Unpack the zip into this folder, so that for example
`clean-results.webp` ends up at `src/assets/screens/clean-results.webp`. The
images are not part of a code patch and this folder is not git-ignored.

- Source format is WebP, about 1200 to 1600 pixels wide. The site turns each one
  into AVIF and WebP at several sizes during the build (`src/components/Shot.astro`).
- A page uses one with `<Shot name="clean-results" />`. The alt text comes from
  the manifest.
- Until a file exists, the page shows a neutral placeholder and the build still
  passes. `npm run check` lists what is missing. Set `DOCS_REQUIRE_SHOTS=1` to make a
  missing file a build error.
