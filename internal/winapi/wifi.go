package winapi

import (
	"errors"
	"fmt"
	"unicode/utf16"
	"unsafe"
)

// WifiInterface is one Wi-Fi card as the Windows WLAN service sees it. Every
// field comes from a read-only call; nothing here scans, connects or changes
// a setting.
type WifiInterface struct {
	// GUID is the interface GUID as text, "{XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX}",
	// the same form as windows.GUID.String, so it matches the IP helper's
	// InterfaceGuid.
	GUID string
	// Description is the card's own name, the same text the IP helper gives.
	Description string
	// PHYs are the 802.11 PHY types the card supports (dot11PhyTypes), nil
	// when Windows would not say.
	PHYs []uint32
	// Connected is true when the WLAN service says the card is connected.
	Connected bool
	// PHY is the PHY type of the current link, 0 when not known.
	PHY uint32
	// RxKbps and TxKbps are the current link rates in kbit/s, 0 when not known.
	RxKbps, TxKbps uint32
	// Channel is the current channel number, 0 when not known.
	Channel uint32
	// Profile is the name of the profile the card is connected with, "" when
	// not known.
	Profile string
}

// 802.11 PHY types (DOT11_PHY_TYPE, windot11.h).
const (
	PHYOFDM   = 4  // 802.11a
	PHYERP    = 5  // 802.11g
	PHYHRDSSS = 6  // 802.11b
	PHYHT     = 7  // 802.11n, Wi-Fi 4
	PHYVHT    = 8  // 802.11ac, Wi-Fi 5
	PHYHE     = 10 // 802.11ax, Wi-Fi 6 and 6E
	PHYEHT    = 11 // 802.11be, Wi-Fi 7
)

// ErrNoWifi means the PC has no usable WLAN service: wlanapi.dll is missing
// or the WLAN AutoConfig service is not running. Callers treat it, like any
// other error from [WifiInterfaces], as "unknown", never as a failure to show.
var ErrNoWifi = errors.New("winapi: the Windows Wi-Fi service is not available")

// WifiInterfaces lists the Wi-Fi cards with their supported standards and,
// when Windows allows it, the current link. It only reads.
//
// A PC with the WLAN service running but no card returns an empty list and
// nil. No WLAN service returns nil and an error wrapping [ErrNoWifi]; other
// platforms return [ErrUnsupported]. If Windows refuses the current-link or
// channel query (access denied, for example with location turned off), the
// card is still listed: those fields stay zero and Connected comes from the
// interface state.
func WifiInterfaces() ([]WifiInterface, error) { return wifiInterfaces() }

// WifiStandard names the newest Wi-Fi generation in a PHY list: "Wi-Fi 7",
// "Wi-Fi 6", "Wi-Fi 5", "Wi-Fi 4", or "" for an older or empty list.
func WifiStandard(phys []uint32) string {
	best := ""
	rank := 0
	for _, p := range phys {
		r, name := 0, ""
		switch p {
		case PHYEHT:
			r, name = 4, "Wi-Fi 7"
		case PHYHE:
			r, name = 3, "Wi-Fi 6"
		case PHYVHT:
			r, name = 2, "Wi-Fi 5"
		case PHYHT:
			r, name = 1, "Wi-Fi 4"
		}
		if r > rank {
			rank, best = r, name
		}
	}
	return best
}

// IsWifi6OrNewer reports whether the PHY list holds HE (Wi-Fi 6/6E) or EHT
// (Wi-Fi 7).
func IsWifi6OrNewer(phys []uint32) bool {
	for _, p := range phys {
		if p == PHYHE || p == PHYEHT {
			return true
		}
	}
	return false
}

// The structures below mirror wlanapi.h exactly. Every member is 4-byte
// aligned and none holds a pointer, so the layout is the same on amd64,
// arm64 and 386. Sizes and offsets are pinned by wifiLayoutChecks.
// Docs: https://learn.microsoft.com/en-us/windows/win32/api/wlanapi/

// wlanGUID has windows.GUID's layout and field names, so the Windows file can
// convert it directly.
type wlanGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// String formats the GUID the way windows.GUID.String does.
func (g wlanGUID) String() string {
	return fmt.Sprintf("{%08X-%04X-%04X-%02X%02X-%02X%02X%02X%02X%02X%02X}",
		g.Data1, g.Data2, g.Data3, g.Data4[0], g.Data4[1],
		g.Data4[2], g.Data4[3], g.Data4[4], g.Data4[5], g.Data4[6], g.Data4[7])
}

// wlanMaxNameLength is WLAN_MAX_NAME_LENGTH.
const wlanMaxNameLength = 256

// wlanMaxPHYIndex is WLAN_MAX_PHY_INDEX.
const wlanMaxPHYIndex = 64

// wlanInterfaceStateConnected is wlan_interface_state_connected.
const wlanInterfaceStateConnected = 1

// wlanInterfaceInfo is WLAN_INTERFACE_INFO: GUID, WCHAR[256], enum (532 bytes).
type wlanInterfaceInfo struct {
	InterfaceGUID wlanGUID
	Description   [wlanMaxNameLength]uint16
	State         uint32
}

// wlanInterfaceInfoList is WLAN_INTERFACE_INFO_LIST: two DWORDs, then a
// variable-length array of WLAN_INTERFACE_INFO.
type wlanInterfaceInfoList struct {
	NumberOfItems uint32
	_             uint32 // dwIndex
	InterfaceInfo [1]wlanInterfaceInfo
}

// wlanInterfaceCapability is WLAN_INTERFACE_CAPABILITY: interfaceType,
// BOOL bDot11DSupported, three DWORDs, DOT11_PHY_TYPE[64] (276 bytes).
type wlanInterfaceCapability struct {
	_            uint32 // interfaceType
	_            uint32 // bDot11DSupported
	_            uint32 // dwMaxDesiredSsidListSize
	_            uint32 // dwMaxDesiredBssidListSize
	NumberOfPHYs uint32
	PHYTypes     [wlanMaxPHYIndex]uint32
}

// wlanAssociationAttributes is WLAN_ASSOCIATION_ATTRIBUTES (68 bytes):
// DOT11_SSID (ULONG + UCHAR[32]), DOT11_BSS_TYPE, DOT11_MAC_ADDRESS (6
// bytes, then 2 bytes of padding before the 4-byte enum), DOT11_PHY_TYPE,
// uDot11PhyIndex, wlanSignalQuality, ulRxRate, ulTxRate (kbit/s).
type wlanAssociationAttributes struct {
	_       uint32   // dot11Ssid.uSSIDLength
	_       [32]byte // dot11Ssid.ucSSID
	_       uint32   // dot11BssType
	_       [6]byte  // dot11Bssid
	_       [2]byte  // padding
	PHYType uint32
	_       uint32 // uDot11PhyIndex
	_       uint32 // wlanSignalQuality
	RxRate  uint32
	TxRate  uint32
}

// wlanConnectionAttributes is WLAN_CONNECTION_ATTRIBUTES (604 bytes): state,
// connection mode, WCHAR[256] profile name, association attributes, and
// WLAN_SECURITY_ATTRIBUTES (BOOL, BOOL, two enums: 16 bytes).
type wlanConnectionAttributes struct {
	State       uint32
	_           uint32 // wlanConnectionMode
	ProfileName [wlanMaxNameLength]uint16
	Association wlanAssociationAttributes
	_           [4]uint32 // wlanSecurityAttributes
}

// wifiLayoutChecks fails to compile if a size or offset drifts from
// wlanapi.h: a constant index outside [1]struct{} is a compile error, and a
// negative uintptr constant is too.
func _() {
	var x [1]struct{}
	_ = x[unsafe.Sizeof(wlanGUID{})-16]
	_ = x[unsafe.Sizeof(wlanInterfaceInfo{})-532]
	_ = x[unsafe.Offsetof(wlanInterfaceInfo{}.State)-528]
	_ = x[unsafe.Offsetof(wlanInterfaceInfoList{}.InterfaceInfo)-8]
	_ = x[unsafe.Sizeof(wlanInterfaceCapability{})-276]
	_ = x[unsafe.Offsetof(wlanInterfaceCapability{}.PHYTypes)-20]
	_ = x[unsafe.Sizeof(wlanAssociationAttributes{})-68]
	_ = x[unsafe.Offsetof(wlanAssociationAttributes{}.PHYType)-48]
	_ = x[unsafe.Offsetof(wlanAssociationAttributes{}.RxRate)-60]
	_ = x[unsafe.Offsetof(wlanAssociationAttributes{}.TxRate)-64]
	_ = x[unsafe.Sizeof(wlanConnectionAttributes{})-604]
	_ = x[unsafe.Offsetof(wlanConnectionAttributes{}.ProfileName)-8]
	_ = x[unsafe.Offsetof(wlanConnectionAttributes{}.Association)-520]
}

// wifiRaw is what the Windows calls returned for one card. A nil pointer or
// a false flag means the call failed or was refused.
type wifiRaw struct {
	info        wlanInterfaceInfo
	caps        *wlanInterfaceCapability
	conn        *wlanConnectionAttributes
	channel     uint32
	haveChannel bool
}

// decodeWifi turns one card's raw answers into a [WifiInterface]. A refused
// link or channel query leaves those fields zero; Connected then comes from
// the interface state alone.
func decodeWifi(r wifiRaw) WifiInterface {
	w := WifiInterface{
		GUID:        r.info.InterfaceGUID.String(),
		Description: utf16String(r.info.Description[:]),
		Connected:   r.info.State == wlanInterfaceStateConnected,
	}
	if r.caps != nil {
		n := min(int(r.caps.NumberOfPHYs), wlanMaxPHYIndex)
		if n > 0 {
			w.PHYs = append([]uint32(nil), r.caps.PHYTypes[:n]...)
		}
	}
	if r.conn != nil && r.conn.State == wlanInterfaceStateConnected {
		w.Connected = true
		w.Profile = utf16String(r.conn.ProfileName[:])
		w.PHY = r.conn.Association.PHYType
		w.RxKbps = r.conn.Association.RxRate
		w.TxKbps = r.conn.Association.TxRate
	}
	if r.haveChannel && w.Connected {
		w.Channel = r.channel
	}
	return w
}

// utf16String decodes a NUL-terminated WCHAR array.
func utf16String(u []uint16) string {
	for i, c := range u {
		if c == 0 {
			u = u[:i]
			break
		}
	}
	return string(utf16.Decode(u))
}
