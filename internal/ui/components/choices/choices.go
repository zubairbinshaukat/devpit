// Package choices draws a screen a person understands at a glance: what is
// true now, what they can do about it, and what happens if they press Enter.
//
// A screen describes itself as a [Spec]:
//
//   - Facts, the "what is true now" block at the top: label and value pairs,
//     each with a quiet line saying where it comes from. Facts carry no
//     cursor and no marker, so they read as information, never as choices.
//   - Groups of [Item]s under short headings: the choices. Each row says how
//     it acts: a trailing › opens a screen, ‹ value › steps through a few
//     choices in place, a dot and a word switch on and off. A row that cannot
//     be used stays in the list, quiet, with the reason.
//   - One description for the focused row, in plain words, ending with what
//     happens next. It sits in a pane beside the list on a wide terminal and
//     under the list on a narrow one.
//
// The cursor stops on rows only, never on headings or notes. Keys: ↑↓ (j/k),
// home/end, pgup/pgdn, Enter or Space, and ←→ on rows that step in place.
// Mouse: a click on a row moves there, a second click (or a click on its
// value) acts, the pointer passing over a row highlights it, the wheel
// moves. When room is short the blank lines between groups go first, then
// the description gives up a line, then the list scrolls with "n more" at
// each end; the focused row is always on screen.
//
// The component holds only the cursor, the scroll position and the "saved"
// mark. The screen owns the items and decides what a row does: [Model.Update]
// hands back an [Act] naming the row.
package choices

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Kind is how a row acts.
type Kind int

const (
	// Opens opens a screen of its own: a trailing ›.
	Opens Kind = iota
	// Cycles steps through a few values in place: ‹ value ›.
	Cycles
	// Toggles switches on and off in place: ● on, ○ off.
	Toggles
	// Note is a quiet line that is never selected.
	Note
)

// Item is one row.
type Item struct {
	// ID tells the screen which row acted.
	ID string
	// Label is the row's name, sentence style: "Commit as someone else here".
	Label string
	// Value is shown in its own column: the current value of a setting, or a
	// short state ("installed"). It may be empty.
	Value string
	// On is a Toggles row's state, and turns a value green elsewhere.
	On bool
	// Quiet draws the value in the muted grey: "not set", "none".
	Quiet bool
	// Path marks a value that is a folder: it is shortened in the middle.
	Path bool
	// Kind is how the row acts.
	Kind Kind
	// Desc says what the row does, in plain words, for someone who has never
	// seen the screen before.
	Desc string
	// Next says what happens after Enter: "You pick an account, then see a
	// preview. Nothing changes until you say yes."
	Next string
	// Disabled, when set, is why the row cannot be used now. The row stays,
	// quiet, and the reason is in its description.
	Disabled string
}

// Group is a heading and its rows.
type Group struct {
	Title string
	Items []Item
}

// Tone colours a fact's value, always with a mark beside it so nothing is
// said by colour alone.
type Tone int

const (
	// Plain is an ordinary value.
	Plain Tone = iota
	// Good is a value that is as it should be.
	Good
	// Warn is a value that wants attention.
	Warn
	// Bad is a value that is wrong.
	Bad
)

// Fact is one line of "what is true now".
type Fact struct {
	Label string
	Value string
	Tone  Tone
	// Path shortens the value in the middle when it is too long; Wrap
	// folds a long value onto more lines instead of cutting it.
	Path bool
	Wrap bool
	// Notes are quiet lines under the value: where it comes from, what it
	// means.
	Notes []string
	// Warnings are lines under the value that want attention.
	Warnings []string
}

// Spec is what a screen shows.
type Spec struct {
	// FactsTitle heads the facts block, e.g. "Right now"; FactsAside is a
	// quiet note at the right of that line, e.g. "in D:\work\shop".
	FactsTitle string
	FactsAside string
	Facts      []Fact
	// Groups are the choices.
	Groups []Group
	// Note is a quiet reminder kept on show: at the right of the line above
	// the description, and at the foot of the pane.
	Note string
	// ValueWidth fixes the value column; 0 sizes it to the values.
	ValueWidth int
	// Top is how many body rows the screen draws above the component; mouse
	// rows are counted from there.
	Top int
}

// Act is what a key or a click asked a row to do.
type Act struct {
	// ID is the row; "" means nothing was asked.
	ID string
	// Dir is +1 for Enter, Space and →, -1 for ←.
	Dir int
	// Kind is the row's kind.
	Kind Kind
}

// KeyMap is the component's keys.
type KeyMap struct {
	Up, Down, Home, End, PageUp, PageDown key.Binding
	Change, Left, Right                   key.Binding
}

// DefaultKeyMap is ↑↓ with j/k as silent aliases, Enter or Space to act, and
// ←→ to step a value.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑↓", "move")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "down")),
		Home:     key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("home", "first")),
		End:      key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("end", "last")),
		PageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
		PageDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdown", "page down")),
		Change:   key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "open")),
		Left:     key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←→", "choose")),
		Right:    key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→", "next")),
	}
}

// ShortHelp is the footer hints: move and act.
func (k KeyMap) ShortHelp() []key.Binding { return []key.Binding{k.Up, k.Change} }

// FullHelp is the help overlay's groups.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Home, k.End, k.PageUp, k.PageDown},
		{k.Change, k.Left, k.Right},
	}
}

// Model is the component's state: the cursor, the scroll position and the
// row showing "✓ saved". It is a value, like every screen.
type Model struct {
	Keys   KeyMap
	cursor string
	offset int
	saved  string
}

// New returns the component with the cursor on the first row.
func New() Model { return Model{Keys: DefaultKeyMap()} }

// Cursor is the id of the highlighted row as last set; "" before any move
// means the first row. [Model.Focused] resolves it against a spec.
func (m Model) Cursor() string { return m.cursor }

// SetCursor moves the highlight to the row with this id.
func (m Model) SetCursor(id string) Model {
	m.cursor = id
	return m
}

// MarkSaved shows "✓ saved" after the value of the row with this id, or
// clears the mark with "". The screen schedules the clearing.
func (m Model) MarkSaved(id string) Model {
	m.saved = id
	return m
}

// Saved is the row showing "✓ saved", or "".
func (m Model) Saved() string { return m.saved }

// Focused is the row under the cursor in this spec.
func (m Model) Focused(ctx uictx.Context, spec Spec) (Item, bool) {
	lo := m.arrange(ctx, spec)
	i := cursorLine(lo.lines, lo.cur)
	if i < 0 {
		return Item{}, false
	}
	return lo.lines[i].item, true
}

// Settle keeps the cursor on a row that exists and the scroll position that
// shows it. Screens call it after the items change.
func (m Model) Settle(ctx uictx.Context, spec Spec) Model {
	lo := m.arrange(ctx, spec)
	m.offset, m.cursor = lo.start, lo.cur
	return m
}

// Update handles the keys and the mouse. act names a row when one was asked
// to act; a disabled row never acts.
func (m Model) Update(msg tea.Msg, ctx uictx.Context, spec Spec) (Model, Act) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.onKey(msg, ctx, spec)
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			return m.move(ctx, spec, -1), Act{}
		case tea.MouseWheelDown:
			return m.move(ctx, spec, 1), Act{}
		}
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, Act{}
		}
		lo := m.arrange(ctx, spec)
		it, onValue, ok := lo.itemAt(ctx.BodyRow(msg.Y)-spec.Top, msg.X)
		if !ok {
			return m, Act{}
		}
		// A click on a value acts at once, as does a second click on the row
		// already highlighted; a first click elsewhere only moves there.
		already := it.ID == lo.cur
		m.cursor = it.ID
		m = m.Settle(ctx, spec)
		if onValue || already {
			return m, act(it, 1)
		}
	case tea.MouseMotionMsg:
		// The pointer highlights the row under it. It never scrolls the list:
		// that row is already on screen, and a list that slid under a still
		// pointer would put the next click on another row.
		lo := m.arrange(ctx, spec)
		if it, _, ok := lo.itemAt(ctx.BodyRow(msg.Y)-spec.Top, msg.X); ok && it.ID != lo.cur {
			next := m
			next.cursor, next.offset = it.ID, lo.start
			if next.arrange(ctx, spec).start == lo.start {
				return next, Act{}
			}
		}
	}
	return m, Act{}
}

// onKey handles the keys.
func (m Model) onKey(msg tea.KeyPressMsg, ctx uictx.Context, spec Spec) (Model, Act) {
	lo := m.arrange(ctx, spec)
	switch {
	case key.Matches(msg, m.Keys.Up):
		return m.move(ctx, spec, -1), Act{}
	case key.Matches(msg, m.Keys.Down):
		return m.move(ctx, spec, 1), Act{}
	case key.Matches(msg, m.Keys.Home):
		return m.move(ctx, spec, -len(lo.sel)), Act{}
	case key.Matches(msg, m.Keys.End):
		return m.move(ctx, spec, len(lo.sel)), Act{}
	case key.Matches(msg, m.Keys.PageUp):
		return m.move(ctx, spec, -max(1, lo.listH/2)), Act{}
	case key.Matches(msg, m.Keys.PageDown):
		return m.move(ctx, spec, max(1, lo.listH/2)), Act{}
	}
	it, ok := m.Focused(ctx, spec)
	if !ok {
		return m, Act{}
	}
	stepsInPlace := it.Kind == Cycles || it.Kind == Toggles
	switch {
	case key.Matches(msg, m.Keys.Change):
		return m, act(it, 1)
	case key.Matches(msg, m.Keys.Left) && stepsInPlace:
		return m, act(it, -1)
	case key.Matches(msg, m.Keys.Right) && stepsInPlace:
		return m, act(it, 1)
	}
	return m, Act{}
}

// act is the Act for a row, or none for a row that cannot act.
func act(it Item, dir int) Act {
	if it.Disabled != "" || it.Kind == Note {
		return Act{}
	}
	return Act{ID: it.ID, Dir: dir, Kind: it.Kind}
}

// move steps the cursor by n rows, stopping at the ends.
func (m Model) move(ctx uictx.Context, spec Spec, n int) Model {
	lo := m.arrange(ctx, spec)
	if len(lo.sel) == 0 {
		return m
	}
	at := 0
	for i, it := range lo.sel {
		if it.ID == lo.cur {
			at = i
		}
	}
	at = min(max(0, at+n), len(lo.sel)-1)
	m.cursor = lo.sel[at].ID
	return m.Settle(ctx, spec)
}

// View draws the component.
func (m Model) View(ctx uictx.Context, spec Spec) string {
	return m.render(ctx, spec, m.arrange(ctx, spec))
}

// ItemAt is the row drawn at a body row and column, the way a click finds
// it: rows are counted from the top of the body, not of the component.
func (m Model) ItemAt(ctx uictx.Context, spec Spec, bodyRow, x int) (Item, bool) {
	it, _, ok := m.arrange(ctx, spec).itemAt(bodyRow-spec.Top, x)
	return it, ok
}

// Layout is what a test or a screen may want to know about this frame.
type Layout struct {
	// Pane is true when the description sits beside the list.
	Pane bool
	// Gaps is how many blank lines separate groups this frame.
	Gaps int
	// Scrolling is true when the list does not fit and scrolls.
	Scrolling bool
	// Start is the first line of the list drawn.
	Start int
	// ValueX is the column the values start at.
	ValueX int
	// Cursor is the id of the highlighted row.
	Cursor string
	// Height is how many lines View draws.
	Height int
}

// Layout reports this frame's layout.
func (m Model) Layout(ctx uictx.Context, spec Spec) Layout {
	lo := m.arrange(ctx, spec)
	gaps := 0
	for _, l := range lo.lines {
		if l.kind == lineGap {
			gaps++
		}
	}
	return Layout{
		Pane: lo.pane, Gaps: gaps, Scrolling: lo.scrolling, Start: lo.start,
		ValueX: lo.valueX(), Cursor: lo.cur, Height: lo.height(),
	}
}
