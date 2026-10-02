package whatsnew

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/version"
)

// TestDueDecisionTable is the whole rule for when the card shows, row by row.
func TestDueDecisionTable(t *testing.T) {
	cases := []struct {
		name          string
		running, last string
		firstRunDone  bool
		want          bool
		wantVersion   string
	}{
		{"fresh install: first run covers it", "0.4.0", "", false, false, ""},
		{"fresh install, even with a value", "0.4.0", "0.3.2", false, false, ""},
		{"updated from before the field existed", "0.4.0", "", true, true, "0.4"},
		{"updated from 0.3.2", "0.4.0", "0.3.2", true, true, "0.4"},
		{"updated with a v prefix", "v0.4.1", "v0.3.2", true, true, "0.4"},
		{"a later patch of a newer minor", "0.4.3", "0.3.2", true, true, "0.4"},
		{"same version: seen already", "0.4.0", "0.4.0", true, false, ""},
		{"patch only, no entry of its own", "0.4.1", "0.4.0", true, false, ""},
		{"downgrade", "0.3.2", "0.4.0", true, false, ""},
		{"downgrade within a minor", "0.4.0", "0.4.1", true, false, ""},
		{"dev build", "dev", "0.3.2", true, false, ""},
		{"empty version", "", "", true, false, ""},
		{"not semver", "0.4", "", true, false, ""},
		{"nightly", "nightly-2026-10-02", "", true, false, ""},
		{"unreadable last seen counts as older", "0.4.0", "garbage", true, true, "0.4"},
		{"pre-release of a newer minor", "0.4.0-rc.1", "0.3.2", true, true, "0.4"},
		{"a minor with no entry shows nothing", "9.9.0", "0.4.0", true, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, ok := Due(c.running, c.last, c.firstRunDone)
			if ok != c.want {
				t.Fatalf("Due(%q, %q, %v) = %v, want %v", c.running, c.last, c.firstRunDone, ok, c.want)
			}
			if ok && e.Version != c.wantVersion {
				t.Errorf("entry %q, want %q", e.Version, c.wantVersion)
			}
		})
	}
}

// TestRecordNeverMovesBackwards: dismissing the card or finishing first run
// stores the running release, but a downgrade or a dev build leaves the
// stored value as it is.
func TestRecordNeverMovesBackwards(t *testing.T) {
	cases := []struct{ last, running, want string }{
		{"", "0.4.0", "0.4.0"},
		{"0.3.2", "0.4.0", "0.4.0"},
		{"0.3.2", "v0.4.0", "0.4.0"},
		{"0.4.1", "0.4.0", "0.4.1"},
		{"0.5.0", "0.4.0", "0.5.0"},
		{"0.4.0", "dev", "0.4.0"},
		{"", "dev", ""},
		{"garbage", "0.4.0", "0.4.0"},
	}
	for _, c := range cases {
		if got := Record(c.last, c.running); got != c.want {
			t.Errorf("Record(%q, %q) = %q, want %q", c.last, c.running, got, c.want)
		}
	}
}

// TestEveryReleaseHasACard fails a release build whose minor version has no
// entry in the table. A plain `go test` runs as "dev", so it also checks the
// rule on the versions the table is meant to cover; to check a release by
// hand: go test -ldflags "-X github.com/zubairbinshaukat/devpit/internal/version.Version=0.4.0" ./internal/ui/screens/whatsnew
func TestEveryReleaseHasACard(t *testing.T) {
	if v := version.Short(); IsRelease(v) {
		if _, ok := For(v); !ok {
			t.Fatalf("Devpit %s has no What's new entry: add one for %s to Releases()", v, v)
		}
	}
	if _, ok := For("0.4.0"); !ok {
		t.Error("0.4 has no entry")
	}
	seen := map[string]bool{}
	for _, e := range Releases() {
		if seen[e.Version] {
			t.Errorf("%s is in the table twice", e.Version)
		}
		seen[e.Version] = true
		if !IsRelease(e.Version+".0") && !IsRelease(e.Version) {
			t.Errorf("entry version %q is neither M.m nor M.m.p", e.Version)
		}
		if e.Headline == "" || len(e.Lines) == 0 || len(e.Lines) > 2 {
			t.Errorf("entry %s needs a headline and one or two lines", e.Version)
		}
	}
}

func testCtx(set icons.Set, w, h int) uictx.Context {
	return uictx.Context{
		Theme: theme.For(true), Icons: set, Config: config.Default(),
		Width: w, Height: h, BodyHeight: h - 4, BodyTop: 2,
	}
}

func run(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

// The card says what moved and links the page; Enter and Esc both continue,
// "a" asks for the skill screen only when it was offered.
func TestCardKeys(t *testing.T) {
	e, _ := For("0.4.0")
	ctx := testCtx(icons.Unicode(), 100, 30)
	m := New("0.4.0", e)
	out := ansi.Strip(m.View(ctx))
	for _, want := range []string{"What's new in Devpit 0.4", "Accounts is new", "Accounts › Git", "Ports & Network", "Install & Update", PageURL} {
		if !strings.Contains(out, want) {
			t.Errorf("card lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "AI agent skill") {
		t.Error("the skill offer showed before the look at the skill came back")
	}

	for _, k := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: tea.KeyEscape}} {
		_, cmd := m.Update(k, ctx)
		if d, ok := run(cmd).(DoneMsg); !ok || d.OpenSkill {
			t.Errorf("%s gave %#v, want DoneMsg without the skill", k.String(), run(cmd))
		}
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"}, ctx); cmd != nil {
		t.Error("a without an offer did something")
	}

	next, _ := m.Update(OfferMsg{Show: true}, ctx)
	m = next.(Model)
	if !strings.Contains(ansi.Strip(m.View(ctx)), "open the AI agent skill screen") {
		t.Fatalf("the offer is not on the card:\n%s", ansi.Strip(m.View(ctx)))
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"}, ctx)
	if d, ok := run(cmd).(DoneMsg); !ok || !d.OpenSkill {
		t.Errorf("a gave %#v, want DoneMsg{OpenSkill}", run(cmd))
	}
}

// Reopened from Settings › About, Enter goes back and records nothing.
func TestReopenedCardGoesBack(t *testing.T) {
	e, _ := For("0.4.0")
	ctx := testCtx(icons.Unicode(), 80, 24)
	m := Reopened("0.4.0", e)
	next, _ := m.Update(OfferMsg{Show: true}, ctx)
	if strings.Contains(ansi.Strip(next.View(ctx)), "AI agent skill screen") {
		t.Error("a reopened card offered the skill")
	}
	_, cmd := next.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, ctx)
	if _, ok := run(cmd).(uictx.PopScreenMsg); !ok {
		t.Errorf("Enter on a reopened card gave %#v, want a pop", run(cmd))
	}
}

// Clicking the button continues and clicking the offer opens the skill.
func TestCardClicks(t *testing.T) {
	e, _ := For("0.4.0")
	ctx := testCtx(icons.Unicode(), 100, 30)
	next, _ := New("0.4.0", e).Update(OfferMsg{Show: true}, ctx)
	m := next.(Model)
	lines := strings.Split(ansi.Strip(m.View(ctx)), "\n")
	button, offer := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "Continue") {
			button = i
		}
		if strings.Contains(l, "open the AI agent skill screen") {
			offer = i
		}
	}
	if button < 0 || offer < 0 {
		t.Fatalf("no button or offer:\n%s", strings.Join(lines, "\n"))
	}
	_, cmd := m.Update(tea.MouseClickMsg{X: 10, Y: ctx.BodyTop + button, Button: tea.MouseLeft}, ctx)
	if d, ok := run(cmd).(DoneMsg); !ok || d.OpenSkill {
		t.Errorf("clicking Continue gave %#v", run(cmd))
	}
	_, cmd = m.Update(tea.MouseClickMsg{X: 10, Y: ctx.BodyTop + offer, Button: tea.MouseLeft}, ctx)
	if d, ok := run(cmd).(DoneMsg); !ok || !d.OpenSkill {
		t.Errorf("clicking the offer gave %#v", run(cmd))
	}
}

// No line is wider than the terminal at any width, in every tier.
func TestCardFitsEveryWidth(t *testing.T) {
	e, _ := For("0.4.0")
	for _, set := range []icons.Set{icons.Unicode(), icons.ASCII(), icons.Nerd()} {
		for w := uictx.MinWidth; w <= 200; w += 7 {
			ctx := testCtx(set, w, 30)
			next, _ := New("0.4.0", e).Update(OfferMsg{Show: true, Older: true}, ctx)
			for _, l := range strings.Split(next.View(ctx), "\n") {
				if lw := ansi.StringWidth(l); lw > w {
					t.Fatalf("%s at %d: line of %d cells: %q", set.Tier, w, lw, ansi.Strip(l))
				}
			}
			if set.Tier == icons.TierASCII {
				for _, r := range ansi.Strip(next.View(ctx)) {
					if r > 0x7e {
						t.Fatalf("ascii card carries %q", r)
					}
				}
			}
		}
	}
}
