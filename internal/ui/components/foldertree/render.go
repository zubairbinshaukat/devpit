package foldertree

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/acctable"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The fixed parts of a row.
const (
	// leftPad is the margin before the caret, matching the menus.
	leftPad = 2
	// leadW is the gutter: margin, caret and the space after it.
	leadW = 4
	// step is the width of one level of tree guides.
	step = 3
	// twistyW is the fold arrow and the space after it.
	twistyW = 2
	// chipsMin is the narrowest the accounts column is squeezed to.
	chipsMin = 18
	// maxChipLines caps how many lines one folder's accounts may wrap onto;
	// the rest are counted ("+2") rather than drawn.
	maxChipLines = 2
	// nameMin is the narrowest a folder name is cut to before the row lets
	// its accounts start further right instead.
	nameMin = 6
)

// Column headings.
const (
	headFolder   = "FOLDER"
	headAccounts = "ACCOUNTS SET HERE"
	headShort    = "ACCOUNTS"
)

// guides are the tree-drawing pieces for a tier, each one level wide.
type guides struct {
	branch, last, pipe, blank string
}

// guidesFor returns the box-drawing guides, or their ASCII stand-ins.
func guidesFor(ctx uictx.Context) guides {
	if ctx.Icons.Tier == icons.TierASCII {
		return guides{branch: "|- ", last: "`- ", pipe: "|  ", blank: "   "}
	}
	return guides{branch: "├─ ", last: "└─ ", pipe: "│  ", blank: "   "}
}

// layout is the geometry of one frame.
type layout struct {
	width  int
	ell    string
	sep    string
	gap    int
	chipsX int
}

// layout places the accounts column: just past the longest folder name, so
// the tree and its accounts read as two columns, but never so far right
// that the accounts have less than [chipsMin] cells or the tree more than
// about half the width.
func (m Model) layout(ctx uictx.Context) layout {
	l := layout{width: m.viewWidth(ctx), ell: ellipsis(ctx), sep: " · ", gap: 3}
	if ctx.Icons.Tier == icons.TierASCII {
		l.sep = " - "
	}
	if l.width < 100 {
		l.gap = 2
	}
	tree := leadW + width(headFolder)
	for _, r := range m.rows {
		tree = max(tree, m.nameX(r)+width(m.label(r))+m.suffixWidth(r))
	}
	limit := min(l.width*11/20, l.width-chipsMin-l.gap)
	l.chipsX = min(tree, max(leadW+nameMin, limit)) + l.gap
	return l
}

// nameX is the column a row's name starts in.
func (m Model) nameX(r row) int {
	d := m.items[r.item].depth
	if d == 0 {
		return leadW
	}
	return leadW + step*d + twistyW
}

// label is the name a row shows.
func (m Model) label(r row) string {
	it := m.items[r.item]
	if it.name != "" {
		return it.name
	}
	return it.path
}

// suffix is what follows a row's name: "(here)" on the current folder, and
// how many folders a fold is hiding.
func (m Model) suffix(r row) []string {
	var out []string
	if m.items[r.item].current {
		out = append(out, "(here)")
	}
	if r.hidden > 0 {
		out = append(out, "+"+strconv.Itoa(r.hidden))
	}
	return out
}

// suffixWidth is the width of a row's suffix, with the space before each
// part.
func (m Model) suffixWidth(r row) int {
	w := 0
	for _, s := range m.suffix(r) {
		w += 1 + width(s)
	}
	return w
}

// token is one entry in the accounts column: a chip, or a folder's state.
type token []piece

// piece is one run of text and its colour.
type piece struct {
	text  string
	style lipgloss.Style
}

// width is the token's width in cells.
func (t token) width() int {
	w := 0
	for _, p := range t {
		w += width(p.text)
	}
	return w
}

// tokens is what a row puts in the accounts column: its state first, when
// the folder is stale, then one token per rule.
func (m Model) tokens(ctx uictx.Context, r row) []token {
	th := ctx.Theme
	it := m.items[r.item]
	var out []token
	switch it.state {
	case StateMissing:
		out = append(out, token{{ctx.Icons.Fail + " " + it.state.Word(), th.Danger}})
	case StateOffline:
		out = append(out, token{{ctx.Icons.Warn + " " + it.state.Word(), th.Warning}})
	}
	for _, c := range it.chips {
		if c.Tool == "" {
			out = append(out, token{{c.Account, th.Base}})
			continue
		}
		out = append(out, token{{c.Tool, th.Muted}, {" ", th.Muted}, {c.Account, th.Base}})
	}
	if r.item == 0 && len(it.chips) == 0 {
		out = append(out, token{{"each tool's own default", th.Muted}})
	}
	return out
}

// pack lays tokens out on at most maxChipLines lines of w cells, whole
// tokens only, joined by the separator. Tokens that do not fit are counted
// in a "+n" at the end of the last line. A single token wider than a whole
// line is cut.
func pack(ctx uictx.Context, l layout, toks []token, w int) [][]token {
	if len(toks) == 0 {
		return nil
	}
	sepW := width(l.sep)
	var lines [][]token
	var cur []token
	used := 0
	for i, t := range toks {
		tw := t.width()
		need := tw
		if len(cur) > 0 {
			need += sepW
		}
		if used+need <= w || len(cur) == 0 {
			if tw > w {
				t = cut(t, w, l.ell)
				tw = t.width()
				need = tw
			}
			cur = append(cur, t)
			used += need
			continue
		}
		lines = append(lines, cur)
		if len(lines) == maxChipLines {
			return withMore(ctx, l, lines, len(toks)-i, w)
		}
		cur, used = []token{cut(t, w, l.ell)}, min(tw, w)
	}
	return append(lines, cur)
}

// withMore ends the last line with "+n", dropping tokens from it until the
// count fits.
func withMore(ctx uictx.Context, l layout, lines [][]token, n, w int) [][]token {
	last := lines[len(lines)-1]
	for {
		more := token{{"+" + strconv.Itoa(n), ctx.Theme.Muted}}
		used := 0
		for i, t := range last {
			if i > 0 {
				used += width(l.sep)
			}
			used += t.width()
		}
		if len(last) == 0 || used+width(l.sep)+more.width() <= w {
			lines[len(lines)-1] = append(last, more)
			return lines
		}
		last = last[:len(last)-1]
		n++
	}
}

// cut shortens a token to w cells, from its end.
func cut(t token, w int, ell string) token {
	if t.width() <= w {
		return t
	}
	out := make(token, 0, len(t))
	used := 0
	for _, p := range t {
		room := w - used
		if room <= 0 {
			break
		}
		text := p.text
		if width(text) > room {
			text = fit(text, room, ell)
		}
		out = append(out, piece{text, p.style})
		used += width(text)
	}
	return out
}

// chipsStart is the column a row's accounts start in: the accounts column,
// or just past a name that ran into it.
func chipsStart(l layout, nameEnd int) int {
	return max(l.chipsX, nameEnd+l.gap)
}

// rowLines is a row's accounts, packed for the room it has.
func (m Model) rowLines(ctx uictx.Context, l layout, r row) (lines [][]token, start int) {
	name, suffix := m.fitName(l, r)
	end := m.nameX(r) + width(name)
	for _, s := range suffix {
		end += 1 + width(s)
	}
	start = chipsStart(l, end)
	return pack(ctx, l, m.tokens(ctx, r), l.width-start), start
}

// fitName cuts a row's name to the room before the accounts column, in the
// middle where it is a path, dropping the fold count before the name gives
// way. It never cuts below [nameMin]; a name that needs more pushes this
// row's accounts to the right instead.
func (m Model) fitName(l layout, r row) (string, []string) {
	name := m.label(r)
	suffix := m.suffix(r)
	room := l.chipsX - l.gap - m.nameX(r)
	sw := m.suffixWidth(r)
	if width(name)+sw <= room {
		return name, suffix
	}
	if r.hidden > 0 {
		suffix = suffix[:len(suffix)-1]
		sw = 0
		for _, s := range suffix {
			sw += 1 + width(s)
		}
	}
	budget := max(nameMin, room-sw)
	if width(name) > budget {
		if strings.ContainsAny(name, `\/`) {
			name = acctable.ShortenPath(name, budget, l.ell)
		} else {
			name = fit(name, budget, l.ell)
		}
	}
	return name, suffix
}

// rowHeight is how many lines visible row i takes.
func (m Model) rowHeight(ctx uictx.Context, l layout, i int) int {
	lines, _ := m.rowLines(ctx, l, m.rows[i])
	return max(1, len(lines))
}

// View renders the heading, the visible window of the tree and, while there
// is a filter, the filter line under it.
func (m Model) View(ctx uictx.Context) string {
	l := m.layout(ctx)
	start, end := m.window(ctx, l, m.bodyLines(ctx))
	out := make([]string, 0, 24)
	out = append(out, m.heading(ctx, l, start, end))
	for i := start; i < end; i++ {
		out = append(out, m.renderRow(ctx, l, i)...)
	}
	if len(m.items) == 1 && m.filter == "" {
		out = append(out, m.emptyLine(ctx, l))
	}
	if line := m.filterLine(ctx, l); line != "" {
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// Height is how many lines View draws this frame.
func (m Model) Height(ctx uictx.Context) int {
	return strings.Count(m.View(ctx), "\n") + 1
}

// bodyLines is how many lines the rows may use: the height less the heading
// and the filter line. Zero is unbounded.
func (m Model) bodyLines(ctx uictx.Context) int {
	h := m.viewHeight(ctx)
	if h <= 0 {
		return 0
	}
	h--
	if m.filtering || m.filter != "" {
		h--
	}
	return max(1, h)
}

// heading draws the muted column titles, and the scroll position at the
// right-hand end when some rows are out of view.
func (m Model) heading(ctx uictx.Context, l layout, start, end int) string {
	th := ctx.Theme
	var ln line
	ln.to(leadW)
	ln.add(headFolder, th.Muted)
	ln.to(l.chipsX)
	switch room := l.width - l.chipsX; {
	case room >= width(headAccounts):
		ln.add(headAccounts, th.Muted)
	case room >= width(headShort):
		ln.add(headShort, th.Muted)
	}
	if n := len(m.rows); start > 0 || end < n {
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

// renderRow draws visible row i and its continuation lines.
func (m Model) renderRow(ctx uictx.Context, l layout, i int) []string {
	th := ctx.Theme
	r := m.rows[i]
	it := m.items[r.item]
	g := guidesFor(ctx)
	selected := i == m.cursor
	hovered := i == m.hover && !selected
	b := bandNone
	if selected {
		b = bandFull
	}

	ln := lead(ctx, selected, hovered, true)
	for _, last := range r.trail {
		ln.add(pick(last, g.blank, g.pipe), th.Rule)
	}
	if it.depth > 0 {
		ln.add(pick(r.last, g.last, g.branch), th.Rule)
		tw, ts := m.twisty(ctx, r)
		ln.add(tw, ts)
		ln.to(m.nameX(r))
	}

	name, suffix := m.fitName(l, r)
	nameStyle := th.Base
	switch {
	case selected:
		nameStyle = th.Selected
	case r.item == 0:
		nameStyle = th.Subtitle
	case len(it.chips) == 0 || it.state != StateOK || !r.match:
		nameStyle = th.Muted
	}
	ln.add(name, nameStyle)
	for _, s := range suffix {
		ln.space(1)
		if s == "(here)" {
			ln.add(s, th.Accent)
		} else {
			ln.add(s, th.Muted)
		}
	}

	lines, start := m.rowLines(ctx, l, r)
	out := make([]string, 0, max(1, len(lines)))
	for k := 0; k < max(1, len(lines)); k++ {
		if k > 0 {
			ln = lead(ctx, selected, hovered, false)
			for _, last := range r.trail {
				ln.add(pick(last, g.blank, g.pipe), th.Rule)
			}
			if it.depth > 0 {
				ln.add(pick(r.last, g.blank, g.pipe), th.Rule)
			}
			if r.open {
				ln.add(strings.TrimRight(g.pipe, " "), th.Rule)
			}
		}
		if k < len(lines) {
			ln.to(start)
			for n, t := range lines[k] {
				if n > 0 {
					ln.add(l.sep, th.Muted)
				}
				for _, p := range t {
					st := p.style
					if selected && p.style.GetForeground() == th.Base.GetForeground() {
						st = st.Bold(true)
					}
					ln.add(p.text, st)
				}
			}
		}
		out = append(out, ln.render(ctx, b, l.width))
	}
	return out
}

// twisty is a row's fold glyph: open or closed for a folder with children,
// and for a leaf the plain folder glyph where the tier has one that is not
// the fold arrow, or nothing.
func (m Model) twisty(ctx uictx.Context, r row) (string, lipgloss.Style) {
	ic := ctx.Icons
	hue := ctx.Theme.SectionIcon(theme.SectionAccounts)
	switch {
	case r.open:
		return ic.FolderOpen, hue
	case r.hidden > 0:
		return ic.Folder, hue
	case ic.Tier == icons.TierNerd:
		return ic.Folder, ctx.Theme.Muted
	default:
		return "", hue
	}
}

// emptyLine is what the tree says under Everywhere when no folder has a
// rule yet: never a blank screen.
func (m Model) emptyLine(ctx uictx.Context, l layout) string {
	var ln line
	ln.to(leadW)
	ln.add(guidesFor(ctx).last, ctx.Theme.Rule)
	ln.add(fit("no folder rules yet", l.width-ln.w, l.ell), ctx.Theme.Muted)
	return ln.render(ctx, bandNone, l.width)
}

// filterLine is the filter input while it has the keyboard, or the filter
// in force and how much it kept. It is empty when there is no filter.
func (m Model) filterLine(ctx uictx.Context, l layout) string {
	th := ctx.Theme
	if m.filtering {
		return pad(leadW) + ansi.Truncate(themedInput(m.input, th).View(), max(1, l.width-leadW), "")
	}
	if m.filter == "" {
		return ""
	}
	matches := 0
	for _, r := range m.rows {
		if r.match && r.item != 0 {
			matches++
		}
	}
	var ln line
	ln.to(leadW)
	ln.add("/", th.Accent)
	ln.add(m.filter, th.Base)
	ln.space(2)
	switch matches {
	case 0:
		ln.add("no folder matches", th.Warning)
	case 1:
		ln.add("1 folder", th.Muted)
	default:
		ln.add(strconv.Itoa(matches)+" folders", th.Muted)
	}
	return ln.render(ctx, bandNone, l.width)
}

// window picks the rows to draw in avail lines, centred on the cursor and
// clamped at both ends, the way the menus scroll. It is computed from the
// cursor on every frame, so View, RowAt and Click cannot disagree.
func (m Model) window(ctx uictx.Context, l layout, avail int) (start, end int) {
	n := len(m.rows)
	if n == 0 {
		return 0, 0
	}
	h := make([]int, n)
	total := 0
	for i := range m.rows {
		h[i] = m.rowHeight(ctx, l, i)
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

// RowAt maps a line of the rendered tree (0 is the heading) to the visible
// row drawn there. ok is false on the heading, the filter line and past the
// end.
func (m Model) RowAt(ctx uictx.Context, y int) (index int, ok bool) {
	if y < 1 {
		return 0, false
	}
	l := m.layout(ctx)
	start, end := m.window(ctx, l, m.bodyLines(ctx))
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

// Click acts on a left click at column x, line y of the rendered tree. A
// click on a fold arrow folds or unfolds that folder; anywhere else on a row
// selects it, and on the row already selected opens it, the same as Enter.
// The arrow's target is a cell wider on each side than the glyph, because a
// one-cell target is a miss waiting to happen.
func (m Model) Click(ctx uictx.Context, x, y int) (Model, tea.Cmd) {
	i, ok := m.RowAt(ctx, y)
	if !ok {
		return m, nil
	}
	r := m.rows[i]
	if d := m.items[r.item].depth; d > 0 && (r.open || r.hidden > 0) {
		tw := leadW + step*d
		if x >= tw-1 && x <= tw+1 {
			before := m.selectedID()
			m.cursor = i
			m.toggle(i)
			return m, m.selectedCmd(before)
		}
	}
	if i == m.cursor {
		return m, m.open()
	}
	return m.moveTo(i)
}

// Hover marks the row under the pointer at line y with a quiet caret. It
// never moves the selection, so it never scrolls. changed is false when
// nothing would be drawn differently.
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

// Pointer handles the mouse for a screen that draws this tree top lines
// below the start of its body, against the left edge: a left click folds,
// selects or opens, the pointer passing over a row marks it, the wheel moves
// the selection. handled is false for anything that is not the mouse, and
// while the filter has the keyboard.
func (m Model) Pointer(ctx uictx.Context, msg tea.Msg, top int) (next Model, cmd tea.Cmd, handled bool) {
	if m.filtering {
		return m, nil, false
	}
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

// viewWidth is the width the tree draws into.
func (m Model) viewWidth(ctx uictx.Context) int {
	if m.width > 0 {
		return m.width
	}
	return max(0, ctx.Width)
}

// viewHeight is how many lines the tree may use. Zero is unbounded.
func (m Model) viewHeight(ctx uictx.Context) int {
	if m.height > 0 {
		return m.height
	}
	return max(0, ctx.BodyHeight)
}

// pick is a ternary for strings.
func pick(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
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
