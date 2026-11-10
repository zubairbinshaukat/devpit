//go:build windows

package winapi

import (
	"errors"
	"net"
	"testing"
)

// Read-only: asks for the gateway of each Wi-Fi card on this PC. Never scans,
// connects or changes a setting, and logs only whether a gateway was found.
func TestInterfaceGatewayOnThisPCOnlyReads(t *testing.T) {
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
		gw, err := InterfaceGateway(w.GUID)
		if err != nil {
			t.Errorf("InterfaceGateway: %v", err)
			continue
		}
		t.Logf("has gateway: %v", gw != "")
		if gw != "" {
			if ip := net.ParseIP(gw); ip == nil || ip.To4() == nil {
				t.Errorf("gateway %q is not an IPv4 address", gw)
			}
		}
	}
}

func TestInterfaceGatewayUnknownAdapterHasNone(t *testing.T) {
	gw, err := InterfaceGateway("{00000000-0000-0000-0000-000000000001}")
	if err != nil || gw != "" {
		t.Fatalf("got %q, %v; want no gateway and no error", gw, err)
	}
	if _, err := InterfaceGateway(""); err == nil {
		t.Error("an empty GUID was accepted")
	}
}
