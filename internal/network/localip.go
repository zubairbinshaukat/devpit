package network

import "net"

// Iface describes one network interface's addresses.
type Iface struct {
	Name string
	IPv4 []net.IP
	IPv6 []net.IP
	Up   bool
}

// LocalIPs returns every network interface with at least one address,
// skipping loopback and down interfaces by default.
func LocalIPs() ([]Iface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	result := make([]Iface, 0, len(ifaces))
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		up := iface.Flags&net.FlagUp != 0
		if !up {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			// A single interface failing to report addresses (permissions,
			// transient state) should not fail the whole lookup.
			continue
		}

		out := Iface{Name: iface.Name, Up: up}
		for _, addr := range addrs {
			ip := addrFromNet(addr)
			if ip == nil {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				out.IPv4 = append(out.IPv4, v4)
			} else {
				out.IPv6 = append(out.IPv6, ip)
			}
		}
		if len(out.IPv4) == 0 && len(out.IPv6) == 0 {
			continue
		}
		result = append(result, out)
	}
	return result, nil
}

func addrFromNet(addr net.Addr) net.IP {
	switch a := addr.(type) {
	case *net.IPNet:
		return a.IP
	case *net.IPAddr:
		return a.IP
	default:
		return nil
	}
}
