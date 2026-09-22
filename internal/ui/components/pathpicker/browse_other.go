//go:build !windows

package pathpicker

import "errors"

// browseSupported reports that this build cannot open a folder browser.
// Devpit ships for Windows; the non-Windows build exists so `GOOS=linux go
// vet ./...` and the Linux half of CI can check everything that is not
// platform-specific.
const browseSupported = false

// browseDesc is the description shown beside the Browse row.
const browseDesc = "Open the system folder browser"

// ErrBrowseFailed means the folder browser could not be opened.
var ErrBrowseFailed = errors.New("the folder browser is only available on Windows — type the path instead")

// browse always fails off Windows. The row that would call it is disabled,
// so this is a backstop rather than a path anyone takes.
func browse(string) (string, error) { return "", ErrBrowseFailed }
