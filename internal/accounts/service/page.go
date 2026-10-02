package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/claudeshare"
	"github.com/zubairbinshaukat/devpit/internal/accounts/importer"
	"github.com/zubairbinshaukat/devpit/internal/gitssh"
)

// This file is what the Accounts screens need beyond what the command line
// uses: small, read-mostly answers (what a tool supports here, whether a
// name is free, which rules are stale), the Git page's questions, the Claude
// setup, the first-open import card's memory, and the two changes the page
// makes that are not an accounts.Change (renaming or removing an account,
// and a group of changes undone together).

// Caps is what Devpit can do with tool on this PC, with the plain-words
// reason for anything it cannot. It may ask the tool for its --help once
// (never a sign-in check); the adapter remembers the answer.
func (s *Service) Caps(ctx context.Context, t accounts.Tool) adapters.Caps {
	a, err := s.Adapter(t)
	if err != nil {
		return adapters.Caps{Why: err.Error()}
	}
	return a.Capabilities(ctx)
}

// LiveCheckRisk says whether asking tool who an idle account is can harm
// that account, and why, in the sentence a screen shows before asking.
func (s *Service) LiveCheckRisk(t accounts.Tool) (bool, string) {
	a, err := s.Adapter(t)
	if err != nil {
		return false, ""
	}
	return a.LiveCheckRisk()
}

// CheckNewName reports whether name can be used for a new account of t: the
// engine's own rules (letters, digits and dashes; "default" is reserved; no
// clash with an account t already has).
func (s *Service) CheckNewName(t accounts.Tool, name string) error {
	st, _, err := s.Load()
	if err != nil {
		return err
	}
	return st.CheckNewName(t, name)
}

// StaleRules lists the rules whose folder is gone or whose drive is not
// connected. Devpit never removes one by itself.
func (s *Service) StaleRules() ([]accounts.StaleRule, error) {
	st, _, err := s.Load()
	if err != nil {
		return nil, err
	}
	return accounts.StaleRules(st, nil), nil
}

// RecoveryReport is what crash recovery did when the Service was opened,
// and the error when a change left unfinished could not be finished or put
// back.
func (s *Service) RecoveryReport() ([]accounts.Recovered, error) {
	return s.Recovered, s.RecoverErr
}

// Hold takes the accounts lock for as long as the Accounts page is open, so
// a second Devpit window is read-only for Accounts. It returns an error
// matching accounts.ErrLocked when another window holds it.
func (s *Service) Hold() error { return s.Engine.Hold() }

// Release lets go of the lock Hold took.
func (s *Service) Release() { s.Engine.Release() }

// ApplyGroup applies several previews as one change, so one undo takes them
// all back (Browse folders' "remove this rule" and "point it at the new
// folder" are a change per tool). Every step is reported, and the last
// event is Final: done, or failed with the error. A preview that changes
// nothing is skipped.
func (s *Service) ApplyGroup(ctx context.Context, previews []accounts.Preview) <-chan accounts.Event {
	out := make(chan accounts.Event, 16)
	go func() {
		defer close(out)
		err := s.ApplyAll(ctx, previews, func(ev accounts.Event) {
			ev.Final = false
			out <- ev
		})
		if err != nil {
			out <- accounts.Failed("Saving the change", err)
			return
		}
		ev := accounts.NewEvent("Saved", accounts.StepDone, fmt.Sprintf("%d change(s), undone together", len(previews)))
		ev.Final = true
		out <- ev
	}()
	return out
}

// DiscardSignIn throws away a sign-in that finished but was not saved (the
// person backed out at "name it"), so nothing is left behind: the fresh
// account folder the sign-in made under a placeholder name is removed. A
// login a tool keeps in its own list (gh, Firebase, Wrangler) cannot be
// taken out by Devpit; note then says how to sign it out.
func (s *Service) DiscardSignIn(acct accounts.Account) (note string, err error) {
	if acct.Dir != "" && strings.HasPrefix(acct.Name, placeholderPrefix) &&
		accounts.SameFolder(acct.Dir, s.Deps.Paths.AccountDir(acct.Tool, acct.Name)) {
		fi, lerr := os.Lstat(acct.Dir)
		switch {
		case errors.Is(lerr, os.ErrNotExist):
		case lerr != nil:
			return "", lerr
		case fi.Mode()&os.ModeSymlink != 0 || fi.Mode()&os.ModeIrregular != 0 || !fi.IsDir():
			return "", fmt.Errorf("refusing to remove %s: it is not a plain folder", acct.Dir)
		default:
			if err := os.RemoveAll(filepath.Clean(acct.Dir)); err != nil {
				return "", err
			}
		}
	}
	switch acct.Tool {
	case accounts.ToolGitHub, accounts.ToolFirebase:
		if hint := signOutHint(acct); hint != "" {
			return acct.Tool.DisplayName() + " keeps this login in its own list. To sign it out: " + hint, nil
		}
	case accounts.ToolCloudflare:
		return "Wrangler keeps the profile " + acct.Name + " it made. Remove it with Wrangler's own commands if you do not want it.", nil
	}
	return "", nil
}

// SignInAgainArgs is the tool's own sign-in command for an account Devpit
// already has, or nil for a tool whose sign-in adds a new login instead of
// renewing one (gh, Firebase, Wrangler). It runs through "just this once",
// so the sign-in lands in that account's own folder.
func SignInAgainArgs(t accounts.Tool) []string {
	switch t {
	case accounts.ToolClaude:
		return []string{"claude", "auth", "login"}
	case accounts.ToolVercel:
		return []string{"vercel", "login"}
	case accounts.ToolSupabase:
		return []string{"supabase", "login"}
	}
	return nil
}

// PrepareSignInAgain works out how to run the tool's own sign-in for the
// account name again, in its own folder (the default account: with no
// folder at all). Run it attached to the terminal with RunOnce.
func (s *Service) PrepareSignInAgain(ctx context.Context, t accounts.Tool, name string) (OnceCommand, error) {
	args := SignInAgainArgs(t)
	if args == nil {
		return OnceCommand{}, fmt.Errorf("%s adds a new login rather than renewing one; use \"Sign in with another account\": %w", t.DisplayName(), accounts.ErrNotSupported)
	}
	return s.PrepareOnce(ctx, t, name, args)
}

// --- Git page ---

// gitAdapters returns the Git and GitHub adapters as their own types, for
// the calls only they have.
func (s *Service) gitAdapters() (*adapters.GitAdapter, *adapters.GitHubAdapter, error) {
	a, err := s.Adapter(accounts.ToolGit)
	if err != nil {
		return nil, nil, err
	}
	b, err := s.Adapter(accounts.ToolGitHub)
	if err != nil {
		return nil, nil, err
	}
	g, ok1 := a.(*adapters.GitAdapter)
	h, ok2 := b.(*adapters.GitHubAdapter)
	if !ok1 || !ok2 {
		return nil, nil, errors.New("the Git adapters are not the expected ones")
	}
	return g, h, nil
}

// CommitsAs is the Git page's "Commits as" row: who Git would commit as in
// folder, which file decided it, and what Devpit's rules expect. Offline.
func (s *Service) CommitsAs(ctx context.Context, folder string) (adapters.GitCommitIdentity, error) {
	st, _, err := s.Load()
	if err != nil {
		return adapters.GitCommitIdentity{}, err
	}
	g, _, err := s.gitAdapters()
	if err != nil {
		return adapters.GitCommitIdentity{}, err
	}
	return g.CommitsAs(ctx, st, folder)
}

// PushesAs is the Git page's "Pushes as" row: which GitHub account a push
// from folder uses, or that the SSH key decides.
func (s *Service) PushesAs(ctx context.Context, folder string) (adapters.GitHubPush, error) {
	st, _, err := s.Load()
	if err != nil {
		return adapters.GitHubPush{}, err
	}
	_, h, err := s.gitAdapters()
	if err != nil {
		return adapters.GitHubPush{}, err
	}
	return h.PushesAs(ctx, st, folder)
}

// SuggestEmails offers commit addresses from every GitHub account Devpit
// knows (the default one first): each one's private noreply address, and
// its verified addresses where gh already may read them. It asks gh, so a
// screen calls it on its own goroutine; an account that cannot be asked is
// left out.
func (s *Service) SuggestEmails(ctx context.Context) ([]adapters.EmailSuggestion, error) {
	st, _, err := s.Load()
	if err != nil {
		return nil, err
	}
	_, h, err := s.gitAdapters()
	if err != nil {
		return nil, err
	}
	var out []adapters.EmailSuggestion
	seen := map[string]bool{}
	for _, acct := range adapters.DefaultAndStore(accounts.ToolGitHub, st) {
		list, err := h.SuggestEmails(ctx, acct)
		if err != nil {
			continue
		}
		for _, e := range list {
			if k := strings.ToLower(e.Email); !seen[k] {
				seen[k] = true
				out = append(out, e)
			}
		}
	}
	return out, nil
}

// SuggestedSSHKeyPath is where a new SSH key for the GitHub account name is
// suggested: ~/.ssh/id_ed25519_devpit_<name>.
func (s *Service) SuggestedSSHKeyPath(name string) string {
	return adapters.SuggestedSSHKeyPath(s.Deps, name)
}

// GenerateSSHKey makes a new ed25519 key pair through the safe keygen,
// which never overwrites a key that is already there (it returns a
// *gitssh.ExistsError and ssh-keygen never runs).
func (s *Service) GenerateSSHKey(ctx context.Context, path, comment string) (gitssh.KeygenResult, error) {
	return adapters.GenerateSSHKey(ctx, path, comment)
}

// PlanSSHKey previews giving folder its own SSH key for pushes; keyPath ""
// takes the folder's key away again.
func (s *Service) PlanSSHKey(folder, keyPath string) (adapters.GitSettingsPreview, error) {
	st, _, err := s.Load()
	if err != nil {
		return adapters.GitSettingsPreview{}, err
	}
	_, h, err := s.gitAdapters()
	if err != nil {
		return adapters.GitSettingsPreview{}, err
	}
	return h.PlanSSHKey(st, folder, keyPath)
}

// ApplyGitSettings makes a previewed Git settings change (an SSH key for a
// folder) as one undoable change, step by step.
func (s *Service) ApplyGitSettings(ctx context.Context, p adapters.GitSettingsPreview) <-chan accounts.Event {
	_, h, err := s.gitAdapters()
	if err != nil {
		return failed("Saving the change", err)
	}
	return h.ApplySettings(ctx, s.Engine, p)
}

// --- Claude Code setup ---

// ClaudeRoots says where the default account's setup is and where the
// Claude Code account name keeps its own.
func (s *Service) ClaudeRoots(name string) (claudeshare.Roots, error) {
	st, _, err := s.Load()
	if err != nil {
		return claudeshare.Roots{}, err
	}
	acct, err := st.FindAccount(accounts.ToolClaude, name)
	if err != nil {
		return claudeshare.Roots{}, err
	}
	if acct.IsDefault() || acct.Dir == "" {
		return claudeshare.Roots{}, errors.New("the default account is the one others share from; pick another account to set up")
	}
	r := claudeshare.RootsFor(s.Deps.Home, acct)
	r.Lock = s.Deps.Paths.Lock
	if s.Deps.Now != nil {
		r.Now = s.Deps.Now
	}
	return r, nil
}

// ClaudeInventory is the item list for bringing the default account's setup
// to the Claude Code account name. It reads; it changes nothing.
func (s *Service) ClaudeInventory(name string) (*claudeshare.Inventory, error) {
	r, err := s.ClaudeRoots(name)
	if err != nil {
		return nil, err
	}
	return claudeshare.Scan(r)
}

// PlanClaudeSetup turns the item list as picked into a plan; Plan.Preview
// says it in plain words.
func (s *Service) PlanClaudeSetup(inv *claudeshare.Inventory, sel claudeshare.Selection) (claudeshare.Plan, error) {
	return claudeshare.BuildPlan(inv, sel)
}

// ClaudeOp is one of the later changes to a Claude Code account's setup.
type ClaudeOp string

// The later changes.
const (
	// ClaudeStopSharing turns a shared item into the account's own copy.
	ClaudeStopSharing ClaudeOp = "stop-sharing"
	// ClaudeShareInstead shares items the account has its own of (the new
	// skills made in it, say).
	ClaudeShareInstead ClaudeOp = "share-instead"
	// ClaudeRepair mends a link whose target is gone.
	ClaudeRepair ClaudeOp = "repair"
)

// ClaudeChange names one later change: what to do, to which row, and to
// which entries of it.
type ClaudeChange struct {
	Op    ClaudeOp
	Kind  claudeshare.Kind
	Names []string
}

// PlanClaudeChange previews a later change to the Claude Code account
// name's setup.
func (s *Service) PlanClaudeChange(name string, c ClaudeChange) (claudeshare.Plan, error) {
	r, err := s.ClaudeRoots(name)
	if err != nil {
		return claudeshare.Plan{}, err
	}
	first := ""
	if len(c.Names) > 0 {
		first = c.Names[0]
	}
	switch c.Op {
	case ClaudeStopSharing:
		return claudeshare.PlanStopSharing(r, c.Kind, first)
	case ClaudeShareInstead:
		return claudeshare.PlanShareInstead(r, c.Kind, c.Names, claudeshare.PolicyKeep)
	case ClaudeRepair:
		return claudeshare.PlanRepair(r, c.Kind, first)
	}
	return claudeshare.Plan{}, fmt.Errorf("unknown change %q", c.Op)
}

// ApplyClaudePlan runs a Claude Code setup plan as one undoable change and
// reports each step. A plan that would link where Windows refuses links
// fails with a *claudeshare.LinkError before anything changed; a folder in
// use fails with a *claudeshare.InUseError and everything is put back.
func (s *Service) ApplyClaudePlan(ctx context.Context, p claudeshare.Plan) <-chan accounts.Event {
	return claudeshare.ApplyStream(ctx, s.Engine, p)
}

// --- First open: accounts already on this PC ---

// importIgnoredFile remembers that the person said "Ignore" to the import
// card, and for which findings, so new findings ask again.
const importIgnoredFile = "accounts-import-ignored"

// importFingerprint names what was found, independent of order.
func importFingerprint(f importer.Found) string {
	var parts []string
	if c := f.ClaudeAcc; c != nil {
		for _, a := range c.Accounts {
			parts = append(parts, "acc:"+strings.ToLower(a.Dir))
		}
		for _, l := range c.Links {
			if l.Usable() {
				parts = append(parts, "link:"+strings.ToLower(l.Folder)+"="+strings.ToLower(l.Account))
			}
		}
	}
	for _, d := range f.ConfigDirs {
		parts = append(parts, "dir:"+strings.ToLower(d.Dir))
	}
	for _, a := range f.GitHub {
		parts = append(parts, "gh:"+strings.ToLower(a.Label))
	}
	slices.Sort(parts)
	return strings.Join(parts, "\n")
}

// ImportDismissed reports whether the person already said "Ignore" to
// exactly these findings.
func (s *Service) ImportDismissed(f importer.Found) bool {
	if s.Deps.Paths.ConfigDir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(s.Deps.Paths.ConfigDir, importIgnoredFile))
	return err == nil && strings.TrimSpace(string(data)) == strings.TrimSpace(importFingerprint(f))
}

// DismissImport remembers "Ignore" for these findings. The accounts are
// left exactly where they are; `i` on the Accounts page offers them again.
func (s *Service) DismissImport(f importer.Found) error {
	if s.Deps.Paths.ConfigDir == "" {
		return errors.New("no settings folder to remember this in")
	}
	if err := os.MkdirAll(s.Deps.Paths.ConfigDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Deps.Paths.ConfigDir, importIgnoredFile), []byte(importFingerprint(f)+"\n"), 0o600)
}

// failed is a channel carrying one final failure.
func failed(step string, err error) <-chan accounts.Event {
	ch := make(chan accounts.Event, 1)
	ch <- accounts.Failed(step, err)
	close(ch)
	return ch
}
