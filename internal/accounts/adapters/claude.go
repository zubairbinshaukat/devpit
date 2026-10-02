package adapters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// claudeConfigDir is the variable Claude Code documents for running several
// accounts: each account is a folder, and the variable picks one for a
// process.
const claudeConfigDir = "CLAUDE_CONFIG_DIR"

// claudeAdapter is Claude Code, fully supported:
//
//   - applied with CLAUDE_CONFIG_DIR=<account folder> for the one process;
//   - who is signed in comes in two tiers. Cached is what Devpit recorded at
//     sign-in, import or the last Verify (email, org) and never runs
//     claude: the Accounts page uses only that. WhoAmI, the live check, is
//     `claude auth status --json` with the folder set (exit 1 and
//     loggedIn:false is "not signed in"; no token is in that output), run
//     only when the person asks. The reason is Claude Code issue #95822
//     (anthropics/claude-code, open on 2026-10-02): a short-lived command
//     starts an OAuth refresh at start-up when the stored access token is
//     expired or about to be, and `auth status` exits before the new token
//     pair is saved, leaving a spent refresh token ("Login expired"). An
//     idle account always has an expired access token, so polling it is
//     exactly the trigger;
//   - CLAUDE_CONFIG_DIR is Devpit's to set while Devpit manages Claude
//     Code: a named account sets it, the default account clears it, so a
//     variable left in the terminal by something else (claude-acc sets it
//     for the whole PowerShell session) never picks the account;
//   - a new account signs in with `claude auth login` into a fresh folder
//     under %USERPROFILE%\.devpit\accounts\claude\<name>, kept only if the
//     sign-in finished;
//   - Devpit never opens .credentials.json; it only checks whether it is
//     there, to tell "expired" from "never signed in".
type claudeAdapter struct {
	deps Deps

	mu      sync.Mutex
	probed  bool
	hasAuth bool
	version string
}

func newClaude(d Deps) Adapter { return &claudeAdapter{deps: d} }

func (a *claudeAdapter) Tool() accounts.Tool { return accounts.ToolClaude }

func (a *claudeAdapter) Installed(ctx context.Context) Install {
	return FindInstalled(ctx, a.deps, "claude")
}

// probe finds out, once per adapter, whether this Claude Code has `claude
// auth status`. It asks `claude auth --help` rather than running `claude
// auth status` blind: on a version without the auth command, unknown words
// could be taken as a prompt and start a session. Every version answers
// --help and exits.
func (a *claudeAdapter) probe(ctx context.Context) (bool, string, error) {
	a.mu.Lock()
	if a.probed {
		defer a.mu.Unlock()
		return a.hasAuth, a.version, nil
	}
	a.mu.Unlock()

	in := a.Installed(ctx)
	if !in.Found {
		return false, "", fmt.Errorf("Claude Code: %w", accounts.ErrNotFoundTool) //nolint:revive,staticcheck // a product name
	}
	res, err := a.deps.run(ctx, Cmd{Name: "claude", Args: []string{"auth", "--help"}})
	if err != nil {
		return false, in.Version, err
	}
	text := strings.ToLower(string(res.Stdout) + "\n" + string(res.Stderr))
	ok := res.ExitCode == 0 && strings.Contains(text, "status") && strings.Contains(text, "logout")

	a.mu.Lock()
	defer a.mu.Unlock()
	a.probed, a.hasAuth, a.version = true, ok, in.Version
	return ok, in.Version, nil
}

func tooOld(version string) error {
	v := version
	if v == "" {
		v = "on this PC"
	}
	return fmt.Errorf("Claude Code %s has no `claude auth status`, which Devpit needs to tell accounts apart. Update Claude Code (run `claude update`) and try again: %w", v, accounts.ErrTooOld) //nolint:revive,staticcheck // a product name
}

func (a *claudeAdapter) Capabilities(ctx context.Context) Caps {
	ok, ver, err := a.probe(ctx)
	switch {
	case errors.Is(err, accounts.ErrNotFoundTool):
		return Caps{Why: "Claude Code is not installed."}
	case err != nil:
		return Caps{Why: "Devpit could not ask Claude Code what it supports: " + accounts.Scrub(err.Error())}
	case !ok:
		return Caps{Why: tooOld(ver).Error()}
	}
	return Caps{FolderRules: true, Everywhere: true, JustOnce: true, AddAccount: true}
}

func (a *claudeAdapter) Accounts(_ context.Context, s *accounts.Store) ([]accounts.Account, error) {
	return DefaultAndStore(accounts.ToolClaude, s), nil
}

// Cached never runs claude; see the type's comment.
func (a *claudeAdapter) Cached(s *accounts.Store, acct accounts.Account) accounts.Identity {
	return CachedIdentity(s, acct)
}

// LiveCheckRisk is true for Claude Code because of issue #95822.
func (a *claudeAdapter) LiveCheckRisk() (bool, string) {
	return true, "Checking asks Claude Code itself. On some versions this can sign an idle account out " +
		"(Claude Code issue #95822), so Devpit only checks when you ask."
}

func (a *claudeAdapter) EnvOverrides() []EnvVar { return claudeEnv() }

// claudeEnv lists what decides Claude Code's account. CLAUDE_CONFIG_DIR is
// Managed: set for a named account, cleared for the default one. The
// token variables are only reported.
func claudeEnv() []EnvVar {
	return []EnvVar{
		{Name: claudeConfigDir, Managed: true, Effect: "Claude Code started through Devpit uses Devpit's rule instead; started any other way, it uses that folder's sign-in."},
		{Name: "ANTHROPIC_API_KEY", Effect: "Claude Code may use this API key instead of the account's sign-in."},
		{Name: "ANTHROPIC_AUTH_TOKEN", Effect: "Claude Code uses this token instead of the account's sign-in."},
		{Name: "CLAUDE_CODE_OAUTH_TOKEN", Effect: "Claude Code uses this token instead of the account's sign-in."},
	}
}

// claudeStatus is the part of `claude auth status --json` Devpit reads.
// orgId and the folder paths are ignored; there is no token in it.
type claudeStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	AuthMethod       string `json:"authMethod"`
	Email            string `json:"email"`
	OrgName          string `json:"orgName"`
	SubscriptionType string `json:"subscriptionType"`
}

// WhoAmI is the live check. Because of Claude Code issue #95822 it may
// spend an idle account's refresh token, so it runs only on an explicit
// request (Verify, Sign in again, right after a sign-in); see
// LiveCheckRisk.
func (a *claudeAdapter) WhoAmI(ctx context.Context, acct accounts.Account) (accounts.Identity, error) {
	cmd := Cmd{Name: "claude", Args: []string{"auth", "status", "--json"}, Timeout: 20 * time.Second}
	credDir := filepath.Join(a.deps.Home, ".claude")
	if acct.IsDefault() {
		// Devpit itself may run inside a shimmed shell; the default
		// account is the one with no folder variable at all.
		cmd.Unset = []string{claudeConfigDir}
	} else {
		if acct.Dir == "" {
			return accounts.NewIdentity(accounts.IdentityFields{Note: "This account has no folder."}),
				fmt.Errorf("the Claude Code account %s has no folder", acct.Name)
		}
		// Running claude against a folder that is gone would quietly make
		// a new, empty one; say it is missing instead.
		if fi, err := os.Stat(acct.Dir); err != nil || !fi.IsDir() {
			return accounts.NewIdentity(accounts.IdentityFields{Note: "Its folder is missing: " + acct.Dir}),
				fmt.Errorf("the folder for the Claude Code account %s is missing: %s", acct.Name, acct.Dir)
		}
		cmd.Env = []string{claudeConfigDir + "=" + acct.Dir}
		credDir = acct.Dir
	}

	ok, ver, err := a.probe(ctx)
	if errors.Is(err, accounts.ErrNotFoundTool) {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotInstalled}), err
	}
	if err != nil {
		return accounts.Identity{}, accounts.ScrubError(err)
	}
	if !ok {
		e := tooOld(ver)
		return accounts.NewIdentity(accounts.IdentityFields{Note: e.Error()}), e
	}

	res, err := a.deps.run(ctx, cmd)
	if err != nil {
		return accounts.Identity{}, accounts.ScrubError(err)
	}
	var st claudeStatus
	if err := DecodeJSON(res.Stdout, &st); err != nil {
		return accounts.Identity{}, fmt.Errorf("Claude Code answered `claude auth status` in a way Devpit does not understand (exit %d): %s", //nolint:revive,staticcheck // a product name
			res.ExitCode, firstOf(FirstLine(res.Stdout), FirstLine(res.Stderr)))
	}
	if st.LoggedIn {
		return accounts.NewIdentity(accounts.IdentityFields{
			State: accounts.StateSignedIn, Email: st.Email, Org: st.OrgName,
			Plan: st.SubscriptionType, Method: st.AuthMethod,
		}), nil
	}
	// Signed out. A credentials file that is still there means a login
	// that no longer works: expired or revoked. Only its presence is
	// checked; it is never opened.
	if _, err := os.Stat(filepath.Join(credDir, ".credentials.json")); err == nil {
		return accounts.NewIdentity(accounts.IdentityFields{
			State: accounts.StateExpired, Note: "The sign-in has expired. Sign in again.",
		}), nil
	}
	return accounts.NewIdentity(accounts.IdentityFields{
		State: accounts.StateNotSignedIn, Note: "Not signed in.",
	}), nil
}

func firstOf(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return "(no output)"
}

func (a *claudeAdapter) Login(ctx context.Context, req LoginRequest) (<-chan accounts.Event, error) {
	s := req.Store
	if s == nil {
		s = accounts.NewStore()
	}
	if err := s.CheckNewName(accounts.ToolClaude, req.Name); err != nil {
		return nil, err
	}
	root := filepath.Join(a.deps.Paths.AccountsDir, string(accounts.ToolClaude))
	dir := a.deps.Paths.AccountDir(accounts.ToolClaude, req.Name)
	if a.deps.Paths.AccountsDir == "" {
		return nil, errors.New("no folder for new accounts is set")
	}
	if _, err := os.Lstat(dir); err == nil {
		return nil, fmt.Errorf("a folder for %s already exists at %s. Pick another name, or add that folder as an existing account", req.Name, dir)
	}

	return Stream("Signing in", func(emit func(accounts.Event)) error {
		step := func(name string, st accounts.StepState, detail string) {
			emit(accounts.NewEvent(name, st, detail))
		}

		step("Checking Claude Code", accounts.StepRunning, "")
		ok, ver, err := a.probe(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return tooOld(ver)
		}
		step("Checking Claude Code", accounts.StepDone, "version "+ver)

		step("Creating the account folder", accounts.StepRunning, dir)
		if err = os.MkdirAll(root, 0o700); err != nil {
			return err
		}
		if err = os.Mkdir(dir, 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		keep := false
		defer func() {
			if !keep {
				if rerr := removeFresh(dir, root); rerr != nil {
					a.deps.logf("could not remove the unfinished account folder %s: %v", dir, rerr)
				}
			}
		}()
		step("Creating the account folder", accounts.StepDone, dir)

		args := []string{"auth", "login"}
		if req.Console {
			args = append(args, "--console")
		}
		if req.Email != "" {
			args = append(args, "--email", req.Email)
		}
		step("Opening Claude Code's own sign-in", accounts.StepRunning, "")
		step("Waiting for you to finish signing in in the browser", accounts.StepWaiting, "")
		res, err := a.deps.run(ctx, Cmd{
			Name: "claude", Args: args, Env: []string{claudeConfigDir + "=" + dir},
			Timeout: LoginTimeout, Stdin: req.Stdin, Stdout: req.Stdout, Stderr: req.Stderr,
		})
		for _, l := range append(Lines(res.Stdout), Lines(res.Stderr)...) {
			step("Claude Code", accounts.StepInfo, l)
		}
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%w (stopped)", accounts.ErrSignInCancelled)
			}
			return err
		}

		step("Checking who is signed in", accounts.StepRunning, "")
		probeAcct := accounts.Account{Tool: accounts.ToolClaude, Name: req.Name, Dir: dir}
		id, err := a.WhoAmI(ctx, probeAcct)
		if err != nil {
			return fmt.Errorf("%w: %w", accounts.ErrSignInCancelled, err)
		}
		if !id.SignedIn() {
			return fmt.Errorf("%w: Claude Code says this folder is %s", accounts.ErrSignInCancelled, id.State())
		}
		ev := accounts.NewEvent("Checking who is signed in", accounts.StepDone, "Signed in as "+id.Who())
		ev.Identity = &id
		emit(ev)

		acct := accounts.Account{
			Tool: accounts.ToolClaude, Name: req.Name, Email: id.Email(), Org: id.Fields().Org,
			Dir: dir, Added: a.deps.now().UTC(),
		}
		if accounts.NewStore().AddAccount(acct) != nil {
			acct.Org = "" // an org name Devpit cannot store is simply not recorded
		}
		if err := accounts.NewStore().AddAccount(acct); err != nil {
			return err
		}
		defEmail := req.DefaultEmail
		if defEmail == "" {
			defEmail = s.DefaultEmail[accounts.ToolClaude]
		}
		if same := sameEmail(s, defEmail, acct.Email); len(same) > 0 {
			step("Same login twice", accounts.StepWarning, fmt.Sprintf(
				"%s is signed in as %s, like %s. Two folders signed in to the same account can sign each other out when the login refreshes.",
				acct.Name, acct.Email, strings.Join(same, " and ")))
		}

		keep = true
		final := accounts.NewEvent("Signed in", accounts.StepDone, "Signed in as "+id.Who())
		final.Final = true
		final.Account = &acct
		final.Identity = &id
		emit(final)
		return nil
	}), nil
}

// sameEmail lists the Claude accounts (default included) already signed in
// as email.
func sameEmail(s *accounts.Store, defaultEmail, email string) []string {
	if email == "" {
		return nil
	}
	var out []string
	if strings.EqualFold(defaultEmail, email) {
		out = append(out, accounts.DefaultName)
	}
	for _, a := range s.AccountsFor(accounts.ToolClaude) {
		if strings.EqualFold(a.Email, email) {
			out = append(out, a.Name)
		}
	}
	return out
}

func (a *claudeAdapter) Plan(in accounts.PreviewInput) (accounts.Preview, error) {
	if in.Change.Tool != accounts.ToolClaude {
		return accounts.Preview{}, fmt.Errorf("the Claude Code adapter cannot plan a %s change", in.Change.Tool)
	}
	s := in.Store
	if s == nil {
		s = accounts.NewStore()
	}
	var warn []string
	if !in.Change.Remove {
		acct, err := s.FindAccount(accounts.ToolClaude, in.Change.Account)
		if err != nil {
			return accounts.Preview{}, err
		}
		if !acct.IsDefault() {
			if acct.Dir == "" {
				return accounts.Preview{}, fmt.Errorf("the Claude Code account %s has no folder; sign in again", acct.Name)
			}
			if fi, err := os.Stat(acct.Dir); err != nil || !fi.IsDir() {
				warn = append(warn, fmt.Sprintf("The folder for %s is missing (%s). Sign in again before relying on this.", acct.Name, acct.Dir))
			}
		}
	}
	p, err := accounts.BuildPreview(in)
	if err != nil {
		return accounts.Preview{}, err
	}
	p.Warnings = append(p.Warnings, warn...)
	return p, nil
}

// Apply has no side effects of its own for Claude Code: the account is
// applied by the shim at each launch, from the rule the caller saves.
func (a *claudeAdapter) Apply(_ context.Context, _ *accounts.Txn, p accounts.Preview, emit func(accounts.Event)) error {
	if emit != nil {
		emit(accounts.NewEvent("Nothing to change in Claude Code itself", accounts.StepDone,
			"Claude Code reads the account from the rule each time it starts"))
	}
	_ = p
	return nil
}

func (a *claudeAdapter) Launch(acct accounts.Account) (Launch, error) { return claudeLaunch(acct) }

// claudeLaunch is the shim's view of claudeAdapter.Launch. The default
// account clears CLAUDE_CONFIG_DIR: Devpit's answer wins over a variable
// some other tool left in the terminal.
func claudeLaunch(acct accounts.Account) (Launch, error) {
	if acct.IsDefault() {
		return defaultLaunch(claudeEnv()), nil
	}
	if acct.Dir == "" {
		return Launch{}, fmt.Errorf("the Claude Code account %s has no folder", acct.Name)
	}
	return Launch{Env: []string{claudeConfigDir + "=" + acct.Dir}}, nil
}
