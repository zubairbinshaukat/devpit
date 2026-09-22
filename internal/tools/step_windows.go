//go:build windows

package tools

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobTracker is the Windows [processTracker]. It puts the child process into
// a Job Object configured with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, so kill
// terminates the process and every descendant it spawned — including a
// grandchild that inherited RunStep's stdout/stderr pipe handles and would
// otherwise keep them open, and RunStep's reader goroutines blocked, long
// after the direct child was gone.
type jobTracker struct {
	mu     sync.Mutex
	handle windows.Handle
}

func newProcessTracker() processTracker {
	return &jobTracker{}
}

// track creates a Job Object, sets its kill-on-close limit, and assigns
// cmd's already-started process to it.
func (j *jobTracker) track(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errNotStarted
	}

	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, setErr := windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); setErr != nil {
		_ = windows.CloseHandle(h)
		return setErr
	}

	ph, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA, false, uint32(cmd.Process.Pid)) //nolint:gosec // Pid is a live OS process id, always non-negative and within uint32 range.
	if err != nil {
		_ = windows.CloseHandle(h)
		return err
	}
	defer func() { _ = windows.CloseHandle(ph) }()

	if err := windows.AssignProcessToJobObject(h, ph); err != nil {
		_ = windows.CloseHandle(h)
		return err
	}

	j.mu.Lock()
	j.handle = h
	j.mu.Unlock()
	return nil
}

// kill closes the Job Object handle. Because the job was created with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, closing its last handle terminates
// every process still assigned to it. Safe to call more than once: the
// handle is cleared after the first close so a second call is a no-op.
func (j *jobTracker) kill() {
	j.mu.Lock()
	h := j.handle
	j.handle = 0
	j.mu.Unlock()
	if h != 0 {
		_ = windows.CloseHandle(h)
	}
}
