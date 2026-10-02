package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Undo takes back the latest change Devpit made (and the changes made
// together with it). It says first, in plain words, what it would put back;
// a file changed by hand since is never overwritten: undo stops and says
// which file and what to do.

// undoInfoMsg is what undo would take back.
type undoInfoMsg struct {
	info service.UndoInfo
	err  error
}

// undoScreen is the undo screen.
type undoScreen struct {
	sess   *session
	depth  int
	run    *runHandle
	ctx    context.Context
	loaded bool
	info   service.UndoInfo
	err    error
	change change
	back   key.Binding
}

func newUndoScreen(s *session, depth int) undoScreen {
	h, ctx := newRunHandle()
	return undoScreen{sess: s, depth: depth, run: h, ctx: ctx, back: bind("back", "enter")}
}

func (m undoScreen) Init() tea.Cmd {
	svc := m.sess.svc
	return func() tea.Msg {
		info, err := svc.UndoPreview()
		return undoInfoMsg{info: info, err: err}
	}
}

func (m undoScreen) Title() string { return "Undo" }

// Busy implements uictx.BusyReporter.
func (m undoScreen) Busy() bool { return m.change.busy() }

// Stop implements uictx.Stopper. Undo itself is not cancelled half way: it
// puts every file back or none.
func (m undoScreen) Stop() {}

// TerminalProgress implements uictx.ProgressReporter.
func (m undoScreen) TerminalProgress() *tea.ProgressBar { return m.change.progress() }

func (m undoScreen) ShortHelp() []key.Binding {
	if !m.loaded || m.err != nil {
		return []key.Binding{m.back}
	}
	return m.change.help()
}

func (m undoScreen) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

func (m undoScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		return m, nil
	}
	if im, ok := msg.(undoInfoMsg); ok {
		m.loaded, m.info, m.err = true, im.info, im.err
		if im.err == nil {
			doc := previewDoc{sentences: im.info.Lines}
			summary := "Undone"
			if len(im.info.Entries) > 0 {
				summary = "Undone: " + im.info.Entries[0].Summary
			}
			svc := m.sess.svc
			m.change = newChange(m.ctx, m.sess, doc, "Undo it?", "Devpit puts back only what it wrote. A file changed by hand since stops it.", summary,
				func(ctx context.Context) <-chan accounts.Event { return undoStream(ctx, svc) })
		}
		return m, nil
	}
	if km, ok := msg.(tea.KeyPressMsg); ok && (!m.loaded || m.err != nil) {
		if key.Matches(km, m.back) {
			return m, uictx.Pop()
		}
		return m, nil
	}
	if !m.loaded || m.err != nil {
		return m, nil
	}
	next, cmd, out := m.change.update(msg, ctx, 2)
	m.change = next
	switch out {
	case outcomeDeclined:
		return m, uictx.Pop()
	case outcomeBack:
		return m, popN(m.depth)
	}
	return m, cmd
}

// undoStream runs Undo and reports it as events: the shims brought back in
// line, then one final event with what was left in place on purpose.
func undoStream(ctx context.Context, svc Service) <-chan accounts.Event {
	ch := make(chan accounts.Event, 16)
	go func() {
		defer close(ch)
		ch <- accounts.NewEvent("Putting the files back", accounts.StepRunning, "")
		res, err := svc.Undo(ctx, func(ev accounts.Event) {
			ev.Final = false
			ch <- ev
		})
		if err != nil {
			ch <- accounts.Failed("Putting the files back", err)
			return
		}
		ch <- accounts.NewEvent("Putting the files back", accounts.StepDone, fmt.Sprintf("%d change(s) taken back", max(1, len(res.Entries))))
		for _, n := range res.Notes {
			ch <- accounts.NewEvent("Left in place", accounts.StepWarning, n)
		}
		final := accounts.NewEvent("Undone", accounts.StepDone, "")
		final.Final = true
		ch <- final
	}()
	return ch
}

func (m undoScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	title := "Undo the last change?"
	switch m.change.stage {
	case chApplying:
		title = "Undoing"
	case chDone:
		title = "Undone"
	case chFailed:
		title = "Undo did not finish"
	}
	out := []string{heading(ctx, title, ""), ""}
	switch {
	case !m.loaded:
		out = append(out, " "+th.Muted.Render("Looking at the last change"+ellipsis(ctx)))
	case errors.Is(m.err, accounts.ErrNothingToUndo):
		out = append(out, " "+th.Base.Render(ctx.Icons.Queued+" Nothing to undo."), "")
		out = append(out, wrap(ctx, th.Muted, "Devpit has made no change to your accounts that can still be taken back.", 1, 0)...)
		out = append(out, "", keyHints(ctx, "enter", "back"))
	case m.err != nil:
		out = append(out, errorView(ctx, m.err), "", keyHints(ctx, "enter", "back"))
	default:
		out = append(out, m.change.view(ctx, ctx.BodyHeight-2)...)
	}
	return strings.Join(out, "\n")
}
