package share

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Menu item identifiers.
const (
	itemHost    = "host"
	itemRecv    = "recv"
	itemResume  = "resume"
	itemCleanup = "cleanup"
)

// stateMsg carries what the menu learns at start: a leftover share and a
// copy that can be resumed. Both come from small files, read in a command so
// the first frame costs nothing.
type stateMsg struct {
	left    host.Manifest
	hasLeft bool
	saved   job.Job
	hasJob  bool
}

// Model is the Share Files menu.
type Model struct {
	deps Deps
	menu menu.Model

	left    host.Manifest
	hasLeft bool
	saved   job.Job
	hasJob  bool
}

// logDir is where robocopy's logs go: Devpit's cache directory.
func logDir() string {
	if d, err := config.CacheDir(); err == nil {
		return filepath.Join(d, "share-logs")
	}
	return filepath.Join(os.TempDir(), "devpit-share-logs")
}

// RealDeps returns the dependencies of a real run.
func RealDeps() Deps {
	dir := host.StateDir()
	return Deps{
		NewHost: func() Hoster { return host.NewManager(host.Deps{Dir: dir}) },
		NewRecv: func() Receiver { return recv.New(recv.Real(dir, logDir())) },
		Leftover: func() (host.Manifest, bool) {
			m, ok, err := host.Leftover(dir, host.ProcessAlive, os.Getpid())
			return m, ok && err == nil
		},
		CleanUp: func(ctx context.Context, m host.Manifest) error { return host.CleanUp(ctx, dir, m, host.RealLauncher) },
		SavedJob: func() (job.Job, bool) {
			j, ok, err := job.Load(dir)
			return j, ok && err == nil && j.Resumable()
		},
		Copy: copyToClipboard,
	}
}

// New returns the Share Files screen over the real machine.
func New() Model { return NewWith(RealDeps()) }

// NewWith returns the screen over the given dependencies.
func NewWith(deps Deps) Model {
	m := Model{deps: deps}
	m.menu = menu.New(m.items())
	return m
}

// items builds the menu from what is known.
func (m Model) items() []menu.Item {
	out := []menu.Item{
		{ID: itemHost, Title: "Share a folder", Desc: "Let another PC on your network copy a folder from this one"},
		{ID: itemRecv, Title: "Copy from a shared folder", Desc: "Copy a folder from another PC on your network to this one"},
	}
	if m.hasJob {
		j := m.saved
		out = append([]menu.Item{{
			ID:    itemResume,
			Title: "Resume the copy from " + j.Host,
			Desc: fmt.Sprintf("%s of %s copied. Carry on where it stopped.",
				recv.FormatBytes(j.DoneBytes), recv.FormatBytes(j.TotalBytes)),
		}}, out...)
	}
	if m.hasLeft {
		out = append(out, menu.Item{
			ID:    itemCleanup,
			Title: "Clean up an old share",
			Desc:  "A share from an earlier run was not removed. Remove it now.",
		})
	}
	return out
}

// Init implements uictx.Screen: look for leftovers and a resumable copy.
func (m Model) Init() tea.Cmd {
	deps := m.deps
	return func() tea.Msg {
		var s stateMsg
		if deps.Leftover != nil {
			s.left, s.hasLeft = deps.Leftover()
		}
		if deps.SavedJob != nil {
			s.saved, s.hasJob = deps.SavedJob()
		}
		return s
	}
}

// Title implements uictx.Screen.
func (m Model) Title() string { return "Share Files" }

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	return []key.Binding{m.menu.Keys.Up, m.menu.Keys.Select}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.menu.Keys.Up, m.menu.Keys.Down, m.menu.Keys.Select}}
}

// menuTop is the body row the menu starts on: under the two-line lead-in and
// the blank line after it.
const menuTop = 3

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case stateMsg:
		m.left, m.hasLeft, m.saved, m.hasJob = msg.left, msg.hasLeft, msg.saved, msg.hasJob
		m.menu = m.menu.SetItems(m.items())
		return m, nil
	case menu.SelectedMsg:
		return m, m.open(msg.ID, ctx.Config)
	}
	if next, cmd, ok := m.menu.Pointer(ctx, msg, menuTop); ok {
		m.menu = next
		return m, cmd
	}
	next, cmd := m.menu.Update(msg)
	m.menu = next
	return m, cmd
}

// open maps a choice to the screen it pushes.
func (m Model) open(id string, cfg config.Config) tea.Cmd {
	switch id {
	case itemHost:
		return uictx.Push(newHostScreen(m.deps, cfg))
	case itemRecv:
		return uictx.Push(newRecvScreen(m.deps, cfg, nil))
	case itemResume:
		j := m.saved
		return uictx.Push(newRecvScreen(m.deps, cfg, &j))
	case itemCleanup:
		return uictx.Push(newCleanupScreen(m.deps, m.left))
	default:
		return nil
	}
}

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	var b strings.Builder
	b.WriteString(ctx.Theme.Muted.Render("Move big folders between two PCs on the same Wi-Fi or network."))
	b.WriteString("\n")
	b.WriteString(ctx.Theme.Muted.Render("Both PCs stay on your own network. Nothing goes to the internet."))
	b.WriteString("\n\n")
	b.WriteString(m.menu.View(ctx))
	return b.String()
}
