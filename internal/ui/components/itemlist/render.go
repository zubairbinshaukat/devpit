package itemlist

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The fixed parts of a row.
const (
	// leftPad is the margin before the caret, matching the menus.
	leftPad = 2
	// leadW is the gutter: margin, caret and the space after it.
	leadW = 4
	// labelW fits the widest label, "▲ Careful".
	labelW = 9
	// titleMax caps the item column, and titleMin is the narrowest it is
	// squeezed to.
	titleMax = 24
	titleMin = 8
	// detailMax caps the size column, and detailMin is the narrowest it is
	// kept at before it is dropped (the focused row then shows it on its
	// hint line, so nothing is lost).
	detailMax = 30
	detailMin = 8
	// subX is where a row's note and hint lines start: two cells in from
	// its title, so they read as belonging to it.
	subX = leadW + 2
)

// Column headings.
const (
	headItem  = "ITEM"
	headSize  = "SIZE"
	headLabel = "LABEL"
	headMode  = "MODE"
)

// slot is one column of the mode control. Share and Same list share the
// first, because a row offers one or the other, never both.
type slot struct {
	modes []Mode
	x, w  int
}

// slotModes is the control's columns, left to right.
func slotModes() [][]Mode { return [][]Mode{{ModeShare, ModeSameList}, {ModeCopy}, {ModeSkip}} }

// layout is the column geometry of one frame.
type layout struct {
	width   int
	gap     int
	ell     string
	sep     string
	titleW  int
	detailW int
	detailX int
	labelX  int
	ctrlX   int
	ctrlW   int
	slots   []slot
}

// layout sizes the columns. The label and the mode control are fixed by
// their content; the item and size columns share what is left, the size
// giving way first and being dropped below [detailMin].
func (m Model) layout(ctx uictx.Context) layout {
	l := layout{width: m.viewWidth(ctx), gap: 3, ell: ellipsis(ctx), sep: " · "}
	if ctx.Icons.Tier == icons.TierASCII {
		l.sep = " - "
	}
	if l.width < 100 {
		l.gap = 2
	}

	x := 0
	for _, modes := range slotModes() {
		w := 0
		for _, r := range m.rows {
			for _, md := range modes {
				if r.offers(md) {
					w = max(w, width(md.String())+2)
				}
			}
		}
		if w == 0 {
			continue
		}
		if len(l.slots) > 0 {
			x++
		}
		l.slots = append(l.slots, slot{modes: modes, x: x, w: w})
		x += w
	}
	l.ctrlW = max(x, width(headMode))
	titleNat, detailNat := width(headItem), 0
	for _, r := range m.rows {
		titleNat = max(titleNat, width(r.Title))
		detailNat = max(detailNat, width(r.Detail))
		if r.Locked {
			l.ctrlW = max(l.ctrlW, min(width(r.LockedReason), 24))
		}
	}
	titleNat = min(titleNat, titleMax)
	detailNat = min(detailNat, detailMax)
	if detailNat > 0 {
		detailNat = max(detailNat, width(headSize))
	}

	rest := l.width - leadW - labelW - l.ctrlW - 3*l.gap
	switch {
	case titleNat+detailNat <= rest:
		l.titleW, l.detailW = titleNat, detailNat
	case rest-titleNat >= detailMin:
		l.titleW, l.detailW = titleNat, rest-titleNat
	default:
		l.titleW = max(titleMin, min(titleNat, rest+l.gap))
	}
	l.detailX = leadW + l.titleW + l.gap
	l.labelX = l.detailX
	if l.detailW > 0 {
		l.labelX += l.detailW + l.gap
	}
	l.ctrlX = l.labelX + labelW + l.gap
	return l
}

// View renders the heading and the visible window of rows.
func (m Model) View(ctx uictx.Context) string {
	l := m.layout(ctx)
	out := make([]string, 0, 24)
	if len(m.rows) == 0 {
		out = append(out, m.heading(ctx, l, 0, 0))
		out = append(out, ctx.Theme.Muted.Render(pad(leadW)+fit("Nothing to bring over.", l.width-leadW, l.ell)))
		return strings.Join(out, "\n")
	}
	start, end := m.window(l, m.viewHeight(ctx)-1)
	out = append(out, m.heading(ctx, l, start, end))
	for i := start; i < end; i++ {
		out = append(out, m.renderRow(ctx, l, i)...)
	}
	return strings.Join(out, "\n")
}

// Height is how many lines View draws this frame.
func (m Model) Height(ctx uictx.Context) int {
	return strings.Count(m.View(ctx), "\n") + 1
}

// heading draws the muted column titles, and the scroll position at the
// right-hand end when some rows are out of view.
func (m Model) heading(ctx uictx.Context, l layout, start, end int) string {
	th := ctx.Theme
	var ln line
	ln.to(leadW)
	ln.add(headItem, th.Muted)
	if l.detailW >= width(headSize) {
		ln.to(l.detailX)
		ln.add(headSize, th.Muted)
	}
	ln.to(l.labelX)
	ln.add(headLabel, th.Muted)
	ln.to(l.ctrlX)
	ln.add(headMode, th.Muted)
	if n := len(m.rows); n > 0 && (start > 0 || end < n) {
		dash := "–"
		if ctx.Icons.Tier == icons.TierASCII {
			dash = "-"
		}
		pos := strconv.Itoa(start+1) + dash + strconv.Itoa(end) + " of " + strconv.Itoa(n)
		if x := l.width - width(pos); x >= ln.w+2 {
			ln.to(x)
			ln.add(pos, th.Muted)
		}
	}
	return ln.render(ctx, bandNone, l.width)
}

// renderRow draws row i: its main line, its note, and its hint while it is
// focused.
func (m Model) renderRow(ctx uictx.Context, l layout, i int) []string {
	th := ctx.Theme
	r := m.rows[i]
	selected := i == m.cursor
	hovered := i == m.hover && !selected
	b := bandNone
	if selected {
		b = bandFull
	}

	ln := lead(ctx, selected, hovered, true)
	titleStyle := th.Base
	switch {
	case selected:
		titleStyle = th.Selected
	case r.Locked:
		titleStyle = th.Muted
	}
	ln.add(fit(r.Title, l.titleW, l.ell), titleStyle)
	if l.detailW > 0 {
		ln.to(l.detailX)
		ln.add(fit(r.Detail, l.detailW, l.ell), th.Muted)
	}
	ln.to(l.labelX)
	glyph, word, st := label(ctx, r)
	ln.add(glyph, st)
	ln.to(l.labelX + 2)
	ln.add(word, st)
	ln.to(l.ctrlX)
	if r.Locked {
		ln.add(fit(r.LockedReason, l.width-l.ctrlX, l.ell), th.Muted)
	} else {
		m.addControl(&ln, ctx, l, r)
	}
	out := []string{ln.render(ctx, b, l.width)}

	if r.Note != "" {
		note := lead(ctx, selected, hovered, false)
		note.to(subX)
		text := ctx.Icons.Warn + " " + r.Note
		note.add(fit(text, l.width-subX, l.ell), th.Warning)
		out = append(out, note.render(ctx, b, l.width))
	}
	if hint := m.hint(l, r); selected && hint != "" {
		h := lead(ctx, true, false, false)
		h.to(subX)
		h.add(fit(hint, l.width-subX, l.ell), th.Muted)
		out = append(out, h.render(ctx, bandSoft, l.width))
	}
	return out
}

// addControl appends the row's options, each in its slot, the current one
// in brackets so the choice reads without colour.
func (m Model) addControl(ln *line, ctx uictx.Context, l layout, r Row) {
	th := ctx.Theme
	for _, s := range l.slots {
		ln.to(l.ctrlX + s.x)
		for _, md := range s.modes {
			if !r.offers(md) {
				continue
			}
			if md != r.Mode {
				// The brackets' cells stay blank, so the word sits
				// exactly where it would inside them.
				ln.space(1)
				ln.add(md.String(), th.Muted)
				ln.space(1)
				continue
			}
			st := th.Accent.Bold(true)
			if md == ModeSkip {
				st = th.Base.Bold(true)
			}
			ln.add("["+md.String()+"]", st)
		}
	}
}

// label is the shape, the word and the colour of a row's label: the risk
// tiers drawn exactly as the results table draws them, and a locked row
// marked with the key glyph where the tier has one.
func label(ctx uictx.Context, r Row) (glyph, word string, st lipgloss.Style) {
	th := ctx.Theme
	ic := ctx.Icons
	switch {
	case r.Locked:
		return ic.Lock, "locked", th.Muted
	case r.Label == LabelCareful:
		return ic.Careful, "Careful", th.Danger
	case r.Label == LabelReview:
		return ic.Review, "Review", th.Warning
	default:
		return ic.Safe, "Safe", th.Success
	}
}

// hint is the line under the focused row: the size when the size column was
// dropped or cut, the row's own hint (or why it is locked, or that it asks
// twice), and the suggested choice when the row has been moved off it.
func (m Model) hint(l layout, r Row) string {
	var parts []string
	if r.Detail != "" && width(r.Detail) > l.detailW {
		parts = append(parts, r.Detail)
	}
	switch {
	case r.Hint != "":
		parts = append(parts, r.Hint)
	case r.Locked && r.LockedReason != "":
		parts = append(parts, "Locked: "+r.LockedReason+".")
	case r.Locked:
		parts = append(parts, "Locked: Devpit never changes this.")
	case r.NeedsConfirm && !r.Confirmed:
		parts = append(parts, "Turning this on asks twice.")
	}
	// A guarded row is meant to start off, so it never nudges towards on.
	if !r.Locked && r.Mode != r.Default && r.offers(r.Default) && (!r.NeedsConfirm || r.Confirmed) {
		parts = append(parts, "suggested: "+r.Default.String())
	}
	return strings.Join(parts, l.sep)
}

// rowHeight is how many lines row i takes this frame.
func (m Model) rowHeight(l layout, i int) int {
	h := 1
	if m.rows[i].Note != "" {
		h++
	}
	if i == m.cursor && m.hint(l, m.rows[i]) != "" {
		h++
	}
	return h
}

// window picks the rows to draw in avail lines, centred on the cursor and
// clamped at both ends, the way the menus scroll. It is computed on every
// frame, so View, RowAt and Click cannot disagree.
func (m Model) window(l layout, avail int) (start, end int) {
	n := len(m.rows)
	if n == 0 {
		return 0, 0
	}
	h := make([]int, n)
	total := 0
	for i := range m.rows {
		h[i] = m.rowHeight(l, i)
		total += h[i]
	}
	if avail <= 0 || total <= avail {
		return 0, n
	}
	cursor := min(max(m.cursor, 0), n-1)
	used := h[cursor]
	if used >= avail {
		return cursor, cursor + 1
	}
	target := (avail - used) / 2
	start = cursor
	back := 0
	for i := cursor - 1; i >= 0; i-- {
		if back+h[i] > target || used+h[i] > avail {
			break
		}
		back += h[i]
		used += h[i]
		start = i
	}
	for end = cursor + 1; end < n; end++ {
		if used+h[end] > avail {
			break
		}
		used += h[end]
	}
	for i := start - 1; i >= 0; i-- {
		if used+h[i] > avail {
			break
		}
		used += h[i]
		start = i
	}
	return start, end
}

// RowAt maps a line of the rendered list (0 is the heading) to the row drawn
// there. ok is false on the heading and past the end.
func (m Model) RowAt(ctx uictx.Context, y int) (index int, ok bool) {
	if y < 1 || len(m.rows) == 0 {
		return 0, false
	}
	l := m.layout(ctx)
	start, end := m.window(l, m.viewHeight(ctx)-1)
	at := 1
	for i := start; i < end; i++ {
		h := m.rowHeight(l, i)
		if y >= at && y < at+h {
			return i, true
		}
		at += h
	}
	return 0, false
}

// ModeAt is the option drawn at column x of row i's main line, if any.
func (m Model) ModeAt(ctx uictx.Context, i, x int) (Mode, bool) {
	if i < 0 || i >= len(m.rows) {
		return ModeSkip, false
	}
	l := m.layout(ctx)
	r := m.rows[i]
	for _, s := range l.slots {
		from := l.ctrlX + s.x
		if x < from || x >= from+s.w {
			continue
		}
		for _, md := range s.modes {
			if r.offers(md) && x < from+width(md.String())+2 {
				return md, true
			}
		}
	}
	return ModeSkip, false
}

// Click acts on a left click at column x, line y of the rendered list. A
// click on an option focuses the row and picks that option, going through
// the same guard as the keys (a guarded row reports [WantsCarefulMsg]
// instead). A click anywhere else on a row focuses it.
func (m Model) Click(ctx uictx.Context, x, y int) (Model, tea.Cmd) {
	i, ok := m.RowAt(ctx, y)
	if !ok {
		return m, nil
	}
	main := m.mainLine(ctx, i) == y
	m.cursor = i
	m.hover = -1
	if md, ok := m.ModeAt(ctx, i, x); ok && main {
		return m.choose(i, md)
	}
	return m, nil
}

// mainLine is the line of the rendered list row i's main line is on, or -1.
// It is measured before the click moves the focus, which is what the user
// was looking at.
func (m Model) mainLine(ctx uictx.Context, i int) int {
	l := m.layout(ctx)
	start, end := m.window(l, m.viewHeight(ctx)-1)
	at := 1
	for k := start; k < end; k++ {
		if k == i {
			return at
		}
		at += m.rowHeight(l, k)
	}
	return -1
}

// Hover marks the row under the pointer at line y with a quiet caret. It
// never moves the focus, so it never scrolls.
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

// Pointer handles the mouse for a screen that draws this list top lines
// below the start of its body, against the left edge: a left click focuses
// a row or picks an option, the pointer passing over a row marks it, the
// wheel moves the focus. handled is false for anything that is not the
// mouse.
func (m Model) Pointer(ctx uictx.Context, msg tea.Msg, top int) (next Model, cmd tea.Cmd, handled bool) {
	switch pm := msg.(type) {
	case tea.MouseClickMsg:
		if pm.Button != tea.MouseLeft {
			return m, nil, true
		}
		next, cmd = m.Click(ctx, pm.X, ctx.BodyRow(pm.Y)-top)
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

// Legend lays out the explanation the screen puts under the list, one entry
// per line: "Share = one folder for both, add once and both see it." The
// word before " = " is drawn in body text and the rest in the muted colour,
// wrapped with a hanging indent so a long entry still reads as one item.
func Legend(ctx uictx.Context, w int, entries ...string) string {
	th := ctx.Theme
	var out []string
	for _, e := range entries {
		term, rest, ok := strings.Cut(e, " = ")
		if !ok {
			term, rest = "", e
		}
		indent := leadW
		head := ""
		if term != "" {
			head = th.Base.Bold(true).Render(term) + th.Muted.Render(" = ")
			indent += width(term) + 3
		}
		if indent > w/2 {
			indent = leadW + 2
		}
		room := max(10, w-indent)
		first := max(10, w-leadW-width(term)-3)
		if term == "" {
			first = max(10, w-leadW)
		}
		wrapped := strings.Split(ansi.Wordwrap(rest, first, " "), "\n")
		if len(wrapped) > 1 {
			tail := strings.Join(wrapped[1:], " ")
			wrapped = append(wrapped[:1], strings.Split(ansi.Wordwrap(tail, room, " "), "\n")...)
		}
		for k, part := range wrapped {
			part = ansi.Truncate(part, room, ellipsis(ctx))
			if k == 0 {
				out = append(out, pad(leadW)+head+th.Muted.Render(part))
				continue
			}
			out = append(out, pad(indent)+th.Muted.Render(part))
		}
	}
	return strings.Join(out, "\n")
}

// SummaryLine draws the footer's live count: "Share 4 · Copy 2 · Skip 3 ·
// 1.2 GB to copy". Modes nobody chose are left out; Skip is always there so
// the line is never empty.
func SummaryLine(ctx uictx.Context, s Summary) string {
	th := ctx.Theme
	sep := " · "
	if ctx.Icons.Tier == icons.TierASCII {
		sep = " - "
	}
	var parts []string
	add := func(name string, n int) {
		if n > 0 {
			parts = append(parts, th.Muted.Render(name+" ")+th.Base.Render(strconv.Itoa(n)))
		}
	}
	add("Share", s.Share)
	add("Same list", s.SameList)
	add("Copy", s.Copy)
	parts = append(parts, th.Muted.Render("Skip ")+th.Base.Render(strconv.Itoa(s.Skip)))
	if s.CopyBytes > 0 {
		parts = append(parts, th.Info.Render(header.FormatBytes(s.CopyBytes))+th.Muted.Render(" to copy"))
	}
	return strings.Join(parts, th.Muted.Render(sep))
}

// viewWidth is the width the list draws into.
func (m Model) viewWidth(ctx uictx.Context) int {
	if m.width > 0 {
		return m.width
	}
	return max(0, ctx.Width)
}

// viewHeight is how many lines the list may use, heading included. Zero is
// unbounded.
func (m Model) viewHeight(ctx uictx.Context) int {
	if m.height > 0 {
		return m.height
	}
	return max(0, ctx.BodyHeight)
}

// ellipsis is the mark a cut ends with, in the tier's spelling.
func ellipsis(ctx uictx.Context) string {
	if ctx.Icons.Tier == icons.TierASCII {
		return "..."
	}
	return "…"
}

// fit cuts plain text to at most w cells, marking the cut.
func fit(s string, w int, ell string) string {
	if w <= 0 {
		return ""
	}
	if width(s) <= w {
		return s
	}
	if w <= width(ell) {
		return ansi.Truncate(s, w, "")
	}
	return ansi.Truncate(s, w, ell)
}

// pad returns n spaces.
func pad(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// width is ansi.StringWidth, short because the layout code says it a lot.
func width(s string) int { return ansi.StringWidth(s) }
