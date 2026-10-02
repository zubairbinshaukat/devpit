//go:build windows

package adapters

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/gitssh"
)

func TestPushesAsFollowsTheProtocol(t *testing.T) {
	w := newGitWorld(t)
	if _, err := w.eng.AddAccount(accounts.Account{Tool: accounts.ToolGitHub, Name: "work", Label: "zubair-work"}); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(w.tmp, "Work")
	w.change(w.hub, accounts.Change{Tool: accounts.ToolGitHub, Account: "work", Scope: accounts.FolderScope(work)})
	ctx := context.Background()
	repo := func(name, url string) string {
		dir := filepath.Join(work, name)
		w.initRepo(dir)
		if url != "" {
			w.gitOut(dir, "remote", "add", "origin", url)
		}
		return dir
	}

	p, err := w.hub.PushesAs(ctx, w.store(), repo("https", "https://github.com/o/r.git"))
	if err != nil || p.Via != PushViaDevpit || p.Resolution.Account.Name != "work" || !p.HelperInstalled || p.Remote.Protocol != ProtocolHTTPS {
		t.Fatalf("https: %+v %v", p, err)
	}
	p, _ = w.hub.PushesAs(ctx, w.store(), repo("ssh", "git@github.com:o/r.git"))
	if p.Via != PushViaSSHKey || !strings.Contains(strings.Join(p.Notes, " "), "SSH key") {
		t.Fatalf("ssh: %+v", p)
	}
	p, _ = w.hub.PushesAs(ctx, w.store(), repo("gitlab", "https://gitlab.com/o/r.git"))
	if p.Via != PushViaNotGitHub {
		t.Fatalf("gitlab: %+v", p)
	}
	p, _ = w.hub.PushesAs(ctx, w.store(), repo("none", ""))
	if p.Via != PushViaNoRemote {
		t.Fatalf("no remote: %+v", p)
	}
	own := repo("own", "https://github.com/o/r.git")
	w.gitOut(own, "config", "--local", "credential.helper", "")
	w.gitOut(own, "config", "--local", "--add", "credential.helper", "store")
	if p, _ = w.hub.PushesAs(ctx, w.store(), own); p.Via != PushViaRepoHelper {
		t.Fatalf("repo helper: %+v", p)
	}
	_ = os.MkdirAll(filepath.Join(work, "empty"), 0o700)
	if p, _ = w.hub.PushesAs(ctx, w.store(), filepath.Join(work, "empty")); p.Via != PushViaNoRemote || p.Repo.IsRepo {
		t.Fatalf("not a repo: %+v", p)
	}
}

// TestFolderSSHKeyReachesSSHIntact runs a real `git ls-remote` over SSH with
// a fake ssh that records its arguments: the key path, with a space and a
// quote in it, arrives as one argument, with IdentitiesOnly.
func TestFolderSSHKeyReachesSSHIntact(t *testing.T) {
	w := newGitWorld(t)
	keyDir := filepath.Join(w.tmp, "John O'Neil", ".ssh")
	_ = os.MkdirAll(keyDir, 0o700)
	key := filepath.Join(keyDir, "id_work")
	_ = os.WriteFile(key, []byte("not a real key"), 0o600)

	// Generating a key where one exists is refused by internal/gitssh.
	if _, err := GenerateSSHKey(context.Background(), key, "z@work.com"); !errors.As(err, new(*gitssh.ExistsError)) {
		t.Fatalf("keygen over an existing key: %v", err)
	}

	work := filepath.Join(w.tmp, "Work")
	p, err := w.hub.PlanSSHKey(w.store(), work, key)
	if err != nil || p.NoChange {
		t.Fatalf("plan: %+v %v", p, err)
	}
	text := strings.Join(p.Lines(), "\n")
	if !strings.Contains(text, `sshCommand = "ssh -i 'C:/`) || !strings.Contains(text, `O'\\''Neil`) {
		t.Fatalf("preview:\n%s", text)
	}
	if fin := last(collect(t, w.hub.ApplySettings(context.Background(), w.eng, p))); fin.State != accounts.StepDone {
		t.Fatalf("apply: %+v", fin)
	}

	// Run the value Git reads in that repo through the shell Git itself
	// uses for core.sshCommand, with ssh replaced by a function that prints
	// its arguments.
	repo := filepath.Join(work, "r")
	w.initRepo(repo)
	value := w.gitOut(repo, "config", "core.sshCommand")
	execPath := filepath.FromSlash(w.gitOut(w.tmp, "--exec-path"))
	root := filepath.Dir(filepath.Dir(filepath.Dir(execPath)))
	sh := filepath.Join(root, "usr", "bin", "sh.exe")
	if !fileExists(sh) {
		sh = filepath.Join(root, "bin", "sh.exe")
	}
	if !fileExists(sh) {
		t.Skipf("Git's sh not found next to %s", execPath)
	}
	out, err := exec.Command(sh, "-c", `ssh() { for a in "$@"; do printf '%s\n' "$a"; done; }; `+value+` "$@"`,
		"sh", "git@github.com", "git-upload-pack 'o/r.git'").Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	args := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(out), "\r", "")), "\n")
	if !slices.Equal(args, []string{"-i", slashPath(key), "-o", "IdentitiesOnly=yes", "git@github.com", "git-upload-pack 'o/r.git'"}) {
		t.Fatalf("ssh got %q", args)
	}
	w.gitOut(repo, "remote", "add", "origin", "git@github.com:o/r.git")
	pa, _ := w.hub.PushesAs(context.Background(), w.store(), repo)
	if pa.SSHKey != key || pa.SSHFrom != GitFromRule || !strings.Contains(pa.SSHCommand, "IdentitiesOnly") {
		t.Fatalf("pushes as: %+v", pa)
	}

	// Taking the key away again, then undo brings it back.
	empty := ""
	p, err = w.hub.PlanSSHKey(w.store(), work, empty)
	if err != nil {
		t.Fatal(err)
	}
	if fin := last(collect(t, w.hub.ApplySettings(context.Background(), w.eng, p))); fin.State != accounts.StepDone {
		t.Fatalf("remove: %+v", fin)
	}
	if fs := w.devpitFiles(); slices.ContainsFunc(fs, func(s string) bool { return strings.HasPrefix(s, "ssh-") }) {
		t.Fatalf("left %v", fs)
	}
	if _, err := w.eng.Undo(); err != nil {
		t.Fatal(err)
	}
	if fs := w.devpitFiles(); !slices.ContainsFunc(fs, func(s string) bool { return strings.HasPrefix(s, "ssh-") }) {
		t.Fatalf("undo did not bring the key file back: %v", fs)
	}
}
