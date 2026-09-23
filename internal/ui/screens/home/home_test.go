package home_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/about"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/settings"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// ctxAt is the context the app hands home at a given terminal size: a
// three-row header above the body and a two-row footer below it.
func ctxAt(w, h int) uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      icons.Unicode(),
		Width:      w,
		Height:     h,
		BodyHeight: h - 5,
		BodyTop:    3,
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

	big := m.View(ctxAt(100, 30))
	if !strings.Contains(big, "██████╗") || !strings.Contains(big, about.Byline) {
		t.Errorf("100x30 masthead lacks the wordmark or the byline:\n%s", big)
	}
	small := m.View(ctxAt(80, 24))
	if strings.Contains(small, "██████╗") || !strings.Contains(small, "D E V P I T") || !strings.Contains(small, about.Byline) {
		t.Errorf("80x24 masthead should be the compact mark with the byline:\n%s", small)
	}
}

func TestTabByDigit(t *testing.T) {
	if tab, ok := home.TabByDigit("3"); !ok || tab.ID != home.SectionInstall {
		t.Errorf("3 = %+v,%v want install", tab, ok)
	}
	if tab, ok := home.TabByDigit("7"); !ok || tab.ID != home.SectionSettings {
		t.Errorf("7 = %+v,%v want settings", tab, ok)
	}
	for _, bad := range []string{"0", "8", "a", "", "12"} {
		if _, ok := home.TabByDigit(bad); ok {
			t.Errorf("%q should not name a tab", bad)
		}
	}
}

func TestDigitOpensItsSection(t *testing.T) {
	ctx := ctxAt(100, 30)
	m := home.New(icons.Unicode())
	next, cmd := m.Update(tea.KeyPressMsg{Code: '7', Text: "7"}, ctx)
	s := pushed(t, next, cmd, ctx)
	if home.SectionFor(s) != home.SectionSettings {
		t.Errorf("7 opened %T, want the settings screen", s)
	}
}

// At 100x30 the body starts on row 3, the masthead is seven rows, then a
// blank line and the card border: the first menu row is terminal row 13 and
// the second entry (two rows each, no gaps) starts on row 15.
func TestClickOnAMenuRowOpensItsSection(t *testing.T) {
	ctx := ctxAt(100, 30)
	m := home.New(icons.Unicode())
	next, cmd := m.Update(tea.MouseClickMsg{X: 40, Y: 15, Button: tea.MouseLeft}, ctx)
	s := pushed(t, next, cmd, ctx)
	if home.SectionFor(s) != home.SectionPorts {
		t.Errorf("the click opened %T, want the ports screen", s)
	}

	// A click above the card is nothing.
	if _, cmd := m.Update(tea.MouseClickMsg{X: 40, Y: 5, Button: tea.MouseLeft}, ctx); cmd != nil {
		t.Error("a click on the masthead selected something")
	}
	// So is a right click on a row.
	if _, cmd := m.Update(tea.MouseClickMsg{X: 40, Y: 15, Button: tea.MouseRight}, ctx); cmd != nil {
		t.Error("a right click selected something")
	}
}

func TestSectionForKnowsTheScreens(t *testing.T) {
	if got := home.SectionFor(settings.New()); got != home.SectionSettings {
		t.Errorf("settings.New() = %q", got)
	}
	if got := home.SectionFor(home.New(icons.Unicode())); got != "" {
		t.Errorf("home itself = %q, want none", got)
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
