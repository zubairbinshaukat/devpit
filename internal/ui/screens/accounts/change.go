package accounts

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// change is the preview → yes/no → live rows → done card cycle as one
// piece, for the screens whose change is not an accounts.Change: undo,
// Fix old rules, renaming or removing an account, an SSH key for a folder,
// the Claude setup and the import. The flow does the same for a Change,
// with the stages around it.
type change struct {
	sess    *session
	ctx     context.Context
	pane    confirmPane
	start   func(ctx context.Context) <-chan accounts.Event
	summary string
	// fresh is the tool whose row the change touches, flagged on the page.
	fresh accounts.Tool

	stage   changeStage
	live    liveRun
	err     error
	showLog bool
	frame   int
	keys    struct{ Back, Log key.Binding }
}

// changeStage is where a change is.
type changeStage int

const (
	chAsk changeStage = iota
	chApplying
	chDone
	chFailed
)

// changeOutcome is what the screen holding a change should do next.
type changeOutcome int

const (
	// outcomeNone: nothing for the screen to do.
	outcomeNone changeOutcome = iota
	// outcomeDeclined: the person said No; nothing changed.
	outcomeDeclined
	// outcomeBack: the person is done with the result.
	outcomeBack
)

// newChange returns a change asking question over doc. start runs it.
func newChange(ctx context.Context, s *session, doc previewDoc, question, detail, summary string, start func(context.Context) <-chan accounts.Event) change {
	c := change{
		sess: s, ctx: ctx, start: start, summary: summary,
		pane: newConfirmPane("change", doc, question, detail).readOnly(s.readOnly),
	}
	c.keys.Back = bind("back", "enter")
	c.keys.Log = bind("log", "l")
	return c
}

// busy reports whether the change is in flight.
func (c change) busy() bool { return c.stage == chApplying }

// help is the footer for the change's stage.
func (c change) help() []key.Binding {
	switch c.stage {
	case chAsk:
		if c.pane.ask {
			return c.pane.dialog.Keys.ShortHelp()
		}
		return []key.Binding{c.keys.Back}
	case chApplying:
		return []key.Binding{c.keys.Log}
	}
	return []key.Binding{c.keys.Back, c.keys.Log}
}

// update handles the change's messages. top is the body row the change's
// view starts on, for clicks.
func (c change) update(msg tea.Msg, ctx uictx.Context, top int) (change, tea.Cmd, changeOutcome) {
	switch msg := msg.(type) {
	case spinMsg:
		if c.stage == chApplying {
			c.frame++
			return c, c.sess.spin(), outcomeNone
		}
	case confirm.AnsweredMsg:
		if c.stage != chAsk {
			return c, nil, outcomeNone
		}
		if msg.Answer != confirm.AnswerYes {
			return c, nil, outcomeDeclined
		}
		c.stage = chApplying
		c.live = startRun(c.sess.nextRun(), c.start(c.ctx))
		return c, tea.Batch(c.live.wait(), c.sess.spin()), outcomeNone
	case runEventMsg:
		if msg.id != c.live.id || c.stage != chApplying {
			return c, nil, outcomeNone
		}
		c.live = c.live.add(msg.ev, msg.ok)
		if !c.live.done() {
			return c, c.live.wait(), outcomeNone
		}
		c.stage = chDone
		if !c.live.ok() {
			c.stage, c.err = chFailed, c.live.err()
		}
		return c, c.sess.refreshCmd(c.sess.folder, c.fresh), outcomeNone
	case tea.KeyPressMsg:
		switch c.stage {
		case chAsk:
			if !c.pane.ask && msg.String() == "enter" {
				return c, nil, outcomeDeclined
			}
			next, cmd := c.pane.update(msg)
			c.pane = next
			return c, cmd, outcomeNone
		case chApplying:
			if key.Matches(msg, c.keys.Log) {
				c.showLog = !c.showLog
			}
		case chDone, chFailed:
			switch {
			case key.Matches(msg, c.keys.Log):
				c.showLog = !c.showLog
			case key.Matches(msg, c.keys.Back):
				return c, nil, outcomeBack
			}
		}
	case tea.MouseClickMsg:
		if c.stage == chAsk && msg.Button == tea.MouseLeft {
			next, cmd := c.pane.click(ctx, ctx.BodyHeight-top, msg.X, ctx.BodyRow(msg.Y)-top)
			c.pane = next
			return c, cmd, outcomeNone
		}
	case tea.MouseWheelMsg:
		if c.stage == chAsk {
			c.pane, _ = c.pane.update(msg)
		}
	}
	return c, nil, outcomeNone
}

// view draws the change in room lines.
func (c change) view(ctx uictx.Context, room int) []string {
	th := ctx.Theme
	switch c.stage {
	case chAsk:
		ls, _ := c.pane.layout(ctx, room)
		return ls
	case chApplying:
		return []string{c.live.view(ctx, c.frame, room, c.showLog)}
	case chDone:
		out := []string{" " + th.Success.Render(ctx.Icons.Tick) + " " + th.CardTitle.Render(fit(ctx, c.summary, ctx.Width-6)), ""}
		var notes []string
		for _, w := range c.live.warnings {
			notes = append(notes, wrap(ctx, th.Warning, ctx.Icons.Warn+" "+w.Step+". "+w.Detail, 1, 2)...)
		}
		out = append(out, c.live.view(ctx, c.frame, max(1, room-len(notes)-5), c.showLog))
		if len(notes) > 0 {
			out = append(append(out, ""), notes...)
		}
		return append(out, "", keyHints(ctx, "enter", "back", "l", "log"))
	}
	out := strings.Split(errorViewAs(ctx, c.err, "The change did not finish"), "\n")
	if len(c.live.rows) > 0 {
		out = append(out, "", c.live.view(ctx, c.frame, max(1, room-len(out)-3), c.showLog))
	}
	return append(out, "", keyHints(ctx, "enter", "back"))
}

// progress is the taskbar bar while the change runs.
func (c change) progress() *tea.ProgressBar {
	if c.stage == chApplying {
		return c.live.progress()
	}
	return nil
}
