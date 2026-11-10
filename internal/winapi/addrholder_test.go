package winapi

import (
	"net/netip"
	"testing"
)

func TestOnlyAWifiDirectVirtualAdapterIsVirtual(t *testing.T) {
	const hw, conn = ifFlagHardwareInterface, ifFlagConnectorPresent
	tests := []struct {
		name   string
		ifType uint32
		flags  uint8
		want   bool
	}{
		{"Wi-Fi Direct virtual adapter", 71, 0, true},
		{"virtual, media not connected", 71, 0x10, true},
		{"the physical Wi-Fi card", 71, hw | conn, false},
		{"802.11 hardware without a connector", 71, hw, false},
		{"802.11 with a connector only", 71, conn, false},
		{"Ethernet", 6, hw | conn, false},
		{"a virtual Ethernet (Hyper-V switch)", 6, 0, false},
		{"loopback", 24, 0, false},
	}
	for _, tt := range tests {
		if got := IsWifiDirectVirtual(tt.ifType, tt.flags); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestHoldersOfFindsEveryAdapterThatHoldsTheAddress(t *testing.T) {
	ip := netip.MustParseAddr("192.168.137.1")
	list := []rawAdapter{
		{alias: "Wi-Fi", index: 11, ifType: 71, flags: 0x05, haveFlags: true, v4: []netip.Addr{netip.MustParseAddr("192.168.137.9")}},
		{alias: "Local Area Connection* 11", index: 43, ifType: 71, haveFlags: true, v4: []netip.Addr{ip}},
		{alias: "Ethernet 2", index: 7, ifType: 6, flags: 0x05, haveFlags: true, v4: []netip.Addr{ip}},
		{alias: "Unread", index: 9, ifType: 71, v4: []netip.Addr{ip}},
	}
	got := holdersOf(list, ip, 43)
	if len(got) != 3 {
		t.Fatalf("got %d holders, want 3: %+v", len(got), got)
	}
	if !got[0].Virtual || !got[0].Routes || got[0].IfIndex != 43 {
		t.Errorf("the virtual holder = %+v", got[0])
	}
	if got[1].Virtual || got[1].Routes {
		t.Errorf("an Ethernet holder = %+v", got[1])
	}
	if got[2].Virtual {
		t.Error("an adapter whose flags were not read counted as virtual")
	}
	if len(holdersOf(list, netip.MustParseAddr("10.0.0.1"), 0)) != 0 {
		t.Error("an address nobody holds was found")
	}
}

func TestSourceOfTellsThatThePCTalksToItself(t *testing.T) {
	ip := netip.MustParseAddr("192.168.137.1")
	list := []rawAdapter{
		{alias: "Wi-Fi", index: 11, v4: []netip.Addr{netip.MustParseAddr("10.1.2.3"), netip.MustParseAddr("192.168.137.9")}},
		{alias: "Local Area Connection* 11", index: 43, v4: []netip.Addr{ip}},
		{alias: "Bare", index: 5},
	}
	if a, s := sourceOf(list, ip, 43); a != "Local Area Connection* 11" || s != "192.168.137.1" {
		t.Errorf("self route = %q %q", a, s)
	}
	if a, s := sourceOf(list, ip, 11); a != "Wi-Fi" || s != "192.168.137.9" {
		t.Errorf("healthy route = %q %q, want the same-subnet address", a, s)
	}
	if a, s := sourceOf(list, netip.MustParseAddr("8.8.8.8"), 11); a != "Wi-Fi" || s != "10.1.2.3" {
		t.Errorf("other route = %q %q, want the first address", a, s)
	}
	if a, s := sourceOf(list, ip, 5); a != "Bare" || s != "" {
		t.Errorf("no-address route = %q %q", a, s)
	}
	if a, s := sourceOf(list, ip, 99); a != "" || s != "" {
		t.Errorf("unknown index = %q %q", a, s)
	}
}

func TestAddressCallsRefuseWhatIsNotIPv4(t *testing.T) {
	for _, ip := range []string{"", "host", "::1", "192.168.137.1/24", "192.168.137.256"} {
		if _, err := LocalAddressHolders(ip); err == nil {
			t.Errorf("LocalAddressHolders(%q) was accepted", ip)
		}
		if _, _, err := RouteSource(ip); err == nil {
			t.Errorf("RouteSource(%q) was accepted", ip)
		}
	}
}
