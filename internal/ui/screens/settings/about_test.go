package settings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/about"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/version"
)

func TestAboutNamesTheAuthorAndTheBuild(t *testing.T) {
	out := newAboutScreen().View(testContext(config.Default()))
	for _, want := range []string{
		about.Byline, about.Author, about.Portfolio, about.Website, about.Repo,
		"v" + version.Short(), about.License, "checks once a day",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("about screen lacks %q:\n%s", want, out)
		}
	}
}

func TestAboutSaysHowToUpgrade(t *testing.T) {
	ctx := testContext(config.Default())
	ctx.Update = uictx.UpdateInfo{Version: "9.9.9", URL: "https://example.test/rel", Hint: "scoop update devpit"}
	out := newAboutScreen().View(ctx)
	for _, want := range []string{"Update available: v9.9.9", "scoop update devpit", "https://example.test/rel"} {
		if !strings.Contains(out, want) {
			t.Errorf("about screen lacks %q:\n%s", want, out)
		}
	}

	cfg := config.Default()
	cfg.SkipUpdateCheck = true
	if out := newAboutScreen().View(testContext(cfg)); !strings.Contains(out, "Update checks are off") {
		t.Errorf("about screen should say checks are off:\n%s", out)
	}
}

func TestUpdateCheckRowToggles(t *testing.T) {
	cfg := config.Default()
	if got := titleOf(rows(cfg), rowUpdates); got != "Update check: on" {
		t.Errorf("fresh config row = %q", got)
	}
	cfg, changed := apply(cfg, rowUpdates)
	if !changed || !cfg.SkipUpdateCheck {
		t.Fatalf("toggle did not turn the check off: changed=%v skip=%v", changed, cfg.SkipUpdateCheck)
	}
	if got := titleOf(rows(cfg), rowUpdates); got != "Update check: off" {
		t.Errorf("after toggle row = %q", got)
	}
}

func TestEnterOnAboutPushesTheScreen(t *testing.T) {
	cfg := config.Default()
	m := New()
	m.menu = m.menu.SetItems(rows(cfg)).SetCursor(len(rows(cfg)) - 1)

	_, cmd := m.Update(pressKey("enter"), testContext(cfg))
	s, ok := findPush(flattenCmd(cmd))
	if !ok {
		t.Fatal("Enter on About pushed nothing")
	}
	if _, ok := s.(aboutScreen); !ok {
		t.Errorf("pushed %T, want aboutScreen", s)
	}
}

// With the cursor on the first row only that row shows its description, so
// every later row is one line: the About row, last of fifteen, sits on menu
// row 15, under the two-line lead-in, three rows below the header.
func TestClickOnAboutPushesTheScreen(t *testing.T) {
	cfg := config.Default()
	ctx := testContext(cfg)
	ctx.BodyTop = 3
	m := New()

	_, cmd := m.Update(tea.MouseClickMsg{X: 10, Y: 3 + menuTop + 15, Button: tea.MouseLeft}, ctx)
	s, ok := findPush(flattenCmd(cmd))
	if !ok {
		t.Fatal("the click pushed nothing")
	}
	if _, ok := s.(aboutScreen); !ok {
		t.Errorf("pushed %T, want aboutScreen", s)
	}
}

func titleOf(items []menu.Item, id string) string {
	for _, it := range items {
		if it.ID == id {
			return it.Title
		}
	}
	return ""
}
