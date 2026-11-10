//go:build windows

package winapi

import (
	"errors"
	"slices"
	"testing"

	"golang.org/x/sys/windows"
)

// Read-only: lists the cards on this PC. Never scans, connects or changes a
// setting. Skips when the PC has no Wi-Fi card or no WLAN service.
func TestWifiInterfacesOnThisPC(t *testing.T) {
	list, err := WifiInterfaces()
	if errors.Is(err, ErrNoWifi) {
		t.Skipf("no WLAN service: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Skip("no Wi-Fi card")
	}
	for _, w := range list {
		t.Logf("%s %q: PHYs %v (%s) connected=%v PHY=%d rx=%d kbps tx=%d kbps channel=%d",
			w.GUID, w.Description, w.PHYs, WifiStandard(w.PHYs), w.Connected, w.PHY, w.RxKbps, w.TxKbps, w.Channel)
		if w.GUID == "" || w.Description == "" {
			t.Errorf("card without a GUID or description: %+v", w)
		}
		if w.PHY != 0 && !slices.Contains(w.PHYs, w.PHY) && len(w.PHYs) > 0 {
			t.Errorf("current PHY %d is not in the supported list %v", w.PHY, w.PHYs)
		}
	}
}

// The GUID text must match windows.GUID.String, which sysinfo compares with
// the IP helper's InterfaceGuid.
func TestWlanGUIDStringMatchesWindows(t *testing.T) {
	g := wlanGUID{Data1: 0x0A0B0C0D, Data2: 0x1, Data3: 0xFFEE, Data4: [8]byte{0, 1, 2, 3, 0xAB, 0xCD, 0xEF, 0x10}}
	if got, want := g.String(), windows.GUID(g).String(); got != want {
		t.Fatalf("String = %q, windows.GUID = %q", got, want)
	}
}
