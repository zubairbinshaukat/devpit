// Package footer draws the key-hint bar that sits at the bottom of every
// screen, plus an optional right-hand status line.
//
// The hints come from bubbles/v2 help, which renders a [key.Binding] slice and
// truncates it to the available width, so screens declare their keys once and
// never format them by hand. On the ascii tier the footer rewrites the hints
// as it renders them: a screen writes "↑↓ move" once, and a terminal that
// cannot draw arrows is shown "^v move" without every screen having to know
// which terminal it is drawing on.
package footer

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Rows is how many lines the footer occupies: the rule and the hint bar.
const Rows = 2

// asciiText spells the few non-ASCII characters that reach a key hint in a
// way a dumb terminal can draw. It is package level because a replacer is a
// value worth building once, not once per frame.
var asciiText = strings.NewReplacer(
	"↑", "^",
	"↓", "v",
	"←", "<",
	"→", ">",
	"·", "-",
	"…", "...",
	"✓", "+",
	"✗", "x",
)

// Model is the footer component.
type Model struct {
	// Status is optional text shown on the right, e.g. "Selected: 4.1 GB".
	Status string
	// StatusLevel tints Status: "", "success", "warning" or "danger".
	StatusLevel string

	help help.Model
}

// New returns a footer with a fresh help renderer.
func New() Model {
	h := help.New()
	h.ShortSeparator = "  ·  "
	h.FullSeparator = "    "
	return Model{help: h}
}

// Height is the number of rows the footer occupies, including its rule.
func (m Model) Height() int { return Rows }

// SetStatus sets the right-hand text and its tint.
func (m Model) SetStatus(level, text string) Model {
	m.Status = text
	m.StatusLevel = level
	return m
}

// View renders the footer for the given bindings at exactly ctx.Width columns.
func (m Model) View(ctx uictx.Context, bindings []key.Binding) string {
	th := ctx.Theme
	ascii := ctx.Icons.Tier == icons.TierASCII

	h := m.styled(ctx)
	if ascii {
		h.ShortSeparator = "  -  "
		h.Ellipsis = "..."
		bindings = foldBindings(bindings)
	}

	status := m.styledStatus(ctx)
	budget := ctx.Width - lipgloss.Width(status)
	if budget < 10 {
		budget = ctx.Width
		status = ""
	}
	h.SetWidth(budget)

	hints := h.ShortHelpView(bindings)
	rule := th.Rule.Render(strings.Repeat(ruleRune(ascii), max(0, ctx.Width)))

	if status == "" {
		return rule + "\n" + hints
	}
	pad := ctx.Width - lipgloss.Width(hints) - lipgloss.Width(status)
	if pad < 1 {
		pad = 1
	}
	return rule + "\n" + hints + strings.Repeat(" ", pad) + status
}

// FullHelpView renders the grouped help used by the "?" overlay.
func (m Model) FullHelpView(ctx uictx.Context, groups [][]key.Binding) string {
	h := m.styled(ctx)
	if ctx.Icons.Tier == icons.TierASCII {
		h.Ellipsis = "..."
		folded := make([][]key.Binding, len(groups))
		for i, g := range groups {
			folded[i] = foldBindings(g)
		}
		groups = folded
	}
	h.SetWidth(ctx.Width)
	return h.FullHelpView(groups)
}

// styled returns the help renderer wearing the current theme.
func (m Model) styled(ctx uictx.Context) help.Model {
	th := ctx.Theme
	h := m.help
	h.Styles.ShortKey = th.Key
	h.Styles.ShortDesc = th.Desc
	h.Styles.ShortSeparator = th.Desc
	h.Styles.FullKey = th.Key
	h.Styles.FullDesc = th.Desc
	h.Styles.FullSeparator = th.Desc
	h.Styles.Ellipsis = th.Desc
	return h
}

// foldBindings copies bindings with their help text spelled in ASCII. The
// copies are local to one frame, so no screen's own bindings are touched.
func foldBindings(bindings []key.Binding) []key.Binding {
	out := make([]key.Binding, len(bindings))
	for i, b := range bindings {
		h := b.Help()
		b.SetHelp(asciiText.Replace(h.Key), asciiText.Replace(h.Desc))
		out[i] = b
	}
	return out
}

// ruleRune is the line over the footer: box drawing where it is available, a
// hyphen where it is not.
func ruleRune(ascii bool) string {
	if ascii {
		return "-"
	}
	return "─"
}

// styledStatus applies the tint for the current status level.
func (m Model) styledStatus(ctx uictx.Context) string {
	if m.Status == "" {
		return ""
	}
	th := ctx.Theme
	switch m.StatusLevel {
	case "success":
		return th.Success.Render(m.Status)
	case "warning":
		return th.Warning.Render(m.Status)
	case "danger":
		return th.Danger.Render(m.Status)
	default:
		return th.Muted.Render(m.Status)
	}
}
