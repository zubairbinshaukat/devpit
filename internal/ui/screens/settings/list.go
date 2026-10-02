package settings

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The list's geometry. A row is
//
//	▌ ▸ Label··············   value···············  ›
//
// lead, caret, the label column, a gap, the value column, a gap and the
// chevron of a row that opens a screen. A row that changes in place has no
// chevron, so its brief "✓ saved" runs on from its value into that room.
const (
	leadW  = 2
	caretW = 2
	gapW   = 3
	valueW = 20
	slotW  = 1
	// paneFrom is the width at which the description moves from under the
	// list to a pane on its right.
	paneFrom = 100
	// paneMax keeps the pane's lines a readable length on a wide terminal.
	paneMax = 60
	// descRows is how many lines the description gets under the list.
	descRows = 3
	// belowRows is everything under the list in the narrow layout: a blank
	// line, the rule with the focused row's name, and the description.
	belowRows = 2 + descRows
)

// saveNote is the one reassurance the screen keeps on show, quietly.
const saveNote = "Changes save as soon as you make them."

// lineKind is what one line of the list is.
type lineKind int

const (
	lineHeading lineKind = iota
	lineRow
	lineGap
)

// line is one line of the list.
type line struct {
	kind  lineKind
	title string
	row   row
	// sel is the row's place among the rows the cursor can stop on, or -1.
	sel int
}

// layout is the list as it is drawn this frame: every line, the rows the
// cursor can stop on, and where everything goes.
type layout struct {
	lines []line
	sel   []row

	pane   bool
	labelW int
	listW  int
	// top is the blank rows above the list; listH the rows it may use.
	top, listH int
	// start and end are the lines drawn; above and below the rows hidden.
	start, end   int
	above, below int
	scrolling    bool
	paneX, paneW int
	// cur is the id of the row under the cursor.
	cur string
}

// flatten turns groups into lines, with a blank line between groups when
// gaps is set.
func flatten(gs []group, gaps bool) (lines []line, sel []row) {
	for gi, g := range gs {
		if gi > 0 && gaps {
			lines = append(lines, line{kind: lineGap, sel: -1})
		}
		lines = append(lines, line{kind: lineHeading, title: g.title, sel: -1})
		for _, r := range g.rows {
			l := line{kind: lineRow, row: r, sel: -1}
			if r.kind != kindNote {
				l.sel = len(sel)
				sel = append(sel, r)
			}
			lines = append(lines, l)
		}
	}
	return lines, sel
}

// arrange works out the frame: whether the description sits beside or under
// the list, whether the groups keep their blank lines (they go first when
// room is short), and which lines are drawn so the cursor is always on
// screen. offset is the first line drawn last time; the result's start is
// the one to keep.
func arrange(ctx uictx.Context, gs []group, cursor string, offset int) layout {
	var lo layout
	lo.labelW = 0
	for _, g := range gs {
		for _, r := range g.rows {
			if r.kind != kindNote {
				lo.labelW = max(lo.labelW, ansi.StringWidth(r.label))
			}
		}
	}
	lo.listW = leadW + caretW + lo.labelW + gapW + valueW + 2 + slotW
	if ctx.Width > 0 {
		lo.listW = min(lo.listW, ctx.Width)
	}
	lo.pane = ctx.Width >= paneFrom
	if lo.pane {
		lo.paneX = lo.listW + 5
		lo.paneW = min(paneMax, ctx.Width-lo.paneX)
	}

	room := ctx.BodyHeight
	if !lo.pane {
		room -= belowRows
	}
	if ctx.BodyHeight <= 0 {
		room = 1 << 20
	}
	room = max(3, room)

	lo.lines, lo.sel = flatten(gs, true)
	if len(lo.lines) > room {
		lo.lines, lo.sel = flatten(gs, false)
	}
	lo.listH = room
	if len(lo.lines) < room {
		lo.top = 1
		lo.listH = room - 1
	}

	cur := cursorLine(lo.lines, cursor)
	if cur >= 0 {
		lo.cur = lo.lines[cur].row.id
	}
	n := len(lo.lines)
	if n <= lo.listH {
		lo.start, lo.end = 0, n
		return lo
	}
	lo.scrolling = true
	avail := max(1, lo.listH-2)
	start := min(max(0, offset), n-avail)
	if cur >= 0 {
		top := cur
		// The first row of a group brings its heading into view with it.
		if cur > 0 && lo.lines[cur-1].kind == lineHeading {
			top = cur - 1
		}
		if top < start {
			start = top
		}
		if cur >= start+avail {
			start = cur - avail + 1
		}
	}
	lo.start, lo.end = start, start+avail
	for i, l := range lo.lines {
		if l.sel < 0 {
			continue
		}
		switch {
		case i < lo.start:
			lo.above++
		case i >= lo.end:
			lo.below++
		}
	}
	return lo
}

// cursorLine is the line the row with this id is on, or the first row's.
func cursorLine(lines []line, id string) int {
	first := -1
	for i, l := range lines {
		if l.sel < 0 {
			continue
		}
		if l.row.id == id {
			return i
		}
		if first < 0 {
			first = i
		}
	}
	return first
}

// rowAt maps a body row and column to the selectable row drawn there.
// onValue is true when the column is on the value or the slot after it.
func (lo layout) rowAt(bodyRow, x int) (r row, onValue, ok bool) {
	i := bodyRow - lo.top
	if lo.scrolling {
		i-- // the "more" line above
	}
	if i < 0 || lo.start+i >= lo.end || x >= lo.listW {
		return row{}, false, false
	}
	l := lo.lines[lo.start+i]
	if l.sel < 0 {
		return row{}, false, false
	}
	return l.row, x >= leadW+caretW+lo.labelW+gapW-1, true
}

// view draws the frame.
func (m Model) view(ctx uictx.Context, lo layout) string {
	list := m.listLines(ctx, lo)
	focused, _ := m.focused(lo)
	if lo.pane {
		pane := m.paneLines(ctx, lo, focused)
		rule := ctx.Theme.Rule.Render(bar(ctx))
		height := max(len(list), len(pane), ctx.BodyHeight)
		out := make([]string, height)
		for i := range height {
			left := ""
			if i < len(list) {
				left = list[i]
			}
			left += strings.Repeat(" ", max(0, lo.listW-ansi.StringWidth(left)))
			right := ""
			if i < len(pane) {
				right = pane[i]
			}
			out[i] = strings.TrimRight(left+"  "+rule+"  "+right, " ")
		}
		return strings.Join(out, "\n")
	}
	for len(list) < lo.top+lo.listH {
		list = append(list, "")
	}
	list = append(list, "", m.belowRule(ctx, lo, focused))
	list = append(list, m.descLines(ctx, focused, min(ctx.Width-4, 84), descRows, 2)...)
	return strings.Join(list, "\n")
}

// listLines draws the visible part of the list.
func (m Model) listLines(ctx uictx.Context, lo layout) []string {
	ascii := ctx.Icons.Tier == icons.TierASCII
	out := make([]string, 0, lo.top+lo.listH)
	for range lo.top {
		out = append(out, "")
	}
	if lo.scrolling {
		out = append(out, more(ctx, lo.above, up(ascii)))
	}
	for i := lo.start; i < lo.end; i++ {
		l := lo.lines[i]
		switch l.kind {
		case lineGap:
			out = append(out, "")
		case lineHeading:
			out = append(out, heading(ctx, lo, l.title))
		default:
			out = append(out, m.rowLine(ctx, lo, l.row))
		}
	}
	if lo.scrolling {
		out = append(out, more(ctx, lo.below, down(ascii)))
	}
	return out
}

// heading is a group's name with a thin rule after it to where the
// chevrons stand, so the groups read apart even when room is short and the
// blank lines between them have gone.
func heading(ctx uictx.Context, lo layout, title string) string {
	w := lo.listW - slotW + 1 - 2
	rule := w - ansi.StringWidth(title) - 1
	if rule < 2 {
		return "  " + ctx.Theme.Title.Render(title)
	}
	return "  " + ctx.Theme.Title.Render(title) + " " + ctx.Theme.Rule.Render(strings.Repeat(ruleGlyph(ctx), rule))
}

// rowLine draws one row. The highlighted row is the house's lifted band: a
// bar at its left edge, the caret, and every piece on the band to the end
// of the list column.
func (m Model) rowLine(ctx uictx.Context, lo layout, r row) string {
	th := ctx.Theme
	if r.kind == kindNote {
		indent := leadW + caretW
		return strings.Repeat(" ", indent) + th.Muted.Render(fit(ctx, r.label, lo.listW-indent))
	}
	selected := r.id == lo.cur

	label := fit(ctx, r.label, lo.labelW)
	label += strings.Repeat(" ", lo.labelW-ansi.StringWidth(label))
	pieces := m.valuePieces(ctx, r, selected)
	if r.id == m.savedID && r.kind != kindOpen {
		pieces = append(pieces, piece{"  " + ctx.Icons.Tick + " saved", th.Success})
	}
	valW := 0
	for _, p := range pieces {
		valW += ansi.StringWidth(p.text)
	}
	chev := m.chevron(ctx, r, selected)

	if !selected {
		var b strings.Builder
		b.WriteString(strings.Repeat(" ", leadW+caretW))
		b.WriteString(th.Base.Render(label))
		b.WriteString(strings.Repeat(" ", gapW))
		for _, p := range pieces {
			b.WriteString(p.style.Render(p.text))
		}
		if chev.text != "" {
			b.WriteString(strings.Repeat(" ", max(0, valueW-valW)+2))
			b.WriteString(chev.style.Render(chev.text))
		}
		return b.String()
	}

	band := th.SelBand
	barGlyph := ctx.Icons.SelectBar
	var b strings.Builder
	b.WriteString(th.SelectBar.Render(barGlyph))
	b.WriteString(band.Render(strings.Repeat(" ", leadW-cellWidth(barGlyph))))
	b.WriteString(th.OnBand(th.Cursor).Render(ctx.Icons.Cursor))
	b.WriteString(band.Render(strings.Repeat(" ", caretW-cellWidth(ctx.Icons.Cursor))))
	b.WriteString(th.Selected.Render(label))
	b.WriteString(band.Render(strings.Repeat(" ", gapW)))
	for _, p := range pieces {
		b.WriteString(th.OnBand(p.style).Render(p.text))
	}
	used := leadW + caretW + lo.labelW + gapW + valW
	if chev.text != "" {
		pad := max(0, valueW-valW) + 2
		b.WriteString(band.Render(strings.Repeat(" ", pad)))
		b.WriteString(th.OnBand(chev.style).Render(chev.text))
		used += pad + ansi.StringWidth(chev.text)
	}
	if n := lo.listW - used; n > 0 {
		b.WriteString(band.Render(strings.Repeat(" ", n)))
	}
	return b.String()
}

// piece is a run of text in one style.
type piece struct {
	text  string
	style lipgloss.Style
}

// valuePieces is a row's value: ‹ auto › for a value that cycles, a dot and
// the word for on and off, the plain value for a row that opens a screen.
// Shape and word always come together, so nothing is told by colour alone.
func (m Model) valuePieces(ctx uictx.Context, r row, selected bool) []piece {
	th := ctx.Theme
	ascii := ctx.Icons.Tier == icons.TierASCII
	arrow := th.Muted
	if selected {
		arrow = th.Accent
	}
	switch r.kind {
	case kindCycle:
		l, rt := "‹", "›"
		if ascii {
			l, rt = "<", ">"
		}
		return []piece{{l + " ", arrow}, {fit(ctx, r.value, valueW-4), th.Info}, {" " + rt, arrow}}
	case kindToggle:
		on, off := "●", "○"
		if ascii {
			on, off = ctx.Icons.Checked, ctx.Icons.Unchecked
		}
		if r.on {
			return []piece{{on + " on", th.Success}}
		}
		return []piece{{off + " off", th.Muted}}
	}
	if r.value == "" {
		return nil
	}
	v := tierText(r.value, ascii)
	if r.path {
		v = shortenPath(v, valueW, ellipsis(ctx))
	} else {
		v = fit(ctx, v, valueW)
	}
	st := th.Info
	switch {
	case r.quiet:
		st = th.Muted
	case r.on:
		st = th.Success
	}
	return []piece{{v, st}}
}

// chevron is the mark after the value of a row that opens a screen.
func (m Model) chevron(ctx uictx.Context, r row, selected bool) piece {
	th := ctx.Theme
	if r.kind != kindOpen {
		return piece{}
	}
	chev := "›"
	if ctx.Icons.Tier == icons.TierASCII {
		chev = ">"
	}
	if selected {
		return piece{chev, th.Accent}
	}
	return piece{chev, th.Muted}
}

// focused is the row under the cursor.
func (m Model) focused(lo layout) (row, bool) {
	i := cursorLine(lo.lines, lo.cur)
	if i < 0 {
		return row{}, false
	}
	return lo.lines[i].row, true
}

// paneLines is the description beside the list: the setting's name, what it
// does in plain words, the keys that change it and, at the bottom, the
// reminder that changes save themselves.
func (m Model) paneLines(ctx uictx.Context, lo layout, r row) []string {
	th := ctx.Theme
	out := make([]string, 0, ctx.BodyHeight)
	for range lo.top {
		out = append(out, "")
	}
	out = append(out, th.Title.Render(fit(ctx, r.label, lo.paneW)), "")
	out = append(out, m.descLines(ctx, r, lo.paneW, 0, 0)...)
	if h := keyHelp(ctx, r); h != "" {
		out = append(out, "", h)
	}
	if ctx.BodyHeight > 0 {
		for len(out) < ctx.BodyHeight-1 {
			out = append(out, "")
		}
	} else {
		out = append(out, "")
	}
	return append(out, th.Muted.Render(fit(ctx, saveNote, lo.paneW)))
}

// keyHelp says how to change the focused row.
func keyHelp(ctx uictx.Context, r row) string {
	switch r.kind {
	case kindCycle:
		lr := "←→"
		if ctx.Icons.Tier == icons.TierASCII {
			lr = "left/right"
		}
		return ctx.KeyHint("enter", "next") + "  " + ctx.KeyHint(lr, "choose")
	case kindToggle:
		if r.on {
			return ctx.KeyHint("enter", "turn off")
		}
		return ctx.KeyHint("enter", "turn on")
	case kindOpen:
		return ctx.KeyHint("enter", "open")
	}
	return ""
}

// belowRule is the line between the list and the description in the narrow
// layout: the focused row's name, a rule, and the save reminder.
func (m Model) belowRule(ctx uictx.Context, lo layout, r row) string {
	th := ctx.Theme
	w := max(20, min(ctx.Width-2, 86))
	name := fit(ctx, r.label, w/2)
	note := saveNote
	ruleW := w - 2 - ansi.StringWidth(name) - 2 - ansi.StringWidth(note) - 1
	if ruleW < 3 {
		note = ""
		ruleW = w - 2 - ansi.StringWidth(name) - 1
	}
	s := "  " + th.Title.Render(name) + " " + th.Rule.Render(strings.Repeat(ruleGlyph(ctx), max(0, ruleW)))
	if note != "" {
		s += " " + th.Muted.Render(note)
	}
	return s
}

// descLines is the focused row's description wrapped to width, cut to most
// lines (0: no limit) and indented.
func (m Model) descLines(ctx uictx.Context, r row, width, most, indent int) []string {
	th := ctx.Theme
	width = max(16, width)
	text := tierText(r.desc, ctx.Icons.Tier == icons.TierASCII)
	ls := strings.Split(ansi.Wrap(text, width, " "), "\n")
	if most > 0 && len(ls) > most {
		ls = ls[:most]
		ls[most-1] = fit(ctx, strings.TrimRight(ls[most-1], " ")+" "+ellipsis(ctx), width)
	}
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, strings.Repeat(" ", indent)+th.Base.Render(strings.TrimRight(l, " ")))
	}
	for most > 0 && len(out) < most {
		out = append(out, "")
	}
	return out
}

// more is the "n more" line of a list that scrolls, or a blank line when
// that end is on screen.
func more(ctx uictx.Context, n int, arrow string) string {
	if n <= 0 {
		return ""
	}
	return "  " + ctx.Theme.Muted.Render(arrow+" "+strconv.Itoa(n)+" more")
}

func up(ascii bool) string {
	if ascii {
		return "^"
	}
	return "▲"
}

func down(ascii bool) string {
	if ascii {
		return "v"
	}
	return "▼"
}

// bar is the pane's divider, ruleGlyph the narrow layout's rule.
func bar(ctx uictx.Context) string {
	if ctx.Icons.Tier == icons.TierASCII {
		return "|"
	}
	return "│"
}

func ruleGlyph(ctx uictx.Context) string {
	if ctx.Icons.Tier == icons.TierASCII {
		return "-"
	}
	return "─"
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

// shortenPath cuts a path in the middle so its drive and last folder stay:
// D:\work\…\shop. A path with no separator to keep is cut at the end.
func shortenPath(p string, w int, ell string) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(p) <= w {
		return p
	}
	i := strings.LastIndexAny(p, `\/`)
	j := strings.IndexAny(p, `\/`)
	if i <= j || j < 0 {
		return ansi.Truncate(p, w, ell)
	}
	head, last := p[:j+1], p[i:]
	room := w - ansi.StringWidth(head) - ansi.StringWidth(ell) - ansi.StringWidth(last)
	if room < 0 {
		return ansi.TruncateLeft(p, ansi.StringWidth(p)-w+ansi.StringWidth(ell), ell)
	}
	return head + ansi.Truncate(p[j+1:i], room, "") + ell + last
}

// cellWidth is the printed width of a one-cell glyph, or zero for none.
func cellWidth(s string) int {
	if s == "" {
		return 0
	}
	return 1
}
