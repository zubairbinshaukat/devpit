package winapi

import (
	"encoding/xml"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// profileDoc is the part of a WLAN profile the tests read back.
type profileDoc struct {
	XMLName xml.Name `xml:"WLANProfile"`
	Name    string   `xml:"name"`
	SSIDHex string   `xml:"SSIDConfig>SSID>hex"`
	SSID    string   `xml:"SSIDConfig>SSID>name"`
	Type    string   `xml:"connectionType"`
	Mode    string   `xml:"connectionMode"`
	Auth    string   `xml:"MSM>security>authEncryption>authentication"`
	Enc     string   `xml:"MSM>security>authEncryption>encryption"`
	OneX    string   `xml:"MSM>security>authEncryption>useOneX"`
	KeyType string   `xml:"MSM>security>sharedKey>keyType"`
	Prot    string   `xml:"MSM>security>sharedKey>protected"`
	Key     string   `xml:"MSM>security>sharedKey>keyMaterial"`
}

func TestProfileXMLEscapesTheNameAndPassphrase(t *testing.T) {
	ssid := `Dev<pit>&"x'`
	pass := `a<b>&c"d'e]]>`
	doc, err := wifiProfileXML(ssid, pass)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc, "<pit>") || strings.Contains(doc, "<b>") || strings.Contains(doc, "]]>") {
		t.Errorf("unescaped text in the profile:\n%s", doc)
	}
	var p profileDoc
	if err := xml.Unmarshal([]byte(doc), &p); err != nil {
		t.Fatalf("not valid XML: %v\n%s", err, doc)
	}
	if p.Name != ssid || p.SSID != ssid || p.Key != pass {
		t.Errorf("round trip: name %q ssid %q key %q", p.Name, p.SSID, p.Key)
	}
	if p.SSIDHex != "4465763C7069743E26227827" {
		t.Errorf("hex = %q", p.SSIDHex)
	}
	if p.Mode != "manual" || p.Type != "ESS" || p.Auth != "WPA2PSK" || p.Enc != "AES" || p.OneX != "false" ||
		p.KeyType != "passPhrase" || p.Prot != "false" {
		t.Errorf("profile = %+v", p)
	}
	if !strings.Contains(doc, `xmlns="http://www.microsoft.com/networking/WLAN/profile/v1"`) {
		t.Error("missing the profile namespace")
	}
}

func TestProfileXMLRefusesWhatWPA2CannotHold(t *testing.T) {
	for _, c := range []struct {
		ssid, pass string
		want       error
	}{
		{"", "12345678", ErrBadNetworkName},
		{strings.Repeat("a", 33), "12345678", ErrBadNetworkName},
		{"a\nb", "12345678", ErrBadNetworkName},
		{"ok", "1234567", ErrBadPassphrase},
		{"ok", strings.Repeat("a", 64), ErrBadPassphrase},
		{"ok", "1234567\x00", ErrBadPassphrase},
		{"ok", "pässwörd1", ErrBadPassphrase},
	} {
		if _, err := wifiProfileXML(c.ssid, c.pass); !errors.Is(err, c.want) {
			t.Errorf("%q/%q: err = %v, want %v", c.ssid, c.pass, err, c.want)
		}
	}
	if _, err := wifiProfileXML(strings.Repeat("a", 32), strings.Repeat("b", 63)); err != nil {
		t.Errorf("limits refused: %v", err)
	}
}

// The calls that change Wi-Fi state live in wlanjoin_windows.go, never in the
// read-only probe file, and the join file loads no DLL of its own.
func TestJoinProcsLiveOutsideTheProbeFile(t *testing.T) {
	procs := func(file string) []string {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range regexp.MustCompile(`NewProc\("([^"]+)"\)`).FindAllSubmatch(src, -1) {
			got = append(got, string(m[1]))
		}
		if file == "wlanjoin_windows.go" && regexp.MustCompile(`NewLazySystemDLL\(`).Match(src) {
			t.Error("wlanjoin_windows.go loads its own DLL")
		}
		slices.Sort(got)
		return got
	}
	join := procs("wlanjoin_windows.go")
	want := []string{
		"WlanConnect", "WlanDeleteProfile", "WlanGetAvailableNetworkList",
		"WlanGetProfileList", "WlanScan", "WlanSetProfile",
	}
	if !slices.Equal(join, want) {
		t.Fatalf("wlanjoin_windows.go resolves %v, want exactly %v", join, want)
	}
	for _, p := range procs("wifi_windows.go") {
		if slices.Contains(want, p) {
			t.Errorf("the probe file resolves the join call %s", p)
		}
	}
}

func rawNet(ssid, profile string, signal, flags, auth uint32) wlanAvailableNetwork {
	n := wlanAvailableNetwork{BSSType: dot11BSSTypeInfrastructure, SignalQuality: signal, Flags: flags, AuthAlgorithm: auth, SecurityEnabled: 1}
	n.SSID.Length = uint32(copy(n.SSID.SSID[:], ssid)) //nolint:gosec // test
	copy(n.ProfileName[:], utf16.Encode([]rune(profile)))
	return n
}

// With location access off, Windows will not name the profile the card is on
// (WlanQueryInterface opcode 7 is refused), so a join must still finish once
// the card reports a connection, never wait for a name that never comes.
func TestAJoinFinishesWhenWindowsWillNotNameTheProfile(t *testing.T) {
	t0 := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	on := func(profile string) connSample { return connSample{found: true, connected: true, profile: profile} }
	off := connSample{found: true}

	// Named: only the profile asked for counts.
	w := connWatch{want: "Devpit-x7k2", start: t0}
	if w.done(on("Home"), t0.Add(time.Minute)) {
		t.Error("the old network counted as the hotspot")
	}
	if !w.done(on("Devpit-x7k2"), t0) {
		t.Error("the hotspot by name did not count")
	}

	// Unnamed, seen leaving the old link: connected again is the hotspot.
	w = connWatch{want: "Devpit-x7k2", start: t0}
	if w.done(on(""), t0.Add(500*time.Millisecond)) {
		t.Error("still on the old link right after the connect request counted")
	}
	if w.done(off, t0.Add(time.Second)) {
		t.Error("a disconnected card counted")
	}
	if !w.done(on(""), t0.Add(1500*time.Millisecond)) {
		t.Error("connected after leaving the old link did not count: every join would time out")
	}

	// Unnamed, never seen off-line: counts after the settle time only.
	w = connWatch{want: "Devpit-x7k2", start: t0}
	if w.done(on(""), t0.Add(unknownProfileSettle-time.Millisecond)) {
		t.Error("counted before the settle time")
	}
	if !w.done(on(""), t0.Add(unknownProfileSettle)) {
		t.Error("did not count after the settle time")
	}

	// The nearby list names the network after all: it decides.
	w = connWatch{want: "Devpit-x7k2", start: t0, left: true}
	if w.done(connSample{found: true, connected: true, listRead: true, listProfile: "Home"}, t0.Add(time.Minute)) {
		t.Error("the nearby list said Home, but it counted")
	}
	if !w.done(connSample{found: true, connected: true, listRead: true, listProfile: "Devpit-x7k2"}, t0) {
		t.Error("the nearby list said the hotspot, but it did not count")
	}
	// A card that is not in the list never counts.
	if w.done(connSample{}, t0.Add(time.Hour)) {
		t.Error("a missing card counted")
	}
}

func TestTheNearbyListNamesTheConnectedNetworkAndFindsAName(t *testing.T) {
	raw := []wlanAvailableNetwork{
		rawNet("Home", "", 70, 0, 7),
		rawNet("Home", "Home", 70, wlanAvailableNetworkHasProfile|wlanAvailableNetworkConnected, 7),
		rawNet("Devpit-x7k2", "", 50, 0, 7),
	}
	if got := connectedProfile(raw); got != "Home" {
		t.Errorf("connected profile = %q", got)
	}
	if got := connectedProfile(raw[2:]); got != "" {
		t.Errorf("connected profile with none connected = %q", got)
	}
	if !listsSSID(raw, "Devpit-x7k2") || listsSSID(raw, "Devpit-gone") || listsSSID(nil, "Home") {
		t.Error("listsSSID is wrong")
	}
}

func TestSameProgramComparesTheFileName(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{`C:\Tools\devpit.exe`, `D:\scoop\apps\devpit\DEVPIT.EXE`, true},
		{`\Device\HarddiskVolume3\Tools\devpit.exe`, `C:\Tools\devpit.exe`, true},
		{`C:\Windows\System32\svchost.exe`, `C:\Tools\devpit.exe`, false},
		{`C:\Tools\devpit.exe.bak`, `C:\Tools\devpit.exe`, false},
		{"", `C:\Tools\devpit.exe`, false},
		{"", "", false},
		{`C:\`, `D:\`, false},
	} {
		if got := SameProgram(c.a, c.b); got != c.want {
			t.Errorf("SameProgram(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckNetworkNameIsTheProfileRule(t *testing.T) {
	if CheckNetworkName("Devpit-x7k2") != nil || !errors.Is(CheckNetworkName(""), ErrBadNetworkName) ||
		!errors.Is(CheckNetworkName(strings.Repeat("a", 33)), ErrBadNetworkName) {
		t.Error("CheckNetworkName does not follow the profile rule")
	}
}

func TestDecodeNetworksMergesByNameAndSortsBySignal(t *testing.T) {
	adhoc := rawNet("AdHoc", "", 99, 0, 7)
	adhoc.BSSType = 2
	got := decodeNetworks([]wlanAvailableNetwork{
		rawNet("Home", "", 60, 0, 7),
		rawNet("Home", "Home", 70, wlanAvailableNetworkHasProfile|wlanAvailableNetworkConnected, 7),
		rawNet("", "", 90, 0, 7), // hidden
		adhoc,
		rawNet("Devpit-x7k2", "", 80, 0, 7),
		rawNet("Cafe", "", 255, 0, 1),
	})
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].SSID != "Cafe" || got[0].Signal != 100 || got[1].SSID != "Devpit-x7k2" || got[2].SSID != "Home" {
		t.Errorf("order = %+v", got)
	}
	h := got[2]
	if !h.HasProfile || h.Profile != "Home" || !h.Connected || h.Signal != 70 || h.Security != "WPA2-Personal" {
		t.Errorf("Home = %+v", h)
	}
	if got[1].HasProfile || got[1].Profile != "" {
		t.Errorf("a network with no saved profile says it has one: %+v", got[1])
	}
}
