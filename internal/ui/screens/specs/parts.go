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

// ascii reports the ascii tier.
func ascii(ctx uictx.Context) bool { return ctx.Icons.Tier == icons.TierASCII }

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
	ell := "…"
	if ascii(ctx) {
		ell = "..."
	}
	return ansi.Truncate(s, w, ell)
}

// pad right-pads plain text to w cells.
func pad(s string, w int) string {
	if n := w - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// wrap folds plain text to w cells, at most maxLines lines (the last one
// cut with an ellipsis).
func wrap(ctx uictx.Context, s string, w, maxLines int) []string {
	s = tierText(s, ascii(ctx))
	if w < 8 {
		w = 8
	}
	ls := strings.Split(ansi.Wrap(s, w, " "), "\n")
	for i := range ls {
		ls[i] = strings.TrimRight(ls[i], " ")
	}
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

// meter is the battery gauge: w cells, a heavy line over a light one in the
// level's colour (ascii: [=====-----]), then always the percent and a word.
func meter(ctx uictx.Context, frac float64, w int, l sysinfo.Level, word string) string {
	frac = min(max(frac, 0), 1)
	filled := int(frac*float64(w) + 0.5)
	st := levelStyle(ctx, l)
	pct := int(frac*100 + 0.5)
	label := " " + st.Render(itoa(pct)+"% "+word)
	if ascii(ctx) {
		return "[" + st.Render(strings.Repeat("=", filled)) + ctx.Theme.Rule.Render(strings.Repeat("-", w-filled)) + "]" + label
	}
	return st.Render(strings.Repeat("━", filled)) + ctx.Theme.Rule.Render(strings.Repeat("─", w-filled)) + label
}

// bar is a usage bar in the accent colour.
func bar(ctx uictx.Context, frac float64, w int) string {
	if ascii(ctx) {
		return "[" + ctx.Track(frac, w) + "]"
	}
	return ctx.Track(frac, w)
}

// barW is the width a bar takes, brackets included.
func barW(ctx uictx.Context, w int) int {
	if ascii(ctx) {
		return w + 2
	}
	return w
}

// healthWord is the word under the battery gauge.
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
