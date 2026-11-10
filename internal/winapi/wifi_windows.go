//go:build windows

package winapi

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The read-only wlanapi.dll entry points, resolved lazily so nothing loads
// until a probe runs. Only read calls belong in this file: the join calls
// live elsewhere, and TestWifiProbeCallsOnlyReadFunctions pins the list.
var (
	modWlanapi                     = windows.NewLazySystemDLL("wlanapi.dll")
	procWlanOpenHandle             = modWlanapi.NewProc("WlanOpenHandle")
	procWlanCloseHandle            = modWlanapi.NewProc("WlanCloseHandle")
	procWlanEnumInterfaces         = modWlanapi.NewProc("WlanEnumInterfaces")
	procWlanGetInterfaceCapability = modWlanapi.NewProc("WlanGetInterfaceCapability")
	procWlanQueryInterface         = modWlanapi.NewProc("WlanQueryInterface")
	procWlanFreeMemory             = modWlanapi.NewProc("WlanFreeMemory")
)

// WLAN_INTF_OPCODE values used here (wlanapi.h).
const (
	wlanIntfOpcodeCurrentConnection = 7
	wlanIntfOpcodeChannelNumber     = 8
)

// wlanClientVersion 2 is the Vista-and-later client.
const wlanClientVersion = 2

// maxWifiInterfaces bounds the list Windows hands back.
const maxWifiInterfaces = 64

// wifiInterfaces implements [WifiInterfaces]. The client handle is opened and
// closed inside the call; nothing is kept between calls.
func wifiInterfaces() ([]WifiInterface, error) {
	if err := modWlanapi.Load(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoWifi, err)
	}
	var negotiated uint32
	var h windows.Handle
	// WLAN functions return the error code instead of setting GetLastError.
	if r, _, _ := procWlanOpenHandle.Call(wlanClientVersion, 0,
		uintptr(unsafe.Pointer(&negotiated)), uintptr(unsafe.Pointer(&h))); r != 0 {
		return nil, fmt.Errorf("%w: WlanOpenHandle: %w", ErrNoWifi, windows.Errno(r))
	}
	defer procWlanCloseHandle.Call(uintptr(h), 0) //nolint:errcheck // nothing to do on failure

	var list *wlanInterfaceInfoList
	if r, _, _ := procWlanEnumInterfaces.Call(uintptr(h), 0, uintptr(unsafe.Pointer(&list))); r != 0 {
		return nil, fmt.Errorf("WlanEnumInterfaces: %w", windows.Errno(r))
	}
	if list == nil {
		return nil, nil
	}
	n := min(int(list.NumberOfItems), maxWifiInterfaces)
	infos := make([]wlanInterfaceInfo, n)
	if n > 0 {
		copy(infos, unsafe.Slice(&list.InterfaceInfo[0], n))
	}
	wlanFree(unsafe.Pointer(list))

	out := make([]WifiInterface, 0, n)
	for _, info := range infos {
		out = append(out, decodeWifi(readWifi(h, info)))
	}
	return out, nil
}

// readWifi makes the per-card read calls. Each failure only leaves its part
// of wifiRaw empty.
func readWifi(h windows.Handle, info wlanInterfaceInfo) wifiRaw {
	raw := wifiRaw{info: info}
	guid := windows.GUID(info.InterfaceGUID)

	var caps *wlanInterfaceCapability
	if r, _, _ := procWlanGetInterfaceCapability.Call(uintptr(h), uintptr(unsafe.Pointer(&guid)), 0,
		uintptr(unsafe.Pointer(&caps))); r == 0 && caps != nil {
		c := *caps
		raw.caps = &c
		wlanFree(unsafe.Pointer(caps))
	}

	var conn *wlanConnectionAttributes
	if size, ok := wlanQuery(h, &guid, wlanIntfOpcodeCurrentConnection, unsafe.Pointer(&conn)); ok && conn != nil {
		if size >= uint32(unsafe.Sizeof(wlanConnectionAttributes{})) {
			c := *conn
			raw.conn = &c
		}
		wlanFree(unsafe.Pointer(conn))
	}

	var ch *uint32
	if size, ok := wlanQuery(h, &guid, wlanIntfOpcodeChannelNumber, unsafe.Pointer(&ch)); ok && ch != nil {
		if size >= 4 {
			raw.channel, raw.haveChannel = *ch, true
		}
		wlanFree(unsafe.Pointer(ch))
	}
	return raw
}

// wlanQuery calls WlanQueryInterface; out is the address of a typed pointer
// that receives WLAN-owned memory, which the caller frees.
func wlanQuery(h windows.Handle, guid *windows.GUID, opcode uint32, out unsafe.Pointer) (uint32, bool) {
	var size, valueType uint32
	r, _, _ := procWlanQueryInterface.Call(uintptr(h), uintptr(unsafe.Pointer(guid)), uintptr(opcode), 0,
		uintptr(unsafe.Pointer(&size)), uintptr(out), uintptr(unsafe.Pointer(&valueType)))
	return size, r == 0
}

// wlanFree releases memory the WLAN service allocated.
func wlanFree(p unsafe.Pointer) {
	if p != nil {
		_, _, _ = procWlanFreeMemory.Call(uintptr(p))
	}
}
