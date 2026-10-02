package choices

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

func ctxAt(set icons.Set, w, h int) uictx.Context {
	return uictx.Context{
		Theme: theme.For(true), Icons: set, Config: config.Default(),
		Width: w, Height: h, BodyHeight: h - 6, BodyTop: 4,
	}
}

// spec is a page with facts, three groups, a value row, a disabled row and
// a quiet note.
func spec() Spec {
	return Spec{
		FactsTitle: "Right now", FactsAside: `in D:\work\shop`,
		Facts: []Fact{
			{Label: "Signed in as", Value: "work (you@work.example)", Notes: []string{"chosen for D:\\work"}},
			{Label: "Other folders", Value: "your usual sign-in"},
		},
		Groups: []Group{
			{Title: "Change", Items: []Item{
				{ID: "here", Label: "Use another account here", Desc: "Pick an account for this folder.", Next: "You see a preview first."},
				{ID: "once", Label: "Just once", Desc: "One command.", Disabled: "the tool cannot do this"},
			}},
			{Title: "Look", Items: []Item{
				{ID: "theme", Label: "Theme", Value: "auto", Kind: Cycles, Desc: "Colours."},
				{ID: "note", Label: "A quiet line", Kind: Note},
				{ID: "emoji", Label: "Emoji", On: true, Kind: Toggles, Desc: "Emoji."},
			}},
			{Title: "Manage", Items: []Item{{ID: "manage", Label: "Rename or remove", Desc: "Tidy up."}}},
		},
		Note: "Changes save as soon as you make them.",
	}
}

func press(name string) tea.KeyPressMsg {
	codes := map[string]rune{"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight, "home": tea.KeyHome, "end": tea.KeyEnd, "enter": tea.KeyEnter}
	if c, ok := codes[name]; ok {
		return tea.KeyPressMsg{Code: c}
	}
	return tea.KeyPressMsg{Code: rune(name[0]), Text: name}
}

// The cursor walks rows only: never a heading, a gap or a note. It stops on a
// disabled row, so its reason can be read, but Enter there does nothing.
func TestCursorWalksRowsOnly(t *testing.T) {
	ctx := ctxAt(icons.Unicode(), 100, 30)
	m := New()
	var seen []string
	for range 7 {
		seen = append(seen, m.Layout(ctx, spec()).Cursor)
		m, _ = m.Update(press("down"), ctx, spec())
	}
	want := "here once theme emoji manage manage manage"
	if got := strings.Join(seen, " "); got != want {
		t.Fatalf("walked %q, want %q", got, want)
	}
	m = m.SetCursor("once")
	if _, a := m.Update(press("enter"), ctx, spec()); a.ID != "" {
		t.Error("Enter on a disabled row acted")
	}
	m = m.SetCursor("here")
	if _, a := m.Update(press("enter"), ctx, spec()); a.ID != "here" || a.Dir != 1 || a.Kind != Opens {
		t.Errorf("Enter on a row gave %+v", a)
	}
	if _, a := m.Update(press("right"), ctx, spec()); a.ID != "" {
		t.Error("→ on a row that opens a screen acted")
	}
	m = m.SetCursor("theme")
	if _, a := m.Update(press("left"), ctx, spec()); a.ID != "theme" || a.Dir != -1 {
		t.Errorf("← on a cycling row gave %+v", a)
	}
	m, _ = m.Update(press("home"), ctx, spec())
	m, _ = m.Update(press("end"), ctx, spec())
	if m.Cursor() != "manage" {
		t.Errorf("end went to %q", m.Cursor())
	}
}

// Facts read as information, the choices carry their markers, and the
// focused row's description says what happens next and why it is off.
func TestViewSaysWhatIsWhat(t *testing.T) {
	for _, w := range []int{80, 120} {
		ctx := ctxAt(icons.Unicode(), w, 34)
		m := New()
		out := ansi.Strip(m.View(ctx, spec()))
		for _, want := range []string{
			"Right now", `in D:\work\shop`, "Signed in as", "work (you@work.example)", "chosen for D:\\work",
			"Change", "Use another account here", "›", "‹ auto ›", "● on", "not available",
			"Pick an account for this folder.", "You see a preview first.",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%d columns: lacks %q:\n%s", w, want, out)
			}
		}
		m = m.SetCursor("once")
		if out := ansi.Strip(m.View(ctx, spec())); !strings.Contains(strings.Join(strings.Fields(out), " "), "Not available now: the tool cannot do this") {
			t.Errorf("%d columns: a disabled row does not say why:\n%s", w, out)
		}
	}
}

// A click on a label moves there, a second click acts; the pointer
// highlights; the wheel moves; a click on a fact does nothing.
func TestMouse(t *testing.T) {
	ctx := ctxAt(icons.Unicode(), 100, 30)
	m := New()
	s := spec()
	row := func(id string) int {
		for y := range ctx.BodyHeight {
			if it, ok := m.ItemAt(ctx, s, y, 6); ok && it.ID == id {
				return y
			}
		}
		t.Fatalf("no row %q", id)
		return 0
	}
	y := ctx.BodyTop + row("manage")
	m, a := m.Update(tea.MouseClickMsg{X: 6, Y: y, Button: tea.MouseLeft}, ctx, s)
	if a.ID != "" || m.Cursor() != "manage" {
		t.Fatalf("first click: %+v, cursor %q", a, m.Cursor())
	}
	if _, a = m.Update(tea.MouseClickMsg{X: 6, Y: y, Button: tea.MouseLeft}, ctx, s); a.ID != "manage" {
		t.Errorf("second click gave %+v", a)
	}
	m, _ = m.Update(tea.MouseMotionMsg{X: 6, Y: ctx.BodyTop + row("here")}, ctx, s)
	if m.Cursor() != "here" {
		t.Errorf("hover left the cursor on %q", m.Cursor())
	}
	m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown}, ctx, s)
	if m.Cursor() != "once" {
		t.Errorf("wheel went to %q", m.Cursor())
	}
	if _, a = m.Update(tea.MouseClickMsg{X: 6, Y: ctx.BodyTop + 1, Button: tea.MouseLeft}, ctx, s); a.ID != "" {
		t.Error("a click on a fact acted")
	}
}

// When room is short the gaps go first, then the description shrinks, and
// only then the list scrolls; the focused row is always drawn and no line is
// wider than the terminal, in every tier and at every width.
func TestGivesWayAndFits(t *testing.T) {
	tall := New().Layout(ctxAt(icons.Unicode(), 80, 40), spec())
	if tall.Gaps == 0 || tall.Scrolling {
		t.Errorf("tall: %+v", tall)
	}
	short := New().Layout(ctxAt(icons.Unicode(), 80, 24), spec())
	if short.Gaps != 0 || short.Scrolling {
		t.Errorf("80x24: %+v", short)
	}
	for _, set := range []icons.Set{icons.Unicode(), icons.ASCII(), icons.Nerd()} {
		for w := 60; w <= 200; w += 3 {
			for _, h := range []int{uictx.MinHeight, 30, 40} {
				ctx := ctxAt(set, w, h)
				m := New().SetCursor("manage").Settle(ctx, spec())
				out := m.View(ctx, spec())
				if !strings.Contains(ansi.Strip(out), "Rename or remove") {
					t.Fatalf("%s %dx%d: the focused row is off screen:\n%s", set.Tier, w, h, ansi.Strip(out))
				}
				lines := strings.Split(out, "\n")
				if len(lines) > ctx.BodyHeight {
					t.Fatalf("%s %dx%d: %d lines for a body of %d", set.Tier, w, h, len(lines), ctx.BodyHeight)
				}
				for _, l := range lines {
					if lw := ansi.StringWidth(l); lw > w {
						t.Fatalf("%s %dx%d: a line of %d cells: %q", set.Tier, w, h, lw, ansi.Strip(l))
					}
				}
			}
		}
	}
}
