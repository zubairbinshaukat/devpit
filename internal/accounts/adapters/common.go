package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/launch"
)

// Deps is what every adapter is built with. Nothing in it is global; tests
// build their own.
type Deps struct {
	// Runner runs the tools.
	Runner Runner
	// Paths are the accounts files and the folder new accounts go in.
	Paths accounts.Paths
	// Home is the person's home folder (where ~/.claude is).
	Home string
	// Now is time.Now unless a test sets it.
	Now func() time.Time
	// Log, when set, gets a line per step. Lines are scrubbed first.
	Log func(string)
	// ShimDir is Devpit's shim folder, skipped when looking for a tool.
	ShimDir string
	// LookPath finds a tool without running it; launch.Find outside
	// ShimDir when nil.
	LookPath func(name string) (string, error)
	// Getenv reads this process's environment; os.Getenv when nil.
	Getenv func(string) string
}

// DefaultDeps returns the real dependencies: real paths, the real runner
// skipping shimDir, and no log.
func DefaultDeps(shimDir string) (Deps, error) {
	p, err := accounts.DefaultPaths()
	if err != nil {
		return Deps{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Deps{}, err
	}
	return Deps{Runner: ExecRunner{ShimDir: shimDir}, Paths: p, Home: home, Now: time.Now, ShimDir: shimDir}, nil
}

// lookPath finds a tool on PATH, outside the shim folder, without running
// it.
func (d Deps) lookPath(name string) (string, error) {
	if d.LookPath != nil {
		return d.LookPath(name)
	}
	opts := launch.FindOptions{}
	if d.ShimDir != "" {
		opts.Skip = []string{d.ShimDir}
	}
	return launch.Find(name, opts)
}

func (d Deps) getenv(k string) string {
	if d.Getenv != nil {
		return d.Getenv(k)
	}
	return os.Getenv(k)
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) logf(format string, args ...any) {
	if d.Log != nil {
		d.Log(accounts.Scrub(fmt.Sprintf(format, args...)))
	}
}

// Version is a dotted version number.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Compare returns -1, 0 or 1.
func (v Version) Compare(o Version) int {
	for _, d := range [3]int{v.Major - o.Major, v.Minor - o.Minor, v.Patch - o.Patch} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	return 0
}

// AtLeast reports whether v >= min (min like "2.40" or "2.118.0").
func (v Version) AtLeast(minimum string) bool {
	m, ok := ParseVersion(minimum)
	return ok && v.Compare(m) >= 0
}

// ParseVersion finds the first dotted number (at least major.minor) in s:
// "2.1.287 (Claude Code)", "gh version 2.102.0 (2026-09-30)", "git version
// 2.55.0.windows.1", "Vercel CLI 60.1.3".
func ParseVersion(s string) (Version, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' || (i > 0 && (isDigit(s[i-1]) || s[i-1] == '.')) {
			continue
		}
		j := i
		for j < len(s) && (isDigit(s[j]) || s[j] == '.') {
			j++
		}
		parts := strings.Split(strings.Trim(s[i:j], "."), ".")
		if len(parts) < 2 {
			i = j
			continue
		}
		var v Version
		nums := []*int{&v.Major, &v.Minor, &v.Patch}
		for k := 0; k < len(parts) && k < 3; k++ {
			n, err := strconv.Atoi(parts[k])
			if err != nil {
				return Version{}, false
			}
			*nums[k] = n
		}
		return v, true
	}
	return Version{}, false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// DecodeJSON decodes the first JSON object in out, skipping any banner a
// tool prints before it. Unknown fields are ignored.
func DecodeJSON(out []byte, v any) error {
	i := bytes.IndexByte(out, '{')
	j := bytes.LastIndexByte(out, '}')
	if i < 0 || j < i {
		return errors.New("no JSON object in the output")
	}
	return json.Unmarshal(out[i:j+1], v)
}

// Lines splits output into trimmed, non-empty lines.
func Lines(b []byte) []string {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(strings.TrimRight(l, "\r")); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// FirstLine is the first non-empty line, scrubbed, for a message.
func FirstLine(b []byte) string {
	if ls := Lines(b); len(ls) > 0 {
		return accounts.Scrub(ls[0])
	}
	return ""
}

// After returns what follows prefix on the first line that starts with it
// (ignoring case and leading space), trimmed.
func After(out []byte, prefix string) (string, bool) {
	lp := strings.ToLower(prefix)
	for _, l := range Lines(out) {
		if strings.HasPrefix(strings.ToLower(l), lp) {
			return strings.TrimSpace(l[len(prefix):]), true
		}
	}
	return "", false
}

// run is a Cmd through the deps' runner.
func (d Deps) run(ctx context.Context, c Cmd) (Result, error) {
	if d.Runner == nil {
		return Result{}, errors.New("no command runner")
	}
	return d.Runner.Run(ctx, c)
}

// FindInstalled is the shared Installed: run "<bin> <versionArgs>" and parse
// the version. A tool that is not on PATH is Found: false.
func FindInstalled(ctx context.Context, d Deps, bin string, versionArgs ...string) Install {
	if len(versionArgs) == 0 {
		versionArgs = []string{"--version"}
	}
	res, err := d.run(ctx, Cmd{Name: bin, Args: versionArgs, Timeout: DefaultTimeout})
	if errors.Is(err, accounts.ErrNotFoundTool) {
		return Install{}
	}
	in := Install{Found: true, Path: res.Path}
	if err != nil {
		in.Note = accounts.Scrub(err.Error())
		return in
	}
	if v, ok := ParseVersion(string(res.Stdout) + "\n" + string(res.Stderr)); ok {
		in.Version = v.String()
	}
	return in
}

// DefaultAndStore is the shared Accounts: "default" then the store's.
func DefaultAndStore(t accounts.Tool, s *accounts.Store) []accounts.Account {
	out := []accounts.Account{{Tool: t, Name: accounts.DefaultName}}
	if s != nil {
		out = append(out, s.AccountsFor(t)...)
	}
	return out
}

// CachedIdentity is the shared Cached: the email, label and org recorded
// for the account, or for the default account the email recorded in
// [default_email]. State is StateRecorded when anything is known.
func CachedIdentity(s *accounts.Store, acct accounts.Account) accounts.Identity {
	if acct.IsDefault() {
		if s != nil && s.DefaultEmail[acct.Tool] != "" {
			return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateRecorded, Email: s.DefaultEmail[acct.Tool]})
		}
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateUnknown, Note: "Not checked yet."})
	}
	if s != nil {
		if stored, ok := s.Account(acct.Tool, acct.Name); ok {
			acct = stored
		}
	}
	if acct.Email == "" && acct.Label == "" {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateUnknown, Note: "Not checked yet."})
	}
	return accounts.NewIdentity(accounts.IdentityFields{
		State: accounts.StateRecorded, Email: acct.Email, Login: acct.Label, Org: acct.Org,
	})
}

// CheckEnv reports each of the tool's override variables that is set in
// this environment by something other than Devpit. A Managed variable that
// a Devpit shim set for this very process tree (DEVPIT_SHIM_GUARD names the
// tool) is Devpit's own and is not reported.
func CheckEnv(t accounts.Tool, getenv func(string) string) []accounts.Problem {
	if getenv == nil {
		getenv = os.Getenv
	}
	ours := strings.HasPrefix(getenv(launch.EnvGuard), string(t)+":")
	var out []accounts.Problem
	for _, v := range EnvOverridesFor(t) {
		val := getenv(v.Name)
		if val == "" || (v.Managed && ours) {
			continue
		}
		msg := fmt.Sprintf("%s is set in this terminal by something other than Devpit. %s", v.Name, v.Effect)
		if v.Managed {
			msg = fmt.Sprintf("%s is set in this terminal by something other than Devpit (to %s). %s", v.Name, accounts.Scrub(val), v.Effect)
		}
		fix := fmt.Sprintf("If you did not mean to set it, remove it here with `Remove-Item Env:%s` (PowerShell) or `set %s=` (cmd), and from wherever sets it (a PowerShell profile, the system environment), then open a new terminal.", v.Name, v.Name)
		out = append(out, accounts.Problem{Kind: accounts.ProblemEnvOverride, Tool: t, Variable: v.Name, Message: msg, Fix: fix})
	}
	return out
}

// NotSupported builds the error for something a tool's adapter does not
// do (yet), naming the tool.
func NotSupported(t accounts.Tool, what string) error {
	return fmt.Errorf("%s: %s: %w", t.DisplayName(), what, accounts.ErrNotSupported)
}

// Stream runs fn on its own goroutine and returns the channel it emits on.
// The channel is closed when fn returns; if fn's last event was not Final,
// a final event is added (a failure if fn returned an error).
func Stream(step string, fn func(emit func(accounts.Event)) error) <-chan accounts.Event {
	ch := make(chan accounts.Event, 16)
	go func() {
		defer close(ch)
		final := false
		emit := func(ev accounts.Event) {
			final = ev.Final
			ch <- ev
		}
		err := fn(emit)
		switch {
		case err != nil && !final:
			ch <- accounts.Failed(step, err)
		case !final:
			ev := accounts.NewEvent(step, accounts.StepDone, "")
			ev.Final = true
			ch <- ev
		}
	}()
	return ch
}

// removeFresh deletes a folder Devpit itself created for an account that
// was never finished. It refuses anything outside root, root itself, and a
// link (the link would be removed, not followed, but a link here means
// something else made it).
func removeFresh(dir, root string) error {
	if root == "" || accounts.SameFolder(dir, root) || !accounts.FolderContains(root, dir) {
		return fmt.Errorf("refusing to remove %s: it is not a folder Devpit made under %s", dir, root)
	}
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || fi.Mode()&os.ModeIrregular != 0 || !fi.IsDir() {
		return fmt.Errorf("refusing to remove %s: it is not a plain folder", dir)
	}
	return os.RemoveAll(filepath.Clean(dir))
}

// base is the shared adapter for a tool Devpit does not switch yet: it finds
// the tool and its version, lists default plus the store's accounts, and
// says "not yet" to everything else in plain words. A tool's own file embeds
// it and overrides methods as they are filled in.
type base struct {
	deps Deps
	tool accounts.Tool
	bin  string
	why  string
	envs []EnvVar
}

func (b *base) Cached(s *accounts.Store, acct accounts.Account) accounts.Identity {
	return CachedIdentity(s, acct)
}

// LiveCheckRisk is "no risk known" for a tool whose live check is not
// built yet.
func (b *base) LiveCheckRisk() (bool, string) { return false, "" }

func (b *base) EnvOverrides() []EnvVar { return b.envs }

func (b *base) Tool() accounts.Tool { return b.tool }

func (b *base) Installed(ctx context.Context) Install { return FindInstalled(ctx, b.deps, b.bin) }

func (b *base) Capabilities(context.Context) Caps { return Caps{Why: b.why} }

func (b *base) Accounts(_ context.Context, s *accounts.Store) ([]accounts.Account, error) {
	return DefaultAndStore(b.tool, s), nil
}

func (b *base) WhoAmI(context.Context, accounts.Account) (accounts.Identity, error) {
	return accounts.Identity{}, NotSupported(b.tool, "checking who is signed in")
}

func (b *base) Login(context.Context, LoginRequest) (<-chan accounts.Event, error) {
	return nil, NotSupported(b.tool, "signing in another account")
}

func (b *base) Plan(accounts.PreviewInput) (accounts.Preview, error) {
	return accounts.Preview{}, NotSupported(b.tool, "switching accounts")
}

func (b *base) Apply(context.Context, *accounts.Txn, accounts.Preview, func(accounts.Event)) error {
	return NotSupported(b.tool, "switching accounts")
}

func (b *base) Launch(acct accounts.Account) (Launch, error) {
	if acct.IsDefault() {
		return defaultLaunch(b.envs), nil
	}
	return Launch{}, NotSupported(b.tool, "switching accounts")
}

// defaultLaunch clears the Managed variables: the default account means
// "as if nothing had picked another one".
func defaultLaunch(envs []EnvVar) Launch {
	var l Launch
	for _, v := range envs {
		if v.Managed {
			l.Unset = append(l.Unset, v.Name)
		}
	}
	return l
}
