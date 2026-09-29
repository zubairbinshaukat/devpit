package host

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strings"
)

// Adapter is one network connection the other PC could reach this one on.
type Adapter struct {
	// Name is the connection's name as Windows shows it ("Ethernet",
	// "Wi-Fi"), in the PC's language. It is only ever displayed.
	Name string
	// Index is the interface index, which is what Windows commands take.
	Index uint32
	// IP is the adapter's IPv4 address.
	IP net.IP
	// Best is true for the adapter Windows uses to reach the internet, the
	// likeliest one to be on the same network as the other PC.
	Best bool
}

// Iface is what an interface looks like to [PickAdapters]: the few facts
// that decide whether it is a real LAN adapter.
type Iface struct {
	// Name is the connection name.
	Name string
	// Index is the interface index.
	Index uint32
	// IPv4 are its IPv4 addresses.
	IPv4 []net.IP
	// Up is true when the interface is connected.
	Up bool
	// Loopback is true for the local loopback.
	Loopback bool
}

// virtualNames are pieces of an adapter name that mark it as virtual:
// virtual machines, containers, VPN and tunnel adapters. None of them can be
// reached from another PC on the same Wi-Fi, so offering one would only
// confuse. They are compared lower case.
var virtualNames = []string{
	"vethernet", "virtualbox", "vmware", "vmnet", "hyper-v", "docker", "wsl",
	"tailscale", "zerotier", "hamachi", "loopback", "bluetooth", "tap-", "tun",
	"vpn", "wireguard", "npcap",
}

// isPrivateLAN reports whether ip is in a private range (RFC 1918) or the
// link-local range: the addresses a home or office network uses.
func isPrivateLAN(ip net.IP) bool { return ip.IsPrivate() || ip.IsLinkLocalUnicast() }

// PickAdapters chooses the adapters worth offering, best first. An adapter
// must be up, not loopback, not virtual by its name, and have a private IPv4
// address. bestIndex is the adapter Windows routes to the internet through
// (0 if unknown); it sorts first and is marked Best. The rest keep their
// order by index so the list does not jump around.
func PickAdapters(ifaces []Iface, bestIndex uint32) []Adapter {
	var out []Adapter
	for _, f := range ifaces {
		if !f.Up || f.Loopback {
			continue
		}
		lname := strings.ToLower(f.Name)
		virtual := false
		for _, v := range virtualNames {
			if strings.Contains(lname, v) {
				virtual = true
				break
			}
		}
		if virtual {
			continue
		}
		for _, ip := range f.IPv4 {
			if v4 := ip.To4(); v4 != nil && isPrivateLAN(v4) && !v4.IsLinkLocalUnicast() {
				out = append(out, Adapter{Name: f.Name, Index: f.Index, IP: v4, Best: f.Index == bestIndex && bestIndex != 0})
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Best != out[j].Best {
			return out[i].Best
		}
		return out[i].Index < out[j].Index
	})
	return out
}

// Category is a Windows network category.
type Category string

// The categories. Only Private and Public can be set by a program;
// DomainAuthenticated belongs to a domain and is left alone.
const (
	// CategoryPrivate is a trusted network: file sharing is allowed.
	CategoryPrivate Category = "Private"
	// CategoryPublic is an untrusted network: sharing is blocked.
	CategoryPublic Category = "Public"
	// CategoryDomain is a network the domain controller vouches for.
	CategoryDomain Category = "DomainAuthenticated"
)

// NetInfo reads the machine's network state. The real one asks Windows; tests
// give it canned answers.
type NetInfo interface {
	// Adapters lists the usable adapters, best first.
	Adapters(ctx context.Context) ([]Adapter, error)
	// CategoryOf returns the category of the connection on an interface.
	CategoryOf(ctx context.Context, index uint32) (Category, error)
}

// ProfileRunner runs the one read-only PowerShell command that reads a
// connection's category. Reading needs no administrator rights.
type ProfileRunner func(ctx context.Context, script string) (string, error)

// ExecProfileRunner is the real [ProfileRunner], over powershell.exe.
func ExecProfileRunner(ctx context.Context, script string) (string, error) {
	// #nosec G204 -- script is built from an integer by SystemNet.CategoryOf.
	cmd := exec.CommandContext(ctx, "powershell", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script)
	hideWindow(cmd)
	out, err := cmd.Output()
	return string(out), err
}

// parseCategory reads the word Get-NetConnectionProfile prints for a
// category. The names are enum names and are the same in every language.
func parseCategory(out string) (Category, error) {
	switch c := Category(strings.TrimSpace(out)); c {
	case CategoryPrivate, CategoryPublic, CategoryDomain:
		return c, nil
	default:
		return "", fmt.Errorf("unknown network category %q", strings.TrimSpace(out))
	}
}
