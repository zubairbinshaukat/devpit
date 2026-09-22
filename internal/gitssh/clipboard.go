package gitssh

import (
	"encoding/base64"
	"fmt"
	"io"
)

// Copy writes text to w as an OSC 52 escape sequence, which asks the
// terminal emulator itself to put text on the system clipboard. This works
// over SSH and inside terminal multiplexers where no OS clipboard is
// reachable directly.
func Copy(w io.Writer, text string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	_, err := fmt.Fprintf(w, "\x1b]52;c;%s\x07", encoded)
	return err
}

// CopyBest tries the native Windows clipboard first (via [CopyWin32]) and
// falls back to an OSC 52 sequence written to w if that is unavailable
// (non-Windows builds, or the clipboard API call failing).
func CopyBest(w io.Writer, text string) error {
	if err := CopyWin32(text); err == nil {
		return nil
	}
	return Copy(w, text)
}
