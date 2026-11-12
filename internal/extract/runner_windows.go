//go:build windows

package extract

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// errRelativeExe is a Spec whose executable is not an absolute path.
var errRelativeExe = errors.New("extract: hatchzip must be run from its absolute path")

// realRunner starts hatchzip on Windows.
type realRunner struct{}

// buildCmd is the exec.Cmd for spec, unstarted (tests inspect it).
//
//   - A context that never ends: cancelling must pause (Ctrl+Break), never
//     TerminateProcess, so the caller's context is not handed to exec.
//   - Stdin is nil, which exec documents as the null device (os.DevNull,
//     NUL): hatchzip prompts only on a terminal, so with NUL it can never
//     wait for a key or a password (rule 86; spike X10: exit 5 in ~70 ms).
//   - Stdout and Stderr become pipes in Start, so hatchzip draws no bars and
//     never writes to the screen (spike X5).
//   - CREATE_NEW_PROCESS_GROUP, and no CREATE_NO_WINDOW, DETACHED_PROCESS or
//     CREATE_NEW_CONSOLE: hatchzip shares the terminal's console, which is
//     what lets GenerateConsoleCtrlEvent reach it, and no console or window
//     is made. HideWindow is not needed for the same reason.
//   - The environment is childEnv's: the password, when set, only there.
func buildCmd(spec Spec, environ []string) (*exec.Cmd, error) {
	if !filepath.IsAbs(spec.Exe) {
		return nil, errRelativeExe
	}
	cmd := exec.CommandContext(context.Background(), spec.Exe, spec.Args...) // #nosec G204 -- the detector's absolute hatchzip path, argv only from argv.go (rule 83); no shell
	cmd.Env = childEnv(environ, spec.Password)
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: winapi.CreateNewProcessGroup}
	return cmd, nil
}

// Start implements Runner.
func (realRunner) Start(spec Spec) (Process, error) {
	cmd, err := buildCmd(spec, os.Environ())
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &realProcess{cmd: cmd}
	p.job = assignJob(cmd.Process.Pid, winapi.NewJob)
	p.lines, p.drained = pumpAll(stdout, stderr)
	return p, nil
}

// jobAPI is the part of winapi.Job the runner uses.
type jobAPI interface {
	Assign(pid int) error
	Kill()
}

// assignJob puts pid in a new kill-on-close Job right after it started, so
// hatchzip never outlives a Devpit that died (rule 87). Through Scoop's shim
// the real hatchzip is the shim's child; killing the shim's tree kills it
// too (spike X8). nil when no job could be made; Kill then falls back to the
// process itself.
func assignJob(pid int, newJob func() (*winapi.Job, error)) jobAPI {
	j, err := newJob()
	if err != nil || j == nil {
		return nil
	}
	if err := j.Assign(pid); err != nil {
		j.Kill()
		return nil
	}
	return j
}

// realProcess is a started hatchzip.
type realProcess struct {
	cmd     *exec.Cmd
	job     jobAPI
	lines   <-chan Line
	drained <-chan struct{}

	waitOnce sync.Once
	exit     int
	err      error
}

func (p *realProcess) Lines() <-chan Line { return p.lines }

// Interrupt sends Ctrl+Break to hatchzip's own process group only.
func (p *realProcess) Interrupt() error { return winapi.SendCtrlBreak(p.cmd.Process.Pid) }

// Kill ends hatchzip's Job (the whole tree), or the process when there is no
// Job.
func (p *realProcess) Kill() {
	if p.job != nil {
		p.job.Kill()
	}
	_ = p.cmd.Process.Kill()
}

// Wait waits for both pipes to be read to the end, then for the exit code.
// A non-zero exit is an exit code, not an error.
func (p *realProcess) Wait() (int, error) {
	p.waitOnce.Do(func() {
		<-p.drained
		err := p.cmd.Wait()
		var ee *exec.ExitError
		switch {
		case err == nil:
			p.exit = 0
		case errors.As(err, &ee):
			p.exit = ee.ExitCode()
		default:
			p.exit, p.err = -1, err
		}
		if p.job != nil {
			p.job.Kill() // closes the handle; the tree is already gone
		}
	})
	return p.exit, p.err
}
