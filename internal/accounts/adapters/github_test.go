package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

const ghStatusTwo = `{"hosts":{"github.com":[
 {"state":"success","active":true,"host":"github.com","login":"zubair","tokenSource":"keyring","scopes":"'gist', 'read:org', 'repo'","gitProtocol":"https"},
 {"state":"success","active":false,"host":"github.com","login":"zubair-work","tokenSource":"keyring","scopes":"'repo', 'user:email'","gitProtocol":"https"}],
 "ghe.example.com":[{"state":"success","active":true,"host":"ghe.example.com","login":"other"}]}}`

// plantedToken looks like a real gh OAuth token, so every scrubber and
// guard treats it as one.
var plantedToken = "gho_PLANTED" + strings.Repeat("x9Q", 10)

func newGhFake() *FakeRunner {
	f := &FakeRunner{}
	f.Set(FakeResponse{Stdout: "gh version 2.102.0 (2026-09-30)\n"}, "gh", "--version")
	f.Set(FakeResponse{Stdout: "Flags:\n  -u, --user string   The account to output the token for\n"}, "gh", "auth", "token", "--help")
	f.Set(FakeResponse{Stdout: ghStatusTwo}, "gh", "auth", "status", "--json", "hosts")
	f.Set(FakeResponse{Stdout: plantedToken + "\n"}, "gh", "auth", "token", "--hostname", "github.com", "--user", "zubair-work")
	return f
}

func TestGhStatusJSONAndText(t *testing.T) {
	f := newGhFake()
	got, err := ghLogins(context.Background(), Deps{Runner: f})
	if err != nil || len(got) != 2 || got[0].Login != "zubair" || !got[0].Active || got[1].Login != "zubair-work" {
		t.Fatalf("json = %+v %v", got, err)
	}
	if !got[1].hasScope("user:email") || got[0].hasScope("user:email") || got[0].hasScope("user") {
		t.Fatal("scopes")
	}
	for _, c := range f.Calls() {
		if _, set, unset := EnvOf(c, "GH_TOKEN"); set || !unset {
			t.Fatalf("gh auth status must run without GH_TOKEN: %+v", c)
		}
	}
	text := "github.com\n  ✓ Logged in to github.com account zubair (keyring)\n  - Active account: false\n" +
		"  ✓ Logged in to github.com account zubair-work (keyring)\n  - Active account: true\n"
	if got := parseGhStatusText(text); len(got) != 2 || got[0].Active || !got[1].Active {
		t.Fatalf("text = %+v", got)
	}
	old := "github.com\n  ✓ Logged in to github.com as zubair (oauth_token)\n  ✓ Git operations for github.com configured to use https protocol.\n"
	if got := parseGhStatusText(old); len(got) != 1 || got[0].Login != "zubair" || !got[0].Active {
		t.Fatalf("old text = %+v", got)
	}
	// An old gh without --json falls back to the text.
	f2 := &FakeRunner{}
	f2.Set(FakeResponse{Stderr: "unknown flag: --json\n", Exit: 1}, "gh", "auth", "status", "--json", "hosts")
	f2.Set(FakeResponse{Stderr: old}, "gh", "auth", "status", "--hostname", "github.com")
	if got, err := ghLogins(context.Background(), Deps{Runner: f2}); err != nil || len(got) != 1 {
		t.Fatalf("fallback = %+v %v", got, err)
	}
}

func TestGitHubCapabilities(t *testing.T) {
	f := newGhFake()
	a := newGitHub(Deps{Runner: f})
	if c := a.Capabilities(context.Background()); !c.FolderRules || !c.Everywhere || !c.JustOnce || !c.AddAccount {
		t.Fatalf("caps = %+v", c)
	}
	f.Set(FakeResponse{Stdout: "gh version 2.39.1\n"}, "gh", "--version")
	f.Set(FakeResponse{Stdout: "Flags:\n  -h, --hostname string\n"}, "gh", "auth", "token", "--help")
	a = newGitHub(Deps{Runner: f})
	if c := a.Capabilities(context.Background()); c.FolderRules || !strings.Contains(c.Why, "2.39.1") || !strings.Contains(c.Why, "2.40") {
		t.Fatalf("old caps = %+v", c)
	}
	a = newGitHub(Deps{Runner: &FakeRunner{Missing: map[string]bool{"gh": true}}})
	if c := a.Capabilities(context.Background()); c.FolderRules || !strings.Contains(c.Why, "not installed") {
		t.Fatalf("missing caps = %+v", c)
	}
}

func TestGitHubAccountsDetectsLoginsNotInTheStore(t *testing.T) {
	a := newGitHub(Deps{Runner: newGhFake()})
	s := accounts.NewStore()
	_ = s.AddAccount(accounts.Account{Tool: accounts.ToolGitHub, Name: "work", Label: "zubair-work"})
	got, err := a.Accounts(context.Background(), s)
	if err != nil || len(got) != 3 || got[2].Label != "zubair" || got[2].Name != "zubair" || got[2].ImportedFrom != "detected" {
		t.Fatalf("accounts = %+v %v", got, err)
	}
	if n := nameForLogin(accounts.NewStore(), "Default"); accounts.IsDefault(n) || accounts.ValidateName(n) != nil {
		t.Fatalf("a login called default became %q", n)
	}
	if n := nameForLogin(accounts.NewStore(), strings.Repeat("a", 39)); accounts.ValidateName(n) != nil {
		t.Fatalf("long login: %q", n)
	}
}

func TestPickNewLogin(t *testing.T) {
	b := []ghAccount{{Login: "a", Active: true}}
	if got := pickNewLogin(b, []ghAccount{{Login: "a"}, {Login: "b", Active: true}}); got != "b" {
		t.Fatal(got)
	}
	if got := pickNewLogin(b, []ghAccount{{Login: "a", Active: true}}); got != "a" {
		t.Fatal("signing in again to the same login picks it:", got)
	}
	if got := pickNewLogin(nil, []ghAccount{{Login: "x"}, {Login: "y"}}); got != "" {
		t.Fatal("two new logins and neither active is ambiguous:", got)
	}
}

func TestGitHubLoginAddsAndWarnsAboutTheActiveAccount(t *testing.T) {
	f := newGhFake()
	first := `{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"zubair"}]}}`
	f.Set(FakeResponse{Stdout: first}, "gh", "auth", "status", "--json", "hosts")
	f.Set(FakeResponse{Stdout: "! First copy your one-time code: ABCD-1234\n", Do: func(Cmd) {
		// After the sign-in gh has two logins and the new one is active.
		f.Set(FakeResponse{Stdout: strings.Replace(ghStatusTwo, `"active":false`, `"active":true`, 1)}, "gh", "auth", "status", "--json", "hosts")
	}}, "gh", "auth", "login", "--hostname", "github.com", "--git-protocol", "https", "--web")
	a := newGitHub(Deps{Runner: f})
	ch, err := a.Login(context.Background(), LoginRequest{Name: "work"})
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, ch)
	fin := last(evs)
	if fin.State != accounts.StepDone || fin.Account == nil || fin.Account.Label != "zubair-work" || fin.Account.Name != "work" {
		t.Fatalf("final = %+v", fin)
	}
	warned := false
	for _, ev := range evs {
		if ev.State == accounts.StepWarning && strings.Contains(ev.Detail, "gh auth switch --user zubair") {
			warned = true
		}
	}
	if !warned {
		t.Fatal("the switch of gh's active account must be explained")
	}
	for _, c := range f.Calls() {
		if slices.Contains(c.Args, "switch") || slices.Contains(c.Args, "logout") {
			t.Fatalf("Devpit ran %v", c.Args)
		}
	}
}

func TestParseRemoteURL(t *testing.T) {
	for raw, want := range map[string]GitRemote{
		"https://github.com/o/r.git":                         {URL: "https://github.com/o/r.git", Protocol: ProtocolHTTPS, Host: "github.com"},
		"https://zubair:" + plantedToken + "@github.com/o/r": {URL: "https://github.com/o/r", Protocol: ProtocolHTTPS, Host: "github.com"},
		"git@github.com:o/r.git":                             {URL: "git@github.com:o/r.git", Protocol: ProtocolSSH, Host: "github.com"},
		"ssh://git@github.com:22/o/r.git":                    {URL: "ssh://git@github.com:22/o/r.git", Protocol: ProtocolSSH, Host: "github.com"},
		"github-work:o/r.git":                                {URL: "github-work:o/r.git", Protocol: ProtocolSSH, Host: "github-work"},
		`C:\repos\r`:                                         {URL: `C:\repos\r`, Protocol: ProtocolOther},
		"https://gitlab.com/o/r":                             {URL: "https://gitlab.com/o/r", Protocol: ProtocolHTTPS, Host: "gitlab.com"},
	} {
		got := ParseRemoteURL(raw)
		if got != want {
			t.Errorf("ParseRemoteURL(%q) = %+v, want %+v", raw, got, want)
		}
	}
}

// TestGitHubTokenGoesOnlyToTheChild is the safety test for the one token
// Devpit handles: a planted token from a fake gh reaches the child's
// environment through LaunchLive and nothing else (no event, log, error,
// identity, store or journal), and the commands that would print one are
// never run.
func TestGitHubTokenGoesOnlyToTheChild(t *testing.T) {
	f := newGhFake()
	user := `{"login":"zubair-work","id":4242,"name":"Zubair","email":null}`
	f.Set(FakeResponse{Stdout: user, Stderr: "debug: token " + plantedToken + "\n"}, "gh", "api", "--hostname", "github.com", "user")
	f.Set(FakeResponse{Stdout: `[{"email":"z@work.com","primary":true,"verified":true},{"email":"x@y.z","verified":false}]`}, "gh", "api", "--hostname", "github.com", "user/emails")
	var logs []string
	d := Deps{Runner: f, Log: func(s string) { logs = append(logs, s) }, Paths: accounts.PathsIn(t.TempDir(), t.TempDir()), Home: t.TempDir()}
	a := newGitHub(d)
	work := accounts.Account{Tool: accounts.ToolGitHub, Name: "work", Label: "zubair-work"}

	l, err := LaunchLive(context.Background(), f, work)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(l.Env, "GH_TOKEN="+plantedToken) || !slices.Contains(l.Env, EnvGitHubAccount+"=work") || !slices.Contains(l.Unset, "GITHUB_TOKEN") {
		t.Fatalf("launch = %d env entries, unset %v", len(l.Env), l.Unset)
	}
	if _, serr := LaunchFor(work); !errors.Is(serr, ErrNeedsLiveLaunch) || !errors.Is(serr, accounts.ErrNotSupported) {
		t.Fatalf("static launch: %v", serr)
	}
	if l, derr := LaunchLive(context.Background(), f, accounts.Account{Tool: accounts.ToolGitHub, Name: "default"}); derr != nil || !l.IsZero() {
		t.Fatalf("default: %+v %v", l, derr)
	}
	if l, cerr := LaunchLive(context.Background(), f, accounts.Account{Tool: accounts.ToolClaude, Name: "work", Dir: `C:\a`}); cerr != nil || len(l.Env) != 1 {
		t.Fatalf("other tools go through LaunchFor: %+v %v", l, cerr)
	}

	var seen []string
	id, err := a.WhoAmI(context.Background(), work)
	if err != nil || id.Login() != "zubair-work" {
		t.Fatalf("who = %+v %v", id.Fields(), err)
	}
	b, _ := json.Marshal(id)
	seen = append(seen, string(b), fmt.Sprintf("%+v", id.Fields()))
	sugg, err := a.SuggestEmails(context.Background(), work)
	if err != nil || len(sugg) != 2 || sugg[0].Email != "4242+zubair-work@users.noreply.github.com" || sugg[1].Email != "z@work.com" {
		t.Fatalf("suggestions = %+v %v", sugg, err)
	}
	seen = append(seen, fmt.Sprintf("%+v", sugg))

	// gh failing with the token in its output: the error carries none.
	f.Set(FakeResponse{Stdout: plantedToken + " " + plantedToken + "\n", Stderr: "error: " + plantedToken + "\n", Exit: 1}, "gh", "auth", "token", "--hostname", "github.com", "--user", "zubair-work")
	_, err = LaunchLive(context.Background(), f, work)
	if err == nil {
		t.Fatal("a failing gh must fail the launch")
	}
	seen = append(seen, err.Error())
	_, err = a.WhoAmI(context.Background(), work)
	if err == nil {
		t.Fatal("who-am-I without a token must fail")
	}
	seen = append(seen, err.Error())
	// Two tokens on one line is not a token.
	f.Set(FakeResponse{Stdout: plantedToken + " " + plantedToken + "\n"}, "gh", "auth", "token", "--hostname", "github.com", "--user", "zubair-work")
	if _, err = LaunchLive(context.Background(), f, work); err == nil {
		t.Fatal("garbage from gh must not become GH_TOKEN")
	}
	seen = append(seen, err.Error())
	seen = append(seen, logs...)

	for _, s := range seen {
		if strings.Contains(s, "PLANTED") {
			t.Fatalf("the token leaked: %q", s)
		}
	}
	for _, c := range f.Calls() {
		if err := CheckArgs(c.Name, c.Args); err != nil || slices.Contains(c.Args, "--show-token") || slices.Contains(c.Args, "switch") {
			t.Fatalf("ran %v", c.Args)
		}
		for _, a := range c.Args {
			if strings.Contains(a, "PLANTED") {
				t.Fatalf("the token went on a command line: %v", c.Args)
			}
		}
	}
}

func TestSuggestEmailsNeverAsksForMoreScopes(t *testing.T) {
	f := newGhFake()
	f.Set(FakeResponse{Stdout: `{"login":"zubair","id":7}`}, "gh", "api", "--hostname", "github.com", "user")
	a := newGitHub(Deps{Runner: f})
	got, err := a.SuggestEmails(context.Background(), accounts.Account{Tool: accounts.ToolGitHub, Name: "default"})
	if err != nil || len(got) != 1 || got[0].Email != "7+zubair@users.noreply.github.com" {
		t.Fatalf("got %+v %v", got, err)
	}
	for _, c := range f.Calls() {
		if slices.Contains(c.Args, "user/emails") || slices.Contains(c.Args, "refresh") {
			t.Fatalf("without the user:email scope Devpit must not ask: %v", c.Args)
		}
	}
}

// TestGitHubRuleSetsUpTheHelperAndUndoIsExact uses the real git: the first
// GitHub rule writes the github.com helper (reset, then Devpit), records
// the chain that was there, and undo restores the global config's exact
// bytes.
func TestGitHubRuleSetsUpTheHelperAndUndoIsExact(t *testing.T) {
	w := newGitWorld(t)
	// "pass""word": the shell reads password, while the secret guard (which
	// rightly refuses a config holding a password) does not see one.
	prev := `!f() { echo username=prev; echo pass""word=prevpw; }; f`
	q, _ := gitQuote(prev)
	w.orig = []byte(baseGlobal + "[credential]\n\thelper = " + q + "\n")
	_ = os.WriteFile(w.global, w.orig, 0o600)
	if _, err := w.eng.AddAccount(accounts.Account{Tool: accounts.ToolGitHub, Name: "work", Label: "zubair-work"}); err != nil {
		t.Fatal(err)
	}
	p, err := w.hub.Plan(accounts.PreviewInput{Store: w.store(), Change: accounts.Change{Tool: accounts.ToolGitHub, Account: "work", Scope: accounts.FolderScope(`C:\Work`)}})
	if err != nil {
		t.Fatal(err)
	}
	text := p.Text()
	for _, want := range []string{
		`[credential "https://github.com"]`, "    helper =", `helper = "!\"` + slashPath(w.hub.HelperExe) + `\" git-credential"`,
		"devpit.json (adds)", `"previous": [`, "gh and pushes to GitHub over HTTPS will use work (zubair-work)",
		"only sign-in helper for github.com", "pushes over SSH",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("preview lacks %q:\n%s", want, text)
		}
	}
	evs := collect(t, Apply(context.Background(), w.eng, w.hub, p))
	if fin := last(evs); fin.State != accounts.StepDone {
		t.Fatalf("apply = %+v", fin)
	}
	h, ok, err := LoadGitHelper(GitDir(w.deps))
	if err != nil || !ok || !slices.Equal(h.Previous, []string{prev}) {
		t.Fatalf("recorded helper = %+v %v %v", h, ok, err)
	}
	// Git now runs only Devpit's helper for github.com.
	got := w.gitOut(w.tmp, "config", "--get-urlmatch", "credential.helper", "https://github.com")
	if got != h.Command {
		t.Fatalf("effective github.com helper = %q, want %q", got, h.Command)
	}
	// A second rule keeps the recorded chain as it was.
	w.change(w.hub, accounts.Change{Tool: accounts.ToolGitHub, Account: "work", Scope: accounts.FolderScope(`D:\Other`)})
	if h2, _, _ := LoadGitHelper(GitDir(w.deps)); !slices.Equal(h2.Previous, h.Previous) {
		t.Fatalf("chain re-recorded as %q", h2.Previous)
	}
	for range 2 {
		if _, err := w.eng.Undo(); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.readGlobal(); !bytes.Equal(got, w.orig) {
		t.Fatalf("undo: global = %q", got)
	}
	if fs := w.devpitFiles(); len(fs) != 0 {
		t.Fatalf("undo left %v", fs)
	}
}

func TestGitHubPlanWithoutGitStillCoversGh(t *testing.T) {
	f := newGhFake()
	f.Missing = map[string]bool{"git": true}
	d := Deps{Runner: f, Paths: accounts.PathsIn(t.TempDir(), t.TempDir()), Home: t.TempDir()}
	s := accounts.NewStore()
	_ = s.AddAccount(accounts.Account{Tool: accounts.ToolGitHub, Name: "work", Label: "zubair-work"})
	p, err := newGitHub(d).Plan(accounts.PreviewInput{Store: s, Change: accounts.Change{Tool: accounts.ToolGitHub, Account: "work", Scope: accounts.EverywhereScope()}})
	if err != nil || len(p.Edits) != 1 || !strings.Contains(strings.Join(p.Warnings, " "), "only gh follows") {
		t.Fatalf("plan = %+v %v", p, err)
	}
	if _, err := newGitHub(d).Plan(accounts.PreviewInput{Store: s, Change: accounts.Change{Tool: accounts.ToolGitHub, Account: "nobody", Scope: accounts.EverywhereScope()}}); err == nil {
		t.Fatal("an unknown account must be refused")
	}
	_ = s.AddAccount(accounts.Account{Tool: accounts.ToolGitHub, Name: "nolabel"})
	if _, err := newGitHub(d).Plan(accounts.PreviewInput{Store: s, Change: accounts.Change{Tool: accounts.ToolGitHub, Account: "nolabel", Scope: accounts.EverywhereScope()}}); err == nil {
		t.Fatal("an account without a login must be refused")
	}
}
