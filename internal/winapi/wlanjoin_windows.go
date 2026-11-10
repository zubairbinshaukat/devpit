//go:build windows

package winapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The wlanapi.dll calls that scan, list profiles, and add, connect or delete
// a profile. They live here, apart from the read-only probe in
// wifi_windows.go, whose list TestWifiProbeCallsOnlyReadFunctions pins; this
// file reuses that file's DLL and its open, close and free calls, and
// TestJoinProcsLiveOutsideTheProbeFile pins this list.
var (
	procWlanScan                    = modWlanapi.NewProc("WlanScan")
	procWlanGetAvailableNetworkList = modWlanapi.NewProc("WlanGetAvailableNetworkList")
	procWlanGetProfileList          = modWlanapi.NewProc("WlanGetProfileList")
	procWlanSetProfile              = modWlanapi.NewProc("WlanSetProfile")
	procWlanConnect                 = modWlanapi.NewProc("WlanConnect")
	procWlanDeleteProfile           = modWlanapi.NewProc("WlanDeleteProfile")
)

const (
	// wlanProfileUser is WLAN_PROFILE_USER: a per-user profile, which a
	// standard user may add.
	wlanProfileUser = 0x2
	// wlanConnectionModeProfile is wlan_connection_mode_profile.
	wlanConnectionModeProfile = 0
	// errorBadProfile is ERROR_BAD_PROFILE.
	errorBadProfile = 1206
	// scanWait is how long a scan may take: drivers that meet the Windows
	// logo requirements finish a WlanScan within 4 seconds.
	scanWait = 4 * time.Second
	// joinTimeout bounds the wait for a connection; joinPoll is how often
	// the card is read meanwhile.
	joinTimeout = 30 * time.Second
	joinPoll    = 500 * time.Millisecond
	// maxWifiNetworks and maxWifiProfiles bound the lists Windows hands
	// back.
	maxWifiNetworks = 1024
	maxWifiProfiles = 1024
)

// wlanConnectionParameters is WLAN_CONNECTION_PARAMETERS: the connection
// mode, LPCWSTR strProfile, PDOT11_SSID, PDOT11_BSSID_LIST, DOT11_BSS_TYPE
// and DWORD flags. It holds pointers, so its layout depends on the pointer
// size; the checks below pin it for both.
type wlanConnectionParameters struct {
	Mode    uint32
	Profile *uint16
	SSID    *dot11SSID
	BSSIDs  unsafe.Pointer
	BSSType uint32
	Flags   uint32
}

func _() {
	const p = unsafe.Sizeof(uintptr(0))
	var x [1]struct{}
	_ = x[unsafe.Offsetof(wlanConnectionParameters{}.Profile)-p]
	_ = x[unsafe.Offsetof(wlanConnectionParameters{}.SSID)-2*p]
	_ = x[unsafe.Offsetof(wlanConnectionParameters{}.BSSIDs)-3*p]
	_ = x[unsafe.Offsetof(wlanConnectionParameters{}.BSSType)-4*p]
	_ = x[unsafe.Offsetof(wlanConnectionParameters{}.Flags)-(4*p+4)]
	_ = x[unsafe.Sizeof(wlanConnectionParameters{})-(4*p+8)]
}

// wlanSession opens a client handle for one card. The caller runs done.
func wlanSession(guid string) (windows.Handle, *windows.GUID, func(), error) {
	g, err := windows.GUIDFromString(guid)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("winapi: bad interface GUID: %w", err)
	}
	if err := modWlanapi.Load(); err != nil {
		return 0, nil, nil, fmt.Errorf("%w: %w", ErrNoWifi, err)
	}
	var negotiated uint32
	var h windows.Handle
	if r, _, _ := procWlanOpenHandle.Call(wlanClientVersion, 0,
		uintptr(unsafe.Pointer(&negotiated)), uintptr(unsafe.Pointer(&h))); r != 0 {
		return 0, nil, nil, fmt.Errorf("%w: WlanOpenHandle: %w", ErrNoWifi, windows.Errno(r))
	}
	return h, &g, func() { _, _, _ = procWlanCloseHandle.Call(uintptr(h), 0) }, nil
}

// wlanErr wraps a WLAN return code, marking access denied.
func wlanErr(call string, r uintptr) error {
	if windows.Errno(r) == windows.ERROR_ACCESS_DENIED {
		return fmt.Errorf("%w: %s: %w", ErrWifiDenied, call, windows.Errno(r))
	}
	return fmt.Errorf("%s: %w", call, windows.Errno(r))
}

// availableNetworks reads the list of nearby networks Windows already has.
// It never scans.
func availableNetworks(h windows.Handle, g *windows.GUID) ([]wlanAvailableNetwork, error) {
	var list *wlanAvailableNetworkList
	if r, _, _ := procWlanGetAvailableNetworkList.Call(uintptr(h), uintptr(unsafe.Pointer(g)), 0, 0,
		uintptr(unsafe.Pointer(&list))); r != 0 {
		return nil, wlanErr("WlanGetAvailableNetworkList", r)
	}
	if list == nil {
		return nil, nil
	}
	n := min(int(list.NumberOfItems), maxWifiNetworks)
	raw := make([]wlanAvailableNetwork, n)
	if n > 0 {
		copy(raw, unsafe.Slice(&list.Network[0], n))
	}
	wlanFree(unsafe.Pointer(list))
	return raw, nil
}

func wifiScan(guid string) ([]WifiNetwork, error) {
	h, g, done, err := wlanSession(guid)
	if err != nil {
		return nil, err
	}
	defer done()
	// A refused scan is not the end: the list Windows already has still
	// helps.
	if r, _, _ := procWlanScan.Call(uintptr(h), uintptr(unsafe.Pointer(g)), 0, 0, 0); r == 0 {
		time.Sleep(scanWait)
	}
	raw, err := availableNetworks(h, g)
	if err != nil {
		return nil, err
	}
	return decodeNetworks(raw), nil
}

func wifiVisible(guid, ssid string) (bool, error) {
	h, g, done, err := wlanSession(guid)
	if err != nil {
		return false, err
	}
	defer done()
	raw, err := availableNetworks(h, g)
	if err != nil {
		return false, err
	}
	return listsSSID(raw, ssid), nil
}

// wifiProfiles lists the saved profile names on the card.
func wifiProfiles(h windows.Handle, g *windows.GUID) ([]string, error) {
	var list *wlanProfileInfoList
	if r, _, _ := procWlanGetProfileList.Call(uintptr(h), uintptr(unsafe.Pointer(g)), 0,
		uintptr(unsafe.Pointer(&list))); r != 0 {
		return nil, wlanErr("WlanGetProfileList", r)
	}
	if list == nil {
		return nil, nil
	}
	n := min(int(list.NumberOfItems), maxWifiProfiles)
	names := make([]string, 0, n)
	if n > 0 {
		for _, p := range unsafe.Slice(&list.ProfileInfo[0], n) {
			names = append(names, utf16String(p.ProfileName[:]))
		}
	}
	wlanFree(unsafe.Pointer(list))
	return names, nil
}

func wifiHasProfile(guid, name string) (bool, error) {
	h, g, done, err := wlanSession(guid)
	if err != nil {
		return false, err
	}
	defer done()
	names, err := wifiProfiles(h, g)
	if err != nil {
		return false, err
	}
	for _, n := range names {
		if n == name { // profile names are case-sensitive
			return true, nil
		}
	}
	return false, nil
}

func wifiJoin(ctx context.Context, guid, ssid, pass string) error {
	doc, err := wifiProfileXML(ssid, pass)
	if err != nil {
		return err
	}
	h, g, done, err := wlanSession(guid)
	if err != nil {
		return err
	}
	defer done()
	xmlp, err := windows.UTF16PtrFromString(doc)
	if err != nil {
		return ErrBadPassphrase
	}
	var reason uint32
	r, _, _ := procWlanSetProfile.Call(uintptr(h), uintptr(unsafe.Pointer(g)), wlanProfileUser,
		uintptr(unsafe.Pointer(xmlp)), 0, 0 /* never overwrite */, 0, uintptr(unsafe.Pointer(&reason)))
	switch {
	case r == 0:
	case windows.Errno(r) == windows.ERROR_ALREADY_EXISTS:
		return ErrProfileExists
	case r == errorBadProfile:
		return fmt.Errorf("%w (reason %d)", ErrBadProfile, reason)
	default:
		return wlanErr("WlanSetProfile", r)
	}
	if err := wlanConnect(h, g, ssid); err != nil {
		return err
	}
	return waitConnected(ctx, h, g, guid, ssid)
}

func wifiConnectProfile(ctx context.Context, guid, profile string) error {
	h, g, done, err := wlanSession(guid)
	if err != nil {
		return err
	}
	defer done()
	if err := wlanConnect(h, g, profile); err != nil {
		return err
	}
	return waitConnected(ctx, h, g, guid, profile)
}

// wlanConnect asks the card to connect with a saved profile. The call only
// starts the connection; waitConnected sees it finish.
func wlanConnect(h windows.Handle, g *windows.GUID, profile string) error {
	p, err := windows.UTF16PtrFromString(profile)
	if err != nil {
		return ErrBadNetworkName
	}
	params := wlanConnectionParameters{
		Mode: wlanConnectionModeProfile, Profile: p, BSSType: dot11BSSTypeInfrastructure,
	}
	if r, _, _ := procWlanConnect.Call(uintptr(h), uintptr(unsafe.Pointer(g)),
		uintptr(unsafe.Pointer(&params)), 0); r != 0 {
		return wlanErr("WlanConnect", r)
	}
	return nil
}

// readConn reads the card once for waitConnected. When Windows will not name
// the profile, the nearby list (no scan) is read to name it instead.
func readConn(h windows.Handle, g *windows.GUID, guid string) connSample {
	var s connSample
	list, err := wifiInterfaces()
	if err != nil {
		return s
	}
	for _, w := range list {
		if strings.EqualFold(w.GUID, guid) {
			s.found, s.connected, s.profile = true, w.Connected, w.Profile
			break
		}
	}
	if s.connected && s.profile == "" {
		if raw, lerr := availableNetworks(h, g); lerr == nil {
			s.listRead, s.listProfile = true, connectedProfile(raw)
		}
	}
	return s
}

// waitConnected polls the card until it reports a connection with profile
// (see [connWatch] for a card whose profile Windows will not name),
// joinTimeout passes, or ctx ends. Polling needs no callback into Go from a
// Windows thread, which a notification would.
func waitConnected(ctx context.Context, h windows.Handle, g *windows.GUID, guid, profile string) error {
	w := connWatch{want: profile, start: time.Now()}
	deadline := w.start.Add(joinTimeout)
	t := time.NewTimer(joinPoll)
	defer t.Stop()
	for {
		if w.done(readConn(h, g, guid), time.Now()) {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrJoinTimeout
		}
		t.Reset(joinPoll)
		select {
		case <-ctx.Done():
			return fmt.Errorf("winapi: stopped waiting for the Wi-Fi connection: %w", ctx.Err())
		case <-t.C:
		}
	}
}

func wifiLeave(ctx context.Context, guid, tempProfile, previous string) error {
	h, g, done, err := wlanSession(guid)
	if err != nil {
		return err
	}
	defer done()
	// The delete is the part that matters and is quick: it runs even when
	// ctx has already ended.
	if tempProfile != "" {
		p, perr := windows.UTF16PtrFromString(tempProfile)
		if perr != nil {
			return ErrBadNetworkName
		}
		r, _, _ := procWlanDeleteProfile.Call(uintptr(h), uintptr(unsafe.Pointer(g)), uintptr(unsafe.Pointer(p)), 0)
		if r != 0 && windows.Errno(r) != windows.ERROR_NOT_FOUND {
			return wlanErr("WlanDeleteProfile", r)
		}
	}
	if previous == "" || previous == tempProfile {
		return nil
	}
	if cerr := ctx.Err(); cerr != nil {
		return fmt.Errorf("%w: %w", ErrReconnect, cerr)
	}
	names, err := wifiProfiles(h, g)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrReconnect, err)
	}
	found := false
	for _, n := range names {
		found = found || n == previous
	}
	if !found {
		return fmt.Errorf("%w: %w", ErrReconnect, ErrPreviousGone)
	}
	if err := wlanConnect(h, g, previous); err != nil {
		return fmt.Errorf("%w: %w", ErrReconnect, err)
	}
	if err := waitConnected(ctx, h, g, guid, previous); err != nil {
		return fmt.Errorf("%w: %w", ErrReconnect, err)
	}
	return nil
}

// hasWiredGateway implements [HasWiredGateway]: an Ethernet adapter that is
// up and has an IPv4 default gateway. Only the adapter table is read.
func hasWiredGateway() (bool, error) {
	size := uint32(15 << 10)
	var buf []byte
	var err error
	for range 4 {
		buf = make([]byte, size)
		err = windows.GetAdaptersAddresses(windows.AF_INET, gatewayFlags, 0,
			(*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil || !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			break
		}
	}
	if errors.Is(err, windows.ERROR_NO_DATA) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("GetAdaptersAddresses: %w", err)
	}
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		if a.IfType != windows.IF_TYPE_ETHERNET_CSMACD || a.OperStatus != windows.IfOperStatusUp {
			continue
		}
		for gw := a.FirstGatewayAddress; gw != nil; gw = gw.Next {
			if ip4 := gw.Address.IP().To4(); ip4 != nil && !ip4.IsUnspecified() {
				return true, nil
			}
		}
	}
	return false, nil
}
