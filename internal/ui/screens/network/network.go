// Package network is the Network Tools screen: local and public IP lookup,
// a ping check and a DNS-cache flush, each pushed as its own screen so Esc
// (handled globally by the router) always lands back on this submenu.
//
// Every call into internal/network is reached through an injectable function
// field on the leaf screen that makes it, so unit tests never exec ping or
// ipconfig, or touch the real network.
package network

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Submenu item identifiers.
const (
	itemIP   = "ip"
	itemPing = "ping"
	itemDNS  = "dns"
)

// Model is the Network Tools submenu.
type Model struct {
	menu menu.Model
}

// New returns the Network Tools screen.
func New() Model {
	return Model{menu: menu.New(items())}
}

func items() []menu.Item {
	return []menu.Item{
		{ID: itemIP, Title: "My IP addresses", Desc: "Local interfaces and your public IP"},
		{ID: itemPing, Title: "Ping a host", Desc: "Check connectivity, default 1.1.1.1"},
		{ID: itemDNS, Title: "Flush DNS cache", Desc: "Clear the resolver cache"},
	}
}

// Init implements uictx.Screen.
func (m Model) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (m Model) Title() string { return "Network Tools" }

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	return []key.Binding{m.menu.Keys.Up, m.menu.Keys.Select}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.menu.Keys.Up, m.menu.Keys.Down, m.menu.Keys.Select}}
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	if sel, ok := msg.(menu.SelectedMsg); ok {
		return m, open(sel.ID)
	}
	next, cmd := m.menu.Update(msg)
	m.menu = next
	return m, cmd
}

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	var b strings.Builder
	b.WriteString(ctx.Theme.Muted.Render("IP, connectivity and DNS helpers."))
	b.WriteString("\n\n")
	b.WriteString(m.menu.View(ctx))
	return b.String()
}

// open maps a submenu selection to the screen it pushes.
func open(id string) tea.Cmd {
	switch id {
	case itemIP:
		return uictx.Push(newIPScreen())
	case itemPing:
		return uictx.Push(newPingScreen())
	case itemDNS:
		return uictx.Push(newDNSScreen())
	default:
		return nil
	}
}
