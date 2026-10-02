//go:build windows

package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// batchCommand runs a script that is not an npm shim through System32's
// cmd.exe with a hand-built, checked command line.
func batchCommand(ctx context.Context, script string, args []string) (*exec.Cmd, error) {
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, fmt.Errorf("finding cmd.exe: %w", err)
	}
	cmdExe := filepath.Join(sys, "cmd.exe")
	line, err := BatchCommandLine(cmdExe, script, args)
	if err != nil {
		return nil, err
	}
	c := exec.CommandContext(ctx, cmdExe) // #nosec G204 -- System32's cmd.exe
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	return c, nil
}

// Run starts the program and waits for it, as a stand-in for the program
// itself: it shares the console, lets the child handle Ctrl+C and
// Ctrl+Break, and returns the child's exit code.
//
// Ctrl+C. Every process on a console gets it. The launcher catches it with
// signal.Notify and does nothing, so it stays alive while the child decides
// what to do. signal.Ignore would not work: Go's console handler then
// returns FALSE and Windows ends the launcher (exit 0xC000013A, measured in
// the Phase 0 spike); and SetConsoleCtrlHandler(nil, TRUE) would be
// inherited by the child, which would then ignore Ctrl+C too. A closing
// console (SIGTERM in Go) is caught as well, so the child gets its own
// few seconds to clean up before the launcher goes.
//
// The job object, a deliberate choice. Unless spec.NoJob is set, the
// launcher puts itself in a job with kill-on-close before starting the
// child, so the child and everything it starts are in the job from their
// first instruction (no start-then-assign race). If the launcher is killed
// (a task runner or an IDE ending it, which only ends the process it
// started), Windows closes the job and ends the whole tree instead of
// leaving the real tool running orphaned. When the child exits on its own,
// the launcher lifts kill-on-close before it exits, so whatever the tool
// meant to leave running (a background session such as `claude --bg`, a
// browser it opened to sign in) is left alone. A process may also leave the
// job itself (BREAKAWAY_OK). The lift-on-exit behaviour still needs the
// Phase 9 real-machine check against `claude --bg`.
//
// Run is meant for a process whose last act is running the child (the shim,
// and "devpit <tool> run"): the launcher stays in the job until it exits.
func Run(spec Spec) (int, error) {
	c, err := spec.command()
	if err != nil {
		return 1, err
	}

	sig := make(chan os.Signal, 4)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		for range sig { //nolint:revive // drained on purpose: the child handles Ctrl+C
		}
	}()

	var job *killJob
	selfInJob := false
	if !spec.NoJob {
		if j, jerr := newKillJob(); jerr == nil {
			job = j
			defer job.close()
			selfInJob = job.assign(windows.CurrentProcess()) == nil
		}
	}

	if err = c.Start(); err != nil {
		return 1, err
	}
	if job != nil && !selfInJob {
		// A parent job that forbids nesting: put the child in alone.
		// Anything it starts in its first microseconds escapes the job.
		if h, herr := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(c.Process.Pid)); herr == nil { //nolint:gosec // a live pid
			_ = job.assign(h)
			_ = windows.CloseHandle(h)
		}
	}
	err = c.Wait()
	if job != nil {
		job.keepOnClose()
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return 1, err
	}
	return c.ProcessState.ExitCode(), nil
}

// killJob is a job object with kill-on-close set.
type killJob struct{ h windows.Handle }

func newKillJob() (*killJob, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	j := &killJob{h: h}
	if err := j.setLimits(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	return j, nil
}

func (j *killJob) setLimits(flags uint32) error {
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: flags},
	}
	_, err := windows.SetInformationJobObject(j.h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return err
}

func (j *killJob) assign(process windows.Handle) error {
	return windows.AssignProcessToJobObject(j.h, process)
}

// keepOnClose lifts kill-on-close: from now on closing the job leaves its
// processes running.
func (j *killJob) keepOnClose() {
	_ = j.setLimits(windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK)
}

func (j *killJob) close() { _ = windows.CloseHandle(j.h) }
