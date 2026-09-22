package network

import (
	"context"
	"errors"
	"net"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/network"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// ipLocalsMsg carries the result of the local-interface lookup.
type ipLocalsMsg struct {
	ifaces []network.Iface
	err    error
}

// ipPublicMsg carries the result of the public-IP lookup. It arrives
// independently of ipLocalsMsg and, since [network.PublicIP] is bounded to a
// few seconds and run entirely inside a tea.Cmd, never blocks Update.
type ipPublicMsg struct {
	ip  net.IP
	err error
}

// ipModel shows every local interface's addresses plus the machine's public
// IP, fetched in the background with a "looking up…" placeholder.
type ipModel struct {
	localsFn func() ([]network.Iface, error)
	publicFn func(context.Context) (net.IP, error)

	ifaces      []network.Iface
	localErr    error
	localLoaded bool

	public       net.IP
	publicErr    error
	publicLoaded bool

	cont key.Binding
}

func newIPScreen() ipModel {
	return ipModel{
		localsFn: network.LocalIPs,
		publicFn: func(ctx context.Context) (net.IP, error) { return network.PublicIP(ctx, nil) },
		cont:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "back")),
	}
}

// Init implements uictx.Screen.
func (m ipModel) Init() tea.Cmd {
	return tea.Batch(m.loadLocalsCmd(), m.loadPublicCmd())
}

func (m ipModel) loadLocalsCmd() tea.Cmd {
	fn := m.localsFn
	return func() tea.Msg {
		ifaces, err := fn()
		return ipLocalsMsg{ifaces: ifaces, err: err}
	}
}

func (m ipModel) loadPublicCmd() tea.Cmd {
	fn := m.publicFn
	return func() tea.Msg {
		ip, err := fn(context.Background())
		return ipPublicMsg{ip: ip, err: err}
	}
}

// Title implements uictx.Screen.
func (m ipModel) Title() string { return "My IP addresses" }

// ShortHelp implements uictx.Screen.
func (m ipModel) ShortHelp() []key.Binding { return []key.Binding{m.cont} }

// FullHelp implements uictx.Screen.
func (m ipModel) FullHelp() [][]key.Binding { return [][]key.Binding{{m.cont}} }

// Update implements uictx.Screen.
func (m ipModel) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case ipLocalsMsg:
		m.ifaces = msg.ifaces
		m.localErr = msg.err
		m.localLoaded = true
		return m, nil
	case ipPublicMsg:
		m.public = msg.ip
		m.publicErr = msg.err
		m.publicLoaded = true
		return m, nil
	case tea.KeyPressMsg:
		if key.Matches(msg, m.cont) {
			return m, uictx.Pop()
		}
	}
	return m, nil
}

// View implements uictx.Screen.
func (m ipModel) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	b.WriteString(th.CardTitle.Render("Local interfaces"))
	switch {
	case !m.localLoaded:
		b.WriteString("\n")
		b.WriteString(th.Muted.Render("Checking interfaces…"))
	case m.localErr != nil:
		b.WriteString("\n")
		b.WriteString(th.Danger.Render(ctx.Icons.Fail + " " + m.localErr.Error()))
	case len(m.ifaces) == 0:
		b.WriteString("\n")
		b.WriteString(th.Muted.Render("No active interfaces found."))
	default:
		for _, ifc := range m.ifaces {
			b.WriteString("\n")
			b.WriteString(th.Base.Render(ifc.Name))
			for _, ip := range ifc.IPv4 {
				b.WriteString("\n  ")
				b.WriteString(th.Info.Render(ip.String()))
			}
			for _, ip := range ifc.IPv6 {
				b.WriteString("\n  ")
				b.WriteString(th.Muted.Render(ip.String()))
			}
		}
	}

	b.WriteString("\n\n")
	b.WriteString(th.CardTitle.Render(ctx.Icons.Globe + " Public IP"))
	b.WriteString("\n")
	switch {
	case !m.publicLoaded:
		b.WriteString(th.Muted.Render("Looking up…"))
	case m.publicErr != nil:
		var offline *network.OfflineError
		if errors.As(m.publicErr, &offline) {
			b.WriteString(th.Warning.Render(ctx.Icons.Warn + " Offline — couldn't reach a public IP service."))
		} else {
			b.WriteString(th.Danger.Render(ctx.Icons.Fail + " " + m.publicErr.Error()))
		}
	default:
		b.WriteString(th.Success.Render(m.public.String()))
	}

	return th.Card.Render(b.String())
}
