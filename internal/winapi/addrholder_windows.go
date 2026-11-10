//go:build windows

package winapi

import (
	"errors"
	"fmt"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
)

// holderFlags asks GetAdaptersAddresses for the unicast addresses only.
const holderFlags = windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST |
	windows.GAA_FLAG_SKIP_DNS_SERVER

// readAdapters lists every IPv4 adapter with its unicast IPv4 addresses and
// the NDIS flags of MIB_IF_ROW2 (GetIfEntry2Ex). It only reads.
func readAdapters() ([]rawAdapter, error) {
	size := uint32(15 << 10)
	var buf []byte
	var err error
	for range 4 {
		buf = make([]byte, size)
		err = windows.GetAdaptersAddresses(windows.AF_INET, holderFlags, 0,
			(*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil || !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			break
		}
	}
	if errors.Is(err, windows.ERROR_NO_DATA) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetAdaptersAddresses: %w", err)
	}
	var out []rawAdapter
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		r := rawAdapter{
			alias:  windows.UTF16PtrToString(a.FriendlyName),
			guid:   windows.BytePtrToString(a.AdapterName),
			index:  a.IfIndex,
			ifType: a.IfType,
		}
		for u := a.FirstUnicastAddress; u != nil; u = u.Next {
			if ip4 := u.Address.IP().To4(); ip4 != nil {
				r.v4 = append(r.v4, netip.AddrFrom4([4]byte(ip4)))
			}
		}
		if r.index != 0 {
			var row windows.MibIfRow2
			row.InterfaceIndex = r.index
			if windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row) == nil {
				r.flags, r.haveFlags = row.InterfaceAndOperStatusFlags, true
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// bestIndex is GetBestInterfaceEx for ip, 0 when Windows has no route.
func bestIndex(ip netip.Addr) uint32 {
	sa := &windows.SockaddrInet4{Addr: ip.As4()}
	var idx uint32
	if windows.GetBestInterfaceEx(sa, &idx) != nil {
		return 0
	}
	return idx
}

// localAddressHolders implements [LocalAddressHolders].
func localAddressHolders(ip netip.Addr) ([]AddressHolder, error) {
	list, err := readAdapters()
	if err != nil {
		return nil, err
	}
	return holdersOf(list, ip, bestIndex(ip)), nil
}

// routeSource implements [RouteSource].
func routeSource(ip netip.Addr) (string, string, error) {
	sa := &windows.SockaddrInet4{Addr: ip.As4()}
	var idx uint32
	if err := windows.GetBestInterfaceEx(sa, &idx); err != nil {
		return "", "", fmt.Errorf("GetBestInterfaceEx: %w", err)
	}
	list, err := readAdapters()
	if err != nil {
		return "", "", err
	}
	alias, src := sourceOf(list, ip, idx)
	return alias, src, nil
}
