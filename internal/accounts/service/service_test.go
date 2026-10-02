package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/shims"
)

// world is a Service on temporary folders: a fake runner for every tool, a
// fake environment, a shim manager on a scratch folder that never touches
// the PATH, and claude and vercel "installed".
type world struct {
	t    *testing.T
	s    *Service
	fake *adapters.FakeRunner
	env  map[string]string
	root string
	home string
	work string // a folder with a Claude rule after addClaudeRule
}

func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "dps")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	// Rules are kept under long names; this world is not about 8.3 names
	// (TEMP is C:\Users\RUNNER~1\… on GitHub's runners).
	return accounts.LongPath(d)
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := shortDir(t)
	w := &world{t: t, root: root, home: filepath.Join(root, "home"), env: map[string]string{}}
	for _, d := range []string{w.home, filepath.Join(root, "shims"), filepath.Join(root, "Work", "api"), filepath.Join(root, "Other")} {
		must(t, os.MkdirAll(d, 0o700))
	}
	w.work = filepath.Join(root, "Work")
	src := filepath.Join(root, "devpit-shim.exe")
	must(t, os.WriteFile(src, []byte("shim v1"), 0o600))
	m := &shims.Manager{
		Dir: filepath.Join(root, "shims"), Source: src, Record: filepath.Join(root, "cfg", "shims.json"),
		Getenv: func(k string) string { return w.env[k] },
	}
	w.fake = &adapters.FakeRunner{}
	w.fake.Set(adapters.FakeResponse{Stdout: "2.1.287 (Claude Code)\n"}, "claude", "--version")
	w.fake.Set(adapters.FakeResponse{Stdout: "Commands:\n  login\n  logout\n  status\n"}, "claude", "auth", "--help")
	w.fake.Set(adapters.FakeResponse{Stdout: "Vercel CLI 60.1.3\n"}, "vercel", "--version")
	w.fake.Set(adapters.FakeResponse{Stdout: "  -Q, --global-config <DIR>\n"}, "vercel", "whoami", "--help")
	w.fake.Func = func(c adapters.Cmd) (adapters.Result, error) {
		if c.Name == "claude" && strings.Join(c.Args, " ") == "auth status --json" {
			dir, set, _ := adapters.EnvOf(c, "CLAUDE_CONFIG_DIR")
			switch {
			case !set:
				return adapters.Result{Stdout: []byte(`{"loggedIn":true,"email":"me@home.com"}`)}, nil
			case strings.HasSuffix(dir, "work"):
				return adapters.Result{Stdout: []byte(`{"loggedIn":true,"email":"` + w.env["FAKE_WORK_EMAIL"] + `"}`)}, nil
			}
			return adapters.Result{Stdout: []byte(`{"loggedIn":true,"email":"other@x.com"}`)}, nil
		}
		return adapters.Result{}, fmt.Errorf("%s: %w", c.Name, accounts.ErrNotFoundTool)
	}
	w.env["FAKE_WORK_EMAIL"] = "z@work.com"
	s, err := Open(Options{
		Paths:  accounts.PathsIn(filepath.Join(root, "cfg"), filepath.Join(w.home, ".devpit", "accounts")),
		Home:   w.home,
		Runner: w.fake,
		Shims:  m,
		Getenv: func(k string) string { return w.env[k] },
		LookPath: func(name string) (string, error) {
			if name == "claude" || name == "vercel" {
				return `C:\fake\` + name + ".exe", nil
			}
			return "", fmt.Errorf("%s: %w", name, accounts.ErrNotFoundTool)
		},
		Now: func() time.Time { return time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC) },
	})
	must(t, err)
	w.s = s
	return w
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// addAccount saves an account with a folder.
func (w *world) addAccount(tool accounts.Tool, name, email string) accounts.Account {
	w.t.Helper()
	dir := w.s.Deps.Paths.AccountDir(tool, name)
	must(w.t, os.MkdirAll(dir, 0o700))
	must(w.t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{}"), 0o600))
	a := accounts.Account{Tool: tool, Name: name, Email: email, Dir: dir}
	_, err := w.s.Engine.AddAccount(a)
	must(w.t, err)
	return a
}

func (w *world) apply(c accounts.Change) accounts.Preview {
	w.t.Helper()
	p, err := w.s.Plan(context.Background(), c)
	must(w.t, err)
	var last accounts.Event
	for ev := range w.s.Apply(context.Background(), p) {
		last = ev
	}
	if last.State != accounts.StepDone || last.EntryID == "" {
		w.t.Fatalf("apply %+v: %+v %v", c, last, last.Err)
	}
	return p
}

// Every effect kind any package records, read from the source, has an undo
// handler on the engine Open builds, so an undo or a recovery never meets a
// kind it cannot put back.
func TestEveryEffectKindHasAHandler(t *testing.T) {
	w := newWorld(t)
	var kinds []string
	scan := func(dir string, want func(*ast.ValueSpec, string) []string) {
		fset := token.NewFileSet()
		entries, err := os.ReadDir(dir)
		must(t, err)
		var files []*ast.File
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			f, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
			must(t, perr)
			files = append(files, f)
		}
		{
			for _, f := range files {
				for _, d := range f.Decls {
					g, ok := d.(*ast.GenDecl)
					if !ok || g.Tok != token.CONST {
						continue
					}
					typ := ""
					for _, sp := range g.Specs {
						vs := sp.(*ast.ValueSpec)
						if vs.Type != nil {
							typ = exprString(vs.Type)
						}
						kinds = append(kinds, want(vs, typ)...)
					}
				}
			}
		}
	}
	values := func(vs *ast.ValueSpec) []string {
		var out []string
		for _, v := range vs.Values {
			if bl, ok := v.(*ast.BasicLit); ok && bl.Kind == token.STRING {
				s, _ := strconv.Unquote(bl.Value)
				out = append(out, s)
			}
		}
		return out
	}
	scan(filepath.Join("..", "adapters"), func(vs *ast.ValueSpec, typ string) []string {
		if typ == "accounts.EffectKind" {
			return values(vs)
		}
		return nil
	})
	scan(filepath.Join(".."), func(vs *ast.ValueSpec, typ string) []string {
		if typ == "EffectKind" {
			return values(vs)
		}
		return nil
	})
	scan(filepath.Join("..", "claudeshare"), func(vs *ast.ValueSpec, typ string) []string {
		if typ == "Op" {
			var out []string
			for _, v := range values(vs) {
				if v == "mkdir" {
					continue // recorded through Txn.MakeDir as the engine's own folder effect
				}
				out = append(out, "claudeshare."+v)
			}
			return out
		}
		return nil
	})
	if len(kinds) < 5 {
		t.Fatalf("found only %v", kinds)
	}
	for _, k := range kinds {
		if k == string(accounts.EffectFile) || k == string(accounts.EffectDir) {
			continue
		}
		if w.s.Engine.Handlers[accounts.EffectKind(k)] == nil {
			t.Errorf("no undo handler for the effect kind %q", k)
		}
	}
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	}
	return ""
}

// Status and Overview say which account is used, why, and the problems
// with their fixes, without running Claude Code; the JSON has no token.
func TestStatusOverviewAndJSON(t *testing.T) {
	w := newWorld(t)
	w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	w.apply(accounts.Change{Tool: accounts.ToolClaude, Account: "work", Scope: accounts.FolderScope(w.work)})
	before := len(w.fake.Calls())

	st, err := w.s.Status(context.Background(), accounts.ToolClaude, filepath.Join(w.work, "api"))
	must(t, err)
	if st.Display != "work (z@work.com)" || st.Why != "folder rule: "+st.Resolution.RuleFolder || st.EverywhereDisplay != "default (not checked yet)" {
		t.Fatalf("status = %+v", st)
	}
	out, err := w.s.Status(context.Background(), accounts.ToolClaude, filepath.Join(w.root, "Other"))
	must(t, err)
	if out.Display != "default (not checked yet)" || out.Why != "everywhere" {
		t.Fatalf("outside = %+v", out)
	}
	for _, c := range w.fake.Calls()[before:] {
		if c.Name == "claude" {
			t.Fatalf("Status ran claude: %v", c.Args)
		}
	}

	w.env["CLAUDE_CONFIG_DIR"] = `C:\stray`
	w.env["GH_TOKEN"] = "ghp_" + strings.Repeat("Ab1", 12)
	ov, err := w.s.Overview(context.Background(), w.work)
	must(t, err)
	text := ov.Text()
	for _, want := range []string{"Claude Code  work (z@work.com)", "folder rule:", "CLAUDE_CONFIG_DIR is set", "Fix: ", "Firebase", "not installed"} {
		if !strings.Contains(text, want) {
			t.Errorf("overview lacks %q:\n%s", want, text)
		}
	}
	data, err := json.Marshal(ov.JSON())
	must(t, err)
	if strings.Contains(string(data), w.env["GH_TOKEN"]) || strings.Contains(text, w.env["GH_TOKEN"]) {
		t.Fatal("a planted token reached the output")
	}
	var back OverviewJSON
	must(t, json.Unmarshal(data, &back))
	if len(back.Tools) != 8 || back.Tools[0].Tool != "claude" || back.Tools[0].Account.Name != "work" || back.Tools[0].Reason != "folder rule" {
		t.Fatalf("json = %s", data)
	}
}

// Apply saves the rule, then sets up the shim; one undo puts everything
// back. A group made by ApplyAll is undone at once.
func TestApplySyncsShimsAndUndoGroups(t *testing.T) {
	w := newWorld(t)
	w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	w.addAccount(accounts.ToolVercel, "work", "z@work.com")
	p := w.apply(accounts.Change{Tool: accounts.ToolClaude, Account: "wo", Scope: accounts.FolderScope(w.work)})
	if !strings.Contains(p.Text(), "In "+p.Change.Scope.Folder+" and every folder inside it, Claude Code will use work (z@work.com).") {
		t.Fatalf("preview:\n%s", p.Text())
	}
	if _, err := os.Stat(w.s.Shims.ShimPath(accounts.ToolClaude)); err != nil {
		t.Fatal("no claude shim after the first rule")
	}
	info, err := w.s.UndoPreview()
	must(t, err)
	if len(info.Entries) != 1 || !strings.Contains(strings.Join(info.Lines, "\n"), "Claude Code uses work in") {
		t.Fatalf("undo preview = %+v", info)
	}
	_, err = w.s.Undo(context.Background(), nil)
	must(t, err)

	st, _, _ := w.s.Load()
	tools := ToolsWithAccount(st, "work")
	if len(tools) != 2 {
		t.Fatalf("tools with work = %v", tools)
	}
	previews, skipped, err := w.s.PlanAll(context.Background(), "work", accounts.EverywhereScope(), tools)
	must(t, err)
	if len(previews) != 2 || len(skipped) != 0 {
		t.Fatalf("previews %d, skipped %v", len(previews), skipped)
	}
	must(t, w.s.ApplyAll(context.Background(), previews, func(accounts.Event) {}))
	st, _, _ = w.s.Load()
	if st.Everywhere[accounts.ToolClaude] != "work" || st.Everywhere[accounts.ToolVercel] != "work" {
		t.Fatalf("everywhere = %v", st.Everywhere)
	}
	info, err = w.s.UndoPreview()
	must(t, err)
	if len(info.Entries) != 2 {
		t.Fatalf("the group's undo covers %d changes", len(info.Entries))
	}
	res, err := w.s.Undo(context.Background(), nil)
	must(t, err)
	st, _, _ = w.s.Load()
	if len(res.Entries) != 2 || len(st.Everywhere) != 0 {
		t.Fatalf("after undo: %d undone, everywhere %v", len(res.Entries), st.Everywhere)
	}
	// Git has no "just this once"; Convex switches nothing.
	if _, err := w.s.Plan(context.Background(), accounts.Change{Tool: accounts.ToolConvex, Account: "default", Scope: accounts.EverywhereScope()}); !errors.Is(err, accounts.ErrNotSupported) {
		t.Fatalf("convex plan: %v", err)
	}
}

// Verify checks the account active here live (Claude Code included: it is
// in use here), reports a mismatch, and skips idle Claude Code accounts
// unless the person agreed.
func TestVerifyRespectsLiveCheckRisk(t *testing.T) {
	w := newWorld(t)
	w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	w.addAccount(accounts.ToolClaude, "oss", "z@oss.dev")
	w.apply(accounts.Change{Tool: accounts.ToolClaude, Account: "work", Scope: accounts.FolderScope(w.work)})
	w.env["PATH"] = w.s.Shims.Dir

	rep, err := w.s.Verify(context.Background(), VerifyOptions{Folder: w.work})
	must(t, err)
	if rep.Mismatch || rep.Checks[0].Status != VerifyOK || rep.Checks[0].How != "live" {
		t.Fatalf("report = %+v\n%s", rep.Checks[0], strings.Join(rep.Lines(), "\n"))
	}
	statusRuns := func() int {
		n := 0
		for _, c := range w.fake.Calls() {
			if c.Name == "claude" && len(c.Args) > 1 && c.Args[1] == "status" {
				n++
			}
		}
		return n
	}
	before := statusRuns()
	rep, err = w.s.Verify(context.Background(), VerifyOptions{Folder: w.work, All: true})
	must(t, err)
	if statusRuns()-before != 1 || len(rep.Skipped) != 1 || !strings.Contains(rep.Skipped[0], "default, oss") {
		t.Fatalf("idle accounts were checked without asking: runs %d, skipped %v", statusRuns()-before, rep.Skipped)
	}
	before = statusRuns()
	_, err = w.s.Verify(context.Background(), VerifyOptions{Folder: w.work, All: true, Risky: true})
	must(t, err)
	if statusRuns()-before != 3 {
		t.Fatalf("with the person's yes all 3 accounts are checked; ran %d", statusRuns()-before)
	}

	w.env["FAKE_WORK_EMAIL"] = "someone@else.com"
	rep, err = w.s.Verify(context.Background(), VerifyOptions{Folder: w.work})
	must(t, err)
	if !rep.Mismatch || rep.Checks[0].Status != VerifyMismatch {
		t.Fatalf("a different sign-in must be a mismatch: %+v", rep.Checks[0])
	}
	data, _ := json.Marshal(rep.JSON())
	if !strings.Contains(string(data), `"mismatch":true`) || !strings.Contains(string(data), `"status":"mismatch"`) {
		t.Fatalf("json = %s", data)
	}
}

// Cleanup removes only what Devpit added (rules, shims, the agent skill)
// and keeps account folders; undo brings the rules back.
func TestCleanupRemovesOnlyWhatDevpitAdded(t *testing.T) {
	w := newWorld(t)
	a := w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	w.apply(accounts.Change{Tool: accounts.ToolClaude, Account: "work", Scope: accounts.FolderScope(w.work)})
	targets, err := w.s.AgentTargets()
	must(t, err)
	_, err = w.s.AgentInstall(targets)
	must(t, err)
	mine := filepath.Join(w.root, "shims", "mine.exe")
	must(t, os.WriteFile(mine, []byte("not devpit"), 0o600))

	p, err := w.s.PlanCleanup(context.Background())
	must(t, err)
	if p.Empty || len(p.Shims) != 1 || len(p.AgentSkills) != 2 || p.Accounts.Rules != 1 {
		t.Fatalf("plan = %+v", p)
	}
	must(t, w.s.ApplyCleanup(context.Background(), p, false, nil))
	st, _, _ := w.s.Load()
	if len(st.Rules) != 0 || len(st.AccountsFor(accounts.ToolClaude)) != 1 {
		t.Fatalf("store after cleanup = %+v", st)
	}
	if _, serr := os.Stat(a.Dir); serr != nil {
		t.Fatal("the account folder was removed")
	}
	if _, serr := os.Stat(mine); serr != nil {
		t.Fatal("a file Devpit did not add was removed")
	}
	if _, serr := os.Stat(w.s.Shims.ShimPath(accounts.ToolClaude)); !errors.Is(serr, os.ErrNotExist) {
		t.Fatal("the shim is still there")
	}
	if left, _ := w.s.AgentInstalled(); len(left) != 0 {
		t.Fatalf("agent skills left: %v", left)
	}
	_, err = w.s.Undo(context.Background(), nil)
	must(t, err)
	st, _, _ = w.s.Load()
	if len(st.Rules) != 1 {
		t.Fatal("undo did not bring the rule back")
	}
	if _, err := os.Stat(w.s.Shims.ShimPath(accounts.ToolClaude)); err != nil {
		t.Fatal("undo did not bring the shim back")
	}
}

// The agent skill is written where it belongs, never over a devpit skill
// Devpit did not write, and removed only where it carries the marker.
func TestAgentSkillNeverOverwritesAForeignOne(t *testing.T) {
	w := newWorld(t)
	a := w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	foreign := filepath.Join(a.Dir, "skills", "devpit", "SKILL.md")
	must(t, os.MkdirAll(filepath.Dir(foreign), 0o700))
	must(t, os.WriteFile(foreign, []byte("mine"), 0o600))
	ts, err := w.s.AgentTargets()
	must(t, err)
	if len(ts) != 2 || ts[0].State != AgentCreate || ts[1].State != AgentForeign {
		t.Fatalf("targets = %+v", ts)
	}
	done, err := w.s.AgentInstall(ts)
	must(t, err)
	if len(done) != 1 {
		t.Fatalf("written = %v", done)
	}
	data, _ := os.ReadFile(done[0])
	for _, want := range []string{"name: devpit", "description:", AgentMarker, "--json", "--yes", "Exit codes", "Never read account folders"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
	removed, err := w.s.AgentRemove([]string{done[0], foreign})
	must(t, err)
	if len(removed) != 1 {
		t.Fatalf("removed = %v", removed)
	}
	if b, _ := os.ReadFile(foreign); string(b) != "mine" {
		t.Fatal("a foreign skill was touched")
	}
}

// Just this once: the account goes into the command's own environment (and
// DEVPIT_ONCE for shims below it); a tool whose account is a flag needs the
// command to start with the tool; Git has none.
func TestPrepareOnce(t *testing.T) {
	w := newWorld(t)
	a := w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	w.addAccount(accounts.ToolVercel, "work", "z@work.com")
	// cmd stands for any program; where there is none (Linux), the world's
	// LookPath finds it.
	look := w.s.Deps.LookPath
	w.s.Deps.LookPath = func(n string) (string, error) {
		if n == "cmd" {
			return `C:\Windows\System32\cmd.exe`, nil
		}
		return look(n)
	}
	c, err := w.s.PrepareOnce(context.Background(), accounts.ToolClaude, "wo", []string{"cmd", "/c", "echo"})
	must(t, err)
	env := strings.Join(c.Env, "\n")
	if !strings.Contains(env, "CLAUDE_CONFIG_DIR="+a.Dir) || !strings.Contains(env, "DEVPIT_ONCE=claude:work") {
		t.Fatal("the account is not in the command's environment")
	}
	if _, err := w.s.PrepareOnce(context.Background(), accounts.ToolVercel, "work", []string{"cmd", "/c", "echo"}); err == nil || !strings.Contains(err.Error(), "must start with vercel") {
		t.Fatalf("vercel through another program: %v", err)
	}
	if _, err := w.s.PrepareOnce(context.Background(), accounts.ToolGit, "default", []string{"git", "commit"}); !errors.Is(err, accounts.ErrNotSupported) {
		t.Fatalf("git once: %v", err)
	}
}

// No network except each tool's own sign-in and who-am-I: no Accounts
// engine package has an HTTP client, a socket of its own, or Bubble Tea.
func TestEngineHasNoNetworkOrUI(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	out, err := exec.Command(goBin, "list", "-deps", "../...").Output() //nolint:gosec // test
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, banned := range []string{"net/http", "net/rpc", "bubbletea", "lipgloss", "/internal/ui", "/internal/app", "cobra"} {
			if strings.Contains(dep, banned) {
				t.Errorf("the accounts engine imports %s (through %s)", banned, dep)
			}
		}
	}
}

// A sign-in run under a placeholder name is saved under the chosen name,
// its fresh folder renamed to match.
func TestSaveAccountRenamesThePlaceholderFolder(t *testing.T) {
	w := newWorld(t)
	tmp := PlaceholderName()
	dir := w.s.Deps.Paths.AccountDir(accounts.ToolClaude, tmp)
	must(t, os.MkdirAll(dir, 0o700))
	got, _, err := w.s.SaveAccount(accounts.Account{Tool: accounts.ToolClaude, Name: tmp, Email: "z@work.com", Dir: dir}, "work")
	must(t, err)
	if got.Dir != w.s.Deps.Paths.AccountDir(accounts.ToolClaude, "work") {
		t.Fatalf("dir = %s", got.Dir)
	}
	if _, err := os.Stat(got.Dir); err != nil {
		t.Fatal(err)
	}
	if w.s.SuggestName(accounts.ToolClaude, "zubair@work.com") == "work" {
		t.Fatal("a taken name was suggested")
	}
}
