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

// firebaseAdapter is the Firebase CLI, which holds several accounts itself:
//
//   - accounts are the ones `firebase login:list` prints, in its TEXT form.
//     `login:list --json` prints every account's tokens (spike, firebase-tools
//     15.32.1), so it is never run: CheckArgs refuses it in every runner;
//   - a Devpit account is a name and the Google address Firebase knows;
//     there is no folder. The shim adds `--account <email>`, a root option
//     every Firebase command honours (spike), before the person's arguments;
//   - "default" is Firebase's own choice, untouched: its global default
//     login, or a project's own account set with `firebase login:use`. A
//     Devpit rule overrides that per-project choice through --account;
//   - adding an account is `firebase login:add`, which needs a default login
//     first (Firebase refuses otherwise);
//   - who is signed in is read from login:list: it reads Firebase's own
//     store and asks no server, so it cannot disturb a login.
type firebaseAdapter struct {
	base
	probe probeOnce
}

func newFirebase(d Deps) Adapter {
	return &firebaseAdapter{base: base{
		deps: d, tool: accounts.ToolFirebase, bin: "firebase",
		why:  "Devpit could not check what this Firebase CLI supports.",
		envs: firebaseEnv(),
	}}
}

// firebaseEnv lists the variables that override Firebase's account. None is
// Managed: Verify reports them, nothing clears them.
func firebaseEnv() []EnvVar {
	return []EnvVar{
		{Name: "FIREBASE_TOKEN", Effect: "Firebase uses this token instead of any signed-in account."},
		{Name: "GOOGLE_APPLICATION_CREDENTIALS", Effect: "Firebase may use this service account instead of a signed-in account."},
	}
}

func (a *firebaseAdapter) Installed(ctx context.Context) Install {
	return installedOrNpx(ctx, a.deps, accounts.ToolFirebase, "firebase", "firebase-tools")
}

// firebaseProbe asks `firebase --help` for the --account option and the
// login:add command.
func (a *firebaseAdapter) firebaseProbe(ctx context.Context) (bool, string, error) {
	r, err := a.probe.get(func() (probeResult, error) {
		in := a.Installed(ctx)
		if !in.Found {
			return probeResult{}, fmt.Errorf("Firebase: %w", accounts.ErrNotFoundTool) //nolint:revive,staticcheck // a product name
		}
		res, err := a.deps.run(ctx, Cmd{Name: "firebase", Args: []string{"--help"}, Timeout: 30 * time.Second})
		if err != nil {
			return probeResult{version: in.Version}, err
		}
		text := string(res.Stdout) + "\n" + string(res.Stderr)
		return probeResult{ok: strings.Contains(text, "--account") && strings.Contains(text, "login:add"), version: in.Version}, nil
	})
	return r.ok, r.version, err
}

func firebaseTooOld(version string) error {
	return fmt.Errorf("Firebase CLI %s has no --account option or no `firebase login:add`, which Devpit needs to pick an account. Update it (npm install -g firebase-tools) and try again: %w", //nolint:revive,staticcheck // a product name
		versionOr(version), accounts.ErrTooOld)
}

func (a *firebaseAdapter) Capabilities(ctx context.Context) Caps {
	ok, ver, err := a.firebaseProbe(ctx)
	switch {
	case errors.Is(err, accounts.ErrNotFoundTool):
		return Caps{Why: "Firebase CLI is not installed."}
	case err != nil:
		return Caps{Why: "Devpit could not ask the Firebase CLI what it supports: " + accounts.Scrub(err.Error())}
	case !ok:
		return Caps{Why: firebaseTooOld(ver).Error()}
	}
	return Caps{FolderRules: true, Everywhere: true, JustOnce: true, AddAccount: true}
}

// LiveCheckRisk: login:list only reads Firebase's own list of accounts.
func (a *firebaseAdapter) LiveCheckRisk() (bool, string) { return false, "" }

// firebaseList is what `firebase login:list` (text) says.
type firebaseList struct {
	// Default is the "Logged in as" account, or "" when none is.
	Default string
	// Others are the "Other accounts".
	Others []string
}

// All is Default then Others.
func (l firebaseList) All() []string {
	if l.Default == "" {
		return l.Others
	}
	return append([]string{l.Default}, l.Others...)
}

func (l firebaseList) has(email string) bool {
	return slices.ContainsFunc(l.All(), func(e string) bool { return strings.EqualFold(e, email) })
}

// parseFirebaseList reads the text form of login:list:
//
//	Logged in as zubair@gmail.com
//
//	Other accounts:
//	 - zubair@work.com
//
// Colour codes and the "i"/"⚠" markers some versions add are ignored.
func parseFirebaseList(out []byte) firebaseList {
	var l firebaseList
	for _, line := range Lines([]byte(stripANSI(string(out)))) {
		line = strings.TrimLeft(line, "iI✔✖⚠! \t")
		if rest, ok := cutPrefixFold(line, "Logged in as "); ok {
			if e := emailWord(rest); e != "" && l.Default == "" {
				l.Default = e
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, "- "); ok {
			if e := emailWord(rest); e != "" && !strings.EqualFold(e, l.Default) && !slices.Contains(l.Others, e) {
				l.Others = append(l.Others, e)
			}
		}
	}
	return l
}

// emailWord is the first word of s if it is an address.
func emailWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	e := strings.TrimRight(f[0], ".,;:")
	local, domain, ok := strings.Cut(e, "@")
	if !ok || local == "" || !strings.Contains(domain, ".") || accounts.LooksSecret(e) {
		return ""
	}
	return e
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// stripANSI removes terminal colour codes (ESC [ … letter).
func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < '@' || s[j] > '~') {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// list runs `firebase login:list`, text form only.
func (a *firebaseAdapter) list(ctx context.Context) (firebaseList, error) {
	res, err := a.deps.run(ctx, Cmd{Name: "firebase", Args: []string{"login:list"}, Dir: a.deps.Home, Timeout: 30 * time.Second})
	if err != nil {
		return firebaseList{}, accounts.ScrubError(err)
	}
	return parseFirebaseList(res.Stdout), nil
}

// Accounts is default, the store's, then any account Firebase has that the
// store does not (ImportedFrom "detected", with a suggested name).
func (a *firebaseAdapter) Accounts(ctx context.Context, s *accounts.Store) ([]accounts.Account, error) {
	out := DefaultAndStore(accounts.ToolFirebase, s)
	// Detection is best effort: a Firebase that cannot list its accounts
	// still has default and the store's.
	var l firebaseList
	if ok, _, perr := a.firebaseProbe(ctx); perr == nil && ok {
		var lerr error
		if l, lerr = a.list(ctx); lerr != nil {
			a.deps.logf("firebase login:list: %v", lerr)
		}
	}
	taken := []string{accounts.DefaultName}
	for _, x := range out {
		taken = append(taken, x.Name)
	}
	for _, e := range l.Others {
		known := false
		for _, x := range out {
			if strings.EqualFold(x.Email, e) {
				known = true
			}
		}
		if known {
			continue
		}
		name := accounts.SuggestName(e, taken)
		taken = append(taken, name)
		out = append(out, accounts.Account{Tool: accounts.ToolFirebase, Name: name, Email: e, ImportedFrom: "detected"})
	}
	return out, nil
}

func (a *firebaseAdapter) WhoAmI(ctx context.Context, acct accounts.Account) (accounts.Identity, error) {
	if !acct.IsDefault() && acct.Email == "" {
		err := fmt.Errorf("the Firebase account %s has no address recorded", acct.Name)
		return accounts.NewIdentity(accounts.IdentityFields{Note: err.Error()}), err
	}
	ok, ver, err := a.firebaseProbe(ctx)
	if errors.Is(err, accounts.ErrNotFoundTool) {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotInstalled}), err
	}
	if err != nil {
		return accounts.Identity{}, accounts.ScrubError(err)
	}
	if !ok {
		e := firebaseTooOld(ver)
		return accounts.NewIdentity(accounts.IdentityFields{Note: e.Error()}), e
	}
	l, err := a.list(ctx)
	if err != nil {
		return accounts.Identity{}, err
	}
	if acct.IsDefault() {
		if l.Default == "" {
			return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotSignedIn, Note: "Not signed in. Run `firebase login`."}), nil
		}
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Email: l.Default}), nil
	}
	if l.has(acct.Email) {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Email: acct.Email}), nil
	}
	return accounts.NewIdentity(accounts.IdentityFields{
		State: accounts.StateNotSignedIn, Email: acct.Email,
		Note: "Firebase no longer has this account. Sign in again.",
	}), nil
}

// Login runs `firebase login:add` and finds the new address by comparing
// login:list before and after. Nothing is created by Devpit, so a cancelled
// sign-in leaves nothing behind.
func (a *firebaseAdapter) Login(ctx context.Context, req LoginRequest) (<-chan accounts.Event, error) {
	s := req.Store
	if s == nil {
		s = accounts.NewStore()
	}
	if err := s.CheckNewName(accounts.ToolFirebase, req.Name); err != nil {
		return nil, err
	}
	return Stream("Signing in", func(emit func(accounts.Event)) error {
		step := func(n string, st accounts.StepState, detail string) { emit(accounts.NewEvent(n, st, detail)) }
		step("Checking Firebase", accounts.StepRunning, "")
		ok, ver, err := a.firebaseProbe(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return firebaseTooOld(ver)
		}
		before, err := a.list(ctx)
		if err != nil {
			return err
		}
		if before.Default == "" {
			return errors.New("Firebase has no signed-in account yet. Run `firebase login` first: that account becomes Firebase's default, and Devpit adds the others") //nolint:revive,staticcheck // a product name
		}
		step("Checking Firebase", accounts.StepDone, "version "+versionOr(ver))

		step("Opening Firebase's own sign-in", accounts.StepRunning, "")
		step("Waiting for you to finish signing in in the browser", accounts.StepWaiting, "")
		res, err := a.deps.run(ctx, Cmd{
			Name: "firebase", Args: []string{"login:add"}, Dir: a.deps.Home,
			Timeout: LoginTimeout, Stdin: req.Stdin, Stdout: req.Stdout, Stderr: req.Stderr,
		})
		for _, l := range append(Lines(res.Stdout), Lines(res.Stderr)...) {
			step("Firebase", accounts.StepInfo, l)
		}
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%w (stopped)", accounts.ErrSignInCancelled)
			}
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%w: Firebase's sign-in ended with exit code %d", accounts.ErrSignInCancelled, res.ExitCode)
		}

		step("Checking who is signed in", accounts.StepRunning, "")
		after, err := a.list(ctx)
		if err != nil {
			return fmt.Errorf("%w: %w", accounts.ErrSignInCancelled, err)
		}
		var added []string
		for _, e := range after.All() {
			if !before.has(e) {
				added = append(added, e)
			}
		}
		var email string
		switch {
		case len(added) == 1:
			email = added[0]
		case len(added) == 0 && req.Email != "" && after.has(req.Email):
			email = req.Email
		case len(added) == 0:
			return fmt.Errorf("%w: Firebase did not add a new account (the one you picked may already be there; it is listed under detected accounts)", accounts.ErrSignInCancelled)
		default:
			return fmt.Errorf("%w: Firebase now has %d new accounts (%s), so Devpit cannot tell which one is %s; add it from the detected accounts", accounts.ErrSignInCancelled, len(added), strings.Join(added, ", "), req.Name)
		}
		id := accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Email: email})
		ev := accounts.NewEvent("Checking who is signed in", accounts.StepDone, "Signed in as "+email)
		ev.Identity = &id
		emit(ev)

		acct := storableAccount(accounts.Account{Tool: accounts.ToolFirebase, Name: req.Name, Email: email, Added: a.deps.now().UTC()})
		if acct.Email == "" {
			return fmt.Errorf("%w: Firebase reported an address Devpit cannot store", accounts.ErrSignInCancelled)
		}
		final := accounts.NewEvent("Signed in", accounts.StepDone, "Signed in as "+email)
		final.Final, final.Account, final.Identity = true, &acct, &id
		emit(final)
		return nil
	}), nil
}

func (a *firebaseAdapter) Plan(in accounts.PreviewInput) (accounts.Preview, error) {
	return planChecked(accounts.ToolFirebase, in, func(_ *accounts.Store, acct accounts.Account, scope accounts.Scope) ([]string, error) {
		if acct.Email == "" {
			return nil, fmt.Errorf("the Firebase account %s has no address recorded; sign in again", acct.Name)
		}
		if scope.Kind == accounts.ScopeFolder {
			return []string{"This rule wins over an account the project picked with `firebase login:use`."}, nil
		}
		return nil, nil
	})
}

func (a *firebaseAdapter) Apply(_ context.Context, _ *accounts.Txn, _ accounts.Preview, emit func(accounts.Event)) error {
	noSideEffects(accounts.ToolFirebase, emit)
	return nil
}

// Launch is what the shim applies for one account.
func (a *firebaseAdapter) Launch(acct accounts.Account) (Launch, error) { return firebaseLaunch(acct) }

// firebaseLaunch is the shim's view of [firebaseAdapter.Launch]: no adapter, no
// command run. --account is a root option, so it goes first.
func firebaseLaunch(acct accounts.Account) (Launch, error) {
	if acct.IsDefault() {
		return defaultLaunch(firebaseEnv()), nil
	}
	if acct.Email == "" {
		return Launch{}, fmt.Errorf("the Firebase account %s has no address recorded", acct.Name)
	}
	return Launch{Args: []string{"--account", acct.Email}}, nil
}
