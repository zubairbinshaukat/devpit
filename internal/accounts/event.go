package accounts

import "errors"

// StepState is the state of one live row on an Applying or Sign-in screen.
type StepState string

const (
	StepRunning StepState = "running"
	StepWaiting StepState = "waiting" // for the person, in the browser
	StepDone    StepState = "done"
	StepFailed  StepState = "failed"
	StepInfo    StepState = "info"
	StepWarning StepState = "warning"
)

// Event is one step of a long-running engine operation (sign in, apply,
// verify), sent on a channel that a screen turns into tea messages. Every
// text field is scrubbed by [NewEvent], so tool output that passes through
// an event can never carry a token to the screen or a log.
type Event struct {
	// Step names what is happening, in plain words: "Checking who is
	// signed in".
	Step string
	// State is the row's state.
	State StepState
	// Detail is extra text: a line of tool output, a path, a reason.
	Detail string
	// Err is set when State is StepFailed. Its text is scrubbed.
	Err error
	// Final is set on the last event; the channel closes after it.
	Final bool
	// Account is set on the final event of a successful sign-in: the
	// account to save (not yet saved).
	Account *Account
	// Identity is set when the step found out who is signed in.
	Identity *Identity
	// EntryID is the journal entry of a change that was applied, for undo.
	EntryID string
}

// NewEvent builds an event with Step and Detail scrubbed.
func NewEvent(step string, state StepState, detail string) Event {
	return Event{Step: Scrub(step), State: state, Detail: Scrub(detail)}
}

// Failed builds a final failure event. The error's text is scrubbed and the
// original error is not kept, so nothing it wrapped can leak through
// errors.Unwrap; errors.Is still works against the sentinel errors this
// package and the adapters define, through ScrubError.
func Failed(step string, err error) Event {
	ev := NewEvent(step, StepFailed, "")
	ev.Err = ScrubError(err)
	ev.Final = true
	return ev
}

// scrubbedError carries a scrubbed message and, for errors.Is, only the
// sentinel errors it was built from.
type scrubbedError struct {
	msg   string
	kinds []error
}

func (e *scrubbedError) Error() string   { return e.msg }
func (e *scrubbedError) Unwrap() []error { return e.kinds }

// sentinels are the errors that callers test with errors.Is. A scrubbed
// error keeps a link to these and to nothing else.
func sentinels() []error {
	return []error{
		ErrLocked, ErrNewerSchema, ErrReservedName, ErrInvalidName, ErrNameTaken,
		ErrRelativePath, ErrNothingToUndo, ErrChangedByHand, ErrStalePreview,
		ErrNotFoundTool, ErrTimeout, ErrTooOld, ErrNotSupported, ErrSignInCancelled,
	}
}

// ScrubError returns an error whose text is err's text scrubbed, and which
// still matches (errors.Is) any sentinel error err matched. nil stays nil.
func ScrubError(err error) error {
	if err == nil {
		return nil
	}
	out := &scrubbedError{msg: Scrub(err.Error())}
	for _, s := range sentinels() {
		if errors.Is(err, s) {
			out.kinds = append(out.kinds, s)
		}
	}
	return out
}

// Sentinel errors shared by the engine and the adapters.
var (
	// ErrNotFoundTool: the tool is not installed (not on PATH).
	ErrNotFoundTool = errors.New("the tool is not installed")
	// ErrTimeout: a tool did not answer in time.
	ErrTimeout = errors.New("the tool did not answer in time")
	// ErrTooOld: the installed tool lacks a feature Devpit relies on.
	ErrTooOld = errors.New("the installed version is too old for this")
	// ErrNotSupported: Devpit does not do this for this tool.
	ErrNotSupported = errors.New("not something Devpit does for this tool")
	// ErrSignInCancelled: a sign-in did not finish; nothing was saved.
	ErrSignInCancelled = errors.New("sign-in did not finish, so nothing was saved")
)
