package accounts

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/importer"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The first time Accounts opens on a PC that already has accounts (from
// claude-acc, or other Claude Code folders), it says so on a card: use
// them in Devpit, or ignore them. Using them is a preview and one undoable
// change; the accounts stay where they are, so nobody signs in again. After
// it, the steps to retire claude-acc safely are shown as a numbered list.
// Ignore is remembered, and `i` on the page offers them again.

// importStage is where the import card is.
type importStage int

const (
	imAsk importStage = iota
	imPlanning
	imChange
	imGuide
	imFailed
)

// Import choices.
const (
	imUse    = "use"
	imIgnore = "ignore"
)

// importPlanMsg is the import's preview.
type importPlanMsg struct {
	plan importer.Plan
	err  error
}

// importScreen is the card.
type importScreen struct {
	sess   *session
	depth  int
	run    *runHandle
	ctx    context.Context
	stage  importStage
	choice menu.Model
	plan   importer.Plan
	change change
	err    error
	scroll int
	back   key.Binding
}

func newImportScreen(s *session, depth int) importScreen {
	h, ctx := newRunHandle()
	m := importScreen{sess: s, depth: depth, run: h, ctx: ctx, back: bind("back", "enter")}
	m.choice = menu.New([]menu.Item{
		{ID: imUse, Title: "Use them in Devpit", Desc: "Preview first. They stay where they are; nobody signs in again."},
		{ID: imIgnore, Title: "Ignore", Desc: "Leave them as they are. Press i on the Accounts page to come back to this."},
	})
	return m
}

func (m importScreen) Init() tea.Cmd { return nil }

func (m importScreen) Title() string { return "Accounts already on this PC" }

func (m importScreen) Busy() bool { return m.stage == imChange && m.change.busy() }

func (m importScreen) Stop() { m.run.stop() }

func (m importScreen) TerminalProgress() *tea.ProgressBar { return m.change.progress() }

func (m importScreen) ShortHelp() []key.Binding {
	switch m.stage {
	case imAsk:
		return []key.Binding{m.choice.Keys.Up, m.choice.Keys.Select}
	case imChange:
		return m.change.help()
	}
	return []key.Binding{m.back}
}

func (m importScreen) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

func (m importScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case menu.SelectedMsg:
		if m.stage != imAsk {
			return m, nil
		}
		if msg.ID == imIgnore {
			svc, f := m.sess.svc, m.sess.found
			m.sess.dismissed = true
			return m, tea.Batch(uictx.Pop(), func() tea.Msg {
				if err := svc.DismissImport(f); err != nil {
					return uictx.StatusMsg{Level: "warning", Text: "Could not remember that: " + accounts.Scrub(err.Error())}
				}
				return nil
			})
		}
		m.stage = imPlanning
		return m, m.planCmd()
	case importPlanMsg:
		if msg.err != nil {
			m.stage, m.err = imFailed, msg.err
			return m, nil
		}
		m.plan = msg.plan
		svc, plan := m.sess.svc, msg.plan
		doc := previewDoc{sentences: plan.Sentences, notes: plan.Notes, noChange: plan.NoChange}
		m.change = newChange(m.ctx, m.sess, doc, "Bring these into Devpit?", "One undo takes all of it back. No account folder is moved or copied.", plan.Summary,
			func(context.Context) <-chan accounts.Event { return importStream(svc, plan) })
		m.change.fresh = accounts.ToolClaude
		m.stage = imChange
		return m, nil
	}
	switch m.stage {
	case imAsk:
		if next, cmd, ok := m.choice.Pointer(ctx, msg, m.choiceTop(ctx)); ok {
			m.choice = next
			return m, cmd
		}
		next, cmd := m.choice.Update(msg)
		m.choice = next
		return m, cmd
	case imChange:
		next, cmd, out := m.change.update(msg, ctx, 2)
		m.change = next
		switch out {
		case outcomeDeclined:
			m.stage = imAsk
			return m, nil
		case outcomeBack:
			if m.change.stage == chDone && len(importer.RemovalGuide(m.sess.found)) > 0 {
				m.stage = imGuide
				return m, nil
			}
			return m, uictx.Pop()
		}
		return m, cmd
	case imGuide, imFailed:
		m.scroll = scrollBy(m.scroll, msg)
		if km, ok := msg.(tea.KeyPressMsg); ok && key.Matches(km, m.back) {
			m.sess.hasFound = false
			return m, uictx.Pop()
		}
	}
	return m, nil
}

// planCmd previews the import: claude-acc's accounts, links and default,
// and the other Claude Code folders found.
func (m importScreen) planCmd() tea.Cmd {
	svc, f := m.sess.svc, m.sess.found
	return func() tea.Msg {
		opt := importer.Options{ClaudeAcc: f.ClaudeAcc != nil}
		for _, d := range f.ConfigDirs {
			opt.ConfigDirs = append(opt.ConfigDirs, d.Dir)
		}
		p, err := svc.PlanImport(f, opt)
		return importPlanMsg{plan: p, err: err}
	}
}

// importStream applies the import and reports it as events.
func importStream(svc Service, p importer.Plan) <-chan accounts.Event {
	ch := make(chan accounts.Event, 16)
	go func() {
		defer close(ch)
		ch <- accounts.NewEvent("Adding the accounts and rules", accounts.StepRunning, "")
		ent, err := svc.ApplyImport(p, func(ev accounts.Event) {
			ev.Final = false
			ch <- ev
		})
		if err != nil {
			ch <- accounts.Failed("Adding the accounts and rules", err)
			return
		}
		ch <- accounts.NewEvent("Adding the accounts and rules", accounts.StepDone, ent.Summary)
		final := accounts.NewEvent("Imported", accounts.StepDone, "")
		final.Final, final.EntryID = true, ent.ID
		ch <- final
	}()
	return ch
}

// facts are what was found, in short lines.
func (m importScreen) facts(ctx uictx.Context) []string {
	th := ctx.Theme
	f := m.sess.found
	out := wrap(ctx, th.Base.Bold(true), f.Summary(), 1, 0)
	if c := f.ClaudeAcc; c != nil {
		for _, a := range c.Accounts {
			who := a.Email
			if who == "" {
				who = "no email recorded"
			}
			out = append(out, "   "+th.Base.Render(fit(ctx, a.Name+" ("+who+")", ctx.Width-6))+"  "+th.Muted.Render(fit(ctx, a.Dir, max(0, ctx.Width-12-len(a.Name)-len(who)))))
		}
		if n := c.UsableLinks(); n > 0 {
			out = append(out, "   "+th.Muted.Render(strconv.Itoa(n)+" folder links become folder rules"))
		}
	}
	for _, d := range f.ConfigDirs {
		out = append(out, "   "+th.Base.Render(fit(ctx, d.Dir, ctx.Width-6)))
	}
	return out
}

func (m importScreen) choiceTop(ctx uictx.Context) int { return 2 + len(m.facts(ctx)) + 1 }

func (m importScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	out := []string{heading(ctx, "Found accounts already on this PC", ""), ""}
	switch m.stage {
	case imAsk:
		out = append(out, m.facts(ctx)...)
		out = append(out, "", m.choice.View(ctx))
	case imPlanning:
		out = append(out, " "+th.Muted.Render("Working out what would be added"+ellipsis(ctx)))
	case imChange:
		out = append(out, m.change.view(ctx, ctx.BodyHeight-2)...)
	case imGuide:
		out = append(out, " "+th.Success.Render(ctx.Icons.Tick)+" "+th.Base.Bold(true).Render("Imported. To retire claude-acc safely:"), "")
		var guide []string
		for i, st := range importer.RemovalGuide(m.sess.found) {
			guide = append(guide, wrap(ctx, th.Base, fmt.Sprintf("%d. %s", i+1, st.Title), 1, 3)...)
			for _, d := range st.Detail {
				guide = append(guide, wrap(ctx, th.Muted, d, 4, 0)...)
			}
		}
		out = append(out, scrolled(ctx, guide, m.scroll, ctx.BodyHeight-len(out)-2)...)
		out = append(out, "", keyHints(ctx, "↑↓", "scroll", "enter", "done"))
	case imFailed:
		out = append(out, errorView(ctx, m.err), "", keyHints(ctx, "enter", "back"))
	}
	if len(out) > ctx.BodyHeight && ctx.BodyHeight > 0 {
		out = out[:ctx.BodyHeight]
	}
	return strings.Join(out, "\n")
}
