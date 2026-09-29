package winapi

import "net"

// BestInterface returns the index of the network adapter Windows would use to
// reach ip, without sending a packet. It is how Devpit picks the adapter to
// watch when it measures a copy's speed: the one that routes to the host.
func BestInterface(ip net.IP) (uint32, error) { return bestInterface(ip) }

// InterfaceOctets returns how many bytes the adapter with the given index has
// received and sent since it came up. Two samples and a clock make a speed.
func InterfaceOctets(index uint32) (in, out uint64, err error) { return interfaceOctets(index) }
