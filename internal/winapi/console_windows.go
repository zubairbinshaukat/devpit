//go:build windows

package winapi

import "golang.org/x/sys/windows"

// CreateNewProcessGroup is the CREATE_NEW_PROCESS_GROUP creation flag. A
// child started with it is the root of its own console process group, so a
// Ctrl+Break sent to that group reaches it and nothing else; it also ignores
// CTRL_C_EVENT, so the user's Ctrl+C stays Devpit's.
const CreateNewProcessGroup = windows.CREATE_NEW_PROCESS_GROUP

func sendCtrlBreak(pid int) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid)) //nolint:gosec // pid > 0 is checked by SendCtrlBreak and is a live process id within uint32 range.
}
