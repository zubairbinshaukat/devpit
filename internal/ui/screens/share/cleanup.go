package share

import (
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// cleanupStage is where the cleanup screen is.
type cleanupStage int

// The stages.
const (
	cleanupAsk cleanupStage = iota
	cleanupRunning
	cleanupDone
	cleanupFailed
)

// cleanupID names the screen's one dialog.
const cleanupID = "share-cleanup"

// cleanupDoneMsg carries the result of the cleanup.
type cleanupDoneMsg struct{ err error }

// cleanupScreen offers to remove a share an earlier run left behind: after a
// crash, a power cut, or a Devpit that was killed. It asks first, because
// removing needs an admin prompt.
type cleanupScreen struct {
	deps    Deps
	man     host.Manifest
	stage   cleanupStage
	confirm confirm.Model
	err     error
	run     *runHandle
}

// newCleanupScreen returns the screen for manifest m.
func newCleanupScreen(deps Deps, m host.Manifest) cleanupScreen {
	return cleanupScreen{
		deps: deps, man: m, stage: cleanupAsk,
		confirm: confirm.New(cleanupID, "Remove the old share?",
			"Devpit shared "+m.Path+" earlier and did not get to remove it. "+
				"This removes the share, its temporary login and the settings Devpit changed. "+
				"Windows will ask for permission."),
	}
}

// Init implements uictx.Screen.
func (m cleanupScreen) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (m cleanupScreen) Title() string { return "Clean up" }

// ShortHelp implements uictx.Screen.
func (m cleanupScreen) ShortHelp() []key.Binding {
	if m.stage == cleanupAsk {
		return m.confirm.Keys.ShortHelp()
	}
	return nil
}

// FullHelp implements uictx.Screen.
func (m cleanupScreen) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

// Busy implements uictx.BusyReporter.
func (m cleanupScreen) Busy() bool { return m.stage == cleanupRunning }

// Stop implements uictx.Stopper.
func (m cleanupScreen) Stop() { m.run.stop() }

// Update implements uictx.Screen.
func (m cleanupScreen) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case confirm.AnsweredMsg:
		if msg.ID != cleanupID {
			return m, nil
		}
		if msg.Answer != confirm.AnswerYes {
			return m, uictx.Pop()
		}
		h, ctx := newRunHandle()
		m.run, m.stage = h, cleanupRunning
		fn, man := m.deps.CleanUp, m.man
		return m, func() tea.Msg { return cleanupDoneMsg{err: fn(ctx, man)} }
	case cleanupDoneMsg:
		if msg.err != nil {
			m.stage, m.err = cleanupFailed, msg.err
			return m, nil
		}
		m.stage = cleanupDone
		return m, nil
	case tea.KeyPressMsg:
		switch m.stage {
		case cleanupAsk:
			next, cmd := m.confirm.Update(msg)
			m.confirm = next
			return m, cmd
		case cleanupDone, cleanupFailed:
			if msg.Code == tea.KeyEnter {
				return m, uictx.Pop()
			}
		case cleanupRunning:
		}
	}
	return m, nil
}

// View implements uictx.Screen.
func (m cleanupScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	switch m.stage {
	case cleanupAsk:
		return m.confirm.View(ctx)
	case cleanupRunning:
		return th.Muted.Render(ctx.SpinnerFrame(0) + " Removing the old share…")
	case cleanupDone:
		return th.Success.Render(ctx.Icons.Tick+" The old share is gone.") + "\n\n" +
			th.Muted.Render("Press Enter to go back.")
	default:
		var declined *elevate.DeclinedError
		var b strings.Builder
		if errors.As(m.err, &declined) {
			b.WriteString(th.Warning.Render(ctx.Icons.Warn+" You said no to the admin prompt. Nothing was changed.") + "\n\n")
			b.WriteString(th.Muted.Render("The old share is still there. Devpit will offer again next time."))
		} else {
			b.WriteString(th.Danger.Render(ctx.Icons.Fail+" Could not remove everything.") + "\n\n")
			b.WriteString(ctx.Wrap(th.Muted.Render(oneLine(m.err.Error()))) + "\n\n")
			b.WriteString(th.Muted.Render("Devpit will offer again next time."))
		}
		b.WriteString("\n\n" + th.Muted.Render("Press Enter to go back."))
		return b.String()
	}
}
