// Package firstrun is the one screen a new user sees before the main menu.
//
// It answers three questions in one pass: does this terminal render the
// glyphs, which theme, and may Devpit send anonymous totals. Everything is
// on one page so the screen can be finished in four keystrokes.
//
// It is hand-rolled on the menu component rather than built with huh: the
// glyph probe has to render raw glyphs from every tier at a fixed width, and
// the whole screen has to work when Devpit's own icon resolution is still
// undecided, which is exactly the case a form library abstracts away.
package firstrun

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// TelemetryQuestion is the exact wording from the PRD. It is a constant so
// the PRIVACY.md text and this screen can never drift apart.
const TelemetryQuestion = "Help show how much space Devpit saves? Only totals are sent — no file names or paths."

// ProbePrompt is the glyph probe question.
const ProbePrompt = "Do all of these render?"

// SafetyBullets are the three promises the screen makes, in order.
var SafetyBullets = []string{
	"Nothing is deleted without a preview and an explicit confirmation.",
	"The default answer on every confirmation is No.",
	"Nothing leaves this machine unless you turn usage stats on.",
}

// Row identifiers.
const (
	rowGlyphs    = "glyphs"
	rowTheme     = "theme"
	rowTelemetry = "telemetry"
	rowDone      = "done"
)

// themes is the cycle order for the theme row.
var themes = []string{config.ThemeAuto, config.ThemeDark, config.ThemeLight}

// DoneMsg is emitted when the user finishes the screen. The app saves the
// config, rebuilds the theme and icon set from it, and swaps in the home
// screen.
type DoneMsg struct {
	// Config is the configuration to persist. FirstRunDone is already set.
	Config config.Config
}

// Model is the first-run screen.
type Model struct {
	menu menu.Model

	glyphsOK  bool
	theme     string
	telemetry bool

	change key.Binding
	yes    key.Binding
	no     key.Binding
}

// New returns the first-run screen seeded from the current config. Usage
// stats start off, as the PRD requires, and the glyph answer starts at No so
// a terminal that cannot draw the probe never claims it can.
func New(cfg config.Config) Model {
	return Model{
		menu:      menu.New(nil),
		glyphsOK:  false,
		theme:     cfg.Theme,
		telemetry: false,
		change:    key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter/space", "change")),
		yes:       key.NewBinding(key.WithKeys("y"), key.WithHelp("y/n", "yes/no")),
		no:        key.NewBinding(key.WithKeys("n")),
	}
}

// Init implements uictx.Screen.
func (m Model) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (m Model) Title() string { return "Welcome" }

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	return []key.Binding{m.menu.Keys.Up, m.change, m.yes}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.menu.Keys.Up, m.menu.Keys.Down},
		{m.change, m.yes},
	}
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	m.menu = m.menu.SetItems(m.rows())

	if km, ok := msg.(tea.KeyPressMsg); ok {
		sel, has := m.menu.Selected()
		switch {
		case key.Matches(km, m.change) && has:
			if sel.ID == rowDone {
				return m, m.finish(ctx.Config)
			}
			return m.cycle(sel.ID), nil
		case key.Matches(km, m.yes) && has:
			return m.set(sel.ID, true), nil
		case key.Matches(km, m.no) && has:
			return m.set(sel.ID, false), nil
		}
	}

	next, cmd := m.menu.Update(msg)
	m.menu = next
	return m, cmd
}

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	if e := ctx.Emoji(icons.EmojiFlag); e != "" {
		b.WriteString(e + " ")
	}
	b.WriteString(th.Title.Render("Welcome to Devpit"))
	b.WriteString("\n\n")
	b.WriteString(th.Base.Render(ctx.Wrap(
		"Devpit is a pit stop for your dev machine: free disk space, fix stuck ports, and keep your tools up to date, from one menu.")))
	b.WriteString("\n\n")

	for _, s := range SafetyBullets {
		b.WriteString(th.Success.Render("  ● "))
		b.WriteString(th.Base.Render(ctx.Truncate(s)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(th.Muted.Render(ProbePrompt + "   " + ProbeLine()))
	b.WriteString("\n\n")

	mm := m.menu.SetItems(m.rows())
	b.WriteString(mm.View(ctx))

	return b.String()
}

// ProbeLine is the glyph sample the user is asked to judge: the three unicode
// shapes plus two Nerd Font glyphs. If the last two show as boxes, the answer
// is No and Devpit stays out of the nerd tier.
func ProbeLine() string {
	u := icons.Unicode()
	n := icons.Nerd()
	return strings.Join([]string{u.Tick, u.Safe, u.Folder, n.Folder, n.Node}, " ")
}

// rows builds the four question rows from the current answers.
func (m Model) rows() []menu.Item {
	return []menu.Item{
		{
			ID:    rowGlyphs,
			Title: "Glyphs render correctly: " + yesNo(m.glyphsOK),
			Desc:  "Yes turns on the Nerd Font icon tier. No keeps the plain unicode one.",
		},
		{
			ID:    rowTheme,
			Title: "Theme: " + m.theme,
			Desc:  "auto follows your terminal background",
		},
		{
			ID:    rowTelemetry,
			Title: "Usage stats: " + yesNo(m.telemetry),
			Desc:  TelemetryQuestion,
		},
		{
			ID:    rowDone,
			Title: "Start using Devpit",
			Desc:  "Saves these answers. You can change all of them in Settings.",
		},
	}
}

// cycle advances one row to its next value.
func (m Model) cycle(id string) Model {
	switch id {
	case rowGlyphs:
		m.glyphsOK = !m.glyphsOK
	case rowTheme:
		m.theme = nextTheme(m.theme)
	case rowTelemetry:
		m.telemetry = !m.telemetry
	}
	return m
}

// set answers a yes/no row directly.
func (m Model) set(id string, v bool) Model {
	switch id {
	case rowGlyphs:
		m.glyphsOK = v
	case rowTelemetry:
		m.telemetry = v
	}
	return m
}

// finish builds the command that persists the answers.
func (m Model) finish(cfg config.Config) tea.Cmd {
	cfg.Theme = m.theme
	cfg.TelemetryOptIn = m.telemetry
	cfg.FirstRunDone = true
	// The answer is recorded as a fact about this terminal, not as a forced
	// tier, so the auto rule (and a later font install) can still reason
	// about it.
	cfg.GlyphsConfirmed = m.glyphsOK
	cfg.Icons = config.IconsAuto
	cfg.Normalize()
	return func() tea.Msg { return DoneMsg{Config: cfg} }
}

// nextTheme returns the theme after current, wrapping around.
func nextTheme(current string) string {
	for i, t := range themes {
		if t == current {
			return themes[(i+1)%len(themes)]
		}
	}
	return themes[0]
}

// yesNo renders a boolean the way this screen words it.
func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
