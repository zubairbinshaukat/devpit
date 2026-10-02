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
)

// Supabase's variables, as the current CLI's TypeScript front end reads
// them (spike, CLI 2.119.0).
const (
	supabaseHome      = "SUPABASE_HOME"
	supabaseNoKeyring = "SUPABASE_NO_KEYRING"
	supabaseToken     = "SUPABASE_ACCESS_TOKEN"
)

// supabaseAdapter is the Supabase CLI:
//
//   - an account is a folder under %USERPROFILE%\.devpit\accounts\supabase.
//     The shim sets SUPABASE_HOME=<folder> and SUPABASE_NO_KEYRING=1 (exactly
//     "1") for the one process, so the CLI keeps that account's login in
//     <folder>\access-token instead of Windows Credential Manager. That file
//     is a plain-text token: Devpit never opens it (it only checks it is
//     there), and the folder is protected from Clean because every account
//     folder in accounts.toml is (accounts.ProtectedRoots);
//   - this only works on a CLI whose login honours SUPABASE_NO_KEYRING. The
//     older built-in Go code path has no such switch and would use Credential
//     Manager whatever SUPABASE_HOME says. So Capabilities probes the real
//     CLI once: in an empty temporary SUPABASE_HOME with the switch set,
//     `supabase whoami --output-format json` must answer
//     AccessTokenRequiredError. Anything else and Supabase is show-only;
//   - SUPABASE_ACCESS_TOKEN wins over everything. The project's rule is
//     that only a tool's own folder variable is ever cleared for the child,
//     never a token variable, so the shim leaves it alone and Verify flags
//     it (EnvOverrides). Devpit's own checks (whoami, the probe, sign-in)
//     run without it, so they describe the folder, not the token;
//   - the default account is Supabase's own login in Credential Manager,
//     untouched.
type supabaseAdapter struct {
	base
	probe probeOnce
}

func newSupabase(d Deps) Adapter {
	return &supabaseAdapter{base: base{
		deps: d, tool: accounts.ToolSupabase, bin: "supabase",
		why:  "Devpit could not check what this Supabase CLI supports.",
		envs: supabaseEnv(),
	}}
}

// supabaseEnv lists the variables that decide or override Supabase's
// account. None is Managed: Verify reports them, nothing clears them.
func supabaseEnv() []EnvVar {
	return []EnvVar{
		{Name: supabaseToken, Effect: "Supabase uses this token before any signed-in account, including the one Devpit picks."},
		{Name: supabaseHome, Effect: "Supabase started any other way than through Devpit reads its sign-in from this folder."},
	}
}

func (a *supabaseAdapter) Installed(ctx context.Context) Install {
	return installedOrNpx(ctx, a.deps, accounts.ToolSupabase, "supabase", "supabase")
}

// supabaseProbe runs whoami in an empty temporary home with the keyring
// switched off. ok: the CLI looked only in that folder (separate logins
// work). extra: `supabase whoami` exists at all.
func (a *supabaseAdapter) supabaseProbe(ctx context.Context) (probeResult, error) {
	return a.probe.get(func() (probeResult, error) {
		in := a.Installed(ctx)
		if !in.Found {
			return probeResult{}, fmt.Errorf("Supabase: %w", accounts.ErrNotFoundTool) //nolint:revive,staticcheck // a product name
		}
		tmp, err := os.MkdirTemp("", "dp-sb-probe-*")
		if err != nil {
			return probeResult{version: in.Version}, err
		}
		defer os.RemoveAll(tmp) //nolint:errcheck // a temporary folder
		res, err := a.deps.run(ctx, Cmd{
			Name: "supabase", Args: []string{"whoami", "--output-format", "json"},
			Env:   []string{supabaseHome + "=" + tmp, supabaseNoKeyring + "=1", "DO_NOT_TRACK=1"},
			Unset: []string{supabaseToken}, Dir: tmp, Timeout: 30 * time.Second,
		})
		if err != nil {
			return probeResult{version: in.Version}, err
		}
		text := string(res.Stdout) + "\n" + string(res.Stderr)
		r := probeResult{version: in.Version}
		r.ok = strings.Contains(text, "AccessTokenRequiredError")
		var w supabaseWhoami
		r.extra = r.ok || (DecodeJSON(res.Stdout, &w) == nil && (w.Tag != "" || w.Email != "" || w.ID != ""))
		return r, nil
	})
}

func supabaseShowOnlyWhy(r probeResult) string {
	if !r.extra {
		return fmt.Sprintf("Supabase CLI %s has no `supabase whoami` and does not keep a separate sign-in per folder, so Devpit shows it but cannot switch it. Update the Supabase CLI (2.118.0 or later has both).",
			versionOr(r.version))
	}
	return fmt.Sprintf("Supabase CLI %s does not keep a separate sign-in per folder (it ignores SUPABASE_NO_KEYRING and uses Windows Credential Manager), so Devpit shows it but cannot switch it. Update the Supabase CLI.",
		versionOr(r.version))
}

func (a *supabaseAdapter) Capabilities(ctx context.Context) Caps {
	r, err := a.supabaseProbe(ctx)
	switch {
	case errors.Is(err, accounts.ErrNotFoundTool):
		return Caps{Why: "Supabase CLI is not installed."}
	case err != nil:
		return Caps{Why: "Devpit could not ask the Supabase CLI what it supports: " + accounts.Scrub(err.Error())}
	case !r.ok:
		return Caps{ShowOnly: true, Why: supabaseShowOnlyWhy(r)}
	}
	return Caps{FolderRules: true, Everywhere: true, JustOnce: true, AddAccount: true}
}

// LiveCheckRisk: `supabase whoami` reads the stored token and asks
// Supabase who it is. Supabase tokens do not refresh, so asking cannot spend
// one.
func (a *supabaseAdapter) LiveCheckRisk() (bool, string) { return false, "" }

// supabaseWhoami is `supabase whoami --output-format json`: {"id","email",
// "username"} when signed in, {"_tag":"Error","error":{"code":…}} when not.
type supabaseWhoami struct {
	Tag      string `json:"_tag"`
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	Error    struct {
		Code string `json:"code"`
	} `json:"error"`
}

func (a *supabaseAdapter) WhoAmI(ctx context.Context, acct accounts.Account) (accounts.Identity, error) {
	cmd := Cmd{Name: "supabase", Args: []string{"whoami", "--output-format", "json"}, Dir: a.deps.Home, Timeout: 30 * time.Second}
	if !acct.IsDefault() {
		if err := accountFolderReady(accounts.ToolSupabase, acct); err != nil {
			return accounts.NewIdentity(accounts.IdentityFields{Note: err.Error()}), err
		}
		cmd.Env = supabaseLaunchEnv(acct.Dir)
		cmd.Unset = []string{supabaseToken}
		cmd.Dir = acct.Dir
	}
	r, err := a.supabaseProbe(ctx)
	if errors.Is(err, accounts.ErrNotFoundTool) {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotInstalled}), err
	}
	if err != nil {
		return accounts.Identity{}, accounts.ScrubError(err)
	}
	if !r.extra || (!r.ok && !acct.IsDefault()) {
		e := fmt.Errorf("%s Devpit cannot check who is signed in: %w", supabaseShowOnlyWhy(r), accounts.ErrTooOld)
		return accounts.NewIdentity(accounts.IdentityFields{Note: "Devpit cannot check this with this Supabase CLI."}), e
	}
	res, err := a.deps.run(ctx, cmd)
	if err != nil {
		return accounts.Identity{}, accounts.ScrubError(err)
	}
	var w supabaseWhoami
	if err := DecodeJSON(res.Stdout, &w); err != nil {
		if err2 := DecodeJSON(res.Stderr, &w); err2 != nil {
			return accounts.Identity{}, fmt.Errorf("Supabase answered `supabase whoami` in a way Devpit does not understand (exit %d): %s", //nolint:revive,staticcheck // a product name
				res.ExitCode, firstOf(FirstLine(res.Stdout), FirstLine(res.Stderr)))
		}
	}
	if w.Tag != "Error" && (w.Email != "" || w.Username != "" || w.ID != "") {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Email: w.Email, Login: w.Username}), nil
	}
	if w.Error.Code == "AccessTokenRequiredError" {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotSignedIn, Note: "Not signed in."}), nil
	}
	// A token is stored but Supabase refused it: expired or revoked. For a
	// named account the file is only checked for, never opened.
	if !acct.IsDefault() {
		if !pathThere(filepath.Join(acct.Dir, "access-token")) {
			return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateNotSignedIn, Note: "Not signed in."}), nil
		}
	}
	return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateExpired, Note: "The sign-in no longer works. Sign in again."}), nil
}

func (a *supabaseAdapter) Login(ctx context.Context, req LoginRequest) (<-chan accounts.Event, error) {
	return folderLogin(ctx, a.deps, req, folderLoginSpec{
		tool: accounts.ToolSupabase,
		check: func(ctx context.Context) (string, error) {
			r, err := a.supabaseProbe(ctx)
			if err == nil && !r.ok {
				err = fmt.Errorf("%s: %w", supabaseShowOnlyWhy(r), accounts.ErrTooOld)
			}
			return r.version, err
		},
		login: func(dir string) Cmd {
			return Cmd{Name: "supabase", Args: []string{"login"}, Env: supabaseLaunchEnv(dir), Unset: []string{supabaseToken}, Dir: dir}
		},
		whoami: a.WhoAmI,
	})
}

func (a *supabaseAdapter) Plan(in accounts.PreviewInput) (accounts.Preview, error) {
	return planChecked(accounts.ToolSupabase, in, func(_ *accounts.Store, acct accounts.Account, _ accounts.Scope) ([]string, error) {
		return folderAccountWarnings(accounts.ToolSupabase, acct)
	})
}

func (a *supabaseAdapter) Apply(_ context.Context, _ *accounts.Txn, _ accounts.Preview, emit func(accounts.Event)) error {
	noSideEffects(accounts.ToolSupabase, emit)
	return nil
}

// Launch is what the shim applies for one account.
func (a *supabaseAdapter) Launch(acct accounts.Account) (Launch, error) { return supabaseLaunch(acct) }

// supabaseLaunch is the shim's view of [supabaseAdapter.Launch]: no adapter, no
// command run. SUPABASE_ACCESS_TOKEN is not cleared (see the type's
// comment); Verify flags it.
func supabaseLaunch(acct accounts.Account) (Launch, error) {
	if acct.IsDefault() {
		return defaultLaunch(supabaseEnv()), nil
	}
	if acct.Dir == "" {
		return Launch{}, fmt.Errorf("the Supabase account %s has no folder", acct.Name)
	}
	return Launch{Env: supabaseLaunchEnv(acct.Dir)}, nil
}

func supabaseLaunchEnv(dir string) []string {
	return []string{supabaseHome + "=" + dir, supabaseNoKeyring + "=1"}
}
