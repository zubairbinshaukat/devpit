package winapi

import (
	"errors"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// errNoJob is returned by [Job.Assign] on a job that is nil or already killed.
var errNoJob = errors.New("winapi: no job object to assign the process to")

// Job is a Windows Job Object set to kill everything in it when it is closed.
// A process put in it, and every process that one goes on to start, dies
// together on [Job.Kill]. That is what "stop this app's installer" needs: an
// installer is a tree (winget starts msiexec, Chocolatey starts the vendor's
// setup, cmd starts npm), and killing only the process Devpit started leaves
// the rest running and holding the output pipe open.
type Job struct {
	mu     sync.Mutex
	handle windows.Handle
}

// NewJob creates an empty job with the kill-on-close limit set.
func NewJob() (*Job, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	return &Job{handle: h}, nil
}

// Assign puts the running process pid into the job. Call it right after the
// process starts: a child it spawns before this call is not in the job.
func (j *Job) Assign(pid int) error {
	if j == nil {
		return errNoJob
	}
	ph, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA, false, uint32(pid)) //nolint:gosec // pid is a live OS process id, always non-negative and within uint32 range.
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(ph) }()

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.handle == 0 {
		return errNoJob
	}
	return windows.AssignProcessToJobObject(j.handle, ph)
}

// KilledExitCode is the exit code every process in a job gets from
// [Job.Kill]. It is not zero on purpose: a process ended by closing its job
// under the kill-on-close limit exits with code 0 (measured on Windows 11),
// so a killed installer looked like one that had succeeded.
const KilledExitCode = 1

// Kill terminates every process in the job with [KilledExitCode], then
// closes the handle; the kill-on-close limit is the backstop should the
// terminate call fail. It is safe to call more than once, on a nil Job, and
// after a failed [Job.Assign].
func (j *Job) Kill() {
	if j == nil {
		return
	}
	j.mu.Lock()
	h := j.handle
	j.handle = 0
	j.mu.Unlock()
	if h != 0 {
		_ = windows.TerminateJobObject(h, KilledExitCode)
		_ = windows.CloseHandle(h)
	}
}
