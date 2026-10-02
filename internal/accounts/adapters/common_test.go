package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

func TestParseVersion(t *testing.T) {
	cases := map[string]string{
		"2.1.287 (Claude Code)":           "2.1.287",
		"gh version 2.102.0 (2026-09-30)": "2.102.0",
		"git version 2.55.0.windows.1":    "2.55.0",
		"Vercel CLI 60.1.3\n60.1.3":       "60.1.3",
		" ⛅️ wrangler 4.143.0":            "4.143.0",
		"15.32.1":                         "15.32.1",
		"v2.40":                           "2.40.0",
	}
	for in, want := range cases {
		v, ok := ParseVersion(in)
		if !ok || v.String() != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %s", in, v, ok, want)
		}
	}
	if _, ok := ParseVersion("no version here 7"); ok {
		t.Error("a lone number is not a version")
	}
	v, _ := ParseVersion("gh version 2.39.9")
	if v.AtLeast("2.40") || !v.AtLeast("2.39") {
		t.Error("AtLeast")
	}
}

func TestCheckArgsRefusesCommandsThatPrintOrCarryTokens(t *testing.T) {
	refused := [][]string{
		{"firebase", "login:list", "--json"},
		{"firebase", "--json", "login:list"},
		{"firebase", "login:list", "-j"},
		{"firebase.exe", "login:list", "--json=true"},
		{"gh", "auth", "status", "--show-token"},
		{"gh", "auth", "status", "-t"},
		{"gh", "auth", "token", "--show-token"},
		{"gh", "auth", "switch", "--user", "zubair"},
		{"supabase", "login", "--token", "sbp_" + strings.Repeat("0a", 20)},
		{"vercel", "--token", "vcp_" + strings.Repeat("Ab1", 10)},
	}
	for _, c := range refused {
		if err := CheckArgs(c[0], c[1:]); !errors.Is(err, ErrForbiddenCommand) {
			t.Errorf("not refused: %v", c)
		}
		// The fake runner refuses them too, so no adapter test can pass by
		// building one.
		f := &FakeRunner{Func: func(Cmd) (Result, error) { return Result{}, nil }}
		if _, err := f.Run(context.Background(), Cmd{Name: c[0], Args: c[1:]}); !errors.Is(err, ErrForbiddenCommand) {
			t.Errorf("fake runner ran %v", c)
		}
	}
	allowed := [][]string{
		{"firebase", "login:list"},
		{"gh", "auth", "status", "--json", "hosts"},
		{"gh", "auth", "token", "--hostname", "github.com", "--user", "zubair"},
		{"claude", "auth", "login", "--email", "zubair@work.com"},
		{"vercel", "whoami", "--json", "--global-config", `C:\Users\z\.devpit\accounts\vercel\work`},
	}
	for _, c := range allowed {
		if err := CheckArgs(c[0], c[1:]); err != nil {
			t.Errorf("refused %v: %v", c, err)
		}
	}
}

// Every tool has a real adapter now. With tools that answer every probe
// with nothing but a version, each one is found, says plainly why it cannot
// switch (too old), never claims more than the static Supports table, and
// still starts its default account with only its own folder variable
// cleared. A named account it cannot apply statically fails with a reason,
// never a silent zero launch.
func TestRegistryCoversEveryToolInOrder(t *testing.T) {
	fake := &FakeRunner{Missing: map[string]bool{"convex": true, "npx": true}}
	fake.Func = func(c Cmd) (Result, error) {
		return Result{Stdout: []byte(c.Name + " version 1.2.3\n")}, nil
	}
	d := Deps{Runner: fake, Home: t.TempDir(), Getenv: func(string) string { return "" }}
	all := All(d)
	if len(all) != 8 {
		t.Fatalf("%d adapters", len(all))
	}
	for i, a := range all {
		tool := a.Tool()
		if tool != accounts.Tools()[i] {
			t.Fatalf("adapter %d is %s, want %s", i, tool, accounts.Tools()[i])
		}
		in := a.Installed(context.Background())
		caps := a.Capabilities(context.Background())
		static := Supports(tool)
		if tool == accounts.ToolConvex {
			if in.Found || !caps.ShowOnly || !strings.Contains(caps.Why, "per project") || !static.ShowOnly {
				t.Errorf("convex: %+v %+v", in, caps)
			}
			continue
		}
		if !in.Found || in.Version != "1.2.3" {
			t.Errorf("%s installed = %+v", tool, in)
		}
		if caps.FolderRules || caps.Everywhere || caps.JustOnce || caps.Why == "" {
			t.Errorf("%s caps = %+v: a tool too old to switch must say so plainly", tool, caps)
		}
		if !static.FolderRules || !static.AddAccount {
			t.Errorf("%s: Supports = %+v; every tool but Convex takes folder rules", tool, static)
		}
		if l, err := a.Launch(accounts.Account{Tool: tool, Name: "default"}); err != nil || len(l.Env) != 0 || len(l.Args) != 0 {
			t.Errorf("%s default launch: %+v %v", tool, l, err)
		} else {
			for _, k := range l.Unset {
				managed := false
				for _, v := range a.EnvOverrides() {
					managed = managed || (v.Name == k && v.Managed)
				}
				if !managed {
					t.Errorf("%s default launch clears %s, which is not its own folder variable", tool, k)
				}
			}
		}
		l, err := a.Launch(accounts.Account{Tool: tool, Name: "work"})
		switch tool {
		case accounts.ToolCloudflare:
			if err != nil || strings.Join(l.Args, " ") != "--profile work" {
				t.Errorf("cloudflare launch: %+v %v", l, err)
			}
		default:
			// No folder, no address, no login recorded, or (Git) no such
			// thing as a per-process account: always a reason.
			if err == nil {
				t.Errorf("%s launched a named account with nothing recorded: %+v", tool, l)
			}
		}
		accts, _ := a.Accounts(context.Background(), nil)
		if len(accts) == 0 || !accts[0].IsDefault() {
			t.Errorf("%s accounts = %+v", tool, accts)
		}
	}
	if !Supports(accounts.ToolCloudflare).Beta || Supports(accounts.ToolCloudflare).Everywhere || Supports(accounts.ToolGit).JustOnce {
		t.Error("static support table")
	}
	if a, ok := For(d, accounts.ToolCloudflare); !ok || !a.Capabilities(context.Background()).Beta {
		t.Error("cloudflare is beta")
	}
	// Not installed: Found false, no error.
	fake.Missing["vercel"] = true
	if in := newVercel(Deps{Runner: fake}).Installed(context.Background()); in.Found {
		t.Error("vercel should be missing")
	}
}

// Wrangler is never shimmed: its own folder bindings apply the rules, and a
// shim adding --profile would break `wrangler auth …`. Nothing assumes every
// tool has a shim.
func TestWranglerHasNoShim(t *testing.T) {
	if accounts.ToolCloudflare.ShimName() != "" {
		t.Fatal("cloudflare has a shim name")
	}
	if _, ok := accounts.ToolForShim("wrangler.exe"); ok {
		t.Fatal("wrangler.exe maps back to a tool")
	}
	s := accounts.NewStore()
	if err := s.AddAccount(accounts.Account{Tool: accounts.ToolCloudflare, Name: "work"}); err != nil {
		t.Fatal(err)
	}
	if s.NeedsShim(accounts.ToolCloudflare) {
		t.Fatal("a Cloudflare account must not ask for a shim")
	}
	// Just this once still works, through the launcher.
	if l, err := LaunchFor(accounts.Account{Tool: accounts.ToolCloudflare, Name: "work"}); err != nil || strings.Join(l.Args, " ") != "--profile work" {
		t.Fatalf("%+v %v", l, err)
	}
}

// Every custom effect kind an adapter records has an undo handler in
// Handlers.
func TestHandlersCoverEveryEffectKind(t *testing.T) {
	h := Handlers(Deps{})
	for _, k := range EffectKinds() {
		if h[k] == nil {
			t.Errorf("no handler for %s", k)
		}
	}
}

func TestDecodeJSONSkipsBanners(t *testing.T) {
	var v struct {
		LoggedIn bool `json:"loggedIn"`
	}
	if err := DecodeJSON([]byte("Update available!\n{\"loggedIn\": true}\n"), &v); err != nil || !v.LoggedIn {
		t.Fatalf("%v %v", v, err)
	}
	if err := DecodeJSON([]byte("no json"), &v); err == nil {
		t.Fatal("no json accepted")
	}
	if got, ok := After([]byte("  Logged in as zubair@work.com\n"), "Logged in as"); !ok || got != "zubair@work.com" {
		t.Fatalf("After = %q %v", got, ok)
	}
}
