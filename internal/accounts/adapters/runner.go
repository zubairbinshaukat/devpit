package adapters

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/launch"
)

// DefaultTimeout bounds a command that does not set its own.
const DefaultTimeout = 15 * time.Second

// LoginTimeout bounds a sign-in, which waits for a person in a browser.
const LoginTimeout = 15 * time.Minute

// Cmd is one run of a tool.
type Cmd struct {
	// Name is the program name ("claude", "gh"). It is found on PATH
	// outside Devpit's shim folder, so an adapter never runs its own shim.
	Name string
	Args []string
	// Env is KEY=VALUE pairs set for this run; Unset is keys removed.
	Env   []string
	Unset []string
	// Dir is the working folder; "" keeps Devpit's.
	Dir string
	// Timeout bounds the run; DefaultTimeout when zero.
	Timeout time.Duration
	// Stdin, Stdout and Stderr, when set, are connected instead of
	// capturing: used for an interactive sign-in. Output that goes there
	// is not in the Result.
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// String is the command line for logs: program and arguments, never the
// environment's values.
func (c Cmd) String() string {
	s := c.Name
	if len(c.Args) > 0 {
		s += " " + strings.Join(c.Args, " ")
	}
	return accounts.Scrub(s)
}

// Result is what a run produced. A non-zero exit code is not an error: many
// tools exit 1 to say "not signed in".
type Result struct {
	Stdout, Stderr []byte
	ExitCode       int
	// Path is the program that ran.
	Path string
}

// Runner runs tools. Tests use [FakeRunner]; the app uses [ExecRunner].
type Runner interface {
	Run(ctx context.Context, c Cmd) (Result, error)
}

// ErrForbiddenCommand is wrapped by the error for a command Devpit must
// never run because it would print or carry a secret.
var ErrForbiddenCommand = errors.New("this command is never run by Devpit, because it would print or pass a token")

// CheckArgs refuses commands that would print a token or carry one on the
// command line:
//
//   - firebase login:list --json (prints every account's tokens; the text
//     form is used instead);
//   - gh auth status --show-token / -t, and gh auth token with --show-token;
//   - any argument that looks like a token at all. An argument that is an
//     absolute path (or --flag=<absolute path>) is a path Devpit built, and
//     is checked with the path rule (accounts.PathLooksSecret: a known token
//     prefix), so a long, random-looking folder name works; every other
//     argument gets the strict rule.
//
// Every runner calls it before starting anything, the fake one included.
func CheckArgs(name string, args []string) error {
	prog := strings.ToLower(strings.TrimSuffix(strings.ToLower(name), ".exe"))
	has := func(want ...string) bool {
		for _, a := range args {
			for _, w := range want {
				if a == w || strings.HasPrefix(a, w+"=") {
					return true
				}
			}
		}
		return false
	}
	switch prog {
	case "firebase":
		if has("login:list") && has("--json", "-j") {
			return fmt.Errorf("%w: firebase login:list --json", ErrForbiddenCommand)
		}
	case "gh":
		if has("--show-token") || (has("auth") && has("status") && has("-t")) {
			return fmt.Errorf("%w: gh auth status --show-token", ErrForbiddenCommand)
		}
		// gh auth switch changes gh's active account for every terminal;
		// Devpit picks the account per process and never runs it.
		if len(args) >= 2 && args[0] == "auth" && args[1] == "switch" {
			return fmt.Errorf("%w: gh auth switch (it would change gh's account everywhere)", ErrForbiddenCommand)
		}
	}
	for i, a := range args {
		if argLooksSecret(a) {
			return fmt.Errorf("%w: argument %d of %s looks like a token", ErrForbiddenCommand, i+1, prog)
		}
	}
	return nil
}

// argLooksSecret is CheckArgs' test for one argument: the path rule for an
// absolute path, the strict rule for anything else. A --flag=<path> is a
// path only when the flag's own name is not a secret's ("--token=C:\x" is
// still refused).
func argLooksSecret(a string) bool {
	if p, ok := accounts.IsAbsPathArg(a); ok {
		if name, _, cut := strings.Cut(a, "="); cut && accounts.LooksSecret(name+"=xxxxxxxx") {
			return true
		}
		return accounts.PathLooksSecret(p)
	}
	return accounts.LooksSecret(a)
}

// ExecRunner runs real tools.
type ExecRunner struct {
	// ShimDir is skipped when looking for tools.
	ShimDir string
	// Log, when set, gets one line per command and its scrubbed output.
	Log func(string)
}

// Run finds c.Name outside the shim folder and runs it with a timeout. A
// tool that is not installed returns an error matching
// accounts.ErrNotFoundTool; one that does not finish in time,
// accounts.ErrTimeout. An npm .cmd runs as node.exe <script>, never through
// cmd.exe with free-form arguments (see package launch).
func (r ExecRunner) Run(ctx context.Context, c Cmd) (Result, error) {
	if err := CheckArgs(c.Name, c.Args); err != nil {
		return Result{}, err
	}
	find := launch.FindOptions{Skip: []string{r.ShimDir}}
	if r.ShimDir == "" {
		find.Skip = nil
	}
	path, err := launch.Find(c.Name, find)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", c.Name, accounts.ErrNotFoundTool)
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	t, err := launch.Prepare(path, find)
	if err != nil {
		return Result{Path: path}, accounts.ScrubError(err)
	}
	cmd, err := t.Command(ctx, c.Args)
	if err != nil {
		return Result{Path: path}, accounts.ScrubError(err)
	}
	cmd.Env = launch.MergeEnv(os.Environ(), c.Env, c.Unset)
	cmd.Dir = c.Dir
	cmd.WaitDelay = 2 * time.Second

	var out, errb bytes.Buffer
	interactive := c.Stdin != nil || c.Stdout != nil || c.Stderr != nil
	cmd.Stdin = c.Stdin
	cmd.Stdout, cmd.Stderr = &out, &errb
	if c.Stdout != nil {
		cmd.Stdout = c.Stdout
	}
	if c.Stderr != nil {
		cmd.Stderr = c.Stderr
	}
	if !interactive {
		hideWindow(cmd)
	}

	start := time.Now()
	runErr := cmd.Run()
	res := Result{Stdout: out.Bytes(), Stderr: errb.Bytes(), Path: path}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	r.log(c, res, time.Since(start))

	if ctx.Err() == context.DeadlineExceeded {
		return res, fmt.Errorf("%s did not answer within %s: %w", c.String(), timeout, accounts.ErrTimeout)
	}
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			return res, nil
		}
		return res, accounts.ScrubError(fmt.Errorf("running %s: %w", c.String(), runErr))
	}
	return res, nil
}

func (r ExecRunner) log(c Cmd, res Result, took time.Duration) {
	if r.Log == nil {
		return
	}
	keys := make([]string, 0, len(c.Env))
	for _, kv := range c.Env {
		k, _, _ := strings.Cut(kv, "=")
		keys = append(keys, k)
	}
	line := fmt.Sprintf("ran %s (exit %d, %s)", c.String(), res.ExitCode, took.Round(time.Millisecond))
	if len(keys) > 0 {
		line += " with " + strings.Join(keys, ", ") + " set"
	}
	r.Log(line)
	for _, l := range Lines(res.Stdout) {
		r.Log("  out: " + accounts.Scrub(l))
	}
	for _, l := range Lines(res.Stderr) {
		r.Log("  err: " + accounts.Scrub(l))
	}
}
