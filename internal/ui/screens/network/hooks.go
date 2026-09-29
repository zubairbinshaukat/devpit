package network

import (
	"context"
	"net"

	"github.com/zubairbinshaukat/devpit/internal/network"
)

// Hooks are the engine calls the leaf screens make. Every field is optional:
// a nil one keeps the real function. They exist so a caller that must not
// touch the network, such as the documentation screenshot renderer in
// internal/shots, can drive every state of the screens from demo data.
type Hooks struct {
	// LocalIPs lists the machine's interfaces.
	LocalIPs func() ([]network.Iface, error)
	// PublicIP asks the outside world for the public address.
	PublicIP func(context.Context) (net.IP, error)
	// Ping runs one ping.
	Ping func(ctx context.Context, host string, count int, onLine func(string), opts network.Options) (network.PingResult, error)
	// FlushDNS clears the resolver cache and returns the tool's output.
	FlushDNS func(context.Context) (string, error)
}

// WithHooks replaces the engine calls the hooks name and keeps the rest. It
// applies to every screen this one opens afterwards.
func (m Model) WithHooks(h Hooks) Model {
	m.hooks = h
	return m
}

// ip applies the hooks to the address screen.
func (h Hooks) ip(s ipModel) ipModel {
	if h.LocalIPs != nil {
		s.localsFn = h.LocalIPs
	}
	if h.PublicIP != nil {
		s.publicFn = h.PublicIP
	}
	return s
}

// ping applies the hooks to the ping screen.
func (h Hooks) ping(s pingModel) pingModel {
	if h.Ping != nil {
		s.runFn = h.Ping
	}
	return s
}

// dns applies the hooks to the DNS screen.
func (h Hooks) dns(s dnsModel) dnsModel {
	if h.FlushDNS != nil {
		s.flushFn = h.FlushDNS
	}
	return s
}
