package firstrun

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

func ctxFor(set icons.Set, cfg config.Config) uictx.Context {
	return uictx.Context{Theme: theme.For(true), Icons: set, Config: cfg, Width: 100, Height: 30, BodyHeight: 26, BodyTop: 2}
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

// drive feeds keys and returns the model and the last command.
func drive(t *testing.T, m Model, ctx uictx.Context, keys ...string) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		next, c := m.Update(press(k), ctx)
		m = next.(Model)
		cmd = c
	}
	return m, cmd
}

func done(t *testing.T, cmd tea.Cmd) config.Config {
	t.Helper()
	if cmd == nil {
		t.Fatal("finishing produced no command")
	}
	msg, ok := cmd().(DoneMsg)
	if !ok {
		t.Fatalf("finishing produced %T, want DoneMsg", cmd())
	}
	return msg.Config
}

// Enter walks the four steps and the last one saves; nothing before it does.
func TestWizardWalksFourSteps(t *testing.T) {
	cfg := config.Default()
	ctx := ctxFor(icons.Unicode(), cfg)
	m := New(cfg)
	for i, want := range []string{"Welcome to Devpit", "Do these look like icons?", "Pick a look", "One last thing"} {
		out := ansi.Strip(m.View(ctx))
		if !strings.Contains(out, want) || !strings.Contains(out, "Step "+string(rune('1'+i))+" of 4") {
			t.Fatalf("step %d lacks %q:\n%s", i+1, want, out)
		}
		var cmd tea.Cmd
		m, cmd = drive(t, m, ctx, "enter")
		if i < 3 && cmd != nil {
			if _, saved := cmd().(DoneMsg); saved {
				t.Fatalf("step %d saved before the end", i+1)
			}
		}
		if i == 3 {
			got := done(t, cmd)
			if !got.FirstRunDone || got.TelemetryOptIn || got.GlyphsConfirmed {
				t.Errorf("defaults saved as %+v", got)
			}
		}
	}
}

// Esc steps back and keeps what was answered; on the first step it stays.
func TestEscGoesBack(t *testing.T) {
	cfg := config.Default()
	ctx := ctxFor(icons.Unicode(), cfg)
	m, _ := drive(t, New(cfg), ctx, "esc")
	if m.step != stepWelcome {
		t.Fatalf("Esc on the first step moved to %v", m.step)
	}
	m, _ = drive(t, m, ctx, "enter", "y", "enter", "esc")
	if m.step != stepIcons || m.cursor != 0 || !m.glyphsOK {
		t.Errorf("back on icons: step=%v cursor=%d ok=%v", m.step, m.cursor, m.glyphsOK)
	}
}

// A terminal limited to ascii is never asked about icons it cannot draw.
func TestAsciiSkipsTheIconsStep(t *testing.T) {
	cfg := config.Default()
	ctx := ctxFor(icons.ASCII(), cfg)
	m, _ := drive(t, New(cfg), ctx, "enter")
	if m.step != stepTheme {
		t.Fatalf("step after welcome = %v, want theme", m.step)
	}
	if out := ansi.Strip(m.View(ctx)); !strings.Contains(out, "Step 2 of 3") {
		t.Errorf("the stepper still counts the skipped step:\n%s", out)
	}
	m, _ = drive(t, m, ctx, "esc")
	if m.step != stepWelcome {
		t.Errorf("back from theme = %v, want welcome", m.step)
	}
}

// When the installer put the icon font in, the answer starts on Yes, and
// the user can still say No.
func TestInstalledFontPreselectsYes(t *testing.T) {
	cfg := config.Default()
	cfg.FontInstalled = true
	ctx := ctxFor(icons.Unicode(), cfg)
	m, _ := drive(t, New(cfg), ctx, "enter")
	if m.cursor != 0 {
		t.Fatalf("icons step starts on option %d, want Yes", m.cursor)
	}
	_, cmd := drive(t, m, ctx, "enter", "enter", "enter")
	if !done(t, cmd).GlyphsConfirmed {
		t.Error("the preselected Yes was not saved")
	}
	m, _ = drive(t, New(cfg), ctx, "enter", "n")
	_, cmd = drive(t, m, ctx, "enter", "enter", "enter")
	if done(t, cmd).GlyphsConfirmed {
		t.Error("an explicit No was overridden by the installed font")
	}
}

// Moving through themes repaints the app without saving; the saved theme is
// the one the user left the step on.
func TestThemePreviewIsLiveButUnsaved(t *testing.T) {
	cfg := config.Default()
	ctx := ctxFor(icons.Unicode(), cfg)
	m, _ := drive(t, New(cfg), ctx, "enter", "enter")
	m, cmd := drive(t, m, ctx, "down")
	if cmd == nil {
		t.Fatal("moving on the theme step sent nothing")
	}
	msg, ok := cmd().(uictx.ConfigChangedMsg)
	if !ok || msg.Persist || msg.Config.Theme != config.Themes[1] {
		t.Fatalf("preview = %#v, want an unsaved change to %q", cmd(), config.Themes[1])
	}
	_, cmd = drive(t, m, ctx, "enter", "enter")
	if got := done(t, cmd).Theme; got != config.Themes[1] {
		t.Errorf("saved theme = %q, want %q", got, config.Themes[1])
	}
}

// Usage stats stay off unless the user picks Yes on the privacy step.
func TestTelemetryNeedsAnExplicitYes(t *testing.T) {
	cfg := config.Default()
	cfg.TelemetryOptIn = true
	ctx := ctxFor(icons.Unicode(), cfg)
	_, cmd := drive(t, New(cfg), ctx, "enter", "enter", "enter", "enter")
	if done(t, cmd).TelemetryOptIn {
		t.Error("a previous opt-in was carried through unasked")
	}
	_, cmd = drive(t, New(cfg), ctx, "enter", "enter", "enter", "y", "enter")
	if !done(t, cmd).TelemetryOptIn {
		t.Error("y on the privacy step did not opt in")
	}
}

// A click on an option picks it, and a click on the button moves on.
func TestClicksPickAndPress(t *testing.T) {
	cfg := config.Default()
	ctx := ctxFor(icons.Unicode(), cfg)
	m, _ := drive(t, New(cfg), ctx, "enter")
	lines := strings.Split(ansi.Strip(m.View(ctx)), "\n")
	yes, next := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "Yes, they look like icons") {
			yes = i
		}
		if strings.Contains(l, "Next") {
			next = i
		}
	}
	if yes < 0 || next < 0 {
		t.Fatalf("no option or button:\n%s", strings.Join(lines, "\n"))
	}
	nm, _ := m.Update(tea.MouseClickMsg{X: 20, Y: ctx.BodyTop + yes, Button: tea.MouseLeft}, ctx)
	m = nm.(Model)
	if !m.glyphsOK {
		t.Error("clicking Yes did not pick it")
	}
	nm, _ = m.Update(tea.MouseClickMsg{X: 20, Y: ctx.BodyTop + next, Button: tea.MouseLeft}, ctx)
	if nm.(Model).step != stepTheme {
		t.Errorf("clicking Next left the wizard on %v", nm.(Model).step)
	}
}
