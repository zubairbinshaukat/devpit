package adapters

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// routeRunner sends git to the real program and everything else (gh) to a
// fake, so tests read real Git config while gh never runs.
type routeRunner struct {
	git   Runner
	other *FakeRunner
}

func (r routeRunner) Run(ctx context.Context, c Cmd) (Result, error) {
	if c.Name == "git" {
		return r.git.Run(ctx, c)
	}
	return r.other.Run(ctx, c)
}

// gitWorld is one isolated test world: a temp global config through
// GIT_CONFIG_GLOBAL, no system config, a temp home, accounts store and
// journal. The person's real ~/.gitconfig is never read or written.
type gitWorld struct {
	t      *testing.T
	tmp    string
	global string
	orig   []byte
	deps   Deps
	gh     *FakeRunner
	eng    *accounts.Engine
	git    *GitAdapter
	hub    *GitHubAdapter
}

const baseGlobal = "[user]\n\tname = Base Person\n\temail = base@example.invalid\n[core]\n\tautocrlf = false\n"

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// gitShortTemp is a temp folder with a short name. t.TempDir's names are long
// runs of letters and digits, which Devpit's secret guard (rightly) takes
// for tokens when they appear inside a file it writes.
func gitShortTemp(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "dpg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func newGitWorld(t *testing.T) *gitWorld {
	t.Helper()
	needGit(t)
	return newGitWorldIn(t, gitShortTemp(t))
}

// newGitWorldIn is newGitWorld in a folder of the caller's choosing (a long,
// random-looking one, say).
func newGitWorldIn(t *testing.T, tmp string) *gitWorld {
	t.Helper()
	needGit(t)
	global := filepath.Join(tmp, "global.gitconfig")
	if err := os.WriteFile(global, []byte(baseGlobal), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GCM_INTERACTIVE", "never")
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL", "GIT_CONFIG_COUNT", "GIT_SSH_COMMAND", EnvGitHubAccount} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	home := filepath.Join(tmp, "home")
	_ = os.MkdirAll(home, 0o700)
	paths := accounts.PathsIn(filepath.Join(tmp, "cfg"), filepath.Join(home, ".devpit", "accounts"))
	gh := &FakeRunner{}
	gh.Set(FakeResponse{Stdout: "gh version 2.102.0 (2026-09-30)\n"}, "gh", "--version")
	gh.Set(FakeResponse{Stdout: "Flags:\n  -h, --hostname string\n  -u, --user string\n"}, "gh", "auth", "token", "--help")
	d := Deps{
		Runner: routeRunner{git: ExecRunner{}, other: gh},
		Paths:  paths, Home: home,
		Now: func() time.Time { return time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC) },
	}
	w := &gitWorld{t: t, tmp: tmp, global: global, orig: []byte(baseGlobal), deps: d, gh: gh, eng: accounts.NewEngine(paths)}
	w.git = newGit(d)
	w.hub = newGitHub(d)
	w.hub.HelperExe = filepath.Join(tmp, "bin", "devpit.exe")
	return w
}

func (w *gitWorld) store() *accounts.Store {
	w.t.Helper()
	res, err := w.eng.Load()
	if err != nil {
		w.t.Fatal(err)
	}
	return res.Store
}

func (w *gitWorld) addIdentity(name, userName, email string) {
	w.t.Helper()
	a, err := NewGitIdentity(w.store(), name, userName, email, time.Now())
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.eng.AddAccount(a); err != nil {
		w.t.Fatal(err)
	}
}

// change plans and applies one change and returns the preview.
func (w *gitWorld) change(a Adapter, c accounts.Change) accounts.Preview {
	w.t.Helper()
	p, err := a.Plan(accounts.PreviewInput{Store: w.store(), Change: c, Home: w.deps.Home})
	if err != nil {
		w.t.Fatalf("plan %+v: %v", c, err)
	}
	evs := collect(w.t, Apply(context.Background(), w.eng, a, p))
	if fin := last(evs); fin.State != accounts.StepDone {
		w.t.Fatalf("apply %+v: %+v (%v)", c, fin, fin.Err)
	}
	return p
}

func (w *gitWorld) readGlobal() []byte {
	w.t.Helper()
	b, err := os.ReadFile(w.global)
	if err != nil {
		w.t.Fatal(err)
	}
	return b
}

// git runs the real git in dir and returns its trimmed stdout.
func (w *gitWorld) gitOut(dir string, args ...string) string {
	w.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		unset := len(args) > 0 && args[0] == "config" && asExit(err, &ee) && ee.ExitCode() == 1
		if !unset {
			w.t.Fatalf("git %v in %s: %v\n%s", args, dir, err, errb.String())
		}
	}
	return strings.TrimSpace(out.String())
}

// gitOutRaw is gitOut without trimming spaces, for values that have them.
func (w *gitWorld) gitOutRaw(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return strings.TrimSuffix(strings.TrimSuffix(string(out), "\n"), "\r")
}

func asExit(err error, ee **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError) //nolint:errorlint // exec returns it unwrapped
	if ok {
		*ee = e
	}
	return ok
}

// initRepo makes a repo at dir (and its parents).
func (w *gitWorld) initRepo(dir string) {
	w.t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		w.t.Fatal(err)
	}
	w.gitOut(dir, "init", "-q")
}

func (w *gitWorld) email(dir string) string { return w.gitOut(dir, "config", "user.email") }

func (w *gitWorld) devpitFiles() []string {
	w.t.Helper()
	entries, _ := os.ReadDir(GitDir(w.deps))
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
