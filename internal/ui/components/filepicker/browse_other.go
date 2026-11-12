//go:build !windows

package filepicker

import "errors"

// browseSupported reports that this build cannot open a file picker. Devpit
// ships for Windows; the non-Windows build exists so `GOOS=linux go vet
// ./...` and the Linux half of CI can check everything that is not
// platform-specific.
const browseSupported = false

// errBrowseFailed means the file picker could not be opened.
var errBrowseFailed = errors.New("the file picker is only available on Windows")

// errBrowseTimeout means the dialog did not return in time. Off Windows it is
// never produced; it exists so the refusal wording compiles everywhere.
var errBrowseTimeout = errors.New("the file picker did not respond")

// browse always fails off Windows. The row that would call it is disabled, so
// this is a backstop rather than a path anyone takes.
func browse(string) (string, error) { return "", errBrowseFailed }
