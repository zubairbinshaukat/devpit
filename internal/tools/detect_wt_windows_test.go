//go:build windows

package tools_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools"
)

// On a machine with Windows Terminal installed from the Store, the real
// detector reads its version from the wt.exe alias and never starts it. The
// test skips where there is no alias (CI images, Windows 10 without WT).
func TestRealWindowsTerminalVersionWithoutRunningIt(t *testing.T) {
	alias := filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WindowsApps", "wt.exe")
	if _, err := os.Lstat(alias); err != nil {
		t.Skip("no Windows Terminal alias on this machine")
	}
	d := tools.New(
		tools.WithRun(func(_ context.Context, path string, args ...string) (string, error) {
			t.Errorf("detection started %s %v", path, args)
			return "", errors.New("not run in this test")
		}),
		// WindowsApps is often unlistable for a standard user: the alias
		// alone must be enough.
		tools.WithReadDir(func(string) ([]string, error) { return nil, os.ErrPermission }),
	)
	got := d.Get(context.Background(), "wt")
	if !got.Found {
		t.Fatal("Windows Terminal not found although its alias exists")
	}
	// Up to three digits first: the bundle's "3001.x" is not the app's.
	if !regexp.MustCompile(`^\d{1,3}\.\d+\.\d+\.\d+$`).MatchString(got.Version) {
		t.Errorf("Version = %q, want a Windows Terminal version such as 1.24.11911.0", got.Version)
	}
	t.Logf("Windows Terminal %s at %s", got.Version, got.Path)
}
