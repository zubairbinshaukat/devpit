package sysinfo

import (
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

func TestWifiStandardMatchesByGUIDFirst(t *testing.T) {
	cards := []winapi.WifiInterface{
		{GUID: "{AAAAAAAA-0000-0000-0000-000000000001}", Description: "Same name", PHYs: []uint32{7, 8}},
		{GUID: "{BBBBBBBB-0000-0000-0000-000000000002}", Description: "Same name", PHYs: []uint32{7, 8, 10}},
		{GUID: "{CCCCCCCC-0000-0000-0000-000000000003}", Description: "Intel(R) Wi-Fi 7 BE200 320MHz", PHYs: []uint32{10, 11}},
	}
	for _, c := range []struct{ guid, desc, want string }{
		{"{bbbbbbbb-0000-0000-0000-000000000002}", "Same name", "Wi-Fi 6"}, // GUID wins over the first description match
		{"", "intel(r) wi-fi 7 be200 320mhz", "Wi-Fi 7"},                   // no GUID: description
		{"{DDDDDDDD-0000-0000-0000-000000000004}", "Intel(R) Wi-Fi 7 BE200 320MHz", "Wi-Fi 7"},
		{"{DDDDDDDD-0000-0000-0000-000000000004}", "Realtek PCIe 2.5GbE", ""},
		{"", "", ""},
	} {
		if got := wifiStandardFor(cards, c.guid, c.desc); got != c.want {
			t.Errorf("wifiStandardFor(%q, %q) = %q, want %q", c.guid, c.desc, got, c.want)
		}
	}
	if got := wifiStandardFor(nil, "{AAAAAAAA-0000-0000-0000-000000000001}", "x"); got != "" {
		t.Errorf("no WLAN service = %q, want empty", got)
	}
}
