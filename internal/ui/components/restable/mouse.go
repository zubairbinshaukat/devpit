package restable

import (
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Click acts on a left click at a row and column of the rendered table.
//
// row counts from the table's first line, the column heading, so row 1 is
// the first body row whatever the scroll offset; col counts from the table's
// left edge. A click on a tick box ticks that row, or everything under a
// heading. A click on a heading's fold arrow folds or unfolds it. A click
// anywhere else on a row moves the cursor there. The heading, the status
// line and anything past the last row are ignored.
//
// The hit zones are one cell wider than the glyphs on each side, because a
// Nerd Font tick box is a single cell and a single-cell target is a miss
// waiting to happen; the zones never overlap.
func (m Model) Click(ctx uictx.Context, row, col int) (Model, tea.Cmd) {
	if ctx.Width < uictx.MinWidth || ctx.Height < uictx.MinHeight {
		return m, nil
	}
	body := row - 1
	if body < 0 || body >= m.bodyHeight() {
		return m, nil
	}
	i := m.top + body
	if i >= len(m.rows) {
		return m, nil
	}
	r := m.rows[i]
	m.cursor = i

	l := m.layout(ctx)
	box := boxColumn(r)
	arrow := box + l.mark + 1
	switch {
	case col >= box-1 && col <= box+l.mark:
		m.toggleAt(r)
	case r.kind == rowGroup && col >= arrow && col <= arrow+1:
		m.toggleFold(r)
	}
	m.clampCursor()
	return m, nil
}

// boxColumn is the column a row's tick box starts at: after the margin, the
// caret and its space, and the row's indent. It mirrors [Model.prefix], which
// is what draws it.
func boxColumn(r row) int { return leftPad + 2 + indent(r) }
