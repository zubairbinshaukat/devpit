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
	// Long settings lists use it so the screen is not a wall of text; the
	// home menu keeps every description because they are the tour.
	descSelectedOnly bool
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

// showsDesc reports whether item i's description is drawn this frame.
func (m Model) showsDesc(i int) bool {
	if i < 0 || i >= len(m.items) || m.items[i].Desc == "" {
		return false
	}
	return !m.descSelectedOnly || i == m.cursor
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
		rows := m.items[i].rows(m.showsDesc(i))
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
		if m.showsDesc(i) {
			b.WriteString(m.renderDesc(ctx, it, selected, width))
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
		h[i] = it.rows(m.showsDesc(i)) + gap
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
	if height <= 0 {
		return 1
	}
	dense := 0
	for i, it := range m.items {
		dense += it.rows(m.showsDesc(i))
	}
	if dense+n-1 <= height {
		return 1
	}
	return 0
}

// renderTitle draws the cursor, icon, title and hint of one row.
func (m Model) renderTitle(ctx uictx.Context, it Item, selected bool, width int) string {
	th := ctx.Theme

	var row strings.Builder
	row.WriteString(strings.Repeat(" ", leftPad))
	if selected {
		row.WriteString(ctx.Icons.Cursor)
	} else {
		row.WriteString(strings.Repeat(" ", cellWidth(ctx.Icons.Cursor)))
	}
	row.WriteByte(' ')
	if it.Icon != "" {
		row.WriteString(it.Icon)
		row.WriteByte(' ')
	}
	row.WriteString(it.Title)

	left := truncate(ctx, row.String(), width)
	gap, hint := spread(left, it.Hint, width)

	switch {
	case it.Disabled:
		return th.Muted.Render(left + gap + hint)
	case selected:
		// One Render over the whole padded row, so the reverse-video bar runs
		// the full width of the menu instead of stopping after the title.
		return th.Selected.Render(pad(left+gap+hint, width))
	case hint != "":
		return th.Base.Render(left) + gap + th.Hint.Render(hint)
	default:
		return th.Base.Render(left)
	}
}

// renderDesc draws the muted second line of one row: the description in
// brackets, aligned under the title text.
func (m Model) renderDesc(ctx uictx.Context, it Item, selected bool, width int) string {
	th := ctx.Theme
	indent := strings.Repeat(" ", leftPad+cellWidth(ctx.Icons.Cursor)+1)
	if it.Icon != "" {
		indent += strings.Repeat(" ", cellWidth(it.Icon)+1)
	}
	text := truncate(ctx, indent+"("+it.Desc+")", width)
	if selected && !it.Disabled {
		return th.SelectedDesc.Render(text)
	}
	return th.Muted.Render(text)
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

// pad grows a line to exactly width cells.
func pad(s string, width int) string {
	if n := width - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
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
