package logo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/logo"
)

// maxLines and maxCols are the wordmark budget from plan.md section 2.
const (
	maxLines = 6
	maxCols  = 40
)

// TestLogoFitsTheBudget pins the size of both wordmarks.
func TestLogoFitsTheBudget(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		lines := logo.Lines(ascii)
		if len(lines) == 0 || len(lines) > maxLines {
			t.Errorf("ascii=%v: %d lines, want 1..%d", ascii, len(lines), maxLines)
		}
		if w := logo.Width(ascii); w == 0 || w > maxCols {
			t.Errorf("ascii=%v: %d columns, want 1..%d", ascii, w, maxCols)
		}
	}
}

// TestEveryLogoLineIsOneCellPerRune is the invariant that keeps the rows of
// the block letters lined up: a double-width rune anywhere would shear the
// wordmark in half. It also proves every row is the same width.
func TestEveryLogoLineIsOneCellPerRune(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		want := logo.Width(ascii)
		for i, line := range logo.Lines(ascii) {
			w := ansi.StringWidth(line)
			if w != want {
				t.Errorf("ascii=%v line %d: width %d, want %d", ascii, i, w, want)
			}
			if n := utf8.RuneCountInString(line); n != w {
				t.Errorf("ascii=%v line %d: %d runes but %d cells, so a rune is not one cell wide: %q",
					ascii, i, n, w, line)
			}
		}
	}
}

// TestASCIILogoIsASCII proves the ascii-tier wordmark has nothing a dumb
// terminal cannot draw.
func TestASCIILogoIsASCII(t *testing.T) {
	for i, line := range logo.Lines(true) {
		for _, r := range line {
			if r > 0x7F {
				t.Fatalf("line %d holds %q (U+%04X), which is not ASCII", i, r, r)
			}
		}
	}
}

// TestAssetsCopyMatchesTheEmbeddedLogo keeps assets/logo.txt, which the README
// and the demo tape use, byte-identical to the copy compiled into the binary.
// Go can only embed files inside the embedding package's own directory, which
// is why there are two.
func TestAssetsCopyMatchesTheEmbeddedLogo(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "assets", "logo.txt"))
	if err != nil {
		t.Fatalf("reading assets/logo.txt: %v", err)
	}
	got := strings.TrimRight(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	want := strings.TrimRight(logo.String(false), " \n")

	gotLines := strings.Split(got, "\n")
	wantLines := strings.Split(want, "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf("assets/logo.txt has %d lines, the embedded logo has %d", len(gotLines), len(wantLines))
	}
	for i := range wantLines {
		if strings.TrimRight(gotLines[i], " ") != strings.TrimRight(wantLines[i], " ") {
			t.Errorf("line %d differs:\n assets: %q\n binary: %q", i, gotLines[i], wantLines[i])
		}
	}
}
