package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/about"
	"github.com/zubairbinshaukat/devpit/internal/app"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/selfupdate"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
)

const (
	cleanSentinel    = "Pick what to look through"
	portsSentinel    = "Free a busy port or stop a stuck process"
	settingsSentinel = "Changes save as soon as you make them"
)

func tab() tea.KeyPressMsg      { return tea.KeyPressMsg{Code: tea.KeyTab} }
func shiftTab() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift} }

func homeAt100x30(t *testing.T) tea.Model {
	t.Helper()
	cfg := config.Default()
	cfg.FirstRunDone = true
	m := tea.Model(app.New(testOptions(cfg, &saveSpy{})))
	return drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})
}

func TestTabWalksTheSections(t *testing.T) {
	m := homeAt100x30(t)

	m = drive(m, tab())
	if !strings.Contains(view(m), cleanSentinel) {
		t.Fatalf("Tab from home did not open the first section:\n%s", view(m))
	}
	m = drive(m, tab())
	if !strings.Contains(view(m), portsSentinel) {
		t.Fatalf("a second Tab did not open the second section:\n%s", view(m))
	}
	m = drive(m, shiftTab())
	if !strings.Contains(view(m), cleanSentinel) {
		t.Fatalf("Shift+Tab did not go back a section:\n%s", view(m))
	}
	m = drive(m, shiftTab())
	if !strings.Contains(view(m), settingsSentinel) {
		t.Fatalf("Shift+Tab from the first section did not wrap to the last:\n%s", view(m))
	}
	// Esc still returns home from a section opened by tab.
	m = drive(m, press("esc"))
	if !strings.Contains(view(m), about.Byline) {
		t.Errorf("Esc did not return to the main menu:\n%s", view(m))
	}
}

func TestDigitOpensASectionFromHome(t *testing.T) {
	m := drive(homeAt100x30(t), press("7"))
	if !strings.Contains(view(m), settingsSentinel) {
		t.Fatalf("7 did not open settings:\n%s", view(m))
	}
}

func TestClickOnTheTabBarOpensThatSection(t *testing.T) {
	// Find the column the Settings tab is drawn at, the same way a user's
	// eye does, rather than hard-coding it.
	h := header.New()
	h.Tabs = home.Tabs()
	x := -1
	for col := 0; col < 100; col++ {
		if tb, ok := h.TabAt(col); ok && tb.ID == home.SectionSettings {
			x = col + 2
			break
		}
	}
	if x < 0 {
		t.Fatal("the settings tab is not on the bar")
	}

	m := homeAt100x30(t)
	m = drive(m, tea.MouseClickMsg{X: x, Y: header.TabRow, Button: tea.MouseLeft})
	if !strings.Contains(view(m), settingsSentinel) {
		t.Fatalf("clicking the Settings tab did not open settings:\n%s", view(m))
	}

	// Clicking the tab of the open section changes nothing.
	m = drive(m, tea.MouseClickMsg{X: x, Y: header.TabRow, Button: tea.MouseLeft})
	if !strings.Contains(view(m), settingsSentinel) {
		t.Errorf("re-clicking the open tab closed it:\n%s", view(m))
	}
	// A click on the rule under the tabs is nobody's.
	m = drive(m, tea.MouseClickMsg{X: x, Y: header.TabRow + 1, Button: tea.MouseLeft})
	if !strings.Contains(view(m), settingsSentinel) {
		t.Errorf("a click on the rule changed the screen:\n%s", view(m))
	}
}

func TestClickOnAHomeRowOpensItsSection(t *testing.T) {
	// The second menu entry sits on terminal row 15 at 100x30: three header
	// rows, a seven-row masthead, a blank line, the card border, then two
	// rows per entry.
	m := drive(homeAt100x30(t), tea.MouseClickMsg{X: 40, Y: 15, Button: tea.MouseLeft})
	if !strings.Contains(view(m), portsSentinel) {
		t.Fatalf("clicking the ports row did not open it:\n%s", view(m))
	}
}

func TestTabsLeaveABusyScreenAlone(t *testing.T) {
	m, screen := withBusyScreen(t)
	m = drive(m, tab())
	if !strings.Contains(view(m), "deleting things") {
		t.Fatalf("Tab tore down a busy screen:\n%s", view(m))
	}
	m = drive(m, tea.MouseClickMsg{X: 3, Y: header.TabRow, Button: tea.MouseLeft})
	if !strings.Contains(view(m), "deleting things") || screen.stopped != 0 {
		t.Fatalf("a tab click tore down a busy screen (stopped=%d):\n%s", screen.stopped, view(m))
	}
}

// updateOptions is testOptions plus a scripted release check.
func updateOptions(cfg config.Config, current string, check func(context.Context) (selfupdate.Result, error)) app.Options {
	o := testOptions(cfg, &saveSpy{})
	o.Version = current
	o.CheckUpdate = check
	o.ExePath = `C:\Users\me\scoop\apps\devpit\current\devpit.exe`
	return o
}

func TestUpdateNoticeShowsWhereAndHow(t *testing.T) {
	cfg := config.Default()
	cfg.FirstRunDone = true
	m := tea.Model(app.New(updateOptions(cfg, "0.1.0", func(context.Context) (selfupdate.Result, error) {
		return selfupdate.Result{Latest: "0.2.0", URL: "https://example.test/rel"}, nil
	})))
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = drive(m, runCmd(m.Init())...)

	out := view(m)
	if !strings.Contains(out, "update") || !strings.Contains(out, "v0.2.0") {
		t.Errorf("the header has no update pill:\n%s", out)
	}
	if !strings.Contains(out, "scoop update devpit") {
		t.Errorf("the footer does not say how to upgrade:\n%s", out)
	}

	// The About screen repeats it, with the release page.
	m = drive(m, press("7"))
	for range 14 {
		m = drive(m, press("down"))
	}
	m = drive(m, press("enter"))
	out = view(m)
	for _, want := range []string{"Update available: v0.2.0", "scoop update devpit", "https://example.test/rel"} {
		if !strings.Contains(out, want) {
			t.Errorf("About lacks %q:\n%s", want, out)
		}
	}
}

func TestUpdateCheckNeverRunsWhenItMustNot(t *testing.T) {
	cases := []struct {
		name    string
		current string
		mutate  func(*config.Config)
	}{
		{"dev build", "dev", func(*config.Config) {}},
		{"setting off", "0.1.0", func(c *config.Config) { c.SkipUpdateCheck = true }},
		{"first run", "0.1.0", func(c *config.Config) { c.FirstRunDone = false }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.FirstRunDone = true
			c.mutate(&cfg)
			called := false
			m := tea.Model(app.New(updateOptions(cfg, c.current, func(context.Context) (selfupdate.Result, error) {
				called = true
				return selfupdate.Result{}, errors.New("must not be called")
			})))
			m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})
			drive(m, runCmd(m.Init())...)
			if called {
				t.Error("the release check ran")
			}
		})
	}
}

func TestUpToDateBuildSaysNothing(t *testing.T) {
	cfg := config.Default()
	cfg.FirstRunDone = true
	m := tea.Model(app.New(updateOptions(cfg, "0.2.0", func(context.Context) (selfupdate.Result, error) {
		return selfupdate.Result{Latest: "0.2.0"}, nil
	})))
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = drive(m, runCmd(m.Init())...)
	if out := view(m); strings.Contains(out, "update ") && strings.Contains(out, "v0.2.0 ") {
		t.Errorf("an up-to-date build shows an update pill:\n%s", out)
	}
}
