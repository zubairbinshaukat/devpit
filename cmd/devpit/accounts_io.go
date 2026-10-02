package devpit

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
)

// accountsEnv is how the accounts commands reach Accounts. Tests hand in a
// service on temporary folders.
type accountsEnv struct {
	// open builds the service for one command.
	open func() (*service.Service, error)
	// getwd is the current folder.
	getwd func() (string, error)
}

func defaultAccountsEnv() accountsEnv {
	return accountsEnv{
		open:  func() (*service.Service, error) { return service.Open(service.Options{}) },
		getwd: os.Getwd,
	}
}

// openService opens the service and tells the person, on stderr, about any
// change a crash left unfinished that was just finished or put back.
func (env accountsEnv) openService(cmd *cobra.Command, quiet bool) (*service.Service, error) {
	s, err := env.open()
	if err != nil {
		return nil, err
	}
	if !quiet {
		for _, r := range s.Recovered {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Devpit %s a change that was left unfinished: %s\n", r.Outcome, r.Entry.Summary)
		}
		if s.RecoverErr != nil {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "! "+s.RecoverErr.Error())
		}
	}
	return s, nil
}

// folder is the --folder value made absolute, or the current folder.
func (env accountsEnv) folder(flag string) (string, error) {
	if flag != "" {
		return filepath.Abs(flag)
	}
	return env.getwd()
}

// jsonError is an error of a --json command: printed on stderr as one JSON
// object, {"error": "...", "code": N}, so an agent never has to parse a
// sentence to know what happened.
type jsonError struct{ err error }

func (e *jsonError) Error() string { return e.err.Error() }
func (e *jsonError) Unwrap() error { return e.err }

func (e *jsonError) encode(code ExitCode) string {
	b, _ := json.Marshal(struct {
		Error string `json:"error"`
		Code  int    `json:"code"`
	}{accounts.Scrub(e.err.Error()), int(code)})
	return string(b)
}

// asJSON wraps err for a --json command.
func asJSON(on bool, err error) error {
	if err == nil || !on {
		return err
	}
	return &jsonError{err: err}
}

// accountsError gives an error from the accounts engine its exit code:
// wrong usage for a name or tool that matches nothing (or more than one),
// failed for anything else.
func accountsError(err error) error {
	if err == nil {
		return nil
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return err
	}
	if service.IsUsageError(err) {
		return withCode(ExitUsage, err)
	}
	return withCode(ExitFailed, err)
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func say(w io.Writer, lines []string) {
	for _, l := range lines {
		_, _ = fmt.Fprintln(w, l)
	}
}

// printEvent prints one step of a running change as a line.
func printEvent(w io.Writer, ev accounts.Event) {
	detail := ""
	if ev.Detail != "" && ev.Detail != ev.Step {
		detail = ": " + ev.Detail
	}
	switch ev.State {
	case accounts.StepDone:
		_, _ = fmt.Fprintln(w, glyphOK+" "+ev.Step+detail)
	case accounts.StepWaiting:
		_, _ = fmt.Fprintln(w, "… "+ev.Step)
	case accounts.StepWarning:
		_, _ = fmt.Fprintln(w, glyphWarn+" "+ev.Step+detail)
	case accounts.StepInfo:
		if ev.Detail != "" {
			_, _ = fmt.Fprintln(w, "  "+ev.Detail)
		}
	case accounts.StepFailed:
		msg := ""
		if ev.Err != nil {
			msg = ": " + ev.Err.Error()
		}
		_, _ = fmt.Fprintln(w, "✘ "+ev.Step+msg)
	}
}

// drain prints every event and returns the last one.
func drain(w io.Writer, ch <-chan accounts.Event) accounts.Event {
	var last accounts.Event
	for ev := range ch {
		printEvent(w, ev)
		last = ev
	}
	return last
}

// prompter reads answers from the command's stdin, one line at a time.
type prompter struct {
	in  *bufio.Reader
	out io.Writer
}

func newPrompter(cmd *cobra.Command) *prompter {
	return &prompter{in: bufio.NewReader(cmd.InOrStdin()), out: cmd.OutOrStdout()}
}

// line asks q and returns the trimmed answer; ok is false at end of input.
func (p *prompter) line(q string) (string, bool) {
	_, _ = fmt.Fprint(p.out, q)
	s, err := p.in.ReadString('\n')
	if err != nil && s == "" {
		_, _ = fmt.Fprintln(p.out)
		return "", false
	}
	return strings.TrimSpace(s), true
}

// confirm asks a yes/no question whose default is No.
func (p *prompter) confirm(q string) bool {
	a, _ := p.line(q + " [y/N] ")
	a = strings.ToLower(a)
	return a == "y" || a == "yes"
}

// agree is the guard every accounts change goes through: --yes, or a person
// at a terminal who answers yes (default No). With nobody at a terminal and
// no --yes it returns the exit-3 refusal and nothing may change.
func agree(cmd *cobra.Command, p *prompter, yes bool, what, question string) (bool, error) {
	if err := requireYes(cmd, yes, what); err != nil {
		return false, err
	}
	if yes {
		return true, nil
	}
	if !p.confirm(question) {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Nothing changed.")
		return false, nil
	}
	return true, nil
}
