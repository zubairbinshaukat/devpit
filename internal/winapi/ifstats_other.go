//go:build !windows

package winapi

import "net"

// bestInterface is not implemented outside Windows.
func bestInterface(net.IP) (uint32, error) { return 0, ErrUnsupported }

// interfaceOctets is not implemented outside Windows.
func interfaceOctets(uint32) (uint64, uint64, error) { return 0, 0, ErrUnsupported }
