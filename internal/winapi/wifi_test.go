package winapi

import (
	"os"
	"regexp"
	"slices"
	"testing"
	"unicode/utf16"
)

func TestWifiStandardFromPHYList(t *testing.T) {
	for _, c := range []struct {
		phys []uint32
		want string
		six  bool
	}{
		{[]uint32{5, 6, 7, 4, 8, 10}, "Wi-Fi 6", true}, // the dev PC's AX201
		{[]uint32{10}, "Wi-Fi 6", true},
		{[]uint32{11}, "Wi-Fi 7", true},
		{[]uint32{4, 5, 6, 7, 8, 10, 11}, "Wi-Fi 7", true},
		{[]uint32{11, 7}, "Wi-Fi 7", true},
		{[]uint32{8}, "Wi-Fi 5", false},
		{[]uint32{4, 7, 8}, "Wi-Fi 5", false},
		{[]uint32{7}, "Wi-Fi 4", false},
		{[]uint32{4, 5, 6}, "", false},
		{[]uint32{9}, "", false},
		{nil, "", false},
		{[]uint32{}, "", false},
	} {
		if got := WifiStandard(c.phys); got != c.want {
			t.Errorf("WifiStandard(%v) = %q, want %q", c.phys, got, c.want)
		}
		if got := IsWifi6OrNewer(c.phys); got != c.six {
			t.Errorf("IsWifi6OrNewer(%v) = %v, want %v", c.phys, got, c.six)
		}
	}
}

// The probe file may only resolve read calls: open, close, enumerate,
// capability, query and free. A scan, connect or profile call belongs in the
// join file, never here.
func TestWifiProbeCallsOnlyReadFunctions(t *testing.T) {
	src, err := os.ReadFile("wifi_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range regexp.MustCompile(`NewProc\("([^"]+)"\)`).FindAllSubmatch(src, -1) {
		got = append(got, string(m[1]))
	}
	slices.Sort(got)
	want := []string{
		"WlanCloseHandle", "WlanEnumInterfaces", "WlanFreeMemory",
		"WlanGetInterfaceCapability", "WlanOpenHandle", "WlanQueryInterface",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("wifi_windows.go resolves %v, want exactly %v", got, want)
	}
	if n := len(regexp.MustCompile(`NewLazySystemDLL\(`).FindAll(src, -1)); n != 1 {
		t.Fatalf("wifi_windows.go loads %d DLLs, want only wlanapi.dll", n)
	}
}

func fakeInfo(desc string, state uint32) wlanInterfaceInfo {
	info := wlanInterfaceInfo{
		InterfaceGUID: wlanGUID{Data1: 0x1A2B3C4D, Data2: 0x5E6F, Data3: 0x0718, Data4: [8]byte{0x29, 0x3A, 0x4B, 0x5C, 0x6D, 0x7E, 0x8F, 0x90}},
		State:         state,
	}
	copy(info.Description[:], utf16.Encode([]rune(desc)))
	return info
}

func fakeCaps(phys ...uint32) *wlanInterfaceCapability {
	c := &wlanInterfaceCapability{NumberOfPHYs: uint32(len(phys))} //nolint:gosec // test
	copy(c.PHYTypes[:], phys)
	return c
}

// When Windows refuses the current-link and channel queries (access denied,
// for example with location off), the card is still listed with its
// standards, Connected comes from the interface state, and the link stays
// unknown rather than becoming an error.
func TestAccessDeniedLeavesTheLinkUnknown(t *testing.T) {
	// readWifi leaves conn nil and haveChannel false for any non-zero return
	// (ERROR_ACCESS_DENIED included); that is the input here.
	w := decodeWifi(wifiRaw{info: fakeInfo("Intel(R) Wi-Fi 6 AX201 160MHz", wlanInterfaceStateConnected), caps: fakeCaps(5, 6, 7, 4, 8, 10)})
	if !w.Connected {
		t.Error("Connected must come from the interface state")
	}
	if w.PHY != 0 || w.RxKbps != 0 || w.TxKbps != 0 || w.Channel != 0 || w.Profile != "" {
		t.Errorf("link must stay unknown, got %+v", w)
	}
	if WifiStandard(w.PHYs) != "Wi-Fi 6" || w.Description != "Intel(R) Wi-Fi 6 AX201 160MHz" {
		t.Errorf("standards lost: %+v", w)
	}
	if w.GUID != "{1A2B3C4D-5E6F-0718-293A-4B5C6D7E8F90}" {
		t.Errorf("GUID = %q", w.GUID)
	}

	// Everything refused, card disconnected: still one quiet entry.
	w = decodeWifi(wifiRaw{info: fakeInfo("Card", 4)})
	if w.Connected || w.PHYs != nil || w.Channel != 0 {
		t.Errorf("disconnected card with nothing readable: %+v", w)
	}
}

func TestDecodeWifiConnectedLink(t *testing.T) {
	conn := &wlanConnectionAttributes{State: wlanInterfaceStateConnected}
	copy(conn.ProfileName[:], utf16.Encode([]rune("Home 5G")))
	conn.Association.PHYType = PHYHE
	conn.Association.RxRate, conn.Association.TxRate = 1_201_000, 864_000
	w := decodeWifi(wifiRaw{
		info: fakeInfo("Card", wlanInterfaceStateConnected), caps: fakeCaps(7, 8, 10),
		conn: conn, channel: 36, haveChannel: true,
	})
	if w.PHY != PHYHE || w.RxKbps != 1_201_000 || w.TxKbps != 864_000 || w.Channel != 36 || w.Profile != "Home 5G" {
		t.Fatalf("link = %+v", w)
	}
	// A channel without a connection means nothing.
	w = decodeWifi(wifiRaw{info: fakeInfo("Card", 4), channel: 6, haveChannel: true})
	if w.Channel != 0 {
		t.Errorf("channel on a disconnected card = %d", w.Channel)
	}
	// A PHY count above the array never reads past it.
	c := fakeCaps(10)
	c.NumberOfPHYs = 1000
	if w = decodeWifi(wifiRaw{info: fakeInfo("Card", 4), caps: c}); len(w.PHYs) != wlanMaxPHYIndex {
		t.Errorf("PHYs = %d, want capped at %d", len(w.PHYs), wlanMaxPHYIndex)
	}
}
