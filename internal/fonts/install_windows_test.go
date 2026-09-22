//go:build windows

package fonts

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRealGDI exercises the actual AddFontResourceW/RemoveFontResourceW/
// SendMessageTimeoutW calls instead of the fakes used elsewhere in this
// package. It is skipped by default because it touches real OS font
// state; set DEVPIT_FONT_INTEGRATION=1 to run it.
func TestRealGDI(t *testing.T) {
	if os.Getenv("DEVPIT_FONT_INTEGRATION") != "1" {
		t.Skip("set DEVPIT_FONT_INTEGRATION=1 to run tests that touch real OS font state")
	}

	gdi := newGDI()

	// BroadcastFontChange is always safe: it just posts a message to
	// HWND_BROADCAST and does not mutate anything.
	gdi.BroadcastFontChange()

	// AddFontResourceW on a file that is not a valid font resource must
	// fail, proving the real syscall runs end to end without panicking.
	bogus := filepath.Join(t.TempDir(), "not-a-font.ttf")
	if err := os.WriteFile(bogus, []byte("not a font"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := gdi.AddFontResource(bogus); err == nil {
		t.Fatal("expected AddFontResourceW to reject a non-font file")
	}

	// RemoveFontResourceW on a resource that was never added must also
	// fail cleanly rather than panic.
	if err := gdi.RemoveFontResource(bogus); err == nil {
		t.Fatal("expected RemoveFontResourceW to fail for a resource that was never added")
	}
}
