package host

import (
	"context"
	"fmt"
	"net"

	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// SystemNet is the real [NetInfo].
type SystemNet struct {
	// Run reads a connection's category; nil means [ExecProfileRunner].
	Run ProfileRunner
	// Wifi lists the Wi-Fi cards; nil means [winapi.WifiInterfaces].
	Wifi func() ([]winapi.WifiInterface, error)
}

// Adapters implements [NetInfo]. The "best" adapter is the one Windows would
// route to a public address through; asking does not send any packet. Each
// adapter carries its link speed and, for Wi-Fi, the card's standard; both
// are zero when Windows would not say.
func (s SystemNet) Adapters(context.Context) ([]Adapter, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("listing network adapters: %w", err)
	}
	var list []Iface
	for _, i := range ifs {
		f := Iface{
			Name: i.Name, Index: uint32(i.Index), //nolint:gosec // interface indexes are small positive numbers
			Up: i.Flags&net.FlagUp != 0, Loopback: i.Flags&net.FlagLoopback != 0,
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				f.IPv4 = append(f.IPv4, n.IP)
			}
		}
		list = append(list, f)
	}
	best, _ := winapi.BestInterface(net.IPv4(8, 8, 8, 8))
	picked := PickAdapters(list, best)
	indexes := make([]uint32, len(picked))
	for i, a := range picked {
		indexes[i] = a.Index
	}
	wifiFn := s.Wifi
	if wifiFn == nil {
		wifiFn = winapi.WifiInterfaces
	}
	// The Wi-Fi standard is a bonus: no WLAN service or any probe error just
	// leaves it empty.
	wifi, _ := wifiFn()
	return annotate(picked, linkRows(indexes), wifi), nil
}

// CategoryOf implements [NetInfo].
func (s SystemNet) CategoryOf(ctx context.Context, index uint32) (Category, error) {
	run := s.Run
	if run == nil {
		run = ExecProfileRunner
	}
	out, err := run(ctx, fmt.Sprintf(
		"Get-NetConnectionProfile -InterfaceIndex %d | Select-Object -ExpandProperty NetworkCategory", index))
	if err != nil {
		return "", fmt.Errorf("reading the network category: %w", err)
	}
	return parseCategory(out)
}
