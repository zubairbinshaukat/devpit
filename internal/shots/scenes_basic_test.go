//go:build shots

package shots

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/app"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/fonts"
	"github.com/zubairbinshaukat/devpit/internal/network"
	"github.com/zubairbinshaukat/devpit/internal/ports"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
	networkui "github.com/zubairbinshaukat/devpit/internal/ui/screens/network"
	portsui "github.com/zubairbinshaukat/devpit/internal/ui/screens/ports"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/settings"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/whatsnew"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/wt"
)

// sceneHome is the main menu, untouched.
func sceneHome(t *testing.T, e entry) *session {
	return newSession(t, e, demoConfig(), nil)
}

// firstRun builds the wizard scene that stops after n Enter presses.
func firstRun(n int, want string) scene {
	return func(t *testing.T, e entry) *session {
		cfg := demoConfig()
		cfg.FirstRunDone = false
		cfg.FontInstalled = true
		cfg.GlyphsConfirmed = false
		cfg.Icons = config.IconsAuto
		cfg.RecentFolders, cfg.NeverTouch, cfg.LifetimeFreedBytes = nil, nil, 0
		s := newSession(t, e, cfg, nil)
		for range n {
			s.key("enter")
		}
		s.waitFor(want)
		return s
	}
}

// --- Settings -------------------------------------------------------------

// settingsRows are the indexes of the settings list rows a scene walks to.
const (
	settingsFont    = 3
	settingsProbe   = 4
	settingsFolders = 5
	settingsNever   = 6
	settingsPorts   = 11
	settingsSkill   = 12
	settingsStats   = 13
	settingsAbout   = 15
)

// settingsScreen returns the settings section over demo hooks: the font is
// not installed, Windows Terminal is nowhere to be found and the AI agent
// skill is answered from memory, so opening any of it touches nothing.
func settingsScreen() uictx.Screen {
	return settings.NewWith(settings.Options{
		Skill: func() (settings.SkillService, error) { return demoSkill{}, nil },
		Tick:  func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil },
	}).WithHooks(settings.Hooks{
		FontStatus:    func(fonts.Options) (fonts.State, error) { return fonts.State{}, nil },
		FindTerminals: func(wt.FindOptions) []wt.Location { return nil },
		ClearCache:    func() error { return nil },
	})
}

// settingsAt opens Settings and walks the cursor down to row, then presses
// Enter when open is set.
func settingsAt(row int, open bool, want string) scene {
	return func(t *testing.T, e entry) *session {
		s := newSession(t, e, demoConfig(), screens{home.SectionSettings: settingsScreen})
		s.key(keySettings)
		s.waitFor("Changes save as soon as you make them")
		for range row {
			s.key("down")
		}
		if open {
			s.key("enter")
		}
		s.waitFor(want)
		return s
	}
}

// demoSkill is the AI agent skill on the demo PC: Claude Code is there and
// the skill is not installed yet.
type demoSkill struct{}

func (demoSkill) AgentStatus(context.Context) (service.AgentStatus, error) {
	return service.AgentStatus{
		ClaudeCode: true, Version: service.AgentSkillVersion,
		Targets: []service.AgentTarget{{Account: "default", File: `C:\Users\you\.claude\skills\devpit\SKILL.md`, State: service.AgentCreate}},
	}, nil
}

func (demoSkill) AgentInstall(ts []service.AgentTarget) ([]string, error) { return nil, nil }

func (demoSkill) AgentRemove(files []string) ([]string, error) { return nil, nil }

// sceneWhatsNew is the card a person sees once after updating to 0.4 from
// 0.3, with Claude Code on the demo PC and the skill not installed yet. The
// look at the skill is the app's own Init, which a scene never runs, so its
// answer is sent the way the app would send it.
func sceneWhatsNew(t *testing.T, e entry) *session {
	cfg := demoConfig()
	cfg.LastSeenVersion = "0.3.2"
	s := newSessionWith(t, e, cfg, nil, func(o *app.Options) {
		o.Version = "0.4.0"
		o.Skill = func() (settings.SkillService, error) { return demoSkill{}, nil }
	})
	show, older := settings.SkillOffer(func() (settings.SkillService, error) { return demoSkill{}, nil })
	s.send(whatsnew.OfferMsg{Show: show, Older: older})
	s.waitFor("What's new in Devpit 0.4", "AI agent skill")
	return s
}

// --- Network --------------------------------------------------------------

// networkHooks are demo answers for the three network tools: two interfaces,
// a documentation-range public address and a ping that answers four times.
func networkHooks() networkui.Hooks {
	return networkui.Hooks{
		LocalIPs: func() ([]network.Iface, error) {
			return []network.Iface{
				{
					Name: "Wi-Fi", Up: true,
					IPv4: []net.IP{net.ParseIP("192.168.1.42")},
					IPv6: []net.IP{net.ParseIP("fe80::4a2b:9cff:fe1d:7a10")},
				},
				{
					Name: "vEthernet (WSL)", Up: true,
					IPv4: []net.IP{net.ParseIP("172.28.16.1")},
				},
			}, nil
		},
		PublicIP: func(context.Context) (net.IP, error) { return net.ParseIP("203.0.113.42"), nil },
		Ping: func(_ context.Context, host string, _ int, onLine func(string), _ network.Options) (network.PingResult, error) {
			lines := []string{
				"Pinging " + host + " with 32 bytes of data:",
				"Reply from " + host + ": bytes=32 time=14ms TTL=58",
				"Reply from " + host + ": bytes=32 time=13ms TTL=58",
				"Reply from " + host + ": bytes=32 time=16ms TTL=58",
				"Reply from " + host + ": bytes=32 time=14ms TTL=58",
				"",
				"Ping statistics for " + host + ":",
				"    Packets: Sent = 4, Received = 4, Lost = 0 (0% loss),",
				"Approximate round trip times in milli-seconds:",
				"    Minimum = 13ms, Maximum = 16ms, Average = 14ms",
			}
			for _, l := range lines {
				onLine(l)
			}
			return network.PingResult{Sent: 4, Received: 4, MinMs: 13, AvgMs: 14, MaxMs: 16, Raw: lines}, nil
		},
		FlushDNS: func(context.Context) (string, error) {
			return "Successfully flushed the DNS Resolver Cache.", nil
		},
	}
}

// parentMenu is one of the two parent sections on its own, untouched: the
// Ports & Network or Install & Update menu, waiting for its lead-in line.
func parentMenu(key, lead string) scene {
	return func(t *testing.T, e entry) *session {
		s := newSession(t, e, demoConfig(), nil)
		s.key(key)
		s.waitFor(lead)
		return s
	}
}

// networkAt opens Ports & Network, then Network tools, walks to row and
// opens it (row < 0 stays on the menu).
func networkAt(row int, extra []string, want string) scene {
	return func(t *testing.T, e entry) *session {
		s := newSession(t, e, demoConfig(), screens{
			home.SectionNetwork: func() uictx.Screen { return networkui.New().WithHooks(networkHooks()) },
		})
		s.key(keyPortsNet)
		s.waitFor(portsNetLead)
		s.key(keyNetwork)
		s.waitFor("IP, connectivity and DNS helpers")
		if row >= 0 {
			for range row {
				s.key("down")
			}
			s.key("enter")
			for _, k := range extra {
				s.key(k)
			}
		}
		s.waitFor(want)
		return s
	}
}

// --- Ports ----------------------------------------------------------------

// machine is a pretend set of listeners and processes for the ports screens.
// Kill removes a process and its listeners, so the port really goes quiet
// afterwards, as it does on a real machine.
type machine struct {
	procs map[uint32]ports.Process
	conns []ports.Conn
	dead  map[uint32]bool
}

// listener builds a LISTEN row.
func listener(port uint16, pid uint32) ports.Conn {
	return ports.Conn{Proto: "tcp", LocalAddr: net.IPv4zero, LocalPort: port, State: "LISTEN", PID: pid}
}

// newMachine returns the demo listeners: a dev server on 3000 started from a
// PowerShell tab, Vite on 5173, an Angular server on 4200, a Java API on
// 8080, and the Windows System process on 8000, which Devpit will refuse to
// touch.
func newMachine() *machine {
	node := `C:\Program Files\nodejs\node.exe`
	return &machine{
		dead: map[uint32]bool{},
		procs: map[uint32]ports.Process{
			6032:  {PID: 6032, ParentPID: 2210, Name: "pwsh.exe", Exe: `C:\Program Files\PowerShell\7\pwsh.exe`},
			14820: {PID: 14820, ParentPID: 6032, Name: "node.exe", Exe: node},
			14944: {PID: 14944, ParentPID: 6032, Name: "node.exe", Exe: node},
			8844:  {PID: 8844, ParentPID: 6032, Name: "node.exe", Exe: node},
			17532: {PID: 17532, ParentPID: 6032, Name: "node.exe", Exe: node},
			20416: {PID: 20416, ParentPID: 2210, Name: "java.exe", Exe: `C:\Program Files\Eclipse Adoptium\jdk-21\bin\java.exe`},
			4: {
				PID: 4, Name: "System", Protected: true,
				ProtectedReason: "PID 4 is a core Windows System process and can never be terminated",
			},
			2210: {PID: 2210, ParentPID: 0, Name: "WindowsTerminal.exe", Exe: `C:\Program Files\WindowsApps\WindowsTerminal.exe`},
		},
		conns: []ports.Conn{
			listener(3000, 14820), listener(3001, 14944), listener(4200, 17532), listener(8000, 4),
			listener(5173, 8844), listener(8080, 20416),
		},
	}
}

// hooks returns the engine calls over the machine.
func (m *machine) hooks() portsui.Hooks {
	return portsui.Hooks{
		List: func(context.Context) ([]ports.Conn, error) { return m.live(), nil },
		ByPort: func(p uint16) ([]ports.Conn, error) {
			var out []ports.Conn
			for _, c := range m.live() {
				if c.LocalPort == p {
					out = append(out, c)
				}
			}
			return out, nil
		},
		Lookup: func(pid uint32) (ports.Process, error) {
			p, ok := m.procs[pid]
			if !ok {
				return ports.Process{}, errors.New("no such process")
			}
			return p, nil
		},
		Tree: func(pid uint32) []ports.Process {
			var out []ports.Process
			for _, p := range m.procs {
				if p.ParentPID == pid {
					out = append(out, p)
				}
			}
			return out
		},
		Kill:     func(pid uint32) error { m.dead[pid] = true; return nil },
		KillTree: func(pid uint32) error { m.dead[pid] = true; return nil },
		WaitFree: func(context.Context, uint16, time.Duration) bool { return true },
	}
}

// live is the listeners whose process has not been killed.
func (m *machine) live() []ports.Conn {
	var out []ports.Conn
	for _, c := range m.conns {
		if !m.dead[c.PID] {
			out = append(out, c)
		}
	}
	return out
}

// portsAt opens Ports & Network, then Fix stuck ports & apps, presses the
// given keys and waits for text.
func portsAt(keys []string, want string) scene {
	return func(t *testing.T, e entry) *session {
		s := newSession(t, e, demoConfig(), screens{
			home.SectionPorts: func() uictx.Screen { return portsui.New().WithHooks(newMachine().hooks()) },
		})
		s.key(keyPortsNet)
		s.waitFor(portsNetLead)
		s.key(keyPorts)
		s.waitFor("Free a busy port or stop a stuck process")
		for _, k := range keys {
			s.key(k)
		}
		s.waitFor(want)
		return s
	}
}
