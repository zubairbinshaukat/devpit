package adapters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

const (
	authHelp = `Usage: claude auth [options] [command]

Manage authentication

Commands:
  login [options]   Sign in to your Anthropic account
  logout            Log out from your Anthropic account
  status [options]  Show authentication status
`
	oldMainHelp = `Usage: claude [options] [command] [prompt]

Claude Code - starts an interactive session by default

Commands:
  config            Manage configuration
  mcp               Configure and manage MCP servers
`
	signedIn  = `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"zubair@work.com","orgId":"0f0e4b1c-1111-2222-3333-444455556666","orgName":"Work Org","subscriptionType":"max"}`
	signedOut = `{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty","configDirectory":"C:\\x"}`
)

type env struct {
	deps   Deps
	fake   *FakeRunner
	claude *claudeAdapter
	logs   *[]string
}

func newEnv(t *testing.T) env {
	t.Helper()
	fake := &FakeRunner{}
	fake.Set(FakeResponse{Stdout: "2.1.287 (Claude Code)\n"}, "claude", "--version")
	fake.Set(FakeResponse{Stdout: authHelp}, "claude", "auth", "--help")
	logs := &[]string{}
	d := Deps{
		Runner: fake,
		Paths:  accounts.PathsIn(t.TempDir(), t.TempDir()),
		Home:   t.TempDir(),
		Now:    func() time.Time { return time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC) },
		Log:    func(s string) { *logs = append(*logs, s) },
	}
	return env{deps: d, fake: fake, claude: newClaude(d).(*claudeAdapter), logs: logs}
}

func collect(t *testing.T, ch <-chan accounts.Event) []accounts.Event {
	t.Helper()
	var out []accounts.Event
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timeout:
			t.Fatal("the event channel never closed")
		}
	}
}

func last(evs []accounts.Event) accounts.Event { return evs[len(evs)-1] }

func TestClaudeInstalledAndCapabilities(t *testing.T) {
	e := newEnv(t)
	in := e.claude.Installed(context.Background())
	if !in.Found || in.Version != "2.1.287" {
		t.Fatalf("installed = %+v", in)
	}
	c := e.claude.Capabilities(context.Background())
	if !c.FolderRules || !c.Everywhere || !c.JustOnce || !c.AddAccount || c.Why != "" {
		t.Fatalf("caps = %+v", c)
	}
}

func TestClaudeMissing(t *testing.T) {
	e := newEnv(t)
	e.fake.Missing = map[string]bool{"claude": true}
	if in := e.claude.Installed(context.Background()); in.Found {
		t.Fatalf("installed = %+v", in)
	}
	if c := e.claude.Capabilities(context.Background()); c.FolderRules || c.Why != "Claude Code is not installed." {
		t.Fatalf("caps = %+v", c)
	}
	id, err := e.claude.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolClaude, Name: "default"})
	if !errors.Is(err, accounts.ErrNotFoundTool) || id.State() != accounts.StateNotInstalled {
		t.Fatalf("who = %v, %v", id.State(), err)
	}
}

func TestClaudeTooOldSaysWhichVersionIsNeeded(t *testing.T) {
	e := newEnv(t)
	e.fake.Set(FakeResponse{Stdout: "1.0.40 (Claude Code)\n"}, "claude", "--version")
	e.fake.Set(FakeResponse{Stdout: oldMainHelp}, "claude", "auth", "--help")
	c := e.claude.Capabilities(context.Background())
	if c.FolderRules || !strings.Contains(c.Why, "1.0.40") || !strings.Contains(c.Why, "claude update") {
		t.Fatalf("caps = %+v", c)
	}
	_, err := e.claude.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolClaude, Name: "default"})
	if !errors.Is(err, accounts.ErrTooOld) {
		t.Fatalf("err = %v", err)
	}
	// The blind `claude auth status` that an old version might take as a
	// prompt is never run.
	for _, c := range e.fake.Calls() {
		if Key(c.Name, c.Args...) == "claude auth status --json" {
			t.Fatal("ran auth status on a version without it")
		}
	}
}

func TestClaudeWhoAmI(t *testing.T) {
	e := newEnv(t)
	work := filepath.Join(e.deps.Paths.AccountsDir, "claude", "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	acct := accounts.Account{Tool: accounts.ToolClaude, Name: "work", Dir: work}

	e.fake.Set(FakeResponse{Stdout: signedIn}, "claude", "auth", "status", "--json")
	id, err := e.claude.WhoAmI(context.Background(), acct)
	if err != nil || !id.SignedIn() || id.Email() != "zubair@work.com" || id.Fields().Org != "Work Org" || id.Fields().Plan != "max" {
		t.Fatalf("signed in: %+v %v", id.Fields(), err)
	}
	calls := e.fake.Calls()
	if v, set, _ := EnvOf(calls[len(calls)-1], "CLAUDE_CONFIG_DIR"); !set || v != work {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q (set %v)", v, set)
	}

	// Default: the variable is removed, so a Devpit started from a shimmed
	// shell still asks about the real default.
	if _, err = e.claude.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolClaude, Name: "default"}); err != nil {
		t.Fatal(err)
	}
	calls = e.fake.Calls()
	if _, set, unset := EnvOf(calls[len(calls)-1], "CLAUDE_CONFIG_DIR"); set || !unset {
		t.Fatal("default must run with CLAUDE_CONFIG_DIR removed")
	}

	e.fake.Set(FakeResponse{Stdout: signedOut, Exit: 1}, "claude", "auth", "status", "--json")
	id, err = e.claude.WhoAmI(context.Background(), acct)
	if err != nil || id.State() != accounts.StateNotSignedIn {
		t.Fatalf("signed out: %v %v", id.State(), err)
	}

	// Signed out but the credentials file is still there: expired. The file
	// holds a planted token; it must never be read.
	tok := "sk-ant-oat01-" + strings.Repeat("PLANTEDcred", 6)
	if err = os.WriteFile(filepath.Join(work, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"`+tok+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err = e.claude.WhoAmI(context.Background(), acct)
	if err != nil || id.State() != accounts.StateExpired || !strings.Contains(id.Fields().Note, "Sign in again") {
		t.Fatalf("expired: %+v %v", id.Fields(), err)
	}
	if strings.Contains(fmt.Sprintf("%+v", id.Fields()), "PLANTEDcred") {
		t.Fatal("the credentials file was read")
	}

	// A folder that is gone: claude is not run (it would make a new one).
	before := len(e.fake.Calls())
	gone := accounts.Account{Tool: accounts.ToolClaude, Name: "gone", Dir: filepath.Join(work, "..", "gone")}
	id, err = e.claude.WhoAmI(context.Background(), gone)
	if err == nil || !strings.Contains(id.Fields().Note, "missing") || len(e.fake.Calls()) != before {
		t.Fatalf("missing folder: %+v %v", id.Fields(), err)
	}

	// Output Devpit does not understand: a plain error, scrubbed.
	e.fake.Set(FakeResponse{Stdout: "Something odd\n", Exit: 2}, "claude", "auth", "status", "--json")
	if _, err := e.claude.WhoAmI(context.Background(), acct); err == nil || !strings.Contains(err.Error(), "Something odd") {
		t.Fatalf("odd output: %v", err)
	}
}

func TestClaudeLoginSucceeds(t *testing.T) {
	e := newEnv(t)
	store := accounts.NewStore()
	if err := store.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "other", Email: "zubair@work.com", Dir: `C:\x`}); err != nil {
		t.Fatal(err)
	}
	e.fake.Set(FakeResponse{Stdout: "Opening browser to sign in…\nLogin successful.\n"}, "claude", "auth", "login")
	e.fake.Set(FakeResponse{Stdout: signedIn}, "claude", "auth", "status", "--json")

	ch, err := e.claude.Login(context.Background(), LoginRequest{Name: "work", Store: store})
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, ch)
	fin := last(evs)
	if !fin.Final || fin.State != accounts.StepDone || fin.Account == nil {
		t.Fatalf("final = %+v", fin)
	}
	want := filepath.Join(e.deps.Paths.AccountsDir, "claude", "work")
	if fin.Account.Dir != want || fin.Account.Email != "zubair@work.com" || fin.Account.Name != "work" || fin.Account.Added.IsZero() {
		t.Fatalf("account = %+v", fin.Account)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Fatal("the account folder must be kept after a sign-in")
	}
	var warned, waited bool
	for _, ev := range evs {
		if ev.State == accounts.StepWarning && strings.Contains(ev.Detail, "other") {
			warned = true
		}
		if ev.State == accounts.StepWaiting {
			waited = true
		}
	}
	if !warned || !waited {
		t.Fatalf("events = %+v", evs)
	}
	// The login ran with the new folder set.
	for _, c := range e.fake.Calls() {
		if Key(c.Name, c.Args...) == "claude auth login" {
			if v, set, _ := EnvOf(c, "CLAUDE_CONFIG_DIR"); !set || v != want {
				t.Fatalf("login env = %q", v)
			}
		}
	}
}

func TestClaudeLoginCancelledLeavesNothingBehind(t *testing.T) {
	for _, tc := range []struct {
		name  string
		login FakeResponse
	}{
		{"browser closed", FakeResponse{Stdout: "Login cancelled\n", Exit: 1}},
		{"tool crashed", FakeResponse{Err: errors.New("boom")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			dir := filepath.Join(e.deps.Paths.AccountsDir, "claude", "work")
			login := tc.login
			login.Do = func(Cmd) { // the tool writes into the folder before giving up
				_ = os.WriteFile(filepath.Join(dir, ".claude.json"), []byte("{}"), 0o600)
			}
			e.fake.Set(login, "claude", "auth", "login")
			e.fake.Set(FakeResponse{Stdout: signedOut, Exit: 1}, "claude", "auth", "status", "--json")
			ch, err := e.claude.Login(context.Background(), LoginRequest{Name: "work"})
			if err != nil {
				t.Fatal(err)
			}
			fin := last(collect(t, ch))
			if fin.State != accounts.StepFailed || fin.Account != nil {
				t.Fatalf("final = %+v", fin)
			}
			if tc.name == "browser closed" && !errors.Is(fin.Err, accounts.ErrSignInCancelled) {
				t.Fatalf("err = %v", fin.Err)
			}
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("a cancelled sign-in left its folder behind")
			}
		})
	}
}

func TestClaudeLoginStoppedByContext(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	e.fake.Set(FakeResponse{Do: func(Cmd) { cancel() }, Err: context.Canceled}, "claude", "auth", "login")
	ch, err := e.claude.Login(ctx, LoginRequest{Name: "work"})
	if err != nil {
		t.Fatal(err)
	}
	fin := last(collect(t, ch))
	if !errors.Is(fin.Err, accounts.ErrSignInCancelled) {
		t.Fatalf("final = %+v", fin)
	}
	if _, err := os.Stat(filepath.Join(e.deps.Paths.AccountsDir, "claude", "work")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("folder left behind")
	}
}

func TestClaudeLoginRefusesBadNamesAndExistingFolders(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"default", "bad name", ""} {
		if _, err := e.claude.Login(context.Background(), LoginRequest{Name: name}); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	store := accounts.NewStore()
	_ = store.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "work", Dir: `C:\x`})
	if _, err := e.claude.Login(context.Background(), LoginRequest{Name: "WORK", Store: store}); !errors.Is(err, accounts.ErrNameTaken) {
		t.Errorf("taken name: %v", err)
	}
	dir := filepath.Join(e.deps.Paths.AccountsDir, "claude", "old")
	_ = os.MkdirAll(dir, 0o700)
	if _, err := e.claude.Login(context.Background(), LoginRequest{Name: "old"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("existing folder: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("an existing folder must never be touched")
	}
}

func TestClaudeLaunch(t *testing.T) {
	// The default account clears a CLAUDE_CONFIG_DIR left by something
	// else; the shim only asks this when Devpit manages Claude Code.
	l, err := LaunchFor(accounts.Account{Tool: accounts.ToolClaude, Name: "default"})
	if err != nil || len(l.Env) != 0 || len(l.Args) != 0 || len(l.Unset) != 1 || l.Unset[0] != "CLAUDE_CONFIG_DIR" {
		t.Fatalf("default: %+v %v", l, err)
	}
	l, err = LaunchFor(accounts.Account{Tool: accounts.ToolClaude, Name: "work", Dir: `C:\a\work`})
	if err != nil || len(l.Env) != 1 || l.Env[0] != `CLAUDE_CONFIG_DIR=C:\a\work` || len(l.Args) != 0 {
		t.Fatalf("work: %+v %v", l, err)
	}
	if _, err := LaunchFor(accounts.Account{Tool: accounts.ToolClaude, Name: "nodir"}); err == nil {
		t.Fatal("an account with no folder must not launch")
	}
}

func TestClaudePlanAndApplyEndToEnd(t *testing.T) {
	e := newEnv(t)
	eng := accounts.NewEngine(e.deps.Paths)
	work := filepath.Join(e.deps.Paths.AccountsDir, "claude", "work")
	_ = os.MkdirAll(work, 0o700)
	if _, err := eng.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "work", Email: "zubair@work.com", Dir: work}); err != nil {
		t.Fatal(err)
	}
	res, err := eng.Load()
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.claude.Plan(accounts.PreviewInput{Store: res.Store, Change: accounts.Change{
		Tool: accounts.ToolClaude, Account: "wo", Scope: accounts.FolderScope(`C:\Work`),
	}, Defaults: map[accounts.Tool]string{accounts.ToolClaude: "zubair@gmail.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Sentences[0] != `In C:\Work and every folder inside it, Claude Code will use work (zubair@work.com).` {
		t.Fatalf("preview = %q", p.Sentences)
	}
	evs := collect(t, Apply(context.Background(), eng, e.claude, p))
	fin := last(evs)
	if fin.State != accounts.StepDone || fin.EntryID == "" {
		t.Fatalf("final = %+v", fin)
	}
	s, _ := accounts.ReadFile(e.deps.Paths.Store)
	r, _ := accounts.Resolve(s, accounts.ToolClaude, `C:\Work\api`, accounts.ResolveOptions{})
	if r.Account.Name != "work" {
		t.Fatalf("resolved %v", r)
	}
	// Applying the same preview again is stale.
	fin = last(collect(t, Apply(context.Background(), eng, e.claude, p)))
	if fin.State != accounts.StepFailed || !errors.Is(fin.Err, accounts.ErrStalePreview) {
		t.Fatalf("stale: %+v", fin)
	}
	if _, err = eng.Undo(); err != nil {
		t.Fatal(err)
	}

	// A missing account folder is a warning in the preview, not a block.
	_ = os.RemoveAll(work)
	res, _ = eng.Load()
	p, err = e.claude.Plan(accounts.PreviewInput{Store: res.Store, Change: accounts.Change{Tool: accounts.ToolClaude, Account: "work", Scope: accounts.EverywhereScope()}})
	if err != nil || len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "missing") {
		t.Fatalf("plan: %+v %v", p.Warnings, err)
	}
}

// TestNoTokenEverLeavesAnAdapter is the plan's section 9 test: fake tools
// print a planted token everywhere they can, and it must not appear in any
// event, log line, identity, JSON or error.
func TestNoTokenEverLeavesAnAdapter(t *testing.T) {
	tokens := []string{
		"sk-ant-oat01-PLANTEDtoken" + strings.Repeat("Ab3", 20),
		"ghp_PLANTEDtoken" + strings.Repeat("Zz9", 10),
		"eyJPLANTEDtoken0123456789abc.eyJzdWIiOiIxMjM0NTY3ODkwIn0.PLANTEDsig0123456789",
	}
	e := newEnv(t)
	planted := strings.Join(tokens, " ")
	status := `{"loggedIn":true,"email":"zubair@work.com","orgName":"Org ` + tokens[1] + `","accessToken":"` + tokens[0] + `","subscriptionType":"max"}`
	e.fake.Set(FakeResponse{Stdout: "Visit https://claude.ai/oauth?code=" + tokens[0] + "\nAuthorization: Bearer " + tokens[1] + "\n", Stderr: "debug: token=" + tokens[2] + "\n" + planted}, "claude", "auth", "login")
	e.fake.Set(FakeResponse{Stdout: status, Stderr: planted}, "claude", "auth", "status", "--json")

	var seen []string
	ch, err := e.claude.Login(context.Background(), LoginRequest{Name: "work"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range collect(t, ch) {
		seen = append(seen, fmt.Sprintf("%+v", ev), ev.Step, ev.Detail)
		if ev.Err != nil {
			seen = append(seen, ev.Err.Error())
		}
		if ev.Identity != nil {
			b, _ := ev.Identity.MarshalJSON()
			seen = append(seen, string(b), fmt.Sprintf("%+v", ev.Identity.Fields()))
		}
		if ev.Account != nil {
			seen = append(seen, fmt.Sprintf("%+v", *ev.Account))
		}
	}
	id, err := e.claude.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolClaude, Name: "default"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := id.MarshalJSON()
	seen = append(seen, string(b), fmt.Sprintf("%+v %v", id.Fields(), id))

	// A failing who-am-I whose output is all token.
	e.fake.Set(FakeResponse{Stdout: tokens[1] + "\n", Exit: 3}, "claude", "auth", "status", "--json")
	_, err = e.claude.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolClaude, Name: "default"})
	if err == nil {
		t.Fatal("garbage output must be an error")
	}
	seen = append(seen, err.Error())
	seen = append(seen, *e.logs...)

	// The planted output did reach the events, scrubbed: the test looks at
	// something real.
	if !strings.Contains(strings.Join(seen, "\n"), accounts.Hidden) {
		t.Fatal("no scrubbed output reached the events; the test is not exercising anything")
	}
	for _, s := range seen {
		for _, tok := range tokens {
			if strings.Contains(s, "PLANTED") {
				t.Fatalf("a planted token leaked (%s…): %q", tok[:12], s)
			}
		}
	}
}

// TestOverviewRunsNoTool pins the fix for Claude Code issue #95822: filling
// the Accounts page must not run `claude` (or any tool) at all, because a
// short-lived claude command can spend an idle account's refresh token.
func TestOverviewRunsNoTool(t *testing.T) {
	e := newEnv(t)
	ran := 0
	e.fake.Func = func(c Cmd) (Result, error) { ran++; return Result{}, nil }
	e.fake.Responses = nil
	d := e.deps
	d.LookPath = func(name string) (string, error) {
		if name == "claude" || name == "gh" {
			return `C:\bin\` + name + ".exe", nil
		}
		return "", accounts.ErrNotFoundTool
	}
	d.Getenv = func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return `C:\Users\z\.claude-acc\work`
		}
		return ""
	}
	s := accounts.NewStore()
	_ = s.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "work", Email: "zubair@work.com", Org: "Work Org", Dir: `C:\a\work`})
	_ = s.SetRule(`C:\Work`, accounts.ToolClaude, "work")
	_ = s.SetDefaultEmail(accounts.ToolClaude, "zubair@gmail.com")

	rows, err := Overview(d, s, `C:\Work\api`, accounts.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ran != 0 || len(e.fake.Calls()) != 0 {
		t.Fatalf("the overview ran %d command(s): %+v", ran, e.fake.Calls())
	}
	if len(rows) != 8 || rows[0].Tool != accounts.ToolClaude {
		t.Fatalf("rows = %+v", rows)
	}
	c := rows[0]
	if c.Resolution.Account.Name != "work" || c.Identity.State() != accounts.StateRecorded ||
		c.Identity.Email() != "zubair@work.com" || c.Identity.Fields().Org != "Work Org" || !c.Installed {
		t.Fatalf("claude row = %+v %+v", c, c.Identity.Fields())
	}
	if !strings.Contains(c.LiveRisk, "#95822") {
		t.Fatalf("live risk = %q", c.LiveRisk)
	}
	if len(c.Problems) != 1 || c.Problems[0].Kind != accounts.ProblemEnvOverride || c.Problems[0].Variable != "CLAUDE_CONFIG_DIR" ||
		!strings.Contains(c.Problems[0].Message, "set in this terminal by something other than Devpit") {
		t.Fatalf("problems = %+v", c.Problems)
	}
	if rows[2].Tool != accounts.ToolGitHub || !rows[2].Installed || rows[3].Installed || rows[2].LiveRisk != "" {
		t.Fatalf("other rows = %+v %+v", rows[2], rows[3])
	}
	// Default, elsewhere: the email recorded at the last Verify, not asked.
	rows, _ = Overview(d, s, `D:\x`, accounts.ResolveOptions{})
	if rows[0].Identity.Email() != "zubair@gmail.com" || rows[0].Identity.State() != accounts.StateRecorded {
		t.Fatalf("default row = %+v", rows[0].Identity.Fields())
	}
	if ran != 0 {
		t.Fatal("ran a command")
	}
}

func TestCheckEnv(t *testing.T) {
	env := map[string]string{
		"CLAUDE_CONFIG_DIR": `C:\x`, "ANTHROPIC_API_KEY": "sk-ant-api03-" + strings.Repeat("Q", 40),
		"GH_TOKEN": "ghp_" + strings.Repeat("A1", 18), "SUPABASE_ACCESS_TOKEN": "sbp_x",
	}
	get := func(k string) string { return env[k] }
	probs := CheckEnv(accounts.ToolClaude, get)
	if len(probs) != 2 {
		t.Fatalf("claude = %+v", probs)
	}
	for _, p := range probs {
		if strings.Contains(p.Message, "sk-ant") {
			t.Fatalf("a token value reached a message: %q", p.Message)
		}
	}
	if p := CheckEnv(accounts.ToolGitHub, get); len(p) != 1 || p[0].Variable != "GH_TOKEN" || strings.Contains(p[0].Message, "ghp_") {
		t.Fatalf("github = %+v", p)
	}
	if p := CheckEnv(accounts.ToolSupabase, get); len(p) != 1 {
		t.Fatalf("supabase = %+v", p)
	}
	// Inside a session a Devpit shim started, CLAUDE_CONFIG_DIR is Devpit's.
	env["DEVPIT_SHIM_GUARD"] = "claude:123"
	if p := CheckEnv(accounts.ToolClaude, get); len(p) != 1 || p[0].Variable != "ANTHROPIC_API_KEY" {
		t.Fatalf("inside a shim = %+v", p)
	}
	// Only Claude Code's own variable is cleared; token variables are not.
	for _, tool := range accounts.Tools() {
		for _, v := range EnvOverridesFor(tool) {
			if v.Managed && v.Name != "CLAUDE_CONFIG_DIR" {
				t.Errorf("%s clears %s without documenting it here", tool, v.Name)
			}
		}
	}
}
