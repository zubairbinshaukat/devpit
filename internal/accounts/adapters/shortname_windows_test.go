//go:build windows

package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// A Git folder rule made for a folder named by its 8.3 short name applies in
// that folder's repositories: Git matches `gitdir/i:` against the long, real
// path, so Devpit writes the long one. (On GitHub's runners TEMP itself is
// C:\Users\RUNNER~1\…, which once made every Git rule look unsupported.)
func TestGitRuleForAShortNameApplies(t *testing.T) {
	tmp := accounts.LongPath(gitShortTemp(t))
	work := filepath.Join(tmp, "Work folder with a long name")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	in, _ := windows.UTF16PtrFromString(work)
	buf := make([]uint16, 4*windows.MAX_PATH)
	n, err := windows.GetShortPathName(in, &buf[0], uint32(len(buf)))
	short := windows.UTF16ToString(buf[:min(int(n), len(buf))])
	if err != nil || strings.EqualFold(short, work) {
		t.Skip("this volume makes no 8.3 short names, so there is nothing to test")
	}

	w := newGitWorldIn(t, tmp)
	w.addIdentity("work", "Zubair", "zubair@work.com")
	repo := filepath.Join(work, "repo")
	w.initRepo(repo)
	p := w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(short)})
	if !strings.Contains(p.Text(), filepath.ToSlash(work)) {
		t.Fatalf("the rule is not written with the long name:\n%s", p.Text())
	}
	if got := w.email(repo); got != "zubair@work.com" {
		t.Fatalf("git commits as %q in %s", got, repo)
	}
	// The capability probe works from a short TEMP too.
	t.Setenv("TMP", shortOf(tmp))
	t.Setenv("TEMP", shortOf(tmp))
	caps, err := probeGit(t.Context(), w.deps)
	if err != nil || !caps.includeIf {
		t.Fatalf("probe from a short TEMP: %+v %v", caps, err)
	}
}

func shortOf(p string) string {
	in, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	buf := make([]uint16, 4*windows.MAX_PATH)
	n, err := windows.GetShortPathName(in, &buf[0], uint32(len(buf)))
	if err != nil || int(n) >= len(buf) {
		return p
	}
	return windows.UTF16ToString(buf[:n])
}
