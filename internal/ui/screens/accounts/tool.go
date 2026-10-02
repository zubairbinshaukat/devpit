package accounts

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Tool page actions.
const (
	actHere       = "here"
	actEverywhere = "everywhere"
	actOnce       = "once"
	actAdd        = "add"
	actSignAgain  = "sign-again"
	actRemoveRule = "remove-rule"
	actClaude     = "claude-setup"
	actManage     = "manage"
	actGit        = "git"
)

// capsMsg is what a tool supports on this PC, asked off the update loop.
type capsMsg struct {
	tool accounts.Tool
	caps adapters.Caps
}

// toolScreen is one tool's page: who it is signed in as here, where that
// comes from (the rule chain when rules are nested), and the actions, the
// main ones first. An action the tool cannot do is shown greyed out with
// the adapter's own reason, never hidden.
type toolScreen struct {
	sess  *session
	tool  accounts.Tool
	depth int
	caps  adapters.Caps
	asked bool
	menu  menu.Model
}

// newToolScreen returns the page of tool t, depth screens above the page.
func newToolScreen(s *session, t accounts.Tool, depth int) toolScreen {
	m := toolScreen{sess: s, tool: t, depth: depth, caps: adapters.Supports(t)}
	m.menu = menu.New(m.items()).DescOnSelectedOnly(true)
	return m
}

// Init implements uictx.Screen: ask the tool what it supports here. For
// Claude Code that is `claude auth --help`, never a sign-in check.
func (m toolScreen) Init() tea.Cmd {
	svc, t := m.sess.svc, m.tool
	return func() tea.Msg {
		ctx, cancel := engineCtx()
		defer cancel()
		return capsMsg{tool: t, caps: svc.Caps(ctx, t)}
	}
}

// Title implements uictx.Screen.
func (m toolScreen) Title() string { return m.tool.DisplayName() }

// ShortHelp implements uictx.Screen.
func (m toolScreen) ShortHelp() []key.Binding {
	return []key.Binding{m.menu.Keys.Up, m.menu.Keys.Select}
}

// FullHelp implements uictx.Screen.
func (m toolScreen) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

// status is the tool's row of the latest overview.
func (m toolScreen) status() service.ToolStatus {
	ts, _ := m.sess.status(m.tool)
	return ts
}

// items builds the action list from what the tool supports and what is set
// up here.
func (m toolScreen) items() []menu.Item {
	ts := m.status()
	c := m.caps
	name := m.tool.DisplayName()
	off := func(it menu.Item, ok bool, hint string) menu.Item {
		if !ok {
			it.Disabled = true
			it.Hint = hint
		}
		return it
	}
	var out []menu.Item
	if c.ShowOnly {
		return []menu.Item{{ID: actHere, Title: "Use another account…", Hint: "show only", Disabled: true}}
	}
	manage := menu.Item{ID: actManage, Title: "Manage accounts…", Desc: "Rename or remove an account"}
	if !ts.Installed {
		// Nothing can be checked or switched; the accounts Devpit already
		// has can still be tidied up.
		return []menu.Item{manage}
	}
	expired := ts.Identity.State() == accounts.StateExpired || ts.Identity.State() == accounts.StateNotSignedIn
	again := menu.Item{ID: actSignAgain, Title: "Sign in again", Desc: "Sign " + ts.Account.Name + " in again, in its own folder"}
	canAgain := service.SignInAgainArgs(m.tool) != nil
	if expired && canAgain {
		out = append(out, again)
	}
	out = append(out,
		off(menu.Item{ID: actHere, Title: "Use another account here…", Desc: "In this folder and every folder inside it"}, c.FolderRules, "not available"),
		off(menu.Item{ID: actEverywhere, Title: "Use another account everywhere…", Desc: "Wherever no folder rule says otherwise"}, c.Everywhere, "not available"),
		off(menu.Item{ID: actOnce, Title: "Just this once…", Desc: "Run one command with another account; nothing is saved"}, c.JustOnce, "not available"),
		off(menu.Item{ID: actAdd, Title: "Sign in with another account…", Desc: "Run " + name + "'s own sign-in and name the account"}, c.AddAccount, "not available"),
	)
	if !expired && canAgain && !ts.Account.IsDefault() {
		out = append(out, again)
	}
	if ts.Resolution.Reason == accounts.ReasonFolderRule {
		out = append(out, menu.Item{
			ID: actRemoveRule, Title: "Remove the rule on " + ts.Resolution.RuleFolder,
			Desc: "This folder then follows the next rule out, or everywhere",
		})
	}
	if m.tool == accounts.ToolClaude {
		out = append(out, menu.Item{ID: actClaude, Title: "Bring your Claude Code setup over…", Desc: "Share or copy skills, agents, commands, CLAUDE.md and settings"})
	}
	if m.tool == accounts.ToolGitHub {
		out = append(out, menu.Item{ID: actGit, Title: "Pushes and SSH keys…", Desc: "Which account a push uses here, and a key for this folder"})
	}
	return append(out, manage)
}

// Update implements uictx.Screen.
func (m toolScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		m.menu = m.menu.SetItems(m.items())
		return m, nil
	}
	switch msg := msg.(type) {
	case capsMsg:
		if msg.tool == m.tool {
			m.caps, m.asked = msg.caps, true
			cur := m.menu.Cursor()
			m.menu = menu.New(m.items()).DescOnSelectedOnly(true)
			m.menu = m.menu.SetCursor(cur)
			if it, ok := m.menu.Selected(); ok && it.Disabled {
				m.menu = menu.New(m.items()).DescOnSelectedOnly(true)
			}
		}
		return m, nil
	case menu.SelectedMsg:
		return m, m.act(msg.ID)
	}
	if next, cmd, ok := m.sizedMenu(ctx).Pointer(ctx, msg, m.menuTop(ctx)); ok {
		m.menu = next
		return m, cmd
	}
	next, cmd := m.menu.Update(msg)
	m.menu = next
	return m, cmd
}

// act opens what an action leads to.
func (m toolScreen) act(id string) tea.Cmd {
	d := m.depth + 1
	switch id {
	case actHere:
		return uictx.Push(newFlow(m.sess, m.tool, flowUse, accounts.ScopeFolder, d))
	case actEverywhere:
		return uictx.Push(newFlow(m.sess, m.tool, flowUse, accounts.ScopeEverywhere, d))
	case actOnce:
		return uictx.Push(newFlow(m.sess, m.tool, flowUse, accounts.ScopeOnce, d))
	case actAdd:
		return uictx.Push(newFlow(m.sess, m.tool, flowAdd, accounts.ScopeFolder, d))
	case actSignAgain:
		return uictx.Push(newSignAgainScreen(m.sess, m.tool, m.status().Account, d))
	case actRemoveRule:
		ts := m.status()
		return uictx.Push(newRemoveRuleFlow(m.sess, m.tool, ts.Resolution.RuleFolder, d))
	case actClaude:
		return uictx.Push(newClaudeScreen(m.sess, "", d, false))
	case actGit:
		return uictx.Push(newGitScreen(m.sess, d))
	case actManage:
		return uictx.Push(newManageScreen(m.sess, m.tool, d))
	}
	return nil
}

// facts are the lines above the actions: who, why, the chain, everywhere.
func (m toolScreen) facts(ctx uictx.Context) []string {
	th := ctx.Theme
	ts := m.status()
	label := func(s string) string { return " " + th.Muted.Render(s+pad(14-len(s))) }
	val := func(s string) string { return fit(ctx, s, ctx.Width-16) }
	var out []string
	if !ts.Installed {
		out = append(out, label("Installed")+th.Muted.Render(ctx.Icons.Absent+" not on this PC"))
		why := m.tool.DisplayName() + " is not installed, so nothing is checked or changed for it."
		if ts.Managed {
			why += " Its rules are kept and apply once it is installed."
		}
		return append(out, "", strings.Join(wrap(ctx, th.Muted, why, 1, 0), "\n"))
	}
	who := ts.Display
	whoStyle := th.Base.Bold(true)
	switch ts.Identity.State() {
	case accounts.StateExpired:
		who += "  " + ctx.Icons.Warn + " expired, sign in again"
		whoStyle = th.Warning
	case accounts.StateNotSignedIn:
		who += "  " + ctx.Icons.Queued + " not signed in"
	}
	first := "Signed in as"
	if m.tool == accounts.ToolGit {
		first = "Commits as"
	}
	if m.tool == accounts.ToolConvex {
		first = "Project"
		who = strings.TrimPrefix(who, "project: ")
	}
	out = append(out, label(first)+whoStyle.Render(val(who)))
	why := ts.Why
	out = append(out, label("Comes from")+th.Base.Render(val(why)))
	if c := chainCaption(ctx, ts.Resolution); c != "" {
		out = append(out, label("Rules here")+th.Muted.Render(val(c)))
	}
	if m.tool != accounts.ToolConvex {
		out = append(out, label("Everywhere")+th.Base.Render(val(ts.EverywhereDisplay)))
	}
	return out
}

// menuTop is the body row the action list starts on.
func (m toolScreen) menuTop(ctx uictx.Context) int {
	return 2 + len(m.facts(ctx)) + 1
}

// View implements uictx.Screen.
func (m toolScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	out := []string{heading(ctx, m.tool.DisplayName(), "in "+m.sess.folder), ""}
	out = append(out, m.facts(ctx)...)
	out = append(out, "")
	out = append(out, m.sizedMenu(ctx).View(ctx))
	note := m.note()
	if note != "" {
		out = append(out, "")
		out = append(out, wrap(ctx, th.Muted, note, 1, 0)...)
	}
	if ts := m.status(); len(ts.Problems) > 0 && ts.Installed {
		p := ts.Problems[0]
		out = append(out, "")
		out = append(out, wrap(ctx, th.Warning, ctx.Icons.Warn+" "+p.Message, 1, 2)...)
		if p.Fix != "" {
			out = append(out, wrap(ctx, th.Muted, "Fix: "+p.Fix, 3, 5)...)
		}
	}
	return strings.Join(out, "\n")
}

// sizedMenu is the action list at the height View draws it, so a click
// and the frame measure the same rows.
func (m toolScreen) sizedMenu(ctx uictx.Context) menu.Model {
	return m.menu.SetHeight(tightHeight(m.menu, ctx.BodyHeight-m.menuTop(ctx)-m.noteRoom(ctx)-m.problemRoom(ctx)))
}

// tightHeight is a list's height when it shows its description under the
// selected row only: every row and that one description, with no blank
// lines between rows (which the menu would add when it has room, so one
// tool's page would be spaced out and the next not), or room, whichever is
// less.
func tightHeight(mm menu.Model, room int) int {
	return max(3, min(room, len(mm.Items())+1))
}

// problemRoom is the room kept for the first problem and its fix.
func (m toolScreen) problemRoom(ctx uictx.Context) int {
	ts := m.status()
	if len(ts.Problems) == 0 || !ts.Installed {
		return 0
	}
	p := ts.Problems[0]
	n := 1 + len(wrap(ctx, ctx.Theme.Warning, p.Message, 1, 2))
	if p.Fix != "" {
		n += len(wrap(ctx, ctx.Theme.Muted, "Fix: "+p.Fix, 3, 5))
	}
	return n
}

// noteRoom is the room kept under the actions for the note.
func (m toolScreen) noteRoom(ctx uictx.Context) int {
	if m.note() == "" {
		return 0
	}
	return 1 + len(wrap(ctx, ctx.Theme.Muted, m.note(), 1, 0))
}

// note explains, in the adapter's own words, why some actions are off, or
// what is special about this tool.
func (m toolScreen) note() string {
	c := m.caps
	if ts := m.status(); !ts.Installed {
		return ""
	}
	switch {
	case c.ShowOnly:
		return c.Why
	case c.Beta && c.Why != "":
		return "Beta: " + c.Why
	case c.Why != "" && (!c.FolderRules || !c.Everywhere || !c.JustOnce || !c.AddAccount):
		return c.Why
	}
	return ""
}
