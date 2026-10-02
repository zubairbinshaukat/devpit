package devpit

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
)

// ExitCode is a process exit status Devpit promises to scripts and agents.
// The numbers are part of the command line's contract: they are listed in
// `devpit --help` and never change meaning.
type ExitCode int

// The exit codes.
const (
	// ExitOK means the command did what it was asked.
	ExitOK ExitCode = 0
	// ExitFailed means the command could not do what it was asked.
	ExitFailed ExitCode = 1
	// ExitUsage means the command line itself was wrong: an unknown command
	// or flag, or the wrong number of arguments.
	ExitUsage ExitCode = 2
	// ExitNeedsYes means the command would have changed something, nobody
	// was at a terminal to agree, and --yes was not given. Nothing changed.
	ExitNeedsYes ExitCode = 3
	// ExitMismatch means a verify command ran and found that what is set up
	// is not what should be.
	ExitMismatch ExitCode = 4
)

// exitCodeHelp lists the codes for the root command's help, from the same
// table the code uses, so the two cannot drift apart. It is a list that reads
// the same in a terminal and on the generated docs page.
func exitCodeHelp() string {
	rows := []struct {
		code ExitCode
		text string
	}{
		{ExitOK, "done"},
		{ExitFailed, "failed"},
		{ExitUsage, "wrong usage"},
		{ExitNeedsYes, "needs --yes: it would change something and nobody agreed"},
		{ExitMismatch, "verify found a mismatch"},
	}
	var b strings.Builder
	b.WriteString("Exit codes:")
	for _, r := range rows {
		fmt.Fprintf(&b, "\n- %d: %s", r.code, r.text)
	}
	return b.String()
}

// ExitError is an error that carries the exit code the process ends with.
// Execute maps it; any other error ends the process with ExitFailed.
//
// An ExitError with a nil Err is silent: the command has already printed
// what it needs to (a verify report, say) and only the code is left to give.
type ExitError struct {
	// Code is the exit status.
	Code ExitCode
	// Err is the message printed on stderr, or nil for none.
	Err error
}

// Error implements error.
func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit code %d", e.Code)
	}
	return e.Err.Error()
}

// Unwrap exposes the underlying error.
func (e *ExitError) Unwrap() error { return e.Err }

// withCode wraps err so the process ends with code. A nil err gives a silent
// exit with that code.
func withCode(code ExitCode, err error) error {
	return &ExitError{Code: code, Err: err}
}

// codeOf is the exit code for what a command returned.
func codeOf(err error) ExitCode {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	if isCobraUsageError(err) {
		return ExitUsage
	}
	return ExitFailed
}

// isCobraUsageError catches the usage mistakes Cobra reports without going
// through an Args validator or the flag error function: required flags and
// flag groups. Everything else is marked at its source by markUsageErrors.
func isCobraUsageError(err error) bool {
	s := err.Error()
	for _, prefix := range []string{
		"required flag(s) ",
		"if any flags in the group ",
		"at least one of the flags in the group ",
	} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// markUsageErrors makes every argument and flag mistake anywhere in the tree
// end the process with ExitUsage. It wraps each command's Args validator,
// which is also where Cobra reports an unknown subcommand, and sets the flag
// error function the whole tree inherits. Run it after every command has
// been added.
func markUsageErrors(root *cobra.Command) {
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return withCode(ExitUsage, err)
	})
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if validate := c.Args; validate != nil {
			c.Args = func(cmd *cobra.Command, args []string) error {
				if err := validate(cmd, args); err != nil {
					return withCode(ExitUsage, err)
				}
				return nil
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// NeedsYesError is the refusal [requireYes] returns, inside an ExitError
// with ExitNeedsYes.
type NeedsYesError struct {
	// What names the thing that would have changed, as the end of "This
	// would change …": "which account is used".
	What string
}

// Error implements error. The sentence is written for the person an agent
// relays it to, so it is a full sentence.
func (e *NeedsYesError) Error() string {
	return "This would change " + e.What + ". Re-run with --yes after the user agrees."
}

// requireYes is the guard every command that changes something calls before
// it changes anything. It returns nil when the command may go on: --yes was
// given, or a person is at a terminal and the command will now ask them
// (with No as the default). Otherwise it returns an ExitNeedsYes error and
// nothing may change.
//
// Only real terminals count. CI, CLAUDECODE, NO_COLOR and TERM=dumb may make
// output plainer, but none of them can stand in for --yes or for a person.
func requireYes(cmd *cobra.Command, yes bool, what string) error {
	return needsYes(yes, isInteractive(cmd), what)
}

// needsYes is requireYes with the terminal check already made.
func needsYes(yes, interactive bool, what string) error {
	if yes || interactive {
		return nil
	}
	return withCode(ExitNeedsYes, &NeedsYesError{What: what})
}

// errorHandler prints a command's error on stderr. A silent ExitError prints
// nothing. When stderr is not a terminal, or the environment asks for plain
// output, the message is printed as one plain line an agent can read back
// verbatim; otherwise fang's styled block is used.
func errorHandler(root *cobra.Command, env lookupEnv) fang.ErrorHandler {
	return func(w io.Writer, styles fang.Styles, err error) {
		var ee *ExitError
		if errors.As(err, &ee) && ee.Err == nil {
			return
		}
		var je *jsonError
		if errors.As(err, &je) {
			// A --json command's error is one JSON object on stderr.
			_, _ = fmt.Fprintln(w, je.encode(codeOf(err)))
			return
		}
		if !isTerminalWriter(root.ErrOrStderr(), terminalCheck) || plainOutput(env) {
			_, _ = fmt.Fprintln(w, err.Error())
			if codeOf(err) == ExitUsage {
				_, _ = fmt.Fprintf(w, "Run '%s --help' for usage.\n", root.Name())
			}
			return
		}
		// fang adds its own full stop; do not end up with two.
		fang.DefaultErrorHandler(w, styles, trimmedError{err})
	}
}

// trimmedError is err with any trailing full stop removed from its message.
type trimmedError struct{ err error }

func (t trimmedError) Error() string { return strings.TrimRight(t.err.Error(), ".") }

func (t trimmedError) Unwrap() error { return t.err }
