//go:build windows

package gitcred

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
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
)

// TestGitCredentialFillReachesDevpit is the whole push path with the real
// Git: the GitHub adapter applies a folder rule (writing the github.com
// helper with this test binary as devpit.exe), then `git credential`
// commands in different folders reach the helper, which answers for the
// ruled folder from a fake gh and hands everything else to the previous
// helper. Undo then restores the global config's exact bytes.
func TestGitCredentialFillReachesDevpit(t *testing.T) {
	needGit(t)
	tmp := shortTemp(t)
	log := filepath.Join(tmp, "prev.log")
	q := `"` + strings.ReplaceAll(strings.ReplaceAll(prevHelper(log), `\`, `\\`), `"`, `\"`) + `"`
	global := filepath.Join(tmp, "global.gitconfig")
	orig := []byte("[user]\n\temail = base@example.invalid\n[credential]\n\thelper = " + q + "\n")
	_ = os.WriteFile(global, orig, 0o600)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GCM_INTERACTIVE", "never")
	for _, k := range []string{"GIT_DIR", adapters.EnvGitHubAccount, EnvActive} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}

	home := filepath.Join(tmp, "home")
	paths := accounts.PathsIn(filepath.Join(tmp, "cfg"), filepath.Join(home, ".devpit", "accounts"))
	gh := &adapters.FakeRunner{}
	gh.Set(adapters.FakeResponse{Stdout: "gh version 2.102.0\n"}, "gh", "--version")
	gh.Set(adapters.FakeResponse{Stdout: "  -u, --user string\n"}, "gh", "auth", "token", "--help")
	d := adapters.Deps{Runner: &route{git: adapters.ExecRunner{}, gh: gh}, Paths: paths, Home: home, Now: time.Now}
	eng := accounts.NewEngine(paths)
	if _, err := eng.AddAccount(accounts.Account{Tool: accounts.ToolGitHub, Name: "work", Label: "zubair-work"}); err != nil {
		t.Fatal(err)
	}
	hub := adapters.NewGitHubAdapter(d)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hub.HelperExe = self
	work := filepath.Join(tmp, "Work")
	res, _ := eng.Load()
	p, err := hub.Plan(accounts.PreviewInput{Store: res.Store, Change: accounts.Change{Tool: accounts.ToolGitHub, Account: "work", Scope: accounts.FolderScope(work)}})
	if err != nil {
		t.Fatal(err)
	}
	var fin accounts.Event
	for ev := range adapters.Apply(context.Background(), eng, hub, p) {
		fin = ev
	}
	if fin.State != accounts.StepDone {
		t.Fatalf("apply: %+v", fin)
	}

	t.Setenv("GITCRED_TEST_HELPER", "1")
	t.Setenv("GITCRED_TEST_STORE", paths.Store)
	t.Setenv("GITCRED_TEST_GITDIR", adapters.GitDir(d))
	t.Setenv("GITCRED_TEST_TOKEN", planted)

	git := func(dir string, env []string, stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdin = strings.NewReader(stdin)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, errb.String())
		}
		if strings.Contains(errb.String(), "PLANTED") {
			t.Fatalf("the token reached stderr: %q", errb.String())
		}
		return strings.ReplaceAll(out.String(), "\r", "")
	}
	initRepo := func(dir string) {
		_ = os.MkdirAll(dir, 0o700)
		git(dir, nil, "", "init", "-q")
	}
	fill := "protocol=https\nhost=github.com\n\n"

	repo := filepath.Join(work, "repo")
	initRepo(repo)
	if out := git(filepath.Join(repo), nil, fill, "credential", "fill"); !strings.Contains(out, "username=zubair-work\npassword="+planted+"\n") {
		t.Fatalf("in the ruled repo: %q", out)
	}
	plain := filepath.Join(tmp, "plain")
	initRepo(plain)
	if out := git(plain, nil, fill, "credential", "fill"); !strings.Contains(out, "username=prev\npassword=prevpw\n") {
		t.Fatalf("outside the rule: %q", out)
	}
	// git clone runs the helper from the caller's folder with GIT_DIR
	// naming the new repo: the target's rule must win.
	target := filepath.Join(work, "target")
	initRepo(target)
	if out := git(plain, []string{"GIT_DIR=" + filepath.Join(target, ".git")}, fill, "credential", "fill"); !strings.Contains(out, "username=zubair-work") {
		t.Fatalf("clone-like call: %q", out)
	}
	// Approve and reject of the work credential never reach the previous
	// helper, so it cannot save or wipe anything.
	cred := "protocol=https\nhost=github.com\nusername=zubair-work\npassword=" + planted + "\n\n"
	git(repo, nil, cred, "credential", "approve")
	git(repo, nil, cred, "credential", "reject")
	b, _ := os.ReadFile(log)
	if got := strings.ReplaceAll(string(b), "\r", ""); got != "get\n" {
		t.Fatalf("the previous helper saw %q; want only the one get from the plain folder", got)
	}
	// The default account's approve does reach it.
	git(plain, nil, "protocol=https\nhost=github.com\nusername=prev\npassword=prevpw\n\n", "credential", "approve")
	b, _ = os.ReadFile(log)
	if got := strings.ReplaceAll(string(b), "\r", ""); got != "get\nstore\n" {
		t.Fatalf("after a default approve: %q", got)
	}

	if _, err := eng.Undo(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(global); !bytes.Equal(got, orig) {
		t.Fatalf("undo: global = %q", got)
	}
	if out := git(repo, nil, fill, "credential", "fill"); !strings.Contains(out, "username=prev") {
		t.Fatalf("after undo the previous helper answers again: %q", out)
	}
}
