package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

const wranglerAuthHelp = `wrangler auth

🔐 Manage authentication profiles

COMMANDS
  wrangler auth create <name>          Create or re-authenticate a named auth profile [experimental]
  wrangler auth delete <name>          Delete a named auth profile [experimental]
  wrangler auth activate <name> [dir]  Bind a named auth profile to a directory [experimental]
  wrangler auth deactivate [dir]       Remove the auth profile binding from a directory [experimental]
  wrangler auth list                   List all auth profiles [experimental]
`

const wranglerList = `┌─────────┬───────────────────┐
│ Profile │ Bound Directories │
├─────────┼───────────────────┤
│ default │ -                 │
├─────────┼───────────────────┤
│ work    │ C:\Work           │
├─────────┼───────────────────┤
│ my_oss  │ -                 │
└─────────┴───────────────────┘
`

// newCloudflareForTest gives Wrangler a config folder under a temp
// XDG_CONFIG_HOME, and a fake wrangler whose activate/deactivate really
// edit the bindings file, as Wrangler 4.143's code does.
func newCloudflareForTest(t *testing.T) (*cloudflareAdapter, Deps, *FakeRunner, string) {
	t.Helper()
	d, fake := toolDeps(t)
	xdg := shortTemp(t)
	d.Getenv = func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return xdg
		}
		return ""
	}
	path := filepath.Join(xdg, ".wrangler", "profiles", "directory-bindings.json")
	fake.Set(FakeResponse{Stdout: " ⛅️ wrangler 4.143.0\n"}, "wrangler", "--version")
	fake.Set(FakeResponse{Stdout: wranglerAuthHelp}, "wrangler", "auth", "--help")
	fake.Func = func(c Cmd) (Result, error) {
		if len(c.Args) >= 3 && c.Args[0] == "auth" && (c.Args[1] == "activate" || c.Args[1] == "deactivate") {
			b := readBindingsForTest(t, path)
			if c.Args[1] == "activate" {
				b[c.Args[3]] = c.Args[2]
			} else {
				if _, ok := b[c.Args[2]]; !ok {
					return Result{Stderr: []byte("✘ [ERROR] No profile is bound to \"x\". Nothing to deactivate.\n"), ExitCode: 1}, nil
				}
				delete(b, c.Args[2])
			}
			writeBindingsForTest(t, path, b)
			return Result{Stdout: []byte("ok\n")}, nil
		}
		return Result{}, errors.New("no answer for " + Key(c.Name, c.Args...))
	}
	return newCloudflare(d).(*cloudflareAdapter), d, fake, path
}

func readBindingsForTest(t *testing.T, path string) map[string]string {
	t.Helper()
	b := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &b); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func writeBindingsForTest(t *testing.T, path string, b map[string]string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	data, _ := json.MarshalIndent(b, "", "\t")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCloudflareCapabilitiesAreBetaWithoutEverywhere(t *testing.T) {
	a, _, _, _ := newCloudflareForTest(t)
	c := a.Capabilities(context.Background())
	if !c.Beta || !c.FolderRules || !c.JustOnce || !c.AddAccount || c.Everywhere || !strings.Contains(c.Why, "wrangler login") {
		t.Fatalf("caps = %+v", c)
	}
	b, _, fake, _ := newCloudflareForTest(t)
	fake.Set(FakeResponse{Stdout: "wrangler login\nwrangler logout\n", Exit: 1}, "wrangler", "auth", "--help")
	c = b.Capabilities(context.Background())
	if !c.Beta || c.FolderRules || !strings.Contains(c.Why, "4.143.0") {
		t.Fatalf("old caps = %+v", c)
	}
}

func TestCloudflareProfilesAndAccounts(t *testing.T) {
	if got := parseWranglerProfiles([]byte(wranglerList)); strings.Join(got, ",") != "default,work,my_oss" {
		t.Fatalf("profiles = %q", got)
	}
	if got := parseWranglerProfiles([]byte("No profiles found. Run `wrangler login` to get started.\n")); len(got) != 0 {
		t.Fatalf("none = %q", got)
	}
	a, _, fake, _ := newCloudflareForTest(t)
	fake.Set(FakeResponse{Stdout: wranglerList}, "wrangler", "auth", "list")
	list, err := a.Accounts(context.Background(), nil)
	// default, then work (detected). my_oss is not a name Devpit can use.
	if err != nil || len(list) != 2 || list[1].Name != "work" || list[1].ImportedFrom != "detected" {
		t.Fatalf("accounts = %+v %v", list, err)
	}
}

func TestCloudflareLaunchAndNames(t *testing.T) {
	l, err := LaunchFor(accounts.Account{Tool: accounts.ToolCloudflare, Name: "work"})
	if err != nil || len(l.Args) != 2 || l.Args[0] != "--profile" || l.Args[1] != "work" {
		t.Fatalf("launch = %+v %v", l, err)
	}
	if l, err := LaunchFor(accounts.Account{Tool: accounts.ToolCloudflare, Name: "default"}); err != nil || !l.IsZero() {
		t.Fatalf("default = %+v %v", l, err)
	}
	a, _, _, _ := newCloudflareForTest(t)
	if _, err := a.Login(context.Background(), LoginRequest{Name: "Staging"}); !errors.Is(err, accounts.ErrReservedName) {
		t.Fatalf("staging: %v", err)
	}
}

func TestCloudflareWhoAmI(t *testing.T) {
	a, _, fake, _ := newCloudflareForTest(t)
	fake.Set(FakeResponse{Stdout: `{"loggedIn":true,"authType":"OAuth Token","email":"zubair@work.com","accounts":[{"id":"0123","name":"Work"}],"tokenPermissions":["workers:write"]}`},
		"wrangler", "whoami", "--json", "--profile", "work")
	id, err := a.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolCloudflare, Name: "work"})
	if err != nil || !id.SignedIn() || id.Email() != "zubair@work.com" || id.Fields().Org != "Work" {
		t.Fatalf("work = %+v %v", id.Fields(), err)
	}
	c, _ := ran(fake, "wrangler", "whoami", "--json", "--profile", "work")
	if _, _, unset := EnvOf(c, "CLOUDFLARE_API_TOKEN"); !unset {
		t.Fatal("whoami must describe the profile, not a token in the environment")
	}
	fake.Set(FakeResponse{Stdout: `{"loggedIn": false}`, Exit: 1}, "wrangler", "whoami", "--json", "--profile", "default")
	id, err = a.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolCloudflare, Name: "default"})
	if err != nil || id.State() != accounts.StateNotSignedIn || !strings.Contains(id.Fields().Note, "wrangler login") {
		t.Fatalf("default = %+v %v", id.Fields(), err)
	}
}

func TestCloudflareOp(t *testing.T) {
	work := accounts.Change{Tool: accounts.ToolCloudflare, Account: "work", Scope: accounts.FolderScope(`Q:\Work`)}
	op, warn, err := cloudflareOp(work, map[string]string{})
	if err != nil || op == nil || op.Dir != `Q:\Work` || op.Before != "" || op.After != "work" || len(warn) == 0 {
		t.Fatalf("add = %+v %q %v", op, warn, err)
	}
	if op, _, _ = cloudflareOp(work, map[string]string{`Q:\Work`: "work"}); op != nil {
		t.Fatalf("already bound = %+v", op)
	}
	op, warn, _ = cloudflareOp(work, map[string]string{`Q:\Work`: "old", `q:\work`: "x"})
	if op == nil || op.Before != "old" || !strings.Contains(strings.Join(warn, " "), `q:\work`) {
		t.Fatalf("change = %+v %q", op, warn)
	}

	// Default inside a folder Wrangler binds to another profile cannot be
	// expressed in Wrangler: refused, with the reason.
	def := accounts.Change{Tool: accounts.ToolCloudflare, Account: "default", Scope: accounts.FolderScope(`Q:\Work\oss`)}
	if _, _, err = cloudflareOp(def, map[string]string{`Q:\Work`: "work"}); !errors.Is(err, accounts.ErrNotSupported) {
		t.Fatalf("default inside a binding: %v", err)
	}
	// Q:\Workshop is not inside Q:\Work.
	def.Scope = accounts.FolderScope(`Q:\Workshop`)
	if op, _, err = cloudflareOp(def, map[string]string{`Q:\Work`: "work"}); err != nil || op != nil {
		t.Fatalf("sibling: %+v %v", op, err)
	}
	// Removing a rule removes its binding.
	rm := accounts.Change{Tool: accounts.ToolCloudflare, Remove: true, Scope: accounts.FolderScope(`Q:\Work`)}
	if op, _, err = cloudflareOp(rm, map[string]string{`Q:\Work`: "work"}); err != nil || op == nil || op.After != "" || op.Before != "work" {
		t.Fatalf("remove: %+v %v", op, err)
	}
	if _, _, err = cloudflareOp(accounts.Change{Tool: accounts.ToolCloudflare, Account: "work", Scope: accounts.EverywhereScope()}, nil); !errors.Is(err, accounts.ErrNotSupported) {
		t.Fatalf("everywhere: %v", err)
	}
}

func TestCloudflareApplyAndUndoMirrorWranglerBindings(t *testing.T) {
	if filepath.Separator != '\\' {
		t.Skip("Wrangler's folder bindings are Windows paths here")
	}
	a, d, fake, path := newCloudflareForTest(t)
	eng := accounts.NewEngine(d.Paths)
	eng.Handlers = CloudflareHandlers(d)
	if _, err := eng.AddAccount(accounts.Account{Tool: accounts.ToolCloudflare, Name: "work", Email: "z@work.com"}); err != nil {
		t.Fatal(err)
	}
	// A mixed-case folder typed in lower case: Wrangler gets the real case.
	site := filepath.Join(shortTemp(t), "MyWork", "Site")
	if err := os.MkdirAll(site, 0o700); err != nil {
		t.Fatal(err)
	}
	trueDir := wranglerTrueCase(site)
	typed := strings.ToLower(site)
	res, _ := eng.Load()
	p, err := a.Plan(accounts.PreviewInput{Store: res.Store, Change: accounts.Change{Tool: accounts.ToolCloudflare, Account: "work", Scope: accounts.FolderScope(typed)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Edits) != 2 || p.Edits[1].Path != path || !strings.Contains(strings.Join(p.Edits[1].Lines, "\n"), "wrangler auth activate work") {
		t.Fatalf("edits = %+v", p.Edits)
	}
	fin := lastEvent(drainEvents(t, Apply(context.Background(), eng, a, p)))
	if fin.State != accounts.StepDone || fin.EntryID == "" {
		t.Fatalf("final = %+v", fin)
	}
	if b := readBindingsForTest(t, path); len(b) != 1 || b[trueDir] != "work" {
		t.Fatalf("bindings = %+v, want %s", b, trueDir)
	}
	c, _ := ran(fake, "wrangler", "auth", "activate", "work", trueDir)
	if _, _, unset := EnvOf(c, "CLOUDFLARE_API_TOKEN"); !unset {
		t.Fatal("Wrangler refuses to manage profiles while CLOUDFLARE_API_TOKEN is set; Devpit's own run must go without it")
	}
	if probs, err := CloudflareDrift(d, mustLoad(t, eng)); err != nil || len(probs) != 0 {
		t.Fatalf("drift after apply = %+v %v", probs, err)
	}

	// Undo removes exactly that binding.
	if _, err := eng.Undo(); err != nil {
		t.Fatal(err)
	}
	if b := readBindingsForTest(t, path); len(b) != 0 {
		t.Fatalf("after undo = %+v", b)
	}

	// Without the handler on the Engine nothing is run and the change fails.
	eng2 := accounts.NewEngine(d.Paths)
	res, _ = eng2.Load()
	p, _ = a.Plan(accounts.PreviewInput{Store: res.Store, Change: accounts.Change{Tool: accounts.ToolCloudflare, Account: "work", Scope: accounts.FolderScope(site)}})
	before := len(fake.Calls())
	if fin := lastEvent(drainEvents(t, Apply(context.Background(), eng2, a, p))); fin.State != accounts.StepFailed {
		t.Fatalf("without handler = %+v", fin)
	}
	for _, c := range fake.Calls()[before:] {
		if len(c.Args) > 1 && c.Args[1] == "activate" {
			t.Fatal("ran activate without a way to undo it")
		}
	}
}

func mustLoad(t *testing.T, eng *accounts.Engine) *accounts.Store {
	t.Helper()
	res, err := eng.Load()
	if err != nil {
		t.Fatal(err)
	}
	return res.Store
}

func TestCloudflareDrift(t *testing.T) {
	_, d, _, path := newCloudflareForTest(t)
	writeBindingsForTest(t, path, map[string]string{
		`Q:\Work`: "work", `Q:\Other`: "work", `q:\client`: "client", `Q:\Hand`: "personal",
	})
	s := accounts.NewStore()
	_ = s.AddAccount(accounts.Account{Tool: accounts.ToolCloudflare, Name: "work"})
	_ = s.AddAccount(accounts.Account{Tool: accounts.ToolCloudflare, Name: "client"})
	_ = s.SetRule(`Q:\Work`, accounts.ToolCloudflare, "work")
	_ = s.SetRule(`Q:\Other`, accounts.ToolCloudflare, "client")
	_ = s.SetRule(`Q:\Client`, accounts.ToolCloudflare, "client")
	probs, err := CloudflareDrift(d, s)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, p := range probs {
		if p.Kind != ProblemWranglerDrift || p.Tool != accounts.ToolCloudflare {
			t.Fatalf("problem = %+v", p)
		}
		msgs = append(msgs, p.Message)
	}
	all := strings.Join(msgs, "\n")
	if len(probs) != 3 || !strings.Contains(all, `Q:\Other, but Wrangler binds it to the profile work`) ||
		!strings.Contains(all, "letter case") || !strings.Contains(all, `Wrangler binds Q:\Hand to the profile personal, but Devpit has no rule`) {
		t.Fatalf("drift =\n%s", all)
	}
}

func TestReadWranglerBindingsKeepsOnlyFolderToProfile(t *testing.T) {
	p := filepath.Join(shortTemp(t), "b.json")
	_ = os.WriteFile(p, []byte(`{"Q:\\Work":"work","Q:\\X":{"token":"PLANTED"},"Q:\\Y":"bad name!"}`), 0o600)
	b, err := readWranglerBindings(p)
	if err != nil || len(b) != 1 || b[`Q:\Work`] != "work" {
		t.Fatalf("bindings = %+v %v", b, err)
	}
	_ = os.WriteFile(p, []byte(`not json PLANTED`), 0o600)
	if _, err := readWranglerBindings(p); err == nil || strings.Contains(err.Error(), "PLANTED") {
		t.Fatalf("bad file: %v", err)
	}
	if b, err := readWranglerBindings(filepath.Join(shortTemp(t), "none.json")); err != nil || len(b) != 0 {
		t.Fatalf("missing: %v %v", b, err)
	}
}
