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

// renderRow draws one line, either a project heading or an item.
func (m Model) renderRow(ctx uictx.Context, l columns, r row, cursor bool) string {
	if r.kind == rowGroup {
		return m.renderGroup(ctx, l, r.group, cursor)
	}
	return m.renderItem(ctx, l, r.item, cursor)
}

// renderGroup draws a project heading: the fold arrow, the project name, the
// project's total size and how many items it holds.
func (m Model) renderGroup(ctx uictx.Context, l columns, gi int, cursor bool) string {
	th := ctx.Theme
	g := m.groups[gi]

	fold := ctx.Icons.FolderOpen
	if m.collapsed[g.key] {
		fold = ctx.Icons.Folder
	}

	count := fmt.Sprintf("%d item", len(g.items))
	if len(g.items) != 1 {
		count += "s"
	}

	prefix := m.prefix(ctx, l, cursor, m.groupMark(ctx, g), fold)
	name := padRight(g.label, l.name)
	size := padLeft(header.FormatBytes(g.size), sizeCol)
	mid := pad(barCells + gap + riskCol)
	notes := m.notesFor(g.lastUsed, g.active, []string{count}, l.notes)

	if cursor {
		line := prefix + name + pad(gap) + size + pad(gap) + mid
		if l.notes > 0 {
			line += pad(gap) + padRight(notes, l.notes)
		}
		return th.Selected.Render(padRight(line, l.total))
	}

	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(th.Subtitle.Render(name))
	b.WriteString(pad(gap))
	b.WriteString(th.Base.Render(size))
	b.WriteString(pad(gap))
	b.WriteString(mid)
	if l.notes > 0 && notes != "" {
		b.WriteString(pad(gap))
		b.WriteString(m.notesStyle(ctx, g.active).Render(truncate(notes, l.notes)))
	}
	return b.String()
}

// renderItem draws one reclaimable item.
func (m Model) renderItem(ctx uictx.Context, l columns, idx int, cursor bool) string {
	th := ctx.Theme
	it := m.items[idx]

	mark := ctx.Icons.Unchecked
	if m.marks[idx] {
		mark = ctx.Icons.Checked
	}

	// Only the nerd tier has a folder glyph that is not the fold arrow; the
	// other tiers leave the cell blank rather than repeat the arrow.
	glyph := ""
	if ctx.Icons.Tier == icons.TierNerd {
		glyph = ctx.Icons.Folder
		if it.Kind == scan.KindLargeFile {
			glyph = ctx.Icons.FileIcon(it.Name)
		}
	}
	prefix := m.prefix(ctx, l, cursor, mark, glyph)
	name := padRight("  "+it.Name, l.name)
	size := padLeft(header.FormatBytes(it.Size), sizeCol)
	bar := m.barFor(ctx, it.Size)
	riskGlyph, riskWord, riskStyle := risk(ctx, it.Tier)
	riskText := padRight(riskGlyph+" "+riskWord, riskCol)
	notes := m.notesFor(it.LastUsed, it.Active, itemNotes(it), l.notes)

	if cursor {
		line := prefix + name + pad(gap) + size + pad(gap) + bar.plain + pad(gap) + riskText
		if l.notes > 0 {
			line += pad(gap) + padRight(notes, l.notes)
		}
		return th.Selected.Render(padRight(line, l.total))
	}

	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(th.Base.Render(name))
	b.WriteString(pad(gap))
	b.WriteString(th.Info.Render(size))
	b.WriteString(pad(gap))
	b.WriteString(bar.styled)
	b.WriteString(pad(gap))
	b.WriteString(riskStyle.Render(riskText))
	if l.notes > 0 && notes != "" {
		b.WriteString(pad(gap))
		b.WriteString(m.notesStyle(ctx, it.Active).Render(truncate(notes, l.notes)))
	}
	return b.String()
}

// prefix draws the cursor cell, the tick box and the row's glyph.
func (m Model) prefix(ctx uictx.Context, l columns, cursor bool, mark, glyph string) string {
	caret := " "
	if cursor {
		caret = ctx.Icons.Cursor
	}
	if glyph == "" {
		glyph = " "
	}
	return pad(leftPad) + caret + " " + padRight(mark, l.mark) + " " + glyph + " "
}

// groupMark is the heading's tick box: ticked when every item under it is
// ticked, empty otherwise.
func (m Model) groupMark(ctx uictx.Context, g group) string {
	for _, i := range g.items {
		if !m.marks[i] {
			return ctx.Icons.Unchecked
		}
	}
	return ctx.Icons.Checked
}

// bar is a relative size bar in both its plain and its styled form; the
// cursor row needs the plain one because it is styled as a whole line.
type bar struct {
	plain  string
	styled string
}

// barFor draws the size bar, relative to the largest item on screen.
func (m Model) barFor(ctx uictx.Context, size uint64) bar {
	full := 0
	if m.maxSize > 0 {
		full = int(float64(size)/float64(m.maxSize)*float64(barCells) + 0.5)
		if full == 0 && size > 0 {
			full = 1
		}
	}
	full = min(barCells, max(0, full))
	filled := strings.Repeat(ctx.Icons.BarFull, full)
	empty := strings.Repeat(ctx.Icons.BarEmpty, barCells-full)
	return bar{
		plain:  filled + empty,
		styled: ctx.Theme.Accent.Render(filled) + ctx.Theme.Muted.Render(empty),
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
