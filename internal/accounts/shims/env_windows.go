//go:build windows

package shims

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// RegistryPath is a PathStore on a registry value. UserPath is the real
// one; tests point it at a scratch key under HKCU\Software.
type RegistryPath struct {
	Root  registry.Key
	Key   string
	Value string
}

// UserPath is HKCU\Environment\Path: the user's own PATH, which needs no
// administrator rights to change.
func UserPath() PathStore {
	return RegistryPath{Root: registry.CURRENT_USER, Key: "Environment", Value: "Path"}
}

// Read returns the stored value without expanding it.
func (r RegistryPath) Read() (string, bool, bool, error) {
	k, err := registry.OpenKey(r.Root, r.Key, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", true, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	defer k.Close() //nolint:errcheck // read-only
	v, typ, err := k.GetStringValue(r.Value)
	if errors.Is(err, registry.ErrNotExist) {
		return "", true, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	return v, typ == registry.EXPAND_SZ, true, nil
}

// Write stores value as REG_EXPAND_SZ or REG_SZ.
func (r RegistryPath) Write(value string, expand bool) error {
	k, _, err := registry.CreateKey(r.Root, r.Key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close() //nolint:errcheck // closing after a write that already succeeded or failed
	if expand {
		return k.SetExpandStringValue(r.Value, value)
	}
	return k.SetStringValue(r.Value, value)
}

// BroadcastEnvironment sends WM_SETTINGCHANGE "Environment" to every
// top-level window, the way the System control panel does, so Explorer
// (and every terminal started from it from now on) sees the new PATH. It
// waits at most two seconds for windows that do not answer.
func BroadcastEnvironment() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
	)
	user32 := windows.NewLazySystemDLL("user32.dll")
	send := user32.NewProc("SendMessageTimeoutW")
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	_, _, _ = send.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)),
		smtoAbortIfHung, 2000, uintptr(unsafe.Pointer(&result)))
}
