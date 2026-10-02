package adapters

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// Cleanup is the preview of `devpit accounts cleanup`'s journalled part:
// every folder rule and "everywhere" choice taken out of accounts.toml (the
// named accounts stay listed, and no account folder or sign-in is touched),
// Devpit's Git files and its include block removed (which takes the
// github.com credential helper with them), and every Wrangler folder
// binding Devpit made removed. Bindings made by hand are left alone. Apply
// it with ApplyCleanup; one Undo puts it all back.
type Cleanup struct {
	// Sentences say what will happen, in plain words.
	Sentences []string
	// Edits are the exact files and lines changed.
	Edits []accounts.FileEdit
	// Notes are what is left in place, and why.
	Notes []string
	// Rules is how many folder rules and "everywhere" choices are removed.
	Rules int
	// Empty: nothing of this part to remove.
	Empty bool
	// BeforeHash is the store this was built from.
	BeforeHash string

	gitOK    bool
	wrangler []wranglerOp
}

// clearedStore is s with every rule and "everywhere" choice removed.
func clearedStore(s *accounts.Store) *accounts.Store {
	out := s.Clone()
	out.Rules = nil
	out.Everywhere = map[accounts.Tool]string{}
	return out
}

// PlanCleanup works out the cleanup for store s. It changes nothing.
func PlanCleanup(ctx context.Context, d Deps, s *accounts.Store) (Cleanup, error) {
	if s == nil {
		s = accounts.NewStore()
	}
	c := Cleanup{BeforeHash: accounts.StoreHash(s), Rules: len(s.Rules) + len(s.Everywhere)}
	if c.Rules > 0 {
		c.Sentences = append(c.Sentences, fmt.Sprintf("Remove %d folder rule(s) and \"everywhere\" choice(s) from accounts.toml. Your %d named account(s) stay listed; no account folder or sign-in is touched.",
			len(s.Rules)+len(s.Everywhere), len(s.Accounts)))
	}
	plan, err := cleanupGit(ctx, d, s)
	switch {
	case errors.Is(err, accounts.ErrNotFoundTool) || errors.Is(err, accounts.ErrTooOld):
		c.Notes = append(c.Notes, "Git is not installed (or too old), so Devpit could not check its Git files in "+GitDir(d)+"; remove them and the [include] block at the end of your ~/.gitconfig by hand if they are there.")
	case err != nil:
		return Cleanup{}, err
	default:
		c.gitOK = true
		if len(plan.ops) > 0 {
			c.Sentences = append(c.Sentences, "Remove Devpit's Git files and its [include] block from your global Git config (Git's sign-in helper for github.com goes back to the one you had).")
			c.Edits = append(c.Edits, plan.edits(d.Home)...)
		}
	}
	ops, notes, err := cleanupWrangler(d, s)
	if err != nil {
		return Cleanup{}, err
	}
	c.wrangler, c.Notes = ops, append(c.Notes, notes...)
	path := WranglerBindingsFile(d)
	for _, op := range ops {
		c.Edits = append(c.Edits, op.edit(path))
	}
	if len(ops) > 0 {
		c.Sentences = append(c.Sentences, fmt.Sprintf("Remove the %d Wrangler folder binding(s) Devpit made.", len(ops)))
	}
	c.Empty = c.Rules == 0 && len(c.Edits) == 0
	return c, nil
}

func cleanupGit(ctx context.Context, d Deps, s *accounts.Store) (gitPlan, error) {
	g, err := newGitSync(ctx, d, &gitProber{}, "")
	if err != nil {
		return gitPlan{}, err
	}
	return g.compute(ctx, clearedStore(s), gitExtras{})
}

// cleanupWrangler lists the bindings that match a Devpit Cloudflare rule.
func cleanupWrangler(d Deps, s *accounts.Store) ([]wranglerOp, []string, error) {
	bindings, err := readWranglerBindings(WranglerBindingsFile(d))
	if err != nil {
		return nil, nil, err
	}
	var ops []wranglerOp
	var notes []string
	claimed := map[string]bool{}
	for _, r := range s.Rules {
		name, ok := r.Accounts[accounts.ToolCloudflare]
		if !ok || accounts.IsDefault(name) {
			continue
		}
		dir := wranglerTrueCase(r.Folder)
		if bindings[dir] == name {
			ops = append(ops, wranglerOp{Dir: dir, Before: name})
			claimed[dir] = true
		}
	}
	keys := make([]string, 0, len(bindings))
	for k := range bindings {
		if !claimed[k] {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		notes = append(notes, fmt.Sprintf("Wrangler binds %s to the profile %s, but Devpit did not make that binding, so it stays.", k, bindings[k]))
	}
	slices.SortFunc(ops, func(a, b wranglerOp) int { return strings.Compare(a.Dir, b.Dir) })
	return ops, notes, nil
}

// ApplyCleanup makes a previewed cleanup as one journalled change. It
// stops with accounts.ErrStalePreview when anything differs from the
// preview.
func ApplyCleanup(ctx context.Context, eng *accounts.Engine, d Deps, c Cleanup, emit func(accounts.Event)) (accounts.Entry, error) {
	if emit == nil {
		emit = func(accounts.Event) {}
	}
	if c.Empty {
		return accounts.Entry{}, errors.New("there is nothing of Devpit's to remove from accounts.toml, Git or Wrangler")
	}
	txn, err := eng.Begin("Removed every folder rule, Devpit's Git files and its Wrangler bindings", func(s *accounts.Store) error {
		if accounts.StoreHash(s) != c.BeforeHash {
			return accounts.ErrStalePreview
		}
		*s = *clearedStore(s)
		return nil
	})
	if err != nil {
		return accounts.Entry{}, err
	}
	fail := func(err error) (accounts.Entry, error) {
		if rerr := txn.Rollback(); rerr != nil {
			return accounts.Entry{}, fmt.Errorf("%w; putting things back also failed: %w", err, rerr)
		}
		return accounts.Entry{}, err
	}
	if c.gitOK {
		plan, err := cleanupGit(ctx, d, txn.Before())
		if err != nil {
			return fail(err)
		}
		var shown []accounts.FileEdit
		for _, e := range c.Edits {
			if !strings.HasSuffix(e.Path, "directory-bindings.json") {
				shown = append(shown, e)
			}
		}
		if !sameEdits(plan.edits(d.Home), shown) {
			return fail(fmt.Errorf("Devpit's Git files are no longer what the preview showed: %w", accounts.ErrStalePreview)) //nolint:revive,staticcheck // a product name
		}
		g, err := newGitSync(ctx, d, &gitProber{}, "")
		if err != nil {
			return fail(err)
		}
		if len(plan.ops) > 0 {
			if err := g.apply(ctx, txn, plan, emit); err != nil {
				return fail(err)
			}
		}
	}
	if len(c.wrangler) > 0 {
		a, _ := newCloudflare(d).(*cloudflareAdapter)
		for _, op := range c.wrangler {
			what := "Removing Wrangler's binding on " + op.Dir
			emit(accounts.NewEvent(what, accounts.StepRunning, ""))
			eff := accounts.Effect{
				Kind: EffectCloudflareBinding, Tool: accounts.ToolCloudflare, Target: op.Dir, Summary: what,
				Data: map[string]string{"dir": op.Dir, "before": op.Before, "after": ""},
			}
			if err := txn.Do(eff, func() (accounts.Effect, error) {
				cctx, cancel := context.WithTimeout(ctx, time.Minute)
				defer cancel()
				return eff, a.setBinding(cctx, op.Dir, "")
			}); err != nil {
				return fail(err)
			}
			emit(accounts.NewEvent(what, accounts.StepDone, ""))
		}
	}
	return txn.Commit()
}
