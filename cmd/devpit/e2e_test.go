package devpit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// fakeClaude makes a copy of this test binary named claude.exe play Claude
// Code for the end-to-end test: it answers --version and `auth --help`, and
// otherwise prints the CLAUDE_CONFIG_DIR it was started with. It returns
// false (and does nothing) for any other name.
func fakeClaude() bool {
	if !strings.EqualFold(filepath.Base(os.Args[0]), "claude.exe") {
		return false
	}
	args := strings.Join(os.Args[1:], " ")
	switch args {
	case "--version":
		fmt.Println("2.1.287 (Claude Code)")
	case "auth --help":
		fmt.Println("Commands:\n  login\n  logout\n  status")
	default:
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"dir": os.Getenv("CLAUDE_CONFIG_DIR")})
	}
	os.Exit(0)
	return true
}

// The whole path, with real binaries on temporary folders: devpit.exe sets a
// folder rule, the shim it installs gives Claude Code the account's folder
// inside that folder and nothing outside it, and devpit undo takes the rule
// away. Every folder Devpit uses is overridden; the real PATH, registry and
// home folder are never touched (DEVPIT_SHIM_DIR keeps Devpit off the PATH).
func TestEndToEndUseShimUndo(t *testing.T) {
	if testing.Short() || runtime.GOOS != "windows" {
		t.Skip("builds two binaries; Windows only")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	tmp, err := os.MkdirTemp("", "dpe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	bin, tools, shimDir := filepath.Join(tmp, "bin"), filepath.Join(tmp, "tools"), filepath.Join(tmp, "shims")
	cfg, acctRoot, home := filepath.Join(tmp, "cfg"), filepath.Join(tmp, "accounts"), filepath.Join(tmp, "home")
	work, other := filepath.Join(tmp, "Work"), filepath.Join(tmp, "Other")
	acctDir := filepath.Join(acctRoot, "claude", "work")
	for _, d := range []string{bin, tools, cfg, home, filepath.Join(work, "repo"), other, acctDir} {
		if err = os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for pkg, out := range map[string]string{
		"github.com/zubairbinshaukat/devpit":                 filepath.Join(bin, "devpit.exe"),
		"github.com/zubairbinshaukat/devpit/cmd/devpit-shim": filepath.Join(bin, "devpit-shim.exe"),
	} {
		if b, berr := exec.Command(goBin, "build", "-o", out, pkg).CombinedOutput(); berr != nil { //nolint:gosec // test
			t.Fatalf("building %s: %v\n%s", pkg, berr, b)
		}
	}
	self, _ := os.Executable()
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(tools, "claude.exe"), data, 0o700); err != nil { //nolint:gosec // an executable
		t.Fatal(err)
	}
	st := accounts.NewStore()
	if err = st.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "work", Email: "z@work.com", Dir: acctDir}); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(cfg, accounts.StoreFileName)
	if err = accounts.SaveFile(store, st); err != nil {
		t.Fatal(err)
	}

	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "PATH", "USERPROFILE", "HOME", "CLAUDE_CONFIG_DIR", "GIT_CONFIG_GLOBAL", "LOCALAPPDATA", "APPDATA":
			continue
		}
		if strings.HasPrefix(strings.ToUpper(k), "DEVPIT_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"PATH="+tools+";"+os.Getenv("SystemRoot")+`\System32`,
		"DEVPIT_CONFIG_DIR="+cfg, "DEVPIT_ACCOUNTS_DIR="+acctRoot, "DEVPIT_SHIM_DIR="+shimDir,
		"USERPROFILE="+home, "LOCALAPPDATA="+filepath.Join(tmp, "local"), "APPDATA="+filepath.Join(tmp, "appdata"),
		"GIT_CONFIG_GLOBAL="+filepath.Join(tmp, "gitconfig"), "GIT_CONFIG_NOSYSTEM=1", "NO_COLOR=1",
	)
	run := func(dir, prog string, args ...string) (int, string) {
		t.Helper()
		c := exec.Command(prog, args...) //nolint:gosec // test
		c.Dir, c.Env = dir, env
		out, rerr := c.CombinedOutput()
		var ee *exec.ExitError
		if rerr != nil && !errors.As(rerr, &ee) {
			t.Fatalf("%s %v: %v", prog, args, rerr)
		}
		return c.ProcessState.ExitCode(), string(out)
	}
	devpit := filepath.Join(bin, "devpit.exe")

	if code, out := run(tmp, devpit, "claude", "use", "work", "--folder", work, "--yes"); code != 0 {
		t.Fatalf("devpit claude use: exit %d\n%s", code, out)
	}
	shim := filepath.Join(shimDir, "claude.exe")
	if _, err = os.Stat(shim); err != nil {
		t.Fatal("devpit did not install the claude shim")
	}
	dirIn := func(dir string) string {
		t.Helper()
		code, out := run(dir, shim, "-p", "hi")
		var got struct{ Dir string }
		if code != 0 || json.Unmarshal([]byte(out), &got) != nil {
			t.Fatalf("shim in %s: exit %d %q", dir, code, out)
		}
		return got.Dir
	}
	if got := dirIn(filepath.Join(work, "repo")); got != acctDir {
		t.Fatalf("inside the rule Claude Code got %q, want %q", got, acctDir)
	}
	if got := dirIn(other); got != "" {
		t.Fatalf("outside the rule Claude Code got %q", got)
	}
	if code, out := run(tmp, devpit, "undo", "--yes"); code != 0 {
		t.Fatalf("devpit undo: exit %d\n%s", code, out)
	}
	after, err := accounts.ReadFile(store)
	if err != nil || len(after.Rules) != 0 {
		t.Fatalf("the rule is still there: %+v %v", after, err)
	}
	if got := dirIn(filepath.Join(work, "repo")); got != "" {
		t.Fatalf("after undo Claude Code still got %q", got)
	}
}
