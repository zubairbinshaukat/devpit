package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// githubHost is the only host Devpit switches accounts on.
const githubHost = "github.com"

// GitHubAdapter is GitHub: pushes over HTTPS and the gh command line.
//
//   - Accounts are gh's own logins on github.com (several since gh 2.40).
//     Devpit's name for one is stored with the GitHub login (Label); the
//     token stays in gh's own storage.
//   - gh: the shim gives the child GH_TOKEN from `gh auth token --user
//     <login>`, fetched at launch, in memory only (LaunchLive). Devpit
//     never runs `gh auth switch` and never `--show-token`.
//   - Pushes: Devpit's credential helper (`devpit git-credential`, package
//     gitcred) is the only github.com helper while Devpit manages pushes. It
//     resolves the folder itself and passes the default account through to
//     the helper chain that was there before, recorded for undo. It is set
//     up by Apply when the first GitHub rule appears, and removed with the
//     last one.
//   - SSH remotes follow the SSH key, not the helper: PushesAs says which
//     applies, and PlanSSHKey gives a folder its own key.
type GitHubAdapter struct {
	deps Deps
	// HelperExe is the devpit.exe Git's helper line runs; this program
	// when "".
	HelperExe string

	gitProbe gitProber

	mu      sync.Mutex
	probed  bool
	multi   bool
	version string
}

func newGitHub(d Deps) *GitHubAdapter { return &GitHubAdapter{deps: d} }

// NewGitHubAdapter returns the GitHub adapter, for the Git page's own calls
// (PushesAs, SuggestEmails, PlanSSHKey…).
func NewGitHubAdapter(d Deps) *GitHubAdapter { return newGitHub(d) }

func (a *GitHubAdapter) Tool() accounts.Tool { return accounts.ToolGitHub }

func (a *GitHubAdapter) Installed(ctx context.Context) Install {
	return FindInstalled(ctx, a.deps, "gh", "--version")
}

// probe finds out, by asking gh, whether it holds several accounts: the
// multi-account release (2.40) added `gh auth token --user`.
func (a *GitHubAdapter) probe(ctx context.Context) (bool, string, error) {
	a.mu.Lock()
	if a.probed {
		defer a.mu.Unlock()
		return a.multi, a.version, nil
	}
	a.mu.Unlock()
	in := a.Installed(ctx)
	if !in.Found {
		return false, "", fmt.Errorf("GitHub CLI: %w", accounts.ErrNotFoundTool)
	}
	res, err := a.deps.run(ctx, Cmd{Name: "gh", Args: []string{"auth", "token", "--help"}})
	if err != nil {
		return false, in.Version, accounts.ScrubError(err)
	}
	ok := strings.Contains(string(res.Stdout)+string(res.Stderr), "--user")
	a.mu.Lock()
	defer a.mu.Unlock()
	a.probed, a.multi, a.version = true, ok, in.Version
	return ok, in.Version, nil
}

func ghTooOld(version string) error {
	v := version
	if v == "" {
		v = "on this PC"
	}
	return fmt.Errorf("GitHub CLI %s holds one account only. Devpit needs gh 2.40 or later, which keeps several accounts side by side. Update gh (winget upgrade GitHub.cli) and try again: %w", v, accounts.ErrTooOld)
}

func (a *GitHubAdapter) Capabilities(ctx context.Context) Caps {
	ok, ver, err := a.probe(ctx)
	switch {
	case errors.Is(err, accounts.ErrNotFoundTool):
		return Caps{Why: "The GitHub CLI (gh) is not installed. Devpit uses gh's own sign-ins for GitHub accounts."}
	case err != nil:
		return Caps{Why: "Devpit could not ask gh what it supports: " + accounts.Scrub(err.Error())}
	case !ok:
		return Caps{Why: ghTooOld(ver).Error()}
	}
	return Caps{FolderRules: true, Everywhere: true, JustOnce: true, AddAccount: true}
}

// ghAccount is one login in `gh auth status --json hosts`. It has no
// token; scopes is one string ("'gist', 'read:org', 'repo'").
type ghAccount struct {
	State       string `json:"state"`
	Active      bool   `json:"active"`
	Host        string `json:"host"`
	Login       string `json:"login"`
	TokenSource string `json:"tokenSource"`
	Scopes      string `json:"scopes"`
	GitProtocol string `json:"gitProtocol"`
}

// hasScope reports whether a's token has scope, without asking for it.
func (g ghAccount) hasScope(scope string) bool {
	f := strings.FieldsFunc(strings.ToLower(g.Scopes), func(r rune) bool {
		return (r < 'a' || r > 'z') && r != ':' && r != '_'
	})
	return slices.Contains(f, scope)
}

// ghClean is what every gh run that must see gh's own accounts unsets: a
// token variable makes gh use it instead.
func ghClean() []string {
	return []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", EnvGitHubAccount}
}

// ghLogins lists gh's logins on github.com: `gh auth status --json hosts`
// (no token in it), or the text form on a gh without --json.
func ghLogins(ctx context.Context, d Deps) ([]ghAccount, error) {
	res, err := d.run(ctx, Cmd{Name: "gh", Args: []string{"auth", "status", "--json", "hosts"}, Unset: ghClean(), Timeout: 30 * time.Second})
	if err != nil {
		return nil, accounts.ScrubError(err)
	}
	var st struct {
		Hosts map[string][]ghAccount `json:"hosts"`
	}
	if DecodeJSON(res.Stdout, &st) == nil && st.Hosts != nil {
		var out []ghAccount
		for host, list := range st.Hosts {
			if !strings.EqualFold(host, githubHost) {
				continue
			}
			for _, a := range list {
				if a.Login != "" {
					out = append(out, a)
				}
			}
		}
		return out, nil
	}
	if !strings.Contains(strings.ToLower(string(res.Stderr)), "unknown flag") {
		if res.ExitCode != 0 {
			return nil, nil // not signed in at all
		}
		return nil, errors.New("gh answered `gh auth status --json hosts` in a way Devpit does not understand")
	}
	res, err = d.run(ctx, Cmd{Name: "gh", Args: []string{"auth", "status", "--hostname", githubHost}, Unset: ghClean(), Timeout: 30 * time.Second})
	if err != nil {
		return nil, accounts.ScrubError(err)
	}
	return parseGhStatusText(string(res.Stdout) + "\n" + string(res.Stderr)), nil
}

// parseGhStatusText reads the text of `gh auth status`: "Logged in to
// github.com account X (keyring)" since 2.40, "Logged in to github.com as
// X (oauth_token)" before, and "Active account: true".
func parseGhStatusText(text string) []ghAccount {
	var out []ghAccount
	for _, l := range Lines([]byte(text)) {
		low := strings.ToLower(l)
		if i := strings.Index(low, "logged in to "+githubHost+" "); i >= 0 {
			rest := strings.Fields(l[i+len("logged in to "+githubHost+" "):])
			if len(rest) >= 2 && (rest[0] == "account" || rest[0] == "as") && validLogin(rest[1]) {
				out = append(out, ghAccount{Host: githubHost, Login: rest[1], State: "success"})
			}
			continue
		}
		if strings.Contains(low, "active account: true") && len(out) > 0 {
			out[len(out)-1].Active = true
		}
	}
	if len(out) == 1 {
		out[0].Active = true
	}
	return out
}

// validLogin is a GitHub login: letters, digits and single dashes, at
// most 39 characters.
func validLogin(s string) bool {
	if s == "" || len(s) > 39 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}

// nameForLogin suggests a Devpit name for a detected gh login.
func nameForLogin(s *accounts.Store, login string) string {
	base := strings.Trim(strings.ToLower(login), "-")
	if len(base) > accounts.MaxNameLen-3 {
		base = strings.Trim(base[:accounts.MaxNameLen-3], "-")
	}
	if accounts.IsDefault(base) || base == "" {
		base = "gh-" + base
		base = strings.Trim(base, "-")
	}
	name := base
	for i := 2; s.CheckNewName(accounts.ToolGitHub, name) != nil && i < 100; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

// Accounts is default, the store's accounts, then gh's logins on
// github.com that no stored account points at yet, as "detected".
func (a *GitHubAdapter) Accounts(ctx context.Context, s *accounts.Store) ([]accounts.Account, error) {
	if s == nil {
		s = accounts.NewStore()
	}
	out := DefaultAndStore(accounts.ToolGitHub, s)
	logins, err := ghLogins(ctx, a.deps)
	if err != nil {
		return out, err
	}
	known := map[string]bool{}
	for _, acct := range s.AccountsFor(accounts.ToolGitHub) {
		known[strings.ToLower(acct.Label)] = true
	}
	taken := s.Clone()
	for _, l := range logins {
		if known[strings.ToLower(l.Login)] {
			continue
		}
		acct := accounts.Account{Tool: accounts.ToolGitHub, Name: nameForLogin(taken, l.Login), Label: l.Login, ImportedFrom: "detected"}
		if taken.AddAccount(acct) == nil {
			out = append(out, acct)
		}
	}
	return out, nil
}

func (a *GitHubAdapter) Cached(s *accounts.Store, acct accounts.Account) accounts.Identity {
	return CachedIdentity(s, acct)
}

// LiveCheckRisk: `gh api user` reads the account and changes nothing.
func (a *GitHubAdapter) LiveCheckRisk() (bool, string) { return false, "" }

func (a *GitHubAdapter) EnvOverrides() []EnvVar { return githubEnv() }

// githubEnv lists the variables that decide gh's account. None is
// Managed: token variables are never cleared (the house rule pinned by
// TestCheckEnv), only reported. For a named account the launcher sets
// GH_TOKEN itself, which wins over one left in the terminal, and removes
// GITHUB_TOKEN (see LaunchLive).
func githubEnv() []EnvVar {
	return []EnvVar{
		{Name: "GH_TOKEN", Effect: "gh uses this token instead of any signed-in account, wherever Devpit's rules do not pick one."},
		{Name: "GITHUB_TOKEN", Effect: "gh uses this token instead of any signed-in account, wherever Devpit's rules do not pick one."},
		{Name: EnvGitHubAccount, Effect: "Devpit's push helper uses this Devpit account instead of the folder's rule."},
		{Name: "GH_CONFIG_DIR", Effect: "gh reads its accounts from this folder instead of its usual one."},
	}
}

// ghUser is the part of `gh api user` Devpit reads.
type ghUser struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// apiUser runs `gh api user` as acct: a named account through GH_TOKEN
// (fetched from gh, in memory), the default one as gh is.
func (a *GitHubAdapter) apiUser(ctx context.Context, acct accounts.Account) (ghUser, accounts.Identity, error) {
	cmd := Cmd{Name: "gh", Args: []string{"api", "--hostname", githubHost, "user"}, Unset: ghClean(), Timeout: 30 * time.Second}
	if !acct.IsDefault() {
		tok, err := ghToken(ctx, a.deps.Runner, acct.Label)
		if err != nil {
			return ghUser{}, accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateExpired, Login: acct.Label, Note: "Sign in again."}), err
		}
		cmd.Env = []string{"GH_TOKEN=" + tok}
	}
	res, err := a.deps.run(ctx, cmd)
	if err != nil {
		return ghUser{}, accounts.Identity{}, accounts.ScrubError(err)
	}
	var u ghUser
	if res.ExitCode != 0 || DecodeJSON(res.Stdout, &u) != nil || u.Login == "" {
		msg := strings.ToLower(string(res.Stderr) + string(res.Stdout))
		switch {
		case strings.Contains(msg, "401") || strings.Contains(msg, "bad credentials"):
			return ghUser{}, accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateExpired, Login: acct.Label, Note: "The sign-in has expired. Sign in again."}), nil
		case strings.Contains(msg, "not logged") || strings.Contains(msg, "gh auth login"):
			return ghUser{}, accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotSignedIn, Note: "Not signed in."}), nil
		}
		return ghUser{}, accounts.Identity{}, fmt.Errorf("gh could not say who is signed in (exit %d): %s", res.ExitCode, firstOf(FirstLine(res.Stderr), FirstLine(res.Stdout)))
	}
	return u, accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Login: u.Login, Name: u.Name, Email: u.Email}), nil
}

// WhoAmI is the live check: `gh api user` with that account's token.
func (a *GitHubAdapter) WhoAmI(ctx context.Context, acct accounts.Account) (accounts.Identity, error) {
	ok, ver, err := a.probe(ctx)
	if errors.Is(err, accounts.ErrNotFoundTool) {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotInstalled}), err
	}
	if err != nil {
		return accounts.Identity{}, err
	}
	if !ok && !acct.IsDefault() {
		e := ghTooOld(ver)
		return accounts.NewIdentity(accounts.IdentityFields{Note: e.Error()}), e
	}
	if !acct.IsDefault() && !validLogin(acct.Label) {
		return accounts.NewIdentity(accounts.IdentityFields{Note: "No GitHub login is recorded for this account."}),
			fmt.Errorf("the GitHub account %s has no GitHub login recorded; sign in again", acct.Name)
	}
	u, id, err := a.apiUser(ctx, acct)
	if err != nil {
		return id, err
	}
	if !acct.IsDefault() && id.SignedIn() && !strings.EqualFold(u.Login, acct.Label) {
		return id, fmt.Errorf("gh's token for %s belongs to %s", acct.Label, u.Login)
	}
	return id, nil
}

// Login runs gh's own sign-in, which adds an account next to the others,
// then finds out which login is new and names it. gh makes the new login
// its active one; Devpit says so and never switches it back itself.
func (a *GitHubAdapter) Login(ctx context.Context, req LoginRequest) (<-chan accounts.Event, error) {
	s := req.Store
	if s == nil {
		s = accounts.NewStore()
	}
	if err := s.CheckNewName(accounts.ToolGitHub, req.Name); err != nil {
		return nil, err
	}
	return Stream("Signing in", func(emit func(accounts.Event)) error {
		step := func(name string, st accounts.StepState, detail string) { emit(accounts.NewEvent(name, st, detail)) }
		step("Checking the GitHub CLI", accounts.StepRunning, "")
		ok, ver, err := a.probe(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return ghTooOld(ver)
		}
		step("Checking the GitHub CLI", accounts.StepDone, "version "+ver)
		before, err := ghLogins(ctx, a.deps)
		if err != nil {
			return err
		}
		oldActive := ""
		for _, l := range before {
			if l.Active {
				oldActive = l.Login
			}
		}

		step("Opening gh's own sign-in", accounts.StepRunning, "")
		step("Waiting for you to finish signing in in the browser", accounts.StepWaiting, "")
		res, err := a.deps.run(ctx, Cmd{
			Name: "gh", Args: []string{"auth", "login", "--hostname", githubHost, "--git-protocol", "https", "--web"},
			Unset: ghClean(), Timeout: LoginTimeout, Stdin: req.Stdin, Stdout: req.Stdout, Stderr: req.Stderr,
		})
		for _, l := range append(Lines(res.Stdout), Lines(res.Stderr)...) {
			step("gh", accounts.StepInfo, l)
		}
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%w (stopped)", accounts.ErrSignInCancelled)
			}
			return err
		}

		step("Checking who is signed in", accounts.StepRunning, "")
		after, err := ghLogins(ctx, a.deps)
		if err != nil {
			return fmt.Errorf("%w: %w", accounts.ErrSignInCancelled, err)
		}
		login := pickNewLogin(before, after)
		if login == "" {
			return fmt.Errorf("%w: gh shows no new account on github.com", accounts.ErrSignInCancelled)
		}
		for _, acct := range s.AccountsFor(accounts.ToolGitHub) {
			if strings.EqualFold(acct.Label, login) {
				return fmt.Errorf("the GitHub login %s is already in Devpit as %s", login, acct.Name)
			}
		}
		acct := accounts.Account{Tool: accounts.ToolGitHub, Name: req.Name, Label: login, Added: a.deps.now().UTC()}
		if err := accounts.NewStore().AddAccount(acct); err != nil {
			return err
		}
		id := accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Login: login})
		ev := accounts.NewEvent("Checking who is signed in", accounts.StepDone, "Signed in as "+login)
		ev.Identity = &id
		emit(ev)
		if oldActive != "" && !strings.EqualFold(oldActive, login) {
			step("gh switched its active account", accounts.StepWarning, fmt.Sprintf(
				"gh now uses %s when Devpit does not pick an account, so \"default\" for gh is %s. To go back, run: gh auth switch --user %s",
				login, login, oldActive))
		}
		final := accounts.NewEvent("Signed in", accounts.StepDone, "Signed in as "+login)
		final.Final, final.Account, final.Identity = true, &acct, &id
		emit(final)
		return nil
	}), nil
}

// pickNewLogin is the login gh's sign-in added: the one in after that was
// not in before, or, when someone signed in again to a login gh already
// had, the active one.
func pickNewLogin(before, after []ghAccount) string {
	had := map[string]bool{}
	for _, b := range before {
		had[strings.ToLower(b.Login)] = true
	}
	var fresh []string
	active := ""
	for _, x := range after {
		if !had[strings.ToLower(x.Login)] {
			fresh = append(fresh, x.Login)
		}
		if x.Active {
			active = x.Login
		}
	}
	if len(fresh) == 1 {
		return fresh[0]
	}
	if len(fresh) > 1 && slices.Contains(fresh, active) {
		return active
	}
	if len(fresh) == 0 {
		return active
	}
	return ""
}

func (a *GitHubAdapter) previewInput(in accounts.PreviewInput) accounts.PreviewInput {
	in.Display = func(acct accounts.Account) string {
		if acct.IsDefault() {
			if who := in.Defaults[accounts.ToolGitHub]; who != "" {
				return accounts.DefaultName + " (" + who + ")"
			}
			return accounts.DefaultName + " (gh's active account)"
		}
		return acct.Display()
	}
	in.Uses = func(_, account string) string { return "gh and pushes to GitHub over HTTPS will use " + account }
	return in
}

// Plan previews a GitHub change: the store's sentences plus Devpit's Git
// files (the github.com helper is added with the first GitHub rule and
// removed with the last). Without Git installed, only gh follows the rule,
// and the preview says so.
func (a *GitHubAdapter) Plan(in accounts.PreviewInput) (accounts.Preview, error) {
	if in.Change.Tool != accounts.ToolGitHub {
		return accounts.Preview{}, fmt.Errorf("the GitHub adapter cannot plan a %s change", in.Change.Tool)
	}
	s := in.Store
	if s == nil {
		s = accounts.NewStore()
	}
	if !in.Change.Remove {
		acct, err := s.FindAccount(accounts.ToolGitHub, in.Change.Account)
		if err != nil {
			return accounts.Preview{}, err
		}
		if !acct.IsDefault() && !validLogin(acct.Label) {
			return accounts.Preview{}, fmt.Errorf("the GitHub account %s has no GitHub login recorded; sign in again", acct.Name)
		}
	}
	pin := a.previewInput(in)
	if in.Change.Scope.Kind == accounts.ScopeOnce {
		return accounts.BuildPreview(pin)
	}
	p, err := planWithGit(a.deps, &a.gitProbe, a.HelperExe, in, pin)
	if errors.Is(err, accounts.ErrNotFoundTool) || errors.Is(err, accounts.ErrTooOld) {
		p, err = accounts.BuildPreview(pin)
		if err != nil {
			return p, err
		}
		p.Warnings = append(p.Warnings, "Git is not installed (or too old for folder rules), so only gh follows this rule, not git push.")
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if after, aerr := in.Change.ApplyTo(s); aerr == nil && githubHelperNeeded(after) {
		if x, xerr := readGitExtras(GitDir(a.deps)); xerr == nil && x.Helper == nil {
			p.Warnings = append(p.Warnings,
				"Devpit becomes Git's only sign-in helper for github.com, so it can pick the account per folder. "+
					"Where the default account applies, it hands over to the helper you use today; removing the last GitHub rule puts that helper back on its own.")
		}
	}
	if in.Change.Scope.Kind == accounts.ScopeFolder && !in.Change.Remove {
		p.Warnings = append(p.Warnings, "A repo that pushes over SSH follows its SSH key instead. The Git page shows which applies, and can give this folder its own key.")
	}
	return p, nil
}

// Apply writes Devpit's Git files (the github.com helper) for the store
// as it will be after the change. gh itself needs nothing written: the
// shim reads the rule at each launch.
func (a *GitHubAdapter) Apply(ctx context.Context, txn *accounts.Txn, p accounts.Preview, emit func(accounts.Event)) error {
	err := applyWithGit(ctx, a.deps, &a.gitProbe, a.HelperExe, txn, p, emit)
	if errors.Is(err, accounts.ErrNotFoundTool) || errors.Is(err, accounts.ErrTooOld) {
		if len(p.Edits) > 1 {
			return fmt.Errorf("Git is no longer usable, so the preview is out of date: %w", accounts.ErrStalePreview) //nolint:revive,staticcheck // a product name
		}
		emitStep(emit, "Nothing to change in Git", accounts.StepDone, "Git is not installed, so only gh follows the rule")
		return nil
	}
	return err
}

// PlanSSHKey previews giving folder its own SSH key for pushes (keyPath
// "" takes it away again). See GenerateSSHKey for making a key.
func (a *GitHubAdapter) PlanSSHKey(s *accounts.Store, folder, keyPath string) (GitSettingsPreview, error) {
	return planSettings(a.deps, &a.gitProbe, a.HelperExe, s, GitSettingsChange{Folder: folder, SSHKey: &keyPath})
}

// ApplySettings makes a previewed SSH key (or other Git settings) change.
func (a *GitHubAdapter) ApplySettings(ctx context.Context, eng *accounts.Engine, p GitSettingsPreview) <-chan accounts.Event {
	return applySettings(ctx, a.deps, &a.gitProbe, a.HelperExe, eng, p)
}

// HelperStatus says whether Devpit's github.com helper is set up and what
// it hands the default account to.
func (a *GitHubAdapter) HelperStatus() (GitHelper, bool, error) {
	return LoadGitHelper(GitDir(a.deps))
}

// Launch is the static answer. A named account needs a token fetched from
// gh when gh starts, which a static answer cannot give: the shim calls
// LaunchLive instead.
func (a *GitHubAdapter) Launch(acct accounts.Account) (Launch, error) { return githubLaunch(acct) }

// githubLaunch is the shim's static view of [GitHubAdapter.Launch].
func githubLaunch(acct accounts.Account) (Launch, error) {
	if acct.IsDefault() {
		return defaultLaunch(githubEnv()), nil
	}
	// It also matches ErrNotSupported: the static answer cannot do this.
	return Launch{}, fmt.Errorf("the GitHub account %s needs its token from gh when gh starts (%w): %w",
		acct.Name, ErrNeedsLiveLaunch, accounts.ErrNotSupported)
}

// ghUserEmails is one entry of `gh api user/emails`.
type ghUserEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

func decodeEmails(out []byte) ([]ghUserEmail, error) {
	var es []ghUserEmail
	i := strings.IndexByte(string(out), '[')
	j := strings.LastIndexByte(string(out), ']')
	if i < 0 || j < i {
		return nil, errors.New("no list in the output")
	}
	return es, json.Unmarshal(out[i:j+1], &es)
}
