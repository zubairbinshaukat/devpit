package network

import "testing"

func TestLocalIPs_NoLoopbackOrDown(t *testing.T) {
	ifaces, err := LocalIPs()
	if err != nil {
		t.Fatalf("LocalIPs() error = %v", err)
	}
	for _, iface := range ifaces {
		if !iface.Up {
			t.Fatalf("LocalIPs() returned a down interface: %+v", iface)
		}
		for _, ip := range iface.IPv4 {
			if ip.IsLoopback() {
				t.Fatalf("LocalIPs() returned a loopback IPv4 address: %+v", iface)
			}
		}
		for _, ip := range iface.IPv6 {
			if ip.IsLoopback() {
				t.Fatalf("LocalIPs() returned a loopback IPv6 address: %+v", iface)
			}
		}
	}
}
