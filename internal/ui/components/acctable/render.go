package acctable

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// View renders the heading and the visible window of rows.
//
// Only the rows inside the window are formatted. The heading carries the
// scroll position ("3–7 of 9") when the rows do not all fit, which costs no
// line of its own: at six lines tall the table still shows five rows.
func (m Model) View(ctx uictx.Context) string {
	l := m.layout(ctx)
	lines := make([]string, 0, 16)
	if len(m.rows) == 0 {
		lines = append(lines, m.heading(ctx, l, 0, 0))
		lines = append(lines, ctx.Theme.Muted.Render(pad(leadW)+fit(m.empty, l.width-leadW, l.ell)))
		return strings.Join(lines, "\n")
	}
	start, end := m.window(ctx, l, m.viewHeight(ctx)-1)
	lines = append(lines, m.heading(ctx, l, start, end))
	for i := start; i < end; i++ {
		lines = append(lines, m.renderRow(ctx, l, i)...)
	}
	return strings.Join(lines, "\n")
}

// Height is how many lines View draws this frame, so a screen can place
// things under the table without rendering it twice.
func (m Model) Height(ctx uictx.Context) int {
	if len(m.rows) == 0 {
		return 2
	}
	l := m.layout(ctx)
	start, end := m.window(ctx, l, m.viewHeight(ctx)-1)
	h := 1
	for i := start; i < end; i++ {
		h += m.rowHeight(ctx, l, i)
	}
	return h
}

// heading draws the muted column titles, and the scroll position at the
// right-hand end when some rows are out of view.
func (m Model) heading(ctx uictx.Context, l layout, start, end int) string {
	var ln line
	ln.to(leadW)
	ln.add(headTool, ctx.Theme.Muted)
	ln.to(l.stackX())
	ln.add(headAccount, ctx.Theme.Muted)
	if l.whyW >= width(headWhy) {
		ln.to(l.whyX)
		ln.add(headWhy, ctx.Theme.Muted)
	}
	if n := len(m.rows); n > 0 && (start > 0 || end < n) {
		dash := "–"
		if ctx.Icons.Tier == icons.TierASCII {
			dash = "-"
		}
		pos := strconv.Itoa(start+1) + dash + strconv.Itoa(end) + " of " + strconv.Itoa(n)
		if x := l.width - width(pos); x >= ln.w+2 {
			ln.to(x)
			ln.add(pos, ctx.Theme.Muted)
		}
	}
	return ln.render(ctx, bandNone, l.width)
}

// renderRow draws row i: its main line, its reason on a second line when it
// wraps, and its caption when it is selected.
func (m Model) renderRow(ctx uictx.Context, l layout, i int) []string {
	th := ctx.Theme
	r := m.rows[i]
	c := l.fit(ctx, r, m.frame)
	selected := i == m.cursor
	hovered := i == m.hover && !selected
	b := bandNone
	if selected {
		b = bandFull
	}

	ln := lead(ctx, selected, hovered, true)
	if l.iconW > 0 {
		glyph := Glyph(ctx.Icons, r.Icon)
		if glyph != "" {
			ln.add(glyph, th.SectionIcon(theme.SectionAccounts))
		}
		ln.to(leadW + l.iconW)
	}
	toolStyle := th.Base
	switch {
	case selected:
		toolStyle = th.Selected
	case r.State == StateNotInstalled:
		toolStyle = th.Muted
	}
	ln.add(fit(r.Tool, l.toolW-l.iconW, l.ell), toolStyle)
	ln.to(l.accX)
	addAccount(&ln, ctx, c, selected)

	out := make([]string, 0, 3)
	if c.hasWhy() && !c.stacked {
		ln.to(l.whyX)
		addWhy(&ln, ctx, c)
	}
	out = append(out, ln.render(ctx, b, l.width))

	if c.stacked {
		next := lead(ctx, selected, hovered, false)
		next.to(l.stackX())
		addWhy(&next, ctx, c)
		out = append(out, next.render(ctx, b, l.width))
	}
	if selected && strings.TrimSpace(r.Caption) != "" {
		out = append(out, m.caption(ctx, l, r.Caption))
	}
	return out
}

// addAccount appends the account cell: the state's mark, the name, the quiet
// detail and the state's word.
func addAccount(ln *line, ctx uictx.Context, c cells, selected bool) {
	th := ctx.Theme
	ln.add(c.mark, c.markStyle)
	ln.space(1)
	if c.name != "" {
		st := c.nameStyle
		if selected {
			st = st.Bold(true)
		}
		ln.add(c.name, st)
		if c.detail != "" {
			ln.space(1)
			ln.add(c.detail, th.Muted)
		}
		if c.word != "" {
			ln.space(width(accGap))
		}
	}
	ln.add(c.word, c.wordStyle)
}

// addWhy appends the reason: its words, its path, and its notes.
func addWhy(ln *line, ctx uictx.Context, c cells) {
	th := ctx.Theme
	start := ln.w
	ln.add(c.whyWords, c.whyStyle)
	if c.path != "" {
		if ln.w > start {
			ln.space(1)
		}
		ln.add(c.path, th.Info)
	}
	for _, n := range c.notes {
		if ln.w > start {
			ln.add(separator(ctx), th.Muted)
		}
		ln.add(n.text, n.style)
	}
}

// separator is [noteSep] in the tier's spelling: a middle dot, or a hyphen
// where only ASCII can be drawn. Both are the same width.
func separator(ctx uictx.Context) string {
	if ctx.Icons.Tier == icons.TierASCII {
		return " - "
	}
	return noteSep
}

// caption draws the selected row's caption on the soft band: under the Why
// column, which it explains, when it fits there whole; otherwise under the
// account, where there is more room.
func (m Model) caption(ctx uictx.Context, l layout, text string) string {
	x := l.whyX
	if width(text) > l.width-l.whyX {
		x = l.stackX()
	}
	ln := lead(ctx, true, false, false)
	ln.to(x)
	ln.add(fit(text, l.width-x, l.ell), ctx.Theme.Muted)
	return ln.render(ctx, bandSoft, l.width)
}

// RowAt maps a line of the rendered table (0 is the heading) to the index of
// the row drawn there. It walks the same window and row heights View draws,
// so the two cannot disagree. ok is false on the heading and past the end.
func (m Model) RowAt(ctx uictx.Context, y int) (index int, ok bool) {
	if y < 1 || len(m.rows) == 0 {
		return 0, false
	}
	l := m.layout(ctx)
	start, end := m.window(ctx, l, m.viewHeight(ctx)-1)
	at := 1
	for i := start; i < end; i++ {
		h := m.rowHeight(ctx, l, i)
		if y >= at && y < at+h {
			return i, true
		}
		at += h
	}
	return 0, false
}

// Click acts on a left click at line y of the rendered table. A click on a
// row selects it; a click on the row that is already selected opens it, the
// same as Enter, which is also what the second click of a double-click is.
// The heading and empty space do nothing.
func (m Model) Click(ctx uictx.Context, y int) (Model, tea.Cmd) {
	i, ok := m.RowAt(ctx, y)
	if !ok {
		return m, nil
	}
	if i == m.cursor {
		return m, m.open()
	}
	m.cursor = i
	m.hover = -1
	return m, nil
}

// Hover marks the row under the pointer at line y with a quiet caret, or
// clears the mark when the pointer is off every row. It never moves the
// selection, so it never scrolls. changed is false when nothing would be
// drawn differently.
func (m Model) Hover(ctx uictx.Context, y int) (next Model, changed bool) {
	i, ok := m.RowAt(ctx, y)
	if !ok {
		i = -1
	}
	if i == m.hover {
		return m, false
	}
	m.hover = i
	return m, true
}

// Pointer handles the mouse for a screen that draws this table top lines
// below the start of its body: a left click selects or opens, the pointer
// passing over a row marks it, the wheel moves the selection. handled is
// false for anything that is not the mouse.
func (m Model) Pointer(ctx uictx.Context, msg tea.Msg, top int) (next Model, cmd tea.Cmd, handled bool) {
	switch pm := msg.(type) {
	case tea.MouseClickMsg:
		if pm.Button != tea.MouseLeft {
			return m, nil, true
		}
		next, cmd = m.Click(ctx, ctx.BodyRow(pm.Y)-top)
		return next, cmd, true
	case tea.MouseMotionMsg:
		next, _ = m.Hover(ctx, ctx.BodyRow(pm.Y)-top)
		return next, nil, true
	case tea.MouseWheelMsg:
		next, cmd = m.Update(pm)
		return next, cmd, true
	}
	return m, nil, false
}
