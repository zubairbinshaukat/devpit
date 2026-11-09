package specs

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// stripNames are the section strip's entries, in page order. A narrow
// terminal says "GPU" for Graphics so the strip fits on one line.
func stripNames(short bool) []string {
	g := "Graphics"
	if short {
		g = "GPU"
	}
	return []string{"Check", "Battery", "Memory", "Storage", "CPU", g, "Display", "Windows", "System", "Network"}
}

// stripCount is how many entries the strip has.
const stripCount = 10

// stripPad is the blank column before the first strip entry.
const stripPad = 1

// stripSpec is the strip's names and the air between two entries: the
// widest layout that fits the page. Each entry is drawn as " name ", like
// the tabs of the header.
func stripSpec(ctx uictx.Context) ([]string, int) {
	w := width(ctx)
	for _, try := range []struct {
		short bool
		gap   int
	}{{false, 2}, {false, 1}, {false, 0}, {true, 0}} {
		names := stripNames(try.short)
		n := stripPad
		for i, s := range names {
			if i > 0 {
				n += try.gap
			}
			n += ansi.StringWidth(s) + 2
		}
		if n <= w {
			return names, try.gap
		}
	}
	return stripNames(true), 0
}

// page is the laid-out screen: the fixed lines, then the scrolling body,
// with the body line each strip entry starts at.
type page struct {
	head    []string
	body    []string
	anchors []int
}

// headLines is the fixed part above the body: the title, the strip and the
// rule under it.
const headLines = 3

// viewHeight is the rows the body scrolls in.
func (m Model) viewHeight(ctx uictx.Context) int {
	h := ctx.BodyHeight - headLines
	if m.asking {
		h -= confirmRows
	}
	return max(1, h)
}

// confirmRows is the room the export question takes.
const confirmRows = 7

// wideMin is the page width from which the details sit two by two.
const wideMin = 140

// colGap is the air between the two columns of a wide page.
const colGap = 2

// width is the page width: the whole terminal, up to 200 columns.
func width(ctx uictx.Context) int { return max(40, min(ctx.Width, 200)) }

// wide reports whether the details sit side by side.
func wide(ctx uictx.Context) bool { return width(ctx) >= wideMin }

// clamp keeps the scroll inside the page.
func (m Model) clamp(ctx uictx.Context) Model {
	p := m.layout(ctx)
	m.scroll = min(max(0, m.scroll), maxScroll(p, m.viewHeight(ctx)))
	return m
}

// maxScroll is the furthest the page scrolls.
func maxScroll(p page, vh int) int { return max(0, len(p.body)-vh) }

// current is the strip entry the top of the view is in: the last one that
// starts at or above it.
func current(p page, scroll int) int {
	cur := 0
	for i, a := range p.anchors {
		if a >= 0 && a <= scroll {
			cur = i
		}
	}
	return cur
}

// active is every strip entry that starts where the current one does: on a
// wide page two sections share a row, and both are lit.
func active(p page, scroll int) map[int]bool {
	at := p.anchors[current(p, scroll)]
	out := map[int]bool{}
	for i, a := range p.anchors {
		if a == at {
			out[i] = true
		}
	}
	return out
}

// jump is the scroll for the next or previous section start.
func (m Model) jump(ctx uictx.Context, dir int) int {
	p := m.layout(ctx)
	s := min(max(0, m.scroll), maxScroll(p, m.viewHeight(ctx)))
	best := -1
	for _, a := range p.anchors {
		switch {
		case dir > 0 && a > s && (best < 0 || a < best):
			best = a
		case dir < 0 && a < s && a > best:
			best = a
		}
	}
	if best < 0 {
		if dir < 0 {
			return 0
		}
		return m.scroll
	}
	return best
}

// onClick jumps when a strip entry is clicked.
func (m Model) onClick(msg tea.MouseClickMsg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return m, nil
	}
	row := ctx.BodyRow(msg.Y)
	if m.asking {
		top := ctx.BodyHeight - confirmRows
		if row >= top {
			cctx := ctx
			cctx.Width = min(width(ctx), 90) - 2
			next, cmd := m.confirm.Click(cctx, msg.X-1, row-top-1)
			m.confirm = next
			return m, cmd
		}
		return m, nil
	}
	if row != 1 {
		return m, nil
	}
	if i, ok := stripAt(ctx, msg.X); ok {
		m.scroll = m.layout(ctx).anchors[i]
		return m.clamp(ctx), nil
	}
	return m, nil
}

// stripAt maps a column on the strip to an entry, walking the widths
// stripLine draws.
func stripAt(ctx uictx.Context, x int) (int, bool) {
	names, g := stripSpec(ctx)
	col := stripPad
	for i, n := range names {
		if i > 0 {
			col += g
		}
		w := ansi.StringWidth(n) + 2
		if x >= col && x < col+w {
			return i, true
		}
		col += w
	}
	return 0, false
}

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	m = m.clamp(ctx)
	p := m.layout(ctx)
	out := append([]string(nil), p.head...)
	vh := m.viewHeight(ctx)
	end := min(len(p.body), m.scroll+vh)
	rows := append([]string(nil), p.body[m.scroll:end]...)
	if end < len(p.body) && len(rows) > 1 {
		rows[len(rows)-1] = moreLine(ctx, len(p.body)-end+1)
	}
	out = append(out, rows...)
	if m.asking {
		for len(out) < ctx.BodyHeight-confirmRows {
			out = append(out, "")
		}
		for i, l := range out {
			out[i] = ansi.Truncate(tierText(l, ascii(ctx)), ctx.Width, "")
		}
		cctx := ctx
		cctx.Width = min(width(ctx), 90) - 2
		if ascii(ctx) {
			th := *ctx.Theme
			th.Card = th.CardASCII
			cctx.Theme = &th
		}
		out = append(out, "")
		for _, l := range strings.Split(m.confirm.View(cctx), "\n") {
			out = append(out, ansi.Truncate(" "+l, ctx.Width, ""))
		}
		if ctx.BodyHeight > 0 && len(out) > ctx.BodyHeight {
			out = out[:ctx.BodyHeight]
		}
		return strings.Join(out, "\n")
	}
	if ctx.BodyHeight > 0 && len(out) > ctx.BodyHeight {
		out = out[:ctx.BodyHeight]
	}
	for i, l := range out {
		out[i] = ansi.Truncate(tierText(l, ascii(ctx)), ctx.Width, "")
	}
	return strings.Join(out, "\n")
}

// moreLine says how much of the page is below the view.
func moreLine(ctx uictx.Context, n int) string {
	arrow := "▼"
	if ascii(ctx) {
		arrow = "v"
	}
	return "  " + ctx.Theme.Muted.Render(arrow+" "+strconv.Itoa(n)+" more lines below")
}

// block is one part of the page: the strip entry it belongs to (-1 for
// none) and what draws it.
type block struct {
	strip int
	draw  func(b *builder)
}

// layout builds every line of the page.
func (m Model) layout(ctx uictx.Context) page {
	var p page
	w := width(ctx)
	findings := sysinfo.Assess(m.rep, m.opts.Now())
	p.anchors = make([]int, stripCount)

	draw := func(cw int, f func(b *builder)) []string {
		b := &builder{ctx: ctx, m: m, w: cw}
		f(b)
		return b.lines
	}

	// The Buyer's check always spans the page.
	p.body = draw(w, func(b *builder) { b.check(findings) })
	blocks := []block{
		{-1, func(b *builder) { b.glance() }},
		{1, func(b *builder) { b.battery(findings) }},
		{2, func(b *builder) { b.memory(findings) }},
		{3, func(b *builder) { b.storage(findings) }},
		{4, func(b *builder) { b.processor(findings) }},
		{5, func(b *builder) { b.graphics() }},
		{6, func(b *builder) { b.displays() }},
		{7, func(b *builder) { b.windows(findings) }},
		{8, func(b *builder) { b.system(findings) }},
		{9, func(b *builder) { b.network() }},
	}
	if wide(ctx) {
		cw := (w - colGap) / 2
		for i := 0; i < len(blocks); i += 2 {
			left := draw(cw, blocks[i].draw)
			var right []string
			p.body = append(p.body, "")
			start := len(p.body)
			if blocks[i].strip >= 0 {
				p.anchors[blocks[i].strip] = start
			}
			if i+1 < len(blocks) {
				right = draw(w-cw-colGap, blocks[i+1].draw)
				if blocks[i+1].strip >= 0 {
					p.anchors[blocks[i+1].strip] = start
				}
			}
			for r := range max(len(left), len(right)) {
				line := ""
				if r < len(left) {
					line = left[r]
				}
				if r < len(right) {
					line = pad(line, cw+colGap) + right[r]
				}
				p.body = append(p.body, line)
			}
		}
	} else {
		for _, bl := range blocks {
			p.body = append(p.body, "")
			if bl.strip >= 0 {
				p.anchors[bl.strip] = len(p.body)
			}
			p.body = append(p.body, draw(w, bl.draw)...)
		}
	}
	p.body = append(p.body, "", " "+ctx.Theme.Muted.Render(fit(ctx, "End of the page. Read only: nothing on this PC was changed.", w-2)))

	lit := active(p, min(m.scroll, maxScroll(p, m.viewHeight(ctx))))
	p.head = []string{m.titleLine(ctx), stripLine(ctx, lit), stripRule(ctx, lit)}
	return p
}

// titleLine is the PC's name, its kind and Windows, and when it was read.
func (m Model) titleLine(ctx uictx.Context) string {
	th := ctx.Theme
	w := width(ctx)
	var right string
	switch {
	case m.reading():
		right = th.Muted.Render("reading"+ell(ctx)+" ") + th.Accent.Render(ctx.SpinnerFrame(m.spin))
	default:
		right = th.Muted.Render("read "+m.read.Format("15:04")+"  ") + ctx.KeyHint("r", "read again")
	}
	title := "This PC"
	if m.rep.Got(sysinfo.SecSystem) {
		title = m.rep.System.Title()
	}
	var sub []string
	if c := m.rep.System.Chassis.String(); c != "" {
		sub = append(sub, c)
	}
	if m.rep.Got(sysinfo.SecWindows) {
		if n := strings.TrimSpace(m.rep.Windows.Name + " " + sysinfo.EditionName(m.rep.Windows.Edition)); n != "" {
			sub = append(sub, n)
		}
	}
	room := w - 2 - ansi.StringWidth(right) - 2
	t := fit(ctx, title, room)
	left := " " + th.Title.Render(t)
	if s := strings.Join(sub, " · "); s != "" && room-ansi.StringWidth(t) > 6 {
		left += th.Muted.Render(" · " + fit(ctx, s, room-ansi.StringWidth(t)-3))
	}
	g := max(1, w-ansi.StringWidth(left)-ansi.StringWidth(right)-1)
	return left + strings.Repeat(" ", g) + right
}

// stripLine is the section strip, drawn like the header's tabs: the
// current entry on the selection band in the accent, the rest muted.
func stripLine(ctx uictx.Context, lit map[int]bool) string {
	th := ctx.Theme
	names, g := stripSpec(ctx)
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", stripPad))
	for i, n := range names {
		if i > 0 {
			b.WriteString(strings.Repeat(" ", g))
		}
		if lit[i] {
			b.WriteString(th.TabActive.Render(" " + n + " "))
		} else {
			b.WriteString(th.TabIdle.Render(" " + n + " "))
		}
	}
	return b.String()
}

// stripRule is the line under the strip: a heavy accent stroke under each
// lit entry, so the current section reads without colour too.
func stripRule(ctx uictx.Context, lit map[int]bool) string {
	th := ctx.Theme
	names, g := stripSpec(ctx)
	w := width(ctx)
	var b strings.Builder
	col := 0
	light := func(n int) {
		if n > 0 {
			b.WriteString(th.Rule.Render(strings.Repeat(ctx.Icons.TrackEmpty, n)))
			col += n
		}
	}
	at := stripPad
	for i, n := range names {
		if i > 0 {
			at += g
		}
		nw := ansi.StringWidth(n) + 2
		if lit[i] {
			light(at - col)
			b.WriteString(th.Accent.Render(strings.Repeat(ctx.Icons.Track, nw)))
			col = at + nw
		}
		at += nw
	}
	light(w - col)
	return b.String()
}
