//go:build windows

package clean

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 constants for SHFileOperationW. FOF_ALLOWUNDO is what sends the item
// to the Recycle Bin instead of deleting it outright.
const (
	shFileOpDelete         = 0x0003 // FO_DELETE
	shFileOpAllowUndo      = 0x0040 // FOF_ALLOWUNDO
	shFileOpNoConfirmation = 0x0010 // FOF_NOCONFIRMATION
	shFileOpSilent         = 0x0004 // FOF_SILENT
	shFileOpNoErrorUI      = 0x0400 // FOF_NOERRORUI
)

var (
	shell32DLL       = windows.NewLazySystemDLL("shell32.dll")
	shFileOperationW = shell32DLL.NewProc("SHFileOperationW")
)

// shFileOpStruct mirrors the Win32 SHFILEOPSTRUCTW structure. Field order and
// widths follow the natural (8-byte pointer, 4-byte alignment) layout of the
// real struct on amd64: hwnd(8) wFunc(4)+pad(4) pFrom(8) pTo(8) fFlags(2)+
// fAnyOperationsAborted needs 4-byte alignment so there are 2 pad bytes,
// fAnyOperationsAborted(4) hNameMappings(8) lpszProgressTitle(8) = 56 bytes,
// matching sizeof(SHFILEOPSTRUCTW).
type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 uintptr
	pTo                   uintptr
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     uintptr
}

// moveToTrash sends path to the Windows Recycle Bin via SHFileOperationW.
//
// This is a hand-rolled replacement for github.com/rafshawn/go2trash's
// Windows implementation (trash_windows.go), which builds the struct's pFrom
// field as uintptr(unsafe.Pointer(&buf[0])) and never keeps buf alive past
// that point. Once a pointer has been narrowed to a uintptr and stashed in a
// struct field, the Go compiler no longer sees it as a pointer, so buf is
// only kept alive by its own last statement of use -- which is that same
// assignment. Nothing about passing the *struct* to Call keeps buf alive:
// the compiler's uintptr-keepalive/escapes handling (see runtime/syscall's
// //go:uintptrkeepalive) only protects the direct call argument, here the
// struct pointer, not values reachable through one of the struct's own
// uintptr fields.
//
// The result is that buf's backing array can be collected while
// SHFileOperationW is still reading from it, if a GC cycle happens to land in
// that window. That window is normally microseconds, which is why this only
// ever showed up once, as a fatal crash, during a `go test ./...` run with
// every package's tests allocating in parallel (far more GC pressure than
// this package's own tests produce alone). runtime.KeepAlive below pins wide
// until SHFileOperationW has returned, closing that window.
func moveToTrash(path string) error {
	// SHFileOperationW wants the path as a double-null-terminated UTF-16
	// string.
	wide, err := windows.UTF16FromString(path)
	if err != nil {
		return fmt.Errorf("clean: invalid path for the Recycle Bin: %w", err)
	}
	wide = append(wide, 0)

	op := &shFileOpStruct{
		wFunc:  shFileOpDelete,
		pFrom:  uintptr(unsafe.Pointer(&wide[0])),
		fFlags: shFileOpAllowUndo | shFileOpNoConfirmation | shFileOpSilent | shFileOpNoErrorUI,
	}
	ret, _, callErr := shFileOperationW.Call(uintptr(unsafe.Pointer(op)))
	// wide is only reachable through op.pFrom, a uintptr the garbage
	// collector cannot see as a pointer, and wide is not otherwise used
	// below this line. Without this, the compiler would be free to treat
	// wide as dead as soon as pFrom was computed above, before
	// SHFileOperationW is done reading it.
	runtime.KeepAlive(wide)
	if ret != 0 {
		if callErr != nil && !errors.Is(callErr, windows.ERROR_SUCCESS) {
			return fmt.Errorf("clean: SHFileOperationW failed (code %d): %w", ret, callErr)
		}
		return fmt.Errorf("clean: SHFileOperationW failed with code %d", ret)
	}
	return nil
}
