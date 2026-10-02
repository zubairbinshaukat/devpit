package devpit

import (
	"io"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

// lookupEnv is the shape of os.LookupEnv, so a test can hand in a fake
// environment.
type lookupEnv func(key string) (string, bool)

// fder is what os.Stdin and os.Stdout are and a bytes.Buffer is not:
// something with a file descriptor that may be a terminal.
type fder interface {
	Fd() uintptr
}

// terminalCheck is the real terminal test.
func terminalCheck(fd uintptr) bool { return term.IsTerminal(fd) }

// isTerminalWriter reports whether a stream is a terminal according to
// isTerm. A stream with no file descriptor (a buffer, a pipe wrapper) is not.
func isTerminalWriter(stream any, isTerm func(uintptr) bool) bool {
	f, ok := stream.(fder)
	return ok && isTerm(f.Fd())
}

// interactive reports whether a person can answer a prompt: stdin and stdout
// are both terminals. Either one redirected (a pipe, a file, an agent's
// captured output) means no.
//
// The environment is deliberately not consulted. A variable can make output
// plainer (see plainOutput) but it can never make Devpit treat a session as
// one where somebody agreed to a change.
func interactive(in io.Reader, out io.Writer, isTerm func(uintptr) bool) bool {
	return isTerminalWriter(in, isTerm) && isTerminalWriter(out, isTerm)
}

// isInteractive is interactive for a running command's own streams.
func isInteractive(cmd *cobra.Command) bool {
	return interactive(cmd.InOrStdin(), cmd.OutOrStdout(), terminalCheck)
}

// plainOutput reports whether the environment asks for plain output: no
// colour, no boxes, one message per line. CI and CLAUDECODE mark an
// automated run, NO_COLOR is the common switch (any non-empty value), and
// TERM=dumb is a terminal that cannot draw.
//
// It only ever changes how things look. Nothing that decides whether a
// change may happen reads it.
func plainOutput(env lookupEnv) bool {
	if env == nil {
		return false
	}
	set := func(k string) bool {
		v, ok := env(k)
		v = strings.TrimSpace(v)
		return ok && v != "" && v != "0" && !strings.EqualFold(v, "false")
	}
	if t, ok := env("TERM"); ok && strings.EqualFold(strings.TrimSpace(t), "dumb") {
		return true
	}
	if v, ok := env("NO_COLOR"); ok && v != "" {
		return true
	}
	return set("CI") || set("CLAUDECODE")
}
