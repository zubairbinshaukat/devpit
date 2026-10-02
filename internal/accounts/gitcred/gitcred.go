// Package gitcred is the engine behind the hidden `devpit git-credential
// <get|store|erase>` command: Git's credential helper for github.com while
// Devpit manages GitHub pushes.
//
// The GitHub adapter writes, in Devpit's rules.gitconfig:
//
//	[credential "https://github.com"]
//	    helper =
//	    helper = "!\"C:/…/devpit.exe\" git-credential"
//
// The empty value drops every helper set before it (Git Credential Manager
// from the system config, for one), so this is the only helper Git runs
// for github.com. The chain that was there before is recorded in
// devpit.json, and this package hands the default account to it.
//
// Per request:
//
//   - Only https://github.com is answered. Anything else passes through.
//   - The folder is the repository Git works on: GIT_DIR when set (during
//     `git clone` the working folder is the caller's, and only GIT_DIR
//     names the new repo), else the working folder. The nearest GitHub
//     rule for it decides the account, unless DEVPIT_GITHUB_ACCOUNT names
//     one (gh, or `devpit github run`, set it).
//   - A named account: `get` answers username=<login> and the token from
//     `gh auth token --user <login>`. `store` and `erase` do nothing: gh
//     owns that token, and handing either to the old chain would make Git
//     Credential Manager save the work token as the default one, or wipe
//     the default one. After a rejected token Git calls `erase` once and
//     stops; it never loops, and a line on stderr says to sign in again.
//   - The default account: get, store and erase all go to the recorded
//     chain, through `git credential fill|approve|reject` with that chain
//     as the only helpers, and the answer is relayed.
//   - Anything in Devpit failing falls back to the recorded chain, so a
//     push keeps working as it did before Devpit. stdout carries the
//     credential protocol and nothing else; Devpit's own errors are one
//     plain line on stderr.
package gitcred

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/shims"
)

// EnvActive is set for the Git that a pass-through starts, so a helper
// started by it can never answer through Devpit again.
const EnvActive = "DEVPIT_GITCRED_ACTIVE"

// maxInput bounds what is read from Git.
const maxInput = 64 << 10

// Deps is what Serve needs; DefaultDeps gives the real ones.
type Deps struct {
	// Runner runs gh (the token) and git (the pass-through). It must skip
	// Devpit's shim folder, so it never runs the gh shim.
	Runner adapters.Runner
	// StorePath is accounts.toml.
	StorePath string
	// GitDir is Devpit's Git folder, where devpit.json records the chain.
	GitDir string
	// Getenv and Getwd read this process's environment and folder.
	Getenv func(string) string
	Getwd  func() (string, error)
	// RealPath resolves junctions and subst drives; nil skips that.
	RealPath func(string) (string, error)
	// Stderr gets Devpit's one-line messages and the pass-through's own.
	Stderr io.Writer
	// Timeout bounds the whole request; one minute when zero. A
	// pass-through to Git Credential Manager may wait for a browser.
	Timeout time.Duration
}

// DefaultDeps returns the real dependencies.
func DefaultDeps() (Deps, error) {
	ad, err := adapters.DefaultDeps("")
	if err != nil {
		return Deps{}, err
	}
	shimDir, err := shims.DefaultDir()
	if err != nil {
		return Deps{}, err
	}
	ad.ShimDir = shimDir
	return Deps{
		Runner:    adapters.ExecRunner{ShimDir: shimDir},
		StorePath: ad.Paths.Store,
		GitDir:    adapters.GitDir(ad),
		Getenv:    os.Getenv,
		Getwd:     os.Getwd,
		RealPath:  accounts.RealPath,
		Stderr:    os.Stderr,
		Timeout:   10 * time.Minute,
	}, nil
}

func (d Deps) getenv(k string) string {
	if d.Getenv != nil {
		return d.Getenv(k)
	}
	return os.Getenv(k)
}

func (d Deps) warn(format string, args ...any) {
	if d.Stderr == nil {
		return
	}
	msg := accounts.Scrub(fmt.Sprintf(format, args...))
	msg = strings.ReplaceAll(msg, "\n", " ")
	fmt.Fprintln(d.Stderr, "devpit: "+msg)
}

// request is one credential description: the key=value lines Git sent,
// in order.
type request struct {
	lines []string
}

func (r request) get(key string) string {
	for _, l := range r.lines {
		if k, v, ok := strings.Cut(l, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// forward is the request without capability lines: Devpit answers in the
// plain protocol, and so does the chain it hands over to.
func (r request) forward() []byte {
	var b bytes.Buffer
	for _, l := range r.lines {
		if strings.HasPrefix(l, "capability[]=") {
			continue
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return b.Bytes()
}

func readRequest(in io.Reader) (request, error) {
	var r request
	sc := bufio.NewScanner(io.LimitReader(in, maxInput))
	sc.Buffer(make([]byte, 0, 4096), maxInput)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if l == "" {
			break
		}
		r.lines = append(r.lines, l)
	}
	return r, sc.Err()
}

// Serve answers one credential request: op is get, store or erase (Git's
// words); stdin is what Git sent and stdout what Git reads back. It returns
// an error only for a request it cannot even read; everything else is
// handled by falling back to the recorded chain, so the command should
// exit 0 whenever Serve returns nil. An unknown op is ignored, as Git asks
// of helpers.
func Serve(ctx context.Context, d Deps, op string, stdin io.Reader, stdout io.Writer) error {
	switch op {
	case "get", "store", "erase":
	default:
		return nil
	}
	if d.getenv(EnvActive) != "" {
		return nil // started by Devpit's own pass-through: never answer twice
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := readRequest(stdin)
	if err != nil {
		return fmt.Errorf("reading the request from Git: %w", err)
	}
	if !strings.EqualFold(req.get("protocol"), "https") || !strings.EqualFold(req.get("host"), "github.com") {
		return passThrough(ctx, d, op, req, stdout)
	}

	acct, how, err := pick(d)
	if err != nil {
		d.warn("%v; using your usual GitHub sign-in", err)
		return passThrough(ctx, d, op, req, stdout)
	}
	if acct.IsDefault() {
		return passThrough(ctx, d, op, req, stdout)
	}
	login := acct.Label
	if user := req.get("username"); user != "" && !strings.EqualFold(user, login) {
		if op == "get" {
			// The URL names another user (https://other@github.com/…):
			// that is an explicit choice, so Devpit stays out of it.
			return passThrough(ctx, d, op, req, stdout)
		}
		// store/erase for a credential Devpit did not hand out.
		return passThrough(ctx, d, op, req, stdout)
	}

	switch op {
	case "store":
		return nil // gh owns this account's token; nothing to save
	case "erase":
		d.warn("GitHub did not accept the sign-in for %s (Devpit account %s, %s). Sign in again with: gh auth login. Your other GitHub sign-ins were not touched.",
			login, acct.Name, how)
		return nil
	}
	tok, err := adapters.GitHubToken(ctx, d.Runner, login)
	if err != nil {
		d.warn("cannot get the GitHub sign-in for %s (%v); using your usual GitHub sign-in instead", acct.Name, err)
		return passThrough(ctx, d, op, req, stdout)
	}
	_, err = io.WriteString(stdout, "username="+login+"\npassword="+tok+"\n")
	return err
}

// pick works out the GitHub account for this request, and says why in a
// few words for messages.
func pick(d Deps) (accounts.Account, string, error) {
	s, err := accounts.ReadFile(d.StorePath)
	if err != nil {
		return accounts.Account{}, "", fmt.Errorf("cannot read the accounts file (%v)", firstLine(err.Error()))
	}
	if name := d.getenv(adapters.EnvGitHubAccount); name != "" {
		if a, ok := s.Account(accounts.ToolGitHub, name); ok {
			return a, adapters.EnvGitHubAccount + " is set", nil
		}
		d.warn("%s names %q, which is not a GitHub account in Devpit; following the folder rules instead", adapters.EnvGitHubAccount, name)
	}
	folder, err := Folder(d.getenv, d.Getwd)
	if err != nil {
		return accounts.Account{}, "", err
	}
	res, err := accounts.Resolve(s, accounts.ToolGitHub, folder, accounts.ResolveOptions{RealPath: d.RealPath})
	if err != nil {
		return accounts.Account{}, "", fmt.Errorf("cannot check the folder rules for %s (%v)", folder, firstLine(err.Error()))
	}
	if !res.Account.IsDefault() && res.Account.Label == "" {
		return accounts.Account{}, "", fmt.Errorf("the GitHub account %s has no GitHub login recorded", res.Account.Name)
	}
	return res.Account, res.Why(), nil
}

// Folder is the folder a credential request is for. Git sets GIT_DIR for
// its helpers inside a repository: during `git clone` it is the only thing
// that names the new repository (the working folder is the caller's). A
// GIT_DIR ending in .git means the folder above it; any other (a linked
// worktree's .git/worktrees/x, a bare repository) means the working
// folder, where Git runs the helper.
func Folder(getenv func(string) string, getwd func() (string, error)) (string, error) {
	if getwd == nil {
		getwd = os.Getwd
	}
	cwd, err := getwd()
	if err != nil {
		return "", fmt.Errorf("cannot tell which folder this is (%w)", err)
	}
	gd := ""
	if getenv != nil {
		gd = getenv("GIT_DIR")
	}
	if gd == "" {
		return cwd, nil
	}
	if !filepath.IsAbs(gd) && !isWinAbs(gd) {
		gd = filepath.Join(cwd, gd)
	}
	gd = strings.TrimRight(gd, `/\`)
	base := gd
	if i := strings.LastIndexAny(gd, `/\`); i >= 0 {
		base = gd[i+1:]
	}
	if strings.EqualFold(base, ".git") {
		return gd[:len(gd)-len(base)-1], nil
	}
	return cwd, nil
}

// isWinAbs: a Windows absolute path, checked by hand so tests of
// Windows-style paths behave the same on every OS.
func isWinAbs(p string) bool {
	return (len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')) || strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//")
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimSpace(s)
}

// passThrough hands the request to the helper chain recorded when Devpit
// took over, through `git credential fill|approve|reject` with exactly
// that chain configured. The chain goes in through GIT_CONFIG_COUNT (the
// command line's own scope, read after every file, so an empty first value
// drops Devpit's helper and everything else), never on the command line.
// With no recorded chain there is nothing to hand over to, and Git goes on
// as it would with no helper.
func passThrough(ctx context.Context, d Deps, op string, req request, stdout io.Writer) error {
	h, ok, err := adapters.LoadGitHelper(d.GitDir)
	if err != nil {
		d.warn("cannot read Devpit's record of your previous sign-in helper (%v)", firstLine(err.Error()))
		return nil
	}
	if !ok || len(h.Previous) == 0 {
		return nil
	}
	sub := map[string]string{"get": "fill", "store": "approve", "erase": "reject"}[op]
	env := []string{EnvActive + "=1"}
	n := 0
	if v := d.getenv("GIT_CONFIG_COUNT"); v != "" {
		if c, cerr := strconv.Atoi(v); cerr == nil && c > 0 {
			n = c
		}
	}
	add := func(k, v string) {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", n, k), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", n, v))
		n++
	}
	add("credential.helper", "")
	for _, p := range h.Previous {
		add("credential.helper", p)
	}
	env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(n))

	res, err := d.Runner.Run(ctx, adapters.Cmd{
		Name: "git", Args: []string{"credential", sub}, Env: env,
		Stdin: bytes.NewReader(req.forward()), Stderr: d.Stderr, Timeout: timeoutLeft(ctx),
	})
	if err != nil {
		d.warn("could not hand the request to your previous sign-in helper (%v)", firstLine(accounts.Scrub(err.Error())))
		return nil
	}
	if op != "get" || res.ExitCode != 0 {
		return nil
	}
	var out bytes.Buffer
	for _, l := range strings.Split(string(res.Stdout), "\n") {
		l = strings.TrimRight(l, "\r")
		if l == "" || strings.HasPrefix(l, "capability[]=") || !strings.Contains(l, "=") {
			continue
		}
		out.WriteString(l)
		out.WriteByte('\n')
	}
	_, err = stdout.Write(out.Bytes())
	return err
}

func timeoutLeft(ctx context.Context) time.Duration {
	if dl, ok := ctx.Deadline(); ok {
		if left := time.Until(dl); left > 0 {
			return left
		}
	}
	return time.Second
}

// ErrUsage is returned by Run for a missing or unknown operation.
var ErrUsage = errors.New("usage: devpit git-credential <get|store|erase>")

// Run is the whole command: real dependencies, the process's stdin and
// stdout. args are the command's arguments (the operation).
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) != 1 {
		return ErrUsage
	}
	d, err := DefaultDeps()
	if err != nil {
		// Devpit cannot even find its files: say nothing on stdout, so Git
		// goes on as with no helper.
		fmt.Fprintln(stderr, "devpit: "+accounts.Scrub(err.Error()))
		return nil
	}
	d.Stderr = stderr
	return Serve(ctx, d, args[0], stdin, stdout)
}
