//go:build windows

package winapi

import (
	"errors"
	"net"

	"golang.org/x/sys/windows"
)

// bestInterface implements [BestInterface] with GetBestInterfaceEx.
func bestInterface(ip net.IP) (uint32, error) {
	v4 := ip.To4()
	if v4 == nil {
		return 0, errors.New("winapi: only IPv4 addresses are supported")
	}
	sa := &windows.SockaddrInet4{}
	copy(sa.Addr[:], v4)
	var idx uint32
	if err := windows.GetBestInterfaceEx(sa, &idx); err != nil {
		return 0, err
	}
	return idx, nil
}

// interfaceOctets implements [InterfaceOctets] with GetIfEntry2Ex. The 64-bit
// counters of MIB_IF_ROW2 are used on purpose: the older GetIfEntry has
// 32-bit ones that wrap every 4 GB, which an 80 GB copy would cross twenty
// times.
func interfaceOctets(index uint32) (uint64, uint64, error) {
	var row windows.MibIfRow2
	row.InterfaceIndex = index
	if err := windows.GetIfEntry2Ex(windows.MibIfTableNormal, &row); err != nil {
		return 0, 0, err
	}
	return row.InOctets, row.OutOctets, nil
}
