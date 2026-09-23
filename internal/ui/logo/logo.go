// Package logo holds Devpit's wordmark: the big block-letter DEVPIT the home
// screen draws above its menu.
//
// There are two versions of it, both embedded into the binary. The default is
// the shadowed block style drawn with U+2588 and the box-drawing set, which
// every modern console font has: six rows of forty-five cells. The ascii one
// is drawn with "#" for terminals that have nothing above U+007F: five rows
// of thirty-five. Every rune in both is exactly one terminal cell wide so no
// row can shift against the others, and assets/logo.txt is kept identical to
// the default so the installer and the app draw the same mark.
package logo

import (
	_ "embed"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

//go:embed logo.txt
var blockArt string

//go:embed logo_ascii.txt
var asciiArt string

// The two wordmarks, split into rows and padded to a rectangle once, at
// program load. Both are read-only after that.
var (
	blockLines = layout(blockArt)
	asciiLines = layout(asciiArt)
)

// Lines returns the wordmark's rows, every one padded to the same width. The
// slice is shared, so callers must not write to it.
func Lines(ascii bool) []string {
	if ascii {
		return asciiLines
	}
	return blockLines
}

// String returns the wordmark as one block of text.
func String(ascii bool) string { return strings.Join(Lines(ascii), "\n") }

// Height is the number of rows the wordmark occupies.
func Height(ascii bool) int { return len(Lines(ascii)) }

// Width is the number of cells the wordmark occupies.
func Width(ascii bool) int {
	l := Lines(ascii)
	if len(l) == 0 {
		return 0
	}
	return ansi.StringWidth(l[0])
}

// layout splits an embedded wordmark into rows and pads every row to the
// width of the widest one. Padding here rather than in the file keeps the
// checked-in art free of trailing whitespace, and keeps the block rectangular
// however it is placed: centring a ragged block centres each row on its own,
// which shears the letters apart.
func layout(art string) []string {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(art, "\r\n", "\n"), "\n"), "\n")
	width := 0
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > width {
			width = w
		}
	}
	for i, l := range lines {
		if pad := width - ansi.StringWidth(l); pad > 0 {
			lines[i] = l + strings.Repeat(" ", pad)
		}
	}
	return lines
}
