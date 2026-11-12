// Package filepicker asks the user which archive to extract.
//
// It is the file-shaped sibling of pathpicker: a list of archives Devpit
// already knows about (unfinished extractions first, then the big archives it
// found), a row to type a path by hand and a row that opens the Windows file
// picker. Whatever the answer, it goes through the caller's identify function
// before it leaves the component, so a network path, a split ZIP or a file
// that is not an archive is refused here, in one plain sentence under the
// list, and never reaches the screen that would run hatchzip.
//
// Identifying a path touches the disk (an Lstat of every part), so it runs in
// a command. Its result, and the file picker's, come back as [CheckedMsg] and
// [BrowsedMsg]; the owning screen must hand both back to [Model.Update], the
// same rule pathpicker has for its BrowsedMsg.
//
// The file picker is the only platform-specific part and lives behind
// browseSupported and browse in the _windows.go and _other.go files.
package filepicker

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Row identifiers for the two rows that are not an archive.
const (
	rowType   = "\x00type"
	rowBrowse = "\x00browse"
)

// Row is one archive the list offers.
type Row struct {
	// Set is the archive: a single file or a split set.
	Set archive.Set
	// Unfinished says hatchzip's journal is beside it.
	Unfinished bool
	// Started is when the unfinished extraction began, when Devpit knows.
	Started time.Time
}

// ChosenMsg is emitted when the user settles on an archive. The set in it has
// already been through the caller's identify function, or came from a row the
// caller supplied.
type ChosenMsg struct {
	// Set is the chosen archive.
	Set archive.Set
}

// CancelledMsg is emitted when the user backs out of the picker.
type CancelledMsg struct{}

// BrowsedMsg carries the result of the Windows file picker. It is exported
// because its command returns to the top of the program's Update loop, and
// the owning screen has to route it back into [Model.Update] itself.
type BrowsedMsg struct {
	path string
	err  error
}

// CheckedMsg carries the result of identifying a typed or browsed path. Like
// [BrowsedMsg], the owning screen routes it back into [Model.Update].
type CheckedMsg struct {
	set archive.Set
	err error
}

// IdentifyFunc turns a path into an archive set, or says why it is refused.
// The real one is extract.Identify.
type IdentifyFunc func(path string) (archive.Set, error)

// ErrEmptyPath means nothing was typed.
var ErrEmptyPath = errors.New("type the path of an archive first")

// KeyMap is the picker's key bindings.
type KeyMap struct {
	Move   key.Binding
	Select key.Binding
	Back   key.Binding
}

// DefaultKeyMap returns the standard bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Move:   key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑↓", "move")),
		Select: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose")),
		Back:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// ShortHelp returns the bindings for the footer.
func (k KeyMap) ShortHelp() []key.Binding { return []key.Binding{k.Move, k.Select, k.Back} }

// FullHelp returns the bindings grouped for the help overlay.
func (k KeyMap) FullHelp() [][]key.Binding { return [][]key.Binding{{k.Move, k.Select, k.Back}} }

// Model is the archive picker.
type Model struct {
	// Keys are the bindings the picker answers to.
	Keys KeyMap

	menu  menu.Model
	rows  []Row
	input textinput.Model

	typing   bool // the text field has the keyboard
	browsing bool // the Windows file picker is open
	checking bool // a path is being identified
	loading  bool // the caller is still looking for archives
	only     bool // list the caller's rows and nothing else
	refusal  string

	// identify, browseFn and now are the things that touch the outside
	// world. They are fields so tests supply their own.
	identify IdentifyFunc
	browseFn func(start string) (string, error)
	now      func() time.Time
	// canBrowse says the Browse row works on this build.
	canBrowse bool
	// startDir is where the file picker opens when no archive row is
	// highlighted.
	startDir string
}

// New returns a picker with no archive rows yet. identify checks every typed
// or browsed path before it is chosen.
func New(identify IdentifyFunc) Model {
	m := Model{
		Keys:      DefaultKeyMap(),
		input:     newPathInput(),
		identify:  identify,
		browseFn:  browse,
		now:       time.Now,
		canBrowse: browseSupported,
	}
	m.menu = menu.New(m.items())
	return m
}

// WithBrowse replaces the platform file picker, for tests.
func (m Model) WithBrowse(fn func(start string) (string, error)) Model {
	if fn != nil {
		m.browseFn = fn
	}
	return m
}

// WithBrowseAvailable says whether the Browse row is offered, whatever the
// build. Tests pin it so a frame is the same on every OS.
func (m Model) WithBrowseAvailable(on bool) Model {
	m.canBrowse = on
	m.menu = m.menu.SetItems(m.items())
	return m
}

// WithClock replaces the clock behind "started 2 days ago".
func (m Model) WithClock(now func() time.Time) Model {
	if now != nil {
		m.now = now
	}
	return m
}

// WithStartDir sets the folder the file picker opens in when no archive row
// is highlighted.
func (m Model) WithStartDir(dir string) Model {
	m.startDir = dir
	return m
}

// SetRows replaces the archive rows and ends the "looking" note. The cursor
// goes to the first row.
func (m Model) SetRows(rows []Row) Model {
	m.rows = rows
	m.loading = false
	m.menu = menu.New(m.items())
	return m
}

// Only lists rows and nothing else: no typed path, no file picker. Share uses
// it to offer exactly the archives a copy brought.
func (m Model) Only(rows []Row) Model {
	m.only = true
	return m.SetRows(rows)
}

// SetLoading shows or hides the note that archives are still being looked
// for.
func (m Model) SetLoading(on bool) Model {
	m.loading = on
	return m
}

// SetRefusal shows a refusal sentence under the list, as when an archive the
// caller opened by itself was refused. An empty string clears it.
func (m Model) SetRefusal(sentence string) Model {
	m.refusal = sentence
	return m
}

// Rows returns the archive rows.
func (m Model) Rows() []Row { return m.rows }

// Typing reports whether the text field has the keyboard.
func (m Model) Typing() bool { return m.typing }

// Refusal is the sentence shown for the last refused path, or "".
func (m Model) Refusal() string { return m.refusal }

// Update handles the picker's keys and its two async results.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case BrowsedMsg:
		m.browsing = false
		if msg.err != nil {
			m.refusal = Refusal(msg.err)
			return m, nil
		}
		if msg.path == "" { // the user cancelled the dialog
			return m, nil
		}
		return m.check(msg.path)

	case CheckedMsg:
		m.checking = false
		if msg.err != nil {
			m.refusal = Refusal(msg.err)
			return m, nil
		}
		return m.choose(msg.set)

	case tea.PasteMsg:
		// A pasted path goes into the field, and nowhere else.
		if !m.typing || m.browsing || m.checking {
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.refusal = ""
		return m, cmd

	case tea.KeyPressMsg:
		if m.browsing || m.checking {
			return m, nil
		}
		if m.typing {
			return m.updateTyping(msg)
		}
		switch {
		case key.Matches(msg, m.Keys.Back):
			return m, func() tea.Msg { return CancelledMsg{} }
		case key.Matches(msg, m.Keys.Select):
			return m.activate()
		}
		next, cmd := m.menu.Update(msg)
		m.menu = next
		return m, cmd
	}
	return m, nil
}

// listTop is the row of the picker's own view the list starts on: under the
// question and the blank line after it.
const listTop = 2

// Pointer handles the mouse for a picker drawn top rows below the start of
// the screen body: a click on a row picks it, hovering highlights it, the
// wheel moves through the list. handled is false for anything that is not the
// mouse, and while the path is being typed or checked.
func (m Model) Pointer(ctx uictx.Context, msg tea.Msg, top int) (next Model, cmd tea.Cmd, handled bool) {
	if m.typing || m.browsing || m.checking {
		return m, nil, false
	}
	m.menu = m.sized(ctx)
	switch pm := msg.(type) {
	case tea.MouseClickMsg:
		if pm.Button != tea.MouseLeft {
			return m, nil, true
		}
		i, ok := m.menu.RowAt(ctx, ctx.BodyRow(pm.Y)-top-listTop)
		if !ok || m.menu.Items()[i].Disabled {
			return m, nil, true
		}
		m.menu = m.menu.SetCursor(i)
		next, cmd = m.activate()
		return next, cmd, true
	case tea.MouseMotionMsg:
		m.menu, _ = m.menu.Hover(ctx, ctx.BodyRow(pm.Y)-top-listTop)
		return m, nil, true
	case tea.MouseWheelMsg:
		m.menu, cmd = m.menu.Update(pm)
		return m, cmd, true
	}
	return m, nil, false
}

// updateTyping routes keys to the text field.
func (m Model) updateTyping(km tea.KeyPressMsg) (Model, tea.Cmd) {
	switch km.Code {
	case tea.KeyEnter:
		return m.check(m.input.Value())
	case tea.KeyEscape:
		m.typing = false
		m.refusal = ""
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(km)
	m.refusal = ""
	return m, cmd
}

// activate acts on the highlighted row.
func (m Model) activate() (Model, tea.Cmd) {
	it, ok := m.menu.Selected()
	if !ok || it.Disabled {
		return m, nil
	}
	switch it.ID {
	case rowType:
		m.typing = true
		m.refusal = ""
		m.input.SetValue("")
		focus := m.input.Focus()
		return m, focus
	case rowBrowse:
		m.browsing = true
		m.refusal = ""
		return m, m.browseCmd()
	}
	for _, r := range m.rows {
		if r.Set.First == it.ID {
			return m.choose(r.Set)
		}
	}
	return m, nil
}

// browseCmd opens the platform file picker off the update loop.
func (m Model) browseCmd() tea.Cmd {
	fn := m.browseFn
	start := m.startDir
	return func() tea.Msg {
		path, err := fn(start)
		return BrowsedMsg{path: path, err: err}
	}
}

// check identifies a typed or browsed path off the update loop.
func (m Model) check(path string) (Model, tea.Cmd) {
	p := strings.Trim(strings.TrimSpace(path), `"`)
	if p == "" {
		m.refusal = Refusal(ErrEmptyPath)
		return m, nil
	}
	if m.identify == nil {
		m.refusal = Refusal(archive.ErrNotArchive)
		return m, nil
	}
	m.checking = true
	m.refusal = ""
	identify := m.identify
	return m, func() tea.Msg {
		set, err := identify(p)
		return CheckedMsg{set: set, err: err}
	}
}

// choose emits an archive.
func (m Model) choose(set archive.Set) (Model, tea.Cmd) {
	m.refusal = ""
	m.typing = false
	m.input.Blur()
	return m, func() tea.Msg { return ChosenMsg{Set: set} }
}

// Rows the picker draws around its list: the question and the blank line
// over it, the blank line and the two notes under it, and the blank line and
// the one line of a refusal or a wait.
const (
	rowsAbove = listTop
	rowsBelow = 3
	rowsNote  = 2
)

// sized is the menu given the rows the picker can spare it, so the notes
// under the list are never pushed off the screen.
func (m Model) sized(ctx uictx.Context) menu.Model {
	if ctx.BodyHeight <= 0 {
		return m.menu
	}
	return m.menu.SetHeight(max(3, ctx.BodyHeight-rowsAbove-rowsBelow-rowsNote))
}

// View renders the picker.
func (m Model) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	if m.typing {
		b.WriteString(th.Muted.Render("Type the path of the archive, then press Enter."))
		b.WriteString("\n\n")
		b.WriteString(themedInput(m.input, th).View())
	} else {
		b.WriteString(th.Muted.Render("Which archive do you want to extract?"))
		b.WriteString("\n\n")
		b.WriteString(m.sized(ctx).View(ctx))
	}

	b.WriteString("\n\n")
	if !m.typing {
		b.WriteString(th.Muted.Render(ctx.Truncate(m.listedLine())))
		b.WriteString("\n")
	}
	b.WriteString(th.Muted.Render(ctx.Truncate("Works with .zip, .7z, .rar and split parts (.001, .part1.rar, .r00).")))

	switch {
	case m.browsing:
		b.WriteString("\n\n")
		b.WriteString(th.Muted.Render("Waiting for the Windows file picker…"))
	case m.checking:
		b.WriteString("\n\n")
		b.WriteString(th.Muted.Render("Looking at that file…"))
	case m.refusal != "":
		b.WriteString("\n\n")
		b.WriteString(th.Danger.Render(ctx.Truncate(ctx.Icons.Fail + " " + m.refusal)))
	}
	return b.String()
}

// listedLine says where the rows came from.
func (m Model) listedLine() string {
	switch {
	case m.only:
		return "Listed: the big archives in the folder you just received."
	case m.loading:
		return "Looking for big archives in Downloads and your recent folders…"
	case len(m.rows) == 0:
		return "No archive of 1 GB or more was found in Downloads or your recent folders."
	default:
		return "Listed: archives of 1 GB or more in Downloads and your recent folders."
	}
}

// items builds the list: the archive rows, then the two actions.
func (m Model) items() []menu.Item {
	out := make([]menu.Item, 0, len(m.rows)+2)
	for _, r := range m.rows {
		out = append(out, menu.Item{ID: r.Set.First, Title: rowTitle(r), Desc: m.rowDesc(r)})
	}
	if m.only {
		return out
	}
	out = append(out, menu.Item{
		ID:    rowType,
		Title: "Type a path…",
		Desc:  "Enter an archive file by hand",
	})
	browseItem := menu.Item{
		ID:    rowBrowse,
		Title: "Browse…",
		Desc:  "Open the Windows file picker",
	}
	if !m.canBrowse {
		browseItem.Desc = "Only available on Windows"
		browseItem.Disabled = true
	}
	return append(out, browseItem)
}

// rowTitle is the archive's first volume, and how many more parts it has.
func rowTitle(r Row) string {
	if n := len(r.Set.Parts) - 1; n > 0 {
		return fmt.Sprintf("%s  (+%d part%s)", r.Set.First, n, plural(n))
	}
	return r.Set.First
}

// rowDesc is the muted line under an archive: its size and where it is, or
// that its extraction is unfinished and when that started.
func (m Model) rowDesc(r Row) string {
	size := FormatSize(r.Set.Bytes)
	if r.Unfinished {
		parts := []string{"Unfinished", size}
		if !r.Started.IsZero() {
			parts = append(parts, "started "+Ago(m.now().Sub(r.Started)))
		}
		return strings.Join(parts, " · ")
	}
	if dir := Base(Dir(r.Set.First)); dir != "" {
		return size + " · in " + dir
	}
	return size
}

// Refusal turns the reason a path was refused into the one line the picker
// shows. It never shows a raw error: an unknown one becomes a plain sentence
// about the file not being readable.
func Refusal(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrEmptyPath):
		return "Type the path of an archive first."
	case errors.Is(err, extract.ErrNetworkPath):
		return "Network paths are not extracted. Copy the archive to this PC first."
	case errors.Is(err, archive.ErrSplitZip):
		return "Split ZIP (.z01) is not supported by hatchzip. Join it with 7-Zip first."
	case errors.Is(err, archive.ErrNotArchive):
		return "That is not an archive Devpit can extract (.zip, .7z, .rar)."
	case errors.Is(err, extract.ErrReparsePoint):
		return "The archive is reached through a link. Pick the file where it really is."
	case errors.Is(err, extract.ErrProtected):
		return "Devpit does not extract archives in that place (system or sign-in files)."
	// Before ErrNotAFile: the engine's "nothing is there" and "the first
	// part is missing" are both also an ErrNotAFile, and neither is a folder.
	case errors.Is(err, fs.ErrNotExist):
		return "There is no file at that path."
	case errors.Is(err, extract.ErrFirstPartMissing):
		return "A part of that archive is missing. Put every part in the same folder."
	case errors.Is(err, extract.ErrNotAFile):
		return "That is a folder, not an archive file."
	case errors.Is(err, fs.ErrPermission):
		return "Windows would not let Devpit look at that file."
	case errors.Is(err, errBrowseTimeout):
		return "The Windows file picker did not respond. Type the path instead."
	case errors.Is(err, errBrowseFailed):
		return "The Windows file picker could not be opened. Type the path instead."
	default:
		return "Devpit could not read that file. Check the path and try again."
	}
}

// FormatSize writes a byte count the way hatchzip does, so a size Devpit
// measured reads like one hatchzip printed: base 1000 and one decimal
// ("84.2 GB", "401.4 KB", "1.0 MB"), whole bytes below 1000.
func FormatSize(n int64) string {
	if n < 0 {
		n = 0
	}
	if n < 1000 {
		return fmt.Sprintf("%d B", n)
	}
	units := [...]string{"KB", "MB", "GB", "TB", "PB"}
	val, i := float64(n)/1000, 0
	for val >= 999.95 && i < len(units)-1 {
		val /= 1000
		i++
	}
	return fmt.Sprintf("%.1f %s", val, units[i])
}

// Ago writes how long ago something happened, in whole units: "just now",
// "5 minutes ago", "3 hours ago", "2 days ago".
func Ago(d time.Duration) string {
	unit := func(n int, word string) string {
		return fmt.Sprintf("%d %s%s ago", n, word, plural(n))
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return unit(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		return unit(int(d.Hours()), "hour")
	default:
		return unit(int(d.Hours()/24), "day")
	}
}

// Base is the last element of a path, splitting on either slash, so a Windows
// path reads the same on every OS.
func Base(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Dir is a path without its last element, splitting on either slash.
func Dir(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[:i]
	}
	return ""
}

// plural is the "s" on a count.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// pathInputWidth is the text field's drawing width.
const pathInputWidth = 60

// newPathInput returns the typed-path text field.
func newPathInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = `D:\Downloads\backup.zip`
	ti.SetWidth(pathInputWidth)
	styles := ti.Styles()
	styles.Cursor.Blink = false // a static caret, like the row cursor.
	ti.SetStyles(styles)
	return ti
}

// themedInput returns a copy of ti styled from th, the same way pathpicker
// styles its field: an accent prompt, muted placeholder and plain text.
func themedInput(ti textinput.Model, th *theme.Theme) textinput.Model {
	state := textinput.StyleState{
		Text:        th.Base,
		Placeholder: th.Muted,
		Suggestion:  th.Muted,
		Prompt:      th.Accent,
	}
	styles := ti.Styles()
	styles.Focused = state
	styles.Blurred = state
	styles.Cursor.Color = th.Palette.Accent
	ti.SetStyles(styles)
	return ti
}
