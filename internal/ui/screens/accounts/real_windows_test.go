package accounts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/accounts/shims"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
)

// The acceptance path once more, through the real engine: real
// accounts.toml, journal, lock, account folders and shim folder, all in a
// temporary folder, with Claude Code faked by the engine's own fake runner
// (no tool runs, no browser opens, nothing outside the temporary folder is
// read or written, the PATH is not touched).
func TestRealEngineAddsAClaudeAccountAndUsesItInOneFolder(t *testing.T) {
	root, err := os.MkdirTemp("", "dpacc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "Work")
	for _, d := range []string{home, work, filepath.Join(root, "shims")} {
		if merr := os.MkdirAll(d, 0o700); merr != nil {
			t.Fatal(merr)
		}
	}
	src := filepath.Join(root, "devpit-shim.exe")
	if werr := os.WriteFile(src, []byte("shim"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	env := map[string]string{}
	m := &shims.Manager{Dir: filepath.Join(root, "shims"), Source: src, Record: filepath.Join(root, "cfg", "shims.json"), Getenv: func(k string) string { return env[k] }}

	fake := &adapters.FakeRunner{}
	fake.Set(adapters.FakeResponse{Stdout: "2.1.287 (Claude Code)\n"}, "claude", "--version")
	fake.Set(adapters.FakeResponse{Stdout: "Commands:\n  login\n  logout\n  status\n"}, "claude", "auth", "--help")
	fake.Set(adapters.FakeResponse{}, "claude", "auth", "login")
	fake.Func = func(c adapters.Cmd) (adapters.Result, error) {
		if c.Name == "claude" && strings.Join(c.Args, " ") == "auth status --json" {
			if _, set, _ := adapters.EnvOf(c, "CLAUDE_CONFIG_DIR"); set {
				return adapters.Result{Stdout: []byte(`{"loggedIn":true,"email":"me@second.example"}`)}, nil
			}
			return adapters.Result{Stdout: []byte(`{"loggedIn":true,"email":"me@home.example"}`)}, nil
		}
		return adapters.Result{}, fmt.Errorf("%s: %w", c.Name, accounts.ErrNotFoundTool)
	}
	paths := accounts.PathsIn(filepath.Join(root, "cfg"), filepath.Join(home, ".devpit", "accounts"))
	svc, err := service.Open(service.Options{
		Paths: paths, Home: home, Runner: fake, Shims: m,
		Getenv: func(k string) string { return env[k] },
		LookPath: func(name string) (string, error) {
			if name == "claude" {
				return `C:\fake\claude.exe`, nil
			}
			return "", fmt.Errorf("%s: %w", name, accounts.ErrNotFoundTool)
		},
		Now: func() time.Time { return time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}

	// The real engine reads real (temporary) files, which can take longer
	// than the demo's instant answers on a busy machine.
	h := &harness{t: t, ctx: testCtx(100, 30, icons.TierUnicode), wait: time.Second}
	opts := testOptions(nil, &h.copied)
	opts.Folder = work
	opts.Open = func() (Service, error) { return svc, nil }
	opts.Stat = os.Stat
	h.open(opts)

	h.mustSee("Claude Code", "default").keys("enter")
	h.clickText("Sign in with another account").keys("enter")
	h.mustSee("Signed in as me@second.example", "What should Devpit call this account?")
	h.keys("enter")
	// The setup question for the new account; this test does not bring
	// anything over.
	if strings.Contains(h.view(), "Not now") {
		h.clickText("Not now")
	} else {
		h.keys("enter")
	}
	h.mustSee("This folder").keys("enter")
	h.mustSee("Claude Code will use", "Make this change?").keys("y")
	h.mustSee("[u] undo")

	st, err := accounts.ReadFile(paths.Store)
	if err != nil {
		t.Fatal(err)
	}
	accts := st.AccountsFor(accounts.ToolClaude)
	if len(accts) != 1 || accts[0].Email != "me@second.example" {
		t.Fatalf("accounts.toml has %+v", accts)
	}
	r, ok := st.Rule(work)
	if !ok || !strings.EqualFold(r.Accounts[accounts.ToolClaude], accts[0].Name) {
		t.Fatalf("no rule for %s on %s: %+v", accts[0].Name, work, st.Rules)
	}
	if _, err := os.Stat(accts[0].Dir); err != nil {
		t.Fatalf("the account's folder is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "shims", "claude.exe")); err != nil {
		t.Fatalf("no shim for Claude Code after its first rule: %v", err)
	}

	// Undo from the done card takes the rule back on disk.
	h.keys("u", "y")
	st, _ = accounts.ReadFile(paths.Store)
	if _, ok := st.Rule(work); ok {
		t.Fatal("undo left the rule")
	}
}
