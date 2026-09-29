//go:build windows

package winapi

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NETRESOURCE constants, from winnetwk.h.
const (
	// resourceTypeDisk asks for a file share, not a printer.
	resourceTypeDisk = 1
	// connectTemporary makes the session last only until sign-out, and keeps
	// it out of the user's saved connections.
	connectTemporary = 0x00000004
)

// netResource mirrors NETRESOURCEW. Only Type and RemoteName are filled in.
type netResource struct {
	Scope       uint32
	Type        uint32
	DisplayType uint32
	Usage       uint32
	LocalName   *uint16
	RemoteName  *uint16
	Comment     *uint16
	Provider    *uint16
}

// mpr is the Windows multiple provider router, home of the WNet calls.
var (
	mpr                        = windows.NewLazySystemDLL("mpr.dll")
	procWNetAddConnection2W    = mpr.NewProc("WNetAddConnection2W")
	procWNetCancelConnection2W = mpr.NewProc("WNetCancelConnection2W")
)

// connectShare implements [ConnectShare] with WNetAddConnection2W, which
// takes the password as an argument in memory. `net use` would have put it
// in a command line any process of the same user could read.
func connectShare(remote, user, password string) error {
	r, err := windows.UTF16PtrFromString(remote)
	if err != nil {
		return err
	}
	u, err := windows.UTF16PtrFromString(user)
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(password)
	if err != nil {
		return err
	}
	nr := netResource{Type: resourceTypeDisk, RemoteName: r}
	code, _, _ := procWNetAddConnection2W.Call(
		uintptr(unsafe.Pointer(&nr)), uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(u)), uintptr(connectTemporary))
	// Keep every pointer reachable until the call has returned.
	runtime.KeepAlive(nr)
	runtime.KeepAlive(p)
	runtime.KeepAlive(u)
	if code != 0 {
		return syscall.Errno(code)
	}
	return nil
}

// disconnectShare implements [DisconnectShare] with WNetCancelConnection2W.
func disconnectShare(remote string, force bool) error {
	r, err := windows.UTF16PtrFromString(remote)
	if err != nil {
		return err
	}
	var f uintptr
	if force {
		f = 1
	}
	code, _, _ := procWNetCancelConnection2W.Call(uintptr(unsafe.Pointer(r)), 0, f)
	runtime.KeepAlive(r)
	if code != 0 {
		return syscall.Errno(code)
	}
	return nil
}
