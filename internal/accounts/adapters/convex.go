package adapters

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// convexAdapter is Convex: show and verify only, by design (plan 3.2, 13).
// Convex picks the account per project: `npx convex dev` writes the
// deployment it uses into the project's .env.local as CONVEX_DEPLOYMENT,
// with the team and project in a comment. Only an undocumented token
// variable could switch the signed-in person, which would break Devpit's
// no-token rule, and Convex is normally run through npx, which cannot be
// shimmed. So Devpit has no Convex accounts, never switches, and shows
// which project a folder uses from that one line ([ConvexProject]).
//
// Reading .env.local is careful because it holds secrets: it is parsed line
// by line, only the CONVEX_DEPLOYMENT line is kept, CONVEX_DEPLOY_KEY is
// reported as present or absent (never its value), and nothing else from
// the file reaches an event, an error or a log.
//
// There is no live check: `npx convex login status` may download Convex,
// and the global login it reads (~/.convex/config.json) holds a token.
type convexAdapter struct{ base }

// convexWhy is the sentence every Convex screen shows.
const convexWhy = "Convex picks the account per project. Devpit shows it but does not switch it."

func newConvex(d Deps) Adapter {
	return &convexAdapter{base{
		deps: d, tool: accounts.ToolConvex, bin: "convex",
		why:  convexWhy,
		envs: convexEnv(),
	}}
}

// convexEnv lists the variables that decide Convex's account or deployment.
// Reported by Verify, never cleared.
func convexEnv() []EnvVar {
	return []EnvVar{
		{Name: "CONVEX_DEPLOY_KEY", Effect: "Convex uses this deploy key instead of your login and the project's .env.local."},
		{Name: "CONVEX_DEPLOYMENT", Effect: "Convex uses this deployment instead of the one in the project's .env.local."},
		{Name: "CONVEX_OVERRIDE_ACCESS_TOKEN", Effect: "Convex uses this token instead of your login."},
	}
}

// Installed finds a global convex, or else npx, which is how Convex is
// normally run. npx is never asked for Convex's version: that could
// download Convex.
func (a *convexAdapter) Installed(ctx context.Context) Install {
	if in := FindInstalled(ctx, a.deps, "convex"); in.Found {
		in.Note = "Installed on PATH, but Convex still picks the account per project."
		return in
	}
	res, err := a.deps.run(ctx, Cmd{Name: "npx", Args: []string{"--version"}})
	if errors.Is(err, accounts.ErrNotFoundTool) {
		return Install{}
	}
	return Install{
		Found: err == nil, Path: res.Path,
		Note: "Used through npx, which Devpit cannot shim; the version depends on each project.",
	}
}

// Capabilities says Convex is show-only.
func (a *convexAdapter) Capabilities(context.Context) Caps {
	return Caps{ShowOnly: true, Why: convexWhy}
}

// Cached has nothing to show without a folder: Convex's account belongs to
// each project. Screens call ConvexProject for the folder they show.
func (a *convexAdapter) Cached(*accounts.Store, accounts.Account) accounts.Identity {
	return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateUnknown, Note: "Set by each project's .env.local."})
}

// WhoAmI is not run for Convex; see the type's comment.
func (a *convexAdapter) WhoAmI(context.Context, accounts.Account) (accounts.Identity, error) {
	return accounts.NewIdentity(accounts.IdentityFields{Note: "Devpit reads the project's .env.local instead of asking Convex."}),
		NotSupported(accounts.ToolConvex, "checking who is signed in (Devpit reads the project's .env.local instead)")
}

// Launch is what the shim applies for one account. Convex is never shimmed.
func (a *convexAdapter) Launch(acct accounts.Account) (Launch, error) { return convexLaunch(acct) }

// convexLaunch is the shim's view of [convexAdapter.Launch].
func convexLaunch(acct accounts.Account) (Launch, error) {
	if acct.IsDefault() {
		return defaultLaunch(convexEnv()), nil
	}
	return Launch{}, NotSupported(accounts.ToolConvex, "switching accounts")
}

// ConvexInfo is what a project's env file says about Convex. It never holds
// a secret.
type ConvexInfo struct {
	// Found is set when a CONVEX_DEPLOYMENT line was found.
	Found bool
	// File is the env file it came from.
	File string
	// Deployment is CONVEX_DEPLOYMENT's value ("dev:happy-animal-123").
	Deployment string
	// Team and Project come from the comment Convex writes after it
	// ("# team: acme, project: chat-app"), when present.
	Team, Project string
	// DeployKey is set when the same file sets CONVEX_DEPLOY_KEY. Only its
	// presence is known; the value is never read into memory past the
	// line's own parse.
	DeployKey bool
}

// Why is the Accounts page's "why" column for Convex.
func (c ConvexInfo) Why() string {
	if !c.Found {
		return "no Convex project here"
	}
	return "set by this project's " + filepath.Base(c.File)
}

// Identity is how a screen shows the project: "project: <name>" (or the
// deployment), the team as the org, and why.
func (c ConvexInfo) Identity() accounts.Identity {
	if !c.Found {
		return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateUnknown, Note: "No Convex project here (no CONVEX_DEPLOYMENT in .env.local or .env)."})
	}
	who := c.Project
	if who == "" {
		who = c.Deployment
	}
	note := c.Why()
	if c.DeployKey {
		note += "; it also sets CONVEX_DEPLOY_KEY, which Convex uses instead of your login"
	}
	return accounts.NewIdentity(accounts.IdentityFields{
		State: accounts.StateRecorded, Name: "project: " + who, Org: c.Team, Note: note,
	})
}

// ConvexProject looks for the Convex project folder uses: the nearest
// .env.local, then .env, at or above folder that sets CONVEX_DEPLOYMENT. It
// stops at the first folder that looks like a project root (package.json or
// .git) so a parent project is not mistaken for this one. Found is false
// when there is none; err only for a file that exists but cannot be read.
func ConvexProject(folder string) (ConvexInfo, error) {
	if folder == "" {
		return ConvexInfo{}, errors.New("no folder given")
	}
	for dir := filepath.Clean(folder); ; {
		for _, name := range []string{".env.local", ".env"} {
			p := filepath.Join(dir, name)
			info, err := readConvexEnv(p)
			if err != nil {
				return ConvexInfo{}, err
			}
			if info.Found {
				return info, nil
			}
		}
		if isProjectRoot(dir) {
			return ConvexInfo{}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "." || parent == "" {
			return ConvexInfo{}, nil
		}
		dir = parent
	}
}

func isProjectRoot(dir string) bool {
	for _, n := range []string{"package.json", ".git"} {
		if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
			return true
		}
	}
	return false
}

// readConvexEnv reads one env file, keeping only what ConvexInfo holds. A
// missing file is no info and no error. Errors name the file, never its
// content.
func readConvexEnv(path string) (ConvexInfo, error) {
	data, err := readSmallFile(path, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return ConvexInfo{}, nil
	}
	if err != nil {
		return ConvexInfo{}, fmt.Errorf("%s could not be read", path)
	}
	return parseConvexEnv(path, data), nil
}

// parseConvexEnv keeps CONVEX_DEPLOYMENT (and the team/project comment
// after it) and whether CONVEX_DEPLOY_KEY is set. Every other line is
// skipped without being kept.
func parseConvexEnv(path string, data []byte) ConvexInfo {
	info := ConvexInfo{File: path}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "CONVEX_DEPLOY_KEY":
			v, _ := envValue(val)
			info.DeployKey = v != ""
		case "CONVEX_DEPLOYMENT":
			v, comment := envValue(val)
			if !validDeployment(v) {
				continue
			}
			info.Found, info.Deployment = true, v
			info.Team, info.Project = convexComment(comment)
		}
	}
	return info
}

// envValue splits `value # comment`, unquoting a quoted value.
func envValue(raw string) (value, comment string) {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') {
		if end := strings.IndexByte(s[1:], s[0]); end >= 0 {
			rest := strings.TrimSpace(s[end+2:])
			_, c, _ := strings.Cut(rest, "#")
			return s[1 : end+1], strings.TrimSpace(c)
		}
	}
	v, c, _ := strings.Cut(s, "#")
	return strings.TrimSpace(v), strings.TrimSpace(c)
}

// validDeployment accepts "<kind>:<name>" with a short name of letters,
// digits and dashes, and nothing that looks like a secret.
func validDeployment(v string) bool {
	kind, name, ok := strings.Cut(v, ":")
	if !ok || kind == "" || name == "" || len(v) > 96 || accounts.LooksSecret(v) {
		return false
	}
	for _, r := range kind + name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// convexComment reads "team: acme, project: chat-app".
func convexComment(c string) (team, project string) {
	for _, part := range strings.Split(c, ",") {
		k, v, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" || len(v) > 64 || accounts.LooksSecret(v) || strings.ContainsAny(v, "\"'<>") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "team":
			team = v
		case "project":
			project = v
		}
	}
	return team, project
}

// installedOrNpx is Installed for a tool that may be reachable only through
// npx: it is found on PATH (outside the shim folder) and asked for its
// version, or, when it is not on PATH but npx is, the note says plainly that
// Devpit cannot pick its account there.
func installedOrNpx(ctx context.Context, d Deps, t accounts.Tool, bin, npmPkg string) Install {
	in := FindInstalled(ctx, d, bin)
	if in.Found {
		return in
	}
	if _, err := d.lookPath("npx"); err == nil {
		in.Note = fmt.Sprintf("%s is not installed on PATH. If you run it with npx inside projects, Devpit cannot choose its account there: only a program on PATH can go through Devpit. Install it with `npm install -g %s` to use Devpit's rules.",
			t.DisplayName(), npmPkg)
	}
	return in
}
