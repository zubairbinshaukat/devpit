package specs

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// labelW is the label column.
const labelW = 11

// leadGap is the air between a row's status glyph and its first word: two
// columns, as on the menus, because a glyph pressed against a word reads as
// part of it.
const leadGap = "  "

// ascii reports the ascii tier.
func ascii(ctx uictx.Context) bool { return ctx.Icons.Tier == icons.TierASCII }

// gap is the air after a glyph inside running text. Nerd Font icons draw
// wider than their one cell and would touch the next word, so they get two.
func gap(ctx uictx.Context) string {
	if ctx.Icons.Tier == icons.TierNerd {
		return "  "
	}
	return " "
}

// tierText swaps typographic glyphs for plain ones in the ascii tier.
func tierText(s string, plain bool) string {
	if !plain {
		return s
	}
	return strings.NewReplacer("…", "...", "×", "x", "°", " C", "·", "-", "–", "-", "—", "-",
		"›", ">", "→", "->", "↑↓", "up/down", "←→", "left/right", "━", "=", "─", "-", "■", "#", "□", "_", "▪", "#", "≈", "~", "“", `"`, "”", `"`, "’", "'").Replace(s)
}

// fit cuts plain text to w cells, after the tier swap.
func fit(ctx uictx.Context, s string, w int) string {
	s = tierText(s, ascii(ctx))
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, ell(ctx))
}

// pad right-pads text, styled or not, to w cells.
func pad(s string, w int) string {
	if n := w - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// wrap folds plain text to w cells, at most maxLines lines (the last one
// cut with an ellipsis).
func wrap(ctx uictx.Context, s string, w, maxLines int) []string {
	if w < 8 {
		w = 8
	}
	// Break only at spaces, so a date or a part number never splits at its
	// hyphen, and never put a "·" at the start of a line: it stays with the
	// word before it. A word wider than the line is cut.
	var words []string
	for _, word := range strings.Fields(s) {
		if word == "·" && len(words) > 0 {
			words[len(words)-1] += " ·"
			continue
		}
		words = append(words, word)
	}
	var ls []string
	line := ""
	for _, word := range words {
		word = tierText(word, ascii(ctx))
		if ansi.StringWidth(word) > w {
			word = ansi.Truncate(word, w, ell(ctx))
		}
		switch {
		case line == "":
			line = word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= w:
			line += " " + word
		default:
			ls = append(ls, line)
			line = word
		}
	}
	ls = append(ls, line)
	if maxLines > 0 && len(ls) > maxLines {
		rest := strings.Join(ls[maxLines-1:], " ")
		ls = append(ls[:maxLines-1], fit(ctx, rest, w))
	}
	return ls
}

// levelStyle is a finding's colour.
func levelStyle(ctx uictx.Context, l sysinfo.Level) lipgloss.Style {
	th := ctx.Theme
	switch l {
	case sysinfo.Bad:
		return th.Danger
	case sysinfo.Warn:
		return th.Warning
	case sysinfo.Good:
		return th.Success
	}
	return th.Muted
}

// levelGlyph is a finding's shape: never colour alone.
func levelGlyph(ctx uictx.Context, l sysinfo.Level) string {
	switch l {
	case sysinfo.Bad:
		return ctx.Icons.Fail
	case sysinfo.Warn:
		return ctx.Icons.Warn
	case sysinfo.Good:
		return ctx.Icons.Tick
	}
	return ctx.Icons.Queued
}

// mark is a glyph and a word in the level's colour: "✓ activated".
func mark(ctx uictx.Context, l sysinfo.Level, word string) string {
	return levelStyle(ctx, l).Render(levelGlyph(ctx, l) + gap(ctx) + tierText(word, ascii(ctx)))
}

// gauge is a bar w cells wide: solid blocks in st over a quiet dotted track,
// so it reads as a gauge with colour and without (ascii: [####------]). A
// share above nothing always shows one block, and one short of everything
// never looks full.
func gauge(ctx uictx.Context, frac float64, w int, st lipgloss.Style) string {
	frac = min(max(frac, 0), 1)
	w = max(w, 1)
	filled := int(frac*float64(w) + 0.5)
	if frac > 0 && filled == 0 {
		filled = 1
	}
	if frac < 1 && filled == w && w > 1 {
		filled = w - 1
	}
	track := ctx.Theme.Rule
	if ascii(ctx) {
		return "[" + st.Render(strings.Repeat("#", filled)) + track.Render(strings.Repeat("-", w-filled)) + "]"
	}
	return st.Render(strings.Repeat(ctx.Icons.BarFull, filled)) + track.Render(strings.Repeat(ctx.Icons.BarEmpty, w-filled))
}

// gaugeW is the width a gauge of w cells takes, brackets included.
func gaugeW(ctx uictx.Context, w int) int {
	if ascii(ctx) {
		return max(w, 1) + 2
	}
	return max(w, 1)
}

// healthWord is the word beside the battery gauge.
func healthWord(pct int) (string, sysinfo.Level) {
	switch {
	case pct < 60:
		return "very worn", sysinfo.Bad
	case pct < 80:
		return "worn", sysinfo.Warn
	case pct >= 100:
		return "as new", sysinfo.Good
	}
	return "good", sysinfo.Good
}

// ell is the tier's ellipsis.
func ell(ctx uictx.Context) string {
	if ascii(ctx) {
		return "..."
	}
	return "…"
}

// itoa is strconv.Itoa without the import in every file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// commas is "1,234".
func commas(n int64) string {
	s := itoa(int(n))
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []byte
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// nonEmpty drops empty strings.
func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
