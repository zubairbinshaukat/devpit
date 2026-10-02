package settings

import (
	"github.com/zubairbinshaukat/devpit/internal/ui/components/choices"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// at is the screen with the cursor on the row with this id.
func (m Model) at(id string) Model {
	m.list = m.list.SetCursor(id)
	return m
}

// settled is the screen with its scroll position worked out for ctx.
func (m Model) settled(ctx uictx.Context) Model {
	m.list = m.list.Settle(ctx, m.spec(ctx))
	return m
}

// lay is this frame's layout.
func (m Model) lay(ctx uictx.Context) choices.Layout { return m.list.Layout(ctx, m.spec(ctx)) }

// cursor is the id of the highlighted row.
func (m Model) cursor() string { return m.list.Cursor() }
