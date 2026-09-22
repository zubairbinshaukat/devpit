package gitssh

import (
	"fmt"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

// CopyWin32 puts text on the Windows system clipboard as CF_UNICODETEXT,
// using OpenClipboard/EmptyClipboard/SetClipboardData from user32.dll and
// GlobalAlloc/GlobalLock/GlobalUnlock/RtlMoveMemory from kernel32.dll,
// loaded lazily via golang.org/x/sys/windows (no cgo).
//
// The copy into the GlobalAlloc'd block goes through RtlMoveMemory (an
// exported kernel32 alias for memmove) rather than casting the block's
// address to a Go pointer: GlobalLock hands back a raw OS-owned address
// with no relation to any Go allocation, and building an unsafe.Pointer
// from that bare uintptr is exactly the invalid pattern `go vet`'s
// unsafeptr check exists to catch.
func CopyWin32(text string) error {
	user32 := windows.NewLazySystemDLL("user32.dll")
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")

	openClipboard := user32.NewProc("OpenClipboard")
	closeClipboard := user32.NewProc("CloseClipboard")
	emptyClipboard := user32.NewProc("EmptyClipboard")
	setClipboardData := user32.NewProc("SetClipboardData")
	globalAlloc := kernel32.NewProc("GlobalAlloc")
	globalLock := kernel32.NewProc("GlobalLock")
	globalUnlock := kernel32.NewProc("GlobalUnlock")
	moveMemory := kernel32.NewProc("RtlMoveMemory")

	r, _, err := openClipboard.Call(0)
	if r == 0 {
		return fmt.Errorf("gitssh: OpenClipboard: %w", err)
	}
	defer closeClipboard.Call() //nolint:errcheck // best-effort cleanup; nothing actionable if it fails.

	r, _, err = emptyClipboard.Call()
	if r == 0 {
		return fmt.Errorf("gitssh: EmptyClipboard: %w", err)
	}

	units := utf16.Encode([]rune(text))
	units = append(units, 0) // NUL terminator required by CF_UNICODETEXT
	size := uintptr(len(units)) * 2

	hMem, _, err := globalAlloc.Call(gmemMoveable, size)
	if hMem == 0 {
		return fmt.Errorf("gitssh: GlobalAlloc: %w", err)
	}

	dst, _, err := globalLock.Call(hMem)
	if dst == 0 {
		return fmt.Errorf("gitssh: GlobalLock: %w", err)
	}
	moveMemory.Call(dst, uintptr(unsafe.Pointer(&units[0])), size) //nolint:errcheck // RtlMoveMemory has no error return.
	globalUnlock.Call(hMem)                                        //nolint:errcheck // GlobalUnlock's own failure is not actionable here.

	r, _, err = setClipboardData.Call(cfUnicodeText, hMem)
	if r == 0 {
		return fmt.Errorf("gitssh: SetClipboardData: %w", err)
	}
	return nil
}
