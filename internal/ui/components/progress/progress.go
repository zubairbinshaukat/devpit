// Package progress draws the bar every long action shows while it runs: a
// relative bar built from the icon set, an n/m counter, and the path being
// worked on right now, truncated so it never wraps.
//
// The component is deliberately passive. It owns no timer, starts no
// goroutine and answers no keys: the screen that owns it feeds it counters as
// they arrive and asks it for a fraction to hand to [tea.View.ProgressBar],
// which is what puts the progress in the Windows Terminal taskbar.
package progress

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// DefaultCells is how many cells the bar is drawn in when the width allows
// it. It matches the results table's relative bar so the two read alike.
const DefaultCells = 24

// Model is the progress component.
type Model struct {
	// Label is the verb shown before the bar, e.g. "Scanning".
	Label string
	// Current is the path being worked on. It is truncated to fit.
	Current string
	// Done and Total are the counters. A zero Total makes the bar
	// indeterminate: a scan does not know how many directories it will find.
	Done, Total int
	// Bytes is an optional running byte total shown beside the counters.
	Bytes uint64
	// Note is optional extra text on the counter line, e.g. "stale ·
	// rescanning".
	Note string

	width int
	cells int
}

// New returns a progress component with the given label.
func New(label string) Model {
	return Model{Label: label, width: uictx.MinWidth, cells: DefaultCells}
}

// SetWidth sets the width the component draws into.
func (m Model) SetWidth(w int) Model {
	if w < 0 {
		w = 0
	}
	m.width = w
	m.cells = DefaultCells
	if w > 0 && w < DefaultCells+40 {
		m.cells = max(4, w-40)
	}
	return m
}

// Width returns the width the component draws into.
func (m Model) Width() int { return m.width }

// SetCounts sets the done and total counters.
func (m Model) SetCounts(done, total int) Model {
	m.Done, m.Total = done, total
	return m
}

// SetCurrent sets the path being worked on.
func (m Model) SetCurrent(s string) Model {
	m.Current = s
	return m
}

// Indeterminate reports that the total is not known yet, which is the normal
// state of a scan: it cannot count directories before it has walked them.
func (m Model) Indeterminate() bool { return m.Total <= 0 }

// Fraction is how far along the action is, between 0 and 1. It is 0 while the
// total is unknown.
func (m Model) Fraction() float64 {
	if m.Total <= 0 {
		return 0
	}
	f := float64(m.Done) / float64(m.Total)
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}

// Percent is Fraction as a whole number between 0 and 100, which is the unit
// [tea.ProgressBar] wants.
func (m Model) Percent() int { return int(m.Fraction()*100 + 0.5) }

// TerminalBar is the value a screen puts on tea.View.ProgressBar so the
// terminal shows the same progress in its taskbar. An action whose total is
// not known yet reports an indeterminate bar rather than a misleading zero.
func (m Model) TerminalBar() *tea.ProgressBar {
	if m.Indeterminate() {
		return tea.NewProgressBar(tea.ProgressBarIndeterminate, 0)
	}
	return tea.NewProgressBar(tea.ProgressBarDefault, m.Percent())
}

// View renders the bar, the counters and the current path.
func (m Model) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	b.WriteString(th.Accent.Render(m.Label))
	b.WriteString("  ")
	b.WriteString(m.bar(ctx))
	b.WriteString("  ")
	b.WriteString(th.Base.Render(m.counters()))
	if m.Note != "" {
		b.WriteString(th.Muted.Render("  ·  "))
		b.WriteString(th.Warning.Render(m.Note))
	}
	b.WriteString("\n")
	b.WriteString(th.Muted.Render(m.current()))
	return b.String()
}

// bar draws the filled and empty cells.
func (m Model) bar(ctx uictx.Context) string {
	cells := m.cells
	if cells <= 0 {
		return ""
	}
	full := 0
	if !m.Indeterminate() {
		full = int(m.Fraction()*float64(cells) + 0.5)
	}
	if full > cells {
		full = cells
	}
	th := ctx.Theme
	return th.Success.Render(strings.Repeat(ctx.Icons.BarFull, full)) +
		th.Muted.Render(strings.Repeat(ctx.Icons.BarEmpty, cells-full))
}

// counters renders the right-hand figures: a percentage and n/m when the
// total is known, a plain count when it is not.
func (m Model) counters() string {
	var s string
	switch {
	case m.Indeterminate() && m.Done > 0:
		s = fmt.Sprintf("%d", m.Done)
	case m.Indeterminate():
		s = ""
	default:
		s = fmt.Sprintf("%3d%%  %d/%d", m.Percent(), m.Done, m.Total)
	}
	if m.Bytes > 0 {
		if s != "" {
			s += "  ·  "
		}
		s += header.FormatBytes(m.Bytes)
	}
	return s
}

// current renders the path line, truncated to the component's width.
func (m Model) current() string {
	if m.Current == "" {
		return ""
	}
	if m.width <= 1 {
		return m.Current
	}
	return ansi.Truncate(m.Current, m.width, "…")
}
