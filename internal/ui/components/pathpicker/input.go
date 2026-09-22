package pathpicker

import (
	"charm.land/bubbles/v2/textinput"

	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
)

// pathInputWidth is the field's drawing width, matching the hand-rolled
// textline it replaced.
const pathInputWidth = 40

// newPathInput returns the typed-path text field, focused only while the
// "Type a path…" row is active (see [Model.typing]).
//
// It used to be a hand-rolled field because bubbles/v2/textinput pulled in a
// clipboard dependency the module did not carry; the module now carries it
// (bubbles/v2/textinput is already used by the settings and network
// screens), so this is bubbles/v2/textinput with the picker's theme applied
// at render time by [themedInput].
func newPathInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = `D:\work`
	ti.SetWidth(pathInputWidth)
	styles := ti.Styles()
	styles.Cursor.Blink = false // a static caret, like the row cursor.
	ti.SetStyles(styles)
	return ti
}

// themedInput returns a copy of ti styled from th, so the path box reads
// like the rest of the picker: an accent prompt, muted placeholder, plain
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
