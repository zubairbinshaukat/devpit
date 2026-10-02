// Package itemlist draws the Claude setup list: one row per part of a Claude
// Code setup (skills, agents, settings, MCP servers…), each with its size,
// a risk label, and a choice of what to do with it.
//
//	ITEM            SIZE                  LABEL       MODE
//	Skills          12 skills · 340 KB    ● Safe      [Share]  Copy    Skip
//	Plugins         4 enabled             ● Safe      [Same list]      Skip
//	Settings        settings.json         ◆ Review              [Copy]   Skip
//	MCP servers     3 servers             ▲ Careful              Copy   [Skip]
//	Login                                   locked    never cloned
//
// The choice is a row of options with the current one in brackets, so it
// reads the same with colour off. Left and right move along it, Space cycles
// it, and a click on an option picks it. Options of the same kind sit in the
// same column on every row, so the list reads down as well as across.
//
// Two kinds of row are guarded. A locked row (the login, the account info)
// is drawn, so nothing is hidden from the user, but it has no options and
// says why. A row that needs a second confirmation (a Careful row such as MCP
// servers, which may hold API keys) starts on Skip, and the list never turns
// it on by itself: a key or click that would do so is reported as
// [WantsCarefulMsg], the screen asks twice, and only [Model.Confirm] turns it
// on.
//
// The component takes plain view-model rows and knows nothing of the engine
// that does the sharing.
package itemlist

import (
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Mode is what happens to one item.
type Mode int

// The modes, in the order their columns are drawn.
const (
	// ModeSkip leaves the item alone.
	ModeSkip Mode = iota
	// ModeShare links the item, so both accounts use one folder.
	ModeShare
	// ModeSameList copies the list of things (e.g. enabled plugins), and
	// each account installs its own copy.
	ModeSameList
	// ModeCopy makes a separate copy now.
	ModeCopy
)

// String is the word an option shows.
func (m Mode) String() string {
	switch m {
	case ModeShare:
		return "Share"
	case ModeSameList:
		return "Same list"
	case ModeCopy:
		return "Copy"
	default:
		return "Skip"
	}
}

// Label is a row's risk label.
type Label int

// The labels. Safe, Review and Careful are the risk tiers the results table
// uses, drawn with the same shapes.
const (
	LabelSafe Label = iota
	LabelReview
	LabelCareful
)

// Row is one item. Every field is plain display text or a choice the screen
// fills in.
type Row struct {
	// ID identifies the row to the screen, e.g. "skills".
	ID string
	// Title is the item's name, e.g. "Skills".
	Title string
	// Detail is its size or count, e.g. "12 skills · 340 KB".
	Detail string
	// Label is its risk tier.
	Label Label
	// Modes are the options offered, e.g. Share, Copy, Skip. Skip should be
	// among them; a row with none is shown without options.
	Modes []Mode
	// Mode is the current choice.
	Mode Mode
	// Default is the recommended choice; a row moved off it says so when
	// focused.
	Default Mode
	// Locked rows are shown but never change, e.g. the login.
	Locked bool
	// LockedReason is what a locked row shows in place of its options,
	// e.g. "never cloned".
	LockedReason string
	// Note is a line under the row that is always shown, e.g. "2 have the
	// same name in work". It is drawn as a warning.
	Note string
	// Hint is a line under the row shown only while it is focused, e.g. why
	// it is locked or what Careful means here.
	Hint string
	// NeedsConfirm marks a row that must be asked about twice before it is
	// turned on. It starts on Skip whatever Mode says, until Confirmed.
	NeedsConfirm bool
	// Confirmed records that the screen has asked twice and the user said
	// yes. Set it through [Model.Confirm].
	Confirmed bool
	// Bytes is how much a Copy of this item writes, for the summary.
	Bytes uint64
}

// offers reports whether the row offers mode md.
func (r Row) offers(md Mode) bool { return !r.Locked && slices.Contains(r.Modes, md) }

// ChangedMsg is emitted when a row's mode changes.
type ChangedMsg struct {
	// Index is the row's position.
	Index int
	// ID is the row's ID.
	ID string
	// Mode is the new mode.
	Mode Mode
}

// WantsCarefulMsg is emitted instead of a change when the user tries to turn
// on a row that needs a second confirmation. The row stays on Skip. The
// screen asks, and calls [Model.Confirm] with Mode if the answer is yes.
type WantsCarefulMsg struct {
	// Index is the row's position.
	Index int
	// ID is the row's ID.
	ID string
	// Mode is the mode the user asked for.
	Mode Mode
}

// ProceedMsg is emitted when the user presses Enter: "these choices are
// what I want". The screen decides what happens next.
type ProceedMsg struct{}

// KeyMap is the list's key bindings.
type KeyMap struct {
	Up      key.Binding
	Down    key.Binding
	Home    key.Binding
	End     key.Binding
	Left    key.Binding
	Right   key.Binding
	Cycle   key.Binding
	Proceed key.Binding
}

// DefaultKeyMap returns the standard bindings. Arrows are the documented
// keys; j, k, h and l are silent aliases.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑↓", "move")),
		Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "down")),
		Home:    key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("home", "first")),
		End:     key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("end", "last")),
		Left:    key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←→", "choose")),
		Right:   key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→", "next")),
		Cycle:   key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "cycle")),
		Proceed: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "continue")),
	}
}

// ShortHelp returns the bindings worth showing in the footer.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Left, k.Cycle, k.Proceed}
}

// FullHelp returns the bindings grouped for the help overlay.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down, k.Home, k.End}, {k.Left, k.Right, k.Cycle, k.Proceed}}
}

// Model is the item list.
type Model struct {
	// Keys are the bindings the list answers to.
	Keys KeyMap

	rows   []Row
	cursor int
	hover  int
	width  int
	height int
}

// New returns a list over rows with the first row focused. A row that needs
// a second confirmation and has not had one starts on Skip, whatever its
// Mode says; a row whose Mode is not among its options starts on its first
// option.
func New(rows []Row) Model {
	m := Model{Keys: DefaultKeyMap(), hover: -1}
	return m.SetRows(rows)
}

// SetRows replaces the rows, applying the same starting rules as [New], and
// keeps the focus on the row with the same ID when there is one.
func (m Model) SetRows(rows []Row) Model {
	id := ""
	if r, ok := m.Selected(); ok {
		id = r.ID
	}
	m.rows = make([]Row, len(rows))
	for i, r := range rows {
		m.rows[i] = settle(r)
		if id != "" && r.ID == id {
			m.cursor = i
		}
	}
	m.hover = -1
	m.cursor = min(max(m.cursor, 0), max(0, len(m.rows)-1))
	return m
}

// settle applies the starting rules to one row.
func settle(r Row) Row {
	r.Modes = slices.Clone(r.Modes)
	if r.NeedsConfirm && !r.Confirmed {
		r.Mode = ModeSkip
	}
	if len(r.Modes) > 0 && !slices.Contains(r.Modes, r.Mode) {
		r.Mode = r.Modes[0]
	}
	return r
}

// Rows returns the rows with their current modes.
func (m Model) Rows() []Row { return m.rows }

// SetSize fixes the area the list draws into. Zero means the context's
// width, or its body height.
func (m Model) SetSize(w, h int) Model {
	m.width, m.height = max(0, w), max(0, h)
	return m
}

// Cursor returns the focused row's index.
func (m Model) Cursor() int { return m.cursor }

// SetCursor moves the focus, clamped to the rows.
func (m Model) SetCursor(i int) Model {
	m.cursor = min(max(i, 0), max(0, len(m.rows)-1))
	return m
}

// Selected returns the focused row and whether there is one.
func (m Model) Selected() (Row, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return Row{}, false
	}
	return m.rows[m.cursor], true
}

// Confirm turns on row i in mode md after the screen has asked twice. It is
// the only way a row that needs a second confirmation leaves Skip. A locked
// row, or a mode the row does not offer, is refused.
func (m Model) Confirm(i int, md Mode) (Model, tea.Cmd) {
	if i < 0 || i >= len(m.rows) || !m.rows[i].offers(md) {
		return m, nil
	}
	m.rows = slices.Clone(m.rows)
	m.rows[i].Confirmed = true
	return m.set(i, md)
}

// ShortHelp returns the bindings worth showing in the footer.
func (m Model) ShortHelp() []key.Binding { return m.Keys.ShortHelp() }

// FullHelp returns the bindings grouped for the help overlay.
func (m Model) FullHelp() [][]key.Binding { return m.Keys.FullHelp() }

// Update handles the keys and the mouse wheel. Clicks and hover go through
// [Model.Pointer], because only the screen knows where the list was drawn.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m = m.SetCursor(m.cursor - 1)
		case tea.MouseWheelDown:
			m = m.SetCursor(m.cursor + 1)
		}
		return m, nil
	case tea.KeyPressMsg:
		m.hover = -1
		switch {
		case key.Matches(msg, m.Keys.Up):
			m = m.SetCursor(m.cursor - 1)
		case key.Matches(msg, m.Keys.Down):
			m = m.SetCursor(m.cursor + 1)
		case key.Matches(msg, m.Keys.Home):
			m = m.SetCursor(0)
		case key.Matches(msg, m.Keys.End):
			m = m.SetCursor(len(m.rows) - 1)
		case key.Matches(msg, m.Keys.Left):
			return m.step(-1, false)
		case key.Matches(msg, m.Keys.Right):
			return m.step(1, false)
		case key.Matches(msg, m.Keys.Cycle):
			return m.step(1, true)
		case key.Matches(msg, m.Keys.Proceed):
			if len(m.rows) == 0 {
				return m, nil
			}
			return m, func() tea.Msg { return ProceedMsg{} }
		}
	}
	return m, nil
}

// step moves the focused row's choice along its options: left and right
// stop at the ends, Space wraps round.
func (m Model) step(delta int, wrap bool) (Model, tea.Cmd) {
	r, ok := m.Selected()
	if !ok || r.Locked || len(r.Modes) < 2 {
		return m, nil
	}
	at := max(0, slices.Index(r.Modes, r.Mode))
	next := at + delta
	switch {
	case wrap:
		next = (next + len(r.Modes)) % len(r.Modes)
	case next < 0 || next >= len(r.Modes):
		return m, nil
	}
	return m.choose(m.cursor, r.Modes[next])
}

// choose is what the user asking for mode md on row i does: nothing on a
// locked row, a [WantsCarefulMsg] on a guarded row that is not yet
// confirmed, the change otherwise.
func (m Model) choose(i int, md Mode) (Model, tea.Cmd) {
	r := m.rows[i]
	if !r.offers(md) || r.Mode == md {
		return m, nil
	}
	if r.NeedsConfirm && !r.Confirmed && md != ModeSkip {
		id := r.ID
		return m, func() tea.Msg { return WantsCarefulMsg{Index: i, ID: id, Mode: md} }
	}
	m.rows = slices.Clone(m.rows)
	if md == ModeSkip && r.NeedsConfirm {
		// Turning a guarded row back off means the next time it is turned
		// on is asked about afresh.
		m.rows[i].Confirmed = false
	}
	return m.set(i, md)
}

// set changes row i's mode and reports it. The caller has already copied
// the row slice, so a Model handed out earlier never sees the change.
func (m Model) set(i int, md Mode) (Model, tea.Cmd) {
	if m.rows[i].Mode == md {
		return m, nil
	}
	m.rows[i].Mode = md
	id := m.rows[i].ID
	return m, func() tea.Msg { return ChangedMsg{Index: i, ID: id, Mode: md} }
}

// Summary is the footer's data: how many items each mode has, and how much
// the copies will write.
type Summary struct {
	Share, SameList, Copy, Skip int
	// Locked is how many rows are locked; they are in no other count.
	Locked int
	// CopyBytes is the total size of the items set to Copy.
	CopyBytes uint64
}

// Summary counts the rows by mode.
func (m Model) Summary() Summary {
	var s Summary
	for _, r := range m.rows {
		if r.Locked {
			s.Locked++
			continue
		}
		switch r.Mode {
		case ModeShare:
			s.Share++
		case ModeSameList:
			s.SameList++
		case ModeCopy:
			s.Copy++
			s.CopyBytes += r.Bytes
		default:
			s.Skip++
		}
	}
	return s
}
