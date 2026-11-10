package winapi

import (
	"errors"
	"net/netip"
)

// AddressHolder is one network adapter on this PC that holds a given IPv4
// address. Every field comes from a read-only call.
type AddressHolder struct {
	// Alias is the adapter's friendly name, such as "Wi-Fi" or "Local Area
	// Connection* 10". It is for the screen only: no rule reads it, because
	// Windows translates it.
	Alias string
	// IfIndex is the interface index and IfType the IANA interface type
	// (71 is IEEE 802.11).
	IfIndex, IfType uint32
	// Virtual is true for a Wi-Fi Direct virtual adapter, the kind Windows
	// Mobile Hotspot runs on; see [IsWifiDirectVirtual]. The physical Wi-Fi
	// card is never Virtual.
	Virtual bool
	// Routes is true when this is the adapter Windows uses to reach the
	// address (GetBestInterfaceEx): the PC is then talking to itself.
	Routes bool
}

// IfTypeIEEE80211 is IF_TYPE_IEEE80211, the interface type of every Wi-Fi
// adapter, physical or virtual.
const IfTypeIEEE80211 = 71

// The bits of MIB_IF_ROW2.InterfaceAndOperStatusFlags that the rule reads
// (netioapi.h: HardwareInterface is bit 0, ConnectorPresent bit 2).
const (
	ifFlagHardwareInterface = 0x01
	ifFlagConnectorPresent  = 0x04
)

// IsWifiDirectVirtual is the one rule that tells a Wi-Fi Direct virtual
// adapter ("Microsoft Wi-Fi Direct Virtual Adapter", the adapter Mobile
// Hotspot and Internet Connection Sharing put 192.168.137.1 on) from the
// physical Wi-Fi card. Both are interface type 71; the virtual one is not a
// hardware interface and has no connector, as NDIS reports in
// MIB_IF_ROW2.InterfaceAndOperStatusFlags. The physical card has both bits
// set. flags is that byte.
//
// The rule reads numbers and flag bits only, never an adapter name or a
// description, so it means the same on every Windows language. The worker's
// freeaddr script applies the same rule through Get-NetAdapter's
// InterfaceType, HardwareInterface and ConnectorPresent, which come from the
// same NDIS fields.
func IsWifiDirectVirtual(ifType uint32, flags uint8) bool {
	return ifType == IfTypeIEEE80211 && flags&ifFlagHardwareInterface == 0 && flags&ifFlagConnectorPresent == 0
}

// LocalAddressHolders returns every adapter on this PC that holds the IPv4
// address ip, with whether each is a Wi-Fi Direct virtual adapter and
// whether it is the one Windows routes to ip through. An empty list means
// no adapter here holds it, which is the healthy case when ip is another PC.
//
// It only reads: it never changes an address, a route or a setting. Other
// platforms return [ErrUnsupported].
func LocalAddressHolders(ip string) ([]AddressHolder, error) {
	a, err := parseIPv4(ip)
	if err != nil {
		return nil, err
	}
	return localAddressHolders(a)
}

// RouteSource returns the adapter Windows would use to reach the IPv4
// address ip (GetBestInterfaceEx), by its friendly name, and the IPv4
// address on that adapter that the connection would come from: ip itself
// when the adapter holds it (the PC talks to itself), else the adapter's
// address in the same /24 as ip, else its first one. src is "" when the
// adapter has no IPv4 address. It only reads. Other platforms return
// [ErrUnsupported].
func RouteSource(ip string) (alias, src string, err error) {
	a, err := parseIPv4(ip)
	if err != nil {
		return "", "", err
	}
	return routeSource(a)
}

// errNotIPv4 is the error for an address that is not plain IPv4 text.
var errNotIPv4 = errors.New("winapi: not an IPv4 address")

// parseIPv4 reads plain dotted IPv4 text.
func parseIPv4(ip string) (netip.Addr, error) {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() {
		return netip.Addr{}, errNotIPv4
	}
	return a, nil
}

// rawAdapter is what the Windows calls say about one adapter.
type rawAdapter struct {
	alias         string
	guid          string // "{XXXXXXXX-...}", as [WifiInterface].GUID
	index, ifType uint32
	flags         uint8
	haveFlags     bool
	v4            []netip.Addr
}

// holdersOf picks the adapters in list that hold ip. best is the index
// Windows routes ip through, 0 when unknown. An adapter whose flags could
// not be read is never Virtual.
func holdersOf(list []rawAdapter, ip netip.Addr, best uint32) []AddressHolder {
	var out []AddressHolder
	for _, r := range list {
		held := false
		for _, a := range r.v4 {
			if a == ip {
				held = true
				break
			}
		}
		if !held {
			continue
		}
		out = append(out, AddressHolder{
			Alias: r.alias, IfIndex: r.index, IfType: r.ifType,
			Virtual: r.haveFlags && IsWifiDirectVirtual(r.ifType, r.flags),
			Routes:  best != 0 && r.index == best,
		})
	}
	return out
}

// sourceOf is [RouteSource] over a list already read.
func sourceOf(list []rawAdapter, ip netip.Addr, best uint32) (alias, src string) {
	for _, r := range list {
		if r.index != best {
			continue
		}
		alias = r.alias
		var same, first netip.Addr
		for _, a := range r.v4 {
			if a == ip {
				return alias, ip.String()
			}
			if !first.IsValid() {
				first = a
			}
			if !same.IsValid() && a.As4()[0] == ip.As4()[0] && a.As4()[1] == ip.As4()[1] && a.As4()[2] == ip.As4()[2] {
				same = a
			}
		}
		switch {
		case same.IsValid():
			return alias, same.String()
		case first.IsValid():
			return alias, first.String()
		}
		return alias, ""
	}
	return "", ""
}
