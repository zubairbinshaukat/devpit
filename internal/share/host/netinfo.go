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
}

// Adapters implements [NetInfo]. The "best" adapter is the one Windows would
// route to a public address through; asking does not send any packet.
func (SystemNet) Adapters(context.Context) ([]Adapter, error) {
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
	return PickAdapters(list, best), nil
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
