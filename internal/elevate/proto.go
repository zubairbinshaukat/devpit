// Package elevate runs admin-only work in a separate, hidden, elevated
// worker process instead of ever elevating Devpit's own TUI. The TUI creates
// a named pipe, launches "devpit --elevated-worker --pipe <name>" through
// ShellExecuteExW's "runas" verb (the one UAC prompt), and then talks to the
// worker over the pipe using one JSON object per line in each direction.
//
// The worker side ([Serve]) has no UI dependency at all: no Bubble Tea, no
// lipgloss, no internal/ui or internal/app import. That is safety.md rule
// 18 — the TUI never runs elevated — and this package is how it is kept
// true: nothing that can open a window or a screen ever runs as admin.
package elevate

// Kind identifies the type of job a [Request] asks the worker to run. The
// worker refuses anything outside this list.
type Kind string

const (
	// KindExec runs a command to completion, streaming its stdout and
	// stderr back as "line" events.
	KindExec Kind = "exec"
	// KindRemove deletes a path. The worker only allows paths under a
	// small set of admin-only cleanup roots; see [validateRemovePath].
	KindRemove Kind = "remove"
	// KindShutdown asks the worker to stop serving and exit its loop. It
	// gets no reply; the connection simply ends.
	KindShutdown Kind = "shutdown"
)

// Request is one job sent from the TUI to the worker, encoded as a single
// line of JSON.
type Request struct {
	// ID correlates the worker's "line" and "done" events back to this
	// request. It is set by the client and echoed by the worker. Shutdown
	// requests leave it empty.
	ID string `json:"id,omitempty"`
	// Kind selects which job this is.
	Kind Kind `json:"kind"`
	// Argv is the command and its arguments, for KindExec.
	Argv []string `json:"argv,omitempty"`
	// Path is the target to delete, for KindRemove.
	Path string `json:"path,omitempty"`
	// TimeoutSec bounds how long an exec job may run. Zero means no
	// timeout beyond the caller's context.
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

// EventType names the kind of message the worker streams back to the TUI.
type EventType string

const (
	// EventHello is the first line the worker ever sends, announcing its
	// PID and whether its token is actually elevated.
	EventHello EventType = "hello"
	// EventLine carries one line of a running job's output.
	EventLine EventType = "line"
	// EventDone marks a job finished, successfully or not.
	EventDone EventType = "done"
)

// Event is one message from the worker to the TUI, encoded as a single line
// of JSON.
type Event struct {
	// ID matches the [Request.ID] this event belongs to. Empty for hello.
	ID string `json:"id,omitempty"`
	// Event says which of the three shapes this is.
	Event EventType `json:"event"`
	// Stream is "stdout" or "stderr", for EventLine.
	Stream string `json:"stream,omitempty"`
	// Text is the line of output, for EventLine.
	Text string `json:"text,omitempty"`
	// ExitCode is the process exit code, for EventDone. -1 means the job
	// never produced a real exit code (refused, or failed to start).
	ExitCode int `json:"exit_code,omitempty"`
	// Err is a human-readable failure reason, for EventDone. Empty means
	// the job succeeded.
	Err string `json:"err,omitempty"`
	// PID is the worker's process ID, for EventHello.
	PID int `json:"pid,omitempty"`
	// Elevated reports whether the worker's token is actually elevated,
	// for EventHello.
	Elevated bool `json:"elevated,omitempty"`
}
