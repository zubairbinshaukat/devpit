// Package checklist draws one row of a tick list: the selection bar and
// caret, a tick box that is green when ticked and grey when not, the row's
// text, and an optional note pushed to the right edge.
//
// Every screen that lets the user tick things (update, install, ports) draws
// its rows through here, so a tick box looks and behaves the same wherever
// it appears, and the highlighted row is the same lifted band the menus use.
package checklist

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Box is the state of a row's tick box.
type Box int

// The tick box states. None draws no box at all, for a heading or a row
// that cannot be ticked but should still line up with the rest.
const (
	None Box = iota
	Off
	On
	Some
	// Blank keeps the box's columns empty, so an untickable row's text
	// lines up with its tickable neighbours.
	Blank
)

// BoxOf returns On or Off for a boolean.
func BoxOf(on bool) Box {
	if on {
		return On
	}
	return Off
}

// Line is one row.
type Line struct {
	// Selected puts the row on the highlight band.
	Selected bool
	// Box is the tick box state.
	Box Box
	// Indent is extra columns before the box, for rows under a heading.
	Indent int
	// Text is the row's label. It may carry its own styling; Style is
	// applied to it only when it is plain.
	Text string
	// Style is the text colour; the zero value means body text.
	Style *lipgloss.Style
	// TextWidth pads Text to this many columns, so the Aside of every row
	// in a list starts in the same column. Zero means no padding.
	TextWidth int
	// Aside follows the text in the muted colour, e.g. "4.87.0 → 4.91.0".
	// It is cut before the text is.
	Aside string
	// Dim greys the whole row out: a row that cannot be ticked.
	Dim bool
	// Note is pushed to the right edge in the muted colour, and dropped when
	// the row is too narrow to hold it.
	Note string
}

// leftPad is the margin before the caret, matching the menus.
const leftPad = 2

// Render draws the row into exactly width columns (or its natural width when
// width is zero). Text is cut, never the box or the caret.
func Render(ctx uictx.Context, l Line, width int) string {
	th := ctx.Theme
	ic := ctx.Icons

	lead := strings.Repeat(" ", leftPad)
	if l.Selected {
		lead = ic.SelectBar + strings.Repeat(" ", leftPad-ansi.StringWidth(ic.SelectBar))
	}
	caret := strings.Repeat(" ", ansi.StringWidth(ic.Cursor))
	if l.Selected {
		caret = ic.Cursor
	}
	indent := strings.Repeat(" ", max(0, l.Indent))

	box, boxStyle := boxGlyph(ctx, l.Box)
	boxCell := ""
	if l.Box != None {
		boxCell = box + " "
	}

	prefix := lead + caret + " " + indent + boxCell
	text := l.Text
	if l.TextWidth > 0 {
		if ansi.StringWidth(text) > l.TextWidth {
			text = ansi.Truncate(text, l.TextWidth, ellipsis(ctx))
		}
		if l.Aside != "" {
			text += strings.Repeat(" ", l.TextWidth-ansi.StringWidth(text))
		}
	}
	aside := ""
	if l.Aside != "" {
		aside = "  " + l.Aside
	}
	note := l.Note
	if width > 0 {
		room := width - ansi.StringWidth(prefix)
		if note != "" && ansi.StringWidth(text+aside)+ansi.StringWidth(note)+2 > room {
			note = ""
		}
		if ansi.StringWidth(text+aside) > room {
			// The aside gives way first; the text only once it is gone.
			if ansi.StringWidth(text) < room {
				aside = ansi.Truncate(aside, room-ansi.StringWidth(text), ellipsis(ctx))
			} else {
				aside = ""
				text = ansi.Truncate(text, max(1, room), ellipsis(ctx))
			}
		}
	}
	gap := ""
	if note != "" && width > 0 {
		gap = strings.Repeat(" ", max(2, width-ansi.StringWidth(prefix)-ansi.StringWidth(text+aside)-ansi.StringWidth(note)))
	} else if note != "" {
		gap = "  "
	}

	textStyle := th.Base
	if l.Style != nil {
		textStyle = *l.Style
	}
	if l.Dim {
		textStyle = th.Muted
		boxStyle = th.Muted
	}

	if !l.Selected {
		row := th.Base.Render(lead+caret+" "+indent) + boxPart(boxStyle, box, l.Box) + textStyle.Render(text)
		if aside != "" {
			row += th.Muted.Render(aside)
		}
		if note != "" {
			row += gap + th.Muted.Render(note)
		}
		return row
	}

	band := th.SelBand
	row := th.SelectBar.Render(ic.SelectBar) + band.Render(lead[len(ic.SelectBar):]) +
		th.OnBand(th.Cursor).Render(caret) + band.Render(" "+indent)
	if l.Box != None {
		row += th.OnBand(boxStyle).Render(box) + band.Render(" ")
	}
	if l.Dim {
		row += th.OnBand(th.Muted).Render(text)
	} else {
		row += th.OnBand(textStyle.Bold(true)).Render(text)
	}
	if aside != "" {
		row += th.OnBand(th.Muted).Render(aside)
	}
	if note != "" {
		row += band.Render(gap) + th.OnBand(th.Muted).Render(note)
	}
	if n := width - ansi.StringWidth(prefix+text+aside+gap+note); width > 0 && n > 0 {
		row += band.Render(strings.Repeat(" ", n))
	}
	return row
}

// BoxColumns is the half-open column range the row's tick box is drawn in,
// so a screen can tell a click on the box from a click on the text.
func BoxColumns(ctx uictx.Context, l Line) (from, to int) {
	from = leftPad + ansi.StringWidth(ctx.Icons.Cursor) + 1 + max(0, l.Indent)
	w, _ := boxGlyph(ctx, l.Box)
	return from, from + ansi.StringWidth(w)
}

// boxGlyph is the glyph and colour of a tick box state.
func boxGlyph(ctx uictx.Context, b Box) (string, lipgloss.Style) {
	th := ctx.Theme
	ic := ctx.Icons
	switch b {
	case On:
		return ic.Checked, th.CheckOn
	case Some:
		return ic.Partial, th.Warning
	case Off:
		return ic.Unchecked, th.CheckOff
	case Blank:
		return strings.Repeat(" ", ansi.StringWidth(ic.Unchecked)), th.Base
	default:
		return "", th.Base
	}
}

// boxPart renders the box and the space after it, or nothing for None.
func boxPart(style lipgloss.Style, glyph string, b Box) string {
	if b == None {
		return ""
	}
	return style.Render(glyph) + " "
}

// ellipsis is the mark a cut line ends with, in the tier's spelling.
func ellipsis(ctx uictx.Context) string {
	if ctx.Icons.Tier == icons.TierASCII {
		return "..."
	}
	return "…"
}
