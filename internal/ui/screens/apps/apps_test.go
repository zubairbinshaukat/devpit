package apps_test

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/apps"
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

// fake is a stand-in for the install and update screens: no test here may
// construct a screen that could exec a package manager.
type fake struct{ name string }

func (f fake) Init() tea.Cmd                                         { return nil }
func (f fake) Update(tea.Msg, uictx.Context) (uictx.Screen, tea.Cmd) { return f, nil }
func (f fake) View(uictx.Context) string                             { return f.name }
func (f fake) Title() string                                         { return f.name }
func (f fake) ShortHelp() []key.Binding                              { return nil }
func (f fake) FullHelp() [][]key.Binding                             { return nil }

// withFakes is the menu wired to the two fakes.
func withFakes() apps.Model {
	return apps.New().WithFactories(map[string]func() uictx.Screen{
		apps.ItemInstall: func() uictx.Screen { return fake{"install"} },
		apps.ItemUpdate:  func() uictx.Screen { return fake{"update"} },
	})
}

// selectAndPush feeds a key or click, then the SelectedMsg it produces, and
// returns the updated menu and the screen pushed, or nil.
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

// name is the fake's name, or "" for anything else.
func name(s uictx.Screen) string {
	if f, ok := s.(fake); ok {
		return f.name
	}
	return ""
}

func TestEnterOpensEachEntry(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := withFakes()

	if _, s := selectAndPush(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}, ctx); name(s) != "install" {
		t.Errorf("Enter on the first entry pushed %#v, want install", s)
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown}, ctx)
	if _, s := selectAndPush(t, next, tea.KeyPressMsg{Code: tea.KeyEnter}, ctx); name(s) != "update" {
		t.Errorf("Enter on the second entry pushed %#v, want update", s)
	}
}

func TestDigitsOpenEntries(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := withFakes()
	if _, s := selectAndPush(t, m, tea.KeyPressMsg{Code: '1', Text: "1"}, ctx); name(s) != "install" {
		t.Errorf("1 pushed %#v, want install", s)
	}
	if _, s := selectAndPush(t, m, tea.KeyPressMsg{Code: '2', Text: "2"}, ctx); name(s) != "update" {
		t.Errorf("2 pushed %#v, want update", s)
	}
	for _, d := range []rune{'0', '3', '9'} {
		if _, s := selectAndPush(t, m, tea.KeyPressMsg{Code: d, Text: string(d)}, ctx); s != nil {
			t.Errorf("%c pushed %#v, want nothing", d, s)
		}
	}
}

// rowOf is the terminal row the line holding text is drawn on.
func rowOf(t *testing.T, m apps.Model, ctx uictx.Context, text string) int {
	t.Helper()
	for i, l := range strings.Split(ansi.Strip(m.View(ctx)), "\n") {
		if strings.Contains(l, text) {
			return ctx.BodyTop + i
		}
	}
	t.Fatalf("no %q row", text)
	return -1
}

// A click on an entry opens it at both sizes, and a click on the lead-in
// opens nothing.
func TestClickOpensAnEntry(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {80, 24}} {
		ctx := ctxAt(size[0], size[1], icons.Unicode())
		m := withFakes()
		y := rowOf(t, m, ctx, "Update everything")
		if _, s := selectAndPush(t, m, tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft}, ctx); name(s) != "update" {
			t.Errorf("%v: clicking Update everything pushed %#v", size, s)
		}
		if _, s := selectAndPush(t, m, tea.MouseClickMsg{X: 10, Y: ctx.BodyTop, Button: tea.MouseLeft}, ctx); s != nil {
			t.Errorf("%v: a click on the lead-in pushed %#v", size, s)
		}
	}
}

// When an entry has nothing to open, the menu stays put and the other entry
// still works: the parent never depends on its children being usable.
func TestAnUnavailableEntryLeavesTheMenuStanding(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := apps.New().WithFactories(map[string]func() uictx.Screen{
		apps.ItemInstall: func() uictx.Screen { return nil },
		apps.ItemUpdate:  func() uictx.Screen { return fake{"update"} },
	})
	next, s := selectAndPush(t, m, tea.KeyPressMsg{Code: '1', Text: "1"}, ctx)
	if s != nil {
		t.Errorf("an entry with nothing behind it pushed %#v", s)
	}
	if _, ok := next.(apps.Model); !ok {
		t.Fatalf("the menu became %T", next)
	}
	if _, s := selectAndPush(t, next, tea.KeyPressMsg{Code: '2', Text: "2"}, ctx); name(s) != "update" {
		t.Errorf("the other entry stopped working: %#v", s)
	}
}

func TestBreadcrumbNamesTheOpenEntry(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	next, _ := selectAndPush(t, withFakes(), tea.KeyPressMsg{Code: '2', Text: "2"}, ctx)
	m, ok := next.(apps.Model)
	if !ok {
		t.Fatalf("the menu became %T", next)
	}
	if got := m.Breadcrumb("Update Everything", true); got != "Install & Update › Update everything" {
		t.Errorf("direct crumb = %q", got)
	}
	// While a run is going the update screen names the app in flight; the
	// breadcrumb keeps it.
	if got := m.Breadcrumb("Update Everything › scoop › git", true); got != "Install & Update › Update everything › scoop › git" {
		t.Errorf("running crumb = %q", got)
	}
	if got := m.Breadcrumb("Details", false); got != "Install & Update › Details" {
		t.Errorf("deeper crumb = %q", got)
	}
	// Before anything is opened the crumb is the section and the title.
	if got := apps.New().Breadcrumb("X", true); got != "Install & Update › X" {
		t.Errorf("crumb with nothing opened = %q", got)
	}
}

// The menu fits both supported sizes in both fallback tiers.
func TestFitsAtBothSizes(t *testing.T) {
	for _, set := range []icons.Set{icons.Unicode(), icons.ASCII()} {
		for _, size := range [][2]int{{100, 30}, {80, 24}} {
			ctx := ctxAt(size[0], size[1], set)
			out := ansi.Strip(apps.New().View(ctx))
			for _, l := range strings.Split(out, "\n") {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("%s %v: line is %d wide: %q", set.Tier, size, w, l)
				}
			}
			for _, want := range []string{"Install developer apps", "Update everything", "Install dev apps, update everything."} {
				if !strings.Contains(out, want) {
					t.Errorf("%s %v: missing %q:\n%s", set.Tier, size, want, out)
				}
			}
		}
	}
}
