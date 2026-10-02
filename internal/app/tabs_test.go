package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/about"
	"github.com/zubairbinshaukat/devpit/internal/app"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/selfupdate"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/apps"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/portsnet"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The first line each section's screen draws, keyed by section id, so a test
// can tell which screen is up without knowing the menu order.
var sentinels = map[string]string{
	home.SectionAccounts: "set by this project's .env.local",
	home.SectionClean:    "Pick what to look through",
	home.SectionPortsNet: "Free a busy port, check your connection.",
	home.SectionApps:     "Install dev apps, update everything.",
	home.SectionShare:    "Move big folders between two PCs on the same Wi-Fi",
	home.SectionSettings: "Changes save as soon as you make them",
}

const (
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

// TestTabWalksTheSections cycles the whole bar both ways: Tab from home opens
// the first section, every Tab after it the next, wrapping from the last back
// to the first, and Shift+Tab walks the same ring backwards.
func TestTabWalksTheSections(t *testing.T) {
	tabs := home.Tabs()
	if len(tabs) != 6 {
		t.Fatalf("%d tabs, want 6", len(tabs))
	}

	m := homeAt100x30(t)
	for i := range len(tabs) + 1 {
		m = drive(m, tab())
		want := tabs[i%len(tabs)].ID
		if !strings.Contains(view(m), sentinels[want]) {
			t.Fatalf("Tab %d did not open %s:\n%s", i+1, want, view(m))
		}
	}
	// One Tab past the end wrapped to the first; Shift+Tab wraps back.
	m = drive(m, shiftTab())
	if !strings.Contains(view(m), sentinels[tabs[len(tabs)-1].ID]) {
		t.Fatalf("Shift+Tab from the first section did not wrap to the last:\n%s", view(m))
	}
	for i := len(tabs) - 2; i >= 0; i-- {
		m = drive(m, shiftTab())
		if !strings.Contains(view(m), sentinels[tabs[i].ID]) {
			t.Fatalf("Shift+Tab did not go back to %s:\n%s", tabs[i].ID, view(m))
		}
	}
	// Esc still returns home from a section opened by tab.
	m = drive(m, press("esc"))
	if !strings.Contains(view(m), about.Byline) {
		t.Errorf("Esc did not return to the main menu:\n%s", view(m))
	}

	// Shift+Tab from home starts at the last section.
	m = drive(homeAt100x30(t), shiftTab())
	if !strings.Contains(view(m), sentinels[tabs[len(tabs)-1].ID]) {
		t.Errorf("Shift+Tab from home did not open the last section:\n%s", view(m))
	}
}

// Inside a parent section's screen, Tab moves on from the parent's section,
// not from wherever the child would sit on its own.
func TestTabFromAChildMovesToTheNextSection(t *testing.T) {
	m := walkTo(t, homeAt100x30(t), home.SectionPortsNet, portsnet.ItemNetwork)
	m = drive(m, tab())
	if !strings.Contains(view(m), sentinels[home.SectionApps]) {
		t.Fatalf("Tab from Network tools did not open Install & Update:\n%s", view(m))
	}
}

func TestDigitOpensASectionFromHome(t *testing.T) {
	for _, tb := range home.Tabs() {
		m := drive(homeAt100x30(t), digitFor(t, tb.ID))
		if !strings.Contains(view(m), sentinels[tb.ID]) {
			t.Errorf("the digit for %s did not open it:\n%s", tb.ID, view(m))
		}
	}
	// Digits past the last section do nothing on home.
	for _, d := range []string{"7", "8", "9", "0"} {
		m := drive(homeAt100x30(t), press(d))
		if !strings.Contains(view(m), about.Byline) {
			t.Errorf("%s left home:\n%s", d, view(m))
		}
	}
}

// The six tabs and their hint fit an 80-column terminal on one row, with no
// label cut, so every tab can be clicked at the smallest supported size.
func TestTabsFitEightyColumns(t *testing.T) {
	m := walkTo(t, homeAt80x24(t), home.SectionSettings, "")
	row := strings.Split(ansi.Strip(view(m)), "\n")[header.TabRow]
	if w := ansi.StringWidth(row); w > 80 {
		t.Errorf("tab row is %d wide: %q", w, row)
	}
	for _, tb := range home.Tabs() {
		if !strings.Contains(row, " "+tb.Label+" ") {
			t.Errorf("tab %q is missing or cut: %q", tb.Label, row)
		}
	}
	if !strings.Contains(row, "1–6") {
		t.Errorf("the hint does not offer 1–6: %q", row)
	}
}

func homeAt80x24(t *testing.T) tea.Model {
	t.Helper()
	cfg := config.Default()
	cfg.FirstRunDone = true
	m := tea.Model(app.New(testOptions(cfg, &saveSpy{})))
	return drive(m, tea.WindowSizeMsg{Width: 80, Height: 24})
}

func TestClickOnTheTabBarOpensThatSection(t *testing.T) {
	// Find the column the Settings tab is drawn at, the same way a user's
	// eye does, rather than hard-coding it.
	h := header.New()
	h.Tabs = home.Tabs()
	h.ShowTabs = true
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

	// Home draws no tab bar, so the same click there lands on the rule
	// under the badge row and does nothing.
	m := homeAt100x30(t)
	m = drive(m, tea.MouseClickMsg{X: x, Y: header.TabRow, Button: tea.MouseLeft})
	if strings.Contains(view(m), settingsSentinel) {
		t.Fatalf("home has no tab bar, yet a click on row %d opened settings", header.TabRow)
	}

	// Inside a section the bar is there, and the click switches to it, from
	// a screen inside a parent section too.
	m = walkTo(t, m, home.SectionPortsNet, portsnet.ItemPorts)
	if !strings.Contains(view(m), portsSentinel) {
		t.Fatalf("did not reach the ports screen:\n%s", view(m))
	}
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

// rowOf is the terminal row the first line holding text is drawn on, found
// the way the eye finds it rather than by counting rows by hand.
func rowOf(t *testing.T, m tea.Model, text string) int {
	t.Helper()
	for i, line := range strings.Split(ansi.Strip(view(m)), "\n") {
		if strings.Contains(line, text) {
			return i
		}
	}
	t.Fatalf("no %q on screen:\n%s", text, view(m))
	return -1
}

func TestClickOnAHomeRowOpensItsSection(t *testing.T) {
	for _, m := range []tea.Model{homeAt100x30(t), homeAt80x24(t)} {
		m = drive(m, tea.MouseClickMsg{X: 30, Y: rowOf(t, m, "Ports & Network"), Button: tea.MouseLeft})
		if !strings.Contains(view(m), sentinels[home.SectionPortsNet]) {
			t.Fatalf("clicking the Ports & Network row did not open it:\n%s", view(m))
		}
		// And a click on an entry of the parent's menu opens its screen.
		m = drive(m, tea.MouseClickMsg{X: 10, Y: rowOf(t, m, "Fix stuck ports & apps"), Button: tea.MouseLeft})
		if !strings.Contains(view(m), portsSentinel) {
			t.Fatalf("clicking Fix stuck ports & apps did not open it:\n%s", view(m))
		}
	}
}

// Hovering a home row highlights it, so its description appears in place.
func TestHoverOnAHomeRowShowsItsDescription(t *testing.T) {
	const desc = "Install dev apps, update everything"
	m := homeAt100x30(t)
	if strings.Contains(view(m), desc) {
		t.Fatal("an unhighlighted row already shows its description")
	}
	m = drive(m, tea.MouseMotionMsg{X: 30, Y: rowOf(t, m, "Install & Update")})
	if !strings.Contains(view(m), desc) {
		t.Errorf("hovering Install & Update did not show its description:\n%s", view(m))
	}
}

// Esc from a parent's screen goes back to the parent's menu, and Esc again
// to home: one router entry at a time.
func TestEscWalksBackThroughAParent(t *testing.T) {
	m := walkTo(t, homeAt100x30(t), home.SectionApps, apps.ItemUpdate)
	if !strings.Contains(view(m), "updates available") {
		t.Fatalf("did not reach the update screen:\n%s", view(m))
	}
	m = drive(m, press("esc"))
	if !strings.Contains(view(m), sentinels[home.SectionApps]) {
		t.Fatalf("Esc did not land on Install & Update:\n%s", view(m))
	}
	if first := strings.Split(ansi.Strip(view(m)), "\n")[0]; strings.Contains(first, "›") {
		t.Errorf("the parent's own breadcrumb still names a child: %q", first)
	}
	m = drive(m, press("esc"))
	if !strings.Contains(view(m), about.Byline) {
		t.Errorf("a second Esc did not return home:\n%s", view(m))
	}
}

// The header lights the parent's tab while one of its screens is open, and
// clicking that tab goes back to the parent's menu.
func TestParentTabStaysLitInsideAChild(t *testing.T) {
	m := walkTo(t, homeAt100x30(t), home.SectionPortsNet, portsnet.ItemNetwork)
	h := header.New()
	h.Tabs = home.Tabs()
	h.ShowTabs = true
	x := -1
	for col := range 100 {
		if tb, ok := h.TabAt(col); ok && tb.ID == home.SectionPortsNet {
			x = col + 2
			break
		}
	}
	lines := strings.Split(ansi.Strip(view(m)), "\n")
	rule := []rune(lines[header.Rows-1])
	if x < 0 || rule[x] != '━' {
		t.Fatalf("the Ports & Net tab is not underlined: %q", lines[header.Rows-1])
	}
	m = drive(m, tea.MouseClickMsg{X: x, Y: header.TabRow, Button: tea.MouseLeft})
	if !strings.Contains(view(m), sentinels[home.SectionPortsNet]) {
		t.Errorf("clicking the open section's tab from a child did not go to its menu:\n%s", view(m))
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

// The busy rule holds for a screen inside a parent section too: a working
// screen pushed over Ports & Network keeps Tab, every tab click and Esc.
func TestTabsLeaveABusyChildAlone(t *testing.T) {
	m := drive(homeAt100x30(t), digitFor(t, home.SectionPortsNet))
	screen := &busyScreen{name: "killing things", busy: true}
	m = drive(m, uictx.PushScreenMsg{Screen: screen})

	h := header.New()
	h.Tabs = home.Tabs()
	h.ShowTabs = true
	m = drive(m, tab(), shiftTab())
	for col := range 80 {
		if _, ok := h.TabAt(col); ok {
			m = drive(m, tea.MouseClickMsg{X: col, Y: header.TabRow, Button: tea.MouseLeft})
		}
	}
	if !strings.Contains(view(m), "killing things") || screen.stopped != 0 {
		t.Fatalf("a tab tore down a busy child (stopped=%d):\n%s", screen.stopped, view(m))
	}
	m = drive(m, press("esc"))
	if !strings.Contains(view(m), "killing things") {
		t.Fatalf("Esc popped a busy child:\n%s", view(m))
	}
	screen.busy = false
	m = drive(m, press("esc"))
	if !strings.Contains(view(m), sentinels[home.SectionPortsNet]) {
		t.Errorf("Esc on the idle child did not go back to its parent:\n%s", view(m))
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
	m = drive(m, digitFor(t, home.SectionSettings))
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
