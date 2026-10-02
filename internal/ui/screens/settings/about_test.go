package settings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/about"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/version"
)

func TestAboutNamesTheAuthorAndTheBuild(t *testing.T) {
	out := newAboutScreen(version.Short()).View(testContext(config.Default()))
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
	out := newAboutScreen(version.Short()).View(ctx)
	for _, want := range []string{"Update available: v9.9.9", "scoop update devpit", "https://example.test/rel"} {
		if !strings.Contains(out, want) {
			t.Errorf("about screen lacks %q:\n%s", want, out)
		}
	}

	cfg := config.Default()
	cfg.SkipUpdateCheck = true
	if out := newAboutScreen(version.Short()).View(testContext(cfg)); !strings.Contains(out, "Update checks are off") {
		t.Errorf("about screen should say checks are off:\n%s", out)
	}
}

func TestUpdateCheckRowToggles(t *testing.T) {
	cfg := config.Default()
	if r := rowOf(cfg, rowUpdates); !r.on || r.kind != kindToggle {
		t.Errorf("fresh config row = %+v, want a toggle that is on", r)
	}
	cfg, changed := apply(cfg, rowUpdates, 1)
	if !changed || !cfg.SkipUpdateCheck {
		t.Fatalf("toggle did not turn the check off: changed=%v skip=%v", changed, cfg.SkipUpdateCheck)
	}
	if r := rowOf(cfg, rowUpdates); r.on {
		t.Errorf("after toggle row = %+v, want off", r)
	}
}

func TestEnterOnAboutPushesTheScreen(t *testing.T) {
	cfg := config.Default()
	m := newTest(skillWith(service.AgentCreate, service.AgentCreate))
	m.cursor = rowAbout

	_, cmd := m.Update(pressKey("enter"), testContext(cfg))
	s, ok := findPush(flattenCmd(cmd))
	if !ok {
		t.Fatal("Enter on About pushed nothing")
	}
	if _, ok := s.(aboutScreen); !ok {
		t.Errorf("pushed %T, want aboutScreen", s)
	}
}

// A click on a row's value opens it at once; a click on its label only
// moves there, and a second click opens it.
func TestClickOnAboutPushesTheScreen(t *testing.T) {
	cfg := config.Default()
	ctx := testContext(cfg)
	ctx.BodyTop = 3
	m := newTest(skillWith(service.AgentCreate, service.AgentCreate))
	y := ctx.BodyTop + bodyRowOf(t, m, ctx, rowAbout)

	next, cmd := m.Update(tea.MouseClickMsg{X: 8, Y: y, Button: tea.MouseLeft}, ctx)
	if _, ok := findPush(flattenCmd(cmd)); ok {
		t.Fatal("a first click on the label opened the screen")
	}
	m = next.(Model)
	if m.cursor != rowAbout {
		t.Fatalf("the click left the cursor on %q", m.cursor)
	}
	_, cmd = m.Update(tea.MouseClickMsg{X: 8, Y: y, Button: tea.MouseLeft}, ctx)
	s, ok := findPush(flattenCmd(cmd))
	if !ok {
		t.Fatal("the second click pushed nothing")
	}
	if _, ok := s.(aboutScreen); !ok {
		t.Errorf("pushed %T, want aboutScreen", s)
	}
}

// w on About opens the What's new card for this version.
func TestAboutOpensWhatsNew(t *testing.T) {
	ctx := testContext(config.Default())
	a := newAboutScreen("0.4.0")
	if !strings.Contains(a.View(ctx), "What's new in this version") {
		t.Error("About does not say how to see what is new")
	}
	_, cmd := a.Update(pressKey("w"), ctx)
	s, ok := findPush(flattenCmd(cmd))
	if !ok {
		t.Fatal("w pushed nothing")
	}
	if s.Title() != "What's new" {
		t.Errorf("w pushed %q", s.Title())
	}
}

// rowOf finds a row by id.
func rowOf(cfg config.Config, id string) row {
	for _, g := range groups(cfg, nil, "0.4.0") {
		for _, r := range g.rows {
			if r.id == id {
				return r
			}
		}
	}
	return row{}
}

// bodyRowOf is the body row the row with this id is drawn on.
func bodyRowOf(t *testing.T, m Model, ctx uictx.Context, id string) int {
	t.Helper()
	lo := m.arrange(ctx)
	for y := range ctx.BodyHeight {
		if r, _, ok := lo.rowAt(y, 6); ok && r.id == id {
			return y
		}
	}
	t.Fatalf("row %q is not on screen", id)
	return -1
}
