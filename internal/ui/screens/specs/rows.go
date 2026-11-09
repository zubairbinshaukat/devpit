package specs

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// builder collects the lines of one part of the page, w columns wide. Every
// line keeps one blank column on the left and one on the right.
type builder struct {
	ctx   uictx.Context
	m     Model
	w     int
	lines []string
}

func (b *builder) add(ls ...string) { b.lines = append(b.lines, ls...) }

// heading is " Battery ──────────── right ": the name in the accent, a quiet
// rule, and a short summary at the end of the rule.
func (b *builder) heading(title, right string) {
	th := b.ctx.Theme
	t := fit(b.ctx, title, b.w-8)
	rw := ansi.StringWidth(right)
	n := b.w - 1 - 1 - ansi.StringWidth(t) - 1
	if right != "" {
		n -= rw + 1
	}
	line := " " + th.Title.Render(t) + " " + th.Rule.Render(strings.Repeat(b.ctx.Icons.TrackEmpty, max(3, n)))
	if right != "" {
		line += " " + right
	}
	b.add(line)
}

// spinning is the muted "reading…" with the spinner.
func (b *builder) spinning() string {
	th := b.ctx.Theme
	return th.Muted.Render("reading"+ell(b.ctx)+" ") + th.Accent.Render(b.ctx.SpinnerFrame(b.m.spin))
}

// state draws the section's heading and handles a section that is still
// reading or failed. It reports whether the section's own rows should
// follow. right is the heading's summary once everything arrived.
func (b *builder) state(title string, right func() string, secs ...sysinfo.Section) bool {
	th := b.ctx.Theme
	for _, s := range secs {
		if !b.m.rep.Have[s] {
			b.heading(title, b.spinning())
			return false
		}
	}
	r := ""
	if right != nil {
		r = right()
	}
	b.heading(title, r)
	for _, s := range secs {
		if e := b.m.rep.Errs[s]; e != "" && len(secs) == 1 {
			for _, l := range wrap(b.ctx, "Could not read this: "+e+".", b.w-5, 2) {
				b.add("   " + th.Warning.Render(l))
			}
			return false
		}
	}
	return true
}

// valW is the room for a value after the label.
func (b *builder) valW() int { return b.w - 1 - 3 - labelW - 1 }

// kv is one labelled row; value is already styled and fits valW.
func (b *builder) kv(label, value string) {
	b.add("   " + b.ctx.Theme.Muted.Render(pad(fit(b.ctx, label, labelW), labelW)) + " " + value)
}

// styled is a labelled row of plain text in st, folded onto a second line
// when it is long, or "not reported".
func (b *builder) styled(label, value string, st lipgloss.Style) {
	if strings.TrimSpace(value) == "" {
		b.kv(label, b.nr())
		return
	}
	for i, l := range wrap(b.ctx, value, b.valW(), 2) {
		if i == 0 {
			b.kv(label, st.Render(l))
		} else {
			b.kv("", st.Render(l))
		}
	}
}

// text is one labelled row of plain text.
func (b *builder) text(label, value string) { b.styled(label, value, b.ctx.Theme.Base) }

// status is a labelled row led by a level's glyph: "✓ on", "! not
// activated". An empty text is "not reported".
func (b *builder) status(label string, l sysinfo.Level, text string) {
	if strings.TrimSpace(text) == "" {
		b.kv(label, b.nr())
		return
	}
	ctx := b.ctx
	g := levelGlyph(ctx, l)
	lead := levelStyle(ctx, l).Render(g) + gap(ctx)
	leadW := ansi.StringWidth(g + gap(ctx))
	for i, s := range wrap(ctx, text, b.valW()-leadW, 2) {
		if i == 0 {
			b.kv(label, lead+ctx.Theme.Base.Render(s))
		} else {
			b.kv("", strings.Repeat(" ", leadW)+ctx.Theme.Base.Render(s))
		}
	}
}

// nr is the muted "not reported by this PC".
func (b *builder) nr() string {
	return b.ctx.Theme.Muted.Render(fit(b.ctx, sysinfo.NotReported, b.valW()))
}

// note is a muted line under the rows, wrapped.
func (b *builder) note(s string) {
	for _, l := range wrap(b.ctx, s, b.w-5, 3) {
		b.add("   " + b.ctx.Theme.Muted.Render(l))
	}
}

// thumbTag marks a rule of thumb, at the right end of the finding.
const thumbTag = "rule of thumb"

// findingText is a finding folded to w columns. A rule of thumb keeps a
// column of its own at the right, with "rule of thumb" on the first line,
// so the tag never hangs on a line by itself.
func (b *builder) findingText(f sysinfo.Finding, w, maxLines int) []string {
	th, ctx := b.ctx.Theme, b.ctx
	tagW := 0
	if f.Thumb && w >= 40 {
		tagW = len(thumbTag) + 2
	}
	ls := wrap(ctx, f.Text, w-tagW, maxLines)
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = th.Base.Render(l)
	}
	switch {
	case tagW > 0:
		out[0] = pad(out[0], w-len(thumbTag)) + th.Muted.Render(thumbTag)
	case f.Thumb:
		out = append(out, th.Muted.Render(fit(ctx, thumbTag, w)))
	}
	return out
}

// areaFindings shows a section's own flags and notes at its end.
func (b *builder) areaFindings(fs []sysinfo.Finding, area string) {
	ctx := b.ctx
	for _, f := range fs {
		if f.Area != area || f.Level == sysinfo.Good {
			continue
		}
		g := levelGlyph(ctx, f.Level)
		lead := "   " + levelStyle(ctx, f.Level).Render(g) + leadGap
		leadW := 3 + ansi.StringWidth(g) + len(leadGap)
		for i, l := range b.findingText(f, b.w-1-leadW, 3) {
			if i == 0 {
				b.add(lead + l)
			} else {
				b.add(strings.Repeat(" ", leadW) + l)
			}
		}
	}
}

// box is the card's border pieces for the tier.
type box struct{ tl, tr, bl, br, h, v string }

func boxFor(ctx uictx.Context) box {
	if ascii(ctx) {
		return box{"+", "+", "+", "+", "-", "|"}
	}
	return box{"╭", "╮", "╰", "╯", "─", "│"}
}

// areaW is the area column of the Buyer's check.
const areaW = 9

// check is the Buyer's check: a card whose border wears the verdict's
// colour, a headline, one aligned row per flag and a row of what is fine.
func (b *builder) check(fs []sysinfo.Finding) {
	th, ctx := b.ctx.Theme, b.ctx
	look, fine := sysinfo.Counts(fs)
	reading := b.m.reading()
	worst := sysinfo.Good
	for _, f := range fs {
		worst = max(worst, f.Level)
	}

	ow := b.w - 2 // the card's outer width, from column 1
	inner := ow - 4
	bx := boxFor(ctx)
	border := th.Success
	switch {
	case look > 0:
		border = levelStyle(ctx, worst)
	case reading:
		border = th.Rule
	}

	var rows []string
	row := func(s string) { rows = append(rows, s) }

	// The headline.
	switch {
	case look > 0:
		word := " things to look at"
		if look == 1 {
			word = " thing to look at"
		}
		pill := levelStyle(ctx, worst).Reverse(true).Bold(true).Render(" " + levelGlyph(ctx, worst) + gap(ctx) + itoa(look) + word + " ")
		extra := th.Muted.Render(fmt.Sprintf("  ·  %d fine", fine))
		if reading {
			extra += th.Muted.Render("  ·  still reading"+ell(ctx)+" ") + th.Accent.Render(ctx.SpinnerFrame(b.m.spin))
		}
		row(pill + extra)
	case reading:
		row(th.Muted.Render("Reading this PC"+ell(ctx)+" ") + th.Accent.Render(ctx.SpinnerFrame(b.m.spin)))
	default:
		pill := th.Success.Reverse(true).Bold(true).Render(" " + ctx.Icons.Tick + gap(ctx) + "Nothing stands out ")
		row(pill + th.Muted.Render(fit(ctx, "  The details are below.", inner-ansi.StringWidth(pill))))
	}
	if ctx.Height >= 30 {
		row("")
	}

	// One row per flag, worst first.
	limit, maxLines := 2, 2
	switch {
	case ctx.Height >= 40:
		limit, maxLines = 5, 3
	case ctx.Height >= 30:
		limit, maxLines = 3, 3
	}
	tw := inner - 1 - len(leadGap) - areaW - 2
	indent := strings.Repeat(" ", 1+len(leadGap)+areaW+2)
	shown := 0
	for _, f := range fs {
		if f.Level < sysinfo.Warn {
			continue
		}
		if shown == limit {
			row(indent + th.Muted.Render(fit(ctx, fmt.Sprintf("+%d more, each in its section below", look-shown), tw)))
			break
		}
		lead := levelStyle(ctx, f.Level).Render(levelGlyph(ctx, f.Level)) + leadGap + th.Subtitle.Render(pad(fit(ctx, f.Area, areaW), areaW)) + "  "
		for i, l := range b.findingText(f, tw, maxLines) {
			if i == 0 {
				row(lead + l)
			} else {
				row(indent + l)
			}
		}
		shown++
	}

	// What is fine, on one row (two when long).
	var chips []string
	for _, f := range fs {
		if f.Level == sysinfo.Good && f.Chip != "" {
			chips = append(chips, f.Chip)
		}
	}
	if len(chips) > 0 {
		lead := th.Success.Render(ctx.Icons.Tick) + leadGap + th.Muted.Render(pad("Fine", areaW)) + "  "
		for i, l := range wrap(ctx, strings.Join(chips, " · "), tw, 2) {
			if i == 0 {
				row(lead + th.Base.Render(l))
			} else {
				row(indent + th.Base.Render(l))
			}
		}
	}

	// The card.
	title := th.Title.Render("Buyer's check")
	b.add(" " + border.Render(bx.tl+bx.h) + " " + title + " " + border.Render(strings.Repeat(bx.h, max(1, ow-5-ansi.StringWidth(title)))+bx.tr))
	for _, r := range rows {
		b.add(" " + border.Render(bx.v) + " " + pad(ansi.Truncate(r, inner, ""), inner) + " " + border.Render(bx.v))
	}
	foot := fit(ctx, "Rules of thumb are marked; missing data is never judged.", ow-7)
	if n := ow - 5 - ansi.StringWidth(foot); n >= 1 && foot != "" {
		b.add(" " + border.Render(bx.bl+bx.h) + " " + th.Muted.Render(foot) + " " + border.Render(strings.Repeat(bx.h, n)+bx.br))
	} else {
		b.add(" " + border.Render(bx.bl+strings.Repeat(bx.h, max(0, ow-2))+bx.br))
	}
}

// glance is the at-a-glance list: dim labels, bright values.
func (b *builder) glance() {
	th := b.ctx.Theme
	b.heading("At a glance", "")
	got := map[string]string{}
	for _, l := range sysinfo.Glance(b.m.rep) {
		got[l.Label] = l.Value
	}
	for _, row := range []struct {
		label string
		sec   sysinfo.Section
	}{
		{"Processor", sysinfo.SecCPU},
		{"Memory", sysinfo.SecMemory},
		{"Storage", sysinfo.SecDisks},
		{"Graphics", sysinfo.SecGraphics},
		{"Display", sysinfo.SecDisplays},
		{"Battery", sysinfo.SecBattery},
		{"Windows", sysinfo.SecWindows},
	} {
		switch v := got[row.label]; {
		case !b.m.rep.Have[row.sec]:
			b.kv(row.label, th.Muted.Render("reading"+ell(b.ctx)))
		case b.m.rep.Errs[row.sec] != "" && v == sysinfo.NotReported:
			b.kv(row.label, th.Muted.Render(fit(b.ctx, "could not read", b.valW())))
		case v == sysinfo.NotReported:
			b.kv(row.label, b.nr())
		default:
			b.styled(row.label, v, th.Subtitle)
		}
	}
}
