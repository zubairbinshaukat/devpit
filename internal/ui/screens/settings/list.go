package settings

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/choices"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The list itself is the shared choices component; these are the small text
// helpers the sub-screens share.

// piece is a run of text in one style.
type piece struct {
	text  string
	style lipgloss.Style
}

// ellipsis is the tier's "…".
func ellipsis(ctx uictx.Context) string {
	if ctx.Icons.Tier == icons.TierASCII {
		return "..."
	}
	return "…"
}

// tierText swaps the typographic glyphs the words use for plain ones on a
// terminal limited to ascii.
func tierText(s string, ascii bool) string {
	if !ascii {
		return s
	}
	return strings.NewReplacer("…", "...", "–", "-", "›", ">", "→", "->").Replace(s)
}

// fit cuts plain text to w cells with the tier's ellipsis.
func fit(ctx uictx.Context, s string, w int) string {
	s = tierText(s, ctx.Icons.Tier == icons.TierASCII)
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, ellipsis(ctx))
}

// shortenPath cuts a path in the middle so its drive and last folder stay.
func shortenPath(p string, w int, ell string) string { return choices.ShortenPath(p, w, ell) }
