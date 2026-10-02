package app_test

import (
	"bytes"
	"fmt"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/app"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/home"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/settings"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/whatsnew"
)

// settingsOrder is every row of Settings the cursor stops on, top to bottom,
// by its label. The goldens walk to a row by finding it here, so a reorder
// moves the cursor further or less far but never captures the wrong row.
var settingsOrder = []string{
	"Theme", "Icons", "Emoji", "Icon font", "Icon check",
	"Projects folder", "Never-touch folders", "Recent projects", "Age filter", "Forget last scan",
	"Package manager", "Dev ports",
	"AI agent skill",
	"Usage stats", "Update check",
	"About Devpit", "What's new",
}

// settingsAt opens Settings over the skill f and walks to the row with the
// given label.
func settingsAt(t *testing.T, cfg config.Config, f fakeSkill, label string, w, h int) tea.Model {
	t.Helper()
	// Each frame gets its own copy of the places: installing in one frame
	// must not leave the next one already installed.
	f.targets = slices.Clone(f.targets)
	o := goldenOptions(cfg)
	o.ScreenFactory[home.SectionSettings] = settingsWith(f)
	m := tea.Model(app.New(o))
	m = drive(m, tea.WindowSizeMsg{Width: w, Height: h}, tea.BackgroundColorMsg{Color: nil})
	m = walkTo(t, m, home.SectionSettings, "")
	n := slices.Index(settingsOrder, label)
	if n < 0 {
		t.Fatalf("no Settings row %q", label)
	}
	for range n {
		m = drive(m, press("down"))
	}
	return m
}

// eachFrame runs fn at both supported sizes and both tiers that must survive
// a terminal without a Nerd Font, with NO_COLOR set, and compares each frame
// to its golden.
func eachFrame(t *testing.T, base string, fn func(t *testing.T, cfg config.Config, w, h int) []byte) {
	sizes := []struct {
		name string
		w, h int
	}{{"100x30", 100, 30}, {"80x24", 80, 24}}
	tiers := []struct{ name, tier string }{{"unicode", config.IconsUnicode}, {"ascii", config.IconsASCII}}
	for _, size := range sizes {
		for _, tier := range tiers {
			name := fmt.Sprintf("%s_%s_%s_nocolor", base, size.name, tier.name)
			t.Run(name, func(t *testing.T) {
				t.Setenv("NO_COLOR", "1")
				cfg := settledConfig()
				cfg.Icons = tier.tier
				cfg.DefaultProjectsFolder = `D:\work\projects`
				cfg.NeverTouch = []string{`D:\work\keep`, `D:\photos`}
				requireGolden(t, name, fn(t, cfg, size.w, size.h))
			})
		}
	}
}

// TestSettingsGoldens captures Settings with the cursor on the first row of
// every group, which at 80x24 also scrolls the list down to the last one,
// so the frames double as the design review of the whole list.
func TestSettingsGoldens(t *testing.T) {
	notInstalled := skillAs(service.AgentCreate, service.AgentCreate)
	for _, c := range []struct{ name, row string }{
		{"settings_top", "Theme"},
		{"settings_cleaning", "Projects folder"},
		{"settings_tools", "Package manager"},
		{"settings_agents", "AI agent skill"},
		{"settings_privacy", "Usage stats"},
		{"settings_bottom", "What's new"},
	} {
		eachFrame(t, c.name, func(t *testing.T, cfg config.Config, w, h int) []byte {
			m := settingsAt(t, cfg, notInstalled, c.row, w, h)
			got := []byte(ansi.Strip(m.View().Content))
			if !bytes.Contains(got, []byte(c.row)) || !bytes.Contains(got, []byte("Changes save as soon as you make them")) {
				t.Fatalf("the %s row is not on screen:\n%s", c.row, got)
			}
			return got
		})
	}

	// Without Claude Code the AI agents group is one quiet line the cursor
	// steps over: walking as far as the skill row lands on Usage stats.
	eachFrame(t, "settings_no_claude", func(t *testing.T, cfg config.Config, w, h int) []byte {
		m := settingsAt(t, cfg, fakeSkill{}, "AI agent skill", w, h)
		got := []byte(ansi.Strip(m.View().Content))
		if !bytes.Contains(got, []byte("Claude Code is not installed")) {
			t.Fatalf("no note:\n%s", got)
		}
		return got
	})
}

// TestSkillScreenGoldens captures Settings › AI agent skill in every state
// the skill can be in, the install question, and the done card.
func TestSkillScreenGoldens(t *testing.T) {
	viaLink := skillAs(service.AgentCurrent, service.AgentShared)
	viaLink.targets[0].ThroughLink = true
	viaLink.targets[1].SharedWith = "default"
	inTheWay := skillAs(service.AgentForeign, service.AgentForeign)
	inTheWay.targets[0].Note = "a devpit skill Devpit did not write."
	inTheWay.targets[1].Note = "a devpit folder without Devpit's SKILL.md, holding other files."

	for _, c := range []struct {
		name     string
		f        fakeSkill
		keys     []string
		sentinel string
	}{
		{"skill_not_installed", skillAs(service.AgentCreate, service.AgentCreate), nil, "Not installed."},
		{"skill_installed", skillAs(service.AgentCurrent, service.AgentCurrent), nil, "Installed. Claude Code can use Devpit."},
		{"skill_update", skillAs(service.AgentCurrent, service.AgentUpdate), nil, "Update available"},
		{"skill_in_the_way", inTheWay, nil, "A different skill called devpit"},
		{"skill_shared", viaLink, nil, "Installed through a shared link"},
		{"skill_confirm", skillAs(service.AgentCreate, service.AgentCreate), []string{"enter"}, "Install the Devpit skill for Claude Code?"},
		{"skill_done", skillAs(service.AgentCreate, service.AgentCreate), []string{"enter", "y"}, "Done in 2 places"},
	} {
		eachFrame(t, c.name, func(t *testing.T, cfg config.Config, w, h int) []byte {
			m := settingsAt(t, cfg, c.f, "AI agent skill", w, h)
			m = drive(m, press("enter"))
			for _, k := range c.keys {
				m = drive(m, press(k))
			}
			got := []byte(ansi.Strip(m.View().Content))
			if !bytes.Contains(got, []byte(c.sentinel)) {
				t.Fatalf("the %s state never appeared:\n%s", c.name, got)
			}
			return got
		})
	}
}

// TestWhatsNewGoldens captures the card at startup after an update, with the
// AI agent skill offered.
func TestWhatsNewGoldens(t *testing.T) {
	eachFrame(t, "whatsnew", func(t *testing.T, cfg config.Config, w, h int) []byte {
		cfg.LastSeenVersion = "0.3.2"
		cfg.SkipUpdateCheck = true
		f := skillAs(service.AgentCreate, service.AgentCreate)
		o := goldenOptions(cfg)
		o.Version = "0.4.0"
		o.Skill = f.opens()
		m := tea.Model(app.New(o))
		m = drive(m, tea.WindowSizeMsg{Width: w, Height: h}, tea.BackgroundColorMsg{Color: nil})
		show, older := settings.SkillOffer(f.opens())
		m = drive(m, whatsnew.OfferMsg{Show: show, Older: older})
		got := []byte(ansi.Strip(m.View().Content))
		if !bytes.Contains(got, []byte("What's new in Devpit 0.4")) || !bytes.Contains(got, []byte("AI agent skill")) {
			t.Fatalf("no card with the offer:\n%s", got)
		}
		return got
	})
}

// TestFirstRunPrivacyGolden captures the last first-run step, which mentions
// the AI agent skill and where to add it, and installs nothing.
func TestFirstRunPrivacyGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	cfg := settledConfig()
	cfg.FirstRunDone = false
	m := tea.Model(app.New(goldenOptions(cfg)))
	m = drive(m, tea.WindowSizeMsg{Width: 100, Height: 30}, tea.BackgroundColorMsg{Color: nil})
	m = drive(m, press("enter"), press("enter"), press("enter"))
	got := []byte(ansi.Strip(m.View().Content))
	if !bytes.Contains(got, []byte("AI agent skill")) || !bytes.Contains(got, []byte("Start using Devpit")) {
		t.Fatalf("the privacy step does not mention the skill:\n%s", got)
	}
	requireGolden(t, "firstrun_privacy_100x30_unicode_nocolor", got)
}
