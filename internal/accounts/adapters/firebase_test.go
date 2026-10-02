package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

const firebaseHelp = `Usage: firebase [options] [command]

Options:
  -V, --version          output the version number
  -P, --project <alias_or_project_id>
  --account <email>      the Google account to use for authorization
  -j, --json             output JSON instead of text, use in conjunction with --non-interactive

Commands:
  login [options]        log the CLI into Firebase
  login:add [options] [email]  authorize the CLI for an additional account
  login:list             list authorized CLI accounts
`

const firebaseList2 = "\x1b[1mLogged in as zubair@gmail.com\x1b[22m\n\nOther accounts:\n - zubair@work.com\n - oss@example.org\n"

func newFirebaseForTest(t *testing.T) (*firebaseAdapter, *FakeRunner) {
	t.Helper()
	d, fake := toolDeps(t)
	fake.Set(FakeResponse{Stdout: "15.32.1\n"}, "firebase", "--version")
	fake.Set(FakeResponse{Stdout: firebaseHelp}, "firebase", "--help")
	return newFirebase(d).(*firebaseAdapter), fake
}

func TestFirebaseNeverAsksForTheJSONList(t *testing.T) {
	// The JSON form of login:list prints every account's tokens. The runner
	// refuses it whatever the order or spelling...
	for _, args := range [][]string{{"login:list", "--json"}, {"--json", "login:list"}, {"login:list", "-j"}, {"login:list", "--json=true"}} {
		if err := CheckArgs("firebase", args); !errors.Is(err, ErrForbiddenCommand) {
			t.Errorf("not refused: %v", args)
		}
	}
	// ...and the adapter never builds it: every call it makes is checked.
	a, fake := newFirebaseForTest(t)
	fake.Set(FakeResponse{Stdout: firebaseList2}, "firebase", "login:list")
	ctx := context.Background()
	_, _ = a.Accounts(ctx, nil)
	_, _ = a.WhoAmI(ctx, accounts.Account{Tool: accounts.ToolFirebase, Name: "default"})
	_, _ = a.WhoAmI(ctx, accounts.Account{Tool: accounts.ToolFirebase, Name: "work", Email: "zubair@work.com"})
	for _, c := range fake.Calls() {
		for _, arg := range c.Args {
			if arg == "--json" || arg == "-j" || strings.HasPrefix(arg, "--json=") {
				t.Fatalf("the adapter ran %s", Key(c.Name, c.Args...))
			}
		}
	}
	if _, ok := ran(fake, "firebase", "login:list"); !ok {
		t.Fatal("login:list (text) was never run; the test checks nothing")
	}
}

func TestParseFirebaseList(t *testing.T) {
	l := parseFirebaseList([]byte(firebaseList2))
	if l.Default != "zubair@gmail.com" || len(l.Others) != 2 || l.Others[0] != "zubair@work.com" || l.Others[1] != "oss@example.org" {
		t.Fatalf("list = %+v", l)
	}
	l = parseFirebaseList([]byte("⚠  No authorized accounts, run \"firebase login\"\n"))
	if l.Default != "" || len(l.Others) != 0 {
		t.Fatalf("empty = %+v", l)
	}
	l = parseFirebaseList([]byte("i  Logged in as zubair@gmail.com.\r\n"))
	if l.Default != "zubair@gmail.com" {
		t.Fatalf("info prefix = %+v", l)
	}
}

func TestFirebaseCapabilitiesAndLaunch(t *testing.T) {
	a, fake := newFirebaseForTest(t)
	if c := a.Capabilities(context.Background()); !c.FolderRules || !c.Everywhere || !c.JustOnce || !c.AddAccount {
		t.Fatalf("caps = %+v", c)
	}
	b, fake2 := newFirebaseForTest(t)
	fake2.Set(FakeResponse{Stdout: "Usage: firebase [options] [command]\n  login\n"}, "firebase", "--help")
	if c := b.Capabilities(context.Background()); c.FolderRules || !strings.Contains(c.Why, "--account") {
		t.Fatalf("old caps = %+v", c)
	}
	_ = fake

	l, err := LaunchFor(accounts.Account{Tool: accounts.ToolFirebase, Name: "work", Email: "zubair@work.com"})
	if err != nil || len(l.Args) != 2 || l.Args[0] != "--account" || l.Args[1] != "zubair@work.com" {
		t.Fatalf("launch = %+v %v", l, err)
	}
	if l, err := LaunchFor(accounts.Account{Tool: accounts.ToolFirebase, Name: "default"}); err != nil || !l.IsZero() {
		t.Fatalf("default = %+v %v", l, err)
	}
	if _, err := LaunchFor(accounts.Account{Tool: accounts.ToolFirebase, Name: "noemail"}); err == nil {
		t.Fatal("an account with no address must not launch")
	}
}

func TestFirebaseAccountsAndWhoAmI(t *testing.T) {
	a, fake := newFirebaseForTest(t)
	fake.Set(FakeResponse{Stdout: firebaseList2}, "firebase", "login:list")
	s := accounts.NewStore()
	_ = s.AddAccount(accounts.Account{Tool: accounts.ToolFirebase, Name: "work", Email: "zubair@work.com"})
	list, err := a.Accounts(context.Background(), s)
	if err != nil || len(list) != 3 || list[1].Name != "work" || list[2].Email != "oss@example.org" || list[2].ImportedFrom != "detected" || list[2].Name != "example" {
		t.Fatalf("accounts = %+v %v", list, err)
	}

	id, err := a.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolFirebase, Name: "default"})
	if err != nil || !id.SignedIn() || id.Email() != "zubair@gmail.com" {
		t.Fatalf("default = %+v %v", id.Fields(), err)
	}
	id, _ = a.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolFirebase, Name: "work", Email: "ZUBAIR@work.com"})
	if !id.SignedIn() {
		t.Fatalf("work = %+v", id.Fields())
	}
	id, _ = a.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolFirebase, Name: "gone", Email: "gone@x.com"})
	if id.State() != accounts.StateNotSignedIn || !strings.Contains(id.Fields().Note, "Sign in again") {
		t.Fatalf("gone = %+v", id.Fields())
	}
}

func TestFirebaseLogin(t *testing.T) {
	a, fake := newFirebaseForTest(t)
	calls := 0
	fake.Func = func(c Cmd) (Result, error) {
		if Key(c.Name, c.Args...) == "firebase login:list" {
			calls++
			if calls == 1 {
				return Result{Stdout: []byte("Logged in as zubair@gmail.com\n")}, nil
			}
			return Result{Stdout: []byte("Logged in as zubair@gmail.com\n\nOther accounts:\n - zubair@work.com\n")}, nil
		}
		return Result{}, errors.New("unexpected " + Key(c.Name, c.Args...))
	}
	fake.Set(FakeResponse{Stdout: "✔  Success! Added account zubair@work.com\n"}, "firebase", "login:add")
	ch, err := a.Login(context.Background(), LoginRequest{Name: "work"})
	if err != nil {
		t.Fatal(err)
	}
	fin := lastEvent(drainEvents(t, ch))
	if fin.State != accounts.StepDone || fin.Account == nil || fin.Account.Email != "zubair@work.com" || fin.Account.Dir != "" {
		t.Fatalf("final = %+v", fin)
	}

	// No default login yet: Firebase would refuse login:add, so Devpit says
	// what to do first and runs nothing more.
	b, fake2 := newFirebaseForTest(t)
	fake2.Set(FakeResponse{Stdout: "⚠  No authorized accounts\n"}, "firebase", "login:list")
	ch, _ = b.Login(context.Background(), LoginRequest{Name: "work"})
	fin = lastEvent(drainEvents(t, ch))
	if fin.State != accounts.StepFailed || !strings.Contains(fin.Err.Error(), "firebase login") {
		t.Fatalf("no default = %+v", fin)
	}
	if _, ok := ran(fake2, "firebase", "login:add"); ok {
		t.Fatal("ran login:add without a default login")
	}

	// Cancelled: nothing new in the list.
	c, fake3 := newFirebaseForTest(t)
	fake3.Set(FakeResponse{Stdout: "Logged in as zubair@gmail.com\n"}, "firebase", "login:list")
	fake3.Set(FakeResponse{Stdout: "Authentication cancelled\n", Exit: 1}, "firebase", "login:add")
	ch, _ = c.Login(context.Background(), LoginRequest{Name: "work"})
	if fin = lastEvent(drainEvents(t, ch)); !errors.Is(fin.Err, accounts.ErrSignInCancelled) || fin.Account != nil {
		t.Fatalf("cancelled = %+v", fin)
	}
}
