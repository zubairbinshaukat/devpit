package restable

import (
	"charm.land/bubbles/v2/textinput"

	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
)

// newFilterInput returns the filter text field, focused only while the user
// is typing into it (see [Model.filtering]).
//
// It used to be a hand-rolled field because bubbles/v2/textinput pulled in a
// clipboard dependency the module did not carry; the module now carries it
// (bubbles/v2/textinput is already used by the settings and network
// screens), so this is bubbles/v2/textinput with the table's theme applied
// at render time by [themedInput].
func newFilterInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "filter"
	styles := ti.Styles()
	styles.Cursor.Blink = false // a static caret, like the row cursor.
	ti.SetStyles(styles)
	return ti
}

// themedInput returns a copy of ti styled from th, so the filter box reads
// like the rest of the table: an accent prompt, muted placeholder, plain
// body text and a reverse-video caret that survives NO_COLOR the same way
// the row cursor does.
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
