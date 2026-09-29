//go:build windows

package elevate

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobTree is the process tree of one exec job, held in a Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. [jobTree.kill] ends the whole tree (an
// installer is a tree: choco starts the vendor's setup, which holds the
// files); [jobTree.release] lets it go without ending anything, for a job
// that finished on its own and may have left a process running on purpose,
// such as the updated app relaunched. It mirrors internal/tools' jobTracker;
// winapi.Job has no release, which is why this package keeps its own.
type jobTree struct {
	mu     sync.Mutex
	handle windows.Handle
}

// newJobTree creates the job. On failure it returns a tree that does
// nothing, and the command's own kill is all there is.
func newJobTree() *jobTree {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return &jobTree{}
	}
	if setJobLimits(h, windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE) != nil {
		_ = windows.CloseHandle(h)
		return &jobTree{}
	}
	return &jobTree{handle: h}
}

// setJobLimits sets the job's extended limit flags.
func setJobLimits(h windows.Handle, flags uint32) error {
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: flags},
	}
	_, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	return err
}

// assign puts the started process pid, and so everything it starts after
// this, into the job.
func (j *jobTree) assign(pid int) error {
	ph, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA, false, uint32(pid)) //nolint:gosec // a live Windows PID is a DWORD
	if err != nil {
		return err
	}
	defer windows.CloseHandle(ph) //nolint:errcheck // best-effort cleanup of a local handle
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return windows.ERROR_INVALID_HANDLE
	}
	return windows.AssignProcessToJobObject(j.handle, ph)
}

// take hands over the handle, once.
func (j *jobTree) take() windows.Handle {
	j.mu.Lock()
	defer j.mu.Unlock()
	h := j.handle
	j.handle = 0
	return h
}

// kill ends every process in the job, with exit code 1 so nothing stopped
// this way can read as a success, then closes the job. Safe to call more
// than once and after release.
func (j *jobTree) kill() {
	if h := j.take(); h != 0 {
		_ = windows.TerminateJobObject(h, 1)
		_ = windows.CloseHandle(h)
	}
}

// release clears the kill-on-close limit and closes the job, leaving its
// processes running. If the limit cannot be cleared the handle stays open,
// since closing it would kill them: that leaks one handle, never a process.
func (j *jobTree) release() {
	if h := j.take(); h != 0 && setJobLimits(h, 0) == nil {
		_ = windows.CloseHandle(h)
	}
}
