// Package shots renders the screenshots in the documentation from the real
// screens.
//
// Everything of substance here lives in _test.go files, on purpose. Nothing
// in the shipping binary imports this package, so no demo data, fake engine
// or renderer can ever be linked into it. The test that does the work,
// TestRender, is skipped unless DEVPIT_SHOTS is set, which keeps a plain
// `go test ./...` fast and free of side effects.
//
// The flow is: read web/docs-site/shots.json (and shots.content.json), build
// the app model with every outside call replaced by a fake that returns
// clearly fictional demo data, drive it to the requested state with the same
// key presses a person would use, draw one frame at the requested size, and
// write it as truecolor ANSI text. web/docs-site/scripts/shots.mjs turns
// those files into compressed images. See docs/screenshots.md in that folder.
package shots
