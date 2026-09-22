//go:build windows

package fonts

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// fontsKeyPath is the per-user font registry key. Writing here needs no
// admin rights, unlike HKLM.
const fontsKeyPath = `Software\Microsoft\Windows NT\CurrentVersion\Fonts`

type winRegistry struct{}

func newRegistry() Registry { return winRegistry{} }

func (winRegistry) Set(name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, fontsKeyPath, registry.SET_VALUE)
	if err != nil {
		return policyWrap(fmt.Errorf("open %s: %w", fontsKeyPath, err))
	}
	defer k.Close()
	if err := k.SetStringValue(name, value); err != nil {
		return policyWrap(fmt.Errorf("set %s: %w", name, err))
	}
	return nil
}

func (winRegistry) Delete(name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, fontsKeyPath, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open %s: %w", fontsKeyPath, err)
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("delete %s: %w", name, err)
	}
	return nil
}

func (winRegistry) Get(name string) (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, fontsKeyPath, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("open %s: %w", fontsKeyPath, err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get %s: %w", name, err)
	}
	return v, true, nil
}

// policyWrap turns an access-denied registry error into a *PolicyError
// with manual steps, so callers (Settings screen, installer prompt) can
// show the user what to do instead of a bare "access denied".
func policyWrap(err error) error {
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return &PolicyError{
			Err: err,
			ManualSteps: "Group Policy is blocking registry changes for your account. Ask an " +
				"administrator to allow it, or install the font manually: open " +
				`%LOCALAPPDATA%\Microsoft\Windows\Fonts, double-click ` + TargetFile +
				`, and choose Install.`,
		}
	}
	return err
}

// gdi32.dll and user32.dll are not wrapped by golang.org/x/sys/windows for
// the three calls Devpit needs, so they are declared here (same pattern as
// internal/ports/table_windows.go).
var (
	modGdi32  = windows.NewLazySystemDLL("gdi32.dll")
	modUser32 = windows.NewLazySystemDLL("user32.dll")

	procAddFontResourceW    = modGdi32.NewProc("AddFontResourceW")
	procRemoveFontResourceW = modGdi32.NewProc("RemoveFontResourceW")
	procSendMessageTimeoutW = modUser32.NewProc("SendMessageTimeoutW")
)

const (
	hwndBroadcast   = 0xffff // HWND_BROADCAST
	wmFontChange    = 0x001D // WM_FONTCHANGE
	smtoAbortIfHung = 0x0002 // SMTO_ABORTIFHUNG
	smtoTimeoutMS   = 3000
)

type winGDI struct{}

func newGDI() GDI { return winGDI{} }

func (winGDI) AddFontResource(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	r, _, callErr := procAddFontResourceW.Call(uintptr(unsafe.Pointer(p)))
	if r == 0 {
		return fmt.Errorf("AddFontResourceW: %w", callErr)
	}
	return nil
}

func (winGDI) RemoveFontResource(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	r, _, callErr := procRemoveFontResourceW.Call(uintptr(unsafe.Pointer(p)))
	if r == 0 {
		return fmt.Errorf("RemoveFontResourceW: %w", callErr)
	}
	return nil
}

func (winGDI) BroadcastFontChange() {
	var result uintptr
	// Best-effort: SMTO_ABORTIFHUNG keeps a hung top-level window from
	// blocking this call for longer than smtoTimeoutMS. Its return value
	// is intentionally ignored (see the GDI interface doc comment).
	_, _, _ = procSendMessageTimeoutW.Call(
		uintptr(hwndBroadcast),
		uintptr(wmFontChange),
		0,
		0,
		uintptr(smtoAbortIfHung),
		uintptr(smtoTimeoutMS),
		uintptr(unsafe.Pointer(&result)),
	)
}
