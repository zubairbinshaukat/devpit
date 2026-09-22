package gitssh

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/gitssh"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// confirmSetIdentityID identifies the set-identity confirmation dialog.
const confirmSetIdentityID = "set-identity"

// setIdentityState is where the screen is in its input → confirm → saving →
// done flow.
type setIdentityState int

const (
	setStateInput setIdentityState = iota
	setStateConfirm
	setStateSaving
	setStateDone
)

// setIdentityLoadedMsg carries the current identity, used only to prefill
// the two fields; a failure to load one just leaves them blank.
type setIdentityLoadedMsg struct {
	identity gitssh.Identity
}

// setIdentitySavedMsg carries the outcome of writing both config keys.
type setIdentitySavedMsg struct{ err error }

// setIdentityModel is the "Set git identity" screen. Safety rule 2 (default
// No): SetGitConfig is only ever called from the confirm.AnswerYes branch of
// [confirm.Model], whose zero value is always No.
type setIdentityModel struct {
	loadFn func(context.Context) (gitssh.Identity, error)
	setFn  func(ctx context.Context, key, value string) error

	name      textinput.Model
	email     textinput.Model
	focusName bool

	state   setIdentityState
	confirm confirm.Model
	err     error

	tab    key.Binding
	submit key.Binding
	cont   key.Binding
}

func newSetIdentityScreen() setIdentityModel {
	name := textinput.New()
	name.Placeholder = "Jane Doe"
	name.Focus()

	email := textinput.New()
	email.Placeholder = "jane@example.com"

	return setIdentityModel{
		loadFn: func(ctx context.Context) (gitssh.Identity, error) {
			return gitssh.GitIdentity(ctx, gitssh.ScopeGlobal, gitssh.Options{})
		},
		setFn: func(ctx context.Context, key, value string) error {
			return gitssh.SetGitConfig(ctx, key, value, gitssh.ScopeGlobal, gitssh.Options{})
		},
		name:      name,
		email:     email,
		focusName: true,
		state:     setStateInput,
		tab:       key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch field")),
		submit:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "review")),
		cont:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "back")),
	}
}

// Init implements uictx.Screen.
func (m setIdentityModel) Init() tea.Cmd {
	fn := m.loadFn
	return func() tea.Msg {
		id, err := fn(context.Background())
		if err != nil {
			return setIdentityLoadedMsg{}
		}
		return setIdentityLoadedMsg{identity: id}
	}
}

// Title implements uictx.Screen.
func (m setIdentityModel) Title() string { return "Set git identity" }

// ShortHelp implements uictx.Screen.
func (m setIdentityModel) ShortHelp() []key.Binding {
	switch m.state {
	case setStateInput:
		return []key.Binding{m.tab, m.submit}
	case setStateConfirm:
		return m.confirm.Keys.ShortHelp()
	case setStateDone:
		return []key.Binding{m.cont}
	default:
		return nil
	}
}

// FullHelp implements uictx.Screen.
func (m setIdentityModel) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

// Update implements uictx.Screen.
func (m setIdentityModel) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case setIdentityLoadedMsg:
		if m.name.Value() == "" {
			m.name.SetValue(msg.identity.Name)
		}
		if m.email.Value() == "" {
			m.email.SetValue(msg.identity.Email)
		}
		return m, nil

	case confirm.AnsweredMsg:
		if msg.ID != confirmSetIdentityID {
			return m, nil
		}
		if msg.Answer == confirm.AnswerYes {
			m.state = setStateSaving
			return m, m.saveCmd()
		}
		// Safety rule 2: the default is No. Go back to the fields rather
		// than saving anything, keeping what the user already typed.
		m.state = setStateInput
		return m, nil

	case setIdentitySavedMsg:
		m.state = setStateDone
		m.err = msg.err
		if msg.err != nil {
			return m, uictx.Status("danger", "Could not save git identity: "+msg.err.Error())
		}
		return m, uictx.Status("success", "Git identity saved")

	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m setIdentityModel) updateKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd) {
	switch m.state {
	case setStateInput:
		switch {
		case key.Matches(msg, m.submit):
			m.state = setStateConfirm
			question := "Set git identity to " + m.name.Value() + " <" + m.email.Value() + ">?"
			m.confirm = confirm.New(confirmSetIdentityID, question, "Applies globally, to every repository (~/.gitconfig).")
			return m, nil
		case key.Matches(msg, m.tab):
			m.focusName = !m.focusName
			if m.focusName {
				m.name.Focus()
				m.email.Blur()
			} else {
				m.email.Focus()
				m.name.Blur()
			}
			return m, nil
		}
		var cmd tea.Cmd
		if m.focusName {
			m.name, cmd = m.name.Update(msg)
		} else {
			m.email, cmd = m.email.Update(msg)
		}
		return m, cmd

	case setStateConfirm:
		var cmd tea.Cmd
		m.confirm, cmd = m.confirm.Update(msg)
		return m, cmd

	case setStateDone:
		if key.Matches(msg, m.cont) {
			return m, uictx.Pop()
		}
	}
	return m, nil
}

func (m setIdentityModel) saveCmd() tea.Cmd {
	fn := m.setFn
	name := m.name.Value()
	email := m.email.Value()
	return func() tea.Msg {
		if err := fn(context.Background(), "user.name", name); err != nil {
			return setIdentitySavedMsg{err: err}
		}
		if err := fn(context.Background(), "user.email", email); err != nil {
			return setIdentitySavedMsg{err: err}
		}
		return setIdentitySavedMsg{}
	}
}

// View implements uictx.Screen.
func (m setIdentityModel) View(ctx uictx.Context) string {
	th := ctx.Theme

	switch m.state {
	case setStateConfirm:
		return m.confirm.View(ctx)

	case setStateSaving:
		return th.Card.Render(th.Muted.Render("Saving…"))

	case setStateDone:
		if m.err != nil {
			return th.Card.Render(th.Danger.Render(ctx.Icons.Fail + " " + m.err.Error()))
		}
		return th.Card.Render(th.Success.Render(ctx.Icons.Tick + " Git identity saved."))

	default:
		var b strings.Builder
		b.WriteString(th.CardTitle.Render("Set git identity"))
		b.WriteString("\n\n")
		b.WriteString(fieldLabel(th, "Name", m.focusName))
		b.WriteString("\n")
		b.WriteString(m.name.View())
		b.WriteString("\n\n")
		b.WriteString(fieldLabel(th, "Email", !m.focusName))
		b.WriteString("\n")
		b.WriteString(m.email.View())
		return th.Card.Render(b.String())
	}
}

// fieldLabel renders a field's caption, accented while its input is focused.
func fieldLabel(th *theme.Theme, label string, focused bool) string {
	if focused {
		return th.Accent.Render(label + ":")
	}
	return th.Muted.Render(label + ":")
}
