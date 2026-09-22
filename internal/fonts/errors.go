package fonts

import "fmt"

// OfflineError indicates the font download failed because the network is
// unreachable. Callers should skip the install silently (plan.md section
// 10: "No internet -> ... font download ... skip silently and never
// block") and let the user retry later, e.g. from Settings.
type OfflineError struct {
	Err error
}

func (e *OfflineError) Error() string {
	return fmt.Sprintf("fonts: offline: %v", e.Err)
}

func (e *OfflineError) Unwrap() error { return e.Err }

// PolicyError indicates a registry write was blocked, typically by Group
// Policy restricting HKCU edits for the current user. ManualSteps is text
// the UI can show verbatim so the user can install the font by hand.
type PolicyError struct {
	Err         error
	ManualSteps string
}

func (e *PolicyError) Error() string {
	return fmt.Sprintf("fonts: registry write blocked: %v", e.Err)
}

func (e *PolicyError) Unwrap() error { return e.Err }
