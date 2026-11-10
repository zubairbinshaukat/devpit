//go:build windows

package winapi

import (
	"errors"
	"testing"
)

// Read-only: lists the saved profiles of each card through WifiHasProfile.
// Never scans, connects, or adds or deletes a profile, and logs nothing that
// names a network.
func TestWifiHasProfileOnThisPCOnlyReads(t *testing.T) {
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
		if _, err := WifiHasProfile(w.GUID, "devpit-test-no-such-profile"); err != nil {
			t.Errorf("WifiHasProfile: %v", err)
		}
	}
	if _, err := WifiHasProfile("not-a-guid", "x"); err == nil {
		t.Error("a bad GUID was accepted")
	}
}

// Read-only: WifiVisible reads the list Windows already has (no scan), and a
// refusal is ErrWifiDenied, never a crash. HasWiredGateway reads the adapter
// table. Nothing that names a network is logged.
func TestWifiVisibleAndWiredGatewayOnThisPCOnlyRead(t *testing.T) {
	if _, err := HasWiredGateway(); err != nil {
		t.Errorf("HasWiredGateway: %v", err)
	}
	list, err := WifiInterfaces()
	if errors.Is(err, ErrNoWifi) || (err == nil && len(list) == 0) {
		t.Skip("no Wi-Fi card")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range list {
		if _, err := WifiVisible(w.GUID, "devpit-test-no-such-network"); err != nil && !errors.Is(err, ErrWifiDenied) {
			t.Errorf("WifiVisible: %v", err)
		}
	}
}
