package settings

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/firstrun"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// restartTerminalHint is what a No answer earns when the font is installed:
// the terminal simply has not picked the font up yet.
const restartTerminalHint = "Close every Windows Terminal window, reopen, then run Settings > Icon check."

// probeScreen asks whether Nerd Font glyphs render in this terminal. It is
// the only way the nerd icon tier is ever turned on under Icons = auto, so a
// terminal that cannot draw the font never ends up as a screen of boxes.
type probeScreen struct {
	confirm confirm.Model
	done    bool
	yes     bool
}

// newProbeScreen returns the glyph probe. The dialog defaults to No like
// every other one in Devpit: nothing changes unless the user says it renders.
func newProbeScreen() probeScreen {
	return probeScreen{
		confirm: confirm.New("probe", firstrun.ProbePrompt+"   "+firstrun.ProbeLine(),
			"The last two are Nerd Font glyphs. Yes turns Nerd Font icons on; No keeps the plain unicode ones."),
	}
}

// Init implements uictx.Screen.
func (s probeScreen) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (s probeScreen) Title() string { return "Icon check" }

// ShortHelp implements uictx.Screen.
func (s probeScreen) ShortHelp() []key.Binding { return s.confirm.Keys.ShortHelp() }

// FullHelp implements uictx.Screen.
func (s probeScreen) FullHelp() [][]key.Binding { return s.confirm.Keys.FullHelp() }

// Update implements uictx.Screen.
func (s probeScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if s.done {
		return s, nil
	}
	if ans, ok := msg.(confirm.AnsweredMsg); ok {
		s.done = true
		cfg := ctx.Config
		s.yes = ans.Answer == confirm.AnswerYes
		cfg.GlyphsConfirmed = s.yes
		cfg.Icons = config.IconsAuto
		switch {
		case s.yes:
			return s, tea.Batch(uictx.SaveConfig(cfg), uictx.Status("success", "Nerd Font icons on"), uictx.Pop())
		case cfg.FontInstalled:
			return s, tea.Batch(uictx.SaveConfig(cfg), uictx.Status("warning", restartTerminalHint), uictx.Pop())
		default:
			return s, tea.Batch(uictx.SaveConfig(cfg), uictx.Status("", "Keeping unicode icons"), uictx.Pop())
		}
	}
	next, cmd := s.confirm.Update(msg)
	s.confirm = next
	return s, cmd
}

// View implements uictx.Screen.
func (s probeScreen) View(ctx uictx.Context) string {
	return s.confirm.View(ctx)
}

// probeLabel is the Settings row value for the glyph check.
func probeLabel(cfg config.Config) string {
	switch {
	case cfg.GlyphsConfirmed:
		return "passed, Nerd Font icons on"
	case cfg.FontInstalled:
		return "not run yet"
	default:
		return "install the icon font first"
	}
}
