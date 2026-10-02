package devpit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// runRoot runs the real command tree, plus any extra test commands, through
// execute: fang, the error handler and the exit code mapping, exactly as
// main does. Its streams are buffers, so it is never interactive.
func runRoot(t *testing.T, extra []*cobra.Command, args ...string) (code ExitCode, stdout, stderr string) {
	t.Helper()
	root := NewRootCmd()
	root.AddCommand(extra...)
	markUsageErrors(root)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(""))
	code = execute(context.Background(), root, args)
	return code, out.String(), errOut.String()
}

// changeCmd is a command that changes something and guards it the way every
// such command must.
func changeCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:  "change-test",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireYes(cmd, yes, "which account is used"); err != nil {
				return err
			}
			_, _ = cmd.OutOrStdout().Write([]byte("changed\n"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "agree to the change")
	return cmd
}

// Every exit code comes out of the root command, with fang's error printing
// still in the path and the message on stderr.
func TestExitCodesThroughTheRootCommand(t *testing.T) {
	verify := &cobra.Command{
		Use:  "verify-test",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = cmd.OutOrStdout().Write([]byte("work: wrong account\n"))
			return withCode(ExitMismatch, nil)
		},
	}
	fail := &cobra.Command{
		Use:  "fail-test",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return errors.New("the disk is full") },
	}
	extra := []*cobra.Command{changeCmd(), verify, fail}

	cases := []struct {
		name       string
		args       []string
		want       ExitCode
		wantStderr string
		wantStdout string
	}{
		{"ok", []string{"version", "--short"}, ExitOK, "", ""},
		{"plain error", []string{"fail-test"}, ExitFailed, "the disk is full", ""},
		{"stub", []string{"clean"}, ExitFailed, "not available from the command line yet", ""},
		{"unknown command", []string{"no-such-command"}, ExitUsage, `unknown command "no-such-command"`, ""},
		{"unknown flag", []string{"version", "--no-such-flag"}, ExitUsage, "unknown flag: --no-such-flag", ""},
		{"too many args", []string{"ports", "kill"}, ExitUsage, "accepts 1 arg(s)", ""},
		{"stray arg to a parent", []string{"font", "bogus"}, ExitUsage, `unknown command "bogus"`, ""},
		{
			"needs yes",
			[]string{"change-test"},
			ExitNeedsYes,
			"This would change which account is used. Re-run with --yes after the user agrees.", "",
		},
		{"yes given", []string{"change-test", "--yes"}, ExitOK, "", "changed"},
		{"verify mismatch", []string{"verify-test"}, ExitMismatch, "", "wrong account"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runRoot(t, extra, tc.args...)
			if code != tc.want {
				t.Errorf("exit code %d, want %d (stderr %q)", code, tc.want, errOut)
			}
			if tc.wantStderr != "" && !strings.Contains(errOut, tc.wantStderr) {
				t.Errorf("stderr %q does not contain %q", errOut, tc.wantStderr)
			}
			if tc.wantStderr == "" && errOut != "" {
				t.Errorf("stderr %q, want nothing", errOut)
			}
			if tc.wantStdout != "" && !strings.Contains(out, tc.wantStdout) {
				t.Errorf("stdout %q does not contain %q", out, tc.wantStdout)
			}
			if tc.want == ExitNeedsYes && strings.Contains(out, "changed") {
				t.Error("the change ran without --yes")
			}
		})
	}
}

// A refused change prints exactly the sentence an agent relays, once, with
// no doubled full stop and no styling when stderr is not a terminal.
func TestNeedsYesMessageIsPlainAndExact(t *testing.T) {
	_, _, errOut := runRoot(t, []*cobra.Command{changeCmd()}, "change-test")
	want := "This would change which account is used. Re-run with --yes after the user agrees.\n"
	if errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
}

// The agent and CI hints never stand in for --yes or for a person: with any
// of them set and no terminal, a change is still refused, and with a
// terminal the decision does not depend on them.
func TestEnvironmentHintsNeverLoosenConfirmation(t *testing.T) {
	for _, kv := range [][2]string{{"CI", "true"}, {"CLAUDECODE", "1"}, {"NO_COLOR", "1"}, {"TERM", "dumb"}} {
		t.Run(kv[0], func(t *testing.T) {
			t.Setenv(kv[0], kv[1])
			code, out, _ := runRoot(t, []*cobra.Command{changeCmd()}, "change-test")
			if code != ExitNeedsYes || strings.Contains(out, "changed") {
				t.Errorf("with %s=%s: exit %d, stdout %q; want %d and no change", kv[0], kv[1], code, out, ExitNeedsYes)
			}
		})
	}
	if err := needsYes(false, false, "x"); codeOf(err) != ExitNeedsYes {
		t.Errorf("needsYes(no --yes, no terminal) = %v, want ExitNeedsYes", err)
	}
	if err := needsYes(false, true, "x"); err != nil {
		t.Errorf("needsYes(no --yes, terminal) = %v, want nil: the command asks the person itself", err)
	}
	if err := needsYes(true, false, "x"); err != nil {
		t.Errorf("needsYes(--yes, no terminal) = %v, want nil", err)
	}
}

// codeOf maps every kind of error the tree can return.
func TestCodeOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want ExitCode
	}{
		{nil, ExitOK},
		{errors.New("boom"), ExitFailed},
		{withCode(ExitUsage, errors.New("x")), ExitUsage},
		{withCode(ExitMismatch, nil), ExitMismatch},
		{errors.New(`required flag(s) "name" not set`), ExitUsage},
		{errors.New("if any flags in the group [a b] are set none of the others can be; [a b] were all set"), ExitUsage},
		{errors.Join(errors.New("wrapped"), withCode(ExitNeedsYes, errors.New("y"))), ExitNeedsYes},
	}
	for _, tc := range cases {
		if got := codeOf(tc.err); got != tc.want {
			t.Errorf("codeOf(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

// The help lists the codes the program actually uses.
func TestHelpListsTheExitCodes(t *testing.T) {
	t.Parallel()
	long := NewRootCmd().Long
	for _, want := range []string{"Exit codes:", "- 0: done", "- 1: failed", "- 2: wrong usage", "- 3: needs --yes", "- 4: verify found a mismatch"} {
		if !strings.Contains(long, want) {
			t.Errorf("root help is missing %q", want)
		}
	}
}

// The reserved commands fail with code 1 and say where to go instead. They
// used to print a note and exit 0, which a script or an agent reads as
// success.
func TestStubCommandsFailAndPointAtTheApp(t *testing.T) {
	for _, args := range [][]string{{"clean"}, {"clean", "--dry-run"}, {"ports"}, {"ports", "kill", "3000"}, {"update"}, {"settings"}} {
		code, out, errOut := runRoot(t, nil, args...)
		if code != ExitFailed {
			t.Errorf("devpit %v: exit %d, want %d", args, code, ExitFailed)
		}
		if out != "" {
			t.Errorf("devpit %v: printed %q on stdout, want nothing", args, out)
		}
		if !strings.Contains(errOut, "not available from the command line yet") || !strings.Contains(errOut, "open `devpit`") {
			t.Errorf("devpit %v: stderr %q does not point at the app", args, errOut)
		}
	}
}

// TestMain doubles as the helper process for TestTheProcessExitsWithTheCode:
// with DEVPIT_TEST_EXECUTE set it runs Execute on the arguments after "--"
// and exits with what it returns, exactly as main does. It does that before
// the tests start, because the testing package treats an os.Exit(0) from
// inside a test as a failure.
func TestMain(m *testing.M) {
	if fakeClaude() {
		return
	}
	if os.Getenv("DEVPIT_TEST_EXECUTE") == "1" {
		args := []string{"devpit"}
		for i, a := range os.Args {
			if a == "--" {
				args = append(args, os.Args[i+1:]...)
				break
			}
		}
		os.Args = args
		os.Exit(Execute())
	}
	os.Exit(m.Run())
}

// The code leaves the process: Execute's result is the real exit status of
// a real process, through main's os.Exit.
func TestTheProcessExitsWithTheCode(t *testing.T) {
	if testing.Short() {
		t.Skip("starts child processes")
	}
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"version", "--short"}, 0},
		{[]string{"update"}, 1},
		{[]string{"no-such-command"}, 2},
	} {
		cmdArgs := append([]string{"-test.run=^$", "--"}, tc.args...)
		cmd := exec.Command(os.Args[0], cmdArgs...) //nolint:gosec // G204: re-running this test binary as a helper process.
		cmd.Env = append(os.Environ(), "DEVPIT_TEST_EXECUTE=1")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		got := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			got = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("devpit %v: %v", tc.args, err)
		}
		if got != tc.want {
			t.Errorf("devpit %v: process exited %d, want %d (stderr %q)", tc.args, got, tc.want, stderr.String())
		}
	}
}
