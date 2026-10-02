package accounts

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/choices"
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

// toolScreen is one tool's page: at the top, what is true now (who it is
// signed in as here, why that account, and which one other folders use);
// below, the choices in groups, the most likely first. A choice the tool
// cannot make is shown quiet with the adapter's own reason, never hidden.
type toolScreen struct {
	sess  *session
	tool  accounts.Tool
	depth int
	caps  adapters.Caps
	asked bool
	list  choices.Model
	check key.Binding
}

// newToolScreen returns the page of tool t, depth screens above the page.
func newToolScreen(s *session, t accounts.Tool, depth int) toolScreen {
	return toolScreen{
		sess: s, tool: t, depth: depth, caps: adapters.Supports(t),
		list: choices.New(), check: bind("check who is signed in", "v"),
	}
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
	if !m.status().Installed {
		return m.list.Keys.ShortHelp()
	}
	return []key.Binding{m.list.Keys.Up, m.list.Keys.Change, m.check}
}

// FullHelp implements uictx.Screen.
func (m toolScreen) FullHelp() [][]key.Binding {
	return append(m.list.Keys.FullHelp(), []key.Binding{m.check})
}

// status is the tool's row of the latest overview.
func (m toolScreen) status() service.ToolStatus {
	ts, _ := m.sess.status(m.tool)
	return ts
}

// groups builds the choices from what the tool supports and what is set up
// here: changing the account, signing in, the tool's own extras, tidying up.
func (m toolScreen) groups() []choices.Group {
	ts := m.status()
	c := m.caps
	name := m.tool.DisplayName()
	reason := c.Why
	if reason == "" {
		reason = name + " cannot do this."
	}
	off := func(it choices.Item, ok bool) choices.Item {
		if !ok {
			it.Disabled = reason
		}
		return it
	}
	manage := choices.Group{Title: "Manage", Items: []choices.Item{{
		ID: actManage, Label: "Rename or remove an account",
		Desc: "Rename or remove an account Devpit saved for " + name + ". Your usual sign-in is " + name + "'s own, so it is not listed.",
		Next: "You pick one, then see what changes before anything happens.",
	}}}
	if !ts.Installed {
		// Nothing can be checked or switched; the accounts Devpit already
		// has can still be tidied up.
		return []choices.Group{manage}
	}
	if c.ShowOnly {
		return []choices.Group{{Title: "Change the account", Items: []choices.Item{{
			ID: actHere, Label: "Use another account here", Disabled: reason,
			Desc: "Pick which " + name + " account this folder uses.",
		}}}}
	}
	expired := ts.Identity.State() == accounts.StateExpired || ts.Identity.State() == accounts.StateNotSignedIn
	canAgain := service.SignInAgainArgs(m.tool) != nil
	again := choices.Item{
		ID: actSignAgain, Label: "Sign in again",
		Desc: "Sign " + ts.Account.Name + " in again with " + name + "'s own sign-in, kept in that account's own folder.",
		Next: name + "'s sign-in runs in this terminal; Devpit comes back when it finishes.",
	}
	change := []choices.Item{
		off(choices.Item{
			ID: actHere, Label: "Use another account here",
			Desc: "Pick which " + name + " account this folder and every folder inside it use.",
			Next: nextPick,
		}, c.FolderRules),
		off(choices.Item{
			ID: actEverywhere, Label: "Use another account everywhere",
			Desc: "Pick which account " + name + " uses in every folder that has no choice of its own.",
			Next: nextPick,
		}, c.Everywhere),
		off(choices.Item{
			ID: actOnce, Label: "Use another account just once",
			Desc: "Run one command with another account. Nothing is saved and nothing else changes.",
			Next: "You pick an account and get the command to run.",
		}, c.JustOnce),
	}
	if ts.Resolution.Reason == accounts.ReasonFolderRule {
		change = append(change, choices.Item{
			ID: actRemoveRule, Label: "Forget this folder's choice",
			Desc: name + " uses " + ts.Account.Name + " here because you chose it for " + ts.Resolution.RuleFolder +
				" and the folders inside it. Forget that choice and this folder uses the account of the folder around it, or your usual one.",
			Next: nextPreview,
		})
	}
	signIn := []choices.Item{off(choices.Item{
		ID: actAdd, Label: "Sign in with another account",
		Desc: "Sign in to another " + name + " account and give it a short name, so you can use it in some folders.",
		Next: name + "'s own sign-in runs in this terminal, then you name the account.",
	}, c.AddAccount)}
	if canAgain && (expired || !ts.Account.IsDefault()) {
		signIn = append([]choices.Item{again}, signIn...)
	}
	groups := []choices.Group{{Title: "Change the account", Items: change}, {Title: "Sign in", Items: signIn}}
	if expired && canAgain {
		// Signing in again is the one thing to do: it comes first.
		groups[0], groups[1] = groups[1], groups[0]
	}
	switch m.tool {
	case accounts.ToolClaude:
		groups = append(groups, choices.Group{Title: "Claude Code setup", Items: []choices.Item{{
			ID: actClaude, Label: "Bring your Claude Code setup over",
			Desc: "Share or copy your skills, agents, commands, CLAUDE.md and settings into another Claude Code account. Login files are never copied.",
			Next: "You choose what comes over and how, then see a preview.",
		}}})
	case accounts.ToolGitHub:
		groups = append(groups, choices.Group{Title: "Pushes", Items: []choices.Item{{
			ID: actGit, Label: "Pushes and SSH keys",
			Desc: "See which GitHub account a push from this folder uses, or use an SSH key for it.",
			Next: "Opens the Git page.",
		}}})
	}
	return append(groups, manage)
}

// spec is what the page shows this frame.
func (m toolScreen) spec(ctx uictx.Context) choices.Spec {
	return choices.Spec{
		FactsTitle: "Right now",
		FactsAside: "in " + m.sess.folder,
		Facts:      m.facts(ctx),
		Groups:     m.groups(),
	}
}

// Update implements uictx.Screen.
func (m toolScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		m.list = m.list.Settle(ctx, m.spec(ctx))
		return m, nil
	}
	switch msg := msg.(type) {
	case capsMsg:
		if msg.tool == m.tool {
			m.caps, m.asked = msg.caps, true
			m.list = m.list.Settle(ctx, m.spec(ctx))
		}
		return m, nil
	case tea.KeyPressMsg:
		if key.Matches(msg, m.check) && m.status().Installed {
			return m, uictx.Push(newVerifyScreen(m.sess, m.depth+1))
		}
	}
	next, a := m.list.Update(msg, ctx, m.spec(ctx))
	m.list = next
	return m, m.act(a.ID)
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

// facts are what is true now: who, why, other folders, and anything that
// needs a look.
func (m toolScreen) facts(ctx uictx.Context) []choices.Fact {
	ts := m.status()
	name := m.tool.DisplayName()
	if !ts.Installed {
		why := name + " is not installed, so nothing is checked or changed for it."
		if ts.Managed {
			why += " The choices you made for it are kept and apply once it is installed."
		}
		return []choices.Fact{{Label: "Installed", Value: "no, not on this PC", Notes: []string{why}}}
	}
	first := "Signed in as"
	switch m.tool {
	case accounts.ToolGit:
		first = "Commits as"
	case accounts.ToolConvex:
		first = "Project"
	}
	who := choices.Fact{Label: first, Value: accountPlain(ts.Display, m.tool)}
	if m.tool == accounts.ToolConvex {
		who.Value = strings.TrimPrefix(ts.Display, "project: ")
	}
	switch ts.Identity.State() {
	case accounts.StateExpired:
		who.Tone = choices.Warn
		who.Value += ", but the sign-in has expired"
		who.Notes = append(who.Notes, "Sign in again (below) to use it.")
	case accounts.StateNotSignedIn:
		who.Tone = choices.Warn
		who.Value += ", not signed in"
	}
	if isNotChecked(ts.Display) {
		who.Notes = append(who.Notes, notCheckedNote(m.tool))
	}
	why := choices.Fact{Label: "Why this one", Value: wherePlain(ts.Resolution), Wrap: true}
	if m.tool == accounts.ToolConvex {
		why.Value = ts.Why
	}
	if c := chainCaption(ctx, ts.Resolution); c != "" {
		why.Notes = append(why.Notes, "Choices around this folder: "+c)
	}
	out := []choices.Fact{who, why}
	if m.tool != accounts.ToolConvex {
		out = append(out, choices.Fact{Label: "Other folders", Value: accountPlain(ts.EverywhereDisplay, m.tool)})
	}
	if n := m.note(); n != "" && !m.caps.ShowOnly {
		out = append(out, choices.Fact{Label: "Good to know", Value: n, Wrap: true})
	}
	if len(ts.Problems) > 0 {
		p := ts.Problems[0]
		f := choices.Fact{Label: "Needs a look", Value: p.Message, Tone: choices.Warn, Wrap: true}
		if p.Fix != "" {
			f.Notes = append(f.Notes, "Fix: "+p.Fix)
		}
		if l := p.Link(); l != "" {
			f.Notes = append(f.Notes, "Help: "+l)
		}
		out = append(out, f)
	}
	return out
}

// View implements uictx.Screen.
func (m toolScreen) View(ctx uictx.Context) string { return m.list.View(ctx, m.spec(ctx)) }

// note explains, in the adapter's own words, why some choices are off, or
// what is special about this tool.
func (m toolScreen) note() string {
	c := m.caps
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
