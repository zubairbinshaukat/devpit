//go:build windows

package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWranglerTrueCaseUsesTheOnDiskCase(t *testing.T) {
	base := shortTemp(t)
	mixed := filepath.Join(base, "MixedCase", "SubDir")
	if err := os.MkdirAll(mixed, 0o700); err != nil {
		t.Fatal(err)
	}
	baseTrue := wranglerTrueCase(base)
	got := wranglerTrueCase(strings.ToLower(mixed))
	if got != baseTrue+`\MixedCase\SubDir` {
		t.Fatalf("true case = %q, want %q", got, baseTrue+`\MixedCase\SubDir`)
	}
	// The drive letter is upper case, as Node's process.cwd() gives it.
	if got[0] < 'A' || got[0] > 'Z' {
		t.Fatalf("drive letter = %q", got[:2])
	}
	// A part that does not exist yet keeps its typed case.
	if got := wranglerTrueCase(strings.ToLower(mixed) + `\NotYet\x`); got != baseTrue+`\MixedCase\SubDir\NotYet\x` {
		t.Fatalf("missing tail = %q", got)
	}
	if got := wranglerTrueCase(`c:\`); got != `C:\` {
		t.Fatalf("root = %q", got)
	}
}
