//go:build windows

package host

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// maxProbeOutput caps what the probe may print.
const maxProbeOutput = 64 << 10

// runHotspotProbe runs a constant script in Windows PowerShell 5.1, by its
// full System32 path (never "powershell" from PATH, and never pwsh, which
// has no WinRT projection), not elevated, with the script on stdin, a hidden
// window and a time limit. The output is returned only to be parsed: it may
// hold the saved hotspot password, so it is never put in an error.
func runHotspotProbe(ctx context.Context, script string, limit time.Duration) (string, error) {
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	exe := filepath.Join(sys, "WindowsPowerShell", "v1.0", "powershell.exe")
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	// "-Command -" alone runs stdin line by line, so stdin is read whole and
	// run as one script block. The script never touches the command line.
	cmd := exec.CommandContext(ctx, exe, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", //nolint:gosec // fixed path, constant script
		"& ([scriptblock]::Create([Console]::In.ReadToEnd()))")
	hideWindow(cmd)
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout = &capped{buf: &out, max: maxProbeOutput}
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", errors.New("the hotspot check did not run")
	}
	return out.String(), nil
}

// capped keeps at most max bytes.
type capped struct {
	buf *bytes.Buffer
	max int
}

// Write implements io.Writer.
func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
