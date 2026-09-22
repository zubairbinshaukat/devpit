// Package restable is the scan results table: one row per reclaimable item,
// grouped by the project that owns it, with a tick box, a size, a relative
// bar and a risk label on every row.
//
// Three things shape the design.
//
// It is fed while the scan is still running. [Model.Append] takes a batch at
// a time and never re-sorts mid-scan, because a table whose rows jump around
// while the user is reading it is worse than one that is briefly out of
// order. Sorting happens when the scan ends, or the moment the user asks for
// it with "s".
//
// It never renders a row it is not showing. A scan of a real projects folder
// finds thousands of items; the table formats exactly the rows inside its
// window and nothing else, which is why it keeps its own scroll offset
// instead of handing a fully rendered document to a viewport.
//
// It decides what is pre-ticked, and that decision is safety-critical.
// [preselect] ticks Safe items and nothing else: never a Careful item, never
// an active project, never an unverified match, never a cloud placeholder.
// Rules 4 and 5 of docs/safety.md live in that one function and the tests
// beside it.
package restable

import (
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// SortMode is the order rows are shown in.
type SortMode int

// The sort orders, cycled by the "s" key in this order.
const (
	// SortSize puts the biggest item first, which is what the user came for.
	SortSize SortMode = iota
	// SortName orders by project then item name.
	SortName
	// SortAge puts the least recently used project first.
	SortAge
)

// String returns the word shown in the table's status line.
func (s SortMode) String() string {
	switch s {
	case SortSize:
		return "size"
	case SortName:
		return "name"
	case SortAge:
		return "age"
	default:
		return "size"
	}
}

// ProceedMsg is emitted when the user presses Enter, meaning "I am done
// choosing". The screen decides what happens next; the table never deletes
// anything and never confirms anything.
type ProceedMsg struct{}

// KeyMap is the table's key bindings.
type KeyMap struct {
	Up       key.Binding
	Down     key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Toggle   key.Binding
	All      key.Binding
	Sort     key.Binding
	Filter   key.Binding
	Older    key.Binding
	Collapse key.Binding
	Expand   key.Binding
	Proceed  key.Binding
}

// DefaultKeyMap returns the standard bindings. Arrow keys are the documented
// ones; j and k are silent aliases so the hint bar stays short.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑↓", "move")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "down")),
		PageUp:   key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up")),
		PageDown: key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down")),
		Toggle:   key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "tick")),
		All:      key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all/none")),
		Sort:     key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort")),
		Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Older:    key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "older only")),
		Collapse: key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←→", "fold")),
		Expand:   key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→", "unfold")),
		Proceed:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "continue")),
	}
}

// ShortHelp returns the bindings worth showing in the footer.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Toggle, k.All, k.Filter, k.Proceed}
}

// FullHelp returns the bindings grouped for the help overlay.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown},
		{k.Toggle, k.All, k.Collapse, k.Expand},
		{k.Sort, k.Filter, k.Older, k.Proceed},
	}
}

// rowKind says whether a row is a project heading or an item under one.
type rowKind int

const (
	rowGroup rowKind = iota
	rowItem
)

// row is one printable line: either a group heading or one item in a group.
type row struct {
	kind  rowKind
	group int
	item  int
}

// group is one project's items, with the aggregates the heading shows.
type group struct {
	key      string
	label    string
	items    []int
	size     uint64
	lastUsed time.Time
	active   bool
}

// Model is the results table.
type Model struct {
	// Keys are the bindings the table answers to.
	Keys KeyMap

	items []scan.Item
	marks []bool

	collapsed map[string]bool

	sortMode   SortMode
	sortAsked  bool
	streaming  bool
	filter     string
	filtering  bool
	input      textinput.Model
	olderOnly  bool
	olderDays  int
	width      int
	height     int
	cursor     int
	top        int
	rows       []row
	groups     []group
	maxSize    uint64
	shownBytes uint64
	shownCount int

	now func() time.Time
}

// New returns an empty table.
func New() Model {
	m := Model{
		Keys:      DefaultKeyMap(),
		collapsed: map[string]bool{},
		input:     newFilterInput(),
		olderDays: 30,
		width:     80,
		height:    16,
		now:       time.Now,
	}
	m.rebuild()
	return m
}

// SetNow replaces the clock the age column reads, so tests can pin relative
// times without sleeping.
func (m Model) SetNow(now func() time.Time) Model {
	if now != nil {
		m.now = now
	}
	return m
}

// SetSize sets the area the table draws into.
func (m Model) SetSize(w, h int) Model {
	m.width, m.height = max(0, w), max(0, h)
	m.input.SetWidth(max(4, m.width-2))
	m.clampCursor()
	return m
}

// SetOlderDays sets the threshold the "o" filter uses. It comes from the
// user's configuration, which the table never reads for itself.
func (m Model) SetOlderDays(days int) Model {
	if days > 0 && days != m.olderDays {
		m.olderDays = days
		m.rebuild()
	}
	return m
}

// SetStreaming tells the table whether a scan is still feeding it. While it
// is, rows keep their arrival order unless the user asked for a sort; when it
// is turned off the table sorts once, which is the moment the scan ends.
func (m Model) SetStreaming(streaming bool) Model {
	if m.streaming == streaming {
		return m
	}
	m.streaming = streaming
	m.rebuild()
	return m
}

// SetItems replaces every row and applies the pre-selection rule.
func (m Model) SetItems(items []scan.Item) Model {
	m.items = append([]scan.Item(nil), items...)
	m.marks = preselect(m.items)
	m.cursor, m.top = 0, 0
	m.rebuild()
	return m
}

// Append adds a batch of newly found items, pre-selecting the new ones and
// leaving every existing tick alone.
func (m Model) Append(items []scan.Item) Model {
	if len(items) == 0 {
		return m
	}
	next := make([]scan.Item, 0, len(m.items)+len(items))
	next = append(next, m.items...)
	next = append(next, items...)

	marks := make([]bool, 0, len(next))
	marks = append(marks, m.marks...)
	marks = append(marks, preselect(items)...)

	m.items, m.marks = next, marks
	m.rebuild()
	return m
}

// Items returns every item the table holds, filtered or not.
func (m Model) Items() []scan.Item { return m.items }

// Len is how many items the table holds.
func (m Model) Len() int { return len(m.items) }

// VisibleCount is how many items pass the current filters.
func (m Model) VisibleCount() int { return m.shownCount }

// Selected returns the ticked items, in the order they are shown.
func (m Model) Selected() []scan.Item {
	out := make([]scan.Item, 0, len(m.items))
	for _, g := range m.groups {
		for _, i := range g.items {
			if m.marks[i] {
				out = append(out, m.items[i])
			}
		}
	}
	return out
}

// SelectedCount is how many items are ticked and how many bytes they hold.
// The footer shows both, live.
func (m Model) SelectedCount() (int, uint64) {
	var n int
	var bytes uint64
	for i := range m.items {
		if m.marks[i] {
			n++
			bytes += m.items[i].Size
		}
	}
	return n, bytes
}

// Sort returns the current sort order.
func (m Model) Sort() SortMode { return m.sortMode }

// Filter returns the current filter text.
func (m Model) Filter() string { return m.filter }

// Filtering reports whether the filter input has the keyboard.
func (m Model) Filtering() bool { return m.filtering }

// OlderOnly reports whether the "older than" filter is on.
func (m Model) OlderOnly() bool { return m.olderOnly }

// Update handles the table's keys. It never blocks and never touches the
// filesystem.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}

	if m.filtering {
		return m.updateFilter(km)
	}

	switch {
	case key.Matches(km, m.Keys.Up):
		m.moveCursor(-1)
	case key.Matches(km, m.Keys.Down):
		m.moveCursor(1)
	case key.Matches(km, m.Keys.PageUp):
		m.moveCursor(-m.bodyHeight())
	case key.Matches(km, m.Keys.PageDown):
		m.moveCursor(m.bodyHeight())
	case key.Matches(km, m.Keys.Toggle):
		m.toggleAtCursor()
	case key.Matches(km, m.Keys.All):
		m.toggleAll()
	case key.Matches(km, m.Keys.Sort):
		m.sortMode = (m.sortMode + 1) % 3
		m.sortAsked = true
		m.rebuild()
	case key.Matches(km, m.Keys.Older):
		m.olderOnly = !m.olderOnly
		m.rebuild()
	case key.Matches(km, m.Keys.Collapse):
		m.fold(true)
	case key.Matches(km, m.Keys.Expand):
		m.fold(false)
	case key.Matches(km, m.Keys.Filter):
		m.filtering = true
		m.input.SetValue(m.filter)
		m.input.CursorEnd()
		return m, m.input.Focus()
	case key.Matches(km, m.Keys.Proceed):
		return m, func() tea.Msg { return ProceedMsg{} }
	}
	return m, nil
}

// updateFilter routes keys to the filter input. Enter keeps the filter, Esc
// throws it away and restores what was there before.
func (m Model) updateFilter(km tea.KeyPressMsg) (Model, tea.Cmd) {
	switch km.Code {
	case tea.KeyEnter:
		m.filter = m.input.Value()
		m.filtering = false
		m.input.Blur()
		m.cursor, m.top = 0, 0
		m.rebuild()
		return m, nil
	case tea.KeyEscape:
		m.filtering = false
		m.input.SetValue(m.filter)
		m.input.Blur()
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(km)
	m.filter = m.input.Value()
	m.cursor, m.top = 0, 0
	m.rebuild()
	return m, cmd
}

// moveCursor steps the cursor over printable rows and keeps the window
// around it.
func (m *Model) moveCursor(delta int) {
	if len(m.rows) == 0 {
		m.cursor, m.top = 0, 0
		return
	}
	m.cursor += delta
	m.clampCursor()
}

// clampCursor keeps the cursor inside the row list and the window around the
// cursor.
func (m *Model) clampCursor() {
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = max(0, len(m.rows)-1)
	}
	h := m.bodyHeight()
	if h <= 0 {
		m.top = 0
		return
	}
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+h {
		m.top = m.cursor - h + 1
	}
	if maxTop := max(0, len(m.rows)-h); m.top > maxTop {
		m.top = maxTop
	}
	if m.top < 0 {
		m.top = 0
	}
}

// toggleAt ticks the item on a row, or every item in the group when the row
// is a heading.
func (m *Model) toggleAt(r row) {
	if r.kind == rowItem {
		m.marks[r.item] = !m.marks[r.item]
		return
	}
	g := m.groups[r.group]
	all := true
	for _, i := range g.items {
		if !m.marks[i] {
			all = false
			break
		}
	}
	for _, i := range g.items {
		m.marks[i] = !all
	}
}

// toggleAtCursor applies toggleAt to the current row.
func (m *Model) toggleAtCursor() {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	m.toggleAt(m.rows[m.cursor])
}

// toggleAll ticks everything currently shown, or unticks it when everything
// shown is already ticked.
func (m *Model) toggleAll() {
	all := true
	for _, g := range m.groups {
		for _, i := range g.items {
			if !m.marks[i] {
				all = false
				break
			}
		}
	}
	for _, g := range m.groups {
		for _, i := range g.items {
			m.marks[i] = !all
		}
	}
}

// fold collapses or expands the group the cursor is in. Collapsing from an
// item row moves the cursor up to the heading so it stays visible.
func (m *Model) fold(collapse bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return
	}
	r := m.rows[m.cursor]
	g := m.groups[r.group]
	if m.collapsed[g.key] == collapse {
		return
	}
	m.collapsed[g.key] = collapse
	m.rebuildRows()
	if collapse {
		for i, rr := range m.rows {
			if rr.kind == rowGroup && rr.group == r.group {
				m.cursor = i
				break
			}
		}
	}
	m.clampCursor()
}

// bodyHeight is how many item rows fit, once the column heading and the
// filter line have taken theirs.
func (m Model) bodyHeight() int {
	h := m.height - 1 // column heading
	if m.filtering || m.filter != "" || m.olderOnly {
		h--
	}
	return max(0, h)
}

// rebuild recomputes the groups and the printable rows from the items, the
// filters and the sort order.
func (m *Model) rebuild() {
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}

	keep := m.keepers()

	byKey := map[string]int{}
	m.groups = m.groups[:0]
	m.maxSize, m.shownBytes, m.shownCount = 0, 0, 0

	for _, i := range keep {
		it := m.items[i]
		k := groupKey(it)
		gi, ok := byKey[k]
		if !ok {
			gi = len(m.groups)
			byKey[k] = gi
			m.groups = append(m.groups, group{key: k, label: groupLabel(it)})
		}
		g := &m.groups[gi]
		g.items = append(g.items, i)
		g.size += it.Size
		if it.Active {
			g.active = true
		}
		if it.LastUsed.After(g.lastUsed) {
			g.lastUsed = it.LastUsed
		}
		if it.Size > m.maxSize {
			m.maxSize = it.Size
		}
		m.shownBytes += it.Size
		m.shownCount++
	}

	if m.sorting() {
		m.applySort()
	}
	m.rebuildRows()
	m.clampCursor()
}

// keepers returns the indices of the items that pass the filters, in
// insertion order.
func (m Model) keepers() []int {
	needle := strings.ToLower(strings.TrimSpace(m.filter))
	cutoff := time.Time{}
	if m.olderOnly {
		cutoff = m.clock().AddDate(0, 0, -m.olderDays)
	}

	out := make([]int, 0, len(m.items))
	for i := range m.items {
		it := m.items[i]
		if needle != "" && !matches(it, needle) {
			continue
		}
		if m.olderOnly {
			ref := it.LastUsed
			if ref.IsZero() {
				ref = it.ModTime
			}
			if ref.IsZero() || !ref.Before(cutoff) {
				continue
			}
		}
		out = append(out, i)
	}
	return out
}

// sorting reports whether rows may be reordered right now: never in the
// middle of a scan, unless the user asked for a sort explicitly.
func (m Model) sorting() bool { return !m.streaming || m.sortAsked }

// applySort orders the groups and the items inside each of them.
func (m *Model) applySort() {
	less := func(a, b int) bool {
		x, y := m.items[a], m.items[b]
		switch m.sortMode {
		case SortName:
			if !strings.EqualFold(x.Name, y.Name) {
				return strings.ToLower(x.Name) < strings.ToLower(y.Name)
			}
		case SortAge:
			if !x.LastUsed.Equal(y.LastUsed) {
				return x.LastUsed.Before(y.LastUsed)
			}
		case SortSize:
			if x.Size != y.Size {
				return x.Size > y.Size
			}
		}
		if x.Size != y.Size {
			return x.Size > y.Size
		}
		return x.Path < y.Path
	}
	for gi := range m.groups {
		g := &m.groups[gi]
		sort.SliceStable(g.items, func(a, b int) bool { return less(g.items[a], g.items[b]) })
	}
	sort.SliceStable(m.groups, func(a, b int) bool {
		x, y := m.groups[a], m.groups[b]
		switch m.sortMode {
		case SortName:
			if !strings.EqualFold(x.label, y.label) {
				return strings.ToLower(x.label) < strings.ToLower(y.label)
			}
		case SortAge:
			if !x.lastUsed.Equal(y.lastUsed) {
				return x.lastUsed.Before(y.lastUsed)
			}
		case SortSize:
			if x.size != y.size {
				return x.size > y.size
			}
		}
		if x.size != y.size {
			return x.size > y.size
		}
		return x.key < y.key
	})
}

// rebuildRows turns the groups into the printable row list, honouring which
// groups are folded.
func (m *Model) rebuildRows() {
	m.rows = m.rows[:0]
	for gi, g := range m.groups {
		m.rows = append(m.rows, row{kind: rowGroup, group: gi})
		if m.collapsed[g.key] {
			continue
		}
		for _, i := range g.items {
			m.rows = append(m.rows, row{kind: rowItem, group: gi, item: i})
		}
	}
}

// clock returns the time source, defaulting to the wall clock.
func (m Model) clock() time.Time {
	if m.now == nil {
		return time.Now()
	}
	return m.now()
}

// matches reports whether an item passes the text filter.
func matches(it scan.Item, needle string) bool {
	return strings.Contains(strings.ToLower(it.Name), needle) ||
		strings.Contains(strings.ToLower(it.Path), needle) ||
		strings.Contains(strings.ToLower(it.Project), needle) ||
		strings.Contains(strings.ToLower(it.Rule), needle)
}

// groupKey is the identity of the project a row belongs under, compared
// without regard to case because Windows paths are case-insensitive.
func groupKey(it scan.Item) string {
	p := it.Project
	if p == "" {
		p = parentPath(it.Path)
	}
	return strings.ToLower(p)
}

// groupLabel is the heading text for a project.
func groupLabel(it scan.Item) string {
	p := it.Project
	if p == "" {
		p = parentPath(it.Path)
	}
	if b := baseName(p); b != "" {
		return b
	}
	return p
}

// parentPath is the directory holding path, for both separators, so the
// table reads the same whichever platform the tests run on.
func parentPath(path string) string {
	trimmed := strings.TrimRight(path, `\/`)
	if i := strings.LastIndexAny(trimmed, `\/`); i >= 0 {
		return trimmed[:i]
	}
	return trimmed
}

// baseName is the last path element, for both separators.
func baseName(path string) string {
	trimmed := strings.TrimRight(path, `\/`)
	if i := strings.LastIndexAny(trimmed, `\/`); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// preselect decides what is ticked when a batch of items arrives, and it is
// the whole of safety rules 4 and 5.
//
// Only a Safe item is ever ticked. A Careful item is never ticked, because
// the user may not be able to get it back. An active project is never
// ticked, because the user is working in it right now. An unverified match is
// never ticked, because the marker file that proves it is junk was not there.
// A cloud placeholder is never ticked, because touching it downloads it.
//
// Review items are not ticked either: they are recoverable but cost time or
// bandwidth to rebuild, so the user chooses them deliberately.
func preselect(items []scan.Item) []bool {
	marks := make([]bool, len(items))
	for i, it := range items {
		marks[i] = it.Tier == scan.TierSafe &&
			!it.Active &&
			!it.Unverified &&
			!it.Cloud
	}
	return marks
}
