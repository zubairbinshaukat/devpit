package winapi

import (
	"errors"
	"os"
)

// ErrNotAGroup is [SendCtrlBreak] asked to signal a group it must never
// signal: group 0 (every process on the console, Devpit and the user's shell
// included), a negative id, or Devpit's own process.
var ErrNotAGroup = errors.New("winapi: refusing to send Ctrl+Break to that process group")

// SendCtrlBreak sends CTRL_BREAK_EVENT to the console process group whose id
// is pid. The child must have been started with [CreateNewProcessGroup] and
// share this process's console; its group id is then its own pid. It refuses
// pid <= 0 and pid == os.Getpid() with [ErrNotAGroup]. Other platforms return
// [ErrUnsupported].
func SendCtrlBreak(pid int) error {
	if pid <= 0 || pid == os.Getpid() {
		return ErrNotAGroup
	}
	return sendCtrlBreak(pid)
}
