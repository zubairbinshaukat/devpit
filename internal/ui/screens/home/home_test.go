package home_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/about"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/footer"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/accounts"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/apps"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/network"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/ports"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/portsnet"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/settings"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// ctxAt is the context the app hands home at a given terminal size: the
// compact header (no tab row on home) above the body and the footer below.
func ctxAt(w, h int) uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      icons.Unicode(),
		Width:      w,
		Height:     h,
		BodyHeight: h - header.RowsCompact - footer.Rows,
		BodyTop:    header.RowsCompact,
	}
}

// pushed runs a command chain the way the program loop would until a screen
// is pushed, and returns that screen.
func pushed(t *testing.T, m uictx.Screen, cmd tea.Cmd, ctx uictx.Context) uictx.Screen {
	t.Helper()
	for range 4 {
		if cmd == nil {
			t.Fatal("nothing was pushed")
		}
		msg := cmd()
		if p, ok := msg.(uictx.PushScreenMsg); ok {
			return p.Screen
		}
		m, cmd = m.Update(msg, ctx)
	}
	t.Fatal("nothing was pushed after four rounds")
	return nil
}

func TestMastheadCarriesTheByline(t *testing.T) {
	m := home.New(icons.Unicode())

	// Six sections leave room for the block wordmark at 100x30.
	big := m.View(ctxAt(100, 30))
	if !strings.Contains(big, "██████╗") || !strings.Contains(big, about.Byline) {
		t.Errorf("100x32 masthead lacks the wordmark or the byline:\n%s", big)
	}
	small := m.View(ctxAt(80, 24))
	if strings.Contains(small, "██████╗") || !strings.Contains(small, "D E V P I T") || !strings.Contains(small, about.Byline) {
		t.Errorf("80x24 masthead should be the compact mark with the byline:\n%s", small)
	}
}

// TestMenuOrder pins the six entries and their order: Accounts first,
// Settings last, and none of the retired entries.
func TestMenuOrder(t *testing.T) {
	want := []string{
		home.SectionAccounts, home.SectionClean, home.SectionPortsNet,
		home.SectionApps, home.SectionShare, home.SectionSettings,
	}
	items := home.Items(icons.Unicode())
	if len(items) != len(want) {
		t.Fatalf("%d menu items, want %d", len(items), len(want))
	}
	for i, id := range want {
		if items[i].ID != id {
			t.Errorf("item %d is %q, want %q", i, items[i].ID, id)
		}
		if items[i].Desc == "" {
			t.Errorf("item %q has no description", id)
		}
	}
	for _, it := range items {
		if it.ID == home.SectionGitSSH {
			t.Error("the retired Git & SSH entry is still on the menu")
		}
	}
}

func TestTabByDigit(t *testing.T) {
	for digit, want := range map[string]string{
		"1": home.SectionAccounts,
		"2": home.SectionClean,
		"3": home.SectionPortsNet,
		"4": home.SectionApps,
		"5": home.SectionShare,
		"6": home.SectionSettings,
	} {
		if tab, ok := home.TabByDigit(digit); !ok || tab.ID != want {
			t.Errorf("%s = %+v,%v want %s", digit, tab, ok, want)
		}
	}
	for _, bad := range []string{"0", "7", "8", "9", "a", "", "12"} {
		if _, ok := home.TabByDigit(bad); ok {
			t.Errorf("%q should not name a tab", bad)
		}
	}
}

func TestDigitOpensItsSection(t *testing.T) {
	ctx := ctxAt(100, 30)
	m := home.New(icons.Unicode())
	next, cmd := m.Update(tea.KeyPressMsg{Code: '6', Text: "6"}, ctx)
	s := pushed(t, next, cmd, ctx)
	if home.SectionFor(s) != home.SectionSettings {
		t.Errorf("6 opened %T, want the settings screen", s)
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: '1', Text: "1"}, ctx)
	if s := pushed(t, next, cmd, ctx); home.SectionFor(s) != home.SectionAccounts {
		t.Errorf("1 opened %T, want the accounts screen", s)
	}
}

// Digits past the last section do nothing: no push, no cursor move.
func TestDigitsPastTheLastSectionDoNothing(t *testing.T) {
	ctx := ctxAt(100, 30)
	m := home.New(icons.Unicode())
	before := m.View(ctx)
	for _, d := range []rune{'7', '8', '9', '0'} {
		next, cmd := m.Update(tea.KeyPressMsg{Code: d, Text: string(d)}, ctx)
		if cmd != nil {
			if msg := cmd(); msg != nil {
				t.Errorf("%c produced %T, want nothing", d, msg)
			}
		}
		if next.View(ctx) != before {
			t.Errorf("%c changed the menu", d)
		}
	}
}

// rowOf is the terminal row a menu entry's title is drawn on, found the way
// the eye finds it rather than by counting the masthead by hand.
func rowOf(t *testing.T, m home.Model, ctx uictx.Context, title string) int {
	t.Helper()
	for i, line := range strings.Split(m.View(ctx), "\n") {
		if strings.Contains(line, title) {
			return ctx.BodyTop + i
		}
	}
	t.Fatalf("no %q row on home", title)
	return -1
}

func TestClickOnAMenuRowOpensItsSection(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {80, 24}} {
		ctx := ctxAt(size[0], size[1])
		m := home.New(icons.Unicode())
		y := rowOf(t, m, ctx, "Ports & Network")
		next, cmd := m.Update(tea.MouseClickMsg{X: 30, Y: y, Button: tea.MouseLeft}, ctx)
		s := pushed(t, next, cmd, ctx)
		if home.SectionFor(s) != home.SectionPortsNet {
			t.Errorf("%dx%d: the click opened %T, want the Ports & Network menu", size[0], size[1], s)
		}

		// A click above the card is nothing.
		if _, cmd := m.Update(tea.MouseClickMsg{X: 30, Y: ctx.BodyTop, Button: tea.MouseLeft}, ctx); cmd != nil {
			t.Error("a click on the masthead selected something")
		}
		// So is a right click on a row.
		if _, cmd := m.Update(tea.MouseClickMsg{X: 30, Y: y, Button: tea.MouseRight}, ctx); cmd != nil {
			t.Error("a right click selected something")
		}
	}
}

func TestSectionForKnowsTheScreens(t *testing.T) {
	cases := []struct {
		name   string
		screen uictx.Screen
		want   string
	}{
		{"accounts", accounts.New(), home.SectionAccounts},
		{"portsnet", portsnet.New(), home.SectionPortsNet},
		{"ports", ports.New(), home.SectionPortsNet},
		{"network", network.New(), home.SectionPortsNet},
		{"apps", apps.New(), home.SectionApps},
		{"settings", settings.New(), home.SectionSettings},
		{"home", home.New(icons.Unicode()), ""},
	}
	for _, c := range cases {
		if got := home.SectionFor(c.screen); got != c.want {
			t.Errorf("SectionFor(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}

// The parent sections hand the factory map on, so a fake keyed by a screen
// inside them is what their menu opens.
func TestFactoriesReachThroughTheParentMenus(t *testing.T) {
	ctx := ctxAt(100, 30)
	fake := settings.New()
	m := home.New(icons.Unicode()).WithFactories(map[string]func() uictx.Screen{
		home.SectionNetwork: func() uictx.Screen { return fake },
	})
	parent := pushed(t, m, m.Open(home.SectionPortsNet), ctx)
	if _, ok := parent.(portsnet.Model); !ok {
		t.Fatalf("Ports & Network opened %T", parent)
	}
	next, cmd := parent.Update(tea.KeyPressMsg{Code: '2', Text: "2"}, ctx)
	if s := pushed(t, next, cmd, ctx); home.SectionFor(s) != home.SectionSettings {
		t.Errorf("Network tools opened %T, want the injected fake", s)
	}
	if home.New(icons.Unicode()).Open(home.SectionGitSSH) != nil {
		t.Error("home still opens the retired Git & SSH screen")
	}
}

func TestTabsMatchTheMenuOrder(t *testing.T) {
	tabs := home.Tabs()
	items := home.Items(icons.Unicode())
	if len(tabs) != len(items) {
		t.Fatalf("%d tabs, %d menu items", len(tabs), len(items))
	}
	for i := range tabs {
		if tabs[i].ID != items[i].ID {
			t.Errorf("tab %d is %q, menu item is %q", i, tabs[i].ID, items[i].ID)
		}
	}
}
