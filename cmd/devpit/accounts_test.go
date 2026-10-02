package devpit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/accounts/shims"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// accWorld is the accounts command line on temporary folders: a fake runner
// plays Claude Code and Vercel, the environment is a map, the shim folder is
// scratch and the PATH is never touched.
type accWorld struct {
	t    *testing.T
	root string
	env  map[string]string
	fake *adapters.FakeRunner
	opts service.Options
	work string
}

func newAccWorld(t *testing.T) *accWorld {
	t.Helper()
	root, err := os.MkdirTemp("", "dpc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	root = accounts.LongPath(root) // rules are kept under long names (TEMP may be 8.3)
	w := &accWorld{t: t, root: root, env: map[string]string{"FAKE_WORK_EMAIL": "z@work.com"}, work: filepath.Join(root, "Work")}
	home := filepath.Join(root, "home")
	for _, d := range []string{home, filepath.Join(root, "shims"), filepath.Join(w.work, "api"), filepath.Join(root, "cfg")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	src := filepath.Join(root, "devpit-shim.exe")
	if err := os.WriteFile(src, []byte("shim"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.fake = &adapters.FakeRunner{}
	w.fake.Set(adapters.FakeResponse{Stdout: "2.1.287 (Claude Code)\n"}, "claude", "--version")
	w.fake.Set(adapters.FakeResponse{Stdout: "Commands:\n  login\n  logout\n  status\n"}, "claude", "auth", "--help")
	w.fake.Func = func(c adapters.Cmd) (adapters.Result, error) {
		if c.Name == "claude" && strings.Join(c.Args, " ") == "auth status --json" {
			email := "me@home.com"
			if dir, set, _ := adapters.EnvOf(c, "CLAUDE_CONFIG_DIR"); set && strings.HasSuffix(dir, "work") {
				email = w.env["FAKE_WORK_EMAIL"]
			}
			return adapters.Result{Stdout: []byte(`{"loggedIn":true,"email":"` + email + `"}`)}, nil
		}
		return adapters.Result{}, fmt.Errorf("%s: %w", c.Name, accounts.ErrNotFoundTool)
	}
	getenv := func(k string) string { return w.env[k] }
	w.opts = service.Options{
		Paths:  accounts.PathsIn(filepath.Join(root, "cfg"), filepath.Join(home, ".devpit", "accounts")),
		Home:   home,
		Runner: w.fake,
		Getenv: getenv,
		LookPath: func(name string) (string, error) {
			if name == "claude" {
				return `C:\fake\claude.exe`, nil
			}
			return "", fmt.Errorf("%s: %w", name, accounts.ErrNotFoundTool)
		},
		Now: func() time.Time { return time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC) },
	}
	return w
}

func (w *accWorld) env2() accountsEnv {
	return accountsEnv{
		open: func() (*service.Service, error) {
			o := w.opts
			o.Shims = &shims.Manager{
				Dir: filepath.Join(w.root, "shims"), Source: filepath.Join(w.root, "devpit-shim.exe"),
				Record: filepath.Join(w.root, "cfg", "shims.json"), Getenv: func(k string) string { return w.env[k] },
			}
			return service.Open(o)
		},
		getwd: func() (string, error) { return w.work, nil },
	}
}

// run runs the real command tree (fang, the error handler, the exit codes)
// with buffers for streams, so it is never interactive.
func (w *accWorld) run(args ...string) (ExitCode, string, string) {
	w.t.Helper()
	root := newRootCmdWith(w.env2())
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	code := execute(context.Background(), root, args)
	return code, out.String(), errOut.String()
}

func (w *accWorld) addClaude(name, email string) string {
	w.t.Helper()
	dir := filepath.Join(w.root, "home", ".devpit", "accounts", "claude", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		w.t.Fatal(err)
	}
	st, err := accounts.ReadFile(w.opts.Paths.Store)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := st.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: name, Email: email, Dir: dir}); err != nil {
		w.t.Fatal(err)
	}
	if err := accounts.SaveFile(w.opts.Paths.Store, st); err != nil {
		w.t.Fatal(err)
	}
	return dir
}

func (w *accWorld) store() *accounts.Store {
	w.t.Helper()
	st, err := accounts.ReadFile(w.opts.Paths.Store)
	if err != nil {
		w.t.Fatal(err)
	}
	return st
}

// normalize makes output independent of the temporary folder. The goldens
// are Windows output; off Windows, the separators of paths under the
// temporary folder are turned into Windows ones so the same goldens hold.
func (w *accWorld) normalize(s string) string {
	for _, r := range []string{strings.ReplaceAll(w.root, `\`, `\\`), w.root, filepath.ToSlash(w.root)} {
		s = strings.ReplaceAll(s, r, "<root>")
	}
	if runtime.GOOS != "windows" {
		sep := `\`
		if strings.HasPrefix(strings.TrimSpace(s), "{") {
			sep = `\\` // inside JSON strings
		}
		s = rootPath.ReplaceAllStringFunc(s, func(m string) string { return strings.ReplaceAll(m, "/", sep) })
	}
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// rootPath is a path under the normalized temporary folder.
var rootPath = regexp.MustCompile(`<root>(/[^\s"/,:]+)+`)

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "accounts", name+".golden")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./cmd/devpit -run Golden -update)", err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Errorf("%s differs from the golden file:\n%s", name, got)
	}
}

// The read commands, human and --json, against golden files: stable field
// names, no ANSI, no prompt, and no token even with one planted in the
// environment.
func TestAccountsReadCommandsGolden(t *testing.T) {
	w := newAccWorld(t)
	w.addClaude("work", "z@work.com")
	w.addClaude("oss", "z@oss.dev")
	if code, _, errOut := w.run("claude", "use", "work", "--folder", w.work, "--yes"); code != ExitOK {
		t.Fatalf("use: %d %s", code, errOut)
	}
	w.env["PATH"] = filepath.Join(w.root, "shims")
	w.env["GH_TOKEN"] = "ghp_" + strings.Repeat("Ab1", 12)
	api := filepath.Join(w.work, "api")
	for _, c := range []struct {
		name string
		args []string
	}{
		{"claude", []string{"claude", "--folder", api}},
		{"claude.json", []string{"claude", "--json", "--folder", api}},
		{"accounts", []string{"accounts", "--folder", api}},
		{"accounts.json", []string{"accounts", "--json", "--folder", api}},
		{"claude-list.json", []string{"claude", "list", "--json", "--folder", api}},
		{"claude-list", []string{"claude", "list", "--folder", api}},
		{"verify.json", []string{"accounts", "verify", "--json", "--folder", api}},
		{"verify-all", []string{"accounts", "verify", "--all", "--folder", api}},
	} {
		code, out, errOut := w.run(c.args...)
		if code != ExitOK || errOut != "" {
			t.Errorf("%v: exit %d, stderr %q", c.args, code, errOut)
		}
		if strings.Contains(out, "\x1b[") || strings.Contains(out, w.env["GH_TOKEN"]) {
			t.Errorf("%v: ANSI or a token in the output", c.args)
		}
		if strings.HasSuffix(c.name, ".json") && !json.Valid([]byte(out)) {
			t.Errorf("%v: not JSON:\n%s", c.args, out)
		}
		golden(t, c.name, w.normalize(out))
	}
}

// A change with nobody at a terminal and no --yes prints the preview,
// changes nothing and exits 3; with --yes it is made, and undo works the
// same way.
func TestAccountsChangesNeedYesWithoutATerminal(t *testing.T) {
	w := newAccWorld(t)
	w.addClaude("work", "z@work.com")
	code, out, errOut := w.run("claude", "use", "wo", "--folder", w.work)
	if code != ExitNeedsYes || !strings.Contains(out, "Claude Code will use work (z@work.com)") ||
		errOut != "This would change which account Claude Code uses. Re-run with --yes after the user agrees.\n" {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if len(w.store().Rules) != 0 {
		t.Fatal("the rule was saved without --yes")
	}
	code, out, _ = w.run("claude", "use", "wo", "--folder", w.work, "--yes")
	if code != ExitOK || len(w.store().Rules) != 1 || !strings.Contains(out, "Undo it with: devpit undo") || !strings.Contains(out, "Added Devpit's shim for Claude Code") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// The same again changes nothing and says so.
	if code, out, _ = w.run("claude", "use", "work", "--folder", w.work); code != ExitOK || !strings.Contains(out, "Nothing will change") {
		t.Fatalf("no-op: %d %s", code, out)
	}
	if code, _, _ = w.run("undo"); code != ExitNeedsYes || len(w.store().Rules) != 1 {
		t.Fatalf("undo without --yes: %d", code)
	}
	if code, out, _ = w.run("undo", "--yes"); code != ExitOK || len(w.store().Rules) != 0 || !strings.Contains(out, "Undone") {
		t.Fatalf("undo: %d %s", code, out)
	}
	if code, _, errOut = w.run("undo", "--yes"); code != ExitFailed || !strings.Contains(errOut, "no account change to undo") {
		t.Fatalf("nothing to undo: %d %s", code, errOut)
	}
	for _, args := range [][]string{
		{"claude", "add", "--name", "x"},
		{"agent", "install"},
		{"accounts", "cleanup"},
		{"use", "work", "--folder", w.work},
	} {
		if code, _, _ := w.run(args...); code != ExitNeedsYes && code != ExitOK {
			t.Errorf("%v: exit %d, want 3 (or 0 when there is nothing to change)", args, code)
		}
	}
}

// Every outcome maps to its documented exit code through the real root
// command.
func TestAccountsExitCodes(t *testing.T) {
	w := newAccWorld(t)
	w.addClaude("work", "z@work.com")
	w.addClaude("wonder", "z@wonder.com")
	cases := []struct {
		args   []string
		want   ExitCode
		stderr string
	}{
		{[]string{"claude", "use", "nobody", "--folder", `C:\x`, "--yes"}, ExitUsage, "no account called"},
		{[]string{"claude", "use", "wo", "--folder", `C:\x`, "--yes"}, ExitUsage, "matches more than one account: wonder, work"},
		{[]string{"convex", "use", "default", "--everywhere", "--yes"}, ExitFailed, "per project"},
		{[]string{"git", "run", "default", "--", "git", "commit"}, ExitFailed, ""},
		{[]string{"claude", "run", "work"}, ExitUsage, "then -- and the command"},
		{[]string{"claude", "use", "work", "--everywhere", "--folder", `C:\x`}, ExitUsage, "none of the others"},
		{[]string{"nosuchtool"}, ExitUsage, "unknown command"},
		{[]string{"use", "nobody"}, ExitUsage, "no tool has an account"},
		{[]string{"claude", "import", "--from", "other"}, ExitUsage, "claude-acc only"},
		{[]string{"claude", "import", "--from", "claude-acc"}, ExitFailed, "claude-acc was not found"},
		{[]string{"claude", "add"}, ExitUsage, "--name is needed"},
	}
	for _, c := range cases {
		code, _, errOut := w.run(c.args...)
		if code != c.want || !strings.Contains(errOut, c.stderr) {
			t.Errorf("%v: exit %d, stderr %q; want %d and %q", c.args, code, errOut, c.want, c.stderr)
		}
	}
	// gh and wrangler are aliases.
	if code, out, _ := w.run("gh", "--folder", w.work); code != ExitOK || !strings.HasPrefix(out, "GitHub") {
		t.Fatalf("gh alias: %d %s", code, out)
	}
	if code, out, _ := w.run("wrangler", "--folder", w.work); code != ExitOK || !strings.HasPrefix(out, "Cloudflare") {
		t.Fatalf("wrangler alias: %d %s", code, out)
	}
	// verify exits 4 on a mismatch, and says so in the JSON too.
	if code, _, _ := w.run("claude", "use", "work", "--folder", w.work, "--yes"); code != ExitOK {
		t.Fatal("use")
	}
	w.env["FAKE_WORK_EMAIL"] = "someone@else.com"
	code, out, _ := w.run("accounts", "verify", "--json", "--folder", w.work)
	if code != ExitMismatch || !strings.Contains(out, `"mismatch": true`) {
		t.Fatalf("verify: %d\n%s", code, out)
	}
	// A --json command's error is JSON on stderr.
	if err := os.WriteFile(w.opts.Paths.Store, []byte("version = 99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := w.run("accounts", "--json")
	var je struct {
		Error string `json:"error"`
		Code  int    `json:"code"`
	}
	if code != ExitFailed || json.Unmarshal([]byte(errOut), &je) != nil || je.Code != 1 || !strings.Contains(je.Error, "newer Devpit") {
		t.Fatalf("json error: %d %q", code, errOut)
	}
}

// The agent skill and cleanup, end to end through the commands.
func TestAgentAndCleanupCommands(t *testing.T) {
	w := newAccWorld(t)
	dir := w.addClaude("work", "z@work.com")
	code, out, _ := w.run("agent", "install", "--yes")
	if code != ExitOK || !strings.Contains(out, "## Accounts (Devpit)") {
		t.Fatalf("install: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "devpit", "SKILL.md")); err != nil {
		t.Fatal("the skill is not in the work account")
	}
	if code, _, _ = w.run("claude", "use", "work", "--folder", w.work, "--yes"); code != ExitOK {
		t.Fatal("use")
	}
	code, out, _ = w.run("accounts", "cleanup", "--yes")
	if code != ExitOK || len(w.store().Rules) != 0 || !strings.Contains(out, "Removed the shim") || !strings.Contains(out, "Removed the agent skill") {
		t.Fatalf("cleanup: %d\n%s", code, out)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("cleanup removed an account folder")
	}
	if code, out, _ = w.run("agent", "remove", "--yes"); code != ExitOK || !strings.Contains(out, "not installed anywhere") {
		t.Fatalf("remove: %d %s", code, out)
	}
}

// Every accounts question defaults to No: Enter, end of input or anything
// but y/yes is No.
func TestConfirmDefaultsToNo(t *testing.T) {
	for in, want := range map[string]bool{"\n": false, "": false, "n\n": false, "yes please\n": false, "y\n": true, "YES\n": true} {
		var out bytes.Buffer
		p := &prompter{in: bufio.NewReader(strings.NewReader(in)), out: &out}
		if got := p.confirm("Make this change?"); got != want || !strings.Contains(out.String(), "[y/N]") {
			t.Errorf("answer %q: %v, want %v (%q)", in, got, want, out.String())
		}
	}
}

// git-credential is there for Git and hidden from people.
func TestGitCredentialIsHidden(t *testing.T) {
	c, _, err := NewRootCmd().Find([]string{"git-credential"})
	if err != nil || !c.Hidden {
		t.Fatalf("%v %v", c, err)
	}
}
