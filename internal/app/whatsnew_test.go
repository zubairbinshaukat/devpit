package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/about"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/app"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
)

const cardSentinel = "What's new in Devpit 0.4"

// cardOptions builds an app at the given running version over cfg, with the
// update check off and the skill answered by f.
func cardOptions(cfg config.Config, version string, f fakeSkill, spy *saveSpy) app.Options {
	cfg.SkipUpdateCheck = true
	o := testOptions(cfg, spy)
	o.Version = version
	o.Skill = f.opens()
	o.ScreenFactory[home.SectionSettings] = settingsWith(f)
	return o
}

// started is the app sized and with its Init commands run, the way the
// program starts it.
func started(o app.Options) tea.Model {
	m := tea.Model(app.New(o))
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	return drive(m, runCmd(m.Init())...)
}

// updated is a config from before last_seen_version existed: first run done,
// nothing recorded.
func updated() config.Config {
	cfg := config.Default()
	cfg.FirstRunDone = true
	return cfg
}

// The card shows once after an update; Enter records the version and goes
// home, and the next start with the saved config goes straight home.
func TestWhatsNewShowsOnceAfterAnUpdate(t *testing.T) {
	for _, k := range []string{"enter", "esc"} {
		t.Run(k, func(t *testing.T) {
			spy := &saveSpy{}
			m := started(cardOptions(updated(), "0.4.0", fakeSkill{}, spy))
			if !strings.Contains(view(m), cardSentinel) {
				t.Fatalf("no card after an update:\n%s", view(m))
			}
			if strings.Contains(ansi.Strip(view(m)), "Settings     tab") {
				t.Error("the card shows the tab bar")
			}
			m = drive(m, press(k))
			if !strings.Contains(view(m), about.Byline) {
				t.Fatalf("%s did not go home:\n%s", k, view(m))
			}
			saved, ok := spy.last()
			if !ok || saved.LastSeenVersion != "0.4.0" {
				t.Fatalf("%s recorded %q (saved %v)", k, saved.LastSeenVersion, ok)
			}
			again := started(cardOptions(saved, "0.4.0", fakeSkill{}, &saveSpy{}))
			if strings.Contains(view(again), cardSentinel) {
				t.Error("the card showed a second time for the same version")
			}
			later := started(cardOptions(func() config.Config { c := saved; c.LastSeenVersion = "0.3.2"; return c }(), "0.4.0", fakeSkill{}, &saveSpy{}))
			if !strings.Contains(view(later), cardSentinel) {
				t.Error("an older recorded version did not show the card")
			}
		})
	}
}

// Never on a fresh install: first run covers it, and finishing first run
// records the version so the card never follows it.
func TestWhatsNewNeverAfterFirstRun(t *testing.T) {
	spy := &saveSpy{}
	m := started(cardOptions(config.Default(), "0.4.0", fakeSkill{}, spy))
	if strings.Contains(view(m), cardSentinel) || !strings.Contains(view(m), "Welcome to Devpit") {
		t.Fatalf("a fresh install shows:\n%s", view(m))
	}
	for range 4 {
		m = drive(m, press("enter"))
	}
	saved, ok := spy.last()
	if !ok || !saved.FirstRunDone || saved.LastSeenVersion != "0.4.0" {
		t.Fatalf("first run saved %+v", saved)
	}
	if strings.Contains(view(m), cardSentinel) {
		t.Error("the card followed first run")
	}
	if strings.Contains(view(started(cardOptions(saved, "0.4.0", fakeSkill{}, &saveSpy{}))), cardSentinel) {
		t.Error("the card showed on the start after first run")
	}
}

// Never for a dev build, a version that is not semver, a downgrade or a
// patch with no card of its own; and nothing is written then.
func TestWhatsNewNotShown(t *testing.T) {
	cases := []struct{ name, running, last string }{
		{"dev build", "dev", ""},
		{"empty", "", ""},
		{"not semver", "0.4", ""},
		{"downgrade", "0.3.2", "0.4.0"},
		{"patch only", "0.4.1", "0.4.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := updated()
			cfg.LastSeenVersion = c.last
			spy := &saveSpy{}
			m := started(cardOptions(cfg, c.running, fakeSkill{}, spy))
			if strings.Contains(view(m), cardSentinel) || !strings.Contains(view(m), about.Byline) {
				t.Fatalf("shows:\n%s", view(m))
			}
			if saved, ok := spy.last(); ok {
				t.Errorf("startup wrote the config: %q", saved.LastSeenVersion)
			}
		})
	}
}

// Tab does not leave the card for a section: the card is dismissed only by
// Enter or Esc, which record the version.
func TestWhatsNewBlocksTabs(t *testing.T) {
	spy := &saveSpy{}
	m := started(cardOptions(updated(), "0.4.0", fakeSkill{}, spy))
	m = drive(m, tea.KeyPressMsg{Code: tea.KeyTab}, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if !strings.Contains(view(m), cardSentinel) {
		t.Fatalf("Tab left the card:\n%s", view(m))
	}
	if _, ok := spy.last(); ok {
		t.Error("Tab recorded the version")
	}
}

// With Claude Code here and the skill missing or older, the card offers the
// skill screen; "a" records the version and opens Settings › AI agent skill,
// installing nothing. Installed, or no Claude Code: no offer.
func TestWhatsNewOffersTheSkill(t *testing.T) {
	cases := []struct {
		name  string
		f     fakeSkill
		offer bool
	}{
		{"missing", skillAs(service.AgentCreate, service.AgentCreate), true},
		{"older", skillAs(service.AgentUpdate, service.AgentCurrent), true},
		{"installed", skillAs(service.AgentCurrent, service.AgentCurrent), false},
		{"no Claude Code", fakeSkill{}, false},
		{"in the way", skillAs(service.AgentForeign, service.AgentForeign), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spy := &saveSpy{}
			m := started(cardOptions(updated(), "0.4.0", c.f, spy))
			has := strings.Contains(view(m), "open the AI agent skill screen")
			if has != c.offer {
				t.Fatalf("offer shown %v, want %v:\n%s", has, c.offer, view(m))
			}
			if !c.offer {
				return
			}
			m = drive(m, press("a"))
			out := ansi.Strip(view(m))
			if !strings.Contains(out, "AI agent skill for Claude Code") || !strings.Contains(out, "Settings") {
				t.Fatalf("a did not open the skill screen in Settings:\n%s", out)
			}
			if saved, ok := spy.last(); !ok || saved.LastSeenVersion != "0.4.0" {
				t.Errorf("a recorded %q", saved.LastSeenVersion)
			}
			// Esc goes back to Settings, then home.
			m = drive(m, press("esc"))
			if !strings.Contains(view(m), "Changes save as soon as you make them") {
				t.Fatalf("Esc from the skill screen:\n%s", view(m))
			}
			m = drive(m, press("esc"))
			if !strings.Contains(view(m), about.Byline) {
				t.Errorf("Esc from Settings did not go home:\n%s", view(m))
			}
		})
	}
}

// A config written by a newer Devpit is still refused: defaults for this
// run, which means first run, and never the card.
func TestWhatsNewNotForANewerConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.EnvConfigDir, dir)
	t.Setenv(config.EnvCacheDir, t.TempDir())
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte("version = 99\nfirst_run_done = true\nlast_seen_version = '0.3.0'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := config.Load()
	if err != nil || res.Warning == "" {
		t.Fatalf("a newer config was not refused: %v %q", err, res.Warning)
	}
	o := cardOptions(res.Config, "0.4.0", fakeSkill{}, &saveSpy{})
	o.Warning = res.Warning
	m := started(o)
	if strings.Contains(view(m), cardSentinel) {
		t.Errorf("the card showed over a refused config:\n%s", view(m))
	}
}
