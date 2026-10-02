package acctable

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The fixed parts of a row. Everything else is sized from the content.
const (
	// leftPad is the margin before the caret, matching the menus.
	leftPad = 2
	// leadW is the gutter: margin, caret and the space after it.
	leadW = 4
	// toolMax caps the tool column, so one long name cannot starve the two
	// columns that carry the information.
	toolMax = 16
	// toolFloor is the narrowest the tool column is squeezed to on a very
	// narrow table.
	toolFloor = 6
	// iconGap is the air between a tool's icon and its name.
	iconGap = "  "
	// noteSep joins the reason, the note and the "updated" flag.
	noteSep = " · "
	// accGap is the air between an account's name and its state word.
	accGap = "  "
)

// Column headings.
const (
	headTool    = "TOOL"
	headAccount = "ACCOUNT"
	headWhy     = "WHY"
)

// layout is the column geometry for one frame.
type layout struct {
	width int
	ell   string
	gap   int
	// iconW is the icon cell plus its gap, or zero when no row has an icon
	// in this tier.
	iconW int
	toolW int
	accX  int
	accW  int
	whyX  int
	whyW  int
}

// piece is one run of text and its colour, before it is placed.
type piece struct {
	text  string
	style lipgloss.Style
}

// cells is one row's content, fitted to the layout.
type cells struct {
	mark      string
	markStyle lipgloss.Style
	name      string
	nameStyle lipgloss.Style
	detail    string
	word      string
	wordStyle lipgloss.Style
	whyWords  string
	whyStyle  lipgloss.Style
	path      string
	notes     []piece
	// stacked puts the reason on a second line, under the account.
	stacked bool
}

// accWidth is the width of an account cell: the mark, the name, the detail
// and the state word.
func accWidth(name, detail, word string) int {
	w := 2 + width(name)
	if detail != "" {
		w += 1 + width(detail)
	}
	if word != "" {
		if name != "" {
			w += width(accGap)
		}
		w += width(word)
	}
	return w
}

// whyWidth is the width of a reason: its words, its path and its notes.
func (c cells) whyWidth() int {
	w := width(c.whyWords)
	if c.path != "" {
		if w > 0 {
			w++
		}
		w += width(c.path)
	}
	for _, n := range c.notes {
		if w > 0 {
			w += width(noteSep)
		}
		w += width(n.text)
	}
	return w
}

// whyMin is the narrowest the reason gets before the row wraps it: the path
// at its shortest, everything else whole.
func (c cells) whyMin(ell string) int {
	if c.path == "" {
		return c.whyWidth()
	}
	return c.whyWidth() - width(c.path) + PathMin(c.path, ell)
}

// hasWhy reports whether the row has a reason to show at all.
func (c cells) hasWhy() bool { return c.whyWidth() > 0 }

// content is a row's text before it is fitted: the mark and colours for its
// state, the account, the reason split from its path, and its notes.
func content(ctx uictx.Context, r Row, frame int) cells {
	th := ctx.Theme
	ic := ctx.Icons
	c := cells{name: r.Name, nameStyle: th.Base, detail: r.Detail, wordStyle: th.Base}

	switch r.State {
	case StateLoading:
		c.mark, c.markStyle = ctx.SpinnerFrame(frame), th.Accent
		c.wordStyle = th.Muted
	case StateExpired:
		c.mark, c.markStyle, c.wordStyle = ic.Warn, th.Warning, th.Warning
	case StateMismatch:
		c.mark, c.markStyle, c.wordStyle = ic.Fail, th.Danger, th.Danger
	case StateProblem:
		c.mark, c.markStyle, c.wordStyle = ic.Fail, th.Danger, th.Danger
	case StateSignedOut:
		c.mark, c.markStyle = ic.Queued, th.Muted
	case StateNotInstalled:
		c.mark, c.markStyle, c.wordStyle = ic.Absent, th.Muted, th.Muted
		c.nameStyle = th.Muted
	default:
		c.mark, c.markStyle = ic.Tick, th.Success
	}
	if c.mark == "" {
		c.mark = " "
	}
	switch {
	case r.State == StateLoading && r.Name == "":
		c.word = StateLoading.Word() + ellipsis(ctx)
	case r.State != StateLoading:
		c.word = r.State.Word()
	}
	if r.Name == "" {
		c.detail = ""
	}
	if r.Fresh {
		c.nameStyle = th.Success.Bold(true)
	}

	c.whyWords, c.path = r.Why, r.WhyPath
	if c.path == "" {
		c.whyWords, c.path = splitWhy(r.Why)
	}
	switch r.WhyKind {
	case WhyFolderRule, WhyProjectFile:
		c.whyStyle = th.Base
	default:
		c.whyStyle = th.Muted
	}
	if r.Note != "" {
		c.notes = append(c.notes, piece{r.Note, noteStyle(th, r.State)})
	}
	if r.Fresh {
		c.notes = append(c.notes, piece{"updated", th.Success})
	}
	return c
}

// noteStyle is the colour of a row's note: the state's own colour when the
// state is a problem, quiet otherwise.
func noteStyle(th *theme.Theme, s State) lipgloss.Style {
	switch s {
	case StateExpired:
		return th.Warning
	case StateMismatch, StateProblem:
		return th.Danger
	default:
		return th.Muted
	}
}

// layout sizes the columns for the current width.
//
// The tool column is as wide as the longest tool name, up to [toolMax]. The
// account and Why columns share the rest, and when they do not both fit the
// table gives ground in a fixed order, which is the contract the tests pin:
//
//  1. every path inside Why is shortened in the middle, down to drive +
//     ellipsis + last folder;
//  2. the account column narrows, shortening emails and then dropping them;
//     names are kept whole;
//  3. a row whose Why still does not fit puts it on a second line under the
//     account. The Why column itself is never dropped.
func (m Model) layout(ctx uictx.Context) layout {
	l := layout{width: m.viewWidth(ctx), ell: ellipsis(ctx), gap: 3}
	if l.width < 100 {
		l.gap = 2
	}
	toolNat := width(headTool)
	for _, r := range m.rows {
		if Glyph(ctx.Icons, r.Icon) != "" {
			l.iconW = 1 + width(iconGap)
		}
		toolNat = max(toolNat, width(r.Tool))
	}
	l.toolW = l.iconW + min(toolNat, toolMax)

	accNat, accFloor := 2+width(headAccount), 2+width(headAccount)
	whyNat := 0
	all := make([]cells, len(m.rows))
	for i, r := range m.rows {
		c := content(ctx, r, m.frame)
		all[i] = c
		accNat = max(accNat, accWidth(c.name, c.detail, c.word))
		accFloor = max(accFloor, accWidth(c.name, "", c.word))
		whyNat = max(whyNat, c.whyWidth())
	}

	room := l.width - leadW - l.toolW - 2*l.gap
	if short := accFloor + minWhy - room; short > 0 {
		// A very narrow table squeezes the tool names before anything that
		// carries information.
		give := min(short, l.toolW-l.iconW-toolFloor)
		if give > 0 {
			l.toolW -= give
			room += give
		}
	}

	// The widest reason that could still sit beside an account at its
	// narrowest. A reason wider than that wraps whatever happens, so it is
	// no reason to squeeze every email on the page.
	whyFloor := 0
	for _, c := range all {
		if w := c.whyMin(l.ell); w <= room-accFloor {
			whyFloor = max(whyFloor, w)
		}
	}

	switch {
	case accNat+whyNat <= room, accNat+whyFloor <= room:
		l.accW = accNat
	default:
		l.accW = min(accNat, max(accFloor, room-whyFloor))
	}
	l.accW = max(0, min(l.accW, room))
	l.accX = leadW + l.toolW + l.gap
	l.whyX = l.accX + l.accW + l.gap
	l.whyW = max(0, l.width-l.whyX)
	return l
}

// minWhy is the room a reason is guaranteed beside the account before the
// tool column starts giving way: enough for "everywhere" and a little more.
const minWhy = 14

// fit fits one row's content to the layout.
func (l layout) fit(ctx uictx.Context, r Row, frame int) cells {
	c := content(ctx, r, frame)

	if accWidth(c.name, c.detail, c.word) > l.accW {
		budget := l.accW - accWidth(c.name, "", c.word) - 1
		c.detail = ShortenEmail(c.detail, budget, l.ell)
		if over := accWidth(c.name, c.detail, c.word) - l.accW; over > 0 && c.name != "" {
			c.name = fit(c.name, max(1, width(c.name)-over), l.ell)
		}
	}

	if !c.hasWhy() {
		return c
	}
	if c.whyWidth() <= l.whyW {
		return c
	}
	if c.whyMin(l.ell) <= l.whyW {
		c.path = ShortenPath(c.path, l.whyW-(c.whyWidth()-width(c.path)), l.ell)
		return c
	}
	c.stacked = true
	c.fitWhy(l.width-l.stackX(), l.ell)
	return c
}

// fitWhy squeezes a reason into w cells for a stacked line: the path first,
// then the notes, then the words, so whatever is left is still the start of
// the sentence.
func (c *cells) fitWhy(w int, ell string) {
	if over := c.whyWidth() - w; over > 0 && c.path != "" {
		c.path = ShortenPath(c.path, max(1, width(c.path)-over), ell)
	}
	for i := len(c.notes) - 1; i >= 0 && c.whyWidth() > w; i-- {
		over := c.whyWidth() - w
		if keep := width(c.notes[i].text) - over; keep >= 4 {
			c.notes[i].text = fit(c.notes[i].text, keep, ell)
		} else {
			c.notes = c.notes[:i]
		}
	}
	if over := c.whyWidth() - w; over > 0 {
		c.whyWords = fit(c.whyWords, max(1, width(c.whyWords)-over), ell)
	}
}

// stackX is the column a wrapped reason starts in: under the account name,
// past its mark.
func (l layout) stackX() int { return l.accX + 2 }

// height is how many lines row i takes this frame.
func (m Model) rowHeight(ctx uictx.Context, l layout, i int) int {
	h := 1
	if l.fit(ctx, m.rows[i], m.frame).stacked {
		h++
	}
	if i == m.cursor && strings.TrimSpace(m.rows[i].Caption) != "" {
		h++
	}
	return h
}

// window picks the rows to draw in avail lines: the half-open range
// [start, end). It follows the menus: centred on the cursor and clamped at
// both ends, so a long list scrolls one row at a time and the ends stand
// still. It is computed from the cursor on every frame rather than stored,
// so View, RowAt and Click can never disagree about it.
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

// viewWidth is the width the table draws into.
func (m Model) viewWidth(ctx uictx.Context) int {
	if m.width > 0 {
		return m.width
	}
	return max(0, ctx.Width)
}

// viewHeight is how many lines the table may use, heading included. Zero is
// unbounded.
func (m Model) viewHeight(ctx uictx.Context) int {
	if m.height > 0 {
		return m.height
	}
	return max(0, ctx.BodyHeight)
}
