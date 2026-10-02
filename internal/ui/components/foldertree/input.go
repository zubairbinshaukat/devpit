package foldertree

import (
	"charm.land/bubbles/v2/textinput"

	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
)

// newFilterInput returns the filter text field, focused only while the user
// is typing into it. It is the results table's field, so a filter looks and
// behaves the same in both.
func newFilterInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "filter folders"
	styles := ti.Styles()
	styles.Cursor.Blink = false // a static caret, like the row cursor.
	ti.SetStyles(styles)
	return ti
}

// themedInput returns a copy of ti styled from th: an accent prompt, muted
// placeholder, plain body text and a reverse-video caret that survives
// NO_COLOR the way the row cursor does.
func themedInput(ti textinput.Model, th *theme.Theme) textinput.Model {
	state := textinput.StyleState{
		Text:        th.Base,
		Placeholder: th.Muted,
		Suggestion:  th.Muted,
		Prompt:      th.Accent,
	}
	styles := ti.Styles()
	styles.Focused = state
	styles.Blurred = state
	styles.Cursor.Color = th.Palette.Accent
	ti.SetStyles(styles)
	return ti
}
