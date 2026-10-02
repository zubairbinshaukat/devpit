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

// shortTemp is a temporary folder with a short name. t.TempDir's names
// carry the test name plus digits, a long letters-and-digits run that
// accounts.Scrub takes for a token, so CheckArgs would refuse any command
// with such a path in it.
func shortTemp(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dpt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// toolDeps is the Deps the tests of vercel, firebase, supabase, cloudflare
// and convex share: temp folders, a fake runner, no real PATH, no real
// environment.
func toolDeps(t *testing.T) (Deps, *FakeRunner) {
	t.Helper()
	fake := &FakeRunner{}
	env := map[string]string{}
	d := Deps{
		Runner: fake,
		Paths:  accounts.PathsIn(shortTemp(t), shortTemp(t)),
		Home:   shortTemp(t),
		Now:    func() time.Time { return time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC) },
		LookPath: func(name string) (string, error) {
			return "", fmt.Errorf("%s: %w", name, accounts.ErrNotFoundTool)
		},
		Getenv: func(k string) string { return env[k] },
	}
	return d, fake
}

func drainEvents(t *testing.T, ch <-chan accounts.Event) []accounts.Event {
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

func lastEvent(evs []accounts.Event) accounts.Event { return evs[len(evs)-1] }

// ran reports whether a call with exactly these program and arguments was
// made, and returns the last such call.
func ran(f *FakeRunner, name string, args ...string) (Cmd, bool) {
	var hit Cmd
	found := false
	for _, c := range f.Calls() {
		if Key(c.Name, c.Args...) == Key(name, args...) {
			hit, found = c, true
		}
	}
	return hit, found
}

const vercelWhoamiHelp = `Vercel CLI 60.1.3
  ▲ vercel whoami [options]
  Global Options:
    -Q, --global-config <DIR>   Path to the global ".vercel" directory
`

func newVercelForTest(t *testing.T) (*vercelAdapter, Deps, *FakeRunner) {
	t.Helper()
	d, fake := toolDeps(t)
	fake.Set(FakeResponse{Stdout: "Vercel CLI 60.1.3\n60.1.3\n"}, "vercel", "--version")
	fake.Set(FakeResponse{Stdout: vercelWhoamiHelp}, "vercel", "whoami", "--help")
	return newVercel(d).(*vercelAdapter), d, fake
}

func TestVercelCapabilitiesProbeTheFlagNotTheVersion(t *testing.T) {
	a, _, _ := newVercelForTest(t)
	c := a.Capabilities(context.Background())
	if !c.FolderRules || !c.Everywhere || !c.JustOnce || !c.AddAccount || c.ShowOnly || c.Beta {
		t.Fatalf("caps = %+v", c)
	}

	a2, _, fake2 := newVercelForTest(t)
	fake2.Set(FakeResponse{Stdout: "  ▲ vercel whoami [options]\n"}, "vercel", "whoami", "--help")
	c = a2.Capabilities(context.Background())
	if c.FolderRules || !strings.Contains(c.Why, "--global-config") || !strings.Contains(c.Why, "60.1.3") {
		t.Fatalf("old caps = %+v", c)
	}

	a3, _, fake3 := newVercelForTest(t)
	fake3.Missing = map[string]bool{"vercel": true}
	if c := a3.Capabilities(context.Background()); c.FolderRules || c.Why != "Vercel CLI is not installed." {
		t.Fatalf("missing caps = %+v", c)
	}
	if in := a3.Installed(context.Background()); in.Found {
		t.Fatalf("installed = %+v", in)
	}
}

func TestVercelNpxOnlyIsSaidPlainly(t *testing.T) {
	a, d, fake := newVercelForTest(t)
	fake.Missing = map[string]bool{"vercel": true}
	d.LookPath = func(name string) (string, error) {
		if name == "npx" {
			return `C:\nodejs\npx.cmd`, nil
		}
		return "", accounts.ErrNotFoundTool
	}
	a.deps = d
	in := a.Installed(context.Background())
	if in.Found || !strings.Contains(in.Note, "npx") || !strings.Contains(in.Note, "npm install -g vercel") {
		t.Fatalf("installed = %+v", in)
	}
}

func TestVercelLaunchPutsGlobalConfigFirst(t *testing.T) {
	l, err := LaunchFor(accounts.Account{Tool: accounts.ToolVercel, Name: "work", Dir: `C:\a\vercel\work`})
	if err != nil || len(l.Args) != 2 || l.Args[0] != "--global-config" || l.Args[1] != `C:\a\vercel\work` || len(l.Env) != 0 || len(l.Unset) != 0 {
		t.Fatalf("work: %+v %v", l, err)
	}
	if l, err := LaunchFor(accounts.Account{Tool: accounts.ToolVercel, Name: "default"}); err != nil || !l.IsZero() {
		t.Fatalf("default: %+v %v", l, err)
	}
	if _, err := LaunchFor(accounts.Account{Tool: accounts.ToolVercel, Name: "nodir"}); err == nil {
		t.Fatal("no folder must not launch")
	}
}

func TestVercelWhoAmI(t *testing.T) {
	a, d, fake := newVercelForTest(t)
	work := filepath.Join(d.Paths.AccountsDir, "vercel", "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	acct := accounts.Account{Tool: accounts.ToolVercel, Name: "work", Dir: work}
	args := []string{"--global-config", work, "whoami", "--json"}

	fake.Set(FakeResponse{Stdout: "{\n  \"loggedIn\": false\n}\n", Exit: 1}, "vercel", args...)
	id, err := a.WhoAmI(context.Background(), acct)
	if err != nil || id.State() != accounts.StateNotSignedIn {
		t.Fatalf("signed out: %v %v", id.State(), err)
	}
	c, _ := ran(fake, "vercel", args...)
	if c.Dir != work {
		t.Fatalf("whoami ran in %q; it must run in the account folder so a linked project does not change the answer", c.Dir)
	}

	// The shape Vercel 60.1.3's source writes when signed in.
	fake.Set(FakeResponse{Stdout: `{"team":{"id":"team_abc","slug":"acme","name":"Acme Inc","plan":"pro"},"plan":"pro","username":"zubair","email":"zubair@work.com","name":"Zubair"}`}, "vercel", args...)
	id, err = a.WhoAmI(context.Background(), acct)
	f := id.Fields()
	if err != nil || !id.SignedIn() || f.Email != "zubair@work.com" || f.Login != "zubair" || f.Org != "Acme Inc" || f.Plan != "pro" {
		t.Fatalf("signed in: %+v %v", f, err)
	}

	// Personal scope: team is null.
	fake.Set(FakeResponse{Stdout: `{"team":null,"plan":"hobby","username":"zubair","email":"z@x.com"}`}, "vercel", args...)
	if id, _ = a.WhoAmI(context.Background(), acct); !id.SignedIn() || id.Fields().Org != "" {
		t.Fatalf("personal: %+v", id.Fields())
	}

	// Text fallback (no JSON): a non-terminal whoami prints the user name.
	fake.Set(FakeResponse{Stdout: "zubair\n"}, "vercel", args...)
	if id, _ = a.WhoAmI(context.Background(), acct); !id.SignedIn() || id.Login() != "zubair" {
		t.Fatalf("text: %+v", id.Fields())
	}
	fake.Set(FakeResponse{Stderr: "Error: The specified token is not valid. Use `vercel login` to generate a new token.\n", Exit: 1}, "vercel", args...)
	if id, _ = a.WhoAmI(context.Background(), acct); id.State() != accounts.StateExpired {
		t.Fatalf("expired: %v", id.State())
	}

	// Default: no --global-config at all.
	fake.Set(FakeResponse{Stdout: `{"loggedIn":false}`, Exit: 1}, "vercel", "whoami", "--json")
	if id, err = a.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolVercel, Name: "default"}); err != nil || id.State() != accounts.StateNotSignedIn {
		t.Fatalf("default: %v %v", id.State(), err)
	}

	// A folder that is gone: Vercel is not run (it would start a new one).
	before := len(fake.Calls())
	if _, err := a.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolVercel, Name: "gone", Dir: filepath.Join(work, "gone")}); err == nil || len(fake.Calls()) != before {
		t.Fatal("ran vercel against a missing folder")
	}
}

func TestVercelLoginKeepsTheFolderOnlyOnSuccess(t *testing.T) {
	a, d, fake := newVercelForTest(t)
	dir := filepath.Join(d.Paths.AccountsDir, "vercel", "work")
	fake.Set(FakeResponse{Stdout: "> Success! Email authentication complete\n"}, "vercel", "--global-config", dir, "login")
	fake.Set(FakeResponse{Stdout: `{"team":null,"username":"zubair","email":"zubair@work.com"}`}, "vercel", "--global-config", dir, "whoami", "--json")
	ch, err := a.Login(context.Background(), LoginRequest{Name: "work"})
	if err != nil {
		t.Fatal(err)
	}
	fin := lastEvent(drainEvents(t, ch))
	if !fin.Final || fin.State != accounts.StepDone || fin.Account == nil || fin.Account.Dir != dir ||
		fin.Account.Email != "zubair@work.com" || fin.Account.Label != "zubair" {
		t.Fatalf("final = %+v %+v", fin, fin.Account)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatal("folder not kept")
	}

	for _, resp := range []FakeResponse{{Stdout: "Login cancelled\n", Exit: 1}, {Err: errors.New("boom")}} {
		va, vd, vfake := newVercelForTest(t)
		vdir := filepath.Join(vd.Paths.AccountsDir, "vercel", "work")
		resp.Do = func(Cmd) { _ = os.WriteFile(filepath.Join(vdir, "config.json"), []byte("{}"), 0o600) }
		vfake.Set(resp, "vercel", "--global-config", vdir, "login")
		ch, err := va.Login(context.Background(), LoginRequest{Name: "work"})
		if err != nil {
			t.Fatal(err)
		}
		if fin := lastEvent(drainEvents(t, ch)); fin.State != accounts.StepFailed || fin.Account != nil {
			t.Fatalf("final = %+v", fin)
		}
		if _, err := os.Stat(vdir); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("a cancelled sign-in left its folder behind")
		}
	}
	if _, err := a.Login(context.Background(), LoginRequest{Name: "default"}); !errors.Is(err, accounts.ErrReservedName) {
		t.Fatalf("default name: %v", err)
	}
}

func TestVercelProjectLinkAndPlanWarning(t *testing.T) {
	a, d, _ := newVercelForTest(t)
	proj := filepath.Join(shortTemp(t), "site")
	sub := filepath.Join(proj, "src", "pages")
	if err := os.MkdirAll(filepath.Join(proj, ".vercel"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(sub, 0o700)
	if err := os.WriteFile(filepath.Join(proj, ".vercel", "project.json"), []byte(`{"projectId":"prj_123","orgId":"team_abc","projectName":"site","settings":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l, ok, err := VercelProjectLink(sub)
	if err != nil || !ok || l.Folder != proj || l.OrgID != "team_abc" || l.ProjectID != "prj_123" || !l.Team() {
		t.Fatalf("link = %+v %v %v", l, ok, err)
	}
	if _, ok, err = VercelProjectLink(shortTemp(t)); ok || err != nil {
		t.Fatalf("no link: %v %v", ok, err)
	}

	if filepath.Separator != '\\' {
		return // the preview needs a Windows folder path
	}
	work := filepath.Join(d.Paths.AccountsDir, "vercel", "work")
	_ = os.MkdirAll(work, 0o700)
	s := accounts.NewStore()
	_ = s.AddAccount(accounts.Account{Tool: accounts.ToolVercel, Name: "work", Email: "z@work.com", Dir: work})
	p, err := a.Plan(accounts.PreviewInput{Store: s, Change: accounts.Change{Tool: accounts.ToolVercel, Account: "work", Scope: accounts.FolderScope(proj)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "team_abc") {
		t.Fatalf("warnings = %q", p.Warnings)
	}
}
