package app_test

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/zubairbinshaukat/devpit/internal/app"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
)

// updating reports whether -update was passed:
//
//	go test ./internal/app -run Golden -update
//
// The flag itself is registered by the teatest golden helper, so this looks it
// up rather than defining a second one.
func updating() bool {
	f := flag.Lookup("update")
	if f == nil {
		return false
	}
	g, ok := f.Value.(flag.Getter)
	if !ok {
		return false
	}
	b, _ := g.Get().(bool)
	return b
}

// goldenDir is the repo-level testdata/golden directory named in the plan.
const goldenDir = "../../testdata/golden"

// TestHomeScreenGolden runs the real program under teatest at 100x30 with
// NO_COLOR and the unicode icon tier, waits for the menu, quits, and compares
// the rendered frame to a checked-in golden file.
//
// The frame compared is the model's own View content with escape sequences
// stripped, not the raw terminal byte stream: the stream also carries cursor
// moves and the altscreen teardown, which differ between renderer versions and
// would make the golden churn for no reason. The stream is still checked, for
// the one thing it is authoritative about, in TestNoColorProducesNoColour.
func TestHomeScreenGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	m := app.New(goldenOptions(settledConfig()))

	runUntilQuit(t, m, []byte("Devpit Settings"), tea.KeyPressMsg{Code: 'q', Text: "q"})
	requireGolden(t, "home_100x30_unicode_nocolor", frame(m, 100, 30))
}

// TestHomeGoldens captures the home screen at the other sizes and tiers: the
// block wordmark needs a tall terminal and a font with block drawing, and both
// fallbacks — the spaced-out letters on a short terminal, the "#" wordmark on
// a dumb one — have to look deliberate rather than broken.
func TestHomeGoldens(t *testing.T) {
	cases := []struct {
		name string
		tier string
		w, h int
	}{
		{"home_100x30_ascii_nocolor", config.IconsASCII, 100, 30},
		{"home_80x24_unicode_nocolor", config.IconsUnicode, 80, 24},
		{"home_80x24_ascii_nocolor", config.IconsASCII, 80, 24},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "1")

			cfg := settledConfig()
			cfg.Icons = c.tier

			m := tea.Model(app.New(goldenOptions(cfg)))
			requireGolden(t, c.name, frame(m, c.w, c.h))
		})
	}
}

// TestFirstRunScreenGolden does the same for the first-run screen.
func TestFirstRunScreenGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	cfg := settledConfig()
	cfg.FirstRunDone = false

	m := app.New(goldenOptions(cfg))

	runUntilQuit(t, m, []byte("Start using Devpit"), tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'c'})
	requireGolden(t, "firstrun_100x30_unicode_nocolor", frame(m, 100, 30))
}

// TestResizeNoticeGolden proves a too-small terminal gets a readable notice
// instead of a garbled layout.
func TestResizeNoticeGolden(t *testing.T) {
	m := app.New(goldenOptions(settledConfig()))
	requireGolden(t, "resize_60x20", frame(m, 60, 20))
}

// TestNoColorProducesNoColour checks the one thing the raw stream is
// authoritative about: with NO_COLOR set, Bubble Tea must downsample the
// truecolor sequences Lip Gloss emits all the way out of the output.
func TestNoColorProducesNoColour(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	m := app.New(goldenOptions(settledConfig()))

	out := runUntilQuit(t, m, []byte("Devpit Settings"), tea.KeyPressMsg{Code: 'q', Text: "q"})
	for _, seq := range [][]byte{[]byte("\x1b[38;2;"), []byte("\x1b[48;2;"), []byte("\x1b[38;5;")} {
		if bytes.Contains(out, seq) {
			t.Errorf("output still carries the colour sequence %q under NO_COLOR", seq)
		}
	}
}

// runUntilQuit starts the model under teatest, waits for sentinel to appear on
// screen, sends quitKey and returns everything the program wrote.
func runUntilQuit(t *testing.T, m tea.Model, sentinel []byte, quitKey tea.Msg) []byte {
	t.Helper()

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))

	var seen bytes.Buffer
	r := io.TeeReader(tm.Output(), &seen)
	teatest.WaitFor(t, r, func(b []byte) bool {
		return bytes.Contains(b, sentinel)
	}, teatest.WithDuration(10*time.Second))

	tm.Send(quitKey)
	tm.WaitFinished(t, teatest.WithFinalTimeout(10*time.Second))

	rest, err := io.ReadAll(tm.FinalOutput(t))
	if err != nil {
		t.Fatalf("reading the final output: %v", err)
	}
	return append(seen.Bytes(), rest...)
}

// frame renders one deterministic frame of a model at the given size, with
// escape sequences stripped. It feeds the same two messages Bubble Tea sends
// at startup: the window size and the background colour.
func frame(m tea.Model, width, height int) []byte {
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	next, _ = next.Update(tea.BackgroundColorMsg{Color: nil})
	return []byte(ansi.Strip(next.View().Content))
}

// sections is the golden coverage for the main menu: every section, the
// sentinel that proves its first frame is the one that was captured, and the
// name the golden files are keyed by.
//
// The frames are the submenu (or first list) state of each screen. Nothing on
// them runs an engine: the cleaner's submenu does no I/O, ports, network and
// Git & SSH start idle, and install and update reach their lists through the
// fakes in fakeScreens.
var sections = []struct {
	name     string
	index    int
	sentinel string
}{
	{"clean", 0, "Pick what to look through"},
	{"ports", 1, "Free a busy port or stop a stuck process"},
	{"install", 2, "Manager: scoop"},
	{"update", 3, "Space unticks a manager"},
	{"network", 4, "IP, connectivity and DNS helpers"},
	{"gitssh", 5, "Get a fresh machine ready to push code"},
}

// TestScreenGoldens captures the first frame of every screen the main menu
// opens, at both supported sizes and both icon tiers that have to survive a
// terminal without a Nerd Font, with NO_COLOR set.
//
// It navigates the way a user does — cursor down, Enter — rather than
// constructing the screen directly, so a section wired to the wrong screen
// fails here too.
func TestScreenGoldens(t *testing.T) {
	sizes := []struct {
		name string
		w, h int
	}{
		{"100x30", 100, 30},
		{"80x24", 80, 24},
	}
	tiers := []struct {
		name string
		tier string
	}{
		{"unicode", config.IconsUnicode},
		{"ascii", config.IconsASCII},
	}

	for _, sec := range sections {
		for _, size := range sizes {
			for _, tier := range tiers {
				name := fmt.Sprintf("%s_%s_%s_nocolor", sec.name, size.name, tier.name)
				t.Run(name, func(t *testing.T) {
					t.Setenv("NO_COLOR", "1")

					cfg := settledConfig()
					cfg.Icons = tier.tier

					m := openSection(t, cfg, sec.index, size.w, size.h)
					got := []byte(ansi.Strip(m.View().Content))
					if !bytes.Contains(got, []byte(sec.sentinel)) {
						t.Fatalf("the %s screen never appeared:\n%s", sec.name, got)
					}
					requireGolden(t, name, got)
				})
			}
		}
	}
}

// TestSettingsBottomGolden is the regression frame for the scrolling bug: the
// settings list is longer than a 24-row terminal can hold, so walking to the
// last row has to scroll the list rather than draw it off the bottom of the
// screen. The frame proves the cursor, the last row and both "n more" markers
// are all on it.
func TestSettingsBottomGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	m := openSection(t, settledConfig(), 6, 80, 24)
	for range 12 {
		m = drive(m, press("down"))
	}

	got := []byte(ansi.Strip(m.View().Content))
	if !bytes.Contains(got, []byte("Rescan my tools")) {
		t.Fatalf("the last settings row never scrolled into view:\n%s", got)
	}
	requireGolden(t, "settings_bottom_80x24_unicode_nocolor", got)
}

// openSection sizes an app, walks the main menu to index and opens it,
// running every command the model returns along the way.
func openSection(t *testing.T, cfg config.Config, index, width, height int) tea.Model {
	t.Helper()

	m := tea.Model(app.New(goldenOptions(cfg)))
	m = drive(m, tea.WindowSizeMsg{Width: width, Height: height}, tea.BackgroundColorMsg{Color: nil})
	for range index {
		m = drive(m, press("down"))
	}
	return drive(m, press("enter"))
}

// goldenOptions builds an app that cannot touch the machine: no config is
// saved, no startup sweep runs, and the two screens whose Init detects real
// tools are replaced by fakes, so a golden frame is the same everywhere.
func goldenOptions(cfg config.Config) app.Options {
	return app.Options{
		Config:        cfg,
		Env:           icons.MapEnv(map[string]string{"WT_SESSION": "test"}),
		SaveConfig:    func(config.Config) error { return nil },
		Sweep:         noSweep,
		ScreenFactory: fakeScreens(),
		ToolVersions:  noToolVersions,
	}
}

// noToolVersions keeps the header pills pending, so goldens never depend on
// what is installed on the machine running them.
func noToolVersions(context.Context) header.ToolVersionsMsg { return header.ToolVersionsMsg{} }

// settledConfig is a configuration with no machine-specific values in it, so
// the golden frames are identical everywhere: past first run, unicode tier,
// emoji off.
func settledConfig() config.Config {
	cfg := config.Default()
	cfg.FirstRunDone = true
	cfg.Icons = config.IconsUnicode
	cfg.Emoji = false
	return cfg
}

// requireGolden compares got against testdata/golden/<name>.golden, or writes
// the file when -update was passed.
func requireGolden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join(goldenDir, name+".golden")
	got = normalise(got)

	if updating() {
		if err := os.MkdirAll(goldenDir, 0o750); err != nil {
			t.Fatalf("creating %s: %v", goldenDir, err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(got))
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v\nRun `go test ./internal/app -run Golden -update` to create it.", path, err)
	}
	want = normalise(want)

	if !bytes.Equal(got, want) {
		t.Errorf("the rendered frame does not match %s.\n--- want ---\n%s\n--- got ---\n%s",
			path, want, got)
	}
}

// normalise strips carriage returns and trailing blanks so a golden file
// checked out with CRLF endings still matches a frame produced with LF.
func normalise(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	lines := bytes.Split(b, []byte("\n"))
	for i := range lines {
		lines[i] = bytes.TrimRight(lines[i], " \t")
	}
	return bytes.Join(lines, []byte("\n"))
}
