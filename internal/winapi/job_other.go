//go:build !windows

package winapi

// Job has no equivalent outside Windows, where the callers rely on the
// context cancelling the direct child. Every method is a no-op so code that
// uses a Job needs no build tags of its own.
type Job struct{}

// KilledExitCode is the exit code a Windows job kill gives its processes.
const KilledExitCode = 1

// NewJob returns a Job that does nothing, and [ErrUnsupported] so a caller
// can tell there is no real job behind it.
func NewJob() (*Job, error) { return nil, ErrUnsupported }

// Assign is not supported outside Windows.
func (*Job) Assign(int) error { return ErrUnsupported }

// Kill does nothing outside Windows.
func (*Job) Kill() {}
