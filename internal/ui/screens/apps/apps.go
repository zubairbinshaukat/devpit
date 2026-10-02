// Package apps is the Install & Update section: a two-entry menu over the
// screens that used to be their own home entries, "Install developer apps"
// (the install screen) and "Update everything" (the update screen).
//
// It opens them exactly as home used to, one push each, so Esc (handled by
// the router) lands back on this menu and the screens themselves are
// unchanged. Nothing is detected here: the menu runs no package manager and
// always opens. When a machine has no package manager, the screen behind
// each entry says so itself, in its own words, once it is open.
package apps

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/install"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/update"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Item identifiers. They are the theme's section names for the screens they
// open, so a factory map keyed by home's Section* identifiers reaches the
// screen behind each entry unchanged.
const (
	ItemInstall = theme.SectionInstall
	ItemUpdate  = theme.SectionUpdate
)

// Model is the Install & Update menu.
type Model struct {
	menu menu.Model
	jump key.Binding
	// factories replaces what an entry opens, keyed by the Item identifiers;
	// nil keeps the real screens.
	factories map[string]func() uictx.Screen
	// opened is the entry last opened, so the breadcrumb can name it.
	opened string
}

// New returns the Install & Update screen.
func New() Model {
	return Model{
		menu: menu.New(Items()),
		jump: key.NewBinding(key.WithKeys("1", "2"), key.WithHelp("1-2", "open")),
	}
}

// Items returns the two entries, in the order the menu draws them.
func Items() []menu.Item {
	return []menu.Item{
		{ID: ItemInstall, Title: "Install developer apps", Desc: "Pick apps from a list; Devpit installs them for you"},
		{ID: ItemUpdate, Title: "Update everything", Desc: "See what is out of date, then pick what to update"},
	}
}

// crumb is how the breadcrumb names an entry once it is open, or "".
func crumb(id string) string {
	switch id {
	case ItemInstall:
		return "Install developer apps"
	case ItemUpdate:
		return "Update everything"
	default:
		return ""
	}
}

// WithFactories overrides what an entry opens, keyed by the Item
// identifiers. Anything the map does not name keeps its real constructor.
// It is how tests and the screenshot renderer open a screen wired to fakes,
// so no test ever execs a package manager.
func (m Model) WithFactories(f map[string]func() uictx.Screen) Model {
	m.factories = f
	return m
}

// Init implements uictx.Screen.
func (m Model) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (m Model) Title() string { return "Install & Update" }

// Breadcrumb is the header title while a screen this menu opened is on top:
// "Install & Update › Update everything". direct says the top screen is the
// one this menu pushed; its own name is swapped for this menu's word for it,
// and anything it adds after that (the app in flight) is kept. A screen
// further down the chain keeps its own title after the section's.
func (m Model) Breadcrumb(top string, direct bool) string {
	if name := crumb(m.opened); name != "" && direct {
		rest := ""
		if i := strings.Index(top, " › "); i >= 0 {
			rest = top[i:]
		}
		return m.Title() + " › " + name + rest
	}
	return m.Title() + " › " + top
}

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	return []key.Binding{m.menu.Keys.Up, m.menu.Keys.Select}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.menu.Keys.Up, m.menu.Keys.Down, m.menu.Keys.Select, m.jump}}
}

// menuTop is the body row the menu starts on: under the one-line lead-in
// and the blank line after it.
const menuTop = 2

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case menu.SelectedMsg:
		return m.open(msg.ID)
	case tea.KeyPressMsg:
		// 1 and 2 open an entry directly, the way digits open a section on
		// home. This menu has no text input, so a digit is never typing.
		if key.Matches(msg, m.jump) {
			i := int(msg.String()[0] - '1')
			m.menu = m.menu.SetCursor(i)
			return m.open(m.menu.Items()[i].ID)
		}
	}
	if next, cmd, ok := m.menu.Pointer(ctx, msg, menuTop); ok {
		m.menu = next
		return m, cmd
	}
	next, cmd := m.menu.Update(msg)
	m.menu = next
	return m, cmd
}

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	var b strings.Builder
	b.WriteString(ctx.Theme.Muted.Render("Install dev apps, update everything."))
	b.WriteString("\n\n")
	b.WriteString(m.menu.View(ctx))
	return b.String()
}

// open pushes the screen behind an entry and remembers which one it was.
func (m Model) open(id string) (Model, tea.Cmd) {
	var s uictx.Screen
	if f, ok := m.factories[id]; ok && f != nil {
		s = f()
	} else {
		switch id {
		case ItemInstall:
			s = install.New()
		case ItemUpdate:
			s = update.New()
		}
	}
	if s == nil {
		return m, nil
	}
	m.opened = id
	return m, uictx.Push(s)
}
