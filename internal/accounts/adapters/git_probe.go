package adapters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// gitCaps is what the installed Git can do, found by trying it, never by
// reading a version number: Git 2.55 accepts a `worktree/i:` condition and
// silently ignores it, so only a real run tells.
type gitCaps struct {
	install Install
	// includeIf: `includeIf "gitdir/i:…"` works (Git 2.13 and later).
	includeIf bool
	// worktree: `includeIf "worktree/i:…"` works too.
	worktree bool
}

// gitProber runs the probe once and remembers the answer.
type gitProber struct {
	mu   sync.Mutex
	done bool
	caps gitCaps
	err  error
}

func (p *gitProber) get(ctx context.Context, d Deps) (gitCaps, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return p.caps, p.err
	}
	c, err := probeGit(ctx, d)
	if ctx.Err() == nil { // a cancelled probe is asked again next time
		p.done, p.caps, p.err = true, c, err
	}
	return c, err
}

// probeGit makes a throw-away repo in a temp folder, with a throw-away
// global config (GIT_CONFIG_GLOBAL) and no system config, whose two rules
// set two probe values: one by `gitdir/i:` and one by `worktree/i:`. The
// person's own config is never read or written. Patterns are in lower case
// while the folder is not necessarily, so "/i" is tested as well.
func probeGit(ctx context.Context, d Deps) (gitCaps, error) {
	in := FindInstalled(ctx, d, "git", "--version")
	c := gitCaps{install: in}
	if !in.Found {
		return c, fmt.Errorf("git: %w", accounts.ErrNotFoundTool)
	}
	tmp, err := os.MkdirTemp("", "devpit-gitprobe-")
	if err != nil {
		return c, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	work := filepath.Join(tmp, "Work")
	repo := filepath.Join(work, "repo")
	if err = os.MkdirAll(repo, 0o700); err != nil {
		return c, err
	}
	pat, err := gitdirPattern(work)
	if err != nil {
		return c, err
	}
	pat = strings.ToLower(pat)
	inc := func(name, key string) (string, error) {
		p := filepath.Join(tmp, name)
		return slashPath(p), os.WriteFile(p, []byte("[devpitprobe]\n"+gitIndent+key+" = yes\n"), 0o600)
	}
	a, err := inc("a.gitconfig", "gitdir")
	if err != nil {
		return c, err
	}
	b, err := inc("b.gitconfig", "worktree")
	if err != nil {
		return c, err
	}
	qa, _ := gitQuote(a)
	qb, _ := gitQuote(b)
	qg, err := gitQuote("gitdir/i:" + pat)
	if err != nil {
		return c, err
	}
	qw, _ := gitQuote("worktree/i:" + pat)
	global := filepath.Join(tmp, "global.gitconfig")
	cfg := "[includeIf " + qg + "]\n" + gitIndent + "path = " + qa + "\n" +
		"[includeIf " + qw + "]\n" + gitIndent + "path = " + qb + "\n"
	if err = os.WriteFile(global, []byte(cfg), 0o600); err != nil {
		return c, err
	}
	env := []string{"GIT_CONFIG_GLOBAL=" + global, "GIT_CONFIG_NOSYSTEM=1", "GIT_CEILING_DIRECTORIES=" + tmp}
	unset := []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"}

	res, err := d.run(ctx, Cmd{Name: "git", Args: []string{"init", "-q"}, Env: env, Unset: unset, Dir: repo})
	if err != nil {
		return c, accounts.ScrubError(err)
	}
	if res.ExitCode != 0 {
		return c, fmt.Errorf("git init failed in a temp folder: %s", firstOf(FirstLine(res.Stderr), FirstLine(res.Stdout)))
	}
	res, err = d.run(ctx, Cmd{
		Name: "git", Args: []string{"config", "--get-regexp", `^devpitprobe\.`},
		Env: env, Unset: unset, Dir: repo,
	})
	if err != nil {
		return c, accounts.ScrubError(err)
	}
	for _, l := range Lines(res.Stdout) {
		switch strings.ToLower(l) {
		case "devpitprobe.gitdir yes":
			c.includeIf = true
		case "devpitprobe.worktree yes":
			c.worktree = true
		}
	}
	return c, nil
}

// gitTooOld is the reason Git cannot take folder rules.
func gitTooOld(version string) error {
	v := version
	if v == "" {
		v = "on this PC"
	}
	return fmt.Errorf("Git %s does not apply `includeIf` folder rules, which Devpit uses for commit identities. Update Git to 2.13 or later: %w", v, accounts.ErrTooOld) //nolint:revive,staticcheck // a product name
}

// gitCheck turns the probe into the error a method returns: not installed,
// too old, or the probe itself failing.
func gitCheck(c gitCaps, err error) error {
	switch {
	case errors.Is(err, accounts.ErrNotFoundTool):
		return fmt.Errorf("Git: %w", accounts.ErrNotFoundTool) //nolint:revive,staticcheck // a product name
	case err != nil:
		return fmt.Errorf("Devpit could not check what this Git supports: %w", err) //nolint:revive,staticcheck // a product name
	case !c.includeIf:
		return gitTooOld(c.install.Version)
	}
	return nil
}
