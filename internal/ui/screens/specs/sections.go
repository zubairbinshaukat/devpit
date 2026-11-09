package specs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// gaugeFor is the width of a gauge that leaves room for text beside it.
func (b *builder) gaugeFor(text int) int { return min(max(b.valW()-text, 8), 30) }

// battery is the Battery section.
func (b *builder) battery(fs []sysinfo.Finding) {
	th, ctx := b.ctx.Theme, b.ctx
	bt := b.m.rep.Battery
	right := func() string {
		if pct, ok := bt.Health(); ok && bt.Present == sysinfo.TriYes {
			word, lvl := healthWord(pct)
			return mark(ctx, lvl, word)
		}
		return ""
	}
	if !b.state("Battery", right, sysinfo.SecBattery) {
		return
	}
	switch bt.Present {
	case sysinfo.TriNo:
		if b.m.rep.System.Chassis.Portable() {
			b.areaFindings([]sysinfo.Finding{{Level: sysinfo.Bad, Area: "Battery", Text: "No battery found, though this is a " + b.m.rep.System.Chassis.String() + "."}}, "Battery")
		} else {
			b.add("   " + th.Base.Render(fit(ctx, "No battery: this looks like a desktop.", b.w-5)))
		}
		return
	case sysinfo.TriUnknown:
		b.text("Battery", "")
		return
	}
	if pct, ok := bt.Health(); ok {
		word, lvl := healthWord(pct)
		st := levelStyle(ctx, lvl)
		label := itoa(pct) + "% " + word
		gw := b.gaugeFor(len(label) + 2)
		b.kv("Health", gauge(ctx, float64(pct)/100, gw, st)+"  "+st.Bold(true).Render(fit(ctx, label, b.valW()-gaugeW(ctx, gw)-2)))
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
		label := "Pack"
		if len(bt.Packs) > 1 {
			label = "Pack " + strconv.Itoa(i+1)
		}
		name := p.Name
		if strings.EqualFold(name, p.Maker) {
			name = ""
		}
		parts := nonEmpty(p.Maker, name, p.Chemistry)
		if ctx.Width >= 100 && p.Serial != "" {
			parts = append(parts, "serial "+p.Serial)
		}
		b.text(label, strings.Join(parts, " · "))
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

// memory is the Memory section: the slots as a row of boxes, a table of
// the sticks, and what is in use.
func (b *builder) memory(fs []sysinfo.Finding) {
	th, ctx := b.ctx.Theme, b.ctx
	m := b.m.rep.Memory
	right := func() string {
		if m.InstalledB > 0 {
			return th.Muted.Render(sysinfo.RAMSize(m.InstalledB))
		}
		return ""
	}
	if !b.state("Memory", right, sysinfo.SecMemory) {
		return
	}
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
		v := th.Accent.Render(slotGlyph(ctx, true)) + gap(ctx) + th.Base.Render(fit(ctx, "soldered "+s.ModuleText()+" on the board, cannot be upgraded", b.valW()-3))
		b.kv("Slots", v)
		if totals != "" {
			b.text("Installed", totals)
		}
	default:
		var shapes []string
		for _, s := range m.Slots {
			switch {
			case s.Soldered:
			case s.Empty:
				shapes = append(shapes, th.Muted.Render(ctx.Icons.SlotEmpty))
			default:
				shapes = append(shapes, th.Accent.Render(ctx.Icons.SlotFull))
			}
		}
		lead := strings.Join(shapes, " ")
		leadW := ansi.StringWidth(lead)
		summary := fmt.Sprintf("%d of %d used", fitted, fitted+free)
		if totals != "" {
			summary += " · " + totals
		}
		b.kv("Slots", lead+"  "+th.Base.Render(fit(ctx, summary, b.valW()-leadW-2)))
		b.slotTable(m.Slots)
	}
	if m.UsableB > 0 && m.InUseB > 0 {
		text := inUse(m)
		gw := b.gaugeFor(len(text) + 2)
		v := gauge(ctx, float64(m.InUseB)/float64(m.UsableB), gw, th.Accent) + "  " + th.Base.Render(fit(ctx, text, b.valW()-gaugeW(ctx, gw)-2))
		b.kv("In use", v)
	}
	b.areaFindings(fs, "Memory")
}

// slotTable is one aligned row per slot under a header: slot, size, type,
// speed, form factor, maker and part number. A narrow page cuts the part
// number first, then the maker.
func (b *builder) slotTable(slots []sysinfo.Module) {
	th, ctx := b.ctx.Theme, b.ctx
	head := []string{"SLOT", "SIZE", "TYPE", "SPEED", "FORM", "MAKER", "PART"}
	type line struct {
		cells []string
		empty bool
		glyph string
	}
	var rows []line
	n := 0
	for _, s := range slots {
		n++
		name := strconv.Itoa(n)
		glyph := ctx.Icons.SlotFull
		if s.Soldered {
			name, glyph = "board", slotGlyph(ctx, true)
			n--
		}
		if s.Empty {
			rows = append(rows, line{cells: []string{name}, empty: true, glyph: ctx.Icons.SlotEmpty})
			continue
		}
		speed := ""
		if s.SpeedMTs > 0 {
			speed = strconv.Itoa(s.SpeedMTs) + " MT/s"
			if s.ConfiguredMTs > 0 && s.ConfiguredMTs < s.SpeedMTs {
				speed += ", runs " + strconv.Itoa(s.ConfiguredMTs)
			}
		}
		form := s.FormFactor
		if s.Soldered {
			form = "soldered"
		}
		rows = append(rows, line{glyph: glyph, cells: []string{name, sysinfo.RAMSize(s.SizeB), s.Type, speed, form, s.Maker, s.Part}})
	}
	// Natural widths; the first column carries the slot's glyph.
	ws := make([]int, len(head))
	for i, h := range head {
		ws[i] = len(h)
	}
	for _, r := range rows {
		ws[0] = max(ws[0], 2+ansi.StringWidth(r.cells[0])) // glyph, space, name
		if r.empty {
			continue
		}
		for i := 1; i < len(r.cells); i++ {
			ws[i] = max(ws[i], ansi.StringWidth(tierText(r.cells[i], ascii(ctx))))
		}
	}
	const sep = 2
	room := b.w - 1 - 3
	total := func() int {
		t := 0
		for i, w := range ws {
			if w > 0 {
				t += w
				if i > 0 {
					t += sep
				}
			}
		}
		return t
	}
	for _, c := range []struct{ col, floor int }{{6, 10}, {5, 8}} {
		if over := total() - room; over > 0 {
			ws[c.col] = max(c.floor, ws[c.col]-over)
		}
	}
	for _, col := range []int{6, 5} {
		if total() > room {
			ws[col] = 0
		}
	}
	cell := func(i int, s string) string {
		if ws[i] == 0 {
			return ""
		}
		out := pad(fit(ctx, s, ws[i]), ws[i])
		if i > 0 {
			out = strings.Repeat(" ", sep) + out
		}
		return out
	}
	var hb strings.Builder
	for i, h := range head {
		hb.WriteString(cell(i, h))
	}
	b.add("   " + th.Muted.Render(strings.TrimRight(hb.String(), " ")))
	for _, r := range rows {
		g := r.glyph
		first := pad(g+" "+r.cells[0], ws[0])
		if r.empty {
			b.add("   " + th.Muted.Render(first) + th.Muted.Render(fit(ctx, strings.Repeat(" ", sep)+"empty, room for one more stick", room-ws[0])))
			continue
		}
		var rest strings.Builder
		for i := 1; i < len(r.cells); i++ {
			rest.WriteString(cell(i, r.cells[i]))
		}
		b.add("   " + th.Accent.Render(g) + th.Base.Render(strings.TrimPrefix(first, g)) + th.Base.Render(strings.TrimRight(rest.String(), " ")))
	}
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

// storage is the drives with their health, then the drive letters with how
// full each is.
func (b *builder) storage(fs []sysinfo.Finding) {
	th, ctx := b.ctx.Theme, b.ctx
	di := b.m.rep.Disks
	right := func() string {
		if !b.m.rep.Got(sysinfo.SecDisks) || len(di.Disks) == 0 {
			return ""
		}
		rank := map[sysinfo.Health]int{sysinfo.HealthGood: 0, sysinfo.HealthUnknown: 1, sysinfo.HealthWarning: 2, sysinfo.HealthBad: 3}
		worst := sysinfo.HealthGood
		for _, d := range di.Disks {
			if rank[d.Health] > rank[worst] {
				worst = d.Health
			}
		}
		if worst == sysinfo.HealthUnknown {
			return ""
		}
		return healthPiece(b, worst)
	}
	if !b.state("Storage", right, sysinfo.SecDisks, sysinfo.SecVolumes) {
		return
	}
	if e := b.m.rep.Errs[sysinfo.SecDisks]; e != "" {
		b.note("The drives could not be listed: " + e + ".")
	}
	hw := 0
	for _, d := range di.Disks {
		hw = max(hw, lenPlain(b, healthText(b, d.Health)))
	}
	for _, d := range di.Disks {
		model := d.Model
		if model == "" {
			model = d.MediaText()
		}
		mw := b.valW() - hw - 2
		b.kv("Disk "+strconv.Itoa(d.Number), th.Subtitle.Render(pad(fit(ctx, model, mw), mw))+"  "+healthPiece(b, d.Health))
		size := strings.TrimSpace(sysinfo.AdvertisedSize(d.SizeB) + " " + d.MediaText())
		if d.SizeB > 0 {
			size += " · Windows shows " + sysinfo.WindowsSize(d.SizeB)
		}
		b.kv("", th.Base.Render(fit(ctx, size, b.valW())))
		if len(d.Letters) > 0 {
			b.kv("", th.Muted.Render(fit(ctx, "holds "+strings.Join(d.Letters, ": ")+":"+boot(d), b.valW())))
		}
		b.wearRow(d)
	}
	b.volumes()
	if di.Note != "" {
		b.note(di.Note)
	}
	b.areaFindings(fs, "Storage")
}

// volumes is one row per drive letter: a gauge of how full it is, the free
// space beside it and the rest muted. Every gauge is the same width.
func (b *builder) volumes() {
	th, ctx := b.ctx.Theme, b.ctx
	type vrow struct {
		label, free, rest string
		frac              float64
		full              bool
	}
	var rows []vrow
	longest := 0
	for _, v := range b.m.rep.Volumes {
		used := v.TotalB - min(v.FreeB, v.TotalB)
		frac := 0.0
		if v.TotalB > 0 {
			frac = float64(used) / float64(v.TotalB)
		}
		var info []string
		info = append(info, "of "+sysinfo.WindowsSize(v.TotalB))
		if v.FileSystem != "" {
			info = append(info, v.FileSystem)
		}
		switch b.m.rep.Disks.BitLocker[v.Letter] {
		case sysinfo.TriYes:
			info = append(info, "BitLocker on")
		case sysinfo.TriNo:
			info = append(info, "BitLocker off")
		}
		if v.Removable {
			info = append(info, "removable")
		}
		full := frac >= 0.9
		if full {
			info = append(info, "nearly full")
		}
		r := vrow{label: v.Letter + ":", free: sysinfo.WindowsSize(v.FreeB) + " free ", rest: strings.Join(info, " · "), frac: frac, full: full}
		longest = max(longest, lenPlain(b, r.free+r.rest))
		rows = append(rows, r)
	}
	gw := min(max(b.valW()-longest-2, 8), 20)
	room := b.valW() - gaugeW(ctx, gw) - 2
	for _, r := range rows {
		st := th.Accent
		if r.full {
			st = th.Warning
		}
		free := fit(ctx, r.free, room)
		b.kv(r.label, gauge(ctx, r.frac, gw, st)+"  "+th.Base.Render(free)+th.Muted.Render(fit(ctx, r.rest, room-ansi.StringWidth(free))))
	}
}

// boot is " · Windows starts from here" for the boot disk.
func boot(d sysinfo.Disk) string {
	if d.Boot {
		return " · Windows starts from here"
	}
	return ""
}

// wearRow is the drive's own counters, or how to read them.
func (b *builder) wearRow(d sysinfo.Disk) {
	th, ctx := b.ctx.Theme, b.ctx
	if d.Rel != nil {
		var p []string
		if d.Rel.WearPct >= 0 {
			p = append(p, strconv.Itoa(d.Rel.WearPct)+"% of rated life used")
		}
		if d.Rel.PowerOnHours >= 0 {
			p = append(p, commas(d.Rel.PowerOnHours)+" hours on")
		}
		if d.Rel.ReadErrors >= 0 {
			p = append(p, commas(d.Rel.ReadErrors)+" read errors")
		}
		if len(p) == 0 {
			b.kv("", th.Muted.Render(fit(ctx, "the drive does not report wear or hours", b.valW())))
			return
		}
		b.kv("", th.Base.Render(fit(ctx, strings.Join(p, " · "), b.valW())))
		return
	}
	switch b.m.rel {
	case relRunning:
		b.kv("", th.Muted.Render("reading wear as admin"+ell(ctx)+" ")+th.Accent.Render(ctx.SpinnerFrame(b.m.spin)))
	case relDeclined:
		b.kv("", th.Warning.Render(fit(ctx, "Skipped: needs admin. Everything else is shown.", b.valW())))
	case relFailed:
		b.kv("", th.Warning.Render(fit(ctx, "Wear not read: "+b.m.relErr+".", b.valW())))
	case relDone:
		b.kv("", th.Muted.Render(fit(ctx, "the drive does not report wear or hours", b.valW())))
	default:
		lead := "wear and hours need admin · "
		hint := ctx.KeyHint("e", "read them (one Windows prompt)")
		if lenPlain(b, lead)+lenPlain(b, "[e] read them (one Windows prompt)") > b.valW() {
			lead = "needs admin · "
		}
		b.kv("", th.Muted.Render(fit(ctx, lead, b.valW()))+hint)
	}
}

// healthText is the drive health with its shape.
func healthText(b *builder, h sysinfo.Health) string {
	g := gap(b.ctx)
	switch h {
	case sysinfo.HealthGood:
		return b.ctx.Icons.Tick + g + "healthy"
	case sysinfo.HealthWarning:
		return b.ctx.Icons.Fail + g + "warning"
	case sysinfo.HealthBad:
		return b.ctx.Icons.Fail + g + "unhealthy"
	}
	return "health not reported"
}

// healthPiece is healthText in its colour.
func healthPiece(b *builder, h sysinfo.Health) string {
	th := b.ctx.Theme
	switch h {
	case sysinfo.HealthGood:
		return th.Success.Render(healthText(b, h))
	case sysinfo.HealthWarning, sysinfo.HealthBad:
		return th.Danger.Render(healthText(b, h))
	}
	return th.Muted.Render(healthText(b, h))
}

// lenPlain is the width of plain text after the tier swap.
func lenPlain(b *builder, s string) int { return ansi.StringWidth(tierText(s, ascii(b.ctx))) }

// processor is the Processor section.
func (b *builder) processor(fs []sysinfo.Finding) {
	c := b.m.rep.CPU
	right := func() string {
		if c.Cores == 0 {
			return ""
		}
		s := strconv.Itoa(c.Cores) + " cores"
		if c.Threads > 0 {
			s += " / " + strconv.Itoa(c.Threads) + " threads"
		}
		return b.ctx.Theme.Muted.Render(s)
	}
	if !b.state("Processor", right, sysinfo.SecCPU) {
		return
	}
	b.text("Name", sysinfo.CPUName(c.Name))
	b.text("Cores", c.CoresText())
	if c.BaseMHz > 0 {
		b.text("Clock", "about "+sysinfo.Clock(c.BaseMHz)+" base; Windows does not report the boost clock")
	} else {
		b.text("Clock", "")
	}
	var cache []string
	if c.L2 > 0 {
		cache = append(cache, "L2 "+mb(c.L2))
	}
	if c.L3 > 0 {
		cache = append(cache, "L3 "+mb(c.L3))
	}
	b.text("Cache", strings.Join(cache, " · "))
	b.text("Type", c.Arch)
	if c.Sockets > 1 {
		b.text("Sockets", strconv.Itoa(c.Sockets))
	}
	switch v := virtText(b.m.rep); {
	case strings.HasPrefix(v, "on"):
		b.status("Virtualize", sysinfo.Good, v)
	case v != "":
		b.status("Virtualize", sysinfo.Note, v)
	default:
		b.text("Virtualize", "")
	}
	b.areaFindings(fs, "Processor")
}

// mb is a cache size: "24 MB", "512 KB".
func mb(v uint64) string {
	if v >= 1<<20 {
		return strconv.FormatFloat(float64(v)/(1<<20), 'f', -1, 64) + " MB"
	}
	return strconv.Itoa(int(v>>10)) + " KB"
}

// virtText says whether virtualization is on, honestly: Windows' own
// hypervisor hides the firmware switch.
func virtText(r sysinfo.Report) string {
	switch {
	case r.Windows.Hypervisor == sysinfo.TriYes:
		return "on, in use by Windows (Hyper-V or memory integrity)"
	case r.CPU.VirtFirmware == sysinfo.TriYes:
		return "on in the firmware"
	case r.CPU.VirtFirmware == sysinfo.TriNo && r.Windows.Hypervisor == sysinfo.TriNo:
		return "off in the firmware"
	}
	return ""
}

// graphics is the Graphics section.
func (b *builder) graphics() {
	th, ctx := b.ctx.Theme, b.ctx
	gs := b.m.rep.GPUs
	right := func() string {
		if len(gs) > 1 {
			return th.Muted.Render(strconv.Itoa(len(gs)) + " adapters")
		}
		return ""
	}
	if !b.state("Graphics", right, sysinfo.SecGraphics) {
		return
	}
	if len(gs) == 0 {
		b.text("Graphics", "")
		return
	}
	for i, g := range gs {
		label := "GPU " + strconv.Itoa(i+1)
		if len(gs) == 1 {
			label = "GPU"
		}
		b.styled(label, sysinfo.GPUName(g.Name), th.Subtitle)
		kind := "dedicated"
		mem := ""
		if g.BuiltIn {
			kind = "built in"
			mem = "shares system memory"
		} else if g.MemoryB > 0 {
			mem = sysinfo.RAMSize(g.MemoryB) + " of its own memory"
		}
		b.text("", strings.Join(nonEmpty(kind, mem, driver(g, ctx.Width >= 100)), " · "))
	}
	b.note("Built in or dedicated is a rule of thumb from the maker and the name.")
}

// driver is "driver 31.0.101.5186", with its date on a wide terminal.
func driver(g sysinfo.GPU, withDate bool) string {
	if g.DriverVersion == "" {
		return ""
	}
	s := "driver " + g.DriverVersion
	if withDate && g.DriverDate != "" {
		s += " (" + g.DriverDate + ")"
	}
	return s
}

// displays is the Display section.
func (b *builder) displays() {
	th := b.ctx.Theme
	ds := b.m.rep.Displays
	right := func() string {
		if len(ds) > 1 {
			return th.Muted.Render(strconv.Itoa(len(ds)) + " screens")
		}
		return ""
	}
	if !b.state("Display", right, sysinfo.SecDisplays) {
		return
	}
	if len(ds) == 0 {
		b.text("Screen", "")
	}
	for i, d := range ds {
		label := "Screen " + strconv.Itoa(i+1)
		if len(ds) == 1 {
			label = "Screen"
		}
		var who []string
		if d.DiagonalIn > 0 {
			who = append(who, strconv.FormatFloat(d.DiagonalIn, 'f', 1, 64)+" inch")
		} else {
			who = append(who, "size not reported")
		}
		if d.BuiltIn {
			who = append(who, "built in")
		} else if d.Connection != "" {
			who = append(who, "on "+d.Connection)
		}
		if d.Name != "" {
			who = append(who, d.Name)
		} else if d.Maker != "" {
			who = append(who, "panel by "+d.Maker)
		}
		b.styled(label, strings.Join(who, " · "), th.Subtitle)
		var mode []string
		w, h := d.NativeW, d.NativeH
		if w == 0 {
			w, h = d.Width, d.Height
		}
		if w > 0 {
			res := fmt.Sprintf("%d×%d", w, h)
			if d.Width > 0 && d.NativeW > 0 && (d.Width != d.NativeW || d.Height != d.NativeH) {
				res += fmt.Sprintf(" (set to %d×%d)", d.Width, d.Height)
			}
			mode = append(mode, res)
		}
		if d.RefreshHz > 0 {
			r := strconv.Itoa(d.RefreshHz) + " Hz now"
			if d.MaxRefreshHz > d.RefreshHz {
				r += ", up to " + strconv.Itoa(d.MaxRefreshHz) + " Hz"
			}
			mode = append(mode, r)
		}
		switch {
		case d.HDR == sysinfo.TriYes && d.HDROn == sysinfo.TriYes:
			mode = append(mode, "HDR on")
		case d.HDR == sysinfo.TriYes:
			mode = append(mode, "HDR capable, off")
		}
		if len(mode) > 0 {
			b.text("", strings.Join(mode, " · "))
		}
	}
	dv := b.m.rep.Devices
	if b.m.rep.Got(sysinfo.SecDevices) && dv.Touch != sysinfo.TriUnknown {
		t := "no touch"
		if dv.Touch == sysinfo.TriYes {
			t = "touch"
			if dv.MaxTouches > 0 {
				t += " (" + strconv.Itoa(dv.MaxTouches) + " points)"
			}
		}
		if dv.Pen == sysinfo.TriYes {
			t += " · pen"
		}
		b.text("Input", t)
	}
}

// windows is the Windows & security section.
func (b *builder) windows(fs []sysinfo.Finding) {
	th, ctx := b.ctx.Theme, b.ctx
	w := b.m.rep.Windows
	right := func() string {
		switch w.Activated {
		case sysinfo.TriYes:
			return mark(ctx, sysinfo.Good, "activated")
		case sysinfo.TriNo:
			return mark(ctx, sysinfo.Warn, "not activated")
		}
		return ""
	}
	if !b.state("Windows & security", right, sysinfo.SecWindows) {
		return
	}
	b.styled("Windows", w.WindowsText(), th.Subtitle)
	if !w.Installed.IsZero() {
		b.text("Installed", w.Installed.Format("2 Jan 2006")+" (installed or last reset)")
	}
	switch w.Activated {
	case sysinfo.TriYes:
		b.status("Activation", sysinfo.Good, strings.Join(nonEmpty("activated", channelText(w.Channel)), " · "))
	case sysinfo.TriNo:
		b.status("Activation", sysinfo.Warn, strings.Join(nonEmpty("not activated", channelText(w.Channel)), " · "))
	default:
		b.text("Activation", "")
	}
	switch w.OEMKeyInFirmware {
	case sysinfo.TriYes:
		b.text("OEM key", "a Windows key is stored in the firmware (not read)")
	case sysinfo.TriNo:
		b.text("OEM key", "none in the firmware")
	}
	switch w.SecureBoot {
	case sysinfo.TriYes:
		b.status("Secure Boot", sysinfo.Good, "on")
	case sysinfo.TriNo:
		b.status("Secure Boot", sysinfo.Note, "off")
	default:
		b.text("Secure Boot", "")
	}
	switch {
	case w.TPM == "2.0":
		b.status("TPM", sysinfo.Good, "TPM 2.0")
	case w.TPM != "":
		b.status("TPM", sysinfo.Warn, "TPM "+w.TPM)
	case w.TPMPresent == sysinfo.TriNo:
		b.status("TPM", sysinfo.Warn, "none found (or off in the firmware)")
	default:
		b.text("TPM", "")
	}
	switch managed := managedText(w); {
	case strings.HasPrefix(managed, "yes"):
		b.status("Managed", sysinfo.Bad, managed)
	case managed != "":
		b.status("Managed", sysinfo.Good, managed)
	default:
		b.text("Managed", "")
	}
	if w.Autopilot != sysinfo.TriYes {
		b.kv("", th.Muted.Render(fit(ctx, "Autopilot lives in the cloud: this PC cannot fully see it.", b.valW())))
	}
	if w.Note != "" {
		b.note(w.Note)
	}
	b.areaFindings(fs, "Windows")
}

// channelText explains the licence channel.
func channelText(ch string) string {
	switch {
	case ch == "":
		return ""
	case strings.HasPrefix(ch, "OEM"):
		return "came with the PC (" + ch + ")"
	case ch == "Retail":
		return "a retail licence"
	case strings.Contains(ch, "GVLK"):
		return "company volume licence, KMS (" + ch + ")"
	case strings.HasPrefix(ch, "Volume"):
		return "company volume licence (" + ch + ")"
	}
	return ch
}

// managedText says who manages the PC, or that nothing says anyone does.
func managedText(w sysinfo.Windows) string {
	var by []string
	if w.DomainJoined == sysinfo.TriYes {
		d := "domain"
		if w.Domain != "" {
			d += " " + w.Domain
		}
		by = append(by, d)
	}
	if w.EntraJoined == sysinfo.TriYes {
		by = append(by, "Microsoft Entra")
	}
	if w.MDM == sysinfo.TriYes {
		by = append(by, "device management (MDM)")
	}
	if w.Autopilot == sysinfo.TriYes {
		a := "Autopilot"
		if w.AutopilotTenant != "" {
			a += " for " + w.AutopilotTenant
		}
		by = append(by, a)
	}
	if len(by) > 0 {
		return "yes: " + strings.Join(by, ", ")
	}
	if w.DomainJoined == sysinfo.TriNo && w.EntraJoined == sysinfo.TriNo && w.MDM == sysinfo.TriNo {
		return "no sign of an organisation on this PC"
	}
	return ""
}

// system is the System & firmware section.
func (b *builder) system(fs []sysinfo.Finding) {
	if !b.state("System & firmware", nil, sysinfo.SecSystem) {
		return
	}
	s := b.m.rep.System
	b.text("Maker", s.Maker)
	b.text("Model", strings.Join(nonEmpty(s.Model, s.Family), " · "))
	if s.SKU != "" {
		b.text("SKU", s.SKU)
	}
	switch {
	case s.Serial != "":
		b.text("Serial", s.Serial+" (private: left out of a report unless you say yes)")
	case s.SerialPlaceholder:
		b.text("Serial", "not set (the firmware holds a placeholder)")
	default:
		b.text("Serial", "")
	}
	b.text("Board", strings.TrimSpace(s.BoardMaker+" "+s.BoardModel))
	fw := strings.Join(nonEmpty(strings.TrimSpace(s.BIOSVendor+" "+s.BIOSVersion), date(s), s.Firmware), " · ")
	b.text("Firmware", fw)
	b.text("Type", s.Chassis.String())
	if s.Uptime > 0 {
		b.text("Up for", sysinfo.Duration(s.Uptime)+" (Fast Startup counts a shutdown as sleep)")
	}
	b.areaFindings(fs, "System")
}

// date is the firmware date.
func date(s sysinfo.System) string {
	if s.BIOSDate.IsZero() {
		return ""
	}
	return s.BIOSDate.Format("2 Jan 2006")
}

// network is the Network & built-in devices section.
func (b *builder) network() {
	ctx := b.ctx
	if !b.state("Network & devices", nil, sysinfo.SecNetwork, sysinfo.SecDevices) {
		return
	}
	if !b.m.rep.Got(sysinfo.SecNetwork) || len(b.m.rep.Network) == 0 {
		b.text("Network", "")
	}
	for _, a := range b.m.rep.Network {
		v := a.Description
		lvl := sysinfo.Note
		if a.Connected {
			lvl = sysinfo.Good
			v += " · connected"
			if s := sysinfo.Speed(a.SpeedBps); s != "" {
				v += " " + s
			}
		} else {
			v += " · not connected"
		}
		if ctx.Width >= 100 && a.MAC != "" {
			v += " · MAC " + a.MAC
		}
		b.status(a.Kind, lvl, v)
	}
	if b.m.rep.Got(sysinfo.SecDevices) {
		dv := b.m.rep.Devices
		var p []string
		for _, d := range []struct {
			name string
			t    sysinfo.Tri
		}{{"Bluetooth", dv.Bluetooth}, {"camera", dv.Camera}, {"fingerprint", dv.Fingerprint}} {
			switch d.t {
			case sysinfo.TriYes:
				p = append(p, d.name)
			case sysinfo.TriNo:
				p = append(p, "no "+d.name)
			}
		}
		b.text("Built in", strings.Join(p, " · "))
	}
}
