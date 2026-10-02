package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// EffectCloudflareBinding is the journal's kind for one Wrangler folder
// binding Devpit made or removed. Its undo needs the handler from
// [CloudflareHandlers] on the Engine.
const EffectCloudflareBinding accounts.EffectKind = "cloudflare-binding"

// ProblemWranglerDrift is a Verify problem: Wrangler's own folder bindings
// and Devpit's Cloudflare rules disagree.
const ProblemWranglerDrift accounts.ProblemKind = "wrangler binding differs"

// wranglerReserved are the profile names Wrangler refuses for a named
// profile (4.143.0: RESERVED_PROFILE_NAMES).
var wranglerReserved = [...]string{"default", "staging"}

// cloudflareCredVars are what Wrangler's getAuthFromEnv reads. Wrangler
// refuses `auth create/activate/deactivate` while one is set, and its whoami
// would describe the token, not the profile, so Devpit's own runs go
// without them. The person's own Wrangler runs keep them (Verify flags
// them).
var cloudflareCredVars = []string{"CLOUDFLARE_API_TOKEN", "CLOUDFLARE_API_KEY", "CLOUDFLARE_EMAIL"}

// wranglerQuiet is set for Devpit's own Wrangler runs. Measured on this PC
// (Wrangler 4.143.0, no terminal): `wrangler auth list` printed its table
// at once but took 23 s to over 90 s to exit; with the banner (and its
// update check) hidden and metrics off it exits in about 1 s, every time.
// The person's own Wrangler runs are not changed.
func wranglerQuiet() []string {
	return []string{"WRANGLER_HIDE_BANNER=true", "WRANGLER_SEND_METRICS=false"}
}

// cloudflareAdapter is Wrangler with its own named profiles, which Wrangler
// calls experimental, so the row says beta:
//
//   - a Devpit Cloudflare account is a Wrangler profile of the same name,
//     made with `wrangler auth create <name>`;
//   - a folder rule is mirrored into Wrangler's own folder bindings with
//     `wrangler auth activate <name> <folder>` (and `deactivate` to remove
//     it), as a journalled side effect with an exact undo. Wrangler then
//     picks the profile itself, so the rule also works for `npx wrangler`
//     and for anything that does not go through Devpit's shim;
//   - Wrangler compares folders with a case-sensitive prefix match, so the
//     folder is written in its real on-disk letter case, resolved component
//     by component from the file system (not from what was typed);
//   - "everywhere" stays Wrangler's own default login (`wrangler login`);
//     Wrangler has no switch for it, so Devpit does not offer one;
//   - just this once is `--profile <name>`, a global Wrangler flag;
//   - Verify compares Devpit's rules with Wrangler's directory-bindings.json,
//     read only (it maps folders to profile names; no secret is in it).
type cloudflareAdapter struct {
	base
	probe probeOnce
}

func newCloudflare(d Deps) Adapter {
	return &cloudflareAdapter{base: base{
		deps: d, tool: accounts.ToolCloudflare, bin: "wrangler",
		why:  "Devpit could not check what this Wrangler supports.",
		envs: cloudflareEnv(),
	}}
}

// cloudflareEnv lists the variables that override Wrangler's account. None
// is Managed: Verify reports them, nothing clears them.
func cloudflareEnv() []EnvVar {
	return []EnvVar{
		{Name: "CLOUDFLARE_API_TOKEN", Effect: "Wrangler uses this token instead of any profile, and refuses to manage profiles while it is set."},
		{Name: "CLOUDFLARE_API_KEY", Effect: "Wrangler uses this key instead of any profile."},
		{Name: "CLOUDFLARE_ACCOUNT_ID", Effect: "Wrangler uses this Cloudflare account, whichever profile is signed in."},
		{Name: "XDG_CONFIG_HOME", Effect: "Wrangler reads its profiles and folder bindings from under this folder instead of the usual one."},
	}
}

func (a *cloudflareAdapter) Installed(ctx context.Context) Install {
	in := FindInstalled(ctx, a.deps, "wrangler")
	if !in.Found {
		if _, err := a.deps.lookPath("npx"); err == nil {
			in.Note = "Wrangler is not installed on PATH. Devpit needs it there to manage profiles (`npm install -g wrangler`); `npx wrangler` in a project still follows the folder rules Devpit sets."
		}
	}
	return in
}

// cloudflareProbe asks `wrangler auth --help` whether named profiles exist.
func (a *cloudflareAdapter) cloudflareProbe(ctx context.Context) (bool, string, error) {
	r, err := a.probe.get(func() (probeResult, error) {
		in := a.Installed(ctx)
		if !in.Found {
			return probeResult{}, fmt.Errorf("Wrangler: %w", accounts.ErrNotFoundTool) //nolint:revive,staticcheck // a product name
		}
		res, err := a.deps.run(ctx, Cmd{Name: "wrangler", Env: wranglerQuiet(), Args: []string{"auth", "--help"}, Dir: a.deps.Home, Timeout: 30 * time.Second})
		if err != nil {
			return probeResult{version: in.Version}, err
		}
		text := string(res.Stdout) + "\n" + string(res.Stderr)
		ok := res.ExitCode == 0 && strings.Contains(text, "activate") && strings.Contains(text, "deactivate") && strings.Contains(text, "create")
		return probeResult{ok: ok, version: in.Version}, nil
	})
	return r.ok, r.version, err
}

func wranglerTooOld(version string) error {
	return fmt.Errorf("Wrangler %s has no named profiles (`wrangler auth create/activate`). Update Wrangler (4.143.0 is the oldest version Devpit has checked) and try again: %w", //nolint:revive,staticcheck // a product name
		versionOr(version), accounts.ErrTooOld)
}

// cloudflareEverywhereWhy is why "everywhere" is not offered.
const cloudflareEverywhereWhy = "Wrangler profiles are beta. Everywhere else, Wrangler uses its own default login (`wrangler login`); Devpit cannot change that in this version."

func (a *cloudflareAdapter) Capabilities(ctx context.Context) Caps {
	ok, ver, err := a.cloudflareProbe(ctx)
	switch {
	case errors.Is(err, accounts.ErrNotFoundTool):
		return Caps{Beta: true, Why: "Wrangler is not installed."}
	case err != nil:
		return Caps{Beta: true, Why: "Devpit could not ask Wrangler what it supports: " + accounts.Scrub(err.Error())}
	case !ok:
		return Caps{Beta: true, Why: wranglerTooOld(ver).Error()}
	}
	return Caps{FolderRules: true, JustOnce: true, AddAccount: true, Beta: true, Why: cloudflareEverywhereWhy}
}

// LiveCheckRisk: `wrangler whoami` may refresh an expired profile login,
// and Wrangler saves the refreshed one itself, so asking cannot sign a
// profile out.
func (a *cloudflareAdapter) LiveCheckRisk() (bool, string) { return false, "" }

// checkProfileName is the extra name check for a Wrangler profile:
// Devpit's own names are already a subset of Wrangler's pattern, but
// Wrangler reserves "staging" too.
func checkProfileName(name string) error {
	for _, r := range wranglerReserved {
		if strings.EqualFold(name, r) {
			return fmt.Errorf("%q is reserved by Wrangler for its own use; pick another name: %w", name, accounts.ErrReservedName)
		}
	}
	return nil
}

// wranglerProfile is the --profile value for an account.
func wranglerProfile(acct accounts.Account) string {
	if acct.IsDefault() {
		return accounts.DefaultName
	}
	return acct.Name
}

// cloudflareWhoami is `wrangler whoami --json`.
type cloudflareWhoami struct {
	LoggedIn *bool  `json:"loggedIn"`
	AuthType string `json:"authType"`
	Email    string `json:"email"`
	Accounts []struct {
		Name string `json:"name"`
	} `json:"accounts"`
}

func (a *cloudflareAdapter) WhoAmI(ctx context.Context, acct accounts.Account) (accounts.Identity, error) {
	ok, ver, err := a.cloudflareProbe(ctx)
	if errors.Is(err, accounts.ErrNotFoundTool) {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotInstalled}), err
	}
	if err != nil {
		return accounts.Identity{}, accounts.ScrubError(err)
	}
	args := []string{"whoami", "--json"}
	switch {
	case ok:
		// --profile default asks for the default login even inside a bound
		// folder; Wrangler accepts "default" there.
		args = append(args, "--profile", wranglerProfile(acct))
	case !acct.IsDefault():
		e := wranglerTooOld(ver)
		return accounts.NewIdentity(accounts.IdentityFields{Note: e.Error()}), e
	}
	res, err := a.deps.run(ctx, Cmd{Name: "wrangler", Env: wranglerQuiet(), Args: args, Unset: cloudflareCredVars, Dir: a.deps.Home, Timeout: 45 * time.Second})
	if err != nil {
		return accounts.Identity{}, accounts.ScrubError(err)
	}
	var w cloudflareWhoami
	if err := DecodeJSON(res.Stdout, &w); err != nil {
		return accounts.Identity{}, fmt.Errorf("Wrangler answered `wrangler whoami` in a way Devpit does not understand (exit %d): %s", //nolint:revive,staticcheck // a product name
			res.ExitCode, firstOf(wranglerFailure(res), FirstLine(res.Stdout)))
	}
	if w.LoggedIn == nil || !*w.LoggedIn {
		note := "Not signed in. Sign in again with `wrangler auth create " + wranglerProfile(acct) + "`."
		if acct.IsDefault() {
			note = "Not signed in. Run `wrangler login`."
		}
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotSignedIn, Note: note}), nil
	}
	org := ""
	if len(w.Accounts) == 1 {
		org = w.Accounts[0].Name
	}
	return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Email: w.Email, Org: org, Method: w.AuthType}), nil
}

// wranglerFailure is the line of Wrangler's output that says what went
// wrong ("✘ [ERROR] …"), scrubbed.
func wranglerFailure(res Result) string {
	all := append(Lines([]byte(stripANSI(string(res.Stderr)))), Lines([]byte(stripANSI(string(res.Stdout))))...)
	for _, l := range all {
		if _, after, ok := strings.Cut(l, "[ERROR]"); ok {
			return accounts.Scrub(strings.TrimSpace(after))
		}
	}
	if len(all) > 0 {
		return accounts.Scrub(all[0])
	}
	return ""
}

// profiles runs `wrangler auth list` and returns the profile names.
func (a *cloudflareAdapter) profiles(ctx context.Context) ([]string, error) {
	res, err := a.deps.run(ctx, Cmd{Name: "wrangler", Env: wranglerQuiet(), Args: []string{"auth", "list"}, Dir: a.deps.Home, Timeout: 30 * time.Second})
	if err != nil {
		return nil, accounts.ScrubError(err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("wrangler auth list failed: %s", wranglerFailure(res))
	}
	return parseWranglerProfiles(res.Stdout), nil
}

// parseWranglerProfiles reads the table `wrangler auth list` prints
// (Profile | Bound Directories), in box-drawing or plain form.
func parseWranglerProfiles(out []byte) []string {
	var names []string
	for _, l := range Lines([]byte(stripANSI(string(out)))) {
		l = strings.NewReplacer("│", "|", "┃", "|", "║", "|").Replace(l)
		if !strings.Contains(l, "|") {
			continue
		}
		var cells []string
		for _, c := range strings.Split(l, "|") {
			if c = strings.TrimSpace(c); c != "" {
				cells = append(cells, c)
			}
		}
		if len(cells) == 0 || strings.EqualFold(cells[0], "Profile") || !validProfile(cells[0]) {
			continue
		}
		if !slices.Contains(names, cells[0]) {
			names = append(names, cells[0])
		}
	}
	return names
}

// validProfile is Wrangler's own pattern: ^[a-zA-Z0-9_-]+$.
func validProfile(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// Accounts is default, the store's, then any Wrangler profile the store
// does not have (ImportedFrom "detected"). A profile whose name Devpit
// cannot use (an underscore, say) is left out.
func (a *cloudflareAdapter) Accounts(ctx context.Context, s *accounts.Store) ([]accounts.Account, error) {
	out := DefaultAndStore(accounts.ToolCloudflare, s)
	// Detection is best effort: a Wrangler that cannot list its profiles
	// still has default and the store's accounts.
	var names []string
	if ok, _, perr := a.cloudflareProbe(ctx); perr == nil && ok {
		var lerr error
		if names, lerr = a.profiles(ctx); lerr != nil {
			a.deps.logf("wrangler auth list: %v", lerr)
		}
	}
	for _, n := range names {
		if accounts.ValidateName(n) != nil || checkProfileName(n) != nil {
			continue
		}
		if slices.ContainsFunc(out, func(x accounts.Account) bool { return strings.EqualFold(x.Name, n) }) {
			continue
		}
		out = append(out, accounts.Account{Tool: accounts.ToolCloudflare, Name: n, ImportedFrom: "detected"})
	}
	return out, nil
}

// Login runs `wrangler auth create <name>`. Wrangler keeps the profile's
// login itself; Devpit creates nothing, so a cancelled sign-in leaves
// nothing of Devpit's behind.
func (a *cloudflareAdapter) Login(ctx context.Context, req LoginRequest) (<-chan accounts.Event, error) {
	s := req.Store
	if s == nil {
		s = accounts.NewStore()
	}
	if err := s.CheckNewName(accounts.ToolCloudflare, req.Name); err != nil {
		return nil, err
	}
	if err := checkProfileName(req.Name); err != nil {
		return nil, err
	}
	return Stream("Signing in", func(emit func(accounts.Event)) error {
		step := func(n string, st accounts.StepState, detail string) { emit(accounts.NewEvent(n, st, detail)) }
		step("Checking Wrangler", accounts.StepRunning, "")
		ok, ver, err := a.cloudflareProbe(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return wranglerTooOld(ver)
		}
		existing, err := a.profiles(ctx)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(existing, func(p string) bool { return strings.EqualFold(p, req.Name) }) {
			return fmt.Errorf("Wrangler already has a profile called %s. Add it as an existing account instead of signing in again", req.Name) //nolint:revive,staticcheck // a product name
		}
		step("Checking Wrangler", accounts.StepDone, "version "+versionOr(ver))

		step("Opening Wrangler's own sign-in", accounts.StepRunning, "")
		step("Waiting for you to finish signing in in the browser", accounts.StepWaiting, "")
		res, err := a.deps.run(ctx, Cmd{
			Name: "wrangler", Env: wranglerQuiet(), Args: []string{"auth", "create", req.Name}, Unset: cloudflareCredVars, Dir: a.deps.Home,
			Timeout: LoginTimeout, Stdin: req.Stdin, Stdout: req.Stdout, Stderr: req.Stderr,
		})
		for _, l := range append(Lines(res.Stdout), Lines(res.Stderr)...) {
			step("Wrangler", accounts.StepInfo, l)
		}
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%w (stopped)", accounts.ErrSignInCancelled)
			}
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%w: %s", accounts.ErrSignInCancelled, firstOf(wranglerFailure(res)))
		}

		step("Checking who is signed in", accounts.StepRunning, "")
		id, err := a.WhoAmI(ctx, accounts.Account{Tool: accounts.ToolCloudflare, Name: req.Name})
		kept := fmt.Sprintf(" Wrangler kept the profile %s; remove it with `wrangler auth delete %s` if you do not want it.", req.Name, req.Name)
		if err != nil {
			return fmt.Errorf("%w: %w.%s", accounts.ErrSignInCancelled, err, kept)
		}
		if !id.SignedIn() {
			return fmt.Errorf("%w: Wrangler says the profile is %s.%s", accounts.ErrSignInCancelled, id.State(), kept)
		}
		ev := accounts.NewEvent("Checking who is signed in", accounts.StepDone, "Signed in as "+id.Who())
		ev.Identity = &id
		emit(ev)

		acct := storableAccount(accounts.Account{
			Tool: accounts.ToolCloudflare, Name: req.Name, Email: id.Email(), Org: id.Fields().Org, Added: a.deps.now().UTC(),
		})
		final := accounts.NewEvent("Signed in", accounts.StepDone, "Signed in as "+id.Who())
		final.Final, final.Account, final.Identity = true, &acct, &id
		emit(final)
		return nil
	}), nil
}

// ---- Wrangler's folder bindings ----

// WranglerConfigDir is Wrangler's global config folder, as Wrangler 4.143
// works it out: %USERPROFILE%\.wrangler if that folder exists (legacy),
// else XDG_CONFIG_HOME\.wrangler, else %APPDATA%\xdg.config\.wrangler.
func WranglerConfigDir(d Deps) string {
	if d.Home != "" {
		legacy := filepath.Join(d.Home, ".wrangler")
		if fi, err := os.Stat(legacy); err == nil && fi.IsDir() {
			return legacy
		}
	}
	base := d.getenv("XDG_CONFIG_HOME")
	if base == "" {
		app := d.getenv("APPDATA")
		if app == "" {
			return ""
		}
		base = filepath.Join(app, "xdg.config")
	}
	return filepath.Join(base, ".wrangler")
}

// WranglerBindingsFile is profiles\directory-bindings.json in Wrangler's
// config folder, or "" when that cannot be worked out.
func WranglerBindingsFile(d Deps) string {
	dir := WranglerConfigDir(d)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "profiles", "directory-bindings.json")
}

// readWranglerBindings reads Wrangler's bindings: a JSON object of folder →
// profile name, and nothing else (Wrangler 4.143.0, createWranglerProfileStore).
// A missing file is no bindings. Entries that are not a folder and a
// profile name are skipped; nothing from the file appears in an error.
func readWranglerBindings(path string) (map[string]string, error) {
	out := map[string]string{}
	if path == "" {
		return out, nil
	}
	data, err := readSmallFile(path, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Wrangler's folder bindings (%s) could not be read", path) //nolint:revive,staticcheck // a product name
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("Wrangler's folder bindings (%s) are not valid JSON", path) //nolint:revive,staticcheck // a product name
	}
	for k, v := range raw {
		p, ok := v.(string)
		if !ok || !validProfile(p) || k == "" || accounts.PathLooksSecret(k) {
			continue
		}
		out[k] = p
	}
	return out, nil
}

// wranglerOuter is the binding Wrangler would use for dir if dir had none
// of its own: the longest bound folder that contains it, compared the way
// Wrangler does (case-sensitive).
func wranglerOuter(bindings map[string]string, dir string) (string, string) {
	best, prof := "", ""
	for k, p := range bindings {
		if k == dir || len(k) <= len(best) {
			continue
		}
		sep := strings.TrimRight(k, `\`) + `\`
		if strings.HasPrefix(dir, sep) {
			best, prof = k, p
		}
	}
	return best, prof
}

// wranglerOp is one binding change: Before and After are profile names,
// "" for "no binding of its own".
type wranglerOp struct {
	Dir, Before, After string
}

// cloudflareOp works out the binding change a Devpit change needs. It
// returns nil when Wrangler already matches, and an error when Wrangler
// cannot express the change.
func cloudflareOp(c accounts.Change, bindings map[string]string) (*wranglerOp, []string, error) {
	switch c.Scope.Kind {
	case accounts.ScopeOnce:
		return nil, nil, nil
	case accounts.ScopeEverywhere:
		return nil, nil, NotSupported(accounts.ToolCloudflare, "choosing the account used everywhere ("+cloudflareEverywhereWhy+")")
	}
	dir := wranglerTrueCase(c.Scope.Folder)
	cur, has := bindings[dir]
	var warn []string
	for k, p := range bindings {
		if k != dir && strings.EqualFold(k, dir) {
			warn = append(warn, fmt.Sprintf("Wrangler also has a binding for %s (profile %s), spelled with different letter case. Wrangler treats it as another folder.", k, p))
		}
	}
	if !c.Remove && !accounts.IsDefault(c.Account) {
		warn = append(warn, fmt.Sprintf("Wrangler matches folders by exact letter case, so this applies where the path is spelled %s.", dir))
		if _, err := os.Stat(c.Scope.Folder); err != nil {
			warn = append(warn, c.Scope.Folder+" does not exist yet, so its letter case is taken as typed.")
		}
		if has && cur == c.Account {
			return nil, warn, nil
		}
		return &wranglerOp{Dir: dir, Before: cur, After: c.Account}, warn, nil
	}
	// The default account, or no rule of its own: Wrangler has no binding
	// for "default", so the folder must simply not be bound, and no outer
	// binding may cover it.
	rest := map[string]string{}
	for k, p := range bindings {
		if k != dir {
			rest[k] = p
		}
	}
	if outer, prof := wranglerOuter(rest, dir); outer != "" {
		if !c.Remove {
			return nil, warn, fmt.Errorf("Wrangler cannot use its default login in %s: it sits inside %s, which Wrangler binds to the profile %s, and Wrangler has no way to bind a folder back to its default login. Run `wrangler --profile default …` there instead: %w", //nolint:revive,staticcheck // a product name
				dir, outer, prof, accounts.ErrNotSupported)
		}
		warn = append(warn, fmt.Sprintf("Inside %s, Wrangler will use the profile %s from its binding on %s.", dir, prof, outer))
	}
	if !has {
		return nil, warn, nil
	}
	return &wranglerOp{Dir: dir, Before: cur}, warn, nil
}

func (a *cloudflareAdapter) Plan(in accounts.PreviewInput) (accounts.Preview, error) {
	if in.Change.Tool != accounts.ToolCloudflare {
		return accounts.Preview{}, fmt.Errorf("the Cloudflare adapter cannot plan a %s change", in.Change.Tool)
	}
	if in.Change.Scope.Kind == accounts.ScopeEverywhere {
		return accounts.Preview{}, NotSupported(accounts.ToolCloudflare, "choosing the account used everywhere ("+cloudflareEverywhereWhy+")")
	}
	p, err := accounts.BuildPreview(in)
	if err != nil || p.NoChange {
		return p, err
	}
	path := WranglerBindingsFile(a.deps)
	bindings, err := readWranglerBindings(path)
	if err != nil {
		return accounts.Preview{}, err
	}
	op, warn, err := cloudflareOp(p.Change, bindings)
	if err != nil {
		return accounts.Preview{}, err
	}
	p.Warnings = append(p.Warnings, warn...)
	if op != nil {
		p.Edits = append(p.Edits, op.edit(path))
	}
	return p, nil
}

// edit is how the preview shows a binding change: the JSON line Wrangler
// writes and the command Devpit runs to write it.
func (op wranglerOp) edit(path string) accounts.FileEdit {
	key, _ := json.Marshal(op.Dir)
	if path == "" {
		path = "directory-bindings.json"
	}
	if op.After == "" {
		return accounts.FileEdit{Path: path, Action: "removes", Lines: []string{
			fmt.Sprintf("%s: %q", key, op.Before), "(through `wrangler auth deactivate " + op.Dir + "`)",
		}}
	}
	action := "adds"
	if op.Before != "" {
		action = "changes"
	}
	return accounts.FileEdit{Path: path, Action: action, Lines: []string{
		fmt.Sprintf("%s: %q", key, op.After), "(through `wrangler auth activate " + op.After + " " + op.Dir + "`)",
	}}
}

// Apply mirrors a folder rule into Wrangler's bindings, recorded in the
// journal first so it can be undone exactly. The Engine needs the handler
// from CloudflareHandlers; without it nothing is run and the change fails.
func (a *cloudflareAdapter) Apply(ctx context.Context, txn *accounts.Txn, p accounts.Preview, emit func(accounts.Event)) error {
	bindings, err := readWranglerBindings(WranglerBindingsFile(a.deps))
	if err != nil {
		return err
	}
	op, _, err := cloudflareOp(p.Change, bindings)
	if err != nil {
		return err
	}
	if op == nil {
		if emit != nil {
			emit(accounts.NewEvent("Nothing to change in Wrangler", accounts.StepDone, "Wrangler's folder bindings already match"))
		}
		return nil
	}
	what := "Binding " + op.Dir + " to the Wrangler profile " + op.After
	if op.After == "" {
		what = "Removing Wrangler's binding on " + op.Dir
	}
	if emit != nil {
		emit(accounts.NewEvent(what, accounts.StepRunning, ""))
	}
	eff := accounts.Effect{
		Kind: EffectCloudflareBinding, Tool: accounts.ToolCloudflare, Target: op.Dir, Summary: what,
		Data: map[string]string{"dir": op.Dir, "before": op.Before, "after": op.After},
	}
	if err := txn.Do(eff, func() (accounts.Effect, error) {
		return eff, a.setBinding(ctx, op.Dir, op.After)
	}); err != nil {
		return err
	}
	if emit != nil {
		emit(accounts.NewEvent(what, accounts.StepDone, ""))
	}
	return nil
}

// setBinding makes Wrangler bind dir to profile ("" removes the binding)
// and checks the result in the bindings file.
func (a *cloudflareAdapter) setBinding(ctx context.Context, dir, profile string) error {
	args := []string{"auth", "activate", profile, dir}
	if profile == "" {
		args = []string{"auth", "deactivate", dir}
	}
	res, err := a.deps.run(ctx, Cmd{Name: "wrangler", Env: wranglerQuiet(), Args: args, Unset: cloudflareCredVars, Dir: a.deps.Home, Timeout: 30 * time.Second})
	if err != nil {
		return accounts.ScrubError(err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("`wrangler %s` failed: %s", strings.Join(args, " "), firstOf(wranglerFailure(res)))
	}
	b, err := readWranglerBindings(WranglerBindingsFile(a.deps))
	if err != nil {
		return err
	}
	if b[dir] != profile {
		return fmt.Errorf("Wrangler did not record %s the way Devpit expected; run `devpit accounts verify`", dir) //nolint:revive,staticcheck // a product name
	}
	return nil
}

// CloudflareHandlers returns the undo handlers this adapter's side effects
// need. Whoever builds the accounts.Engine adds them to Engine.Handlers.
func CloudflareHandlers(d Deps) map[accounts.EffectKind]accounts.EffectHandler {
	a, _ := newCloudflare(d).(*cloudflareAdapter)
	return map[accounts.EffectKind]accounts.EffectHandler{EffectCloudflareBinding: cloudflareBindingHandler{a: a}}
}

// cloudflareBindingHandler checks and undoes one binding change.
type cloudflareBindingHandler struct{ a *cloudflareAdapter }

func (h cloudflareBindingHandler) State(e accounts.Effect) (accounts.EffectState, error) {
	b, err := readWranglerBindings(WranglerBindingsFile(h.a.deps))
	if err != nil {
		return accounts.Changed, err
	}
	switch cur := b[e.Data["dir"]]; cur {
	case e.Data["after"]:
		return accounts.AtAfter, nil
	case e.Data["before"]:
		return accounts.AtBefore, nil
	}
	return accounts.Changed, nil
}

func (h cloudflareBindingHandler) Revert(e accounts.Effect) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return "", h.a.setBinding(ctx, e.Data["dir"], e.Data["before"])
}

// CloudflareDrift compares Devpit's Cloudflare rules with Wrangler's own
// folder bindings, reading the bindings file only. It reports a rule
// Wrangler does not follow (missing, another profile, other letter case)
// and a binding made outside Devpit.
func CloudflareDrift(d Deps, s *accounts.Store) ([]accounts.Problem, error) {
	if s == nil {
		s = accounts.NewStore()
	}
	bindings, err := readWranglerBindings(WranglerBindingsFile(d))
	if err != nil {
		return nil, err
	}
	var out []accounts.Problem
	add := func(folder, acct, msg string) {
		out = append(out, accounts.Problem{Kind: ProblemWranglerDrift, Tool: accounts.ToolCloudflare, Folder: folder, Account: acct, Message: msg})
	}
	claimed := map[string]bool{}
	for _, r := range s.Rules {
		name, ok := r.Accounts[accounts.ToolCloudflare]
		if !ok {
			continue
		}
		dir := wranglerTrueCase(r.Folder)
		claimed[dir] = true
		cur, has := bindings[dir]
		if accounts.IsDefault(name) {
			if has {
				add(r.Folder, name, fmt.Sprintf("Devpit's rule says Cloudflare uses its default login in %s, but Wrangler binds it to the profile %s.", dir, cur))
			} else if outer, prof := wranglerOuter(bindings, dir); outer != "" {
				add(r.Folder, name, fmt.Sprintf("Devpit's rule says Cloudflare uses its default login in %s, but Wrangler uses the profile %s from its binding on %s.", dir, prof, outer))
			}
			continue
		}
		switch {
		case has && cur == name:
		case has:
			add(r.Folder, name, fmt.Sprintf("Devpit's rule says Cloudflare uses %s in %s, but Wrangler binds it to the profile %s.", name, dir, cur))
		default:
			msg := fmt.Sprintf("Devpit's rule says Cloudflare uses %s in %s, but Wrangler has no binding there.", name, dir)
			for k, p := range bindings {
				if strings.EqualFold(k, dir) {
					claimed[k] = true
					msg = fmt.Sprintf("Devpit's rule says Cloudflare uses %s in %s, but Wrangler's binding is spelled %s (profile %s), and Wrangler matches letter case exactly.", name, dir, k, p)
				}
			}
			add(r.Folder, name, msg)
		}
	}
	keys := make([]string, 0, len(bindings))
	for k := range bindings {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !claimed[k] {
			add(k, bindings[k], fmt.Sprintf("Wrangler binds %s to the profile %s, but Devpit has no rule there (it was made with `wrangler auth activate`).", k, bindings[k]))
		}
	}
	return out, nil
}

// Launch is what the launcher applies for one account.
func (a *cloudflareAdapter) Launch(acct accounts.Account) (Launch, error) {
	return cloudflareLaunch(acct)
}

// cloudflareLaunch is the launcher's view of [cloudflareAdapter.Launch]: no
// adapter, no command run. A named profile is `--profile <name>`, a global
// Wrangler flag, first. Folder rules do not need it (Wrangler's own
// bindings pick the profile), and Wrangler's own `auth` commands refuse
// --profile, so Wrangler is best left unshimmed; see the report.
func cloudflareLaunch(acct accounts.Account) (Launch, error) {
	if acct.IsDefault() {
		return defaultLaunch(cloudflareEnv()), nil
	}
	if !validProfile(acct.Name) || checkProfileName(acct.Name) != nil {
		return Launch{}, fmt.Errorf("%q cannot be a Wrangler profile name", acct.Name)
	}
	return Launch{Args: []string{"--profile", acct.Name}}, nil
}
