package app_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/about"
	"github.com/zubairbinshaukat/devpit/internal/app"
	cleanengine "github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	accountsui "github.com/zubairbinshaukat/devpit/internal/ui/screens/accounts"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/accounts/demo"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/apps"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/install"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/portsnet"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/share"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/update"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// saveSpy captures configuration saves so no test writes to a real machine.
type saveSpy struct {
	mu    sync.Mutex
	saved []config.Config
}

func (s *saveSpy) save(c config.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved = append(s.saved, c)
	return nil
}

func (s *saveSpy) last() (config.Config, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.saved) == 0 {
		return config.Config{}, false
	}
	return s.saved[len(s.saved)-1], true
}

// drive feeds messages to a model, running every command the model returns so
// navigation and save messages actually land, the way the real program does.
func drive(m tea.Model, msgs ...tea.Msg) tea.Model {
	queue := append([]tea.Msg(nil), msgs...)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]

		next, cmd := m.Update(msg)
		m = next
		queue = append(queue, runCmd(cmd)...)
	}
	return m
}

// runCmd executes a command and flattens any batch it produces.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch msg := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, runCmd(c)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: 13}
	case "down":
		return tea.KeyPressMsg{Code: 'j', Text: "j"}
	case "esc":
		return tea.KeyPressMsg{Code: 27}
	default:
		return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	}
}

func view(m tea.Model) string { return m.View().Content }

// indexOf is where the entry with the given id sits in items. Tests reach a
// section by what it is, never by where it happens to be, so a reorder of the
// menu cannot quietly point a test at the wrong screen.
func indexOf(t *testing.T, items []menu.Item, id string) int {
	t.Helper()
	for i, it := range items {
		if it.ID == id {
			return i
		}
	}
	t.Fatalf("no entry %q on the menu", id)
	return -1
}

// digitFor is the key that opens a section from home.
func digitFor(t *testing.T, id string) tea.KeyPressMsg {
	t.Helper()
	return press(strconv.Itoa(indexOf(t, home.Items(icons.Unicode()), id) + 1))
}

// childItems is the menu of a parent section.
func childItems(t *testing.T, section string) []menu.Item {
	t.Helper()
	switch section {
	case home.SectionPortsNet:
		return portsnet.Items()
	case home.SectionApps:
		return apps.Items()
	}
	t.Fatalf("%q is not a parent section", section)
	return nil
}

// walkTo opens a section from home the way a user does, cursor down and
// Enter, and then, when child is set, the entry inside the parent's menu.
func walkTo(t *testing.T, m tea.Model, section, child string) tea.Model {
	t.Helper()
	for range indexOf(t, home.Items(icons.Unicode()), section) {
		m = drive(m, press("down"))
	}
	m = drive(m, press("enter"))
	if child == "" {
		return m
	}
	for range indexOf(t, childItems(t, section), child) {
		m = drive(m, press("down"))
	}
	return drive(m, press("enter"))
}

func testOptions(cfg config.Config, spy *saveSpy) app.Options {
	return app.Options{
		Config:        cfg,
		Env:           icons.MapEnv(map[string]string{"WT_SESSION": "test"}),
		SaveConfig:    spy.save,
		Sweep:         noSweep,
		ScreenFactory: fakeScreens(),
		ToolVersions:  noToolVersions,
	}
}

// noSweep is the startup sweep every test runs instead of the real one, so
// nothing in the suite walks the developer's projects folder.
func noSweep(context.Context, []string, cleanengine.Options, func(cleanengine.Progress)) cleanengine.Report {
	return cleanengine.Report{}
}

// fakeScreens replaces the two screens whose Init runs real detection with
// ones wired to fakes: no test may exec a package manager or read PATH.
func fakeScreens() map[string]func() uictx.Screen {
	detected := []tools.Tool{{Name: "scoop", Found: true}}
	return map[string]func() uictx.Screen{
		home.SectionInstall: func() uictx.Screen {
			return install.New(
				install.WithDetectFunc(func(context.Context) []tools.Tool { return detected }),
				install.WithLookPathFunc(func(string) (string, error) { return "", errors.New("not installed") }),
			)
		},
		home.SectionShare:    func() uictx.Screen { return share.NewWith(share.Deps{}) },
		home.SectionAccounts: func() uictx.Screen { return accountsui.NewWith(accountsOptions(demo.New())) },
		home.SectionUpdate: func() uictx.Screen {
			return update.New(
				update.WithDetectFunc(func(context.Context) []tools.Tool { return detected }),
				update.WithRunStepFunc(fakeScoop),
			)
		},
	}
}

// dirInfo is a folder that exists, for the Accounts screen's Stat.
type dirInfo struct{ os.FileInfo }

func (dirInfo) IsDir() bool { return true }

// accountsOptions run the Accounts screens over the in-memory demo engine:
// no tool runs, nothing is handed to the terminal, and no real account,
// ~/.claude or ~/.gitconfig is read.
func accountsOptions(svc *demo.Service) accountsui.Options {
	return accountsui.Options{
		Folder: demo.Folder,
		Open:   func() (accountsui.Service, error) { return svc, nil },
		Exec:   func(tea.ExecCommand, tea.ExecCallback) tea.Cmd { return nil },
		Copy:   func(string) tea.Cmd { return nil },
		Tick:   func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil },
		Stat:   func(string) (os.FileInfo, error) { return dirInfo{}, nil },
		Getwd:  func() (string, error) { return demo.Folder, nil },
	}
}

// fakeScoop answers the update screen's scoop checks with a fixed status
// table, so its golden frame shows a real pick list without running Scoop.
func fakeScoop(_ context.Context, argv []string, _ time.Duration, onLine func(tools.Line)) tools.StepResult {
	if strings.Join(argv, " ") == "scoop status" {
		for _, l := range []string{
			"Name       Installed Version Latest Version Missing Dependencies Info",
			"----       ----------------- -------------- -------------------- ----",
			"git        2.46.0            2.47.1",
			"nodejs-lts 22.9.0            22.11.0",
			"pnpm       9.11.0            9.14.2",
			"nvm        1.1.12            1.2.2                               Held package",
		} {
			onLine(tools.Line{Text: l})
		}
	}
	return tools.StepResult{OK: true}
}

// busyScreen is a screen that is working until it is asked to stop. It is a
// pointer type so a test can see that Stop was called on the very value the
// router holds.
type busyScreen struct {
	name    string
	busy    bool
	stopped int
	keys    []string
}

func (b *busyScreen) Init() tea.Cmd { return nil }

func (b *busyScreen) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		b.keys = append(b.keys, km.String())
	}
	return b, nil
}

func (b *busyScreen) View(uictx.Context) string { return b.name }
func (b *busyScreen) Title() string             { return b.name }
func (b *busyScreen) ShortHelp() []key.Binding  { return nil }
func (b *busyScreen) FullHelp() [][]key.Binding { return nil }
func (b *busyScreen) Busy() bool                { return b.busy }
func (b *busyScreen) Stop()                     { b.stopped++; b.busy = false }

// withBusyScreen builds an app past first run, sized, with a busy screen
// pushed on top of home.
func withBusyScreen(t *testing.T) (tea.Model, *busyScreen) {
	t.Helper()
	cfg := config.Default()
	cfg.FirstRunDone = true

	m := tea.Model(app.New(testOptions(cfg, &saveSpy{})))
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	screen := &busyScreen{name: "deleting things", busy: true}
	m = drive(m, uictx.PushScreenMsg{Screen: screen})
	if !strings.Contains(view(m), "deleting things") {
		t.Fatalf("the busy screen was not pushed:\n%s", view(m))
	}
	return m, screen
}

// TestFirstRunSavesAndHandsOverToHome walks the first-run screen the way a
// user would and checks both halves of the handover: the answers are
// persisted, and the main menu replaces the first-run screen rather than
// stacking on top of it.
func TestFirstRunSavesAndHandsOverToHome(t *testing.T) {
	spy := &saveSpy{}
	cfg := config.Default()
	cfg.FirstRunDone = false

	m := tea.Model(app.New(testOptions(cfg, spy)))
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	if !strings.Contains(view(m), "Welcome to Devpit") {
		t.Fatal("a fresh install should open on the first-run screen")
	}

	// Welcome, then yes to the glyph probe, keep the theme, keep stats off.
	m = drive(m, press("enter"), press("y"), press("enter"), press("enter"), press("enter"))

	saved, ok := spy.last()
	if !ok {
		t.Fatal("finishing first run did not save anything")
	}
	if !saved.FirstRunDone {
		t.Error("the saved config should have first_run_done set")
	}
	if !saved.GlyphsConfirmed || saved.Icons != config.IconsAuto {
		t.Errorf("answering yes to the probe should confirm glyphs under auto, got confirmed=%v icons=%q", saved.GlyphsConfirmed, saved.Icons)
	}
	if saved.TelemetryOptIn {
		t.Error("usage stats must stay off when the user never turned them on")
	}

	out := view(m)
	if strings.Contains(out, "Welcome to Devpit") {
		t.Error("the first-run screen is still showing after it finished")
	}
	if !strings.Contains(out, "Free Up Disk Space") {
		t.Error("the main menu did not take over after first run")
	}

	// Esc must not pop back to first run: home is the root of the stack now.
	m = drive(m, press("esc"))
	if !strings.Contains(view(m), "Free Up Disk Space") {
		t.Error("Esc on the main menu should stay on the main menu")
	}
}

// TestTelemetryStaysOffUnlessAsked pins the privacy default through the whole
// first-run flow, not just in config.Default.
func TestTelemetryStaysOffUnlessAsked(t *testing.T) {
	spy := &saveSpy{}
	cfg := config.Default()
	cfg.FirstRunDone = false
	cfg.TelemetryOptIn = true // even a config that says yes must be re-asked

	m := tea.Model(app.New(testOptions(cfg, spy)))
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	drive(m, press("enter"), press("enter"), press("enter"), press("enter"))

	saved, ok := spy.last()
	if !ok {
		t.Fatal("finishing first run did not save anything")
	}
	if saved.TelemetryOptIn {
		t.Error("first run must not carry a previous opt-in through unasked")
	}
}

// TestSettingsTogglePersists checks the whole settings path: a key press in
// the screen, the config-changed message, the rebuild, and the save.
func TestSettingsTogglePersists(t *testing.T) {
	spy := &saveSpy{}
	cfg := config.Default()
	cfg.FirstRunDone = true
	cfg.Icons = config.IconsUnicode

	m := tea.Model(app.New(testOptions(cfg, spy)))
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	// Walk down to Settings and open it.
	m = walkTo(t, m, home.SectionSettings, "")
	if !strings.Contains(view(m), "Changes save as soon as you make them") {
		t.Fatalf("the settings screen did not open:\n%s", view(m))
	}

	// The first row is the icon tier. Cycling it moves unicode to ascii.
	m = drive(m, press("enter"))

	saved, ok := spy.last()
	if !ok {
		t.Fatal("toggling a setting did not save anything")
	}
	if saved.Icons != config.IconsASCII {
		t.Errorf("Icons = %q after one cycle from unicode, want ascii", saved.Icons)
	}
	if !strings.Contains(view(m), "Icons: ascii") {
		t.Error("the settings screen did not show the new value")
	}

	// Esc returns to the main menu.
	m = drive(m, press("esc"))
	if !strings.Contains(view(m), "Free Up Disk Space") {
		t.Error("Esc from settings did not return to the main menu")
	}
}

// TestEverySectionOpensItsRealScreen walks the whole main menu: each section
// opens the screen it owns, each entry of a parent section opens its screen,
// and Esc comes back one level at a time. The header names where the user
// is, breadcrumb and tab alike.
func TestEverySectionOpensItsRealScreen(t *testing.T) {
	sections := []struct {
		section  string
		child    string
		sentinel string
		crumb    string
		tab      string
	}{
		{home.SectionAccounts, "", "set by this project's .env.local", "Accounts", "Accounts"},
		{home.SectionClean, "", "Pick what to look through", "Free Up Disk Space", "Clean"},
		{home.SectionPortsNet, portsnet.ItemPorts, "Free a busy port or stop a stuck process", "Ports & Network › Fix stuck ports", "Ports & Net"},
		{home.SectionPortsNet, portsnet.ItemNetwork, "IP, connectivity and DNS helpers", "Ports & Network › Network tools", "Ports & Net"},
		{home.SectionApps, apps.ItemInstall, "Manager: scoop", "Install & Update › Install developer apps", "Apps"},
		{home.SectionApps, apps.ItemUpdate, "updates available", "Install & Update › Update everything", "Apps"},
		{home.SectionShare, "", "Move big folders between two PCs on the same Wi-Fi", "Share Files", "Share"},
		{home.SectionSettings, "", "Changes save as soon as you make them", "Settings", "Settings"},
	}

	for _, sec := range sections {
		t.Run(sec.section+"/"+sec.child, func(t *testing.T) {
			cfg := config.Default()
			cfg.FirstRunDone = true

			m := tea.Model(app.New(testOptions(cfg, &saveSpy{})))
			m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})
			m = walkTo(t, m, sec.section, sec.child)

			out := view(m)
			if !strings.Contains(out, sec.sentinel) {
				t.Fatalf("%s did not open its screen:\n%s", sec.crumb, out)
			}
			lines := strings.Split(ansi.Strip(out), "\n")
			if !strings.Contains(lines[0], sec.crumb) {
				t.Errorf("breadcrumb = %q, want %q", lines[0], sec.crumb)
			}
			if !strings.Contains(lines[header.TabRow], sec.tab) {
				t.Errorf("tab row = %q, want %q on it", lines[header.TabRow], sec.tab)
			}

			if sec.child != "" {
				// One Esc goes back to the parent's menu, not to home.
				m = drive(m, press("esc"))
				parent := childItems(t, sec.section)
				if out := view(m); !strings.Contains(out, parent[0].Title) || strings.Contains(out, about.Byline) {
					t.Fatalf("Esc from %s did not land on its parent menu:\n%s", sec.crumb, out)
				}
			}
			m = drive(m, press("esc"))
			if !strings.Contains(view(m), about.Byline) {
				t.Errorf("Esc from %s did not return to the main menu", sec.crumb)
			}
		})
	}
}

// TestParentOpensWhenItsScreensHaveNothingToWorkWith keeps Install & Update
// usable on a machine with no package manager: the menu still opens, both
// entries still open, and each screen says why it cannot help.
func TestParentOpensWhenItsScreensHaveNothingToWorkWith(t *testing.T) {
	none := func(context.Context) []tools.Tool { return nil }
	cfg := config.Default()
	cfg.FirstRunDone = true
	opts := testOptions(cfg, &saveSpy{})
	opts.ScreenFactory = map[string]func() uictx.Screen{
		home.SectionInstall: func() uictx.Screen {
			return install.New(
				install.WithDetectFunc(none),
				install.WithLookPathFunc(func(string) (string, error) { return "", errors.New("not installed") }),
			)
		},
		home.SectionUpdate: func() uictx.Screen { return update.New(update.WithDetectFunc(none)) },
	}

	for _, child := range []string{apps.ItemInstall, apps.ItemUpdate} {
		m := tea.Model(app.New(opts))
		m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30}, digitFor(t, home.SectionApps))
		if !strings.Contains(view(m), "Install dev apps, update everything.") {
			t.Fatalf("Install & Update did not open:\n%s", view(m))
		}
		m = drive(m, press(strconv.Itoa(indexOf(t, apps.Items(), child)+1)))
		if out := view(m); !strings.Contains(out, "No package manager") {
			t.Errorf("%s does not say why it cannot help:\n%s", child, out)
		}
	}
}

// TestEscIsForwardedToABusyScreen holds down the rule that makes stopping a
// delete safe: while a screen is working, Esc means "stop after the item in
// flight", so it has to reach the screen rather than pop it.
func TestEscIsForwardedToABusyScreen(t *testing.T) {
	m, screen := withBusyScreen(t)

	m = drive(m, press("esc"))

	if len(screen.keys) != 1 || screen.keys[0] != "esc" {
		t.Errorf("the busy screen saw %v, want one esc", screen.keys)
	}
	if !strings.Contains(view(m), "deleting things") {
		t.Error("Esc popped a busy screen instead of reaching it")
	}

	// Once the screen is idle again, Esc goes back to meaning "back".
	screen.busy = false
	m = drive(m, press("esc"))
	if !strings.Contains(view(m), about.Byline) {
		t.Error("Esc on an idle screen should pop back to the main menu")
	}
}

// TestCtrlCWaitsForABusyScreen proves Ctrl+C mid-delete does not abandon the
// item in flight: the screen is asked to stop, the footer says so, and the
// program leaves only once the screen reports it is done.
func TestCtrlCWaitsForABusyScreen(t *testing.T) {
	m, screen := withBusyScreen(t)

	next, cmd := m.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'c'})
	if screen.stopped != 1 {
		t.Fatalf("Stop was called %d times, want 1", screen.stopped)
	}
	if !strings.Contains(view(next), "finishing the item in flight") {
		t.Error("the footer should say why Devpit has not quit yet")
	}
	if cmd == nil {
		t.Fatal("Ctrl+C over a busy screen produced no poll command")
	}

	// The command is a poll, not a quit. Stop already flipped the screen to
	// idle, so feeding that poll back is what finally leaves.
	polled := runCmd(cmd)
	if isQuit(polled) {
		t.Fatal("Ctrl+C quit immediately instead of waiting for the screen")
	}
	if len(polled) != 1 {
		t.Fatalf("the poll produced %d messages, want 1", len(polled))
	}
	_, cmd = next.Update(polled[0])
	if !isQuit(runCmd(cmd)) {
		t.Error("Devpit did not quit once the screen reported it was done")
	}
}

// TestCtrlCQuitsImmediatelyWhenNothingIsBusy keeps the ordinary case sharp.
func TestCtrlCQuitsImmediatelyWhenNothingIsBusy(t *testing.T) {
	cfg := config.Default()
	cfg.FirstRunDone = true

	m := tea.Model(app.New(testOptions(cfg, &saveSpy{})))
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	_, cmd := m.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'c'})
	if !isQuit(runCmd(cmd)) {
		t.Error("Ctrl+C on the main menu should quit at once")
	}
}

// TestStartupSweepReportsWhatItFinished checks the startup half of the
// tombstone story: a sweep that finished something says so in the footer, and
// one that found nothing stays quiet.
func TestStartupSweepReportsWhatItFinished(t *testing.T) {
	cfg := config.Default()
	cfg.FirstRunDone = true
	cfg.DefaultProjectsFolder = `D:\work`

	var gotRoots []string
	opts := testOptions(cfg, &saveSpy{})
	opts.Sweep = func(_ context.Context, roots []string, o cleanengine.Options, _ func(cleanengine.Progress)) cleanengine.Report {
		gotRoots = roots
		if len(o.NeverTouch) != len(cfg.NeverTouch) {
			t.Errorf("the sweep got %d never-touch entries, want %d", len(o.NeverTouch), len(cfg.NeverTouch))
		}
		return cleanengine.Report{Deleted: []cleanengine.Result{{}, {}}}
	}

	m := tea.Model(app.New(opts))
	msgs := runCmd(m.Init())
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = drive(m, msgs...)

	if len(gotRoots) != 1 || gotRoots[0] != `D:\work` {
		t.Errorf("the sweep looked in %v, want the default projects folder", gotRoots)
	}
	if !strings.Contains(view(m), "Finished 2 interrupted deletes") {
		t.Errorf("the footer does not report the sweep:\n%s", view(m))
	}
}

// TestStartupSweepIsSkippedBeforeFirstRun keeps a fresh install from touching
// a disk before the user has agreed to anything.
func TestStartupSweepIsSkippedBeforeFirstRun(t *testing.T) {
	cfg := config.Default()
	cfg.FirstRunDone = false
	cfg.DefaultProjectsFolder = `D:\work`

	called := false
	opts := testOptions(cfg, &saveSpy{})
	opts.Sweep = func(context.Context, []string, cleanengine.Options, func(cleanengine.Progress)) cleanengine.Report {
		called = true
		return cleanengine.Report{}
	}

	m := tea.Model(app.New(opts))
	runCmd(m.Init())

	if called {
		t.Error("the startup sweep ran before first run was finished")
	}
}

// isQuit reports whether running a command produced Bubble Tea's quit
// message.
func isQuit(msgs []tea.Msg) bool {
	for _, msg := range msgs {
		if _, ok := msg.(tea.QuitMsg); ok {
			return true
		}
	}
	return false
}

// TestStopGraceIsBounded pins the cap from plan.md section 10 so a screen
// that never finishes cannot hold the program open.
func TestStopGraceIsBounded(t *testing.T) {
	if app.StopGrace <= 0 || app.StopGrace > 30*time.Second {
		t.Errorf("StopGrace = %v, want a small positive cap", app.StopGrace)
	}
}

// TestHelpOverlayTogglesAndSwallowsKeys checks that "?" opens the overlay
// anywhere and that no screen acts on keys meant for it.
func TestHelpOverlayTogglesAndSwallowsKeys(t *testing.T) {
	spy := &saveSpy{}
	cfg := config.Default()
	cfg.FirstRunDone = true

	m := tea.Model(app.New(testOptions(cfg, spy)))
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m = drive(m, press("?"))
	if !strings.Contains(view(m), "Keyboard shortcuts") {
		t.Fatal("? did not open the help overlay")
	}

	// Enter must not select a menu item while the overlay is up.
	m = drive(m, press("enter"))
	if !strings.Contains(view(m), "Keyboard shortcuts") {
		t.Error("a key reached the screen behind the help overlay")
	}

	m = drive(m, press("?"))
	if !strings.Contains(view(m), "Free Up Disk Space") {
		t.Error("? did not close the help overlay")
	}
}

// TestResizeNoticeReplacesTheFrame checks the guard against a garbled layout.
func TestResizeNoticeReplacesTheFrame(t *testing.T) {
	spy := &saveSpy{}
	cfg := config.Default()
	cfg.FirstRunDone = true

	m := tea.Model(app.New(testOptions(cfg, spy)))

	m = drive(m, tea.WindowSizeMsg{Width: 60, Height: 20})
	out := view(m)
	if !strings.Contains(out, "bigger window") {
		t.Errorf("a small terminal should get the resize notice:\n%s", out)
	}
	if strings.Contains(out, "Free Up Disk Space") {
		t.Error("the menu should not be drawn in a terminal too small for it")
	}

	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if !strings.Contains(view(m), "Free Up Disk Space") {
		t.Error("the menu should come back once the terminal is big enough")
	}
}

// TestViewIsAltScreenWithATitle pins the two view-level settings the whole
// app depends on.
func TestViewIsAltScreenWithATitle(t *testing.T) {
	spy := &saveSpy{}
	cfg := config.Default()
	cfg.FirstRunDone = true

	m := tea.Model(app.New(testOptions(cfg, spy)))
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	v := m.View()
	if !v.AltScreen {
		t.Error("the view should use the alternate screen buffer")
	}
	if v.WindowTitle != app.WindowTitle {
		t.Errorf("WindowTitle = %q, want %q", v.WindowTitle, app.WindowTitle)
	}
}
