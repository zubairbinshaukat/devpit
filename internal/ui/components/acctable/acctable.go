// Package acctable draws the Accounts page: one row per tool, with three
// columns that answer the page's three questions.
//
//	TOOL          ACCOUNT                       WHY
//	Claude Code   ✓ work (you@work.com)         folder rule: C:\Projects
//	Git           ✓ You <you@gmail.com>         everywhere
//	Firebase      · not signed in
//
// The Why column is the point of the page, so it is never dropped. When the
// width runs short the table gives ground in a fixed order: first the path
// inside Why is shortened in the middle (C:\Proj…\quiz-slayer keeps the drive
// and the last folder), then the email in the account column (the name is
// always kept whole), and only when even that is not enough does a row put
// its Why on a second line under the account. See layout.go.
//
// A state is never carried by colour alone: every row starts its account
// with a shape (✓ ! ✗ · ─ or the spinner), and every state but ok says its
// word as well. "not installed" is muted rather than red, because a tool the
// user does not have is not a problem.
//
// The component takes plain view-model rows the screen fills in. It knows
// nothing of the accounts engine and never asks a tool anything itself; a
// row that is still being asked is [StateLoading], and the spinner frame is
// handed in with [Model.SetFrame] so a frame always renders the same way.
package acctable

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// State is what is known about one tool's account right now.
type State int

// The row states.
const (
	// StateOK is a signed-in account that matches what the rules expect.
	StateOK State = iota
	// StateLoading is a tool still being asked. A row with an account shown
	// from the cache keeps it, with the spinner as its mark; a row with
	// nothing yet says "checking…".
	StateLoading
	// StateExpired is an account whose login has run out.
	StateExpired
	// StateMismatch is a tool signed in as someone other than the rule says.
	StateMismatch
	// StateSignedOut is a tool with nobody signed in.
	StateSignedOut
	// StateNotInstalled is a tool that is not on this PC. It is muted, not
	// red: nothing is wrong.
	StateNotInstalled
	// StateProblem is a tool that could not be asked, e.g. it crashed.
	StateProblem
)

// Word is the word a state adds to its row. OK says nothing, because the
// account itself is the news. Loading says "checking" (the table adds the
// tier's ellipsis) only on a row with no account yet; once one is shown from
// the cache, the spinner is the mark.
func (s State) Word() string {
	switch s {
	case StateExpired:
		return "expired"
	case StateMismatch:
		return "mismatch"
	case StateSignedOut:
		return "not signed in"
	case StateNotInstalled:
		return "not installed"
	case StateProblem:
		return "problem"
	case StateLoading:
		return "checking"
	default:
		return ""
	}
}

// WhyKind says where a row's account comes from. It tints the Why text: a
// default is quiet, a rule reads as body text, so the rows a rule decided
// stand out from the ones that simply fell through to "everywhere".
type WhyKind int

// The kinds of reason.
const (
	// WhyNone is a row with no reason to give, e.g. a tool not installed.
	WhyNone WhyKind = iota
	// WhyEverywhere is the account used when no folder rule applies.
	WhyEverywhere
	// WhyFolderRule is a rule set on this folder or one above it.
	WhyFolderRule
	// WhyProjectFile is a file in the project that picks the account, such
	// as Convex's .env.local.
	WhyProjectFile
)

// Row is one tool. Every field is plain display text the screen fills in.
type Row struct {
	// ID identifies the row to the screen, e.g. "claude". It is echoed in
	// [OpenMsg] and used to keep the cursor on the same tool across
	// [Model.SetRows].
	ID string
	// Tool is the tool's display name, e.g. "Claude Code".
	Tool string
	// Icon is an optional glyph key: an [icons.Set] tool or object name
	// ("git", "node", "docker", "key", "globe", "package", "gear", …) or a
	// home section id. Only the nerd tier has most of them; an unknown key or
	// a tier without the glyph draws nothing.
	Icon string
	// Name is the strong part of the account, e.g. "work", "You" or
	// "project:". It is never shortened while anything else can give way.
	Name string
	// Detail is the quiet part after it, e.g. "(you@work.com)",
	// "<you@gmail.com>" or "quiz-slayer". An email in it is shortened in the
	// middle of its local part when the row is tight, then dropped.
	Detail string
	// Why is the reason in words, e.g. "folder rule:", "everywhere" or
	// "set by this project's .env.local".
	Why string
	// WhyPath is an optional path drawn after Why, e.g. `C:\Projects`. It
	// is what gets shortened in the middle when the row is tight. When it is
	// empty and Why itself ends in a path, that path is used.
	WhyPath string
	// WhyKind tints the reason.
	WhyKind WhyKind
	// State is what is known about the account.
	State State
	// Note is a short remark after the reason, e.g. "beta" or "sign in
	// again". It wears the state's colour.
	Note string
	// Fresh marks a row that just changed, so the screen can draw attention
	// to it once. It adds the word "updated" and greens the account; the
	// screen clears it on the next change of focus or the next tick.
	Fresh bool
	// Caption is an optional line shown under the row while it is selected,
	// e.g. the rule chain `C:\Work → C:\Work\client (won)`.
	Caption string
}

// OpenMsg is emitted when the user opens a row: Enter on it, or a click on
// the row that is already selected (which is also what a double-click is).
type OpenMsg struct {
	// Index is the row's position.
	Index int
	// ID is the row's ID.
	ID string
}

// KeyMap is the table's key bindings.
type KeyMap struct {
	Up       key.Binding
	Down     key.Binding
	Home     key.Binding
	End      key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Open     key.Binding
}

// DefaultKeyMap returns the standard bindings: arrows, with j and k as silent
// aliases so the hint bar stays short.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑↓", "move")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "down")),
		Home:     key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("home", "first")),
		End:      key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("end", "last")),
		PageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
		PageDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down")),
		Open:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
	}
}

// ShortHelp returns the bindings worth showing in the footer.
func (k KeyMap) ShortHelp() []key.Binding { return []key.Binding{k.Up, k.Open} }

// FullHelp returns the bindings grouped for the help overlay.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down, k.Home, k.End}, {k.PageUp, k.PageDown, k.Open}}
}

// Model is the accounts table.
type Model struct {
	// Keys are the bindings the table answers to.
	Keys KeyMap

	rows   []Row
	cursor int
	// hover is the row under the mouse pointer, or -1. It is drawn as a
	// quiet caret and never moves the selection: a click selects, and only
	// a click on the selected row opens it.
	hover  int
	width  int
	height int
	frame  int
	empty  string
}

// New returns a table over rows with the first row selected.
func New(rows []Row) Model {
	return Model{Keys: DefaultKeyMap(), rows: rows, hover: -1, empty: "No tools to show yet."}
}

// SetRows replaces the rows. The cursor stays on the row with the same ID
// when there is one, so a row that finishes loading does not move the
// selection out from under the user.
func (m Model) SetRows(rows []Row) Model {
	id := ""
	if r, ok := m.Selected(); ok {
		id = r.ID
	}
	m.rows = rows
	m.hover = -1
	if id != "" {
		for i, r := range rows {
			if r.ID == id {
				m.cursor = i
				return m
			}
		}
	}
	return m.SetCursor(m.cursor)
}

// Rows returns the current rows.
func (m Model) Rows() []Row { return m.rows }

// SetSize fixes the area the table draws into. Zero means the context's
// width, or its body height.
func (m Model) SetSize(w, h int) Model {
	m.width, m.height = max(0, w), max(0, h)
	return m
}

// SetFrame sets the spinner frame loading rows draw. The screen advances it
// from its own tick, so rendering stays a pure function of the model.
func (m Model) SetFrame(n int) Model {
	m.frame = n
	return m
}

// SetEmptyText replaces what the table says when it has no rows.
func (m Model) SetEmptyText(s string) Model {
	if s != "" {
		m.empty = s
	}
	return m
}

// Cursor returns the selected row's index.
func (m Model) Cursor() int { return m.cursor }

// SetCursor moves the selection, clamped to the rows.
func (m Model) SetCursor(i int) Model {
	m.cursor = min(max(i, 0), max(0, len(m.rows)-1))
	return m
}

// Selected returns the selected row and whether there is one.
func (m Model) Selected() (Row, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return Row{}, false
	}
	return m.rows[m.cursor], true
}

// ShortHelp returns the bindings worth showing in the footer.
func (m Model) ShortHelp() []key.Binding { return m.Keys.ShortHelp() }

// FullHelp returns the bindings grouped for the help overlay.
func (m Model) FullHelp() [][]key.Binding { return m.Keys.FullHelp() }

// Update handles the keys and the mouse wheel. Clicks and hover go through
// [Model.Pointer], because only the screen knows where the table was drawn.
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
		page := max(1, m.height-2)
		switch {
		case key.Matches(msg, m.Keys.Up):
			m = m.SetCursor(m.cursor - 1)
		case key.Matches(msg, m.Keys.Down):
			m = m.SetCursor(m.cursor + 1)
		case key.Matches(msg, m.Keys.Home):
			m = m.SetCursor(0)
		case key.Matches(msg, m.Keys.End):
			m = m.SetCursor(len(m.rows) - 1)
		case key.Matches(msg, m.Keys.PageUp):
			m = m.SetCursor(m.cursor - page)
		case key.Matches(msg, m.Keys.PageDown):
			m = m.SetCursor(m.cursor + page)
		case key.Matches(msg, m.Keys.Open):
			return m, m.open()
		default:
			return m, nil
		}
		m.hover = -1
	}
	return m, nil
}

// open is the command that reports the selected row as opened.
func (m Model) open() tea.Cmd {
	r, ok := m.Selected()
	if !ok {
		return nil
	}
	i := m.cursor
	return func() tea.Msg { return OpenMsg{Index: i, ID: r.ID} }
}
