package winapi

import (
	"context"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"
)

// WifiNetwork is one nearby network from a scan, merged by name: Windows
// lists a network once per saved profile plus once without, and this keeps
// one entry per SSID.
type WifiNetwork struct {
	// SSID is the network name as broadcast.
	SSID string
	// Signal is the signal quality, 0 to 100 (the strongest entry seen).
	Signal uint32
	// HasProfile is true when this user already has a saved Wi-Fi profile
	// for the network, and Profile is its name.
	HasProfile bool
	Profile    string
	// Connected is true when the card is on this network now.
	Connected bool
	// Secured is true when the network needs a key. Auth is the
	// DOT11_AUTH_ALGORITHM value and Security its plain name.
	Secured  bool
	Auth     uint32
	Security string
}

// Errors from the join calls. Callers match them with errors.Is.
var (
	// ErrWifiDenied means Windows refused the call, for example a scan with
	// location access turned off.
	ErrWifiDenied = errors.New("winapi: Windows did not allow this Wi-Fi call")
	// ErrProfileExists means a saved profile of that name is already there.
	// [WifiJoin] never overwrites one.
	ErrProfileExists = errors.New("winapi: a Wi-Fi profile with this name already exists")
	// ErrBadProfile means Windows refused the profile Devpit built.
	ErrBadProfile = errors.New("winapi: Windows refused the Wi-Fi profile")
	// ErrBadNetworkName and ErrBadPassphrase mean the input cannot make a
	// WPA2-Personal profile: a name of 1 to 32 bytes with no control
	// character, a passphrase of 8 to 63 printable ASCII characters.
	ErrBadNetworkName = errors.New("winapi: the network name must be 1 to 32 bytes with no control characters")
	ErrBadPassphrase  = errors.New("winapi: the network password must be 8 to 63 printable characters")
	// ErrJoinTimeout means the card did not report the connection in time.
	// A wrong password looks like this too: Windows reports it only through
	// a notification, which Devpit does not register for.
	ErrJoinTimeout = errors.New("winapi: the Wi-Fi connection did not come up in time")
	// ErrReconnect wraps every failure of [WifiLeave] that happens after
	// the profile was deleted: the delete is done, only the reconnect failed.
	ErrReconnect = errors.New("winapi: could not reconnect to the previous Wi-Fi network")
	// ErrPreviousGone means the previous network's profile no longer exists.
	ErrPreviousGone = errors.New("winapi: the previous Wi-Fi profile no longer exists")
)

// WifiScan asks the card to scan, waits the few seconds a scan takes, and
// lists the nearby networks, strongest first. A refused scan still returns
// what Windows already knew; a refused list wraps [ErrWifiDenied].
func WifiScan(guid string) ([]WifiNetwork, error) { return wifiScan(guid) }

// WifiVisible reports whether a network named ssid is in the list of nearby
// networks Windows already has. It never asks the card to scan, so it is
// cheap and changes nothing: Windows refreshes that list with its own
// background scans. A refused list (for example with location access off)
// wraps [ErrWifiDenied], and the caller then does not know.
func WifiVisible(guid, ssid string) (bool, error) { return wifiVisible(guid, ssid) }

// WifiHasProfile reports whether this user has a saved Wi-Fi profile called
// name on the card. It only reads.
func WifiHasProfile(guid, name string) (bool, error) { return wifiHasProfile(guid, name) }

// WifiJoin saves a per-user WPA2-Personal profile named after ssid, with
// manual connection mode so Windows never rejoins it by itself, connects
// with it, and waits until the card is on it, or ctx ends (the error then
// wraps ctx.Err()). It never overwrites a saved profile ([ErrProfileExists]).
// On failure the profile may be left behind; the caller removes it with
// [WifiLeave].
//
// When Windows will not name the profile the card is on (location access
// off), the join counts as done once the card reports a connection after
// the connect request; the link's rates then stay unknown.
func WifiJoin(ctx context.Context, guid, ssid, pass string) error {
	return wifiJoin(ctx, guid, ssid, pass)
}

// WifiConnectProfile connects with a profile that is already saved, and
// waits until the card is on it or ctx ends. No password is involved.
func WifiConnectProfile(ctx context.Context, guid, profile string) error {
	return wifiConnectProfile(ctx, guid, profile)
}

// WifiLeave deletes tempProfile when it is not "" (a profile that is already
// gone is fine), then reconnects to previous when it is not "" and still
// exists. The delete comes first and is not cut short by ctx; the wait for
// the reconnect ends with ctx. Failures after the delete, a cancelled wait
// included, wrap [ErrReconnect], and a missing previous profile also wraps
// [ErrPreviousGone].
func WifiLeave(ctx context.Context, guid, tempProfile, previous string) error {
	return wifiLeave(ctx, guid, tempProfile, previous)
}

// CheckNetworkName reports whether s can be a Wi-Fi network name Devpit
// makes a profile for: 1 to 32 bytes of valid UTF-8 with no control
// character. A failure is [ErrBadNetworkName].
func CheckNetworkName(s string) error { return checkNetworkName(s) }

// HasWiredGateway reports whether an Ethernet adapter that is up has an IPv4
// default gateway: a PC on a cable. It only reads the adapter table.
func HasWiredGateway() (bool, error) { return hasWiredGateway() }

// connSample is one read of the card while waiting for a connection.
type connSample struct {
	// found is false when the card was not in the list (or the read failed).
	found bool
	// connected is the interface state; profile is the profile the card is
	// on, "" when Windows would not say.
	connected bool
	profile   string
	// listRead is true when the nearby list (no scan) could be read to name
	// the connected network after all; listProfile is the profile of the
	// entry it marks connected, "" for none.
	listRead    bool
	listProfile string
}

// unknownProfileSettle is how long a card must report a connection, without
// Windows naming the profile and without being seen leaving its old link,
// before the connection is taken as the one asked for. Associating takes a
// second or more, so a card that switched is nearly always seen off-line
// in between; this covers one that switched between two reads.
const unknownProfileSettle = 5 * time.Second

// connWatch decides, read by read, whether the card is on the profile asked
// for. It is pure, so the rule is tested without a card.
type connWatch struct {
	want  string
	start time.Time
	// left is true once the card was seen not connected after the connect
	// request.
	left bool
}

// done reports whether s shows the card connected with w.want. A named
// profile must match. When Windows names none (opcode 7 refused, as with
// location access off on Windows 11 24H2, or no data), the nearby list
// decides when it can be read; else a connected state counts once the card
// was seen leaving its old link or after [unknownProfileSettle], since
// WlanConnect itself succeeded.
func (w *connWatch) done(s connSample, now time.Time) bool {
	if !s.found {
		return false
	}
	if !s.connected {
		w.left = true
		return false
	}
	if s.profile != "" {
		return s.profile == w.want
	}
	if s.listRead && s.listProfile != "" {
		return s.listProfile == w.want
	}
	return w.left || now.Sub(w.start) >= unknownProfileSettle
}

// wifiProfileXML builds the WLAN profile for a WPA2-Personal network
// (https://learn.microsoft.com/en-us/windows/win32/nativewifi/wlan-profileschema-elements).
// The profile is named after the SSID; the SSID also travels as hex, which
// Windows prefers over the name, so any byte in it is matched exactly.
// Every text value is XML-escaped.
func wifiProfileXML(ssid, pass string) (string, error) {
	if err := checkNetworkName(ssid); err != nil {
		return "", err
	}
	if len(pass) < 8 || len(pass) > 63 {
		return "", ErrBadPassphrase
	}
	for i := range len(pass) {
		if pass[i] < 0x20 || pass[i] > 0x7e {
			return "", ErrBadPassphrase
		}
	}
	esc := func(s string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	name := esc(ssid)
	return `<?xml version="1.0"?>
<WLANProfile xmlns="http://www.microsoft.com/networking/WLAN/profile/v1">
	<name>` + name + `</name>
	<SSIDConfig>
		<SSID>
			<hex>` + strings.ToUpper(hex.EncodeToString([]byte(ssid))) + `</hex>
			<name>` + name + `</name>
		</SSID>
	</SSIDConfig>
	<connectionType>ESS</connectionType>
	<connectionMode>manual</connectionMode>
	<MSM>
		<security>
			<authEncryption>
				<authentication>WPA2PSK</authentication>
				<encryption>AES</encryption>
				<useOneX>false</useOneX>
			</authEncryption>
			<sharedKey>
				<keyType>passPhrase</keyType>
				<protected>false</protected>
				<keyMaterial>` + esc(pass) + `</keyMaterial>
			</sharedKey>
		</security>
	</MSM>
</WLANProfile>
`, nil
}

// checkNetworkName accepts what a DOT11_SSID can hold and a profile name can
// carry: 1 to 32 bytes of valid UTF-8 with no control character.
func checkNetworkName(s string) error {
	if s == "" || len(s) > 32 || !utf8.ValidString(s) {
		return ErrBadNetworkName
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return ErrBadNetworkName
		}
	}
	return nil
}

// wifiAuthName names a DOT11_AUTH_ALGORITHM value.
func wifiAuthName(a uint32) string {
	switch a {
	case 1:
		return "Open"
	case 2:
		return "Shared key"
	case 3:
		return "WPA-Enterprise"
	case 4:
		return "WPA-Personal"
	case 6:
		return "WPA2-Enterprise"
	case 7:
		return "WPA2-Personal"
	case 8, 11:
		return "WPA3-Enterprise"
	case 9:
		return "WPA3-Personal"
	case 10:
		return "OWE"
	}
	return ""
}

// wlanMaxPHYTypeNumber is WLAN_MAX_PHY_TYPE_NUMBER.
const wlanMaxPHYTypeNumber = 8

// WLAN_AVAILABLE_NETWORK dwFlags.
const (
	wlanAvailableNetworkConnected  = 0x1
	wlanAvailableNetworkHasProfile = 0x2
)

// dot11BSSTypeInfrastructure is dot11_BSS_type_infrastructure.
const dot11BSSTypeInfrastructure = 1

// The structures below mirror wlanapi.h; none holds a pointer and every
// member is 4-byte aligned, so the layout is the same on every architecture.
// Sizes and offsets are pinned by the checks after them.

// dot11SSID is DOT11_SSID: ULONG length, UCHAR[32] (36 bytes).
type dot11SSID struct {
	Length uint32
	SSID   [32]byte
}

// wlanAvailableNetwork is WLAN_AVAILABLE_NETWORK (628 bytes): WCHAR[256]
// profile name, DOT11_SSID, BSS type, number of BSSIDs, connectable flag and
// reason, PHY count and DOT11_PHY_TYPE[8], more-PHYs flag, signal quality,
// security flag, auth and cipher algorithms, flags, reserved.
type wlanAvailableNetwork struct {
	ProfileName     [wlanMaxNameLength]uint16
	SSID            dot11SSID
	BSSType         uint32
	_               uint32 // uNumberOfBssids
	_               uint32 // bNetworkConnectable
	_               uint32 // wlanNotConnectableReason
	_               uint32 // uNumberOfPhyTypes
	_               [wlanMaxPHYTypeNumber]uint32
	_               uint32 // bMorePhyTypes
	SignalQuality   uint32
	SecurityEnabled uint32
	AuthAlgorithm   uint32
	_               uint32 // dot11DefaultCipherAlgorithm
	Flags           uint32
	_               uint32 // dwReserved
}

// wlanAvailableNetworkList is WLAN_AVAILABLE_NETWORK_LIST: two DWORDs, then a
// variable-length array.
type wlanAvailableNetworkList struct {
	NumberOfItems uint32
	_             uint32 // dwIndex
	Network       [1]wlanAvailableNetwork
}

// wlanProfileInfo is WLAN_PROFILE_INFO: WCHAR[256], DWORD flags (516 bytes).
type wlanProfileInfo struct {
	ProfileName [wlanMaxNameLength]uint16
	_           uint32 // dwFlags
}

// wlanProfileInfoList is WLAN_PROFILE_INFO_LIST: two DWORDs, then an array.
type wlanProfileInfoList struct {
	NumberOfItems uint32
	_             uint32 // dwIndex
	ProfileInfo   [1]wlanProfileInfo
}

// wlanJoinLayoutChecks fails to compile if a size or offset drifts from
// wlanapi.h, the same way wifiLayoutChecks does.
func _() {
	var x [1]struct{}
	_ = x[unsafe.Sizeof(dot11SSID{})-36]
	_ = x[unsafe.Sizeof(wlanAvailableNetwork{})-628]
	_ = x[unsafe.Offsetof(wlanAvailableNetwork{}.SSID)-512]
	_ = x[unsafe.Offsetof(wlanAvailableNetwork{}.BSSType)-548]
	_ = x[unsafe.Offsetof(wlanAvailableNetwork{}.SignalQuality)-604]
	_ = x[unsafe.Offsetof(wlanAvailableNetwork{}.SecurityEnabled)-608]
	_ = x[unsafe.Offsetof(wlanAvailableNetwork{}.AuthAlgorithm)-612]
	_ = x[unsafe.Offsetof(wlanAvailableNetwork{}.Flags)-620]
	_ = x[unsafe.Offsetof(wlanAvailableNetworkList{}.Network)-8]
	_ = x[unsafe.Sizeof(wlanProfileInfo{})-516]
	_ = x[unsafe.Offsetof(wlanProfileInfoList{}.ProfileInfo)-8]
}

// connectedProfile is the profile name of the entry the raw list marks
// connected, "" when none is.
func connectedProfile(raw []wlanAvailableNetwork) string {
	for _, n := range raw {
		if n.Flags&wlanAvailableNetworkConnected != 0 {
			if p := utf16String(n.ProfileName[:]); p != "" {
				return p
			}
		}
	}
	return ""
}

// listsSSID reports whether the raw list has a network named ssid.
func listsSSID(raw []wlanAvailableNetwork, ssid string) bool {
	return slices.ContainsFunc(decodeNetworks(raw), func(n WifiNetwork) bool { return n.SSID == ssid })
}

// decodeNetworks turns the raw list into one entry per SSID, strongest
// first. Hidden networks (no name) and ad hoc networks are left out.
func decodeNetworks(raw []wlanAvailableNetwork) []WifiNetwork {
	byName := map[string]int{}
	var out []WifiNetwork
	for _, n := range raw {
		if n.BSSType != dot11BSSTypeInfrastructure {
			continue
		}
		l := min(int(n.SSID.Length), len(n.SSID.SSID))
		ssid := string(n.SSID.SSID[:l])
		if ssid == "" {
			continue
		}
		i, seen := byName[ssid]
		if !seen {
			i = len(out)
			byName[ssid] = i
			out = append(out, WifiNetwork{SSID: ssid})
		}
		w := &out[i]
		w.Signal = max(w.Signal, min(n.SignalQuality, 100))
		if n.Flags&wlanAvailableNetworkConnected != 0 {
			w.Connected = true
		}
		if p := utf16String(n.ProfileName[:]); p != "" && n.Flags&wlanAvailableNetworkHasProfile != 0 {
			w.HasProfile = true
			if w.Profile == "" {
				w.Profile = p
			}
		}
		if n.SecurityEnabled != 0 {
			w.Secured = true
		}
		if w.Auth == 0 {
			w.Auth = n.AuthAlgorithm
			w.Security = wifiAuthName(n.AuthAlgorithm)
		}
	}
	slices.SortStableFunc(out, func(a, b WifiNetwork) int {
		if a.Signal != b.Signal {
			return int(b.Signal) - int(a.Signal)
		}
		return strings.Compare(a.SSID, b.SSID)
	})
	return out
}
