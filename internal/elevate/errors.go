package elevate

import "fmt"

// DeclinedError is returned by [Launch] when the user declines the UAC
// prompt. ShellExecuteExW reports this as ERROR_CANCELLED (1223); Devpit
// turns it into a typed error so callers can show "skipped (needs admin)"
// instead of a generic failure.
type DeclinedError struct{}

// Error implements the error interface.
func (*DeclinedError) Error() string {
	return "elevate: the UAC prompt was declined"
}

// WorkerDiedError is returned when the connection to the elevated worker
// closes or errors while a job is outstanding, whether from a crash, the
// process being killed, or the pipe otherwise breaking.
type WorkerDiedError struct {
	// LastLines holds the most recent stdout/stderr lines the worker
	// streamed before it died, oldest first.
	LastLines []string
	// Err is the underlying transport error, if any. It is nil when the
	// worker simply closed the pipe without an OS-level error.
	Err error
}

// Error implements the error interface.
func (e *WorkerDiedError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("elevate: worker died: %v", e.Err)
	}
	return "elevate: worker died"
}

// Unwrap lets errors.Is/As reach the underlying transport error.
func (e *WorkerDiedError) Unwrap() error { return e.Err }
