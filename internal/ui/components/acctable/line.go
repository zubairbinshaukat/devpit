package acctable

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// band is how a line sits on the selection highlight.
type band int

const (
	// bandNone is an ordinary line: its pieces in their own colours, and
	// nothing after the last of them.
	bandNone band = iota
	// bandFull is a line of the selected row: every piece on the highlight,
	// filled out to the table's edge.
	bandFull
	// bandSoft is a secondary line under the selected row, such as its
	// caption: the band where the band is a colour, and nothing on mono,
	// where a second reverse-video line would turn the selection into a slab.
	bandSoft
)

// seg is one piece of a line: plain text and the style it is drawn in. A
// line is kept as pieces until the last moment because the selected row
// draws every piece on the band, and a piece rendered on its own would end
// with a reset that punches a hole in it.
type seg struct {
	text  string
	style lipgloss.Style
	// plain pieces have no style of their own: spaces and padding.
	plain bool
	// raw pieces are drawn in exactly their style, band or not. The
	// selection bar is one: its style already carries the band.
	raw bool
}

// line is a row under construction. w tracks its width so far, in cells.
type line struct {
	segs []seg
	w    int
}

// add appends a styled piece.
func (ln *line) add(text string, st lipgloss.Style) {
	if text == "" {
		return
	}
	ln.segs = append(ln.segs, seg{text: text, style: st})
	ln.w += width(text)
}

// raw appends a piece drawn in exactly its style.
func (ln *line) raw(text string, st lipgloss.Style) {
	if text == "" {
		return
	}
	ln.segs = append(ln.segs, seg{text: text, style: st, raw: true})
	ln.w += width(text)
}

// space appends n blank cells.
func (ln *line) space(n int) {
	if n <= 0 {
		return
	}
	ln.segs = append(ln.segs, seg{text: pad(n), plain: true})
	ln.w += n
}

// to pads the line out to column x. A line already past x is left alone.
func (ln *line) to(x int) { ln.space(x - ln.w) }

// lead starts a line with the gutter: the selection bar and the caret on
// the selected row, a quiet caret on the row under the mouse pointer, blank
// otherwise. caret false draws the bar alone, for a selected row's second
// line.
func lead(ctx uictx.Context, selected, hovered, caret bool) line {
	th := ctx.Theme
	ic := ctx.Icons
	var ln line
	switch {
	case selected:
		ln.raw(ic.SelectBar, th.SelectBar)
		ln.space(leftPad - width(ic.SelectBar))
		if caret {
			ln.add(ic.Cursor, th.Cursor)
		}
	case hovered && caret:
		ln.space(leftPad)
		ln.add(ic.Cursor, th.Muted)
	}
	ln.to(leadW)
	return ln
}

// render draws the line into at most total cells. Nothing is ever drawn past
// total, whatever the pieces add up to: a piece that would cross the edge is
// cut there, which is the last line of defence for a terminal narrower than
// the layout's own floors.
func (ln line) render(ctx uictx.Context, b band, total int) string {
	th := ctx.Theme
	segs := ln.segs
	if b == bandNone {
		for len(segs) > 0 && segs[len(segs)-1].plain {
			segs = segs[:len(segs)-1]
		}
	}
	mono := th.Variant == theme.VariantMono
	var sb strings.Builder
	used := 0
	for _, s := range segs {
		text := s.text
		if total > 0 && used+width(text) > total {
			text = ansi.Truncate(text, total-used, "")
		}
		if text == "" {
			continue
		}
		used += width(text)
		switch {
		case s.raw:
			sb.WriteString(s.style.Render(text))
		case b == bandNone || (b == bandSoft && mono):
			if s.plain {
				sb.WriteString(text)
			} else {
				sb.WriteString(s.style.Render(text))
			}
		case s.plain:
			sb.WriteString(th.SelBand.Render(text))
		default:
			sb.WriteString(th.OnBand(s.style).Render(text))
		}
	}
	if b != bandNone && total > used && (b == bandFull || !mono) {
		sb.WriteString(th.SelBand.Render(pad(total - used)))
	}
	return sb.String()
}
