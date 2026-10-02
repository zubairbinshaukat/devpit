package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// GitAdapter is Git's commit identity: who a commit is made as. An account
// is an identity (user.name + user.email, optionally signing), named like
// any other account; "default" is whatever the person's global config
// already says.
//
//   - A folder rule is an `[includeIf "gitdir/i:C:/Work/"]` block in
//     Devpit's own rules.gitconfig, which the person's global config
//     includes with one block at its very end. Blocks go shallow → deep,
//     because the last match wins in Git. When the installed Git proves it
//     understands `worktree/i:` (a probe, never a version number), matching
//     worktree blocks follow, so linked worktrees and submodules follow
//     the folder they are in.
//   - "Everywhere" is an [include] of the identity's file at the top of
//     rules.gitconfig: after the person's own [user] lines, before every
//     folder rule. Their own user.email is never edited, and undo is exact.
//   - No shim and no "just this once": Git reads its identity from its
//     config files. Capabilities says so.
//   - Who-am-I is offline and safe: `git config --show-origin --show-scope`
//     in the folder, which also says which file won (CommitsAs).
type GitAdapter struct {
	deps  Deps
	probe gitProber
}

func newGit(d Deps) *GitAdapter { return &GitAdapter{deps: d} }

// NewGitAdapter returns the Git adapter, for the Git page's own calls
// (CommitsAs, PlanSettings…). For the shared Adapter calls, For works too.
func NewGitAdapter(d Deps) *GitAdapter { return newGit(d) }

func (a *GitAdapter) Tool() accounts.Tool { return accounts.ToolGit }

func (a *GitAdapter) Installed(ctx context.Context) Install {
	return FindInstalled(ctx, a.deps, "git", "--version")
}

// gitNoOnce is why Git has no "just this once".
const gitNoOnce = "Git takes its commit identity from its config files, not from a sign-in, so there is no \"just this once\". " +
	"For one commit, run git -c user.email=you@example.com commit."

func (a *GitAdapter) Capabilities(ctx context.Context) Caps {
	c, err := a.probe.get(ctx, a.deps)
	switch {
	case errors.Is(err, accounts.ErrNotFoundTool):
		return Caps{Why: "Git is not installed."}
	case err != nil:
		return Caps{Why: "Devpit could not check what this Git supports: " + accounts.Scrub(err.Error())}
	case !c.includeIf:
		return Caps{Why: gitTooOld(c.install.Version).Error()}
	}
	return Caps{FolderRules: true, Everywhere: true, AddAccount: true, Why: gitNoOnce}
}

func (a *GitAdapter) Accounts(_ context.Context, s *accounts.Store) ([]accounts.Account, error) {
	return DefaultAndStore(accounts.ToolGit, s), nil
}

// Cached is the identity as recorded: Label is the name Git commits under.
func (a *GitAdapter) Cached(s *accounts.Store, acct accounts.Account) accounts.Identity {
	if acct.IsDefault() {
		return CachedIdentity(s, acct)
	}
	if s != nil {
		if stored, ok := s.Account(accounts.ToolGit, acct.Name); ok {
			acct = stored
		}
	}
	if acct.Email == "" {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateUnknown, Note: "No email recorded."})
	}
	return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateRecorded, Email: acct.Email, Name: acct.Label})
}

// LiveCheckRisk: reading Git's config harms nothing.
func (a *GitAdapter) LiveCheckRisk() (bool, string) { return false, "" }

func (a *GitAdapter) EnvOverrides() []EnvVar { return gitEnv() }

// gitEnv lists the variables that override Git's identity. None is
// Managed: Git has no shim, so they are only reported.
func gitEnv() []EnvVar {
	return []EnvVar{
		{Name: "GIT_AUTHOR_NAME", Effect: "Git commits under this name instead of the folder's identity."},
		{Name: "GIT_AUTHOR_EMAIL", Effect: "Git commits under this address instead of the folder's identity."},
		{Name: "GIT_COMMITTER_NAME", Effect: "Git records this committer name instead of the folder's identity."},
		{Name: "GIT_COMMITTER_EMAIL", Effect: "Git records this committer address instead of the folder's identity."},
		{Name: "GIT_CONFIG_GLOBAL", Effect: "Git reads this file instead of your ~/.gitconfig, so Devpit's include line is not read."},
		{Name: "GIT_CONFIG_NOSYSTEM", Effect: "Git skips its system config. Devpit's rules still apply."},
		{Name: "GIT_SSH_COMMAND", Effect: "Git pushes over SSH with this command instead of the folder's SSH key."},
	}
}

// WhoAmI for Git reads config, which is offline and harmless. The default
// account is the person's own global identity (Devpit's files left out); a
// named one is what Devpit writes for it.
func (a *GitAdapter) WhoAmI(ctx context.Context, acct accounts.Account) (accounts.Identity, error) {
	if !acct.IsDefault() {
		if acct.Email == "" {
			return accounts.NewIdentity(accounts.IdentityFields{Note: "This identity has no email."}),
				fmt.Errorf("the Git identity %s has no email", acct.Name)
		}
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Email: acct.Email, Name: acct.Label}), nil
	}
	g := gitSync{d: a.deps, dir: GitDir(a.deps), global: gitGlobalPath(a.deps)}
	name, email, err := g.ownIdentity(ctx)
	if errors.Is(err, accounts.ErrNotFoundTool) {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotInstalled}), err
	}
	if err != nil {
		return accounts.Identity{}, accounts.ScrubError(err)
	}
	if email == "" {
		return accounts.NewIdentity(accounts.IdentityFields{
			State: accounts.StateNotSignedIn, Name: name,
			Note: "Your global Git config sets no user.email.",
		}), nil
	}
	return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Email: email, Name: name}), nil
}

// NewGitIdentity checks a typed identity and returns the Account to save
// with Engine.AddAccount: name is Devpit's name for it, userName and email
// are what Git commits under. Values that could change the meaning of a
// Git config file (a line break, a control character) are refused here;
// quotes, '#', ';' and backslashes are fine, because Devpit always quotes.
func NewGitIdentity(s *accounts.Store, name, userName, email string, now time.Time) (accounts.Account, error) {
	if s == nil {
		s = accounts.NewStore()
	}
	if err := s.CheckNewName(accounts.ToolGit, name); err != nil {
		return accounts.Account{}, err
	}
	userName = strings.TrimSpace(userName)
	email = strings.TrimSpace(email)
	if email == "" {
		return accounts.Account{}, errors.New("a Git identity needs an email")
	}
	for _, v := range []string{userName, email} {
		if _, err := gitQuote(v); err != nil {
			return accounts.Account{}, err
		}
	}
	acct := accounts.Account{Tool: accounts.ToolGit, Name: name, Email: email, Label: userName, Added: now.UTC()}
	if err := accounts.NewStore().AddAccount(acct); err != nil {
		return accounts.Account{}, err
	}
	return acct, nil
}

// Login for Git is typed, not a sign-in: req.Email is the address, and
// req.DisplayName the name Git commits under ("" takes the one the person's
// global config already has).
func (a *GitAdapter) Login(ctx context.Context, req LoginRequest) (<-chan accounts.Event, error) {
	s := req.Store
	if s == nil {
		s = accounts.NewStore()
	}
	if err := s.CheckNewName(accounts.ToolGit, req.Name); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Email) == "" {
		return nil, errors.New("a Git identity needs an email")
	}
	return Stream("Adding the Git identity", func(emit func(accounts.Event)) error {
		userName := strings.TrimSpace(req.DisplayName)
		if userName == "" {
			g := gitSync{d: a.deps, dir: GitDir(a.deps), global: gitGlobalPath(a.deps)}
			own, _, err := g.ownIdentity(ctx)
			if err != nil {
				return err
			}
			userName = own
		}
		acct, err := NewGitIdentity(s, req.Name, userName, req.Email, a.deps.now())
		if err != nil {
			return err
		}
		id := accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Email: acct.Email, Name: acct.Label})
		final := accounts.NewEvent("Adding the Git identity", accounts.StepDone, gitDisplay(acct))
		final.Final, final.Account, final.Identity = true, &acct, &id
		emit(final)
		return nil
	}), nil
}

// gitDisplay names an identity the way Git shows an author:
// "Zubair <zubair@work.com>".
func gitDisplay(a accounts.Account) string {
	switch {
	case a.Label != "" && a.Email != "":
		return a.Label + " <" + a.Email + ">"
	case a.Email != "":
		return "<" + a.Email + ">"
	}
	return a.Name
}

func (a *GitAdapter) previewInput(in accounts.PreviewInput) accounts.PreviewInput {
	defaults := in.Defaults
	in.Display = func(acct accounts.Account) string {
		if acct.IsDefault() {
			if who := defaults[accounts.ToolGit]; who != "" {
				return accounts.DefaultName + " (" + who + ")"
			}
			if in.Store != nil && in.Store.DefaultEmail[accounts.ToolGit] != "" {
				return accounts.DefaultName + " (" + in.Store.DefaultEmail[accounts.ToolGit] + ")"
			}
			return accounts.DefaultName + " (your own identity)"
		}
		return gitDisplay(acct)
	}
	in.Uses = func(_, account string) string { return "Git will commit as " + account }
	return in
}

// Plan shows the change in plain words and every line Devpit's Git files
// will hold afterwards, plus the one block it adds to (or removes from) the
// global config. A "just this once" change is refused with the reason.
func (a *GitAdapter) Plan(in accounts.PreviewInput) (accounts.Preview, error) {
	if in.Change.Tool != accounts.ToolGit {
		return accounts.Preview{}, fmt.Errorf("the Git adapter cannot plan a %s change", in.Change.Tool)
	}
	if in.Change.Scope.Kind == accounts.ScopeOnce {
		return accounts.Preview{}, NotSupported(accounts.ToolGit, gitNoOnce)
	}
	return planWithGit(a.deps, &a.probe, "", in, a.previewInput(in))
}

// planWithGit is the shared Plan of the Git and GitHub adapters: the
// store's preview plus the Git files as they will be after the change.
func planWithGit(d Deps, p *gitProber, exe string, raw, in accounts.PreviewInput) (accounts.Preview, error) {
	s := raw.Store
	if s == nil {
		s = accounts.NewStore()
	}
	after, err := raw.Change.ApplyTo(s)
	if err != nil {
		return accounts.Preview{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	plan, err := planGitFiles(ctx, d, p, exe, after, nil)
	if err != nil {
		return accounts.Preview{}, err
	}
	in.Edits = append(in.Edits, plan.edits(d.Home)...)
	pv, err := accounts.BuildPreview(in)
	if err != nil {
		return accounts.Preview{}, err
	}
	pv.Warnings = append(pv.Warnings, plan.warnings...)
	return pv, nil
}

// planGitFiles computes the Git files for store s. mutate, when set,
// changes devpit.json's settings first.
func planGitFiles(ctx context.Context, d Deps, p *gitProber, exe string, s *accounts.Store, mutate func(*gitExtras) error) (gitPlan, error) {
	g, err := newGitSync(ctx, d, p, exe)
	if err != nil {
		return gitPlan{}, err
	}
	x, err := readGitExtras(g.dir)
	if err != nil {
		return gitPlan{}, err
	}
	if mutate != nil {
		x = x.clone()
		if err = mutate(&x); err != nil {
			return gitPlan{}, err
		}
	}
	return g.compute(ctx, s, x)
}

// Apply writes Devpit's Git files for the store as it will be after the
// change. It first works the files out again and stops with
// ErrStalePreview if they are not exactly what the preview showed.
func (a *GitAdapter) Apply(ctx context.Context, txn *accounts.Txn, p accounts.Preview, emit func(accounts.Event)) error {
	return applyWithGit(ctx, a.deps, &a.probe, "", txn, p, emit)
}

func applyWithGit(ctx context.Context, d Deps, pr *gitProber, exe string, txn *accounts.Txn, p accounts.Preview, emit func(accounts.Event)) error {
	g, err := newGitSync(ctx, d, pr, exe)
	if err != nil {
		return err
	}
	x, err := readGitExtras(g.dir)
	if err != nil {
		return err
	}
	plan, err := g.compute(ctx, txn.After(), x)
	if err != nil {
		return err
	}
	shown := p.Edits
	if len(shown) > 0 {
		shown = shown[1:] // accounts.toml comes first
	}
	if !sameEdits(plan.edits(d.Home), shown) {
		return fmt.Errorf("Devpit's Git files would no longer be what the preview showed: %w", accounts.ErrStalePreview) //nolint:revive,staticcheck // a product name
	}
	return g.apply(ctx, txn, plan, emit)
}

// Launch: Git has no shim. The default account changes nothing; a named
// one cannot be applied to one process.
func (a *GitAdapter) Launch(acct accounts.Account) (Launch, error) { return gitLaunch(acct) }

// gitLaunch is the shim's view of [GitAdapter.Launch]: no adapter, no
// command run.
func gitLaunch(acct accounts.Account) (Launch, error) {
	if acct.IsDefault() {
		return defaultLaunch(gitEnv()), nil
	}
	return Launch{}, NotSupported(accounts.ToolGit, gitNoOnce)
}
