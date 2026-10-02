package gitcred

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
)

// TestMain doubles as the credential helper in the end-to-end test: Git
// runs this test binary as `<exe> git-credential <op>`, with a fake gh.
func TestMain(m *testing.M) {
	if os.Getenv("GITCRED_TEST_HELPER") == "1" && len(os.Args) >= 3 && os.Args[1] == "git-credential" {
		os.Exit(helperMain(os.Args[2]))
	}
	os.Exit(m.Run())
}

func helperMain(op string) int {
	gh := &adapters.FakeRunner{}
	gh.Set(adapters.FakeResponse{Stdout: os.Getenv("GITCRED_TEST_TOKEN") + "\n"}, "gh", "auth", "token", "--hostname", "github.com", "--user", "zubair-work")
	d := Deps{
		Runner:    &route{git: adapters.ExecRunner{}, gh: gh},
		StorePath: os.Getenv("GITCRED_TEST_STORE"), GitDir: os.Getenv("GITCRED_TEST_GITDIR"),
		Getenv: os.Getenv, Getwd: os.Getwd, RealPath: accounts.RealPath, Stderr: os.Stderr,
	}
	if err := Serve(context.Background(), d, op, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// route runs git for real and gh from a fake, and records git calls.
type route struct {
	git adapters.Runner
	gh  *adapters.FakeRunner
	mu  sync.Mutex
	ran []adapters.Cmd
}

func (r *route) Run(ctx context.Context, c adapters.Cmd) (adapters.Result, error) {
	if c.Name == "git" {
		r.mu.Lock()
		r.ran = append(r.ran, c)
		r.mu.Unlock()
		if r.git == nil {
			return adapters.Result{}, fmt.Errorf("git: %w", accounts.ErrNotFoundTool)
		}
		return r.git.Run(ctx, c)
	}
	return r.gh.Run(ctx, c)
}

func (r *route) gitCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ran)
}

var planted = "gho_PLANTED" + strings.Repeat("k7Z", 10)

func TestFolder(t *testing.T) {
	wd := func(s string) func() (string, error) { return func() (string, error) { return s, nil } }
	env := func(gd string) func(string) string {
		return func(k string) string {
			if k == "GIT_DIR" {
				return gd
			}
			return ""
		}
	}
	sep := string(filepath.Separator)
	base := filepath.Join(t.TempDir(), "caller")
	target := filepath.Join(filepath.Dir(base), "Work", "target")
	cases := []struct{ gd, cwd, want string }{
		{"", base, base},                              // no repo: the working folder
		{".git", target, target},                      // inside a repo: git moved to the top level
		{filepath.Join(target, ".git"), base, target}, // git clone: cwd is the caller's, GIT_DIR the target's
		{filepath.Join(target, ".git") + sep, base, target},
		{filepath.Join(target, ".git", "worktrees", "wt"), base, base}, // a linked worktree: its own folder
		{filepath.Join(target, "bare.git"), base, base},
	}
	for _, c := range cases {
		got, err := Folder(env(c.gd), wd(c.cwd))
		if err != nil || got != c.want {
			t.Errorf("GIT_DIR=%q cwd=%q: %q %v, want %q", c.gd, c.cwd, got, err, c.want)
		}
	}
}

// world is a store with a GitHub rule, Devpit's record of the previous
// helper chain, and a previous helper that logs what it is asked.
type world struct {
	t      *testing.T
	tmp    string
	store  string
	gitDir string
	log    string
	env    map[string]string
	cwd    string
	r      *route
	gh     *adapters.FakeRunner
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func shortTemp(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "dpc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// prevHelper is a shell-snippet helper standing in for Git Credential
// Manager: it logs the operation and answers get with "prev". ("pass""word"
// is password to the shell, and not a password to Devpit's secret guard,
// which refuses to copy a config holding one into its undo history.)
func prevHelper(log string) string {
	return `!f() { echo "$1" >> '` + filepath.ToSlash(log) + `'; if [ "$1" = get ]; then echo username=prev; echo pass""word=prevpw; fi; }; f`
}

func newWorld(t *testing.T, ruleFolder string) *world {
	t.Helper()
	tmp := shortTemp(t)
	w := &world{t: t, tmp: tmp, store: filepath.Join(tmp, "accounts.toml"), gitDir: filepath.Join(tmp, "git"), log: filepath.Join(tmp, "prev.log"), env: map[string]string{}}
	s := accounts.NewStore()
	if err := s.AddAccount(accounts.Account{Tool: accounts.ToolGitHub, Name: "work", Label: "zubair-work"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRule(ruleFolder, accounts.ToolGitHub, "work"); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveFile(w.store, s); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(w.gitDir, 0o700)
	x := map[string]any{"version": 1, "credential_helper": map[string]any{"command": `!"C:/x/devpit.exe" git-credential`, "previous": []string{prevHelper(w.log)}}}
	b, _ := json.Marshal(x)
	_ = os.WriteFile(filepath.Join(w.gitDir, "devpit.json"), b, 0o600)

	global := filepath.Join(tmp, "global.gitconfig")
	_ = os.WriteFile(global, nil, 0o600)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GCM_INTERACTIVE", "never")
	w.gh = &adapters.FakeRunner{}
	w.gh.Set(adapters.FakeResponse{Stdout: planted + "\n"}, "gh", "auth", "token", "--hostname", "github.com", "--user", "zubair-work")
	w.r = &route{git: adapters.ExecRunner{}, gh: w.gh}
	return w
}

func (w *world) deps(stderr *bytes.Buffer) Deps {
	return Deps{
		Runner: w.r, StorePath: w.store, GitDir: w.gitDir,
		Getenv: func(k string) string { return w.env[k] },
		Getwd:  func() (string, error) { return w.cwd, nil },
		Stderr: stderr,
	}
}

func (w *world) serve(op, in string) (string, string) {
	w.t.Helper()
	var out, errb bytes.Buffer
	if err := Serve(context.Background(), w.deps(&errb), op, strings.NewReader(in), &out); err != nil {
		w.t.Fatalf("serve %s: %v", op, err)
	}
	return out.String(), errb.String()
}

func (w *world) prevLog() string {
	b, _ := os.ReadFile(w.log)
	return strings.ReplaceAll(string(b), "\r", "")
}

const getGitHub = "capability[]=authtype\ncapability[]=state\nprotocol=https\nhost=github.com\nwwwauth[]=Basic realm=\"GitHub\"\n\n"

func TestNamedAccountGetsItsTokenAndNothingElse(t *testing.T) {
	w := newWorld(t, `C:\Work`)
	w.cwd = `C:\Work\api`
	out, errs := w.serve("get", getGitHub)
	if out != "username=zubair-work\npassword="+planted+"\n" || errs != "" {
		t.Fatalf("out=%q err=%q", out, errs)
	}
	if w.r.gitCalls() != 0 {
		t.Fatal("a named account must not touch the previous chain")
	}
	// Git approves the credential after a push: nothing is stored anywhere.
	out, errs = w.serve("store", "protocol=https\nhost=github.com\nusername=zubair-work\npassword="+planted+"\n\n")
	if out != "" || errs != "" || w.r.gitCalls() != 0 {
		t.Fatalf("store: out=%q err=%q calls=%d", out, errs, w.r.gitCalls())
	}
	// GitHub rejected it: Git calls erase once. It is not forwarded (that
	// could wipe the default sign-in) and says, without the token, what to do.
	out, errs = w.serve("erase", "protocol=https\nhost=github.com\nusername=zubair-work\npassword="+planted+"\n\n")
	if out != "" || w.r.gitCalls() != 0 || !strings.Contains(errs, "gh auth login") || strings.Contains(errs, "PLANTED") || strings.Count(errs, "\n") != 1 {
		t.Fatalf("erase: out=%q err=%q calls=%d", out, errs, w.r.gitCalls())
	}
	// DEVPIT_GITHUB_ACCOUNT picks the account wherever the folder is.
	w.cwd = `D:\Elsewhere`
	w.env[adapters.EnvGitHubAccount] = "work"
	if out, _ = w.serve("get", getGitHub); !strings.HasPrefix(out, "username=zubair-work\n") {
		t.Fatalf("env account: %q", out)
	}
	// The recursion guard: a helper started by Devpit's own pass-through
	// says nothing at all.
	w.env[EnvActive] = "1"
	if out, errs = w.serve("get", getGitHub); out != "" || errs != "" {
		t.Fatalf("guard: %q %q", out, errs)
	}
}

func TestDefaultAndOtherHostsPassThrough(t *testing.T) {
	needGit(t)
	w := newWorld(t, `C:\Work`)
	w.cwd = shortTemp(t) // a real folder outside the rule
	out, errs := w.serve("get", getGitHub)
	if !strings.Contains(out, "username=prev\n") || !strings.Contains(out, "password=prevpw\n") || strings.Contains(out, "capability") {
		t.Fatalf("default get: out=%q err=%q", out, errs)
	}
	w.serve("store", "protocol=https\nhost=github.com\nusername=prev\npassword=prevpw\n\n")
	w.serve("erase", "protocol=https\nhost=github.com\nusername=prev\npassword=prevpw\n\n")
	if got := w.prevLog(); got != "get\nstore\nerase\n" {
		t.Fatalf("the previous helper saw %q", got)
	}
	// Not github.com: passed through whatever the folder.
	w.cwd = `C:\Work\api`
	if out, _ = w.serve("get", "protocol=https\nhost=gitlab.com\n\n"); !strings.Contains(out, "username=prev") {
		t.Fatalf("gitlab: %q", out)
	}
	// A URL naming another user is left to the previous chain too.
	if out, _ = w.serve("get", "protocol=https\nhost=github.com\nusername=someone-else\n\n"); !strings.Contains(out, "username=prev") {
		t.Fatalf("other user: %q", out)
	}
}

func TestFailuresFallBackToThePreviousChain(t *testing.T) {
	needGit(t)
	w := newWorld(t, `C:\Work`)
	w.cwd = `C:\Work\api`
	// gh has lost the sign-in, and prints the token-looking thing it had.
	w.gh.Set(adapters.FakeResponse{Stderr: "no oauth token found for " + planted + "\n", Exit: 1}, "gh", "auth", "token", "--hostname", "github.com", "--user", "zubair-work")
	out, errs := w.serve("get", getGitHub)
	if !strings.Contains(out, "username=prev") || !strings.Contains(errs, "using your usual GitHub sign-in") || strings.Contains(errs, "PLANTED") {
		t.Fatalf("gh failing: out=%q err=%q", out, errs)
	}
	// An accounts file that cannot be read.
	_ = os.WriteFile(w.store, []byte("this is = = not toml"), 0o600)
	out, errs = w.serve("get", getGitHub)
	if !strings.Contains(out, "username=prev") || !strings.Contains(errs, "accounts file") {
		t.Fatalf("broken store: out=%q err=%q", out, errs)
	}
	// No record of a previous chain: nothing to say, Git goes on as before.
	_ = os.Remove(filepath.Join(w.gitDir, "devpit.json"))
	if out, _ = w.serve("get", "protocol=https\nhost=gitlab.com\n\n"); out != "" {
		t.Fatalf("no chain: %q", out)
	}
	// Unknown operations are ignored.
	if out, errs = w.serve("capabilities", ""); out != "" || errs != "" {
		t.Fatalf("unknown op: %q %q", out, errs)
	}
}
