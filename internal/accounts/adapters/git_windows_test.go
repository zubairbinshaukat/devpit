//go:build windows

package adapters

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// These tests run the real Git against real folders, with an isolated
// GIT_CONFIG_GLOBAL in a temp folder. Windows only: rules are Windows paths.

func TestGitNearestRuleWinsThroughRealGit(t *testing.T) {
	w := newGitWorld(t)
	w.addIdentity("work", "Work Me", "work@example.invalid")
	w.addIdentity("deep", "Deep Me", "deep@example.invalid")
	w.addIdentity("uni", "Zübair Ünicode", "uni@example.invalid")
	w.addIdentity("brack", "Bracket", "brack@example.invalid")
	work := filepath.Join(w.tmp, "Work")
	deep := filepath.Join(work, "deep")
	uni := filepath.Join(w.tmp, "My Projects", "Zübair 日本")
	brack := filepath.Join(w.tmp, "Old [2024] stuff")
	// The deeper rule is made first: the file must still list it last.
	w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "deep", Scope: accounts.FolderScope(deep)})
	w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(work)})
	w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "uni", Scope: accounts.FolderScope(uni)})
	w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "brack", Scope: accounts.FolderScope(brack)})

	cases := map[string]string{
		filepath.Join(work, "a"):                       "work@example.invalid",
		filepath.Join(deep, "b"):                       "deep@example.invalid",
		filepath.Join(deep, "more", "c"):               "deep@example.invalid",
		work:                                           "work@example.invalid", // a repo right on the ruled folder
		filepath.Join(w.tmp, "Workshop", "d"):          "base@example.invalid", // C:\Work is not C:\Workshop
		filepath.Join(w.tmp, "plain", "e"):             "base@example.invalid",
		filepath.Join(uni, "repo"):                     "uni@example.invalid",
		filepath.Join(brack, "repo"):                   "brack@example.invalid",
		filepath.Join(w.tmp, "Old 2 stuff", "notmine"): "base@example.invalid", // [2024] is not a character class
	}
	for dir, want := range cases {
		w.initRepo(dir)
		if got := w.email(dir); got != want {
			t.Errorf("%s: Git commits as %q, want %q", dir, got, want)
		}
	}
	// Case-insensitive: a folder typed in another case still matches.
	lower := strings.ToLower(filepath.Join(work, "lowercase"))
	w.initRepo(lower)
	if got := w.email(lower); got != "work@example.invalid" {
		t.Errorf("lower-case path: %q", got)
	}
	rules, _ := os.ReadFile(filepath.Join(GitDir(w.deps), gitRulesFile))
	t.Logf("rules.gitconfig:\n%s", rules)
	if i, j := strings.Index(string(rules), "/Work/\""), strings.Index(string(rules), "/Work/deep/\""); i < 0 || j < i {
		t.Fatalf("the outer rule must come before the deeper one:\n%s", rules)
	}
}

func TestGitCloneIntoRuledFolderCommitsWithTheRightEmail(t *testing.T) {
	w := newGitWorld(t)
	w.addIdentity("work", "Work Me", "work@example.invalid")
	work := filepath.Join(w.tmp, "Work Space")
	w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(work)})

	src := filepath.Join(w.tmp, "src")
	w.initRepo(src)
	w.gitOut(src, "-c", "user.email=src@example.invalid", "-c", "user.name=Src", "commit", "-q", "--allow-empty", "-m", "first")
	caller := filepath.Join(w.tmp, "plain")
	_ = os.MkdirAll(caller, 0o700)
	_ = os.MkdirAll(work, 0o700)
	target := filepath.Join(work, "c1")
	w.gitOut(caller, "clone", "-q", src, target)

	logHead, _ := os.ReadFile(filepath.Join(target, ".git", "logs", "HEAD"))
	if !strings.Contains(string(logHead), "work@example.invalid") {
		t.Errorf("the clone itself was recorded as: %s", logHead)
	}
	w.gitOut(target, "commit", "-q", "--allow-empty", "-m", "mine")
	if got := w.gitOut(target, "log", "-1", "--format=%an <%ae>"); got != "Work Me <work@example.invalid>" {
		t.Fatalf("the commit is by %q", got)
	}
}

func TestGitEverywhereAndADefaultRuleInside(t *testing.T) {
	w := newGitWorld(t)
	w.addIdentity("work", "Work Me", "work@example.invalid")
	w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.EverywhereScope()})
	oss := filepath.Join(w.tmp, "oss")
	p := w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "default", Scope: accounts.FolderScope(oss)})
	if !strings.Contains(p.Text(), "default.gitconfig (adds)") || !strings.Contains(p.Text(), `email = "base@example.invalid"`) {
		t.Fatalf("preview:\n%s", p.Text())
	}
	w.initRepo(filepath.Join(oss, "x"))
	w.initRepo(filepath.Join(w.tmp, "elsewhere"))
	if got := w.email(filepath.Join(oss, "x")); got != "base@example.invalid" {
		t.Errorf("oss = %q", got)
	}
	if got := w.email(filepath.Join(w.tmp, "elsewhere")); got != "work@example.invalid" {
		t.Errorf("elsewhere = %q", got)
	}
}

func TestGitRuleOnAUNCPath(t *testing.T) {
	w := newGitWorld(t)
	unc := `\\localhost\` + strings.Replace(w.tmp, ":", "$", 1)
	if _, err := os.Stat(unc); err != nil {
		t.Skipf("no admin share here: %v", err)
	}
	_ = os.WriteFile(w.global, []byte(baseGlobal+"[safe]\n\tdirectory = *\n"), 0o600)
	w.orig, _ = os.ReadFile(w.global)
	w.addIdentity("net", "Net Me", "net@example.invalid")
	folder := filepath.Join(unc, "share-proj")
	p := w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "net", Scope: accounts.FolderScope(folder)})
	if !strings.Contains(p.Text(), `"gitdir/i://localhost/`) {
		t.Fatalf("a UNC rule must be written as //server/share/:\n%s", p.Text())
	}
	repo := filepath.Join(folder, "repo")
	w.initRepo(repo)
	if got := w.email(repo); got != "net@example.invalid" {
		t.Errorf("through the share Git commits as %q", got)
	}
}

func TestGitCommitsAsSaysWhichFileWon(t *testing.T) {
	w := newGitWorld(t)
	w.addIdentity("work", "Work Me", "work@example.invalid")
	work := filepath.Join(w.tmp, "Work")
	w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(work)})
	ctx := context.Background()

	// Not a repo yet.
	_ = os.MkdirAll(filepath.Join(work, "new"), 0o700)
	c, err := w.git.CommitsAs(ctx, w.store(), filepath.Join(work, "new"))
	if err != nil || c.Repo.IsRepo || c.Mismatch || len(c.Notes) == 0 || !strings.Contains(c.Notes[0], "not a Git repo yet") {
		t.Fatalf("not a repo: %+v %v", c, err)
	}

	repo := filepath.Join(work, "r")
	w.initRepo(repo)
	c, err = w.git.CommitsAs(ctx, w.store(), filepath.Join(repo))
	if err != nil || c.Email.Value != "work@example.invalid" || c.Email.From != GitFromRule || c.Mismatch || c.ExpectedEmail != "work@example.invalid" {
		t.Fatalf("ruled repo: %+v %v", c, err)
	}

	// The repo's own user.email wins over every rule.
	w.gitOut(repo, "config", "--local", "user.email", "local@example.invalid")
	c, _ = w.git.CommitsAs(ctx, w.store(), repo)
	if !c.Mismatch || c.Email.From != GitFromRepo || !strings.Contains(strings.Join(c.Notes, " "), "repo's own config") {
		t.Fatalf("local override: %+v", c)
	}
	w.gitOut(repo, "config", "--local", "--unset", "user.email")

	// A line after Devpit's include.
	_ = os.WriteFile(w.global, append(w.readGlobal(), []byte("[user]\n\temail = late@example.invalid\n")...), 0o600)
	c, _ = w.git.CommitsAs(ctx, w.store(), repo)
	if !c.Mismatch || c.Email.From != GitFromAfterDevpit || c.Email.Value != "late@example.invalid" {
		t.Fatalf("after include: %+v", c)
	}
	_ = os.WriteFile(w.global, w.readGlobal()[:len(w.readGlobal())-len("[user]\n\temail = late@example.invalid\n")], 0o600)

	// A linked worktree in Work whose repository is elsewhere.
	main := filepath.Join(w.tmp, "plain", "main")
	w.initRepo(main)
	w.gitOut(main, "-c", "user.email=x@example.invalid", "-c", "user.name=X", "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(work, "wt")
	w.gitOut(main, "worktree", "add", "-q", wt)
	c, err = w.git.CommitsAs(ctx, w.store(), wt)
	if err != nil || !c.Repo.LinkedWorktree || !accounts.SameFolder(accounts.LongPath(c.Repo.MatchFolder), accounts.LongPath(main)) {
		t.Fatalf("worktree: %+v %v", c, err)
	}
	if caps, _ := w.git.probe.get(ctx, w.deps); !caps.worktree {
		if !c.Mismatch || !strings.Contains(strings.Join(c.Notes, " "), "linked worktree") {
			t.Fatalf("worktree on a Git without worktree/i: %+v", c)
		}
	} else if c.Mismatch {
		t.Fatalf("worktree/i: is supported, yet the worktree does not follow its folder: %+v", c)
	}
}
