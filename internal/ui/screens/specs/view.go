package specs

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// stripNames are the section strip's entries, in page order.
func stripNames() []string {
	return []string{"Check", "Battery", "Memory", "Storage", "CPU", "Graphics", "Display", "Windows", "System", "Network"}
}

// page is the laid-out screen: two fixed lines, then the scrolling body,
// with the body line each strip entry starts at.
type page struct {
	head    []string
	body    []string
	anchors []int
}

// headLines is the fixed part above the body.
const headLines = 2

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

// width is the body width: the terminal, up to 100 columns.
func width(ctx uictx.Context) int { return max(40, min(ctx.Width, 100)) }

// clamp keeps the scroll inside the page.
func (m Model) clamp(ctx uictx.Context) Model {
	p := m.layout(ctx)
	m.scroll = min(max(0, m.scroll), max(0, len(p.body)-m.viewHeight(ctx)))
	return m
}

// current is the strip entry the top of the view is in.
func current(p page, scroll int) int {
	cur := 0
	for i, a := range p.anchors {
		if a >= 0 && a <= scroll {
			cur = i
		}
	}
	return cur
}

// jump is the scroll for the next or previous section.
func (m Model) jump(ctx uictx.Context, dir int) int {
	p := m.layout(ctx)
	i := current(p, m.scroll) + dir
	i = min(max(0, i), len(p.anchors)-1)
	return p.anchors[i]
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

// stripAt maps a column on the strip to an entry.
func stripAt(ctx uictx.Context, x int) (int, bool) {
	col := 2
	for i, n := range stripNames() {
		w := ansi.StringWidth(n)
		if x >= col-1 && x < col+w {
			return i, true
		}
		col += w + 1
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
	out = append(out, p.body[m.scroll:end]...)
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

// layout builds every line of the page.
func (m Model) layout(ctx uictx.Context) page {
	var p page
	p.head = []string{m.titleLine(ctx), ""}
	b := &builder{ctx: ctx, m: m, w: width(ctx)}
	findings := sysinfo.Assess(m.rep, m.opts.Now())

	p.anchors = make([]int, len(stripNames()))
	mark := func(i int) { p.anchors[i] = len(b.lines) }

	mark(0)
	b.check(findings)
	b.glance()
	mark(1)
	b.battery(findings)
	mark(2)
	b.memory(findings)
	mark(3)
	b.storage(findings)
	mark(4)
	b.processor(findings)
	mark(5)
	b.graphics()
	mark(6)
	b.displays()
	mark(7)
	b.windows(findings)
	mark(8)
	b.system(findings)
	mark(9)
	b.network()
	b.add("", " "+ctx.Theme.Muted.Render(fit(ctx, "End of the page. Read only: nothing on this PC was changed.", b.w-2)))
	p.body = b.lines
	p.head[1] = m.stripLine(ctx, current(p, min(m.scroll, max(0, len(p.body)-m.viewHeight(ctx)))))
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
	gap := max(1, w-ansi.StringWidth(left)-ansi.StringWidth(right)-1)
	return left + strings.Repeat(" ", gap) + right
}

// stripLine is the section strip with the current entry marked.
func (m Model) stripLine(ctx uictx.Context, cur int) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(" ")
	for i, n := range stripNames() {
		if i == cur {
			b.WriteString(th.Accent.Render(ctx.Icons.Cursor) + th.Selected.Render(n))
		} else {
			b.WriteString(" " + th.Muted.Render(n))
		}
	}
	return b.String()
}

// ell is the tier's ellipsis.
func ell(ctx uictx.Context) string {
	if ascii(ctx) {
		return "..."
	}
	return "…"
}

// builder collects the body lines.
type builder struct {
	ctx   uictx.Context
	m     Model
	w     int
	lines []string
}

func (b *builder) add(ls ...string) { b.lines = append(b.lines, ls...) }

// heading is "Battery ─────── right".
func (b *builder) heading(title, right string) {
	th := b.ctx.Theme
	rw := ansi.StringWidth(right)
	n := max(3, b.w-2-ansi.StringWidth(title)-1-rw-2)
	line := " " + th.Subtitle.Render(title) + " " + th.Rule.Render(strings.Repeat(b.ctx.Icons.TrackEmpty, n))
	if right != "" {
		line += " " + right
	}
	b.add("", line)
}

// state handles a section that is still reading or failed. It reports
// whether the section's own lines should follow.
func (b *builder) state(title string, secs ...sysinfo.Section) bool {
	th := b.ctx.Theme
	for _, s := range secs {
		if !b.m.rep.Have[s] {
			b.heading(title, th.Muted.Render("reading"+ell(b.ctx)+" ")+th.Accent.Render(b.ctx.SpinnerFrame(b.m.spin)))
			return false
		}
	}
	b.heading(title, "")
	for _, s := range secs {
		if e := b.m.rep.Errs[s]; e != "" && len(secs) == 1 {
			for _, l := range wrap(b.ctx, "Could not read this: "+e+".", b.w-4, 2) {
				b.add("   " + th.Warning.Render(l))
			}
			return false
		}
	}
	return true
}

// valW is the room for a value after the label.
func (b *builder) valW() int { return b.w - 3 - labelW - 1 }

// kv is one labelled row; value is already styled and fits valW.
func (b *builder) kv(label, value string) {
	b.add("   " + b.ctx.Theme.Muted.Render(pad(fit(b.ctx, label, labelW), labelW)) + " " + value)
}

// text is one labelled row of plain text, cut to fit, or "not reported".
func (b *builder) text(label, value string) {
	if strings.TrimSpace(value) == "" {
		b.kv(label, b.nr())
		return
	}
	b.kv(label, b.ctx.Theme.Base.Render(fit(b.ctx, value, b.valW())))
}

// nr is the muted "not reported by this PC".
func (b *builder) nr() string {
	return b.ctx.Theme.Muted.Render(fit(b.ctx, sysinfo.NotReported, b.valW()))
}

// note is a muted line under the rows, wrapped.
func (b *builder) note(s string) {
	for _, l := range wrap(b.ctx, s, b.w-4, 3) {
		b.add("   " + b.ctx.Theme.Muted.Render(l))
	}
}

// flag is one finding: glyph, word and text, wrapped with a hanging indent.
func (b *builder) flag(f sysinfo.Finding, indent int, area bool, maxLines int) {
	th := b.ctx.Theme
	st := levelStyle(b.ctx, f.Level)
	text := f.Text
	if f.Thumb {
		text += " (rule of thumb)"
	}
	lead := strings.Repeat(" ", indent) + st.Render(levelGlyph(b.ctx, f.Level)) + " "
	leadW := indent + 2
	if area {
		lead += th.Base.Render(pad(f.Area, 10))
		leadW += 10
	}
	ls := wrap(b.ctx, text, b.w-leadW-1, maxLines)
	for i, l := range ls {
		if i == 0 {
			b.add(lead + th.Base.Render(l))
			continue
		}
		b.add(strings.Repeat(" ", leadW) + th.Base.Render(l))
	}
}

// areaFindings shows a section's own flags and notes at its end.
func (b *builder) areaFindings(fs []sysinfo.Finding, area string) {
	for _, f := range fs {
		if f.Area == area && f.Level != sysinfo.Good {
			b.flag(f, 3, false, 3)
		}
	}
}

// check is the Buyer's check.
func (b *builder) check(fs []sysinfo.Finding) {
	th, ctx := b.ctx.Theme, b.ctx
	look, fine := sysinfo.Counts(fs)
	right := th.Muted.Render(fmt.Sprintf("%d to look at · %d fine", look, fine))
	if b.m.reading() {
		right = th.Muted.Render("filling in"+ell(ctx)+" ") + th.Accent.Render(ctx.SpinnerFrame(b.m.spin))
	}
	b.heading("Buyer's check", right)
	limit := 2
	if ctx.Height >= 30 {
		limit = 3
	}
	shown := 0
	for _, f := range fs {
		if f.Level < sysinfo.Warn {
			continue
		}
		if shown == limit {
			b.add("   " + th.Muted.Render(fit(ctx, fmt.Sprintf("+%d more, each in its section below", look-shown), b.w-4)))
			break
		}
		b.flag(f, 1, true, 2)
		shown++
	}
	if look == 0 && !b.m.reading() {
		b.add(" " + th.Success.Render(ctx.Icons.Tick) + " " + th.Base.Render(fit(ctx, "Nothing stands out. The details are below.", b.w-4)))
	}
	// One line of ticks.
	var chips []string
	for _, f := range fs {
		if f.Level == sysinfo.Good && f.Chip != "" {
			chips = append(chips, f.Chip)
		}
	}
	if len(chips) > 0 {
		line, used := " ", 1
		for i, c := range chips {
			piece := ctx.Icons.Tick + " " + c
			sep := 0
			if i > 0 {
				sep = 3
			}
			more := fmt.Sprintf(" +%d", len(chips)-i)
			if used+sep+ansi.StringWidth(tierText(piece, ascii(ctx)))+len(more) > b.w-1 && i < len(chips) {
				line += th.Muted.Render(more)
				break
			}
			if i > 0 {
				line += th.Muted.Render(" · ")
			}
			line += th.Success.Render(ctx.Icons.Tick) + " " + th.Base.Render(c)
			used += sep + ansi.StringWidth(tierText(piece, ascii(ctx)))
		}
		b.add(line)
	}
	b.add(" " + th.Muted.Render(fit(ctx, "Rules of thumb are marked; missing data is never judged.", b.w-2)))
}

// glance is the at-a-glance list.
func (b *builder) glance() {
	th := b.ctx.Theme
	b.heading("At a glance", "")
	got := map[string]string{}
	for _, l := range sysinfo.Glance(b.m.rep) {
		got[l.Label] = l.Value
	}
	for _, row := range []struct {
		label string
		sec   sysinfo.Section
	}{
		{"Processor", sysinfo.SecCPU},
		{"Memory", sysinfo.SecMemory},
		{"Storage", sysinfo.SecDisks},
		{"Graphics", sysinfo.SecGraphics},
		{"Display", sysinfo.SecDisplays},
		{"Battery", sysinfo.SecBattery},
		{"Windows", sysinfo.SecWindows},
	} {
		switch {
		case !b.m.rep.Have[row.sec]:
			b.kv(row.label, th.Muted.Render("reading"+ell(b.ctx)))
		case b.m.rep.Errs[row.sec] != "" && got[row.label] == sysinfo.NotReported:
			b.kv(row.label, th.Muted.Render(fit(b.ctx, "could not read", b.valW())))
		default:
			b.text(row.label, got[row.label])
		}
	}
}

// battery is the Battery section.
func (b *builder) battery(fs []sysinfo.Finding) {
	th, ctx := b.ctx.Theme, b.ctx
	if !b.state("Battery", sysinfo.SecBattery) {
		return
	}
	bt := b.m.rep.Battery
	switch bt.Present {
	case sysinfo.TriNo:
		if b.m.rep.System.Chassis.Portable() {
			b.flag(sysinfo.Finding{Level: sysinfo.Bad, Text: "No battery found, though this is a " + b.m.rep.System.Chassis.String() + "."}, 3, false, 2)
		} else {
			b.add("   " + th.Base.Render(fit(ctx, "No battery: this looks like a desktop.", b.w-4)))
		}
		return
	case sysinfo.TriUnknown:
		b.text("Battery", "")
		return
	}
	if pct, ok := bt.Health(); ok {
		word, lvl := healthWord(pct)
		gw := min(20, b.valW()-16)
		b.kv("Health", meter(ctx, float64(pct)/100, gw, lvl, word))
	} else {
		b.text("Health", "")
	}
	design, full, rel := bt.Totals()
	switch {
	case rel:
		b.text("Capacity", "shown as a share only: this battery does not report watt-hours")
	case design > 0:
		b.text("Capacity", fmt.Sprintf("%.1f Wh now · %.1f Wh when new", float64(full)/1000, float64(design)/1000))
	}
	if n, ok := bt.Cycles(); ok {
		b.text("Cycles", commas(int64(n))+" charge cycles")
	} else {
		b.text("Cycles", "")
	}
	b.text("Charge", chargeText(bt))
	for i, p := range bt.Packs {
		label := "Battery"
		if len(bt.Packs) > 1 {
			label = "Battery " + strconv.Itoa(i+1)
		}
		desc := strings.TrimSpace(strings.Join(nonEmpty(p.Maker, p.Name, p.Chemistry), " · "))
		if ctx.Width >= 100 && p.Serial != "" {
			desc += " · serial " + p.Serial
		}
		b.text(label, desc)
	}
	b.areaFindings(fs, "Battery")
}

// chargeText is "94% · on AC, charging · full in 40 min".
func chargeText(bt sysinfo.Battery) string {
	var p []string
	if bt.ChargePct >= 0 {
		p = append(p, strconv.Itoa(bt.ChargePct)+"%")
	}
	switch {
	case bt.Charging == sysinfo.TriYes:
		p = append(p, "charging")
	case bt.OnAC == sysinfo.TriYes:
		p = append(p, "plugged in")
	case bt.OnAC == sysinfo.TriNo:
		p = append(p, "on battery")
	}
	if bt.TimeToFull > 0 {
		p = append(p, "full in "+sysinfo.Duration(bt.TimeToFull))
	}
	if bt.TimeLeft > 0 {
		p = append(p, "about "+sysinfo.Duration(bt.TimeLeft)+" left")
	}
	return strings.Join(p, " · ")
}

// nonEmpty drops empty strings.
func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// memory is the Memory section: the slots as shapes, one row per slot, and
// what is in use.
func (b *builder) memory(fs []sysinfo.Finding) {
	th, ctx := b.ctx.Theme, b.ctx
	if !b.state("Memory", sysinfo.SecMemory) {
		return
	}
	m := b.m.rep.Memory
	fitted, free, soldered := m.Fitted()
	totals := strings.Join(nonEmpty(installed(m.InstalledB), usable(m.UsableB)), ", ")
	switch {
	case len(m.Slots) == 0:
		b.kv("Slots", b.nr())
		if totals != "" {
			b.text("Installed", totals)
		}
	case soldered && fitted+free == 0:
		s := m.Slots[0]
		v := th.Accent.Render(slotGlyph(ctx, true)) + " " + th.Base.Render(fit(ctx, "soldered "+s.ModuleText()+" on the board, cannot be upgraded", b.valW()-2))
		b.kv("Slots", v)
		if totals != "" {
			b.text("Installed", totals)
		}
	default:
		var shapes []string
		for _, s := range m.Slots {
			if s.Soldered {
				continue
			}
			if s.Empty {
				shapes = append(shapes, th.Muted.Render(ctx.Icons.SlotEmpty))
			} else {
				shapes = append(shapes, th.Accent.Render(ctx.Icons.SlotFull))
			}
		}
		lead := strings.Join(shapes, " ")
		leadW := 2*len(shapes) - 1
		summary := fmt.Sprintf("%d of %d used", fitted, fitted+free)
		if totals != "" {
			summary += " · " + totals
		}
		b.kv("Slots", lead+"  "+th.Base.Render(fit(ctx, summary, b.valW()-leadW-2)))
		n := 0
		for _, s := range m.Slots {
			n++
			label := "Slot " + strconv.Itoa(n)
			if s.Soldered {
				label = "On board"
				n--
			}
			b.slotRow(label, s)
		}
	}
	if m.UsableB > 0 && m.InUseB > 0 {
		bw := min(16, b.valW()-24)
		v := bar(ctx, float64(m.InUseB)/float64(m.UsableB), bw) + " " + th.Base.Render(fit(ctx, inUse(m), b.valW()-barW(ctx, bw)-1))
		b.kv("In use", v)
	}
	b.areaFindings(fs, "Memory")
}

// slotRow is one slot.
func (b *builder) slotRow(label string, s sysinfo.Module) {
	th, ctx := b.ctx.Theme, b.ctx
	if s.Empty {
		b.kv(label, th.Muted.Render(ctx.Icons.SlotEmpty)+" "+th.Muted.Render(fit(ctx, "empty, room for one more stick", b.valW()-2)))
		return
	}
	desc := s.ModuleText()
	if s.Soldered {
		desc += " soldered"
	}
	extra := strings.Join(nonEmpty(s.Maker, s.Part), " ")
	if s.ConfiguredMTs > 0 && s.SpeedMTs > 0 && s.ConfiguredMTs < s.SpeedMTs {
		desc += " (runs at " + strconv.Itoa(s.ConfiguredMTs) + ")"
	}
	if extra != "" {
		desc += " · " + extra
	}
	b.kv(label, th.Accent.Render(slotGlyph(ctx, s.Soldered))+" "+th.Base.Render(fit(ctx, desc, b.valW()-2)))
}

// slotGlyph is the shape of a fitted slot; soldered memory gets a small
// square.
func slotGlyph(ctx uictx.Context, soldered bool) string {
	if soldered && !ascii(ctx) {
		return "▪"
	}
	return ctx.Icons.SlotFull
}

func installed(b uint64) string {
	if b == 0 {
		return ""
	}
	return sysinfo.RAMSize(b) + " installed"
}

func usable(b uint64) string {
	if b == 0 {
		return ""
	}
	return sysinfo.RAMSize(b) + " usable"
}

// inUse is "9.8 of 15.7 GB in use now".
func inUse(m sysinfo.Memory) string {
	g := func(v uint64) string { return strconv.FormatFloat(float64(v)/(1<<30), 'f', 1, 64) }
	return g(m.InUseB) + " of " + g(m.UsableB) + " GB in use now"
}
