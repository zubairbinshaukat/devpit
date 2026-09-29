// Package menu draws a vertical list of choices: a cursor, an optional icon,
// a title with an optional right-aligned hint, and a muted one-line
// description under each entry.
//
// It is the shape every top-level screen in Devpit uses, and the fallback the
// first-run screen uses for its questions.
//
// The list is windowed. A menu never draws more rows than it was given, so a
// twelve-row settings list on a 24-row terminal scrolls with the cursor
// instead of running off the bottom of the screen, and says how many entries
// are hidden at each end. The window is computed from the cursor on every
// frame rather than stored, because screens rebuild their menus inside View
// (settings does, so a toggled value shows immediately) and anything written
// to the model there would be thrown away.
package menu

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Screens draw a line or two of their own above their menu — a lead-in
// sentence and a blank line is the house pattern — and a menu that has not
// been given an explicit height has no way to know that. headroom is what it
// holds back from [uictx.Context.BodyHeight] to cover it, and minRows is the
// floor it will not shrink below however small the terminal is.
const (
	headroom = 2
	minRows  = 3
	// leftPad is the margin before the cursor cell. A menu that starts hard
	// against the left edge reads as a log, not as a list of choices.
	leftPad = 2
)

// Item is one row of the menu.
type Item struct {
	// ID identifies the item to the screen that owns the menu.
	ID string
	// Title is the first line.
	Title string
	// Desc is the muted second line. It may be empty, in which case the row
	// is a single line.
	Desc string
	// Hint is an optional note pushed to the right-hand edge of the title
	// row, e.g. "~14 GB can be freed". It is dropped when the row is too
	// narrow to hold both.
	Hint string
	// Icon is a glyph drawn before the title. It may be empty.
	Icon string
	// Hue names the colour the icon is drawn in: a section id the theme
	// has a hue for ([theme.Theme.SectionIcon]). Empty draws the icon in the
	// row's own colour.
	Hue string
	// Disabled greys the row out and skips it when moving.
	Disabled bool
}

// rows is how many lines the item occupies when its description is shown.
func (i Item) rows(withDesc bool) int {
	if i.Desc == "" || !withDesc {
		return 1
	}
	return 2
}

// iconGap is the air between an icon and its title. Two columns, because a
// glyph pressed against a word reads as part of it.
const iconGap = "  "

// SelectedMsg is emitted when the user presses Enter on an item.
type SelectedMsg struct {
	// ID is the selected item's ID.
	ID string
	// Index is its position in the item slice.
	Index int
}

// KeyMap is the menu's key bindings.
type KeyMap struct {
	Up     key.Binding
	Down   key.Binding
	Select key.Binding
}

// DefaultKeyMap is the standard navigation set: arrows, with j and k as
// silent aliases so the hint bar stays short.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑↓", "move"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓", "down"),
		),
		Select: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select"),
		),
	}
}

// ShortHelp returns the bindings worth showing in the footer.
func (k KeyMap) ShortHelp() []key.Binding { return []key.Binding{k.Up, k.Select} }

// FullHelp returns the bindings grouped for the help overlay.
func (k KeyMap) FullHelp() [][]key.Binding { return [][]key.Binding{{k.Up, k.Down, k.Select}} }

// Model is the menu component.
type Model struct {
	// Keys are the bindings this menu answers to.
	Keys KeyMap

	items  []Item
	cursor int
	width  int
	height int
	// descSelectedOnly draws a description under the highlighted row only.
	// Long settings lists use it so the screen is not a wall of text.
	descSelectedOnly bool
	// reserveDesc keeps an empty line where a hidden description would be,
	// so moving the highlight never makes the rows under it jump. The home
	// menu uses it with descSelectedOnly: the description appears in place,
	// under whichever row is highlighted.
	reserveDesc bool
}

// New returns a menu over the given items with the cursor on the first
// enabled one.
func New(items []Item) Model {
	m := Model{Keys: DefaultKeyMap(), items: items}
	m.cursor = m.nextEnabled(-1, 1)
	if m.cursor < 0 {
		m.cursor = 0
	}
	return m
}

// Items returns the current items.
func (m Model) Items() []Item { return m.items }

// DescOnSelectedOnly makes the menu draw a description under the highlighted
// row only, which keeps a long list quiet.
func (m Model) DescOnSelectedOnly(on bool) Model {
	m.descSelectedOnly = on
	return m
}

// ReserveDescRows keeps a blank line under every row whose description is
// hidden, so the list keeps its shape as the highlight moves. It only
// matters together with [Model.DescOnSelectedOnly].
func (m Model) ReserveDescRows(on bool) Model {
	m.reserveDesc = on
	return m
}

// showsDesc reports whether item i's description is drawn this frame.
func (m Model) showsDesc(i int) bool {
	if i < 0 || i >= len(m.items) || m.items[i].Desc == "" {
		return false
	}
	return !m.descSelectedOnly || i == m.cursor
}

// descRow reports whether item i takes a second line this frame: its
// description, or the blank line reserved for it.
func (m Model) descRow(i int) bool {
	if i < 0 || i >= len(m.items) || m.items[i].Desc == "" {
		return false
	}
	return m.showsDesc(i) || m.reserveDesc
}

// SetItems replaces the items, keeping the cursor in range.
func (m Model) SetItems(items []Item) Model {
	m.items = items
	if m.cursor >= len(items) {
		m.cursor = max(0, len(items)-1)
	}
	return m
}

// SetWidth fixes the width the menu draws into. Zero, the default, means the
// full terminal width.
func (m Model) SetWidth(w int) Model {
	m.width = max(0, w)
	return m
}

// SetHeight fixes how many rows the menu may draw. Zero, the default, means
// it works one out from the body height in the render context.
func (m Model) SetHeight(h int) Model {
	m.height = max(0, h)
	return m
}

// Cursor returns the index of the highlighted item.
func (m Model) Cursor() int { return m.cursor }

// SetCursor moves the cursor, clamped to the item range.
func (m Model) SetCursor(i int) Model {
	if i < 0 {
		i = 0
	}
	if i >= len(m.items) {
		i = max(0, len(m.items)-1)
	}
	m.cursor = i
	return m
}

// Selected returns the highlighted item and whether there is one.
func (m Model) Selected() (Item, bool) {
	if m.cursor < 0 || m.cursor >= len(m.items) {
		return Item{}, false
	}
	return m.items[m.cursor], true
}

// Update handles navigation and selection. The mouse wheel moves the cursor
// the way the arrow keys do; clicks are handled by [Model.Click], because
// only the screen knows where on the terminal its menu was drawn.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if wm, ok := msg.(tea.MouseWheelMsg); ok {
		switch wm.Button {
		case tea.MouseWheelUp:
			if i := m.nextEnabled(m.cursor, -1); i >= 0 {
				m.cursor = i
			}
		case tea.MouseWheelDown:
			if i := m.nextEnabled(m.cursor, 1); i >= 0 {
				m.cursor = i
			}
		}
		return m, nil
	}
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch {
	case key.Matches(km, m.Keys.Up):
		if i := m.nextEnabled(m.cursor, -1); i >= 0 {
			m.cursor = i
		}
	case key.Matches(km, m.Keys.Down):
		if i := m.nextEnabled(m.cursor, 1); i >= 0 {
			m.cursor = i
		}
	case key.Matches(km, m.Keys.Select):
		it, ok := m.Selected()
		if !ok || it.Disabled {
			return m, nil
		}
		idx := m.cursor
		return m, func() tea.Msg { return SelectedMsg{ID: it.ID, Index: idx} }
	}
	return m, nil
}

// RowAt maps a row of the rendered menu (0 is its first line) to the index
// of the item drawn there. It walks the same window and gaps View draws, so
// the two cannot disagree. ok is false on a blank line, a "more" marker or
// past the end.
func (m Model) RowAt(ctx uictx.Context, row int) (index int, ok bool) {
	if row < 0 {
		return 0, false
	}
	height := m.viewHeight(ctx)
	gap := m.gap(height)
	start, end, above, below := m.window(height, gap)
	line := 0
	if above > 0 || below > 0 {
		if row == 0 {
			return 0, false
		}
		line = 1
	}
	for i := start; i < end; i++ {
		rows := m.items[i].rows(m.descRow(i))
		if row >= line && row < line+rows {
			return i, true
		}
		line += rows
		if gap > 0 && i < end-1 {
			line++
		}
	}
	return 0, false
}

// Click moves the cursor to the item on the given row of the rendered menu
// and selects it, the same as pressing Enter there. A click on a disabled
// item or on empty space only moves the cursor, or nothing at all.
func (m Model) Click(ctx uictx.Context, row int) (Model, tea.Cmd) {
	i, ok := m.RowAt(ctx, row)
	if !ok {
		return m, nil
	}
	m.cursor = i
	it := m.items[i]
	if it.Disabled {
		return m, nil
	}
	return m, func() tea.Msg { return SelectedMsg{ID: it.ID, Index: i} }
}

// Hover moves the highlight to the item on the given row of the rendered
// menu, without selecting it: it is what the pointer passing over a row
// does. changed is false when the row is already highlighted, blank, or
// disabled, so the caller can skip a redraw that would draw the same frame.
//
// Hover never scrolls. The window follows the cursor, so moving the cursor
// to a row near the edge of a long list would slide a different row under
// a pointer that has not moved, and the next click would land on it. When
// highlighting the row would scroll the list, the highlight stays put.
func (m Model) Hover(ctx uictx.Context, row int) (next Model, changed bool) {
	i, ok := m.RowAt(ctx, row)
	if !ok || i == m.cursor || m.items[i].Disabled {
		return m, false
	}
	height := m.viewHeight(ctx)
	before, _, _, _ := m.window(height, m.gap(height))
	moved := m
	moved.cursor = i
	if after, _, _, _ := moved.window(height, moved.gap(height)); after != before {
		return m, false
	}
	return moved, true
}

// Pointer handles the mouse for a screen that draws this menu top rows
// below the start of its body: a left click selects the row under it, the
// pointer passing over a row highlights it. handled is false for anything
// else, which the screen should pass on to [Model.Update] as usual (that is
// where the wheel is handled).
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
	}
	return m, nil, false
}

// View renders the menu, windowed so the cursor is always on screen.
func (m Model) View(ctx uictx.Context) string {
	width := m.viewWidth(ctx)
	height := m.viewHeight(ctx)
	ascii := ctx.Icons.Tier == icons.TierASCII
	gap := m.gap(height)

	start, end, above, below := m.window(height, gap)

	var b strings.Builder
	scrolling := above > 0 || below > 0
	if scrolling {
		b.WriteString(indicator(ctx, above, up(ascii)))
		b.WriteByte('\n')
	}
	for i := start; i < end; i++ {
		it := m.items[i]
		selected := i == m.cursor
		b.WriteString(m.renderTitle(ctx, it, selected, width))
		b.WriteByte('\n')
		switch {
		case m.showsDesc(i):
			b.WriteString(m.renderDesc(ctx, it, selected, width))
			b.WriteByte('\n')
		case m.descRow(i):
			b.WriteByte('\n')
		}
		if gap > 0 && i < end-1 {
			b.WriteByte('\n')
		}
	}
	if scrolling {
		b.WriteString(indicator(ctx, below, down(ascii)))
		return b.String()
	}
	return strings.TrimRight(b.String(), "\n")
}

// Height is how many lines View draws this frame: every row with its gaps
// when the list fits, the full window when it scrolls. Screens use it to
// place things under the menu without rendering it twice.
func (m Model) Height(ctx uictx.Context) int {
	height := m.viewHeight(ctx)
	gap := m.gap(height)
	total := 0
	for i, it := range m.items {
		total += it.rows(m.descRow(i)) + gap
	}
	if total > 0 {
		total -= gap
	}
	if height > 0 && total > height {
		return height
	}
	return total
}

// viewWidth is the width the menu draws into.
func (m Model) viewWidth(ctx uictx.Context) int {
	if m.width > 0 {
		return m.width
	}
	if ctx.Width > 0 {
		return ctx.Width
	}
	return 0
}

// viewHeight is how many rows the menu may use: the height it was given, or
// what is left of the body once the screen's own lead-in is allowed for. Zero
// means unbounded, which is what a context with no body height asks for.
func (m Model) viewHeight(ctx uictx.Context) int {
	if m.height > 0 {
		return m.height
	}
	if ctx.BodyHeight <= 0 {
		return 0
	}
	return max(minRows, ctx.BodyHeight-headroom)
}

// window picks the slice of items to draw. It returns the half-open range
// [start, end) plus how many items are hidden above and below it.
//
// The window is centred on the cursor and clamped at both ends, so moving
// through a long list scrolls one row at a time and the ends stand still. Two
// rows are held back for the "n more" markers whenever anything is hidden, so
// a scrolling menu is exactly as tall as the height it was given.
func (m Model) window(height, gap int) (start, end, above, below int) {
	n := len(m.items)
	if n == 0 {
		return 0, 0, 0, 0
	}
	h := make([]int, n)
	total := 0
	for i, it := range m.items {
		h[i] = it.rows(m.descRow(i)) + gap
		total += h[i]
	}
	total -= gap // the blank line after the last item is never drawn
	if height <= 0 || total <= height {
		return 0, n, 0, 0
	}

	avail := max(1, height-2)
	cursor := min(max(m.cursor, 0), n-1)

	used := h[cursor]
	if used > avail {
		return cursor, cursor + 1, cursor, n - cursor - 1
	}

	// Fill backwards to about half the window, so the cursor sits in the
	// middle of a long list rather than skating along an edge.
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
	// Then forwards with whatever is left.
	for end = cursor + 1; end < n; end++ {
		if used+h[end] > avail {
			break
		}
		used += h[end]
	}
	// At the bottom of the list there is nothing left to fill forwards with,
	// so spend the remainder going further back.
	for i := start - 1; i >= 0; i-- {
		if used+h[i] > avail {
			break
		}
		used += h[i]
		start = i
	}
	return start, end, start, n - end
}

// gap is the blank line between rows. A menu that fits with air around every
// entry gets it; one that has to scroll spends those rows on entries instead.
func (m Model) gap(height int) int {
	n := len(m.items)
	if n < 2 {
		return 0
	}
	// A reserved description line already separates every row from the
	// next; a gap on top of it would space the list out twice.
	if m.reserveDesc && m.descSelectedOnly {
		return 0
	}
	if height <= 0 {
		return 1
	}
	dense := 0
	for i, it := range m.items {
		dense += it.rows(m.descRow(i))
	}
	if dense+n-1 <= height {
		return 1
	}
	return 0
}

// renderTitle draws the cursor, icon, title and hint of one row.
//
// The highlighted row is a lifted band: an accent bar at its left edge, the
// caret, and every piece drawn on the highlight colour right to the menu's
// edge. Each piece carries the band itself, because the reset that ends one
// styled piece would otherwise punch a hole in it.
func (m Model) renderTitle(ctx uictx.Context, it Item, selected bool, width int) string {
	th := ctx.Theme

	lead := strings.Repeat(" ", leftPad)
	if selected {
		lead = ctx.Icons.SelectBar + strings.Repeat(" ", leftPad-cellWidth(ctx.Icons.SelectBar))
	}
	caret := strings.Repeat(" ", cellWidth(ctx.Icons.Cursor))
	if selected {
		caret = ctx.Icons.Cursor
	}
	icon := ""
	if it.Icon != "" {
		icon = it.Icon + iconGap
	}

	// Measure on plain text, then style: the title is what gets cut when the
	// row is too narrow, never the caret or the icon.
	prefixW := ansi.StringWidth(lead) + ansi.StringWidth(caret) + 1 + ansi.StringWidth(icon)
	title := it.Title
	if width > 0 && prefixW+ansi.StringWidth(title) > width {
		title = truncate(ctx, title, max(1, width-prefixW))
	}
	gap, hint := spread(lead+caret+" "+icon+title, it.Hint, width)

	iconStyle := th.Base
	if it.Hue != "" {
		iconStyle = th.SectionIcon(it.Hue)
	}

	switch {
	case it.Disabled:
		return th.Muted.Render(lead + caret + " " + icon + title + gap + hint)
	case selected:
		band := th.SelBand
		row := th.SelectBar.Render(lead[:len(ctx.Icons.SelectBar)]) +
			band.Render(lead[len(ctx.Icons.SelectBar):]) +
			th.OnBand(th.Cursor).Render(caret) + band.Render(" ")
		if it.Icon != "" {
			row += th.OnBand(iconStyle).Render(it.Icon) + band.Render(iconGap)
		}
		row += th.Selected.Render(title) + band.Render(gap)
		if hint != "" {
			row += th.OnBand(th.Hint).Render(hint)
		}
		if n := width - ansi.StringWidth(lead+caret+" "+icon+title+gap+hint); n > 0 {
			row += band.Render(strings.Repeat(" ", n))
		}
		return row
	default:
		row := th.Base.Render(lead + caret + " ")
		if it.Icon != "" {
			row += iconStyle.Render(it.Icon) + iconGap
		}
		row += th.Base.Render(title)
		if hint != "" {
			row += gap + th.Hint.Render(hint)
		}
		return row
	}
}

// renderDesc draws the muted second line of one row, aligned under the
// title text. Under the highlighted row it continues the band, bar and all,
// so the title and its description read as one lifted card.
func (m Model) renderDesc(ctx uictx.Context, it Item, selected bool, width int) string {
	th := ctx.Theme
	indentW := leftPad + cellWidth(ctx.Icons.Cursor) + 1
	if it.Icon != "" {
		indentW += cellWidth(it.Icon) + len(iconGap)
	}
	text := it.Desc
	if width > 0 && indentW+ansi.StringWidth(text) > width {
		text = truncate(ctx, text, max(1, width-indentW))
	}
	if !selected || it.Disabled {
		return th.Muted.Render(strings.Repeat(" ", indentW) + text)
	}
	bar := ctx.Icons.SelectBar
	row := th.SelectBar.Render(bar) +
		th.SelBandSoft.Render(strings.Repeat(" ", indentW-cellWidth(bar))) +
		th.SelectedDesc.Render(text)
	if n := width - indentW - ansi.StringWidth(text); n > 0 {
		row += th.SelBandSoft.Render(strings.Repeat(" ", n))
	}
	return row
}

// indicator draws one "n more" marker, or a blank line when that end of the
// list is already on screen.
func indicator(ctx uictx.Context, n int, arrow string) string {
	if n <= 0 {
		return ""
	}
	return ctx.Theme.Muted.Render(strings.Repeat(" ", leftPad) + arrow + " " + strconv.Itoa(n) + " more")
}

// up and down are the scroll markers for a tier.
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

// spread works out the padding between a row's title and its hint. The hint
// is dropped when the two cannot share the row with a gap between them.
func spread(left, hint string, width int) (gap, kept string) {
	if hint == "" || width <= 0 {
		return "", ""
	}
	space := width - ansi.StringWidth(left) - ansi.StringWidth(hint)
	if space < 2 {
		return "", ""
	}
	return strings.Repeat(" ", space), hint
}

// truncate cuts a line to the menu's width, marking the cut the way the
// terminal's tier can draw it.
func truncate(ctx uictx.Context, s string, width int) string {
	if width <= 1 || ansi.StringWidth(s) <= width {
		return s
	}
	tail := "…"
	if ctx.Icons.Tier == icons.TierASCII {
		tail = "..."
	}
	return ansi.Truncate(s, width, tail)
}

// nextEnabled walks from i in the given direction and returns the next index
// that is not disabled, or -1 when there is none. It does not wrap, so the
// cursor stops at the ends rather than jumping across the list.
func (m Model) nextEnabled(i, step int) int {
	for n := i + step; n >= 0 && n < len(m.items); n += step {
		if !m.items[n].Disabled {
			return n
		}
	}
	return -1
}

// cellWidth is the printed width of a glyph. Every glyph in every tier is one
// cell, but an empty glyph is zero, and the layout has to account for that.
func cellWidth(s string) int {
	if s == "" {
		return 0
	}
	return 1
}
