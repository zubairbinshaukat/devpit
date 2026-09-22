package gitssh

import (
	"context"
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/gitssh"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// identityLoadedMsg carries the result of reading the global git identity.
type identityLoadedMsg struct {
	identity gitssh.Identity
	err      error
}

// identityModel is the "Show git identity" screen.
type identityModel struct {
	identityFn func(context.Context) (gitssh.Identity, error)

	loaded   bool
	identity gitssh.Identity
	err      error

	cont key.Binding
}

func newIdentityScreen() identityModel {
	return identityModel{
		identityFn: func(ctx context.Context) (gitssh.Identity, error) {
			return gitssh.GitIdentity(ctx, gitssh.ScopeGlobal, gitssh.Options{})
		},
		cont: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "back")),
	}
}

// Init implements uictx.Screen.
func (m identityModel) Init() tea.Cmd {
	fn := m.identityFn
	return func() tea.Msg {
		id, err := fn(context.Background())
		return identityLoadedMsg{identity: id, err: err}
	}
}

// Title implements uictx.Screen.
func (m identityModel) Title() string { return "Show git identity" }

// ShortHelp implements uictx.Screen.
func (m identityModel) ShortHelp() []key.Binding { return []key.Binding{m.cont} }

// FullHelp implements uictx.Screen.
func (m identityModel) FullHelp() [][]key.Binding { return [][]key.Binding{{m.cont}} }

// Update implements uictx.Screen.
func (m identityModel) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case identityLoadedMsg:
		m.loaded = true
		m.identity = msg.identity
		m.err = msg.err
		return m, nil
	case tea.KeyPressMsg:
		if key.Matches(msg, m.cont) {
			return m, uictx.Pop()
		}
	}
	return m, nil
}

// View implements uictx.Screen.
func (m identityModel) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	switch {
	case !m.loaded:
		b.WriteString(th.Muted.Render("Checking git identity…"))

	case m.err != nil:
		var notInstalled *gitssh.NotInstalledError
		if errors.As(m.err, &notInstalled) {
			b.WriteString(th.Danger.Render(ctx.Icons.Fail + " git is not installed, or not on PATH."))
			b.WriteString("\n")
			b.WriteString(th.Muted.Render("Install Git, then come back to this screen."))
		} else {
			b.WriteString(th.Danger.Render(ctx.Icons.Fail + " " + m.err.Error()))
		}

	default:
		name := m.identity.Name
		if name == "" {
			name = "(not set)"
		}
		email := m.identity.Email
		if email == "" {
			email = "(not set)"
		}
		b.WriteString(th.CardTitle.Render(ctx.Icons.Key + " Git identity"))
		b.WriteString("\n")
		b.WriteString(th.Base.Render("Name:  "))
		b.WriteString(th.Info.Render(name))
		b.WriteString("\n")
		b.WriteString(th.Base.Render("Email: "))
		b.WriteString(th.Info.Render(email))
	}

	return th.Card.Render(b.String())
}
