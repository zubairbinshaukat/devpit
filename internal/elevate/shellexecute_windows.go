//go:build windows

package elevate

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// See/Mask flags from shellapi.h. Only the two this package needs.
const (
	seeMaskNoCloseProcess = 0x00000040 // fill in hProcess instead of closing it for us
	seeMaskFlagNoUI       = 0x00000400 // report failure via GetLastError, never a message box
)

// shellExecuteInfoW mirrors SHELLEXECUTEINFOW from shellapi.h field for
// field. Devpit calls ShellExecuteExW directly via a raw syscall rather than
// cgo, so this layout has to match the Win32 struct exactly; golang.org/x/sys
// does not expose it because ShellExecuteEx is COM-adjacent shell API, not a
// plain kernel call.
type shellExecuteInfoW struct {
	cbSize         uint32
	fMask          uint32
	hwnd           windows.Handle
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       windows.Handle
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      windows.Handle
	dwHotKey       uint32
	hIconOrMonitor windows.Handle
	hProcess       windows.Handle
}

// shellExecuteRunas launches exe with the "runas" verb — the one UAC prompt
// — hidden, passing params as its raw command-line parameter string. It
// waits only for ShellExecuteExW itself to return, not for the process to
// exit.
//
// If the user declines the UAC prompt, the returned error satisfies
// errors.Is(err, windows.ERROR_CANCELLED); [Launch] turns that into a
// [DeclinedError].
func shellExecuteRunas(exe, params string) error {
	dll := windows.NewLazySystemDLL("shell32.dll")
	if err := dll.Load(); err != nil {
		return fmt.Errorf("elevate: load shell32.dll: %w", err)
	}
	proc := dll.NewProc("ShellExecuteExW")
	if err := proc.Find(); err != nil {
		return fmt.Errorf("elevate: shell32.dll is missing ShellExecuteExW: %w", err)
	}

	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	var paramsPtr *uint16
	if params != "" {
		paramsPtr, err = windows.UTF16PtrFromString(params)
		if err != nil {
			return err
		}
	}

	info := shellExecuteInfoW{
		fMask:        seeMaskNoCloseProcess | seeMaskFlagNoUI,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: paramsPtr,
		nShow:        windows.SW_HIDE,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))

	r1, _, callErr := proc.Call(uintptr(unsafe.Pointer(&info)))
	if r1 == 0 {
		return callErr
	}
	if info.hProcess != 0 {
		_ = windows.CloseHandle(info.hProcess)
	}
	return nil
}
