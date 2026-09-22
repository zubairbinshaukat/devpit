// Package gitssh is the Git & SSH Setup screen: show the current git
// identity, set it, and generate an SSH key, each pushed as its own screen
// so Esc (handled globally by the router) always lands back on this
// submenu.
//
// Every call into internal/gitssh is reached through an injectable function
// field on the leaf screen that makes it, so unit tests never exec git or
// ssh-keygen, or touch the real ~/.ssh. Safety rule 15 (docs/safety.md): the
// key-generation screen never calls Keygen with Force set unless the user
// has typed the overwrite word into a [confirm.Model]; see keygen.go.
package gitssh

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Submenu item identifiers.
const (
	itemShow   = "show"
	itemSet    = "set"
	itemKeygen = "keygen"
)

// Model is the Git & SSH Setup submenu.
type Model struct {
	menu menu.Model
}

// New returns the Git & SSH Setup screen.
func New() Model {
	return Model{menu: menu.New(items())}
}

func items() []menu.Item {
	return []menu.Item{
		{ID: itemShow, Title: "Show git identity", Desc: "See the name and email git will commit as"},
		{ID: itemSet, Title: "Set git identity", Desc: "Set your global git name and email"},
		{ID: itemKeygen, Title: "Generate SSH key", Desc: "Create an ed25519 key and copy the public half"},
	}
}

// Init implements uictx.Screen.
func (m Model) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (m Model) Title() string { return "Git & SSH Setup" }

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
	// TrimSpace keeps the tiers that have no key glyph from opening the
	// screen with a stray leading space.
	b.WriteString(ctx.Theme.Muted.Render(strings.TrimSpace(ctx.Icons.Key + " Get a fresh machine ready to push code.")))
	b.WriteString("\n\n")
	b.WriteString(m.menu.View(ctx))
	return b.String()
}

// open maps a submenu selection to the screen it pushes.
func open(id string) tea.Cmd {
	switch id {
	case itemShow:
		return uictx.Push(newIdentityScreen())
	case itemSet:
		return uictx.Push(newSetIdentityScreen())
	case itemKeygen:
		return uictx.Push(newKeygenScreen())
	default:
		return nil
	}
}
