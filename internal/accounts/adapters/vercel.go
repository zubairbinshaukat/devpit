package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// vercelAdapter is the Vercel CLI, fully supported:
//
//   - an account is its own config folder under
//     %USERPROFILE%\.devpit\accounts\vercel\<name>. The shim puts
//     `--global-config <folder>` right after the program name, before the
//     person's own arguments: the spike found both positions work, and
//     first never clashes with arguments after `--`;
//   - a new account signs in with `vercel --global-config <folder> login`
//     into a fresh folder, kept only if the sign-in finished. Vercel writes
//     config.json, auth.json and telemetry files there; that is normal;
//   - who is signed in: `vercel --global-config <folder> whoami --json`.
//     Signed out is exit 1 and {"loggedIn": false}. Signed in, Vercel 60
//     prints {team, plan, username, email, name}, read from the installed
//     source, not yet from a real sign-in, so the parse is defensive and
//     falls back to the plain-text answer;
//   - whether this Vercel has --global-config is probed at run time (`vercel
//     whoami --help`), not taken from the version number;
//   - a folder linked to a project (.vercel\project.json) names the team or
//     person that owns it (orgId). [VercelProjectLink] exposes that so
//     Verify can warn when the account in use there may not see it. Devpit
//     never asks Vercel's API about it;
//   - the default account is Vercel's own folder, untouched.
type vercelAdapter struct {
	base
	probe probeOnce
}

func newVercel(d Deps) Adapter {
	return &vercelAdapter{base: base{
		deps: d, tool: accounts.ToolVercel, bin: "vercel",
		why:  "Devpit could not check what this Vercel CLI supports.",
		envs: vercelEnv(),
	}}
}

// vercelEnv lists the variables that override Vercel's account. None is
// Managed: Verify reports them, nothing clears them.
func vercelEnv() []EnvVar {
	return []EnvVar{
		{Name: "VERCEL_TOKEN", Effect: "Vercel uses this token instead of any signed-in account."},
		{Name: "VERCEL_ORG_ID", Effect: "Vercel uses this team or account for the project, whichever account is signed in."},
		{Name: "VERCEL_PROJECT_ID", Effect: "Vercel uses this project instead of the folder's own link."},
	}
}

func (a *vercelAdapter) Installed(ctx context.Context) Install {
	return installedOrNpx(ctx, a.deps, accounts.ToolVercel, "vercel", "vercel")
}

// vercelProbe asks `vercel whoami --help` whether --global-config exists.
func (a *vercelAdapter) vercelProbe(ctx context.Context) (bool, string, error) {
	r, err := a.probe.get(func() (probeResult, error) {
		in := a.Installed(ctx)
		if !in.Found {
			return probeResult{}, fmt.Errorf("Vercel: %w", accounts.ErrNotFoundTool) //nolint:revive,staticcheck // a product name
		}
		res, err := a.deps.run(ctx, Cmd{Name: "vercel", Args: []string{"whoami", "--help"}, Timeout: 30 * time.Second})
		if err != nil {
			return probeResult{version: in.Version}, err
		}
		text := string(res.Stdout) + "\n" + string(res.Stderr)
		return probeResult{ok: strings.Contains(text, "--global-config"), version: in.Version}, nil
	})
	return r.ok, r.version, err
}

func vercelTooOld(version string) error {
	return fmt.Errorf("Vercel CLI %s has no --global-config option, which Devpit needs to keep each account in its own folder. Update it (npm install -g vercel) and try again: %w", //nolint:revive,staticcheck // a product name
		versionOr(version), accounts.ErrTooOld)
}

func (a *vercelAdapter) Capabilities(ctx context.Context) Caps {
	ok, ver, err := a.vercelProbe(ctx)
	switch {
	case errors.Is(err, accounts.ErrNotFoundTool):
		return Caps{Why: "Vercel CLI is not installed."}
	case err != nil:
		return Caps{Why: "Devpit could not ask the Vercel CLI what it supports: " + accounts.Scrub(err.Error())}
	case !ok:
		return Caps{Why: vercelTooOld(ver).Error()}
	}
	return Caps{FolderRules: true, Everywhere: true, JustOnce: true, AddAccount: true}
}

// LiveCheckRisk: `vercel whoami` only reads the folder's login and asks
// Vercel who it is. A Vercel login does not refresh, so asking cannot spend
// or replace it.
func (a *vercelAdapter) LiveCheckRisk() (bool, string) { return false, "" }

// vercelWhoami is the part of `vercel whoami --json` Devpit reads.
type vercelWhoami struct {
	LoggedIn *bool  `json:"loggedIn"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Plan     string `json:"plan"`
	Team     *struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"team"`
}

func (a *vercelAdapter) WhoAmI(ctx context.Context, acct accounts.Account) (accounts.Identity, error) {
	var args []string
	dir := a.deps.Home
	if !acct.IsDefault() {
		if err := accountFolderReady(accounts.ToolVercel, acct); err != nil {
			return accounts.NewIdentity(accounts.IdentityFields{Note: err.Error()}), err
		}
		args = append(args, "--global-config", acct.Dir)
		dir = acct.Dir
	}
	ok, ver, err := a.vercelProbe(ctx)
	if errors.Is(err, accounts.ErrNotFoundTool) {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotInstalled}), err
	}
	if err != nil {
		return accounts.Identity{}, accounts.ScrubError(err)
	}
	if !ok && !acct.IsDefault() {
		e := vercelTooOld(ver)
		return accounts.NewIdentity(accounts.IdentityFields{Note: e.Error()}), e
	}
	// The account's own folder (or home) as the working folder: whoami
	// otherwise reports the team of a project linked in Devpit's folder.
	args = append(args, "whoami", "--json")
	res, err := a.deps.run(ctx, Cmd{Name: "vercel", Args: args, Dir: dir, Timeout: 30 * time.Second})
	if err != nil {
		return accounts.Identity{}, accounts.ScrubError(err)
	}
	return parseVercelWhoami(res)
}

// parseVercelWhoami reads the JSON form, and falls back to the text form
// (a non-terminal `vercel whoami` prints just the user name).
func parseVercelWhoami(res Result) (accounts.Identity, error) {
	var w vercelWhoami
	if err := DecodeJSON(res.Stdout, &w); err == nil {
		switch {
		case w.LoggedIn != nil && !*w.LoggedIn:
			return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotSignedIn, Note: "Not signed in."}), nil
		case w.Username != "" || w.Email != "":
			org := ""
			if w.Team != nil {
				org = firstNonEmpty(w.Team.Name, w.Team.Slug)
			}
			return accounts.NewIdentity(accounts.IdentityFields{
				State: accounts.StateSignedIn, Email: w.Email, Login: w.Username, Name: w.Name, Org: org, Plan: w.Plan,
			}), nil
		}
	}
	text := strings.ToLower(string(res.Stdout) + "\n" + string(res.Stderr))
	switch {
	case strings.Contains(text, "logged out") || strings.Contains(text, "not logged in"):
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotSignedIn, Note: "Not signed in."}), nil
	case strings.Contains(text, "token") && (strings.Contains(text, "not valid") || strings.Contains(text, "invalid") || strings.Contains(text, "expired")):
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateExpired, Note: "The sign-in no longer works. Sign in again."}), nil
	}
	if ls := Lines(res.Stdout); res.ExitCode == 0 && len(ls) == 1 && !strings.ContainsAny(ls[0], " {}\"") {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Login: ls[0]}), nil
	}
	return accounts.Identity{}, fmt.Errorf("Vercel answered `vercel whoami` in a way Devpit does not understand (exit %d): %s", //nolint:revive,staticcheck // a product name
		res.ExitCode, firstOf(FirstLine(res.Stdout), FirstLine(res.Stderr)))
}

func (a *vercelAdapter) Login(ctx context.Context, req LoginRequest) (<-chan accounts.Event, error) {
	return folderLogin(ctx, a.deps, req, folderLoginSpec{
		tool: accounts.ToolVercel,
		check: func(ctx context.Context) (string, error) {
			ok, ver, err := a.vercelProbe(ctx)
			if err == nil && !ok {
				err = vercelTooOld(ver)
			}
			return ver, err
		},
		login: func(dir string) Cmd {
			return Cmd{Name: "vercel", Args: []string{"--global-config", dir, "login"}, Dir: dir}
		},
		whoami: a.WhoAmI,
	})
}

func (a *vercelAdapter) Plan(in accounts.PreviewInput) (accounts.Preview, error) {
	return planChecked(accounts.ToolVercel, in, func(_ *accounts.Store, acct accounts.Account, scope accounts.Scope) ([]string, error) {
		warn, err := folderAccountWarnings(accounts.ToolVercel, acct)
		if err != nil {
			return nil, err
		}
		if scope.Kind == accounts.ScopeFolder {
			if link, ok, _ := VercelProjectLink(scope.Folder); ok {
				warn = append(warn, link.Sentence(acct.Name))
			}
		}
		return warn, nil
	})
}

func (a *vercelAdapter) Apply(_ context.Context, _ *accounts.Txn, _ accounts.Preview, emit func(accounts.Event)) error {
	noSideEffects(accounts.ToolVercel, emit)
	return nil
}

// Launch is what the shim applies for one account.
func (a *vercelAdapter) Launch(acct accounts.Account) (Launch, error) { return vercelLaunch(acct) }

// vercelLaunch is the shim's view of [vercelAdapter.Launch]: no adapter, no
// command run. --global-config goes first, before the person's arguments.
func vercelLaunch(acct accounts.Account) (Launch, error) {
	if acct.IsDefault() {
		return defaultLaunch(vercelEnv()), nil
	}
	if acct.Dir == "" {
		return Launch{}, fmt.Errorf("the Vercel account %s has no folder", acct.Name)
	}
	return Launch{Args: []string{"--global-config", acct.Dir}}, nil
}

// VercelLink is a folder's link to a Vercel project, from
// .vercel\project.json. It holds ids only, no secret.
type VercelLink struct {
	// Folder is the folder whose .vercel\project.json was read.
	Folder string
	// OrgID owns the project: "team_…" for a team, otherwise a person's own
	// account.
	OrgID       string
	ProjectID   string
	ProjectName string
}

// Team reports whether the project belongs to a team rather than to one
// person's own account.
func (l VercelLink) Team() bool { return strings.HasPrefix(l.OrgID, "team_") }

// Sentence is the plain-words warning for a preview or Verify.
func (l VercelLink) Sentence(account string) string {
	owner := "a personal Vercel account (" + l.OrgID + ")"
	if l.Team() {
		owner = "the Vercel team " + l.OrgID
	}
	return fmt.Sprintf("%s is linked to a Vercel project owned by %s. If %s cannot see it, Vercel commands there will fail; Devpit does not check this with Vercel.",
		l.Folder, owner, account)
}

// VercelProjectLink reads the nearest .vercel\project.json at or above
// folder. ok is false when there is none. Only orgId, projectId and
// projectName are kept.
func VercelProjectLink(folder string) (VercelLink, bool, error) {
	if folder == "" {
		return VercelLink{}, false, errors.New("no folder given")
	}
	for dir := filepath.Clean(folder); ; {
		p := filepath.Join(dir, ".vercel", "project.json")
		data, rerr := readSmallFile(p, 64<<10)
		if rerr == nil {
			var v struct {
				OrgID       string `json:"orgId"`
				ProjectID   string `json:"projectId"`
				ProjectName string `json:"projectName"`
			}
			if jerr := json.Unmarshal(data, &v); jerr != nil || v.OrgID == "" {
				return VercelLink{}, false, fmt.Errorf("%s could not be read as a Vercel project link", p)
			}
			l := VercelLink{Folder: dir, OrgID: accounts.Scrub(v.OrgID), ProjectID: accounts.Scrub(v.ProjectID), ProjectName: accounts.Scrub(v.ProjectName)}
			return l, true, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "." || parent == "" {
			return VercelLink{}, false, nil
		}
		dir = parent
	}
}

// ---- shared by the folder-per-account tools (Vercel, Supabase) and the
// other adapters in this file set. ----

// probeResult is what one adapter learned about its tool.
type probeResult struct {
	ok      bool
	version string
	// extra is per tool (Supabase: whether `whoami` exists at all).
	extra bool
}

// probeOnce runs a probe once per adapter and keeps a successful answer.
// A probe that failed to run (a timeout) is tried again next time.
type probeOnce struct {
	mu   sync.Mutex
	done bool
	r    probeResult
}

func (p *probeOnce) get(fn func() (probeResult, error)) (probeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return p.r, nil
	}
	r, err := fn()
	if err == nil {
		p.done, p.r = true, r
	}
	return r, err
}

func versionOr(v string) string {
	if v == "" {
		return "on this PC"
	}
	return v
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// readSmallFile reads a file of at most limit bytes.
func readSmallFile(path string, limit int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%s is a folder", path)
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("%s is larger than expected (%d bytes)", path, fi.Size())
	}
	return os.ReadFile(path) // #nosec G304 -- a known, non-secret file of a tool
}

// accountFolderReady checks a named account's folder exists, so a tool is
// never pointed at a folder that is gone (it would quietly start a new,
// empty login there).
func accountFolderReady(t accounts.Tool, acct accounts.Account) error {
	if acct.Dir == "" {
		return fmt.Errorf("the %s account %s has no folder", t.DisplayName(), acct.Name)
	}
	if fi, err := os.Stat(acct.Dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("the folder for the %s account %s is missing: %s", t.DisplayName(), acct.Name, acct.Dir)
	}
	return nil
}

// dirThere reports whether p is a folder.
func dirThere(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// pathThere reports whether anything is at p. Only its presence is
// checked; nothing is opened.
func pathThere(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// folderAccountWarnings is the Plan check for a folder-per-account tool: no
// folder blocks, a missing folder warns.
func folderAccountWarnings(t accounts.Tool, acct accounts.Account) ([]string, error) {
	if acct.Dir == "" {
		return nil, fmt.Errorf("the %s account %s has no folder; sign in again", t.DisplayName(), acct.Name)
	}
	if !dirThere(acct.Dir) {
		return []string{fmt.Sprintf("The folder for %s is missing (%s). Sign in again before relying on this.", acct.Name, acct.Dir)}, nil
	}
	return nil, nil
}

// planChecked is the shared Plan: the account named in the change is looked
// up and checked (named accounts only), then accounts.BuildPreview says the
// rest in plain words.
func planChecked(t accounts.Tool, in accounts.PreviewInput, check func(*accounts.Store, accounts.Account, accounts.Scope) ([]string, error)) (accounts.Preview, error) {
	if in.Change.Tool != t {
		return accounts.Preview{}, fmt.Errorf("the %s adapter cannot plan a %s change", t.DisplayName(), in.Change.Tool)
	}
	s := in.Store
	if s == nil {
		s = accounts.NewStore()
	}
	var warn []string
	if !in.Change.Remove {
		acct, err := s.FindAccount(t, in.Change.Account)
		if err != nil {
			return accounts.Preview{}, err
		}
		if !acct.IsDefault() && check != nil {
			w, err := check(s, acct, in.Change.Scope)
			if err != nil {
				return accounts.Preview{}, err
			}
			warn = w
		}
	}
	p, err := accounts.BuildPreview(in)
	if err != nil {
		return accounts.Preview{}, err
	}
	p.Warnings = append(p.Warnings, warn...)
	return p, nil
}

// noSideEffects is Apply for a tool the shim applies at each launch.
func noSideEffects(t accounts.Tool, emit func(accounts.Event)) {
	if emit != nil {
		emit(accounts.NewEvent("Nothing to change in "+t.DisplayName()+" itself", accounts.StepDone,
			t.DisplayName()+" gets the account from the rule each time it starts through Devpit"))
	}
}

// folderLoginSpec describes a sign-in into a fresh account folder.
type folderLoginSpec struct {
	tool accounts.Tool
	// check says whether the tool can do this; it returns the version.
	check func(ctx context.Context) (string, error)
	// login is the sign-in command for the new folder.
	login func(dir string) Cmd
	// whoami checks the new folder.
	whoami func(ctx context.Context, acct accounts.Account) (accounts.Identity, error)
}

// folderLogin is Login for a tool whose account is a folder (Vercel,
// Supabase): a fresh folder under Devpit's accounts folder, the tool's own
// sign-in pointed at it, then a who-am-I. The folder is removed unless all
// of that finished.
func folderLogin(ctx context.Context, d Deps, req LoginRequest, spec folderLoginSpec) (<-chan accounts.Event, error) {
	t := spec.tool
	s := req.Store
	if s == nil {
		s = accounts.NewStore()
	}
	if err := s.CheckNewName(t, req.Name); err != nil {
		return nil, err
	}
	if d.Paths.AccountsDir == "" {
		return nil, errors.New("no folder for new accounts is set")
	}
	root := filepath.Join(d.Paths.AccountsDir, string(t))
	dir := d.Paths.AccountDir(t, req.Name)
	if _, err := os.Lstat(dir); err == nil {
		return nil, fmt.Errorf("a folder for %s already exists at %s. Pick another name", req.Name, dir)
	}
	name := t.DisplayName()

	return Stream("Signing in", func(emit func(accounts.Event)) error {
		step := func(n string, st accounts.StepState, detail string) { emit(accounts.NewEvent(n, st, detail)) }

		step("Checking "+name, accounts.StepRunning, "")
		ver, err := spec.check(ctx)
		if err != nil {
			return err
		}
		step("Checking "+name, accounts.StepDone, "version "+versionOr(ver))

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
					d.logf("could not remove the unfinished account folder %s: %v", dir, rerr)
				}
			}
		}()
		step("Creating the account folder", accounts.StepDone, dir)

		cmd := spec.login(dir)
		cmd.Timeout, cmd.Stdin, cmd.Stdout, cmd.Stderr = LoginTimeout, req.Stdin, req.Stdout, req.Stderr
		step("Opening "+name+"'s own sign-in", accounts.StepRunning, "")
		step("Waiting for you to finish signing in in the browser", accounts.StepWaiting, "")
		res, err := d.run(ctx, cmd)
		for _, l := range append(Lines(res.Stdout), Lines(res.Stderr)...) {
			step(name, accounts.StepInfo, l)
		}
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%w (stopped)", accounts.ErrSignInCancelled)
			}
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%w: %s's sign-in ended with exit code %d", accounts.ErrSignInCancelled, name, res.ExitCode)
		}

		step("Checking who is signed in", accounts.StepRunning, "")
		id, err := spec.whoami(ctx, accounts.Account{Tool: t, Name: req.Name, Dir: dir})
		if err != nil {
			return fmt.Errorf("%w: %w", accounts.ErrSignInCancelled, err)
		}
		if !id.SignedIn() {
			return fmt.Errorf("%w: %s says this folder is %s", accounts.ErrSignInCancelled, name, id.State())
		}
		ev := accounts.NewEvent("Checking who is signed in", accounts.StepDone, "Signed in as "+id.Who())
		ev.Identity = &id
		emit(ev)

		acct := storableAccount(accounts.Account{
			Tool: t, Name: req.Name, Email: id.Email(), Label: id.Login(), Org: id.Fields().Org,
			Dir: dir, Added: d.now().UTC(),
		})
		if err := accounts.NewStore().AddAccount(acct); err != nil {
			return err
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

// storableAccount drops any display field the store would refuse (an org
// name with a character a label cannot have), rather than failing a
// sign-in that worked.
func storableAccount(a accounts.Account) accounts.Account {
	if accounts.NewStore().AddAccount(a) == nil {
		return a
	}
	a.Org = ""
	if accounts.NewStore().AddAccount(a) == nil {
		return a
	}
	a.Label = ""
	if accounts.NewStore().AddAccount(a) == nil {
		return a
	}
	a.Email = ""
	return a
}
