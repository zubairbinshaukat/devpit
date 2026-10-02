// Package foldertree draws Browse folders: the folders that have account
// rules, as a tree under an "Everywhere" root, with the accounts each folder
// sets beside it.
//
//	FOLDER                      ACCOUNTS SET HERE
//	Everywhere                  claude default · git You · github default
//	├─ ▾ C:\Work                claude work · github work
//	│  └─ client                git client
//	├─ ▾ C:\Users\you\code
//	│  ├─ api                   vercel api-team
//	│  └─ web (here)            vercel web
//	└─ D:\oss                   ! drive not connected · claude oss
//
// Only folders with rules are worth a row. A folder without one is drawn,
// quietly, only where it joins two ruled folders, and a chain of folders
// with nothing in them is folded into one row (C:\Users\you\code above), so
// the tree stays as short as the rules are. [Build] makes that tree from a
// flat list of folders; a screen that builds its own gets the same folding
// from [New].
//
// A rule whose folder is gone, or whose drive is not plugged in, keeps its
// row and says so with a shape and a word, because the user has to see it to
// fix it.
//
// The component takes plain view-model values. It knows nothing of the
// accounts engine; it reports what the user picked and the screen acts.
package foldertree

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// State is whether a ruled folder is still where the rule says.
type State int

// The folder states.
const (
	// StateOK is a folder that exists.
	StateOK State = iota
	// StateMissing is a folder that is gone: the rule points at nothing.
	StateMissing
	// StateOffline is a folder on a drive that is not connected right now,
	// e.g. a USB disk. It may well come back, so it is a warning, not an
	// error.
	StateOffline
)

// Word is what a stale folder says beside it.
func (s State) Word() string {
	switch s {
	case StateMissing:
		return "folder not found"
	case StateOffline:
		return "drive not connected"
	default:
		return ""
	}
}

// Chip is one rule set at a folder: which account a tool uses there.
type Chip struct {
	// Tool is the tool's short name, e.g. "claude" or "github".
	Tool string
	// Account is the account's name, e.g. "work".
	Account string
}

// Node is one folder in the tree.
type Node struct {
	// ID identifies the node to the screen. Empty means the path.
	ID string
	// Path is the folder's full path.
	Path string
	// Name is what the row shows. Empty means the part of the path below
	// the parent node, or the whole path for a node at the top.
	Name string
	// Chips are the rules set at this folder, not the ones it inherits. A
	// node without any is a connector, drawn quietly.
	Chips []Chip
	// State says whether the folder is still there.
	State State
	// Current marks the folder the user is in.
	Current bool
	// Children are the nodes below this one.
	Children []Node
}

// Folder is one ruled folder in a flat list, the input to [Build].
type Folder struct {
	// Path is the folder's full path.
	Path string
	// Chips are the rules set at this folder.
	Chips []Chip
	// State says whether the folder is still there.
	State State
	// Current marks the folder the user is in.
	Current bool
}

// EverywhereID is the ID of the "Everywhere" row at the top of the tree.
const EverywhereID = "\x00everywhere"

// SelectedMsg is emitted when the selection lands on a different node, by
// key or by click, so a screen can show that folder's details beside the
// tree.
type SelectedMsg struct {
	// ID is the node's ID, or [EverywhereID].
	ID string
	// Path is the node's path; empty for Everywhere.
	Path string
}

// OpenMsg is emitted when the user opens a node: Enter on it, or a click on
// the node that is already selected.
type OpenMsg struct {
	// ID is the node's ID, or [EverywhereID].
	ID string
	// Path is the node's path; empty for Everywhere.
	Path string
}

// KeyMap is the tree's key bindings.
type KeyMap struct {
	Up       key.Binding
	Down     key.Binding
	Home     key.Binding
	End      key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Collapse key.Binding
	Expand   key.Binding
	Toggle   key.Binding
	Open     key.Binding
	Filter   key.Binding
}

// DefaultKeyMap returns the standard bindings. Arrows are the documented
// keys; j, k, h and l are silent aliases.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑↓", "move")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "down")),
		Home:     key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("home", "first")),
		End:      key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("end", "last")),
		PageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
		PageDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down")),
		Collapse: key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←→", "fold")),
		Expand:   key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→", "unfold")),
		Toggle:   key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "fold/unfold")),
		Open:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	}
}

// ShortHelp returns the bindings worth showing in the footer.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Collapse, k.Open, k.Filter}
}

// FullHelp returns the bindings grouped for the help overlay.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Home, k.End, k.PageUp, k.PageDown},
		{k.Collapse, k.Expand, k.Toggle, k.Open, k.Filter},
	}
}

// item is one node, flattened, with its place in the tree.
type item struct {
	id      string
	path    string
	name    string
	chips   []Chip
	state   State
	current bool
	parent  int
	kids    []int
	depth   int
}

// row is one visible node and what its guides need to know: whether it is
// the last of its visible siblings, and for every level above it whether
// the ancestor there was, which decides between "│" and blank.
type row struct {
	item  int
	last  bool
	trail []bool
	// open is a node drawn unfolded with visible children under it.
	open bool
	// hidden counts the nodes a fold is hiding under this one.
	hidden int
	// match is false for an ancestor shown only to connect a filter match.
	match bool
}

// Model is the folder tree.
type Model struct {
	// Keys are the bindings the tree answers to.
	Keys KeyMap

	items     []item
	rows      []row
	collapsed map[string]bool
	cursor    int
	hover     int
	width     int
	height    int

	filter    string
	filtering bool
	input     textinput.Model
}

// New returns a tree with the "Everywhere" row on top, showing everywhere,
// and roots under it. Chains of chip-less folders with a single child are
// folded into one row. Every node starts unfolded, and the cursor starts on
// the current folder when one is marked, or on Everywhere.
func New(everywhere []Chip, roots []Node) Model {
	m := Model{
		Keys:      DefaultKeyMap(),
		collapsed: map[string]bool{},
		hover:     -1,
		input:     newFilterInput(),
	}
	m.items = append(m.items, item{id: EverywhereID, name: "Everywhere", chips: everywhere, parent: -1})
	for _, n := range compact(roots, "") {
		m.flatten(n, 0, 1)
	}
	m.rebuild()
	for i, r := range m.rows {
		if m.items[r.item].current {
			m.cursor = i
			break
		}
	}
	return m
}

// flatten appends n and everything under it, depth first.
func (m *Model) flatten(n Node, parent, depth int) {
	id := n.ID
	if id == "" {
		id = n.Path
	}
	i := len(m.items)
	m.items = append(m.items, item{
		id: id, path: n.Path, name: n.Name, chips: n.Chips,
		state: n.State, current: n.Current, parent: parent, depth: depth,
	})
	m.items[parent].kids = append(m.items[parent].kids, i)
	for _, c := range n.Children {
		m.flatten(c, i, depth+1)
	}
}

// SetSize fixes the area the tree draws into. Zero means the context's
// width, or its body height.
func (m Model) SetSize(w, h int) Model {
	m.width, m.height = max(0, w), max(0, h)
	m.input.SetWidth(max(4, m.width-6))
	return m
}

// Cursor returns the index of the selected visible row.
func (m Model) Cursor() int { return m.cursor }

// Selected returns the selected node's ID and path. ok is false only for an
// empty filter result.
func (m Model) Selected() (id, path string, ok bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return "", "", false
	}
	it := m.items[m.rows[m.cursor].item]
	return it.id, it.path, true
}

// Select moves the cursor to the node with the given ID, unfolding its
// ancestors so it is visible. It reports whether the node exists.
func (m Model) Select(id string) (Model, bool) {
	for i, it := range m.items {
		if it.id != id {
			continue
		}
		for p := it.parent; p > 0; p = m.items[p].parent {
			delete(m.collapsed, m.items[p].id)
		}
		m.rebuild()
		for ri, r := range m.rows {
			if r.item == i {
				m.cursor = ri
				return m, true
			}
		}
	}
	return m, false
}

// Filter returns the current filter text.
func (m Model) Filter() string { return m.filter }

// Filtering reports whether the filter input has the keyboard. While it
// does, the screen should pass every key here, Esc included.
func (m Model) Filtering() bool { return m.filtering }

// VisibleCount is how many nodes are on the list, Everywhere included.
func (m Model) VisibleCount() int { return len(m.rows) }

// ShortHelp returns the bindings worth showing in the footer.
func (m Model) ShortHelp() []key.Binding { return m.Keys.ShortHelp() }

// FullHelp returns the bindings grouped for the help overlay.
func (m Model) FullHelp() [][]key.Binding { return m.Keys.FullHelp() }

// Update handles the keys and the mouse wheel. Clicks and hover go through
// [Model.Pointer], because only the screen knows where the tree was drawn.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			return m.moveTo(m.cursor - 1)
		case tea.MouseWheelDown:
			return m.moveTo(m.cursor + 1)
		}
		return m, nil
	case tea.KeyPressMsg:
		if m.filtering {
			return m.updateFilter(msg)
		}
		page := max(1, m.height-3)
		switch {
		case key.Matches(msg, m.Keys.Up):
			return m.moveTo(m.cursor - 1)
		case key.Matches(msg, m.Keys.Down):
			return m.moveTo(m.cursor + 1)
		case key.Matches(msg, m.Keys.Home):
			return m.moveTo(0)
		case key.Matches(msg, m.Keys.End):
			return m.moveTo(len(m.rows) - 1)
		case key.Matches(msg, m.Keys.PageUp):
			return m.moveTo(m.cursor - page)
		case key.Matches(msg, m.Keys.PageDown):
			return m.moveTo(m.cursor + page)
		case key.Matches(msg, m.Keys.Collapse):
			return m.left()
		case key.Matches(msg, m.Keys.Expand):
			return m.right()
		case key.Matches(msg, m.Keys.Toggle):
			m.toggle(m.cursor)
			return m, nil
		case key.Matches(msg, m.Keys.Open):
			return m, m.open()
		case key.Matches(msg, m.Keys.Filter):
			m.filtering = true
			m.input.SetValue(m.filter)
			m.input.CursorEnd()
			return m, m.input.Focus()
		}
	}
	return m, nil
}

// updateFilter routes keys to the filter input. Enter keeps the filter; Esc
// clears it, which is what someone reaching for Esc in a search wants.
func (m Model) updateFilter(km tea.KeyPressMsg) (Model, tea.Cmd) {
	switch km.Code {
	case tea.KeyEnter:
		m.filtering = false
		m.input.Blur()
		return m, nil
	case tea.KeyEscape:
		m.filtering = false
		m.input.Blur()
		m.input.SetValue("")
		return m.applyFilter("")
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(km)
	next, sel := m.applyFilter(m.input.Value())
	return next, tea.Batch(cmd, sel)
}

// applyFilter narrows the tree to the nodes whose path contains text, plus
// the ancestors that connect them, and puts the cursor on the first match.
func (m Model) applyFilter(text string) (Model, tea.Cmd) {
	before := m.selectedID()
	m.filter = text
	m.rebuild()
	m.cursor = 0
	for i, r := range m.rows {
		if r.match && r.item != 0 {
			m.cursor = i
			break
		}
	}
	return m, m.selectedCmd(before)
}

// moveTo moves the cursor to visible row i, clamped, and reports the move.
func (m Model) moveTo(i int) (Model, tea.Cmd) {
	before := m.selectedID()
	m.cursor = min(max(i, 0), max(0, len(m.rows)-1))
	m.hover = -1
	return m, m.selectedCmd(before)
}

// left folds the selected node, or climbs to its parent when it is already
// folded or has nothing to fold, the way a file tree does, so holding ←
// walks all the way out.
func (m Model) left() (Model, tea.Cmd) {
	if m.cursor >= len(m.rows) {
		return m, nil
	}
	r := m.rows[m.cursor]
	if r.open {
		m.toggle(m.cursor)
		return m, nil
	}
	p := m.items[r.item].parent
	for i, rr := range m.rows {
		if rr.item == p {
			return m.moveTo(i)
		}
	}
	return m, nil
}

// right unfolds the selected node, or steps to its first child when it is
// already unfolded.
func (m Model) right() (Model, tea.Cmd) {
	if m.cursor >= len(m.rows) {
		return m, nil
	}
	r := m.rows[m.cursor]
	switch {
	case r.open:
		return m.moveTo(m.cursor + 1)
	case r.hidden > 0:
		m.toggle(m.cursor)
	}
	return m, nil
}

// toggle folds or unfolds visible row i. The row's own index cannot change,
// since only rows after it come or go, so the cursor stays put. Everywhere
// never folds: it is the trunk, not a folder.
func (m *Model) toggle(i int) {
	if i < 0 || i >= len(m.rows) || m.rows[i].item == 0 {
		return
	}
	r := m.rows[i]
	if !r.open && r.hidden == 0 {
		return
	}
	id := m.items[r.item].id
	m.collapsed[id] = !m.collapsed[id]
	m.rebuild()
}

// open is the command that reports the selected node as opened.
func (m Model) open() tea.Cmd {
	id, path, ok := m.Selected()
	if !ok {
		return nil
	}
	return func() tea.Msg { return OpenMsg{ID: id, Path: path} }
}

// selectedID is the ID under the cursor, or "".
func (m Model) selectedID() string {
	id, _, _ := m.Selected()
	return id
}

// selectedCmd reports the selection when it differs from before.
func (m Model) selectedCmd(before string) tea.Cmd {
	id, path, ok := m.Selected()
	if !ok || id == before {
		return nil
	}
	return func() tea.Msg { return SelectedMsg{ID: id, Path: path} }
}

// rebuild recomputes the visible rows from the folds and the filter.
func (m *Model) rebuild() {
	needle := normPath(strings.TrimSpace(m.filter))
	keep := make([]bool, len(m.items))
	match := make([]bool, len(m.items))
	for i := len(m.items) - 1; i >= 0; i-- {
		it := m.items[i]
		match[i] = needle == "" || (i > 0 && strings.Contains(normPath(it.path), needle))
		keep[i] = keep[i] || match[i]
		if keep[i] && it.parent >= 0 {
			keep[it.parent] = true
		}
	}
	keep[0] = true

	m.rows = m.rows[:0]
	var walk func(i int, last bool, trail []bool)
	walk = func(i int, last bool, trail []bool) {
		r := row{item: i, last: last, trail: trail, match: match[i]}
		var kids []int
		for _, k := range m.items[i].kids {
			if keep[k] {
				kids = append(kids, k)
			}
		}
		folded := needle == "" && m.collapsed[m.items[i].id] && i != 0
		if folded {
			r.hidden = m.descendants(i)
		}
		r.open = len(kids) > 0 && !folded
		m.rows = append(m.rows, r)
		if !r.open {
			return
		}
		next := trail
		if i != 0 {
			next = append(append([]bool(nil), trail...), last)
		}
		for n, k := range kids {
			walk(k, n == len(kids)-1, next)
		}
	}
	walk(0, true, nil)
	m.cursor = min(max(m.cursor, 0), max(0, len(m.rows)-1))
}

// descendants counts every node under item i.
func (m Model) descendants(i int) int {
	n := 0
	for _, k := range m.items[i].kids {
		n += 1 + m.descendants(k)
	}
	return n
}

// normPath lower-cases a path and turns its separators one way, so a filter
// typed with either slash matches.
func normPath(p string) string { return strings.ToLower(strings.ReplaceAll(p, "/", `\`)) }
