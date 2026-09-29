package restable

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Column widths. The name and the age column flex; everything else is fixed
// so the size figures and the risk shapes line up down the table.
const (
	sizeCol  = 9
	barCells = 10
	riskCol  = 9 // "▲ Careful"
	notesMax = 26
	gap      = 3
	// leftPad is the margin before the cursor cell, matching the menus.
	leftPad = 2
)

// View renders the visible window of the table.
//
// Nothing outside the window is formatted: a scan can find thousands of
// items and the table draws at most [Model.SetSize]'s worth of rows.
func (m Model) View(ctx uictx.Context) string {
	if ctx.Width < uictx.MinWidth || ctx.Height < uictx.MinHeight {
		return resizeNotice(ctx)
	}

	l := m.layout(ctx)
	var b strings.Builder

	b.WriteString(m.heading(ctx, l))

	if len(m.rows) == 0 {
		b.WriteString("\n")
		b.WriteString(ctx.Theme.Muted.Render(m.emptyText()))
		if line := m.statusLine(ctx); line != "" {
			b.WriteString("\n")
			b.WriteString(line)
		}
		return b.String()
	}

	h := m.bodyHeight()
	end := min(len(m.rows), m.top+h)
	for i := m.top; i < end; i++ {
		b.WriteString("\n")
		b.WriteString(m.renderRow(ctx, l, m.rows[i], i == m.cursor))
	}

	if line := m.statusLine(ctx); line != "" {
		b.WriteString("\n")
		b.WriteString(line)
	}
	return b.String()
}

// columns holds the computed width of every column for one frame.
type columns struct {
	mark  int
	name  int
	notes int
	total int
}

// layout computes the column widths for the current terminal width.
func (m Model) layout(ctx uictx.Context) columns {
	markW := cellWidth(ctx.Icons.Unchecked)
	prefix := leftPad + 2 + markW + 1 + 1 + 1 // margin, cursor, space, mark, space, icon, space
	tail := gap + sizeCol + gap + barCells + gap + riskCol

	width := m.width
	if width <= 0 {
		width = ctx.Width
	}
	avail := width - prefix - tail

	notes := min(notesMax, max(0, avail-20))
	if notes > 0 {
		avail -= notes + gap
	}
	return columns{mark: markW, name: max(6, avail), notes: notes, total: width}
}

// heading renders the muted column titles.
func (m Model) heading(ctx uictx.Context, l columns) string {
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", leftPad+2+l.mark+1+1+1))
	b.WriteString(padRight("NAME", l.name))
	b.WriteString(strings.Repeat(" ", gap))
	b.WriteString(padLeft("SIZE", sizeCol))
	b.WriteString(strings.Repeat(" ", gap+barCells+gap))
	b.WriteString(padRight("RISK", riskCol))
	if l.notes > 0 {
		b.WriteString(strings.Repeat(" ", gap))
		b.WriteString(padRight("LAST USED", l.notes))
	}
	return ctx.Theme.Muted.Render(strings.TrimRight(b.String(), " "))
}

// renderRow draws one line, either a heading or an item.
func (m Model) renderRow(ctx uictx.Context, l columns, r row, cursor bool) string {
	if r.kind == rowGroup {
		return m.renderGroup(ctx, l, r, cursor)
	}
	return m.renderItem(ctx, l, r, cursor)
}

// seg is one piece of a row: plain text and the style it is drawn in. A row
// is kept as pieces until the last moment because the cursor row draws every
// piece on the selection band, and a piece rendered on its own would end
// with a reset that punches a hole in it.
type seg struct {
	text string
	// style is the piece's own style; nil draws it unstyled.
	style *lipgloss.Style
	// pre, when set, is the piece already rendered for an ordinary row. The
	// tick box uses it so ordinary rows go through [uictx.Context.Checkbox],
	// the one place a tick box's look is decided.
	pre string
}

// line is a row under construction.
type line []seg

// add appends a styled piece.
func (ln *line) add(text string, style lipgloss.Style) {
	*ln = append(*ln, seg{text: text, style: &style})
}

// plain appends an unstyled piece.
func (ln *line) plain(text string) { *ln = append(*ln, seg{text: text}) }

// render draws the row. An ordinary row is its pieces after the left margin,
// as long as they are. The cursor row puts the selection bar glyph in the
// margin's first cell, draws every piece on the band in its own colour, and
// fills the band out to exactly the table's width.
func (ln line) render(ctx uictx.Context, cursor bool, total int) string {
	th := ctx.Theme
	var b strings.Builder
	if !cursor {
		b.WriteString(pad(leftPad))
		for _, s := range ln {
			switch {
			case s.pre != "":
				b.WriteString(s.pre)
			case s.text == "":
			case s.style == nil:
				b.WriteString(s.text)
			default:
				b.WriteString(s.style.Render(s.text))
			}
		}
		return b.String()
	}

	barGlyph := ctx.Icons.SelectBar
	used := cellWidth(barGlyph)
	b.WriteString(th.SelectBar.Render(barGlyph))
	if n := leftPad - used; n > 0 {
		b.WriteString(th.SelBand.Render(pad(n)))
		used += n
	}
	for _, s := range ln {
		if s.text == "" {
			continue
		}
		used += ansi.StringWidth(s.text)
		if s.style == nil {
			b.WriteString(th.SelBand.Render(s.text))
			continue
		}
		b.WriteString(th.OnBand(*s.style).Render(s.text))
	}
	if used < total {
		b.WriteString(th.SelBand.Render(pad(total - used)))
	}
	return b.String()
}

// renderGroup draws a heading: the fold arrow, the label, the total size and
// what the heading holds.
func (m Model) renderGroup(ctx uictx.Context, l columns, r row, cursor bool) string {
	th := ctx.Theme
	g := m.node(r)

	fold := ctx.Icons.FolderOpen
	if m.collapsed[g.key] {
		fold = ctx.Icons.Folder
	}

	count := countWord(len(g.items), "item")
	if len(g.kids) > 0 {
		count = countWord(len(g.kids), "project")
	}

	ln := m.prefix(ctx, l, r, cursor, m.ticked(g), len(g.items), fold)
	name := fitLabel(g.label, l.name-indent(r))
	ln.add(name.dir, th.Muted)
	ln.add(name.base, th.Subtitle)
	ln.plain(pad(gap))
	ln.add(padLeft(header.FormatBytes(g.size), sizeCol), th.Base)
	ln.plain(pad(gap + barCells + gap + riskCol))
	m.addNotes(ctx, &ln, l, m.notesFor(g.lastUsed, g.active, []string{count}, l.notes), g.active, cursor)
	return ln.render(ctx, cursor, l.total)
}

// renderItem draws one reclaimable item.
func (m Model) renderItem(ctx uictx.Context, l columns, r row, cursor bool) string {
	th := ctx.Theme
	idx := r.item
	it := m.items[idx]

	// Only the nerd tier has a folder glyph that is not the fold arrow; the
	// other tiers leave the cell blank rather than repeat the arrow.
	glyph := ""
	if ctx.Icons.Tier == icons.TierNerd {
		glyph = ctx.Icons.Folder
		if it.Kind == scan.KindLargeFile {
			glyph = ctx.Icons.FileIcon(it.Name)
		}
	}
	ticked := 0
	if m.marks[idx] {
		ticked = 1
	}
	ln := m.prefix(ctx, l, r, cursor, ticked, 1, glyph)

	// A plain project's items step in two cells under its heading; under a
	// repository the indent has already done that.
	name := it.Name
	if r.sub < 0 {
		name = "  " + name
	}
	ln.add(padRight(name, l.name-indent(r)), th.Base)
	ln.plain(pad(gap))
	ln.add(padLeft(header.FormatBytes(it.Size), sizeCol), th.Info)
	ln.plain(pad(gap))
	m.addBar(ctx, &ln, it.Size)
	ln.plain(pad(gap))
	riskGlyph, riskWord, riskStyle := risk(ctx, it.Tier)
	ln.add(padRight(riskGlyph+" "+riskWord, riskCol), riskStyle)
	m.addNotes(ctx, &ln, l, m.notesFor(it.LastUsed, it.Active, itemNotes(it), l.notes), it.Active, cursor)
	return ln.render(ctx, cursor, l.total)
}

// addNotes appends the last-used column. An ordinary row stops at the end of
// its text; the cursor row pads it so the band runs on to the edge.
func (m Model) addNotes(ctx uictx.Context, ln *line, l columns, notes string, active, cursor bool) {
	if l.notes <= 0 || notes == "" {
		return
	}
	ln.plain(pad(gap))
	text := truncate(notes, l.notes)
	if cursor {
		text = padRight(notes, l.notes)
	}
	ln.add(text, m.notesStyle(ctx, active))
}

// prefix starts a row: the caret, the indent, the tick box and the row's
// glyph. The margin before the caret is added by [line.render], because the
// cursor row draws its selection bar there.
func (m Model) prefix(ctx uictx.Context, l columns, r row, cursor bool, ticked, total int, glyph string) line {
	th := ctx.Theme
	ln := make(line, 0, 16)
	if cursor {
		ln.add(ctx.Icons.Cursor, th.Cursor)
	} else {
		ln.plain(" ")
	}
	ln.plain(" " + pad(indent(r)))

	boxGlyph, boxStyle := checkbox(ctx, ticked, total)
	fill := pad(l.mark - cellWidth(boxGlyph))
	ln = append(ln, seg{text: boxGlyph, style: &boxStyle, pre: ctx.CheckboxState(ticked, total)})
	if glyph == "" {
		glyph = " "
	}
	ln.plain(fill + " " + glyph + " ")
	return ln
}

// checkbox is the glyph and style [uictx.Context.CheckboxState] draws, taken
// apart so the cursor row can put the same box on the selection band.
func checkbox(ctx uictx.Context, ticked, total int) (string, lipgloss.Style) {
	th := ctx.Theme
	switch {
	case total > 0 && ticked >= total:
		return ctx.Icons.Checked, th.CheckOn
	case ticked > 0:
		return ctx.Icons.Partial, th.Warning
	default:
		return ctx.Icons.Unchecked, th.CheckOff
	}
}

// countWord is "1 item", "3 items", "4 projects".
func countWord(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// addBar appends the relative size bar, scaled to the largest item on
// screen: the filled part in the accent, the rest in the rule colour, which
// is quieter than muted text so a column of bars reads as shapes rather than
// as a second column of text.
func (m Model) addBar(ctx uictx.Context, ln *line, size uint64) {
	full := 0
	if m.maxSize > 0 {
		full = int(float64(size)/float64(m.maxSize)*float64(barCells) + 0.5)
		if full == 0 && size > 0 {
			full = 1
		}
	}
	full = min(barCells, max(0, full))
	if full > 0 {
		ln.add(strings.Repeat(ctx.Icons.BarFull, full), ctx.Theme.Accent)
	}
	if full < barCells {
		ln.add(strings.Repeat(ctx.Icons.BarEmpty, barCells-full), ctx.Theme.Rule)
	}
}

// risk returns the shape, the word and the style for a tier. Risk is never
// carried by colour alone: the shape and the word say it too.
func risk(ctx uictx.Context, t scan.Tier) (glyph, word string, style lipgloss.Style) {
	switch t {
	case scan.TierCareful:
		return ctx.Icons.Careful, "Careful", ctx.Theme.Danger
	case scan.TierReview:
		return ctx.Icons.Review, "Review", ctx.Theme.Warning
	case scan.TierSafe:
		return ctx.Icons.Safe, "Safe", ctx.Theme.Success
	default:
		return ctx.Icons.Review, "Review", ctx.Theme.Warning
	}
}

// itemNotes returns the flags worth printing beside an item.
func itemNotes(it scan.Item) []string {
	var out []string
	if it.Unverified {
		out = append(out, "unverified")
	}
	if it.Cloud {
		out = append(out, "cloud")
	}
	if it.HardLinkedToStore {
		out = append(out, "hard-linked to pnpm store")
	}
	return out
}

// notesFor builds the age column: an "active" badge for a project in use, a
// relative last-used time otherwise, then any flags, truncated to fit.
func (m Model) notesFor(lastUsed time.Time, active bool, extra []string, width int) string {
	if width <= 0 {
		return ""
	}
	parts := make([]string, 0, 1+len(extra))
	switch {
	case active:
		parts = append(parts, "active")
	case !lastUsed.IsZero():
		parts = append(parts, relTime(lastUsed, m.clock()))
	}
	parts = append(parts, extra...)
	return strings.Join(parts, " · ")
}

// notesStyle tints the age column: an active project is a warning, because
// it is the one thing the user should not be deleting.
func (m Model) notesStyle(ctx uictx.Context, active bool) lipgloss.Style {
	if active {
		return ctx.Theme.Warning
	}
	return ctx.Theme.Muted
}

// statusLine renders the filter input, or the filters currently in force.
func (m Model) statusLine(ctx uictx.Context) string {
	th := ctx.Theme
	if m.filtering {
		return themedInput(m.input, th).View()
	}
	var parts []string
	if m.filter != "" {
		parts = append(parts, "filter: "+m.filter)
	}
	if m.olderOnly {
		parts = append(parts, fmt.Sprintf("older than %d days", m.olderDays))
	}
	if len(parts) == 0 {
		return ""
	}
	parts = append(parts, "sorted by "+m.sortMode.String())
	return th.Warning.Render(strings.Join(parts, "  ·  "))
}

// emptyText is what the table says when it has nothing to show, which is
// never simply blank.
func (m Model) emptyText() string {
	switch {
	case len(m.items) == 0:
		return "Nothing found yet."
	case m.filter != "" || m.olderOnly:
		return "Nothing matches these filters. Press / or o to clear them."
	default:
		return "Nothing found."
	}
}

// resizeNotice is what the table draws instead of a garbled layout in a
// terminal too small to hold it.
func resizeNotice(ctx uictx.Context) string {
	return ctx.Theme.Notice.Render(fmt.Sprintf(
		"This table needs a bigger window.\nNow: %d×%d   Needs: %d×%d",
		ctx.Width, ctx.Height, uictx.MinWidth, uictx.MinHeight,
	))
}

// relTime renders how long ago a moment was, in the table's shorthand.
func relTime(then, now time.Time) string {
	if then.IsZero() {
		return ""
	}
	d := now.Sub(then)
	const day = 24 * time.Hour
	switch {
	case d < 0, d < day:
		return "today"
	case d < 2*day:
		return "yesterday"
	case d < 14*day:
		return fmt.Sprintf("%d d ago", int(d/day))
	case d < 60*day:
		return fmt.Sprintf("%d wk ago", int(d/(7*day)))
	case d < 365*day:
		return fmt.Sprintf("%d mo ago", int(d/(30*day)))
	default:
		return fmt.Sprintf("%d yr ago", int(d/(365*day)))
	}
}

// pad returns n spaces.
func pad(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// truncate cuts plain text to at most w cells, marking the cut.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return s
}

// padRight truncates or pads plain text to exactly w cells.
func padRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if n := ansi.StringWidth(s); n > w {
		return ansi.Truncate(s, w, "…")
	} else if n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// padLeft truncates or right-aligns plain text in exactly w cells.
func padLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if n := ansi.StringWidth(s); n > w {
		return ansi.Truncate(s, w, "…")
	} else if n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

// cellWidth is the printed width of a glyph. Most are one cell; the unicode
// and ascii tick boxes are three.
func cellWidth(s string) int {
	if s == "" {
		return 0
	}
	return ansi.StringWidth(s)
}
