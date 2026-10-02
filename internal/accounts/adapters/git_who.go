package adapters

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// GitFrom says where an effective Git value came from, in words the Git
// page shows next to it.
type GitFrom string

const (
	GitFromRule        GitFrom = "Devpit folder rule"
	GitFromEverywhere  GitFrom = "Devpit, everywhere"
	GitFromDefaultRule GitFrom = "Devpit folder rule (your own identity)"
	GitFromRepo        GitFrom = "this repo's own config"
	GitFromGlobal      GitFrom = "your global Git config"
	// GitFromAfterDevpit: a line in the global config after Devpit's
	// include, which wins over the rules.
	GitFromAfterDevpit GitFrom = "a line after Devpit's include in your global Git config"
	GitFromSystem      GitFrom = "Git's system config"
	GitFromCommand     GitFrom = "the command line or the environment"
	GitFromOther       GitFrom = "another config file"
	GitFromNowhere     GitFrom = "not set"
)

// GitValue is one effective Git setting and where it came from.
type GitValue struct {
	Value string
	// Scope is Git's own word: system, global, local, worktree, command.
	Scope string
	// File is the config file that set it, as Git reports it.
	File string
	From GitFrom
}

// GitRepo is what Git says about a folder.
type GitRepo struct {
	IsRepo   bool
	TopLevel string
	// GitDir is this checkout's .git folder; CommonDir the repository's.
	GitDir, CommonDir string
	// LinkedWorktree: a `git worktree add` checkout, whose repository is
	// elsewhere. Submodule: a submodule, whose .git lives in the parent.
	LinkedWorktree, Submodule bool
	// MatchFolder is the folder `gitdir/i:` rules are matched against:
	// the main repository's folder for a linked worktree or a submodule.
	MatchFolder string
}

// GitCommitIdentity is the Git page's "Commits as" row for one folder.
type GitCommitIdentity struct {
	Folder string
	Repo   GitRepo
	Name   GitValue
	Email  GitValue
	// Expected is the account Devpit's rules pick for the folder, with the
	// reason and the rule chain; ExpectedName and ExpectedEmail are who
	// that is ("default" is the person's own global identity).
	Expected      accounts.Resolution
	ExpectedName  string
	ExpectedEmail string
	// Mismatch: in a repo, Git would commit as someone else than the rule
	// says. Notes say why, in plain words.
	Mismatch bool
	Notes    []string
}

// CommitsAs asks Git, offline, who it would commit as in folder and which
// file decided it, and compares that with Devpit's rules. It handles a
// folder that is not a repo yet, a repo's own user.email, lines after
// Devpit's include, linked worktrees and submodules whose .git is
// elsewhere, and Git being absent (ErrNotFoundTool).
func (a *GitAdapter) CommitsAs(ctx context.Context, s *accounts.Store, folder string) (GitCommitIdentity, error) {
	if s == nil {
		s = accounts.NewStore()
	}
	out := GitCommitIdentity{Folder: folder}
	if fi, err := os.Stat(folder); err != nil || !fi.IsDir() {
		return out, fmt.Errorf("the folder %s was not found", folder)
	}
	res, err := accounts.Resolve(s, accounts.ToolGit, folder, accounts.ResolveOptions{RealPath: accounts.RealPath})
	if err != nil {
		return out, err
	}
	out.Expected = res

	g := gitSync{d: a.deps, dir: GitDir(a.deps), global: gitGlobalPath(a.deps)}
	if out.Repo, err = gitRepoInfo(ctx, a.deps, folder); err != nil {
		return out, err
	}
	if out.Name, err = g.effective(ctx, folder, "user.name", res); err != nil {
		return out, err
	}
	if out.Email, err = g.effective(ctx, folder, "user.email", res); err != nil {
		return out, err
	}

	if res.Account.IsDefault() {
		if out.ExpectedName, out.ExpectedEmail, err = g.ownIdentity(ctx); err != nil {
			return out, err
		}
	} else {
		out.ExpectedName, out.ExpectedEmail = res.Account.Label, res.Account.Email
	}

	note := func(f string, args ...any) { out.Notes = append(out.Notes, fmt.Sprintf(f, args...)) }
	switch {
	case !out.Repo.IsRepo:
		if res.Reason == accounts.ReasonFolderRule {
			note("This folder is not a Git repo yet. The rule applies once it is one (git init or git clone); until then Git would use %s.",
				firstOf(out.Email.Value, "no email"))
		}
	case !strings.EqualFold(out.Email.Value, out.ExpectedEmail):
		out.Mismatch = true
		switch out.Email.From {
		case GitFromRepo:
			note("This repo's own config sets user.email to %s, and a repo's own setting wins over every rule. Remove it with: git config --local --unset user.email", out.Email.Value)
		case GitFromAfterDevpit:
			note("A user.email line after Devpit's include in %s wins over the rules. Move Devpit's [include] block to the end of that file.", out.Email.File)
		case GitFromCommand:
			note("user.email comes from the command line or the environment (GIT_CONFIG_*), which wins over every file.")
		}
		if c, _ := a.probe.get(ctx, a.deps); !c.worktree && (out.Repo.LinkedWorktree || out.Repo.Submodule) &&
			out.Repo.MatchFolder != "" && !accounts.SameFolder(out.Repo.MatchFolder, out.Repo.TopLevel) {
			kind := "a linked worktree"
			if out.Repo.Submodule {
				kind = "a submodule"
			}
			note("This is %s whose repository is in %s, and this Git matches folder rules against that folder. Git 2.56's worktree/i: rules fix this; Devpit uses them as soon as your Git supports them.",
				kind, out.Repo.MatchFolder)
		}
	}
	for _, v := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		if val := a.deps.getenv(v); val != "" {
			note("%s is set in this terminal to %s; commits made here use it instead of any config.", v, accounts.Scrub(val))
		}
	}
	return out, nil
}

// gitRepoInfo asks Git whether folder is in a repository, and where its
// .git folders are.
func gitRepoInfo(ctx context.Context, d Deps, folder string) (GitRepo, error) {
	res, err := d.run(ctx, Cmd{Name: "git", Dir: folder, Args: []string{
		"rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir", "--show-toplevel", "--show-superproject-working-tree",
	}})
	if err != nil {
		return GitRepo{}, accounts.ScrubError(err)
	}
	if res.ExitCode != 0 {
		if strings.Contains(strings.ToLower(string(res.Stderr)), "not a git repository") {
			return GitRepo{}, nil
		}
		return GitRepo{}, fmt.Errorf("Git could not look at %s: %s", folder, firstOf(FirstLine(res.Stderr), FirstLine(res.Stdout))) //nolint:revive,staticcheck // a product name
	}
	ls := Lines(res.Stdout)
	if len(ls) < 2 {
		// A bare repository has no top level; nothing commits there.
		return GitRepo{IsRepo: false}, nil
	}
	r := GitRepo{IsRepo: true, GitDir: ls[0], CommonDir: ls[1]}
	if len(ls) > 2 {
		r.TopLevel = ls[2]
	}
	if len(ls) > 3 && ls[3] != "" {
		r.Submodule = true
	}
	r.LinkedWorktree = !sameConfigPath(r.GitDir, r.CommonDir)
	if strings.Contains(strings.ToLower(slashPath(r.GitDir)), "/modules/") {
		r.Submodule = true
	}
	// gitdir/i: rules match the .git folder's path: the main repository's
	// folder is the parent of the folder that holds it.
	gd := r.GitDir
	if r.LinkedWorktree {
		gd = r.CommonDir
	}
	if strings.EqualFold(filepath.Base(filepath.FromSlash(gd)), ".git") {
		r.MatchFolder = filepath.Dir(filepath.FromSlash(gd))
	} else {
		r.MatchFolder = filepath.FromSlash(gd)
	}
	return r, nil
}

// effective reads one key's values in folder with their scope and file,
// takes the last (the one in effect) and says where it came from.
func (g gitSync) effective(ctx context.Context, folder, key string, res accounts.Resolution) (GitValue, error) {
	r, err := g.d.run(ctx, Cmd{Name: "git", Dir: folder, Args: []string{"config", "--show-origin", "--show-scope", "-z", "--get-all", key}})
	if err != nil {
		return GitValue{}, accounts.ScrubError(err)
	}
	if r.ExitCode == 1 {
		return GitValue{From: GitFromNowhere}, nil
	}
	if r.ExitCode != 0 {
		return GitValue{}, fmt.Errorf("Git could not read %s in %s: %s", key, folder, FirstLine(r.Stderr)) //nolint:revive,staticcheck // a product name
	}
	recs := zRecords(r.Stdout)
	var last GitValue
	devpitBefore := false
	for i := 0; i+2 < len(recs); i += 3 {
		if last.File != "" && pathInside(g.dir, last.File) {
			devpitBefore = true
		}
		file, _ := originPath(recs[i+1])
		last = GitValue{Scope: recs[i], File: file, Value: recs[i+2]}
	}
	last.From = g.classify(last, devpitBefore, res)
	return last, nil
}

func (g gitSync) classify(v GitValue, devpitBefore bool, res accounts.Resolution) GitFrom {
	switch v.Scope {
	case "local", "worktree":
		return GitFromRepo
	case "command":
		return GitFromCommand
	case "system":
		return GitFromSystem
	}
	switch {
	case pathInside(g.dir, v.File):
		switch {
		case strings.EqualFold(filepath.Base(filepath.FromSlash(v.File)), gitDefaultFile):
			return GitFromDefaultRule
		case res.Reason == accounts.ReasonFolderRule:
			return GitFromRule
		}
		return GitFromEverywhere
	case sameConfigPath(v.File, g.global):
		if devpitBefore {
			return GitFromAfterDevpit
		}
		return GitFromGlobal
	}
	return GitFromOther
}

// GitIncludeStatus is whether Devpit's include is in the global config,
// and which keys after it override the rules (for Verify).
type GitIncludeStatus struct {
	GlobalFile string
	Present    bool
	// After lists keys (user.email, include.path…) that come after
	// Devpit's include and so win over the rules.
	After []string
}

// IncludeStatus reports where Devpit's include line stands.
func (a *GitAdapter) IncludeStatus(ctx context.Context) (GitIncludeStatus, error) {
	g := gitSync{d: a.deps, dir: GitDir(a.deps), global: gitGlobalPath(a.deps)}
	st, err := g.includeStatus(ctx)
	if err != nil {
		return GitIncludeStatus{GlobalFile: g.global}, err
	}
	return GitIncludeStatus{GlobalFile: g.global, Present: st.present, After: st.after}, nil
}
