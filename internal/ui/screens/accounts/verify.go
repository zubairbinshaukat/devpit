package accounts

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Verify asks the tools themselves who is signed in and compares that with
// what the rules say. It is honest about which checks are safe: the account
// active in this folder is checked for every tool; an idle account is
// checked only when the person asks, and for a tool whose check can harm an
// idle account (Claude Code: it can sign one out) the screen shows the
// adapter's own reason and asks first, with No as the answer it starts on.

// verifiedMsg is a verify report.
type verifiedMsg struct {
	rep service.VerifyReport
	err error
	all bool
}

// verifyStage is where verify is.
type verifyStage int

const (
	vChecking verifyStage = iota
	vResults
	vAskRisky
)

// verifyScreen is the verify screen.
type verifyScreen struct {
	sess   *session
	depth  int
	stage  verifyStage
	frame  int
	rep    service.VerifyReport
	err    error
	all    bool
	cursor int
	ask    confirm.Model
	risky  []string
	keys   struct{ Up, Down, Fix, All, Again, Back key.Binding }
}

func newVerifyScreen(s *session, depth int) verifyScreen {
	m := verifyScreen{sess: s, depth: depth}
	m.keys.Up = bind("move", "up", "k")
	m.keys.Down = bind("down", "down", "j")
	m.keys.Fix = bind("fix", "f")
	m.keys.All = bind("check idle accounts", "a")
	m.keys.Again = bind("check again", "r")
	m.keys.Back = bind("back", "enter")
	m.keys.Up.SetHelp("↑↓", "move")
	return m
}

// Init implements uictx.Screen: check every tool's active account now.
func (m verifyScreen) Init() tea.Cmd { return tea.Batch(m.verifyCmd(false, false), m.sess.spin()) }

// verifyCmd runs Verify. risky allows the live checks that can harm an
// idle account; only ever true after the person said yes.
func (m verifyScreen) verifyCmd(all, risky bool) tea.Cmd {
	svc, folder := m.sess.svc, m.sess.folder
	return func() tea.Msg {
		ctx, cancel := engineCtx()
		defer cancel()
		rep, err := svc.Verify(ctx, service.VerifyOptions{Folder: folder, All: all, Risky: risky})
		return verifiedMsg{rep: rep, err: err, all: all}
	}
}

func (m verifyScreen) Title() string { return "Verify" }

// Busy implements uictx.BusyReporter: the checks finish first.
func (m verifyScreen) Busy() bool { return false }

func (m verifyScreen) ShortHelp() []key.Binding {
	switch m.stage {
	case vAskRisky:
		return m.ask.Keys.ShortHelp()
	case vResults:
		out := []key.Binding{m.keys.Up}
		if m.fixable(m.cursor) {
			out = append(out, m.keys.Fix)
		}
		if !m.all {
			out = append(out, m.keys.All)
		}
		return append(out, m.keys.Again, m.keys.Back)
	}
	return nil
}

func (m verifyScreen) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.keys.Up, m.keys.Down, m.keys.Fix}, {m.keys.All, m.keys.Again, m.keys.Back}}
}

func (m verifyScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case spinMsg:
		if m.stage == vChecking {
			m.frame++
			return m, m.sess.spin()
		}
	case verifiedMsg:
		m.stage, m.rep, m.err, m.all = vResults, msg.rep, msg.err, msg.all
		if msg.err == nil {
			m.remember(msg.rep)
		}
		m.cursor = min(m.cursor, max(0, len(m.rep.Checks)-1))
		return m, nil
	case confirm.AnsweredMsg:
		if m.stage != vAskRisky {
			return m, nil
		}
		m.stage = vChecking
		return m, tea.Batch(m.verifyCmd(true, msg.Answer == confirm.AnswerYes), m.sess.spin())
	case tea.KeyPressMsg:
		return m.onKey(msg)
	case tea.MouseClickMsg:
		if m.stage == vAskRisky && msg.Button == tea.MouseLeft {
			next, cmd := m.ask.Click(ctx, msg.X, ctx.BodyRow(msg.Y)-m.askTop(ctx))
			m.ask = next
			return m, cmd
		}
		if m.stage == vResults && msg.Button == tea.MouseLeft {
			if i := ctx.BodyRow(msg.Y) - 3; i >= 0 && i < len(m.rep.Checks) {
				if i == m.cursor && m.fixable(i) {
					return m, m.fix(i)
				}
				m.cursor = i
			}
		}
	case tea.MouseWheelMsg:
		if m.stage == vResults {
			if msg.Button == tea.MouseWheelDown {
				m.cursor = min(m.cursor+1, max(0, len(m.rep.Checks)-1))
			} else {
				m.cursor = max(0, m.cursor-1)
			}
		}
	}
	return m, nil
}

// remember keeps the checks of the accounts active here, so the page's
// rows show what verify found (an expired login, a mismatch).
func (m verifyScreen) remember(rep service.VerifyReport) {
	if m.sess.checks == nil {
		m.sess.checks = map[accounts.Tool]service.VerifyCheck{}
	}
	for _, c := range rep.Checks {
		if c.Here {
			m.sess.checks[c.Tool] = c
		}
	}
}

func (m verifyScreen) onKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd) {
	switch m.stage {
	case vAskRisky:
		next, cmd := m.ask.Update(msg)
		m.ask = next
		return m, cmd
	case vResults:
		switch {
		case key.Matches(msg, m.keys.Up):
			m.cursor = max(0, m.cursor-1)
		case key.Matches(msg, m.keys.Down):
			m.cursor = min(m.cursor+1, max(0, len(m.rep.Checks)-1))
		case key.Matches(msg, m.keys.Fix) && m.fixable(m.cursor):
			return m, m.fix(m.cursor)
		case key.Matches(msg, m.keys.All) && !m.all:
			return m.checkAll()
		case key.Matches(msg, m.keys.Again):
			m.stage = vChecking
			return m, tea.Batch(m.verifyCmd(m.all, false), m.sess.spin())
		case key.Matches(msg, m.keys.Back):
			return m, uictx.Pop()
		}
	}
	return m, nil
}

// checkAll checks the idle accounts too, asking first when a tool's check
// can harm an idle account.
func (m verifyScreen) checkAll() (uictx.Screen, tea.Cmd) {
	var reasons []string
	m.risky = nil
	for _, t := range accounts.Tools() {
		if risky, why := m.sess.svc.LiveCheckRisk(t); risky {
			m.risky = append(m.risky, t.DisplayName())
			reasons = append(reasons, why)
		}
	}
	if len(reasons) == 0 {
		m.stage = vChecking
		return m, tea.Batch(m.verifyCmd(true, false), m.sess.spin())
	}
	m.stage = vAskRisky
	m.ask = confirm.New("risky", "Also ask "+strings.Join(m.risky, ", ")+" about its idle accounts?",
		strings.Join(reasons, " ")+" No checks the other tools only.")
	return m, nil
}

// fixable reports whether check i has a one-key fix.
func (m verifyScreen) fixable(i int) bool {
	if i < 0 || i >= len(m.rep.Checks) {
		return false
	}
	c := m.rep.Checks[i]
	return c.Status == service.VerifyMismatch || c.Status == service.VerifyError || len(m.problemsOf(c.Tool)) > 0
}

// fix opens what puts check i right: signing the account in again when its
// login ran out, the Git page for Git, the tool's page otherwise (where the
// problem is shown with its fix).
func (m verifyScreen) fix(i int) tea.Cmd {
	c := m.rep.Checks[i]
	d := m.depth + 1
	if c.Actual != nil && (c.Actual.State() == accounts.StateExpired || c.Actual.State() == accounts.StateNotSignedIn) &&
		service.SignInAgainArgs(c.Tool) != nil {
		return uictx.Push(newSignAgainScreen(m.sess, c.Tool, accounts.Account{Tool: c.Tool, Name: c.Account}, d))
	}
	if c.Tool == accounts.ToolGit {
		return uictx.Push(newGitScreen(m.sess, d))
	}
	return uictx.Push(newToolScreen(m.sess, c.Tool, d))
}

// problemsOf are the problems verify found for tool.
func (m verifyScreen) problemsOf(t accounts.Tool) []service.Problem {
	var out []service.Problem
	for _, p := range m.rep.Problems {
		if p.Tool == string(t) {
			out = append(out, p)
		}
	}
	return out
}

func (m verifyScreen) askTop(uictx.Context) int { return 2 }

func (m verifyScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	out := []string{heading(ctx, "Who each tool says is signed in", "in "+m.sess.folder), ""}
	switch {
	case m.stage == vChecking:
		out = append(out, " "+th.Muted.Render(ctx.SpinnerFrame(m.frame)+" Asking each tool"+ellipsis(ctx)))
		out = append(out, "")
		out = append(out, wrap(ctx, th.Muted, "Only the account active in this folder is checked for each tool. Devpit never asks on its own.", 1, 0)...)
		return strings.Join(out, "\n")
	case m.err != nil:
		return strings.Join(append(out, errorView(ctx, m.err)), "\n")
	case m.stage == vAskRisky:
		out = append(out, m.ask.View(ctx))
		return strings.Join(out, "\n")
	}
	out = append(out, m.tableHead(ctx))
	for i, c := range m.rep.Checks {
		out = append(out, m.row(ctx, c, i == m.cursor))
	}
	out = append(out, "")
	out = append(out, m.details(ctx)...)
	return strings.Join(out, "\n")
}

// Column widths of the verify table.
func (m verifyScreen) cols(ctx uictx.Context) (toolW, expW int) {
	toolW = 12
	expW = max(16, min(34, (ctx.Width-toolW-8)/2))
	return toolW, expW
}

func (m verifyScreen) tableHead(ctx uictx.Context) string {
	toolW, expW := m.cols(ctx)
	return ctx.Theme.Muted.Render("     " + padTo("TOOL", toolW) + padTo("EXPECTED", expW+2) + "TOOL SAYS")
}

// row draws one check: a shape, the tool, who is expected, who the tool
// says it is.
func (m verifyScreen) row(ctx uictx.Context, c service.VerifyCheck, selected bool) string {
	th, ic := ctx.Theme, ctx.Icons
	toolW, expW := m.cols(ctx)
	mark, st, word := ic.Tick, th.Success, ""
	switch c.Status {
	case service.VerifyMismatch:
		mark, st, word = ic.Fail, th.Danger, "mismatch"
	case service.VerifyError:
		mark, st, word = ic.Warn, th.Warning, "could not check"
	case service.VerifyNotInstalled:
		mark, st, word = ic.Absent, th.Muted, "not installed"
	case service.VerifyInfo:
		mark, st = ic.Queued, th.Muted
	}
	exp := c.Expected
	if !c.Here {
		exp += " (idle)"
	}
	says := c.ActualDisplay
	if c.Status == service.VerifyNotInstalled {
		says = ""
	}
	if word != "" && c.Status != service.VerifyNotInstalled {
		says = word + sep(ctx) + says
	} else if word != "" {
		says = word
	}
	lead := "   "
	if selected {
		lead = " " + th.Cursor.Render(ic.Cursor) + " "
	}
	rest := ctx.Width - 5 - toolW - expW - 2
	line := lead + st.Render(mark) + " " + th.Base.Render(padTo(fit(ctx, c.Tool.DisplayName(), toolW-1), toolW)) +
		th.Base.Render(padTo(fit(ctx, exp, expW), expW+2)) + st.Render(fit(ctx, says, rest))
	if selected {
		return th.SelBand.Render(ansi.Strip(line))
	}
	return line
}

// details are the notes of the selected check, its problems with their
// fixes, and what was not checked and why.
func (m verifyScreen) details(ctx uictx.Context) []string {
	th := ctx.Theme
	var out []string
	for _, s := range m.rep.Skipped {
		// The engine's sentence ends with how to do it from the command
		// line; here, a key does it.
		if i := strings.Index(s, " To check them anyway"); i > 0 {
			s = s[:i]
		}
		out = append(out, wrap(ctx, th.Muted, ctx.Icons.Queued+" "+s, 1, 2)...)
	}
	if m.cursor < len(m.rep.Checks) {
		c := m.rep.Checks[m.cursor]
		for _, n := range c.Notes {
			out = append(out, wrap(ctx, th.Muted, n, 1, 2)...)
		}
		if c.Docs != "" {
			out = append(out, docsLine(ctx, service.Problem{Docs: c.Docs}, 1)...)
		}
		for _, p := range m.problemsOf(c.Tool) {
			out = append(out, wrap(ctx, th.Warning, ctx.Icons.Warn+" "+p.Message, 1, 2)...)
			if p.Fix != "" {
				out = append(out, wrap(ctx, th.Muted, "Fix: "+p.Fix, 3, 5)...)
			}
			out = append(out, docsLine(ctx, p, 3)...)
		}
		if m.fixable(m.cursor) {
			out = append(out, " "+ctx.KeyHint("f", "fix this on the tool's page"))
		}
	}
	if !m.all {
		out = append(out, "", " "+ctx.KeyHint("a", "also check the accounts not used here"))
	}
	verdict := " " + th.Success.Render(ctx.Icons.Tick+" Everything checked is as it should be.")
	if m.rep.Mismatch {
		verdict = " " + th.Danger.Render(ctx.Icons.Fail+" Something is not as it should be. Pick a row marked "+ctx.Icons.Fail+" and press f.")
	}
	room := ctx.BodyHeight - 4 - len(m.rep.Checks) - 2
	if len(out) > room {
		out = out[:max(0, room)]
	}
	return append([]string{verdict, ""}, out...)
}

// padTo pads plain text to w cells.
func padTo(s string, w int) string {
	if n := ansi.StringWidth(s); n < w {
		return s + pad(w-n)
	}
	return s
}
