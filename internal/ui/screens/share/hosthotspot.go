package share

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Suggester is optional on a Hoster: an engine that can say whether a direct
// Wi-Fi hotspot is worth offering for this share. The screen checks for it
// with a type assertion, so a Hoster without it (a fake, or an older engine)
// simply never offers hotspot mode. *host.Manager is the real one.
type Suggester interface {
	Suggest(ctx context.Context) host.Suggestion
}

// The menu IDs of the hotspot rows.
const (
	hotspotRowID = "hotspot"
	bandSwitchID = "band-switch"
	bandTempID   = "band-temp"
	bandKeepID   = "band-keep"
)

// suggestCmdOf runs the engine's suggestion off the update loop, next to the
// adapter list. An engine without one suggests nothing.
func suggestOf(ctx context.Context, mgr Hoster) host.Suggestion {
	if s, ok := mgr.(Suggester); ok {
		return s.Suggest(ctx)
	}
	return host.Suggestion{}
}

// linkHint writes an adapter's negotiated link, such as "Wi-Fi 6 · 1201 Mbps
// link", or "" when Windows did not say. It is the link rate, not a copy
// speed.
func linkHint(standard string, bps uint64) string {
	var p []string
	if standard != "" {
		p = append(p, standard)
	}
	if bps > 0 {
		p = append(p, fmt.Sprintf("%d Mbps link", bps/1_000_000))
	}
	return strings.Join(p, " · ")
}

// adapterItems builds the connection list: the hotspot row first when it is
// suggested, last when it is only offered, and one row per adapter with its
// link.
func (m hostScreen) adapterItems() []menu.Item {
	items := make([]menu.Item, 0, len(m.adapters)+1)
	for i, a := range m.adapters {
		desc := "Other PCs reach this one at " + a.IP.String()
		if a.Best {
			desc += ". Windows uses it for the internet."
		}
		items = append(items, menu.Item{
			ID: fmt.Sprint(i), Title: a.Name + "  " + a.IP.String(), Desc: desc,
			Hint: linkHint(a.Standard, a.LinkBps),
		})
	}
	s := m.suggest
	if !s.Offer {
		return items
	}
	row := menu.Item{ID: hotspotRowID, Title: "Direct hotspot"}
	if s.Suggested {
		row.Title += " (suggested)"
		row.Desc = "This PC starts its own Wi-Fi hotspot. The other PC joins it directly."
		return append([]menu.Item{row}, items...)
	}
	row.Desc = "This PC starts its own Wi-Fi hotspot and the other PC joins it."
	if s.Hint != "" {
		row.Desc += " " + s.Hint
	}
	return append(items, row)
}

// chooseHotspot starts hotspot mode, asking about the band first when the
// saved hotspot is not on 5 GHz.
func (m hostScreen) chooseHotspot() (uictx.Screen, tea.Cmd) {
	if m.suggest.NeedsBandChoice {
		items := []menu.Item{{
			ID: bandSwitchID, Title: "Switch my hotspot to 5 GHz",
			Desc: "Keeps its name and password. The setting stays on 5 GHz after sharing.",
		}}
		if m.suggest.TempSupported {
			items = append(items, menu.Item{
				ID: bandTempID, Title: "Use a temporary name and password on 5 GHz",
				Desc: "Your own hotspot settings are not touched.",
			})
		}
		items = append(items, menu.Item{
			ID: bandKeepID, Title: "Keep it as it is (slower)",
			Desc: "Uses your saved hotspot as it is, which may be on 2.4 GHz.",
		})
		m.bandMenu = menu.New(items)
		m.stage = hostBand
		return m, nil
	}
	return m.beginHotspot(host.HotspotSaved)
}

// onBandKey handles the band options.
func (m hostScreen) onBandKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd) {
	if msg.Code == tea.KeyEscape {
		m.stage = hostAdapters
		return m, nil
	}
	next, cmd := m.bandMenu.Update(msg)
	m.bandMenu = next
	if it, ok := m.bandMenu.Selected(); ok && msg.Code == tea.KeyEnter {
		switch it.ID {
		case bandSwitchID:
			return m.beginHotspot(host.HotspotSavedSwitchTo5)
		case bandTempID:
			return m.beginHotspot(host.HotspotTemp)
		default:
			return m.beginHotspot(host.HotspotSaved)
		}
	}
	return m, cmd
}

// beginHotspot starts the share in hotspot mode. It never asks the Private
// question: hotspot mode opens the firewall for the hotspot only.
func (m hostScreen) beginHotspot(choice host.HotspotChoice) (uictx.Screen, tea.Cmd) {
	m.adapter = m.suggest.Adapter
	return m.start(host.Options{Path: m.path, Adapter: m.adapter, Mode: host.ModeHotspot, Hotspot: choice})
}

// fallbackPrivate handles a hotspot that did not start on a PC whose normal
// network is Public: the engine kept its worker open, so a yes shares over
// the normal network with no second admin prompt, and a no takes it down.
func (m hostScreen) fallbackPrivate(err error) (uictx.Screen, tea.Cmd, bool) {
	var fb *host.HotspotFallbackError
	if !errors.As(err, &fb) || !errors.Is(err, host.ErrNeedPrivate) {
		return m, nil, false
	}
	m.fallback = fb.Message
	m.adapter = m.suggest.Adapter
	m.stage = hostPrivate
	m.confirm = confirm.New(privateID,
		"Share over your network instead?",
		fb.Message+" Windows blocks file sharing on a Public network, so Devpit would switch it to Private "+
			"while you share and put it back when you stop.")
	return m, nil, true
}

// hotspotSteps is the step list of a hotspot share, in the screen's words.
func (m hostScreen) hotspotSteps() []host.Step {
	out := []host.Step{}
	if m.opts.Hotspot == host.HotspotSavedSwitchTo5 {
		out = append(out, host.Step{Key: "band5", Label: "Switch your hotspot to 5 GHz"})
	}
	out = append(out,
		host.Step{Key: "hotspot", Label: "Start the direct Wi-Fi hotspot"},
		host.Step{Key: "hsfirewall", Label: "Allow sharing on the hotspot"},
	)
	// The engine also opens the normal network as a second way in when it
	// is already Private; the row shows only when that step happens.
	for _, s := range m.steps {
		if s.Key == "firewall" {
			out = append(out, host.Step{Key: "firewall", Label: "Allow sharing on your network too"})
			break
		}
	}
	return append(out,
		host.Step{Key: "login", Label: "Create a temporary login"},
		host.Step{Key: "folder", Label: "Let that login read the folder"},
		host.Step{Key: "share", Label: "Share the folder read-only"},
	)
}

// bandText writes a band for the card.
func bandText(b string) string {
	switch b {
	case "2.4", "5", "6":
		return b + " GHz"
	case "auto":
		return "chosen by Windows"
	}
	return ""
}

// hotspotCardBody is the top of the card in hotspot mode: how the other PC
// joins, then how it signs in.
func (m hostScreen) hotspotCardBody(ctx uictx.Context, b *strings.Builder) {
	th := ctx.Theme
	c := m.card
	label := func(s string) string { return th.Muted.Render(fmt.Sprintf("%-10s", s)) }
	b.WriteString(th.Muted.Render("1. On the other PC, join this Wi-Fi:") + "\n")
	b.WriteString(label("Hotspot") + th.Info.Render(c.SSID))
	if bt := bandText(c.Band); bt != "" {
		b.WriteString(th.Muted.Render("  " + bt))
	}
	b.WriteString("\n")
	pass := c.HotspotPassword
	if pass == "" {
		pass = "your saved hotspot password (Settings > Network & internet > Mobile hotspot)"
	}
	b.WriteString(label("Password") + th.Base.Bold(true).Render(pass) + "\n")
	if c.OwnPassword && c.HotspotPassword != "" {
		b.WriteString(label("") + th.Muted.Render("This is your own hotspot password.") + "\n")
	}
	b.WriteString("\n" + th.Muted.Render("2. Then sign in to the shared folder:") + "\n")
	b.WriteString(label("Address") + th.Info.Render(c.IP) + "\n")
	b.WriteString(label("Share") + th.Base.Render(c.Share) + "\n")
	b.WriteString(label("User") + th.Base.Render(c.User) + "\n")
	b.WriteString(label("Code") + th.Base.Bold(true).Render(c.Password) + "\n")
	if c.NormalIP != "" {
		b.WriteString(label("") + th.Muted.Render("If the hotspot is slow, use "+c.NormalIP+" over your network.") + "\n")
	}
	b.WriteString("\n")
}
