package ports

import "fmt"

// ProtectedError is returned by Kill and KillTree when the target PID is
// protected (docs/safety.md rule 16); Reason explains why, so the UI can
// show it.
type ProtectedError struct {
	PID    uint32
	Reason string
}

func (e *ProtectedError) Error() string {
	return fmt.Sprintf("ports: refusing to kill PID %d: %s", e.PID, e.Reason)
}

// AccessDeniedError is returned by Kill and KillTree when OpenProcess or
// TerminateProcess fails with access denied — typically another user's
// process, or one running elevated. The UI uses this type to offer running
// the kill through the elevated worker instead.
type AccessDeniedError struct {
	PID uint32
	Err error
}

func (e *AccessDeniedError) Error() string {
	return fmt.Sprintf("ports: access denied killing PID %d: %v", e.PID, e.Err)
}

func (e *AccessDeniedError) Unwrap() error { return e.Err }
