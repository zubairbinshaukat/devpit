package accounts

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Manage accounts renames or removes a tool's named accounts. Removing one
// lists every folder rule that named it and what that folder uses after,
// suggests the tool's own sign-out first, keeps the account's folder (and
// the sign-in in it), and asks for a typed word. Both are previewed and
// undoable like every other change.

// manageStage is where Manage accounts is.
type manageStage int

const (
	mgLoading manageStage = iota
	mgList
	mgRename
	mgPlanning
	mgChange
	mgFailed
)

// Messages of Manage accounts.
type (
	manageListMsg struct {
		st  *accounts.Store
		err error
	}
	editPlanMsg struct {
		p   service.AccountEditPreview
		err error
	}
)

// manageScreen is Manage accounts.
type manageScreen struct {
	sess   *session
	tool   accounts.Tool
	depth  int
	run    *runHandle
	ctx    context.Context
	stage  manageStage
	st     *accounts.Store
	list   menu.Model
	name   textinput.Model
	target string
	change change
	err    error
	keys   struct{ Rename, Remove, Save, Back key.Binding }
}

func newManageScreen(s *session, t accounts.Tool, depth int) manageScreen {
	h, ctx := newRunHandle()
	m := manageScreen{sess: s, tool: t, depth: depth, run: h, ctx: ctx}
	m.keys.Rename = bind("rename", "r")
	m.keys.Remove = bind("remove", "d")
	m.keys.Save = bind("preview", "enter")
	m.keys.Back = bind("back", "enter")
	m.name = newInput("a new name", 32)
	return m
}

func (m manageScreen) Init() tea.Cmd { return m.loadCmd() }

func (m manageScreen) loadCmd() tea.Cmd {
	svc := m.sess.svc
	return func() tea.Msg {
		st, _, err := svc.Load()
		return manageListMsg{st: st, err: err}
	}
}

func (m manageScreen) Title() string { return m.tool.DisplayName() + " › Manage accounts" }

func (m manageScreen) Busy() bool { return m.stage == mgChange && m.change.busy() }

func (m manageScreen) Stop() { m.run.stop() }

func (m manageScreen) TerminalProgress() *tea.ProgressBar { return m.change.progress() }

func (m manageScreen) ShortHelp() []key.Binding {
	switch m.stage {
	case mgList:
		if len(m.list.Items()) == 0 {
			return nil
		}
		return []key.Binding{m.list.Keys.Up, m.keys.Rename, m.keys.Remove}
	case mgRename:
		return []key.Binding{m.keys.Save}
	case mgChange:
		return m.change.help()
	case mgFailed:
		return []key.Binding{m.keys.Back}
	}
	return nil
}

func (m manageScreen) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

func (m manageScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case manageListMsg:
		if msg.err != nil {
			m.stage, m.err = mgFailed, msg.err
			return m, nil
		}
		m.st = msg.st
		var items []menu.Item
		for _, a := range msg.st.AccountsFor(m.tool) {
			desc := "not chosen for any folder"
			if rs := msg.st.RulesUsing(m.tool, a.Name); len(rs) > 0 {
				var fs []string
				for _, r := range rs {
					fs = append(fs, r.Folder)
				}
				desc = "chosen for " + strings.Join(fs, ", ")
			}
			items = append(items, menu.Item{ID: a.Name, Title: a.Display(), Desc: desc})
		}
		m.list = menu.New(items)
		m.stage = mgList
		return m, nil
	case editPlanMsg:
		if msg.err != nil {
			m.stage, m.err = mgFailed, msg.err
			return m, nil
		}
		svc, p := m.sess.svc, msg.p
		doc := previewDoc{sentences: p.Sentences, keptIntro: p.FallsBackIntro, warnings: append(append([]string(nil), p.Before...), p.Warnings...), edits: p.Edits}
		for _, k := range p.FallsBack {
			doc.kept = append(doc.kept, [2]string{k.Folder, k.Display})
		}
		question := "Rename it?"
		if p.Edit.Remove {
			question = "Remove " + p.Edit.Name + " from Devpit?"
		}
		m.change = newChange(m.ctx, m.sess, doc, question, "You can undo it afterwards with u on the Accounts page.", p.Summary,
			func(ctx context.Context) <-chan accounts.Event { return svc.ApplyAccountEdit(ctx, p) })
		m.change.pane = m.change.pane.withWord(p.TypedWord)
		m.change.fresh = m.tool
		m.stage = mgChange
		return m, nil
	}
	switch m.stage {
	case mgList:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			it, have := m.list.Selected()
			switch {
			case have && key.Matches(km, m.keys.Rename):
				m.target = it.ID
				m.stage = mgRename
				m.name.SetValue(it.ID)
				m.name.CursorEnd()
				return m, m.name.Focus()
			case have && key.Matches(km, m.keys.Remove):
				m.target = it.ID
				m.stage = mgPlanning
				return m, m.planCmd(service.AccountEdit{Tool: m.tool, Name: it.ID, Remove: true})
			}
		}
		if _, ok := msg.(menu.SelectedMsg); ok {
			return m, nil
		}
		if next, cmd, ok := m.sizedList(ctx).Pointer(ctx, msg, m.listTop(ctx)); ok {
			m.list = next
			return m, cmd
		}
		next, cmd := m.list.Update(msg)
		m.list = next
		return m, cmd
	case mgRename:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			if km.String() == "enter" {
				m.stage = mgPlanning
				return m, m.planCmd(service.AccountEdit{Tool: m.tool, Name: m.target, NewName: strings.TrimSpace(m.name.Value())})
			}
			var cmd tea.Cmd
			m.name, cmd = m.name.Update(km)
			return m, cmd
		}
	case mgChange:
		next, cmd, out := m.change.update(msg, ctx, 2)
		m.change = next
		switch out {
		case outcomeDeclined, outcomeBack:
			m.stage = mgLoading
			return m, m.loadCmd()
		}
		return m, cmd
	case mgFailed:
		if km, ok := msg.(tea.KeyPressMsg); ok && key.Matches(km, m.keys.Back) {
			m.stage = mgLoading
			m.err = nil
			return m, m.loadCmd()
		}
	}
	return m, nil
}

// listIntro is the line above the list.
func (m manageScreen) listIntro(ctx uictx.Context) []string {
	return wrap(ctx, ctx.Theme.Muted, "Your usual sign-in is the tool's own, so it is not listed. Pick one, then press r to rename it or d to remove it.", 1, 0)
}

// listTop is the body row the list starts on.
func (m manageScreen) listTop(ctx uictx.Context) int { return 2 + len(m.listIntro(ctx)) + 1 }

// sizedList is the list at the height View draws it.
func (m manageScreen) sizedList(ctx uictx.Context) menu.Model {
	return m.list.SetHeight(max(3, ctx.BodyHeight-m.listTop(ctx)))
}

func (m manageScreen) planCmd(e service.AccountEdit) tea.Cmd {
	svc := m.sess.svc
	return func() tea.Msg {
		ctx, cancel := engineCtx()
		defer cancel()
		p, err := svc.PlanAccountEdit(ctx, e)
		return editPlanMsg{p: p, err: err}
	}
}

func (m manageScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	out := []string{heading(ctx, m.tool.DisplayName()+" accounts", ""), ""}
	switch m.stage {
	case mgLoading, mgPlanning:
		out = append(out, " "+th.Muted.Render("Reading the accounts"+ellipsis(ctx)))
	case mgList:
		if len(m.list.Items()) == 0 {
			out = append(out, wrap(ctx, th.Muted, "Devpit has not saved any "+m.tool.DisplayName()+" account yet. Your usual sign-in is the tool's own; it is not Devpit's to rename or remove.", 1, 0)...)
			break
		}
		out = append(out, m.listIntro(ctx)...)
		out = append(out, "", m.sizedList(ctx).View(ctx))
	case mgRename:
		out = append(out, " "+th.Base.Render("A new name for "+m.target+":"), " "+styleInput(m.name, ctx).View())
		out = append(out, "   "+th.Muted.Render("Folders that use it keep using it under the new name."))
	case mgChange:
		out = append(out, m.change.view(ctx, ctx.BodyHeight-2)...)
	case mgFailed:
		out = append(out, errorView(ctx, m.err), "", keyHints(ctx, "enter", "back"))
	}
	return strings.Join(out, "\n")
}
