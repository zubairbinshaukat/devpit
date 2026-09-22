//go:build !windows

package tools

import "os/exec"

// noopTracker is the [processTracker] used outside Windows. RunStep's
// grandchild-keeps-the-pipe-open failure mode is specific to how Windows
// process trees and pipe handle inheritance work; elsewhere
// exec.CommandContext's own cancellation is enough for the commands RunStep
// runs.
type noopTracker struct{}

func newProcessTracker() processTracker { return noopTracker{} }

func (noopTracker) track(*exec.Cmd) error { return nil }

func (noopTracker) kill() {}
