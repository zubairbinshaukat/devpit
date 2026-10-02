package adapters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

const (
	sbTokenRequired = `{"_tag":"Error","error":{"code":"AccessTokenRequiredError","message":"Access token not provided. Supply an access token by running supabase login or setting the SUPABASE_ACCESS_TOKEN environment variable."}}`
	sbSignedIn      = `{"id":"7b0c2b8e-1111-2222-3333-444455556666","email":"zubair@work.com","username":"zubair"}`
	sbBadToken      = `{"_tag":"Error","error":{"code":"WhoamiUnexpectedStatusError","message":"token is invalid or has expired"}}`
)

var sbWhoami = []string{"whoami", "--output-format", "json"}

// newSupabaseForTest answers the probe (whoami in an empty temp home) as
// the current CLI does.
func newSupabaseForTest(t *testing.T, probe FakeResponse) (*supabaseAdapter, Deps, *FakeRunner) {
	t.Helper()
	d, fake := toolDeps(t)
	fake.Set(FakeResponse{Stdout: "2.119.0\n"}, "supabase", "--version")
	fake.Set(probe, "supabase", sbWhoami...)
	return newSupabase(d).(*supabaseAdapter), d, fake
}

func TestSupabaseProbeGatesOnBehaviour(t *testing.T) {
	a, _, fake := newSupabaseForTest(t, FakeResponse{Stdout: sbTokenRequired, Exit: 1})
	c := a.Capabilities(context.Background())
	if !c.FolderRules || !c.Everywhere || !c.JustOnce || !c.AddAccount || c.ShowOnly {
		t.Fatalf("caps = %+v", c)
	}
	probe, ok := ran(fake, "supabase", sbWhoami...)
	if !ok {
		t.Fatal("no probe")
	}
	home, set, _ := EnvOf(probe, "SUPABASE_HOME")
	nk, _, _ := EnvOf(probe, "SUPABASE_NO_KEYRING")
	_, _, unsetTok := EnvOf(probe, "SUPABASE_ACCESS_TOKEN")
	if !set || nk != "1" || !unsetTok || !strings.Contains(filepath.Base(home), "dp-sb-probe") {
		t.Fatalf("probe env = %+v unset %+v", probe.Env, probe.Unset)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the probe's temporary home was left behind")
	}

	// A CLI that ignores the switch finds the default login in Credential
	// Manager instead: show-only, with the reason.
	b, _, _ := newSupabaseForTest(t, FakeResponse{Stdout: sbSignedIn})
	c = b.Capabilities(context.Background())
	if c.FolderRules || !c.ShowOnly || !strings.Contains(c.Why, "SUPABASE_NO_KEYRING") {
		t.Fatalf("ignoring caps = %+v", c)
	}
	// An old CLI with no whoami at all.
	o, _, _ := newSupabaseForTest(t, FakeResponse{Stderr: "Unknown command \"whoami\" for \"supabase\"\n", Exit: 1})
	c = o.Capabilities(context.Background())
	if c.FolderRules || !c.ShowOnly || !strings.Contains(c.Why, "2.118.0") {
		t.Fatalf("old caps = %+v", c)
	}
	if _, err := o.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolSupabase, Name: "default"}); !errors.Is(err, accounts.ErrTooOld) {
		t.Fatalf("old whoami: %v", err)
	}
}

func TestSupabaseLaunchSetsTheFolderAndLeavesTheTokenVariable(t *testing.T) {
	l, err := LaunchFor(accounts.Account{Tool: accounts.ToolSupabase, Name: "work", Dir: `C:\a\supabase\work`})
	if err != nil || len(l.Env) != 2 || l.Env[0] != `SUPABASE_HOME=C:\a\supabase\work` || l.Env[1] != "SUPABASE_NO_KEYRING=1" {
		t.Fatalf("work = %+v %v", l, err)
	}
	// The project's rule: a token variable is reported, never cleared.
	if len(l.Unset) != 0 || len(l.Args) != 0 {
		t.Fatalf("work = %+v", l)
	}
	if l, err := LaunchFor(accounts.Account{Tool: accounts.ToolSupabase, Name: "default"}); err != nil || !l.IsZero() {
		t.Fatalf("default = %+v %v", l, err)
	}
	if p := CheckEnv(accounts.ToolSupabase, func(k string) string {
		if k == "SUPABASE_ACCESS_TOKEN" {
			return "sbp_" + strings.Repeat("0a", 20)
		}
		return ""
	}); len(p) != 1 || strings.Contains(p[0].Message, "sbp_") {
		t.Fatalf("env problems = %+v", p)
	}
}

func TestSupabaseWhoAmINeverReadsTheTokenFile(t *testing.T) {
	a, d, fake := newSupabaseForTest(t, FakeResponse{Stdout: sbTokenRequired, Exit: 1})
	work := filepath.Join(d.Paths.AccountsDir, "supabase", "work")
	_ = os.MkdirAll(work, 0o700)
	acct := accounts.Account{Tool: accounts.ToolSupabase, Name: "work", Dir: work}

	id, err := a.WhoAmI(context.Background(), acct)
	if err != nil || id.State() != accounts.StateNotSignedIn {
		t.Fatalf("signed out: %v %v", id.State(), err)
	}
	c, _ := ran(fake, "supabase", sbWhoami...)
	if v, _, _ := EnvOf(c, "SUPABASE_HOME"); v != work {
		t.Fatalf("SUPABASE_HOME = %q", v)
	}

	tok := "sbp_PLANTED" + strings.Repeat("a1", 20)
	if err = os.WriteFile(filepath.Join(work, "access-token"), []byte(tok), 0o600); err != nil {
		t.Fatal(err)
	}
	fake.Set(FakeResponse{Stdout: sbBadToken, Exit: 1}, "supabase", sbWhoami...)
	id, err = a.WhoAmI(context.Background(), acct)
	if err != nil || id.State() != accounts.StateExpired || strings.Contains(fmt.Sprintf("%+v", id.Fields()), "PLANTED") {
		t.Fatalf("expired: %+v %v", id.Fields(), err)
	}

	fake.Set(FakeResponse{Stdout: sbSignedIn}, "supabase", sbWhoami...)
	id, err = a.WhoAmI(context.Background(), acct)
	if err != nil || !id.SignedIn() || id.Email() != "zubair@work.com" || id.Login() != "zubair" {
		t.Fatalf("signed in: %+v %v", id.Fields(), err)
	}
	// Default: Supabase untouched, no folder variables.
	if _, err = a.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolSupabase, Name: "default"}); err != nil {
		t.Fatal(err)
	}
	c, _ = ran(fake, "supabase", sbWhoami...)
	if _, set, _ := EnvOf(c, "SUPABASE_HOME"); set || len(c.Unset) != 0 {
		t.Fatalf("default whoami env = %+v unset %+v", c.Env, c.Unset)
	}
}

func TestSupabaseLogin(t *testing.T) {
	a, d, fake := newSupabaseForTest(t, FakeResponse{Stdout: sbTokenRequired, Exit: 1})
	dir := filepath.Join(d.Paths.AccountsDir, "supabase", "work")
	fake.Set(FakeResponse{Stdout: "You are now logged in. Happy coding!\n", Do: func(c Cmd) {
		// The CLI writes its token file into SUPABASE_HOME.
		if v, _, _ := EnvOf(c, "SUPABASE_HOME"); v == dir {
			_ = os.WriteFile(filepath.Join(dir, "access-token"), []byte("x"), 0o600)
		}
		// From now on whoami in that folder is signed in.
		fake.Set(FakeResponse{Stdout: sbSignedIn}, "supabase", sbWhoami...)
	}}, "supabase", "login")
	// Run the probe first, while whoami still answers as an empty home.
	_ = a.Capabilities(context.Background())
	ch, err := a.Login(context.Background(), LoginRequest{Name: "work"})
	if err != nil {
		t.Fatal(err)
	}
	fin := lastEvent(drainEvents(t, ch))
	if fin.State != accounts.StepDone || fin.Account == nil || fin.Account.Dir != dir || fin.Account.Email != "zubair@work.com" {
		t.Fatalf("final = %+v", fin)
	}
	c, _ := ran(fake, "supabase", "login")
	if nk, _, _ := EnvOf(c, "SUPABASE_NO_KEYRING"); nk != "1" {
		t.Fatal("login ran without SUPABASE_NO_KEYRING=1: the token would go to Credential Manager")
	}
}
