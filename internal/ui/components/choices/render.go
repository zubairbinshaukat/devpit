package choices

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
	slotW  = 1
	// valueMax caps a value column sized to its values.
	valueMax = 24
	// paneFrom is the width at which the description moves from under the
	// list to a pane on its right.
	paneFrom = 100
	// paneMax keeps the pane's lines a readable length on a wide terminal.
	paneMax = 60
	// descRows is how many lines the description gets under the list, and
	// descShort how many when that is what lets the list fit.
	descRows  = 3
	descShort = 2
	// factIndent is where a fact's label starts.
	factIndent = 4
)

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
	item  Item
	// sel is the row's place among the rows the cursor can stop on, or -1.
	sel int
}

// layout is the frame as it is drawn: the facts, every line of the list,
// the rows the cursor can stop on, and where everything goes.
type layout struct {
	facts []string
	lines []line
	sel   []Item

	pane   bool
	labelW int
	valueW int
	listW  int
	// top is the blank rows above the list; listH the rows it may use.
	top, listH int
	// descN is how many description lines go under the list (narrow), and
	// blank whether a blank line sits above the rule there.
	descN int
	blank bool
	// start and end are the lines drawn; above and below the rows hidden.
	start, end   int
	above, below int
	scrolling    bool
	paneW        int
	// cur is the id of the row under the cursor.
	cur string
}

// valueX is the column values start at.
func (lo layout) valueX() int { return leadW + caretW + lo.labelW + gapW }

// height is how many lines the frame draws.
func (lo layout) height() int {
	n := len(lo.facts) + lo.top + lo.listH
	if !lo.pane {
		n += 1 + lo.descN
		if lo.blank {
			n++
		}
	}
	return n
}

// flatten turns groups into lines, with a blank line between groups when
// gaps is set. A group without a title has no heading line.
func flatten(gs []Group, gaps bool) (lines []line, sel []Item) {
	for gi, g := range gs {
		if gi > 0 && gaps {
			lines = append(lines, line{kind: lineGap, sel: -1})
		}
		if g.Title != "" {
			lines = append(lines, line{kind: lineHeading, title: g.Title, sel: -1})
		}
		for _, it := range g.Items {
			l := line{kind: lineRow, item: it, sel: -1}
			if it.Kind != Note {
				l.sel = len(sel)
				sel = append(sel, it)
			}
			lines = append(lines, l)
		}
	}
	return lines, sel
}

// valueCell is how wide an item's value column content is.
func valueCell(it Item) int {
	switch it.Kind {
	case Cycles:
		return ansi.StringWidth(it.Value) + 4
	case Toggles:
		return 5
	case Note:
		return 0
	}
	if it.Value == "" && it.Disabled != "" {
		return ansi.StringWidth(unavailable)
	}
	return ansi.StringWidth(it.Value)
}

// unavailable is the value a disabled row shows when it has none.
const unavailable = "not available"

// arrange works out the frame. offset is the first list line drawn last
// time; the result's start is the one to keep.
func (m Model) arrange(ctx uictx.Context, spec Spec) layout {
	var lo layout
	for _, g := range spec.Groups {
		for _, it := range g.Items {
			if it.Kind == Note {
				continue
			}
			lo.labelW = max(lo.labelW, ansi.StringWidth(it.Label))
			lo.valueW = max(lo.valueW, valueCell(it))
		}
	}
	lo.valueW = min(lo.valueW, valueMax)
	if spec.ValueWidth > 0 {
		lo.valueW = spec.ValueWidth
	}
	lo.listW = leadW + caretW + lo.labelW + 2 + slotW
	if lo.valueW > 0 {
		lo.listW += gapW + lo.valueW
	}
	if ctx.Width > 0 {
		lo.listW = min(lo.listW, ctx.Width)
	}
	lo.pane = ctx.Width >= paneFrom
	if lo.pane {
		lo.paneW = min(paneMax, ctx.Width-lo.listW-6)
	}
	lo.facts = factLines(ctx, spec, lo)

	room := 1 << 20
	if ctx.BodyHeight > 0 {
		room = max(3, ctx.BodyHeight-spec.Top-len(lo.facts))
	}
	gapped, _ := flatten(spec.Groups, true)
	tight, _ := flatten(spec.Groups, false)
	full := 2 + descRows
	short := 1 + descShort
	lo.descN, lo.blank = descRows, true
	switch {
	case lo.pane && len(gapped) <= room:
		lo.lines, lo.sel = flatten(spec.Groups, true)
		lo.listH = room
	case lo.pane:
		lo.lines, lo.sel = flatten(spec.Groups, false)
		lo.listH = room
		if len(lo.facts) > 0 && len(tight) > room {
			lo.facts = lo.facts[:len(lo.facts)-1]
			lo.listH++
		}
	case len(gapped) <= room-full:
		lo.lines, lo.sel = flatten(spec.Groups, true)
		lo.listH = room - full
	case len(tight) <= room-full:
		lo.lines, lo.sel = flatten(spec.Groups, false)
		lo.listH = room - full
	case len(tight) <= room-full+1:
		// The blank line above the description goes next.
		lo.lines, lo.sel = flatten(spec.Groups, false)
		lo.blank = false
		lo.listH = room - full + 1
	case len(lo.facts) > 0 && len(tight) <= room-full+2:
		// Then the blank line under the facts, before any words are cut.
		lo.lines, lo.sel = flatten(spec.Groups, false)
		lo.blank = false
		lo.facts = lo.facts[:len(lo.facts)-1]
		lo.listH = room - full + 2
	case len(tight) <= room-short:
		lo.lines, lo.sel = flatten(spec.Groups, false)
		lo.descN, lo.blank = descShort, false
		lo.listH = room - short
	case len(lo.facts) > 0 && len(tight) <= room+1-short:
		// Last before scrolling: the blank line under the facts goes. The
		// first group's heading, with its rule, still sets the list apart.
		lo.lines, lo.sel = flatten(spec.Groups, false)
		lo.descN, lo.blank = descShort, false
		lo.facts = lo.facts[:len(lo.facts)-1]
		lo.listH = room + 1 - short
	default:
		lo.lines, lo.sel = flatten(spec.Groups, false)
		lo.listH = max(3, room-full)
		if len(lo.facts) > 0 {
			// A list that scrolls opens with its "more" line, blank at the
			// top, which already separates it from the facts.
			lo.facts = lo.facts[:len(lo.facts)-1]
			lo.listH++
		}
	}
	if ctx.BodyHeight <= 0 {
		lo.listH = len(lo.lines)
	}
	// Without facts above it the list gets a blank line of air at the top
	// when there is room for one.
	if len(lo.facts) == 0 && len(lo.lines) < lo.listH {
		lo.top = 1
		lo.listH--
	}

	cur := cursorLine(lo.lines, m.cursor)
	if cur >= 0 {
		lo.cur = lo.lines[cur].item.ID
	}
	n := len(lo.lines)
	if n <= lo.listH {
		lo.start, lo.end = 0, n
		return lo
	}
	lo.scrolling = true
	avail := max(1, lo.listH-2)
	start := min(max(0, m.offset), n-avail)
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
		if l.item.ID == id {
			return i
		}
		if first < 0 {
			first = i
		}
	}
	return first
}

// itemAt maps a row of the component (0 is its first line) and a column to
// the row drawn there. onValue is true on the value or the slot after it.
func (lo layout) itemAt(y, x int) (it Item, onValue, ok bool) {
	i := y - len(lo.facts) - lo.top
	if lo.scrolling {
		i-- // the "more" line above
	}
	if i < 0 || lo.start+i >= lo.end || x >= lo.listW {
		return Item{}, false, false
	}
	l := lo.lines[lo.start+i]
	if l.sel < 0 {
		return Item{}, false, false
	}
	return l.item, lo.valueW > 0 && x >= lo.valueX()-1, true
}

// render draws the frame.
func (m Model) render(ctx uictx.Context, spec Spec, lo layout) string {
	out := append([]string(nil), lo.facts...)
	list := m.listLines(ctx, lo)
	focused, _ := m.focusedIn(lo)
	if lo.pane {
		pane := m.paneLines(ctx, spec, lo, focused)
		rule := ctx.Theme.Rule.Render(bar(ctx))
		height := max(len(list), len(pane), lo.top+lo.listH)
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
			out = append(out, strings.TrimRight(left+"  "+rule+"  "+right, " "))
		}
		return strings.Join(out, "\n")
	}
	for len(list) < lo.top+lo.listH {
		list = append(list, "")
	}
	out = append(out, list...)
	if lo.blank {
		out = append(out, "")
	}
	out = append(out, m.belowRule(ctx, spec, focused))
	out = append(out, descLines(ctx, belowText(focused), min(ctx.Width-4, 84), lo.descN, 2)...)
	return strings.Join(out, "\n")
}

// focusedIn is the row under the cursor in a layout.
func (m Model) focusedIn(lo layout) (Item, bool) {
	i := cursorLine(lo.lines, lo.cur)
	if i < 0 {
		return Item{}, false
	}
	return lo.lines[i].item, true
}

// factLines is the "what is true now" block: a heading, then each fact as
// a muted label, the value, and quiet lines under it. A blank line ends it.
func factLines(ctx uictx.Context, spec Spec, lo layout) []string {
	if len(spec.Facts) == 0 && spec.FactsTitle == "" {
		return nil
	}
	th := ctx.Theme
	ic := ctx.Icons
	w := ctx.Width
	if w <= 0 {
		w = 100
	}
	var out []string
	if spec.FactsTitle != "" {
		out = append(out, titledRule(ctx, spec.FactsTitle, spec.FactsAside, max(lo.listW-slotW+1, min(w-1, 96))))
	}
	labelW := 0
	for _, f := range spec.Facts {
		labelW = max(labelW, ansi.StringWidth(f.Label))
	}
	valX := factIndent + labelW + 2
	valW := max(10, w-valX-1)
	for _, f := range spec.Facts {
		v := tierText(f.Value, ic.Tier == icons.TierASCII)
		st := th.Base.Bold(true)
		switch f.Tone {
		case Good:
			v, st = ic.Tick+" "+v, th.Success
		case Warn:
			v, st = ic.Warn+" "+v, th.Warning
		case Bad:
			v, st = ic.Fail+" "+v, th.Danger
		}
		label := f.Label + strings.Repeat(" ", labelW-ansi.StringWidth(f.Label))
		lead := strings.Repeat(" ", factIndent) + th.Muted.Render(label) + "  "
		switch {
		case f.Wrap:
			ls := wrapped(ctx, st, v, valX, valW)
			ls[0] = lead + strings.TrimLeft(ls[0], " ")
			out = append(out, ls...)
		case f.Path:
			out = append(out, lead+st.Render(shortenPath(v, valW, ellipsis(ctx))))
		default:
			out = append(out, lead+st.Render(fit(ctx, v, valW)))
		}
		for _, n := range f.Notes {
			out = append(out, wrapped(ctx, th.Muted, n, valX, valW)...)
		}
		for _, n := range f.Warnings {
			out = append(out, wrapped(ctx, th.Warning, ic.Warn+" "+n, valX, valW)...)
		}
	}
	return append(out, "")
}

// wrapped folds text to width, indented by indent, in st.
func wrapped(ctx uictx.Context, st lipgloss.Style, text string, indent, width int) []string {
	text = tierText(text, ctx.Icons.Tier == icons.TierASCII)
	var out []string
	for _, l := range strings.Split(ansi.Wrap(text, max(10, width), " "), "\n") {
		out = append(out, strings.Repeat(" ", indent)+st.Render(strings.TrimRight(l, " ")))
	}
	return out
}

// titledRule is a heading with a thin rule after it to width, and a quiet
// aside at its right end when there is room.
func titledRule(ctx uictx.Context, title, aside string, width int) string {
	th := ctx.Theme
	head := "  " + th.Title.Render(title)
	used := 2 + ansi.StringWidth(title)
	if aside != "" {
		room := width - used - 6
		if room >= 12 {
			aside = shortenPath(tierText(aside, ctx.Icons.Tier == icons.TierASCII), room, ellipsis(ctx))
			rule := width - used - 2 - ansi.StringWidth(aside)
			return head + " " + th.Rule.Render(strings.Repeat(ruleGlyph(ctx), max(2, rule))) + " " + th.Muted.Render(aside)
		}
	}
	rule := width - used - 1
	if rule < 2 {
		return head
	}
	return head + " " + th.Rule.Render(strings.Repeat(ruleGlyph(ctx), rule))
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
			out = append(out, titledRule(ctx, l.title, "", lo.listW-slotW+1))
		default:
			out = append(out, m.rowLine(ctx, lo, l.item))
		}
	}
	if lo.scrolling {
		out = append(out, more(ctx, lo.below, down(ascii)))
	}
	return out
}

// rowLine draws one row. The highlighted row is the house's lifted band: a
// bar at its left edge, the caret, and every piece on the band to the end
// of the list column.
func (m Model) rowLine(ctx uictx.Context, lo layout, it Item) string {
	th := ctx.Theme
	if it.Kind == Note {
		indent := leadW + caretW
		return strings.Repeat(" ", indent) + th.Muted.Render(fit(ctx, it.Label, lo.listW-indent))
	}
	selected := it.ID == lo.cur
	off := it.Disabled != ""

	label := fit(ctx, it.Label, lo.labelW)
	label += strings.Repeat(" ", lo.labelW-ansi.StringWidth(label))
	pieces := m.valuePieces(ctx, lo, it, selected)
	if it.ID == m.saved && it.Kind != Opens {
		pieces = append(pieces, piece{"  " + ctx.Icons.Tick + " saved", th.Success})
	}
	valW := 0
	for _, p := range pieces {
		valW += ansi.StringWidth(p.text)
	}
	chev := chevron(ctx, it, selected)
	gap := gapW
	if lo.valueW == 0 {
		gap = 0
	}
	labelStyle := th.Base
	if off {
		labelStyle = th.Muted
	}

	if !selected {
		var b strings.Builder
		b.WriteString(strings.Repeat(" ", leadW+caretW))
		b.WriteString(labelStyle.Render(label))
		b.WriteString(strings.Repeat(" ", gap))
		for _, p := range pieces {
			b.WriteString(p.style.Render(p.text))
		}
		if chev.text != "" {
			b.WriteString(strings.Repeat(" ", max(0, lo.valueW-valW)+2))
			b.WriteString(chev.style.Render(chev.text))
		}
		return b.String()
	}

	band := th.SelBand
	barGlyph := ctx.Icons.SelectBar
	sel := th.Selected
	if off {
		sel = th.OnBand(th.Muted)
	}
	var b strings.Builder
	b.WriteString(th.SelectBar.Render(barGlyph))
	b.WriteString(band.Render(strings.Repeat(" ", leadW-cellWidth(barGlyph))))
	b.WriteString(th.OnBand(th.Cursor).Render(ctx.Icons.Cursor))
	b.WriteString(band.Render(strings.Repeat(" ", caretW-cellWidth(ctx.Icons.Cursor))))
	b.WriteString(sel.Render(label))
	b.WriteString(band.Render(strings.Repeat(" ", gap)))
	for _, p := range pieces {
		b.WriteString(th.OnBand(p.style).Render(p.text))
	}
	used := leadW + caretW + lo.labelW + gap + valW
	if chev.text != "" {
		pad := max(0, lo.valueW-valW) + 2
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

// valuePieces is a row's value: ‹ auto › for a value that steps in place, a
// dot and the word for on and off, the plain value otherwise. Shape and word
// always come together, so nothing is told by colour alone.
func (m Model) valuePieces(ctx uictx.Context, lo layout, it Item, selected bool) []piece {
	th := ctx.Theme
	ascii := ctx.Icons.Tier == icons.TierASCII
	if lo.valueW == 0 {
		return nil
	}
	if it.Disabled != "" && it.Value == "" {
		return []piece{{fit(ctx, unavailable, lo.valueW), th.Muted}}
	}
	arrow := th.Muted
	if selected {
		arrow = th.Accent
	}
	switch it.Kind {
	case Cycles:
		l, r := "‹", "›"
		if ascii {
			l, r = "<", ">"
		}
		return []piece{{l + " ", arrow}, {fit(ctx, it.Value, lo.valueW-4), th.Info}, {" " + r, arrow}}
	case Toggles:
		on, off := "●", "○"
		if ascii {
			on, off = ctx.Icons.Checked, ctx.Icons.Unchecked
		}
		if it.On {
			return []piece{{on + " on", th.Success}}
		}
		return []piece{{off + " off", th.Muted}}
	}
	if it.Value == "" {
		return nil
	}
	v := tierText(it.Value, ascii)
	if it.Path {
		v = shortenPath(v, lo.valueW, ellipsis(ctx))
	} else {
		v = fit(ctx, v, lo.valueW)
	}
	st := th.Info
	switch {
	case it.Quiet || it.Disabled != "":
		st = th.Muted
	case it.On:
		st = th.Success
	}
	return []piece{{v, st}}
}

// chevron is the mark after a row that opens a screen.
func chevron(ctx uictx.Context, it Item, selected bool) piece {
	th := ctx.Theme
	if it.Kind != Opens || it.Disabled != "" {
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

// paneLines is the description beside the list: the row's name, what it
// does, what happens next, why it is off when it is, the key that acts and,
// at the foot, the screen's reminder.
func (m Model) paneLines(ctx uictx.Context, spec Spec, lo layout, it Item) []string {
	th := ctx.Theme
	height := lo.top + lo.listH
	out := make([]string, 0, height)
	for range lo.top {
		out = append(out, "")
	}
	if it.ID == "" {
		return out
	}
	out = append(out, th.Title.Render(fit(ctx, it.Label, lo.paneW)), "")
	out = append(out, descLines(ctx, it.Desc, lo.paneW, 0, 0)...)
	if it.Next != "" {
		out = append(out, "")
		out = append(out, descLines(ctx, it.Next, lo.paneW, 0, 0)...)
	}
	if it.Disabled != "" {
		out = append(out, "")
		out = append(out, wrapped(ctx, th.Warning, ctx.Icons.Warn+" Not available now: "+it.Disabled, 0, lo.paneW)...)
	}
	if h := keyHelp(ctx, it); h != "" {
		out = append(out, "", h)
	}
	if spec.Note == "" {
		return out
	}
	if ctx.BodyHeight > 0 {
		for len(out) < height-1 {
			out = append(out, "")
		}
	} else {
		out = append(out, "")
	}
	return append(out, th.Muted.Render(fit(ctx, spec.Note, lo.paneW)))
}

// belowText is the description under the list: what the row does, what
// happens next, and why it is off.
func belowText(it Item) string {
	parts := []string{it.Desc}
	if it.Next != "" {
		parts = append(parts, it.Next)
	}
	if it.Disabled != "" {
		parts = append(parts, "Not available now: "+it.Disabled)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// keyHelp says how to act on the focused row.
func keyHelp(ctx uictx.Context, it Item) string {
	if it.Disabled != "" {
		return ""
	}
	switch it.Kind {
	case Cycles:
		lr := "←→"
		if ctx.Icons.Tier == icons.TierASCII {
			lr = "left/right"
		}
		return ctx.KeyHint("enter", "next") + "  " + ctx.KeyHint(lr, "choose")
	case Toggles:
		if it.On {
			return ctx.KeyHint("enter", "turn off")
		}
		return ctx.KeyHint("enter", "turn on")
	case Opens:
		return ctx.KeyHint("enter", "open")
	}
	return ""
}

// belowRule is the line between the list and the description in the narrow
// layout: the focused row's name, a rule, and the screen's reminder.
func (m Model) belowRule(ctx uictx.Context, spec Spec, it Item) string {
	th := ctx.Theme
	w := max(20, min(ctx.Width-2, 86))
	name := fit(ctx, it.Label, w/2)
	note := spec.Note
	ruleW := w - 2 - ansi.StringWidth(name) - 2 - ansi.StringWidth(note) - 1
	if note == "" || ruleW < 3 {
		note = ""
		ruleW = w - 2 - ansi.StringWidth(name) - 1
	}
	s := "  " + th.Title.Render(name) + " " + th.Rule.Render(strings.Repeat(ruleGlyph(ctx), max(0, ruleW)))
	if note != "" {
		s += " " + th.Muted.Render(note)
	}
	return s
}

// descLines is text wrapped to width, cut to most lines (0: no limit) and
// indented. Cut text ends with the tier's ellipsis.
func descLines(ctx uictx.Context, text string, width, most, indent int) []string {
	th := ctx.Theme
	width = max(16, width)
	text = tierText(text, ctx.Icons.Tier == icons.TierASCII)
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

// bar is the pane's divider, ruleGlyph the rules'.
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
	return strings.NewReplacer("…", "...", "–", "-", "›", ">", "→", "->", "—", "-", "·", "-").Replace(s)
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

// ShortenPath cuts a path in the middle so its drive and last folder stay:
// D:\work\…\shop. A path with no separator to keep is cut at the end.
func ShortenPath(p string, w int, ell string) string { return shortenPath(p, w, ell) }

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
