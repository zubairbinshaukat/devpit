package settings

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// neverTouchScreen edits the never-touch path list. Devpit's own executable
// is always skipped by the scanner and the delete pre-flight regardless of
// this list (plan.md section 10: "Devpit's own exe path is always
// never-touch"), so it is never shown here and never needs adding: this
// screen only edits the user's own extra entries.
type neverTouchScreen struct {
	list   menu.Model
	input  textinput.Model
	adding bool
	errMsg string

	add    key.Binding
	submit key.Binding
	remove key.Binding
	back   key.Binding
}

// newNeverTouchScreen returns the never-touch sub-screen seeded from cfg.
func newNeverTouchScreen(cfg config.Config) neverTouchScreen {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Placeholder = `C:\path\to\never\touch`
	return neverTouchScreen{
		list:   menu.New(neverTouchItems(cfg)),
		input:  ti,
		add:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add")),
		submit: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save")),
		remove: key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "remove")),
		back:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// neverTouchItems rebuilds the list rows from cfg.
func neverTouchItems(cfg config.Config) []menu.Item {
	if len(cfg.NeverTouch) == 0 {
		return []menu.Item{{ID: "empty", Title: "No paths added yet.", Desc: "Press a to add one.", Disabled: true}}
	}
	items := make([]menu.Item, 0, len(cfg.NeverTouch))
	for _, p := range cfg.NeverTouch {
		items = append(items, menu.Item{ID: "entry", Title: p, Desc: "x to remove"})
	}
	return items
}

// Init implements uictx.Screen.
func (s neverTouchScreen) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (s neverTouchScreen) Title() string { return "Never-touch list" }

// ShortHelp implements uictx.Screen.
func (s neverTouchScreen) ShortHelp() []key.Binding {
	return []key.Binding{s.list.Keys.Up, s.add, s.remove, s.back}
}

// FullHelp implements uictx.Screen.
func (s neverTouchScreen) FullHelp() [][]key.Binding {
	return [][]key.Binding{{s.list.Keys.Up, s.list.Keys.Down}, {s.add, s.remove, s.back}}
}

// Update implements uictx.Screen.
func (s neverTouchScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if s.adding {
		return s.updateAdding(msg, ctx)
	}

	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(km, s.add):
			s.adding = true
			s.errMsg = ""
			s.input.SetValue("")
			return s, s.input.Focus()
		case key.Matches(km, s.remove):
			if it, ok := s.list.Selected(); ok && it.ID == "entry" {
				idx := s.list.Cursor()
				cfg := ctx.Config
				cfg.NeverTouch = slices.Delete(slices.Clone(cfg.NeverTouch), idx, idx+1)
				s.list = s.list.SetItems(neverTouchItems(cfg))
				return s, uictx.SaveConfig(cfg)
			}
		}
	}

	next, cmd := s.list.Update(msg)
	s.list = next
	return s, cmd
}

// updateAdding handles keys while the "add a path" text field is focused.
func (s neverTouchScreen) updateAdding(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok && key.Matches(km, s.submit) {
		path := strings.TrimSpace(s.input.Value())
		if path == "" {
			s.errMsg = "Enter a path first."
			return s, nil
		}
		cfg := ctx.Config
		for _, p := range cfg.NeverTouch {
			if strings.EqualFold(p, path) {
				s.adding = false
				s.input.Blur()
				return s, nil
			}
		}
		cfg.NeverTouch = append(slices.Clone(cfg.NeverTouch), path)
		s.adding = false
		s.errMsg = ""
		s.input.Blur()
		s.list = s.list.SetItems(neverTouchItems(cfg))
		return s, uictx.SaveConfig(cfg)
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return s, cmd
}

// View implements uictx.Screen.
func (s neverTouchScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(th.Muted.Render(ctx.Wrap(
		"Paths here are always skipped by scanning and deleting, on top of Devpit's own built-in protections.")))
	b.WriteString("\n\n")

	if s.adding {
		b.WriteString(th.Base.Render("Path to protect:"))
		b.WriteString("\n")
		b.WriteString(s.input.View())
	} else {
		b.WriteString(s.list.View(ctx))
	}

	if s.errMsg != "" {
		b.WriteString("\n\n")
		b.WriteString(th.Danger.Render(ctx.Icons.Warn + " " + s.errMsg))
	}
	return b.String()
}
