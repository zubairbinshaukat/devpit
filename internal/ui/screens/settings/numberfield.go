package settings

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// numberDaysMin and numberDaysMax bound both the active-days and
// older-days settings.
const (
	numberDaysMin = 1
	numberDaysMax = 365
)

// numberField describes one integer setting: its label text and how to
// fold a validated value back into the configuration.
type numberField struct {
	title string
	hint  string
	value int
	apply func(cfg config.Config, n int) config.Config
}

// numberScreen is a single-field numeric editor shared by the active-days
// and older-days rows.
type numberScreen struct {
	field  numberField
	input  textinput.Model
	errMsg string
	saved  bool

	submit key.Binding
	back   key.Binding
}

// newNumberScreen returns a numeric sub-screen for f.
func newNumberScreen(f numberField) numberScreen {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.SetValue(strconv.Itoa(f.value))
	ti.CursorEnd()
	return numberScreen{
		field:  f,
		input:  ti,
		submit: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save")),
		back:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// Init implements uictx.Screen.
func (s numberScreen) Init() tea.Cmd { return s.input.Focus() }

// Title implements uictx.Screen.
func (s numberScreen) Title() string { return s.field.title }

// ShortHelp implements uictx.Screen.
func (s numberScreen) ShortHelp() []key.Binding { return []key.Binding{s.submit, s.back} }

// FullHelp implements uictx.Screen.
func (s numberScreen) FullHelp() [][]key.Binding { return [][]key.Binding{{s.submit, s.back}} }

// Update implements uictx.Screen.
func (s numberScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok && key.Matches(km, s.submit) {
		n, err := strconv.Atoi(strings.TrimSpace(s.input.Value()))
		if err != nil || n < numberDaysMin || n > numberDaysMax {
			s.errMsg = "Enter a whole number of days from 1 to 365."
			s.saved = false
			return s, nil
		}
		s.errMsg = ""
		s.saved = true
		cfg := s.field.apply(ctx.Config, n)
		return s, tea.Batch(uictx.SaveConfig(cfg), uictx.Status("success", "Saved"))
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	s.saved = false
	return s, cmd
}

// View implements uictx.Screen.
func (s numberScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(th.Muted.Render(ctx.Wrap(s.field.hint)))
	b.WriteString("\n\n")
	b.WriteString(s.input.View())

	if s.errMsg != "" {
		b.WriteString("\n\n")
		b.WriteString(th.Danger.Render(ctx.Icons.Warn + " " + s.errMsg))
	} else if s.saved {
		b.WriteString("\n\n")
		b.WriteString(th.Success.Render(ctx.Icons.Tick + " Saved"))
	}
	return b.String()
}
