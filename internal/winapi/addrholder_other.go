//go:build !windows

package winapi

import "net/netip"

// localAddressHolders is not implemented outside Windows.
func localAddressHolders(netip.Addr) ([]AddressHolder, error) { return nil, ErrUnsupported }

// routeSource is not implemented outside Windows.
func routeSource(netip.Addr) (string, string, error) { return "", "", ErrUnsupported }
