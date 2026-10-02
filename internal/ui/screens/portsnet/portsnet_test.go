package portsnet_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/network"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/ports"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/portsnet"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// ctxAt is the context the app hands a section screen: the four-row header
// with its tab bar above the body and the two-row footer below it.
func ctxAt(w, h int, set icons.Set) uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      set,
		Config:     config.Default(),
		Width:      w,
		Height:     h,
		BodyHeight: h - 6,
		BodyTop:    4,
	}
}

// selectAndPush feeds a key or click, then the SelectedMsg it produces, and
// returns the updated menu and the screen pushed.
func selectAndPush(t *testing.T, m uictx.Screen, msg tea.Msg, ctx uictx.Context) (uictx.Screen, uictx.Screen) {
	t.Helper()
	m, cmd := m.Update(msg, ctx)
	for range 3 {
		if cmd == nil {
			return m, nil
		}
		out := cmd()
		if p, ok := out.(uictx.PushScreenMsg); ok {
			return m, p.Screen
		}
		m, cmd = m.Update(out, ctx)
	}
	return m, nil
}

func TestEnterOpensEachEntry(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := portsnet.New()

	_, s := selectAndPush(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}, ctx)
	if _, ok := s.(ports.Model); !ok {
		t.Errorf("Enter on the first entry pushed %T, want the ports screen", s)
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown}, ctx)
	_, s = selectAndPush(t, next, tea.KeyPressMsg{Code: tea.KeyEnter}, ctx)
	if _, ok := s.(network.Model); !ok {
		t.Errorf("Enter on the second entry pushed %T, want the network screen", s)
	}
}

func TestDigitsOpenEntries(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := portsnet.New()
	if _, s := selectAndPush(t, m, tea.KeyPressMsg{Code: '1', Text: "1"}, ctx); s == nil {
		t.Error("1 opened nothing")
	} else if _, ok := s.(ports.Model); !ok {
		t.Errorf("1 pushed %T, want ports", s)
	}
	if _, s := selectAndPush(t, m, tea.KeyPressMsg{Code: '2', Text: "2"}, ctx); s == nil {
		t.Error("2 opened nothing")
	} else if _, ok := s.(network.Model); !ok {
		t.Errorf("2 pushed %T, want network", s)
	}
	for _, d := range []rune{'0', '3', '9'} {
		if _, s := selectAndPush(t, m, tea.KeyPressMsg{Code: d, Text: string(d)}, ctx); s != nil {
			t.Errorf("%c pushed %T, want nothing", d, s)
		}
	}
}

// A click on an entry's title or description row opens it, at both sizes;
// a click on the lead-in opens nothing.
func TestClickOpensAnEntry(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {80, 24}} {
		ctx := ctxAt(size[0], size[1], icons.Unicode())
		m := portsnet.New()
		lines := strings.Split(ansi.Strip(m.View(ctx)), "\n")
		y := -1
		for i, l := range lines {
			if strings.Contains(l, "Network tools") {
				y = ctx.BodyTop + i
			}
		}
		if y < 0 {
			t.Fatalf("no Network tools row:\n%s", strings.Join(lines, "\n"))
		}
		_, s := selectAndPush(t, m, tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft}, ctx)
		if _, ok := s.(network.Model); !ok {
			t.Errorf("%v: clicking Network tools pushed %T", size, s)
		}
		if _, s := selectAndPush(t, m, tea.MouseClickMsg{X: 10, Y: ctx.BodyTop, Button: tea.MouseLeft}, ctx); s != nil {
			t.Errorf("%v: a click on the lead-in pushed %T", size, s)
		}
	}
}

// Hovering a row moves the highlight there without opening it.
func TestHoverHighlights(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := portsnet.New()
	lines := strings.Split(ansi.Strip(m.View(ctx)), "\n")
	y := -1
	for i, l := range lines {
		if strings.Contains(l, "Network tools") {
			y = ctx.BodyTop + i
		}
	}
	next, cmd := m.Update(tea.MouseMotionMsg{X: 10, Y: y}, ctx)
	if cmd != nil {
		t.Error("hover produced a command")
	}
	for _, l := range strings.Split(ansi.Strip(next.View(ctx)), "\n") {
		if strings.Contains(l, "Network tools") && !strings.Contains(l, icons.Unicode().Cursor) {
			t.Errorf("hovered row has no cursor: %q", l)
		}
	}
}

// Fakes injected by id are what the menu opens; an id the map leaves out
// keeps its real screen, and a factory that gives nothing leaves the menu
// standing rather than pushing an empty screen.
func TestFactories(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	fake := network.New()
	m := portsnet.New().WithFactories(map[string]func() uictx.Screen{
		portsnet.ItemPorts:   func() uictx.Screen { return nil },
		portsnet.ItemNetwork: func() uictx.Screen { return fake },
	})
	if _, s := selectAndPush(t, m, tea.KeyPressMsg{Code: '1', Text: "1"}, ctx); s != nil {
		t.Errorf("a factory that returned nil still pushed %T", s)
	}
	if _, s := selectAndPush(t, m, tea.KeyPressMsg{Code: '2', Text: "2"}, ctx); s == nil {
		t.Error("the injected network screen was not pushed")
	}
}

func TestBreadcrumbNamesTheOpenEntry(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	next, _ := selectAndPush(t, portsnet.New(), tea.KeyPressMsg{Code: '1', Text: "1"}, ctx)
	m, ok := next.(portsnet.Model)
	if !ok {
		t.Fatalf("the menu became %T", next)
	}
	if got := m.Breadcrumb("Ports", true); got != "Ports & Network › Fix stuck ports" {
		t.Errorf("direct crumb = %q", got)
	}
	if got := m.Breadcrumb("Ports › step", true); got != "Ports & Network › Fix stuck ports › step" {
		t.Errorf("a direct crumb dropped the screen's own suffix: %q", got)
	}
	if got := m.Breadcrumb("Ping a host", false); got != "Ports & Network › Ping a host" {
		t.Errorf("deeper crumb = %q", got)
	}
}

// The menu fits both supported sizes in both fallback tiers: no line is
// wider than the terminal and both entries are on screen.
func TestFitsAtBothSizes(t *testing.T) {
	for _, set := range []icons.Set{icons.Unicode(), icons.ASCII()} {
		for _, size := range [][2]int{{100, 30}, {80, 24}} {
			ctx := ctxAt(size[0], size[1], set)
			out := ansi.Strip(portsnet.New().View(ctx))
			for _, l := range strings.Split(out, "\n") {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("%s %v: line is %d wide: %q", set.Tier, size, w, l)
				}
			}
			for _, want := range []string{"Fix stuck ports & apps", "Network tools", "Free a busy port, check your connection."} {
				if !strings.Contains(out, want) {
					t.Errorf("%s %v: missing %q:\n%s", set.Tier, size, want, out)
				}
			}
			if lines := strings.Count(out, "\n") + 1; lines > ctx.BodyHeight {
				t.Errorf("%s %v: %d lines in a %d-row body", set.Tier, size, lines, ctx.BodyHeight)
			}
		}
	}
}
