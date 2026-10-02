package settings

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// sized is the render context at a size, header and footer taken off the
// way the app takes them off.
func sized(cfg config.Config, set icons.Set, w, h int) uictx.Context {
	return uictx.Context{
		Theme: theme.For(true), Icons: set, Config: cfg,
		Width: w, Height: h, BodyHeight: h - 6, BodyTop: 4,
	}
}

// key builds a named key press.
func keyPress(name string) tea.KeyPressMsg {
	codes := map[string]rune{
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
		"enter": tea.KeyEnter,
	}
	if c, ok := codes[name]; ok {
		return tea.KeyPressMsg{Code: c}
	}
	return pressKey(name)
}

// step feeds one message and returns the next Model and its command.
func step(t *testing.T, m Model, ctx uictx.Context, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg, ctx)
	nm, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return nm, cmd
}

// order is every row the cursor can stop on, top to bottom.
var order = []string{
	rowTheme, rowIcons, rowEmoji, rowFont, rowProbe,
	rowFolders, rowNeverT, rowActiveDays, rowOlderDays, rowRescan,
	rowManager, rowDevPorts,
	rowSkill,
	rowTelemetry, rowUpdates,
	rowAbout, rowWhatsNew,
}

// Down walks every row in order and never stops on a heading or a gap; up
// walks back; the ends hold; home and end jump.
func TestKeysWalkRowsAndSkipHeadings(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {100, 30}, {140, 44}} {
		ctx := sized(config.Default(), icons.Unicode(), size[0], size[1])
		m := newTest(skillWith(service.AgentCreate, service.AgentCreate))
		var seen []string
		for range len(order) + 2 {
			seen = append(seen, m.lay(ctx).Cursor)
			m, _ = step(t, m, ctx, keyPress("down"))
		}
		if want := append(slices.Clone(order), rowWhatsNew, rowWhatsNew); !slices.Equal(seen, want) {
			t.Fatalf("%v: down walked %v, want %v", size, seen, want)
		}
		m, _ = step(t, m, ctx, keyPress("k"))
		if m.cursor() != rowAbout {
			t.Errorf("k from the last row went to %q", m.cursor())
		}
		m, _ = step(t, m, ctx, keyPress("home"))
		if m.cursor() != rowTheme {
			t.Errorf("home went to %q", m.cursor())
		}
		m, _ = step(t, m, ctx, keyPress("up"))
		if m.cursor() != rowTheme {
			t.Errorf("up at the top moved to %q", m.cursor())
		}
		m, _ = step(t, m, ctx, keyPress("end"))
		if m.cursor() != rowWhatsNew {
			t.Errorf("end went to %q", m.cursor())
		}
		m, _ = step(t, m, ctx, keyPress("pgup"))
		if m.cursor() == rowWhatsNew {
			t.Error("page up did not move")
		}
	}
}

// Enter and → step a choice forward, ← steps it back; on and off flip
// either way; every change saves at once and the row says so.
func TestCyclingAndToggling(t *testing.T) {
	cfg := config.Default()
	ctx := sized(cfg, icons.Unicode(), 100, 30)
	m := newTest(skillWith(service.AgentCreate, service.AgentCreate))

	m, cmd := step(t, m, ctx, keyPress("right"))
	saved, ok := findConfigChanged(flattenCmd(cmd))
	if !ok || saved.Theme != themes[1] {
		t.Fatalf("→ on Theme saved %q (%v), want %q", saved.Theme, ok, themes[1])
	}
	if m.list.Saved() != rowTheme || !strings.Contains(ansi.Strip(m.View(ctx)), "✓ saved") {
		t.Error("the row does not say it saved")
	}
	_, cmd = step(t, m, ctx, keyPress("left"))
	if saved, _ := findConfigChanged(flattenCmd(cmd)); saved.Theme != themes[len(themes)-1] {
		t.Errorf("← on Theme from auto saved %q, want the last theme", saved.Theme)
	}

	// The fade clears only the mark it was scheduled for.
	faded, _ := step(t, m, ctx, savedFadeMsg{seq: m.savedSeq - 1})
	if faded.list.Saved() == "" {
		t.Error("a stale fade cleared the mark")
	}
	faded, _ = step(t, m, ctx, savedFadeMsg{seq: m.savedSeq})
	if faded.list.Saved() != "" {
		t.Error("the fade did not clear the mark")
	}

	m = m.at(rowTelemetry)
	for _, k := range []string{"enter", "left", "right", " "} {
		_, cmd = step(t, m, ctx, keyPress(k))
		if saved, ok := findConfigChanged(flattenCmd(cmd)); !ok || !saved.TelemetryOptIn {
			t.Errorf("%q on Usage stats (off) saved %v, %v", k, ok, saved.TelemetryOptIn)
		}
	}

	// ← and → do nothing on a row that opens a screen.
	m = m.at(rowFont)
	if _, cmd := step(t, m, ctx, keyPress("right")); cmd != nil {
		t.Error("→ on Icon font did something")
	}
}

// Enter on each row that opens a screen pushes the right one.
func TestEveryOpenRowOpensItsScreen(t *testing.T) {
	cfg := config.Default()
	ctx := sized(cfg, icons.Unicode(), 100, 30)
	want := map[string]string{
		rowFont: "Icon font", rowProbe: "Icon check", rowFolders: "Projects folder",
		rowNeverT: "Never-touch folders", rowActiveDays: "Recent projects", rowOlderDays: "Age filter",
		rowRescan: "Forget last scan", rowDevPorts: "Dev ports", rowSkill: "AI agent skill",
		rowAbout: "About", rowWhatsNew: "What's new",
	}
	for id, title := range want {
		if id == rowFont {
			continue // the font screen reads the font state when built; its own tests cover it
		}
		m := newTest(skillWith(service.AgentCreate, service.AgentCreate))
		m = m.at(id)
		_, cmd := step(t, m, ctx, keyPress("enter"))
		s, ok := findPush(flattenCmd(cmd))
		if !ok {
			t.Errorf("Enter on %s pushed nothing", id)
			continue
		}
		if s.Title() != title {
			t.Errorf("Enter on %s pushed %q, want %q", id, s.Title(), title)
		}
	}
}

// A click on a label moves there, a second click acts, a click on a value
// acts at once, a click on a heading or past the list does nothing, the
// pointer passing over a row highlights it, and the wheel moves the cursor.
func TestMouse(t *testing.T) {
	cfg := config.Default()
	ctx := sized(cfg, icons.Unicode(), 100, 30)
	m := newTest(skillWith(service.AgentCreate, service.AgentCreate))
	valueX := m.lay(ctx).ValueX + 1

	y := ctx.BodyTop + bodyRowOf(t, m, ctx, rowEmoji)
	m, cmd := step(t, m, ctx, tea.MouseClickMsg{X: 6, Y: y, Button: tea.MouseLeft})
	if m.cursor() != rowEmoji || cmd != nil {
		t.Fatalf("a first click on a label: cursor %q, cmd %v", m.cursor(), cmd != nil)
	}
	_, cmd = step(t, m, ctx, tea.MouseClickMsg{X: 6, Y: y, Button: tea.MouseLeft})
	if saved, ok := findConfigChanged(flattenCmd(cmd)); !ok || saved.Emoji {
		t.Error("a second click did not flip Emoji off")
	}

	y = ctx.BodyTop + bodyRowOf(t, m, ctx, rowManager)
	m2, cmd := step(t, m, ctx, tea.MouseClickMsg{X: valueX, Y: y, Button: tea.MouseLeft})
	if saved, ok := findConfigChanged(flattenCmd(cmd)); !ok || saved.PreferredManager != "scoop" || m2.cursor() != rowManager {
		t.Error("a click on a value did not change it")
	}

	// The heading above the first row, and a click right of the list.
	head := ctx.BodyTop + bodyRowOf(t, m, ctx, rowTheme) - 1
	if n, cmd := step(t, m, ctx, tea.MouseClickMsg{X: 4, Y: head, Button: tea.MouseLeft}); cmd != nil || n.cursor() != m.cursor() {
		t.Error("a click on a heading did something")
	}
	if _, cmd := step(t, m, ctx, tea.MouseClickMsg{X: 98, Y: y, Button: tea.MouseLeft}); cmd != nil {
		t.Error("a click on the description pane did something")
	}

	y = ctx.BodyTop + bodyRowOf(t, m, ctx, rowDevPorts)
	m, _ = step(t, m, ctx, tea.MouseMotionMsg{X: 6, Y: y})
	if m.cursor() != rowDevPorts {
		t.Errorf("hover left the cursor on %q", m.cursor())
	}
	m, _ = step(t, m, ctx, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.cursor() != rowSkill {
		t.Errorf("the wheel moved to %q, want the next row", m.cursor())
	}
	m, _ = step(t, m, ctx, tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.cursor() != rowDevPorts {
		t.Errorf("the wheel back moved to %q", m.cursor())
	}
}

// At 80x24 the list scrolls: the focused row is always drawn, the ends say
// how many rows are hidden, and hover never scrolls the list.
func TestScrollingKeepsTheCursorOnScreen(t *testing.T) {
	for _, set := range []icons.Set{icons.Unicode(), icons.ASCII()} {
		ctx := sized(config.Default(), set, 80, 24)
		m := newTest(skillWith(service.AgentCreate, service.AgentCreate))
		up, down := "▲", "▼"
		if set.Tier == icons.TierASCII {
			up, down = "^", "v"
		}
		if out := ansi.Strip(m.View(ctx)); !hasMore(out, down) || hasMore(out, up) {
			t.Fatalf("%s: the top of a long list should say what is below only:\n%s", set.Tier, out)
		}
		for _, id := range order {
			if cur := m.lay(ctx).Cursor; cur != id {
				t.Fatalf("cursor on %q, want %q", cur, id)
			}
			if !strings.Contains(ansi.Strip(m.View(ctx)), labelOf(id)) {
				t.Fatalf("%s: %q is not on screen:\n%s", set.Tier, id, ansi.Strip(m.View(ctx)))
			}
			if lines := strings.Split(m.View(ctx), "\n"); len(lines) > ctx.BodyHeight {
				t.Fatalf("the frame is %d lines, the body %d", len(lines), ctx.BodyHeight)
			}
			m, _ = step(t, m, ctx, keyPress("down"))
		}
		if out := ansi.Strip(m.View(ctx)); !hasMore(out, up) {
			t.Errorf("%s: the bottom of the list should say what is above:\n%s", set.Tier, out)
		}

		// Hover over the top row of the window: the window stays put.
		before := m.lay(ctx).Start
		for y := range ctx.BodyHeight {
			m2, _ := step(t, m, ctx, tea.MouseMotionMsg{X: 6, Y: ctx.BodyTop + y})
			if m2.lay(ctx).Start != before {
				t.Fatalf("hover on body row %d scrolled the list", y)
			}
		}
	}
}

// The groups keep a blank line between them when there is room and give it
// up first when there is not.
func TestGapsGoFirst(t *testing.T) {
	m := newTest(skillWith(service.AgentCreate, service.AgentCreate))
	tall := m.lay(sized(config.Default(), icons.Unicode(), 120, 40))
	short := m.lay(sized(config.Default(), icons.Unicode(), 100, 30))
	if tall.Gaps == 0 || tall.Scrolling {
		t.Errorf("a tall terminal: %d gaps, scrolling %v", tall.Gaps, tall.Scrolling)
	}
	if short.Gaps != 0 || short.Scrolling {
		t.Errorf("100x30: %d gaps, scrolling %v; the gaps should go and the list fit", short.Gaps, short.Scrolling)
	}
}

// Every row has a description a new user can read, in every skill state.
func TestEveryRowExplainsItself(t *testing.T) {
	sessions := []*skillSession{nil, {loaded: true, err: errors.New("boom")}}
	for _, f := range skillStates() {
		sessions = append(sessions, sessionOf(f))
	}
	for _, s := range sessions {
		for _, g := range groups(config.Default(), s, "0.4.0") {
			if g.Title == "" {
				t.Error("a group has no heading")
			}
			for _, r := range g.Items {
				if r.Kind == kindNote {
					continue
				}
				if len(r.Desc) < 60 || !strings.HasSuffix(r.Desc, ".") {
					t.Errorf("%s: description %q is too short or not a sentence", r.ID, r.Desc)
				}
				if strings.Contains(r.Label, ":") || strings.Contains(r.Value, "(s)") {
					t.Errorf("%s: label %q value %q", r.ID, r.Label, r.Value)
				}
			}
		}
	}
}

// No line is wider than the terminal, at every width from 60 to 200, in
// every tier, with a very long projects folder.
func TestNoLineWiderThanTheTerminal(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProjectsFolder = `D:\work\clients\a-very-long-client-name-for-testing\monorepo\packages\web-frontend`
	for _, set := range []icons.Set{icons.Unicode(), icons.ASCII(), icons.Nerd()} {
		for w := 60; w <= 200; w++ {
			for _, h := range []int{24, 30, 44} {
				ctx := sized(cfg, set, w, h)
				m := newTest(skillWith(service.AgentCurrent, service.AgentUpdate))
				for _, id := range []string{rowTheme, rowFolders, rowWhatsNew} {
					m = m.at(id).settled(ctx)
					for _, l := range strings.Split(m.View(ctx), "\n") {
						if lw := ansi.StringWidth(l); lw > w {
							t.Fatalf("%s %dx%d on %s: a line of %d cells: %q", set.Tier, w, h, id, lw, ansi.Strip(l))
						}
					}
				}
			}
		}
	}
}

// A long folder is shortened in the middle, keeping its drive and its last
// folder; no never-touch folders reads "none".
func TestValuesReadNaturally(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultProjectsFolder = `D:\work\clients\acme-corporation\projects`
	ctx := sized(cfg, icons.Unicode(), 100, 30)
	out := ansi.Strip(newTest(skillWith(service.AgentCreate, service.AgentCreate)).View(ctx))
	for _, want := range []string{`D:\`, `…\projects`, "none", "27 ports", "last 7 days", "older than 30 days", "‹ auto ›", "● on", "○ off"} {
		if !strings.Contains(out, want) {
			t.Errorf("the list lacks %q:\n%s", want, out)
		}
	}
	cfg.NeverTouch = []string{`D:\a`, `D:\b`}
	if v := rowOf(cfg, rowNeverT).Value; v != "2 folders" {
		t.Errorf("two never-touch folders read %q", v)
	}
}

// The skill row follows what is on the PC; without Claude Code it is one
// quiet line the cursor never stops on.
func TestSkillRowStates(t *testing.T) {
	cases := []struct {
		f     *fakeSkill
		value string
	}{
		{skillWith(service.AgentCreate, service.AgentCreate), "not installed"},
		{skillWith(service.AgentCurrent, service.AgentCurrent), "installed"},
		{skillWith(service.AgentCurrent, service.AgentUpdate), "update available"},
		{skillWith(service.AgentForeign, service.AgentForeign), "another devpit skill"},
		{viaLink(), "installed via a link"},
		{&fakeSkill{statusErr: errors.New("boom")}, "could not check"},
	}
	for _, c := range cases {
		r := skillRow(sessionOf(c.f))
		if r.Value != c.value || r.Kind != kindOpen {
			t.Errorf("row %+v, want %q", r, c.value)
		}
	}
	if r := skillRow(&skillSession{}); r.Value != "checking…" {
		t.Errorf("before the look the row says %q", r.Value)
	}

	ctx := sized(config.Default(), icons.Unicode(), 100, 30)
	m := newTest(&fakeSkill{claude: false})
	if !strings.Contains(ansi.Strip(m.View(ctx)), "Claude Code is not installed") {
		t.Errorf("no note without Claude Code:\n%s", ansi.Strip(m.View(ctx)))
	}
	m = m.at(rowDevPorts)
	m, _ = step(t, m, ctx, keyPress("down"))
	if m.cursor() != rowTelemetry {
		t.Errorf("down from Dev ports stopped on %q, past the note", m.cursor())
	}
}

// Each opening of Settings looks again, so Claude Code installed since the
// last time shows up.
func TestSettingsLooksAgainOnEveryOpen(t *testing.T) {
	f := &fakeSkill{claude: false}
	if r := skillRow(newTest(f).skill); r.Kind != kindNote {
		t.Fatalf("without Claude Code: %+v", r)
	}
	f.claude = true
	f.targets = []service.AgentTarget{{Account: "default", File: skillDefault, State: service.AgentCreate}}
	if r := skillRow(newTest(f).skill); r.Kind != kindOpen || r.Value != "not installed" {
		t.Errorf("after installing Claude Code: %+v", r)
	}
}

// skillStates are the fakes for every state the skill can be in.
func skillStates() []*fakeSkill {
	return []*fakeSkill{
		skillWith(service.AgentCreate, service.AgentCreate),
		skillWith(service.AgentCurrent, service.AgentCurrent),
		skillWith(service.AgentCurrent, service.AgentUpdate),
		skillWith(service.AgentForeign, service.AgentForeign),
		viaLink(),
		{claude: false},
	}
}

// viaLink is the skill installed, the default account reaching it through a
// shared skills folder.
func viaLink() *fakeSkill {
	f := skillWith(service.AgentCurrent, service.AgentShared)
	f.targets[0].ThroughLink = true
	f.targets[1].SharedWith = "default"
	return f
}

// sessionOf is a session that has already looked at f.
func sessionOf(f *fakeSkill) *skillSession {
	s := &skillSession{open: opener(f)}
	if msg, ok := s.load()().(skillLoadedMsg); ok {
		s.apply(msg)
	}
	return s
}

// labelOf is the label of the row with this id.
func labelOf(id string) string { return rowOf(config.Default(), id).Label }

// hasMore reports a "n more" line with the given arrow.
func hasMore(out, arrow string) bool {
	return regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(arrow) + ` \d+ more`).MatchString(out)
}
