package adapters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/gitssh"
)

// GitSettingsChange is a change to Devpit's Git settings that accounts.toml
// has no field for. Set one part; a change with nothing set re-syncs
// Devpit's Git files with the store (a repair after a file was deleted).
type GitSettingsChange struct {
	// Identity and Signing set how commits under a named identity are
	// signed; a zero GitSigning stops signing.
	Identity string
	Signing  *GitSigning
	// Folder and SSHKey set the private key Git pushes with over SSH from
	// Folder and every folder inside it; "" removes the folder's key.
	Folder string
	SSHKey *string
}

// GitSettingsPreview is shown before a GitSettingsChange is made: plain
// sentences and the exact lines of every file written. Apply it with
// ApplySettings.
type GitSettingsPreview struct {
	Sentences []string
	Edits     []accounts.FileEdit
	Warnings  []string
	Summary   string
	NoChange  bool
	// BeforeHash is the store the preview was built from.
	BeforeHash string

	change GitSettingsChange
}

// Lines renders the preview the way accounts.Preview.Lines does.
func (p GitSettingsPreview) Lines() []string {
	out := append(append([]string(nil), p.Sentences...), p.Warnings...)
	if len(p.Edits) > 0 && !p.NoChange {
		out = append(out, "", "Devpit will write:")
		for _, e := range p.Edits {
			out = append(out, "  "+e.Path+" ("+e.Action+")")
			for _, l := range e.Lines {
				out = append(out, "    "+l)
			}
		}
	}
	return out
}

// mutate applies c to devpit.json's settings, checking every value.
func (c GitSettingsChange) mutate(s *accounts.Store) (func(*gitExtras) error, []string, string, error) {
	switch {
	case c.Signing != nil && c.SSHKey != nil:
		return nil, nil, "", errors.New("change signing and an SSH key one at a time")
	case c.Signing != nil:
		a, ok := s.Account(accounts.ToolGit, c.Identity)
		if !ok {
			return nil, nil, "", &accounts.UnknownNameError{Input: c.Identity, Known: s.Names(accounts.ToolGit)}
		}
		if a.IsDefault() {
			return nil, nil, "", errors.New("Devpit does not change signing for your own identity; set it in your ~/.gitconfig") //nolint:revive,staticcheck // a product name
		}
		sg := *c.Signing
		if err := sg.validate(); err != nil {
			return nil, nil, "", err
		}
		say := fmt.Sprintf("Commits as %s will no longer be signed by Devpit's settings.", gitDisplay(a))
		if !sg.IsZero() {
			say = fmt.Sprintf("Commits as %s will be signed", gitDisplay(a))
			if sg.Key != "" {
				say += " with " + sg.Key
			}
			if !sg.Sign {
				say += " when you ask for it (git commit -S)"
			}
			say += "."
		}
		return func(x *gitExtras) error {
			if sg.IsZero() {
				delete(x.Signing, strings.ToLower(a.Name))
				return nil
			}
			if x.Signing == nil {
				x.Signing = map[string]GitSigning{}
			}
			x.Signing[strings.ToLower(a.Name)] = sg
			return nil
		}, []string{say}, "Signing for the Git identity " + a.Name, nil

	case c.SSHKey != nil:
		norm, err := accounts.NormalizeFolder(c.Folder, "")
		if err != nil {
			return nil, nil, "", err
		}
		key := strings.TrimSpace(*c.SSHKey)
		if key == "" {
			return func(x *gitExtras) error {
				for f := range x.SSHKeys {
					if accounts.SameFolder(f, norm) {
						delete(x.SSHKeys, f)
					}
				}
				return nil
			}, []string{fmt.Sprintf("Git will push over SSH from %s with your usual SSH keys again.", norm)}, "Removed the SSH key for " + norm, nil
		}
		if !filepath.IsAbs(key) {
			return nil, nil, "", fmt.Errorf("the SSH key %s must be a full path", key)
		}
		if strings.HasSuffix(strings.ToLower(key), ".pub") {
			return nil, nil, "", fmt.Errorf("%s is a public key; pick the private key next to it (the same name without .pub)", key)
		}
		if _, err := gitQuote(sshCommand(key)); err != nil {
			return nil, nil, "", err
		}
		if fi, err := os.Stat(key); err != nil || fi.IsDir() {
			return nil, nil, "", fmt.Errorf("there is no SSH key at %s. Generate one first (GenerateSSHKey)", key)
		}
		set := func(x *gitExtras) error {
			if x.SSHKeys == nil {
				x.SSHKeys = map[string]string{}
			}
			for f := range x.SSHKeys {
				if accounts.SameFolder(f, norm) {
					delete(x.SSHKeys, f)
				}
			}
			x.SSHKeys[norm] = key
			return nil
		}
		say := []string{
			fmt.Sprintf("In %s and every folder inside it, Git will push over SSH with the key %s, and only that key.", norm, key),
			"GitHub then sees the account that key belongs to. Pushes over HTTPS are not affected.",
		}
		return set, say, "SSH key for " + norm, nil
	}
	return func(*gitExtras) error { return nil }, []string{"Devpit will rewrite its Git files to match your rules."}, "Repaired Devpit's Git files", nil
}

// PlanSettings previews a GitSettingsChange.
func (a *GitAdapter) PlanSettings(s *accounts.Store, c GitSettingsChange) (GitSettingsPreview, error) {
	return planSettings(a.deps, &a.probe, "", s, c)
}

func planSettings(d Deps, pr *gitProber, exe string, s *accounts.Store, c GitSettingsChange) (GitSettingsPreview, error) {
	if s == nil {
		s = accounts.NewStore()
	}
	mutate, say, summary, err := c.mutate(s)
	if err != nil {
		return GitSettingsPreview{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	plan, err := planGitFiles(ctx, d, pr, exe, s, mutate)
	if err != nil {
		return GitSettingsPreview{}, err
	}
	p := GitSettingsPreview{
		Sentences: say, Edits: plan.edits(d.Home), Warnings: plan.warnings,
		Summary: summary, BeforeHash: accounts.StoreHash(s), change: c,
	}
	if len(p.Edits) == 0 {
		p.NoChange = true
		p.Sentences = []string{"Nothing would change."}
	}
	return p, nil
}

// ApplySettings makes a previewed GitSettingsChange as one journalled
// change (undo puts every file back) and reports each step. The final
// event carries the journal entry id.
func (a *GitAdapter) ApplySettings(ctx context.Context, eng *accounts.Engine, p GitSettingsPreview) <-chan accounts.Event {
	return applySettings(ctx, a.deps, &a.probe, "", eng, p)
}

func applySettings(ctx context.Context, d Deps, pr *gitProber, exe string, eng *accounts.Engine, p GitSettingsPreview) <-chan accounts.Event {
	return Stream("Saving the change", func(emit func(accounts.Event)) error {
		if p.NoChange {
			return errors.New("this change would change nothing")
		}
		txn, err := eng.Begin(p.Summary, func(s *accounts.Store) error {
			if accounts.StoreHash(s) != p.BeforeHash {
				return accounts.ErrStalePreview
			}
			return nil
		})
		if err != nil {
			return err
		}
		fail := func(err error) error {
			if rerr := txn.Rollback(); rerr != nil {
				return fmt.Errorf("%w; putting things back also failed: %w", err, rerr)
			}
			return err
		}
		after := txn.After()
		mutate, _, _, err := p.change.mutate(after)
		if err != nil {
			return fail(err)
		}
		g, err := newGitSync(ctx, d, pr, exe)
		if err != nil {
			return fail(err)
		}
		plan, err := planGitFiles(ctx, d, pr, exe, after, mutate)
		if err != nil {
			return fail(err)
		}
		if !sameEdits(plan.edits(d.Home), p.Edits) {
			return fail(fmt.Errorf("Devpit's Git files would no longer be what the preview showed: %w", accounts.ErrStalePreview)) //nolint:revive,staticcheck // a product name
		}
		if err = g.apply(ctx, txn, plan, emit); err != nil {
			return fail(err)
		}
		entry, err := txn.Commit()
		if err != nil {
			return err
		}
		ev := accounts.NewEvent("Saving the change", accounts.StepDone, entry.Summary)
		ev.Final, ev.EntryID = true, entry.ID
		emit(ev)
		return nil
	})
}

// SuggestedSSHKeyPath is where Devpit suggests a new key for a GitHub
// account: ~/.ssh/id_ed25519_devpit_<name>.
func SuggestedSSHKeyPath(d Deps, account string) string {
	return filepath.Join(d.Home, ".ssh", "id_ed25519_devpit_"+strings.ToLower(account))
}

// GenerateSSHKey makes a new ed25519 key pair at path through
// internal/gitssh, which refuses to overwrite an existing key (safety
// rule 15: a *gitssh.ExistsError comes back and ssh-keygen never runs).
// comment is usually the account's email. The public key is returned for
// the person to add to GitHub; the private key is never read.
func GenerateSSHKey(ctx context.Context, path, comment string) (gitssh.KeygenResult, error) {
	if !filepath.IsAbs(path) {
		return gitssh.KeygenResult{}, fmt.Errorf("the key path %s must be a full path", path)
	}
	return gitssh.Keygen(ctx, gitssh.KeygenOptions{Path: path, Comment: comment, Algorithm: "ed25519"}, gitssh.Options{})
}
