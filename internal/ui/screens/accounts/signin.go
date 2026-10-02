package accounts

import (
	"context"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Signing in is the one place Devpit gives the terminal away. Every tool's
// own sign-in is interactive: claude auth login and wrangler auth create
// open a browser and wait, gh auth login prints a one-time code and waits
// for Enter, vercel login, firebase login:add and supabase login ask
// questions. A full-screen program cannot answer for the person, so the
// screen explains what will happen first, then hands the terminal to the
// tool through Bubble Tea's exec (the program steps aside, the tool owns
// the window, Ctrl+C goes to the tool), and takes it back when the tool is
// done. What the engine reported along the way comes back as events and is
// shown as live rows, so the person sees what happened while they were in
// the browser.

// signInCommand is the tool's own sign-in, in the words the person sees on
// the intro card.
func signInCommand(t accounts.Tool, name string) string {
	switch t {
	case accounts.ToolClaude:
		return "claude auth login"
	case accounts.ToolGitHub:
		return "gh auth login --web"
	case accounts.ToolVercel:
		return "vercel login"
	case accounts.ToolFirebase:
		return "firebase login:add"
	case accounts.ToolSupabase:
		return "supabase login"
	case accounts.ToolCloudflare:
		return "wrangler auth create " + name
	}
	return t.Binary()
}

// signInWhere says where the tool's sign-in happens.
func signInWhere(t accounts.Tool) string {
	switch t {
	case accounts.ToolGitHub:
		return "It shows a one-time code and opens github.com in your browser; enter the code there."
	case accounts.ToolVercel, accounts.ToolSupabase:
		return "It may ask a question or two in this window, then opens your browser."
	case accounts.ToolFirebase:
		return "It opens your browser to sign in with a Google account."
	}
	return "It opens your browser. Sign in there with the account you want to add."
}

// signInResult is what a sign-in run left: every event the engine sent,
// and the error that stopped it before it began.
type signInResult struct {
	events []accounts.Event
	err    error
}

// signedInMsg is the sign-in coming back.
type signedInMsg struct {
	res signInResult
	err error
}

// signInExec is the sign-in as a tea.ExecCommand: the program releases the
// terminal, Run hands it to the engine's SignIn (which runs the tool with
// the terminal attached), and the events are collected for the screen.
type signInExec struct {
	ctx  context.Context
	svc  Service
	tool accounts.Tool
	req  adapters.LoginRequest
	in   io.Reader
	out  io.Writer
	errw io.Writer
	res  *signInResult
}

func (c *signInExec) SetStdin(r io.Reader)  { c.in = r }
func (c *signInExec) SetStdout(w io.Writer) { c.out = w }
func (c *signInExec) SetStderr(w io.Writer) { c.errw = w }

// Run runs the sign-in. It never fails itself: what went wrong is in the
// events, and the screen explains it.
func (c *signInExec) Run() error {
	if c.out != nil {
		_, _ = fmt.Fprintf(c.out, "\r\nDevpit › Accounts › Sign in to %s\r\n%s's own sign-in starts now (%s). %s\r\nPress Ctrl+C to stop. Devpit comes back when it is done.\r\n\r\n",
			c.tool.DisplayName(), c.tool.DisplayName(), signInCommand(c.tool, c.req.Name), signInWhere(c.tool))
	}
	req := c.req
	req.Stdin, req.Stdout, req.Stderr = c.in, c.out, c.errw
	ch, err := c.svc.SignIn(c.ctx, c.tool, req)
	if err != nil {
		c.res.err = err
		return nil //nolint:nilerr // the error travels in the result, which the screen explains
	}
	for ev := range ch {
		c.res.events = append(c.res.events, ev)
	}
	return nil
}

// signInCmd hands the terminal to the tool's sign-in for a new account
// called name (a placeholder when it is named afterwards).
func signInCmd(ctx context.Context, s *session, t accounts.Tool, name string) tea.Cmd {
	res := &signInResult{}
	c := &signInExec{ctx: ctx, svc: s.svc, tool: t, req: adapters.LoginRequest{Name: name}, res: res}
	return s.opts.Exec(c, func(err error) tea.Msg { return signedInMsg{res: *res, err: err} })
}

// --- Sign in again ---

// signAgainStage is where "Sign in again" is.
type signAgainStage int

const (
	againIntro signAgainStage = iota
	againDone
	againFailed
)

// signedAgainMsg is the tool's sign-in coming back.
type signedAgainMsg struct {
	code int
	err  error
}

// onceExec runs a prepared command on the terminal.
type onceExec struct {
	run  func(service.OnceCommand) (int, error)
	prep func() (service.OnceCommand, error)
	head string
	out  io.Writer
	code *int
	err  *error
}

func (c *onceExec) SetStdin(io.Reader)    {}
func (c *onceExec) SetStdout(w io.Writer) { c.out = w }
func (c *onceExec) SetStderr(io.Writer)   {}

// Run prepares the command (for GitHub that fetches nothing; the sign-in
// commands carry no token) and runs it on this terminal.
func (c *onceExec) Run() error {
	if c.out != nil {
		_, _ = fmt.Fprint(c.out, "\r\n"+c.head+"\r\nPress Ctrl+C to stop. Devpit comes back when it is done.\r\n\r\n")
	}
	oc, err := c.prep()
	if err != nil {
		*c.err = err
		return nil //nolint:nilerr // the error travels in the result, which the screen explains
	}
	*c.code, *c.err = c.run(oc)
	return nil
}

// signAgainScreen signs an account in again, in its own folder, through the
// tool's own sign-in run "just this once" for that account.
type signAgainScreen struct {
	sess  *session
	tool  accounts.Tool
	acct  accounts.Account
	depth int
	stage signAgainStage
	err   error
	code  int
	keys  struct{ Start, Verify, Back key.Binding }
}

func newSignAgainScreen(s *session, t accounts.Tool, acct accounts.Account, depth int) signAgainScreen {
	m := signAgainScreen{sess: s, tool: t, acct: acct, depth: depth}
	m.keys.Start = bind("sign in", "enter")
	m.keys.Verify = bind("check who is signed in", "v")
	m.keys.Back = bind("back", "enter")
	return m
}

func (m signAgainScreen) Init() tea.Cmd { return nil }

func (m signAgainScreen) Title() string { return m.tool.DisplayName() + " › Sign in again" }

func (m signAgainScreen) ShortHelp() []key.Binding {
	switch m.stage {
	case againDone:
		return []key.Binding{m.keys.Verify, m.keys.Back}
	case againFailed:
		return []key.Binding{m.keys.Back}
	}
	return []key.Binding{m.keys.Start}
}

func (m signAgainScreen) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

func (m signAgainScreen) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case signedAgainMsg:
		m.code, m.err = msg.code, msg.err
		m.stage = againDone
		if msg.err != nil {
			m.stage = againFailed
		}
		return m, m.sess.refreshCmd(m.sess.folder, m.tool)
	case tea.KeyPressMsg:
		switch m.stage {
		case againIntro:
			if key.Matches(msg, m.keys.Start) {
				return m, m.start()
			}
		case againDone:
			switch {
			case key.Matches(msg, m.keys.Verify):
				return m, uictx.Push(newVerifyScreen(m.sess, m.depth+1))
			case key.Matches(msg, m.keys.Back):
				return m, uictx.Pop()
			}
		case againFailed:
			if key.Matches(msg, m.keys.Back) {
				return m, uictx.Pop()
			}
		}
	}
	return m, nil
}

// start hands the terminal to the tool's own sign-in for this account.
func (m signAgainScreen) start() tea.Cmd {
	svc, t, name := m.sess.svc, m.tool, m.acct.Name
	code, errp := new(int), new(error)
	c := &onceExec{
		run: m.sess.opts.RunOnce,
		prep: func() (service.OnceCommand, error) {
			ctx, cancel := engineCtx()
			defer cancel()
			return svc.PrepareSignInAgain(ctx, t, name)
		},
		head: fmt.Sprintf("Devpit › Accounts › Sign %s in again (%s)", name, strings.Join(service.SignInAgainArgs(t), " ")),
		code: code, err: errp,
	}
	return m.sess.opts.Exec(c, func(err error) tea.Msg {
		if err == nil {
			err = *errp
		}
		return signedAgainMsg{code: *code, err: err}
	})
}

func (m signAgainScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	out := []string{heading(ctx, "Sign "+m.acct.Name+" in again", ""), ""}
	switch m.stage {
	case againIntro:
		out = append(out, wrap(ctx, th.Base, fmt.Sprintf("Devpit hands this terminal to %s's own sign-in for %s, in that account's own folder:", m.tool.DisplayName(), m.acct.Name), 1, 0)...)
		out = append(out, "   "+th.Info.Render(strings.Join(service.SignInAgainArgs(m.tool), " ")), "")
		out = append(out, wrap(ctx, th.Base, "It may open your browser: sign in there with the account "+m.acct.Name+" is for. Devpit comes back here when it is done. Nothing else changes.", 1, 0)...)
		out = append(out, "", keyHints(ctx, "enter", "sign in", "esc", "back"))
	case againDone:
		word := "finished"
		if m.code != 0 {
			word = fmt.Sprintf("ended with exit code %d", m.code)
		}
		out = append(out, " "+th.Success.Render(ctx.Icons.Tick)+" "+th.Base.Render(m.tool.DisplayName()+"'s sign-in "+word+"."), "")
		out = append(out, wrap(ctx, th.Muted, "Devpit does not ask who is signed in by itself. Press v to check now.", 1, 0)...)
	case againFailed:
		out = append(out, errorView(ctx, m.err))
	}
	return strings.Join(out, "\n")
}
