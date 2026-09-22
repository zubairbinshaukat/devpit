package gitssh

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/gitssh"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// confirmOverwriteKeyID identifies the overwrite confirmation dialog.
const confirmOverwriteKeyID = "overwrite-ssh-key"

// overwriteWord is the word docs/safety.md rule 15 requires the user to type
// before an existing key can be replaced.
const overwriteWord = "OVERWRITE"

// keygenAlgorithm is the only algorithm this screen offers, matching the
// PRD's "Generate an SSH key" action.
const keygenAlgorithm = "ed25519"

// keygenState is where the screen is in its load → (confirm) → generating →
// done flow.
type keygenState int

const (
	keygenStateLoading keygenState = iota
	keygenStateExists
	keygenStateGenerating
	keygenStateDone
	keygenStateError
)

// keygenLoadedMsg carries the default comment (the git email, best-effort)
// and whether a key already sits at the target path.
type keygenLoadedMsg struct {
	email  string
	exists bool
}

// keygenResultMsg carries the outcome of a generation attempt.
type keygenResultMsg struct {
	result gitssh.KeygenResult
	err    error
}

// copyResultMsg reports whether the public key was copied, and by which
// route.
type copyResultMsg struct {
	viaWin32 bool
}

// keygenModel is the "Generate SSH key" screen.
//
// Safety rule 15 (docs/safety.md): keygenFn is never called with Force set
// to true except from the branch that runs after a [confirm.Model] with
// [confirm.Model.WithTypedWord] has been satisfied with exactly
// [overwriteWord]; answering No never calls it at all. TestExistingKeyRequiresTypedOverwrite
// pins this.
type keygenModel struct {
	emailFn   func(context.Context) (string, error)
	existsFn  func(string) bool
	keygenFn  func(context.Context, gitssh.KeygenOptions, gitssh.Options) (gitssh.KeygenResult, error)
	copyWin32 func(string) error

	path    string
	comment string

	state   keygenState
	confirm confirm.Model
	result  gitssh.KeygenResult
	err     error
	view    viewport.Model
	copied  bool

	cont key.Binding
	copy key.Binding
}

func newKeygenScreen() keygenModel {
	path := gitssh.DefaultKeyPath(keygenAlgorithm)
	vp := viewport.New(viewport.WithWidth(70), viewport.WithHeight(4))

	return keygenModel{
		emailFn: func(ctx context.Context) (string, error) {
			return gitssh.GitConfig(ctx, "user.email", gitssh.ScopeGlobal, gitssh.Options{})
		},
		existsFn:  gitssh.KeyExists,
		keygenFn:  gitssh.Keygen,
		copyWin32: gitssh.CopyWin32,
		path:      path,
		state:     keygenStateLoading,
		view:      vp,
		cont:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "back")),
		copy:      key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy")),
	}
}

// Init implements uictx.Screen.
func (m keygenModel) Init() tea.Cmd {
	emailFn := m.emailFn
	existsFn := m.existsFn
	path := m.path
	return func() tea.Msg {
		email, _ := emailFn(context.Background()) // best-effort; an empty comment is fine.
		return keygenLoadedMsg{email: email, exists: existsFn(path)}
	}
}

// Title implements uictx.Screen.
func (m keygenModel) Title() string { return "Generate SSH key" }

// ShortHelp implements uictx.Screen.
func (m keygenModel) ShortHelp() []key.Binding {
	switch m.state {
	case keygenStateExists:
		return m.confirm.Keys.ShortHelp()
	case keygenStateDone:
		return []key.Binding{m.copy, m.cont}
	case keygenStateError:
		return []key.Binding{m.cont}
	default:
		return nil
	}
}

// FullHelp implements uictx.Screen.
func (m keygenModel) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

// Update implements uictx.Screen.
func (m keygenModel) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case keygenLoadedMsg:
		m.comment = msg.email
		if msg.exists {
			m.state = keygenStateExists
			question := "A key already exists at " + m.path + " — overwrite it?"
			m.confirm = confirm.New(confirmOverwriteKeyID, question,
				"The old key stops working anywhere it was added (GitHub, servers, ...).").
				WithTypedWord(overwriteWord)
			return m, nil
		}
		m.state = keygenStateGenerating
		return m, m.generateCmd(false)

	case confirm.AnsweredMsg:
		if msg.ID != confirmOverwriteKeyID {
			return m, nil
		}
		if msg.Answer == confirm.AnswerYes {
			// Only reachable once the typed word matched: confirm.Model
			// never reports Yes for a WithTypedWord dialog otherwise.
			m.state = keygenStateGenerating
			return m, m.generateCmd(true)
		}
		return m, uictx.Pop()

	case keygenResultMsg:
		if msg.err != nil {
			m.state = keygenStateError
			m.err = msg.err
			return m, nil
		}
		m.state = keygenStateDone
		m.result = msg.result
		m.view.SetContent(msg.result.PublicKey)
		return m, nil

	case copyResultMsg:
		m.copied = true
		if msg.viaWin32 {
			return m, uictx.Status("success", "Copied to clipboard")
		}
		return m, tea.Batch(tea.SetClipboard(m.result.PublicKey), uictx.Status("success", "Copied to clipboard"))

	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m keygenModel) updateKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd) {
	switch m.state {
	case keygenStateExists:
		var cmd tea.Cmd
		m.confirm, cmd = m.confirm.Update(msg)
		return m, cmd

	case keygenStateDone:
		switch {
		case key.Matches(msg, m.copy):
			return m, m.copyCmd()
		case key.Matches(msg, m.cont):
			return m, uictx.Pop()
		}
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd

	case keygenStateError:
		if key.Matches(msg, m.cont) {
			return m, uictx.Pop()
		}
	}
	return m, nil
}

// generateCmd calls keygenFn. force must only ever be true when it is set by
// the confirm.AnswerYes branch above, which only fires once the typed
// overwrite word has been matched.
func (m keygenModel) generateCmd(force bool) tea.Cmd {
	fn := m.keygenFn
	opts := gitssh.KeygenOptions{
		Path:      m.path,
		Comment:   m.comment,
		Algorithm: keygenAlgorithm,
		Force:     force,
	}
	return func() tea.Msg {
		result, err := fn(context.Background(), opts, gitssh.Options{})
		return keygenResultMsg{result: result, err: err}
	}
}

// copyCmd tries the native Windows clipboard first and falls back to asking
// the terminal itself to set the clipboard via OSC 52.
func (m keygenModel) copyCmd() tea.Cmd {
	win32 := m.copyWin32
	return func() tea.Msg {
		return copyResultMsg{viaWin32: win32(m.result.PublicKey) == nil}
	}
}

// View implements uictx.Screen.
func (m keygenModel) View(ctx uictx.Context) string {
	th := ctx.Theme

	switch m.state {
	case keygenStateLoading:
		return th.Card.Render(th.Muted.Render("Checking for an existing key…"))

	case keygenStateExists:
		return m.confirm.View(ctx)

	case keygenStateGenerating:
		return th.Card.Render(th.Muted.Render(ctx.Icons.Key + " Generating…"))

	case keygenStateError:
		return th.Card.Render(th.Danger.Render(ctx.Icons.Fail + " " + m.err.Error()))

	default: // keygenStateDone
		var b strings.Builder
		b.WriteString(th.Success.Render(ctx.Icons.Tick))
		b.WriteString(" ")
		b.WriteString(th.CardTitle.Render(ctx.Icons.Key + " Key generated"))
		b.WriteString("\n")
		b.WriteString(th.Muted.Render(m.result.Path))
		b.WriteString("\n\n")
		b.WriteString(m.view.View())
		if m.copied {
			b.WriteString("\n\n")
			b.WriteString(th.Success.Render(ctx.Icons.Tick + " Copied to clipboard."))
		}
		return th.Card.Render(b.String())
	}
}
