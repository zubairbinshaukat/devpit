//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// fakeGhToken is what the fake gh answers to `gh auth token`. It is never
// printed by the fake tool; the tool only says whether GH_TOKEN holds it.
const fakeGhToken = "fake-gh-token-value-0001"

// A copy of the test binary named claude.exe plays the real Claude Code: it
// prints what it was given as JSON and exits 7. A copy named gh.exe plays
// gh: `gh auth token` prints a token (or fails when FAKE_GH_FAIL is set),
// anything else prints whether GH_TOKEN holds that token, never the token.
func TestMain(m *testing.M) {
	base := strings.ToLower(filepath.Base(os.Args[0]))
	if base == "claude.exe" || base == "gh.exe" {
		args := os.Args[1:]
		if args == nil {
			args = []string{}
		}
		if base == "gh.exe" && len(args) >= 2 && args[0] == "auth" && args[1] == "token" {
			if os.Getenv("FAKE_GH_FAIL") != "" || os.Getenv("GH_TOKEN") != "" {
				fmt.Fprintln(os.Stderr, "no oauth token found for github.com")
				os.Exit(1)
			}
			fmt.Println(fakeGhToken)
			os.Exit(0)
		}
		cwd, _ := os.Getwd()
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"args": args, "dir": os.Getenv("CLAUDE_CONFIG_DIR"), "cwd": cwd,
			"guard": os.Getenv("DEVPIT_SHIM_GUARD"), "has_token": os.Getenv("GH_TOKEN") == fakeGhToken,
			"any_token": os.Getenv("GH_TOKEN") != "", "account": os.Getenv("DEVPIT_GITHUB_ACCOUNT"),
		})
		os.Exit(7)
	}
	os.Exit(m.Run())
}

type world struct {
	shim, ghShim, realTool, realGh, cfg, project, other, acctDir, home string
	env                                                                []string
}

// setup builds the real shim as shims\claude.exe, puts a fake claude.exe in
// another folder after it on PATH, and writes an accounts.toml with a rule
// for the project folder.
func setup(t *testing.T) world {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	root := t.TempDir()
	w := world{
		cfg:     filepath.Join(root, "config"),
		project: filepath.Join(root, "Work"),
		other:   filepath.Join(root, "Workshop"),
		acctDir: filepath.Join(root, "accounts", "claude", "work"),
		home:    filepath.Join(root, "home"),
	}
	shimDir, realDir := filepath.Join(root, "shims"), filepath.Join(root, "bin")
	for _, d := range []string{shimDir, realDir, w.cfg, filepath.Join(w.project, "api"), w.other, w.acctDir, w.home} {
		if err = os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	w.shim = filepath.Join(shimDir, "claude.exe")
	out, err := exec.Command(goBin, "build", "-trimpath", "-ldflags=-s -w", "-o", w.shim, ".").CombinedOutput() //nolint:gosec // test
	if err != nil {
		t.Fatalf("building the shim: %v\n%s", err, out)
	}
	w.ghShim = filepath.Join(shimDir, "gh.exe")
	copyFile(t, w.shim, w.ghShim)
	exe, _ := os.Executable()
	w.realTool = filepath.Join(realDir, "claude.exe")
	w.realGh = filepath.Join(realDir, "gh.exe")
	copyFile(t, exe, w.realTool)
	copyFile(t, exe, w.realGh)

	s := accounts.NewStore()
	if err := s.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "work", Email: "z@work.com", Dir: w.acctDir}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAccount(accounts.Account{Tool: accounts.ToolGitHub, Name: "work", Label: "zwork"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRule(w.project, accounts.ToolClaude, "work"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRule(w.project, accounts.ToolGitHub, "work"); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveFile(filepath.Join(w.cfg, accounts.StoreFileName), s); err != nil {
		t.Fatal(err)
	}

	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "PATH", "DEVPIT_CONFIG_DIR", "CLAUDE_CONFIG_DIR", "DEVPIT_SHIM_GUARD", "GH_TOKEN", "GITHUB_TOKEN",
			"DEVPIT_ONCE", "DEVPIT_GITHUB_ACCOUNT", "USERPROFILE", "DEVPIT_ACCOUNTS_DIR":
			continue
		}
		w.env = append(w.env, kv)
	}
	w.env = append(w.env, "PATH="+shimDir+";"+realDir+";"+os.Getenv("SystemRoot")+`\System32`, "DEVPIT_CONFIG_DIR="+w.cfg,
		"USERPROFILE="+w.home, "DEVPIT_ACCOUNTS_DIR="+filepath.Join(root, "accounts"))
	return w
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from) //nolint:gosec // test
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o700); err != nil { //nolint:gosec // an executable
		t.Fatal(err)
	}
}

type answer struct {
	Args     []string `json:"args"`
	Dir      string   `json:"dir"`
	Cwd      string   `json:"cwd"`
	Guard    string   `json:"guard"`
	HasToken bool     `json:"has_token"`
	AnyToken bool     `json:"any_token"`
	Account  string   `json:"account"`
}

func (w world) run(t *testing.T, dir string, extraEnv []string, args ...string) (int, answer, string) {
	t.Helper()
	return w.runProg(t, w.shim, dir, extraEnv, args...)
}

func (w world) runProg(t *testing.T, prog, dir string, extraEnv []string, args ...string) (int, answer, string) {
	t.Helper()
	c := exec.Command(prog, args...)
	c.Dir = dir
	c.Env = append(slices.Clone(w.env), extraEnv...)
	var out, errb strings.Builder
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		t.Fatal(err)
	}
	var a answer
	if out.Len() > 0 {
		if err := json.Unmarshal([]byte(out.String()), &a); err != nil {
			t.Fatalf("tool output %q: %v", out.String(), err)
		}
	}
	return c.ProcessState.ExitCode(), a, errb.String()
}

func TestShimAppliesTheFolderRule(t *testing.T) {
	w := setup(t)
	args := []string{"-p", "fix the bug & test it", "100%", `C:\dir\`}
	code, a, stderr := w.run(t, filepath.Join(w.project, "api"), nil, args...)
	if code != 7 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if a.Dir != w.acctDir || strings.Join(a.Args, "\x00") != strings.Join(args, "\x00") {
		t.Fatalf("tool got %+v", a)
	}
	if !strings.HasPrefix(a.Guard, "claude:") {
		t.Fatalf("guard = %q", a.Guard)
	}

	// C:\...\Workshop is not inside C:\...\Work: default, untouched.
	code, a, stderr = w.run(t, w.other, nil, "x")
	if code != 7 || stderr != "" || a.Dir != "" {
		t.Fatalf("outside the rule: exit %d, %+v, %q", code, a, stderr)
	}
	// Devpit manages Claude Code here, so its answer wins: a
	// CLAUDE_CONFIG_DIR left in the terminal by something else is replaced
	// by the rule's folder, and cleared where the answer is default.
	_, a, _ = w.run(t, w.project, []string{`CLAUDE_CONFIG_DIR=C:\stray`})
	if a.Dir != w.acctDir {
		t.Fatalf("rule folder: %+v", a)
	}
	_, a, _ = w.run(t, w.other, []string{`CLAUDE_CONFIG_DIR=C:\stray`})
	if a.Dir != "" {
		t.Fatalf("default must clear a stray CLAUDE_CONFIG_DIR: %+v", a)
	}

	// When Devpit has nothing for Claude Code at all, nothing is touched.
	s := accounts.NewStore()
	if err := s.SetRule(w.project, accounts.ToolVercel, "default"); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SaveFile(filepath.Join(w.cfg, accounts.StoreFileName), s); err != nil {
		t.Fatal(err)
	}
	_, a, stderr = w.run(t, w.project, []string{`CLAUDE_CONFIG_DIR=C:\mine`})
	if a.Dir != `C:\mine` || stderr != "" {
		t.Fatalf("unmanaged tool was touched: %+v %q", a, stderr)
	}
}

func TestABrokenAccountsFileNeverBlocksTheTool(t *testing.T) {
	w := setup(t)
	if err := os.WriteFile(filepath.Join(w.cfg, accounts.StoreFileName), []byte("this is [ not toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, a, stderr := w.run(t, w.project, nil, "hello")
	if code != 7 || a.Dir != "" || len(a.Args) != 1 {
		t.Fatalf("exit %d, %+v", code, a)
	}
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "devpit: cannot read ") || !strings.Contains(lines[0], "its own sign-in") {
		t.Fatalf("stderr = %q", stderr)
	}
	// The shim never moves the file: that is the app's job, once.
	if _, err := os.Stat(filepath.Join(w.cfg, accounts.StoreFileName)); err != nil {
		t.Fatal("the shim moved the broken file")
	}

	// A rule for an account that has no folder: one warning, untouched.
	s := accounts.NewStore()
	_ = s.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "nodir", Email: "x@y.com"})
	_ = s.SetRule(w.project, accounts.ToolClaude, "nodir")
	if err := accounts.SaveFile(filepath.Join(w.cfg, accounts.StoreFileName), s); err != nil {
		t.Fatal(err)
	}
	code, a, stderr = w.run(t, w.project, nil)
	if code != 7 || a.Dir != "" || !strings.Contains(stderr, "cannot use the account nodir") {
		t.Fatalf("exit %d, %+v, %q", code, a, stderr)
	}
}

func TestShimRefusesToLoopAndExplainsAMissingTool(t *testing.T) {
	w := setup(t)
	code, a, stderr := w.run(t, w.project, []string{"DEVPIT_SHIM_GUARD=claude:" + strconv.Itoa(os.Getpid())})
	if code != 1 || a.Args != nil || !strings.Contains(stderr, "keeps leading back") {
		t.Fatalf("loop: exit %d, %+v, %q", code, a, stderr)
	}
	// A guard from a shim further up (not the direct parent) is a normal
	// nested call: claude started by a shim running claude again.
	if nested, _, _ := w.run(t, w.project, []string{"DEVPIT_SHIM_GUARD=claude:1"}); nested != 7 {
		t.Fatalf("nested call: exit %d", nested)
	}
	if err := os.Remove(w.realTool); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = w.run(t, w.project, nil)
	if code != 1 || !strings.Contains(stderr, "claude is not installed") {
		t.Fatalf("missing: exit %d, %q", code, stderr)
	}
}

// The gh shim gets the named account's token from the real gh at launch
// and hands it to gh in GH_TOKEN, with the Devpit account name for the push
// helper. Outside the rule (default) no token is added. If gh cannot give
// the token, gh starts untouched with one warning line.
func TestGhShimGetsItsTokenAtLaunch(t *testing.T) {
	w := setup(t)
	code, a, stderr := w.runProg(t, w.ghShim, filepath.Join(w.project, "api"), nil, "pr", "list")
	if code != 7 || stderr != "" || !a.HasToken || a.Account != "work" || strings.Join(a.Args, " ") != "pr list" {
		t.Fatalf("in the rule: exit %d, %+v, %q", code, a, stderr)
	}
	code, a, stderr = w.runProg(t, w.ghShim, w.other, nil, "pr", "list")
	if code != 7 || stderr != "" || a.AnyToken || a.Account != "" {
		t.Fatalf("outside the rule: exit %d, %+v, %q", code, a, stderr)
	}
	code, a, stderr = w.runProg(t, w.ghShim, w.project, []string{"FAKE_GH_FAIL=1"}, "pr", "list")
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if code != 7 || a.AnyToken || len(lines) != 1 || !strings.Contains(lines[0], "cannot use the account work") || strings.Contains(stderr, fakeGhToken) {
		t.Fatalf("token refused: exit %d, %+v, %q", code, a, stderr)
	}
}

// `devpit <tool> run <name>` forces an account for the whole command tree:
// a shim below it uses that account, not the folder's rule.
func TestOnceOverridesTheFolderRule(t *testing.T) {
	w := setup(t)
	_, a, stderr := w.run(t, w.other, []string{"DEVPIT_ONCE=claude:work"})
	if a.Dir != w.acctDir || stderr != "" {
		t.Fatalf("forced account: %+v %q", a, stderr)
	}
	_, a, stderr = w.run(t, w.project, []string{"DEVPIT_ONCE=claude:gone"})
	if a.Dir != "" || !strings.Contains(stderr, "no longer exists") {
		t.Fatalf("forced missing account: %+v %q", a, stderr)
	}
	// Another tool's forced account changes nothing here.
	if _, a, _ = w.run(t, w.project, []string{"DEVPIT_ONCE=github:work"}); a.Dir != w.acctDir {
		t.Fatalf("other tool: %+v", a)
	}
}

// An account that shares its skills gets the links of skills added to the
// default account since, at launch, beside the tool.
func TestClaudeShimAddsMissingSkillLinks(t *testing.T) {
	w := setup(t)
	shareSkills(t, w)
	if code, _, stderr := w.run(t, w.project, nil); code != 7 || stderr != "" {
		t.Fatalf("exit %d, %q", code, stderr)
	}
	fi, err := os.Lstat(filepath.Join(w.acctDir, "skills", "demo"))
	if err != nil || fi.Mode()&os.ModeIrregular == 0 && fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the demo skill was not linked: %v %v", fi, err)
	}
}

// shareSkills makes the work account share skills with a default account
// that has one skill.
func shareSkills(t *testing.T, w world) {
	t.Helper()
	skill := filepath.Join(w.home, ".claude", "skills", "demo")
	if err := os.MkdirAll(skill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: demo\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.acctDir, ".devpit-share.json"), []byte(`{"version":1,"shared":["skills"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestShimOverhead measures what the shim adds to a launch, median of 15
// runs after 3 warm-up runs: Claude Code with a folder rule, Claude Code
// with skill links to check, and gh with a named account (one extra `gh
// auth token` run, paid only for a named GitHub account).
func TestShimOverhead(t *testing.T) {
	if testing.Short() {
		t.Skip("timing")
	}
	w := setup(t)
	const n = 15
	measure := func(prog string, args ...string) time.Duration {
		var ds []time.Duration
		for i := 0; i < n+3; i++ {
			c := exec.Command(prog, args...)
			c.Dir = filepath.Join(w.project, "api")
			c.Env = w.env
			start := time.Now()
			_ = c.Run()
			if i >= 3 {
				ds = append(ds, time.Since(start))
			}
		}
		slices.Sort(ds)
		return ds[len(ds)/2]
	}
	direct := measure(w.realTool)
	shimmed := measure(w.shim)
	shareSkills(t, w)
	_ = measure(w.shim) // the first run makes the link
	linked := measure(w.shim)
	ghDirect := measure(w.realGh, "pr", "list")
	ghShimmed := measure(w.ghShim, "pr", "list")
	t.Logf("claude: tool alone %v, through the shim %v, overhead %v (target about 30 ms)", direct, shimmed, shimmed-direct)
	t.Logf("claude sharing skills: through the shim %v, overhead %v", linked, linked-direct)
	t.Logf("gh with a named account: tool alone %v, through the shim %v, overhead %v (includes one `gh auth token` run)", ghDirect, ghShimmed, ghShimmed-ghDirect)
	if shimmed-direct > 150*time.Millisecond || linked-direct > 150*time.Millisecond {
		t.Fatalf("the shim adds %v / %v", shimmed-direct, linked-direct)
	}
}

func TestShimImportsNothingItDoesNotNeed(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	out, err := exec.Command(goBin, "list", "-deps", ".").Output() //nolint:gosec // test
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, banned := range []string{"bubbletea", "lipgloss", "bubbles", "cobra", "pflag", "fang", "/internal/ui", "/internal/app", "net/http", "/internal/scan", "/internal/clean", "/accounts/service", "/accounts/importer"} {
			if strings.Contains(dep, banned) {
				t.Errorf("devpit-shim imports %s (through %s)", banned, dep)
			}
		}
	}
}
