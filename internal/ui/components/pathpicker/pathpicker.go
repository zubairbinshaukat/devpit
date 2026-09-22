// Package pathpicker asks the user which folder to scan.
//
// It offers four ways to answer, in the order a returning user wants them:
// the folders scanned recently, the configured default projects folder, a
// typed path, and the Windows folder browser. Whatever the answer, the path
// is validated before it leaves the component: it must exist, it must be a
// directory, and it must not be a UNC or mapped network path, because Devpit
// does not scan the network.
//
// The browser is the only part of this that is platform-specific, and it
// lives behind [browseSupported] and [browse] in the _windows.go and
// _other.go files so `GOOS=linux go vet ./...` stays clean.
package pathpicker

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Row identifiers for the two rows that are not a folder.
const (
	rowType   = "\x00type"
	rowBrowse = "\x00browse"
)

// ChosenMsg is emitted when the user settles on a folder. The path in it has
// already been validated.
type ChosenMsg struct {
	// Path is the chosen folder.
	Path string
}

// CancelledMsg is emitted when the user backs out of the picker.
type CancelledMsg struct{}

// BrowsedMsg carries the result of the platform folder browser. It is
// exported, unlike the picker's other internal messages, because the
// browseCmd's tea.Cmd returns it to the top of the program's Update loop and
// the owning screen must route it back into [Model.Update] itself — Bubble
// Tea has no other way to get an async Cmd's result to a child model. A
// screen that shows the picker needs a case for this type in its own Update,
// forwarding it to the picker while the picker is the active view, or the
// browser's result (including cancellation and errors) is silently dropped
// and the picker is stuck showing "Waiting for the folder browser…" forever.
type BrowsedMsg struct {
	path string
	err  error
}

// Errors the validator returns. They are worded for the user, because the
// user is who reads them.
var (
	// ErrEmptyPath means nothing was typed.
	ErrEmptyPath = errors.New("type a folder path first")
	// ErrNoSuchFolder means the path is not there.
	ErrNoSuchFolder = errors.New("there is no folder at that path")
	// ErrNotADirectory means the path is a file.
	ErrNotADirectory = errors.New("that is a file, not a folder")
	// ErrNetworkPath means the path is UNC. Devpit never scans the network.
	ErrNetworkPath = errors.New("network paths are not scanned")
)

// KeyMap is the picker's key bindings.
type KeyMap struct {
	Select key.Binding
	Back   key.Binding
}

// DefaultKeyMap returns the standard bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Select: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose")),
		Back:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// ShortHelp returns the bindings for the footer.
func (k KeyMap) ShortHelp() []key.Binding { return []key.Binding{k.Select, k.Back} }

// FullHelp returns the bindings grouped for the help overlay.
func (k KeyMap) FullHelp() [][]key.Binding { return [][]key.Binding{{k.Select, k.Back}} }

// Model is the folder picker.
type Model struct {
	// Keys are the bindings the picker answers to.
	Keys KeyMap

	menu   menu.Model
	input  textinput.Model
	typing bool
	err    error
	busy   bool

	// stat and browseFn are the two things that touch the outside world.
	// They are fields so tests can supply their own and never walk a real
	// disk or open a real dialog.
	stat     func(string) (os.FileInfo, error)
	browseFn func(start string) (string, error)
}

// New returns a picker over the folders in cfg.
func New(cfg config.Config) Model {
	return Model{
		Keys:     DefaultKeyMap(),
		menu:     menu.New(rows(cfg)),
		input:    newPathInput(),
		stat:     os.Stat,
		browseFn: browse,
	}
}

// WithStat replaces the filesystem check, for tests.
func (m Model) WithStat(stat func(string) (os.FileInfo, error)) Model {
	if stat != nil {
		m.stat = stat
	}
	return m
}

// WithBrowse replaces the platform folder browser, for tests.
func (m Model) WithBrowse(fn func(start string) (string, error)) Model {
	if fn != nil {
		m.browseFn = fn
	}
	return m
}

// SetItems rebuilds the list from a configuration, so a picker shown again
// after a scan lists the folder that scan used.
func (m Model) SetItems(cfg config.Config) Model {
	m.menu = m.menu.SetItems(rows(cfg))
	return m
}

// Typing reports whether the text input currently has the keyboard.
func (m Model) Typing() bool { return m.typing }

// Err returns the last validation failure, or nil.
func (m Model) Err() error { return m.err }

// Update handles the picker's keys and the browser's result.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case BrowsedMsg:
		m.busy = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		if msg.path == "" { // the user cancelled the dialog
			return m, nil
		}
		return m.choose(msg.path)

	case tea.KeyPressMsg:
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

// updateTyping routes keys to the text input.
func (m Model) updateTyping(km tea.KeyPressMsg) (Model, tea.Cmd) {
	switch km.Code {
	case tea.KeyEnter:
		return m.choose(strings.TrimSpace(m.input.Value()))
	case tea.KeyEscape:
		m.typing = false
		m.err = nil
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(km)
	m.err = nil
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
		m.err = nil
		m.input.SetValue("")
		return m, m.input.Focus()
	case rowBrowse:
		if m.busy {
			return m, nil
		}
		m.busy = true
		m.err = nil
		return m, m.browseCmd()
	default:
		return m.choose(it.ID)
	}
}

// browseCmd opens the platform folder browser off the update loop.
func (m Model) browseCmd() tea.Cmd {
	fn := m.browseFn
	start := ""
	if it, ok := m.menu.Selected(); ok && it.ID != rowBrowse {
		start = it.ID
	}
	return func() tea.Msg {
		path, err := fn(start)
		return BrowsedMsg{path: path, err: err}
	}
}

// choose validates a path and emits it, or records why it was refused.
func (m Model) choose(path string) (Model, tea.Cmd) {
	if err := m.validate(path); err != nil {
		m.err = err
		return m, nil
	}
	m.err = nil
	m.typing = false
	m.input.Blur()
	chosen := path
	return m, func() tea.Msg { return ChosenMsg{Path: chosen} }
}

// validate applies the three rules: something was typed, it is a folder, and
// it is not on the network.
func (m Model) validate(path string) error {
	p := strings.TrimSpace(path)
	if p == "" {
		return ErrEmptyPath
	}
	if IsUNC(p) {
		return ErrNetworkPath
	}
	info, err := m.stat(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ErrNoSuchFolder
	case err != nil:
		return fmt.Errorf("%s could not be read: %w", p, err)
	case !info.IsDir():
		return ErrNotADirectory
	}
	return nil
}

// View renders the picker.
func (m Model) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	if m.typing {
		b.WriteString(th.Muted.Render("Type the folder to scan, then press Enter. Esc goes back to the list."))
		b.WriteString("\n\n")
		b.WriteString(themedInput(m.input, th).View())
	} else {
		b.WriteString(th.Muted.Render("Which folder holds your projects?"))
		b.WriteString("\n\n")
		b.WriteString(m.menu.View(ctx))
	}

	if m.busy {
		b.WriteString("\n\n")
		b.WriteString(th.Muted.Render("Waiting for the folder browser…"))
	}
	if m.err != nil {
		b.WriteString("\n\n")
		b.WriteString(th.Danger.Render(ctx.Icons.Fail + " " + m.err.Error()))
	}
	return b.String()
}

// rows builds the list: recent folders first, then the configured default,
// then the two actions.
func rows(cfg config.Config) []menu.Item {
	seen := map[string]bool{}
	out := make([]menu.Item, 0, len(cfg.RecentFolders)+3)

	add := func(path, desc string) {
		if path == "" {
			return
		}
		k := strings.ToLower(path)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, menu.Item{ID: path, Title: path, Desc: desc})
	}

	add(cfg.DefaultProjectsFolder, "Your default projects folder")
	for _, p := range cfg.RecentFolders {
		add(p, "Scanned recently")
	}

	out = append(out, menu.Item{
		ID:    rowType,
		Title: "Type a path…",
		Desc:  "Enter a folder by hand",
	})
	browseItem := menu.Item{
		ID:    rowBrowse,
		Title: "Browse…",
		Desc:  browseDesc,
	}
	if !browseSupported {
		browseItem.Desc = "Only available on Windows"
		browseItem.Disabled = true
	}
	out = append(out, browseItem)
	return out
}

// IsUNC reports whether a path is a UNC path. Devpit refuses to scan the
// network, and this is the check that makes that true whichever way the path
// arrived: typed, picked from the browser, or remembered from a config file.
func IsUNC(path string) bool {
	p := strings.TrimSpace(path)
	if len(p) < 2 {
		return false
	}
	return (p[0] == '\\' || p[0] == '/') && (p[1] == '\\' || p[1] == '/')
}
