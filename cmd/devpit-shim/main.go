// Command devpit-shim stands in for a developer tool. Devpit copies it into
// %LOCALAPPDATA%\Programs\devpit\shims as claude.exe, gh.exe, vercel.exe…
// and puts that folder first on PATH. Each call then:
//
//  1. takes the tool's name from its own file name;
//  2. reads accounts.toml and works out the account for the current folder
//     (the nearest folder rule for that tool, else "everywhere"), or the
//     account `devpit <tool> run <name>` forced for this command tree
//     (DEVPIT_ONCE);
//  3. starts the real tool (found on PATH with the shim folder skipped)
//     with that account applied to this one process, and passes its exit
//     code back.
//
// It must never get in the way: if accounts.toml cannot be read, or
// anything about the account cannot be worked out, the real tool starts
// untouched and one warning line goes to stderr.
//
// Whose answer wins: when Devpit has nothing at all for the tool (no
// account, no rule, no "everywhere"), the tool starts fully untouched. When
// it has, Devpit's answer wins over whatever the terminal holds: a named
// account sets its variable, and the default account clears it, so a
// CLAUDE_CONFIG_DIR left in the session by something else (claude-acc sets
// one for the whole PowerShell session) never quietly picks the account.
//
// gh: a named GitHub account needs its token, which gh itself holds. The
// shim asks the real gh for it (`gh auth token --user <login>`, one extra
// short run, only for a named GitHub account) and hands it to the child in
// GH_TOKEN, in memory only. If that fails, gh starts untouched with one
// warning line.
//
// Claude Code: for a named account that shares its skills with the default
// account, the shim adds the links of skills that appeared since
// (claudeshare.EnsureSkillLinks). That runs beside the tool, never before
// it, is additive only, and a failure is ignored: it can never delay or
// block the launch.
//
// It imports only the resolver, the adapters' launch table, the launcher
// and claudeshare's link check: no screens, no command-line framework, no
// HTTP client.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/claudeshare"
	"github.com/zubairbinshaukat/devpit/internal/accounts/launch"
)

// skillLinkWait bounds how long the shim waits, after the tool has exited,
// for the skill-link check it started beside the tool.
const skillLinkWait = 2 * time.Second

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run is the whole shim, minus os.Exit, so tests can call it.
func run(args []string, stderr io.Writer) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "devpit: cannot find its own program: %v\n", err)
		return 1
	}
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(self), filepath.Ext(self)))
	if name == "devpit-shim" {
		fmt.Fprintln(stderr, "devpit-shim stands in for a developer tool (claude, gh, vercel…) so the right account is used in each folder.")
		fmt.Fprintln(stderr, "Devpit makes copies of it by itself when you add a folder rule; it is not meant to be run directly.")
		return 2
	}

	tool, known := accounts.ToolForShim(name)
	if known && loopsBack(tool) {
		fmt.Fprintf(stderr, "devpit: %s keeps leading back to a Devpit shim instead of the real program. "+
			"Remove the extra copy of the shim from PATH, or run `devpit accounts verify`.\n", name)
		return 1
	}

	shimDir := filepath.Dir(self)
	find := launch.FindOptions{Skip: []string{shimDir}, Self: self}
	target, err := launch.Find(name, find)
	if err != nil {
		fmt.Fprintf(stderr, "devpit: %s is not installed (it is not on PATH outside Devpit's shim folder).\n", name)
		return 1
	}

	l := adapters.Launch{}
	var links <-chan struct{}
	if known {
		var warn string
		var acct accounts.Account
		acct, l, warn = resolve(tool, shimDir)
		if warn != "" {
			fmt.Fprintf(stderr, "devpit: %s; running %s with its own sign-in.\n", warn, name)
		}
		if tool == accounts.ToolClaude && !acct.IsDefault() && acct.Dir != "" && !l.IsZero() {
			links = ensureSkillLinks(acct)
		}
	}

	env := os.Environ()
	if !l.IsZero() {
		env = launch.MergeEnv(env, l.Env, l.Unset)
	}
	if known {
		env = launch.MergeEnv(env, []string{launch.EnvGuard + "=" + string(tool) + ":" + strconv.Itoa(os.Getpid())}, nil)
	}
	code, err := launch.Run(launch.Spec{
		Path: target,
		Args: append(append([]string(nil), l.Args...), args...),
		Env:  env,
		Find: find,
	})
	if links != nil {
		select {
		case <-links:
		case <-time.After(skillLinkWait):
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "devpit: could not start %s: %v\n", target, err)
		return 1
	}
	return code
}

// resolve works out what to apply for tool in the current folder. Anything
// that goes wrong, a panic included, comes back as a warning and the zero
// Launch: the tool then starts untouched.
func resolve(tool accounts.Tool, shimDir string) (acct accounts.Account, l adapters.Launch, warn string) {
	defer func() {
		if r := recover(); r != nil {
			acct, l, warn = accounts.Account{}, adapters.Launch{}, fmt.Sprintf("an internal error stopped the account check (%v)", r)
		}
	}()
	path, err := accounts.StorePath()
	if err != nil {
		return acct, l, "cannot find the accounts file"
	}
	s, err := accounts.ReadFile(path)
	if err != nil {
		return acct, l, "cannot read " + path + " (" + firstLine(err.Error()) + ")"
	}
	if once, ok := forcedAccount(tool); ok {
		a, found := s.Account(tool, once)
		if !found {
			return acct, l, "`devpit " + string(tool) + " run` named the account " + once + ", which no longer exists"
		}
		acct = a
	} else {
		if !s.Manages(tool) {
			return acct, l, ""
		}
		cwd, gerr := os.Getwd()
		if gerr != nil {
			return acct, l, "cannot tell which folder this is"
		}
		res, rerr := accounts.Resolve(s, tool, cwd, accounts.ResolveOptions{RealPath: accounts.RealPath})
		if rerr != nil {
			return acct, l, "cannot check the folder rules here (" + firstLine(rerr.Error()) + ")"
		}
		if len(res.Problems) > 0 {
			warn = firstLine(res.Problems[0].Message)
		}
		acct = res.Account
	}
	// LaunchLive runs nothing except for a named GitHub account, whose token
	// it asks the real gh for (skipping this shim folder).
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	l, err = adapters.LaunchLive(ctx, adapters.ExecRunner{ShimDir: shimDir}, acct)
	if err != nil {
		return accounts.Account{}, adapters.Launch{}, "cannot use the account " + acct.Name + " (" + firstLine(err.Error()) + ")"
	}
	return acct, l, warn
}

// forcedAccount is the account `devpit <tool> run <name>` set for this
// command tree, if it names this tool.
func forcedAccount(tool accounts.Tool) (string, bool) {
	t, name, ok := strings.Cut(os.Getenv(launch.EnvOnce), ":")
	if !ok || t != string(tool) || name == "" {
		return "", false
	}
	return name, true
}

// ensureSkillLinks adds the links of skills that appeared in the default
// account since, beside the tool. The channel closes when it is done. Any
// failure, a panic included, is dropped: the tool runs either way.
func ensureSkillLinks(acct accounts.Account) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		r := claudeshare.RootsFor(home, acct)
		if p, err := accounts.DefaultPaths(); err == nil {
			r.Lock = p.Lock
		}
		_, _ = claudeshare.EnsureSkillLinks(r)
	}()
	return done
}

// loopsBack reports whether this shim was started by a shim for the same
// tool, which means PATH leads from the shim back to a shim.
func loopsBack(tool accounts.Tool) bool {
	v := os.Getenv(launch.EnvGuard)
	t, pid, ok := strings.Cut(v, ":")
	if !ok || t != string(tool) {
		return false
	}
	return pid == strconv.Itoa(os.Getppid())
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimRight(strings.TrimSpace(s), ".")
}
