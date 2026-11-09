package specs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
)

// storage is the drives with their health, then the drive letters.
func (b *builder) storage(fs []sysinfo.Finding) {
	th, ctx := b.ctx.Theme, b.ctx
	if !b.state("Storage", sysinfo.SecDisks, sysinfo.SecVolumes) {
		return
	}
	di := b.m.rep.Disks
	if e := b.m.rep.Errs[sysinfo.SecDisks]; e != "" {
		b.note("The drives could not be listed: " + e + ".")
	}
	wide := ctx.Width >= 100
	for _, d := range di.Disks {
		label := "Disk " + strconv.Itoa(d.Number)
		health := healthPiece(b, d.Health)
		hw := lenPlain(b, healthText(b, d.Health))
		size := sysinfo.AdvertisedSize(d.SizeB) + " " + d.MediaText() + " (Windows shows " + sysinfo.WindowsSize(d.SizeB) + ")"
		if wide && lenPlain(b, d.Model+" · "+size) <= b.valW()-hw-2 {
			v := fit(ctx, d.Model+" · "+size, b.valW()-hw-2)
			b.kv(label, th.Base.Render(pad(v, b.valW()-hw-2))+"  "+health)
		} else {
			model := d.Model
			if model == "" {
				model = d.MediaText()
			}
			v := fit(ctx, model, b.valW()-hw-2)
			b.kv(label, th.Base.Render(pad(v, b.valW()-hw-2))+"  "+health)
			b.kv("", th.Base.Render(fit(ctx, size, b.valW())))
		}
		if len(d.Letters) > 0 {
			b.kv("", th.Muted.Render(fit(ctx, "holds "+strings.Join(d.Letters, ": ")+":"+boot(d), b.valW())))
		}
		b.wearRow(d)
	}
	for _, v := range b.m.rep.Volumes {
		used := v.TotalB - min(v.FreeB, v.TotalB)
		bw := min(12, b.valW()-36)
		frac := 0.0
		if v.TotalB > 0 {
			frac = float64(used) / float64(v.TotalB)
		}
		info := sysinfo.WindowsSize(v.FreeB) + " free of " + sysinfo.WindowsSize(v.TotalB)
		if v.FileSystem != "" {
			info += " · " + v.FileSystem
		}
		switch di.BitLocker[v.Letter] {
		case sysinfo.TriYes:
			info += " · BitLocker on"
		case sysinfo.TriNo:
			info += " · BitLocker off"
		}
		if v.Removable {
			info += " · removable"
		}
		b.kv(v.Letter+":", bar(ctx, frac, bw)+" "+th.Base.Render(fit(ctx, info, b.valW()-barW(ctx, bw)-1)))
	}
	if di.Note != "" {
		b.note(di.Note)
	}
	b.areaFindings(fs, "Storage")
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
	switch h {
	case sysinfo.HealthGood:
		return b.ctx.Icons.Tick + " healthy"
	case sysinfo.HealthWarning:
		return b.ctx.Icons.Fail + " warning"
	case sysinfo.HealthBad:
		return b.ctx.Icons.Fail + " unhealthy"
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
func lenPlain(b *builder, s string) int { return len([]rune(tierText(s, ascii(b.ctx)))) }

// processor is the Processor section.
func (b *builder) processor(fs []sysinfo.Finding) {
	if !b.state("Processor", sysinfo.SecCPU) {
		return
	}
	c := b.m.rep.CPU
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
	b.text("Virtualize", virtText(b.m.rep))
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
	if !b.state("Graphics", sysinfo.SecGraphics) {
		return
	}
	gs := b.m.rep.GPUs
	if len(gs) == 0 {
		b.text("Graphics", "")
		return
	}
	for i, g := range gs {
		label := "GPU " + strconv.Itoa(i+1)
		if len(gs) == 1 {
			label = "GPU"
		}
		b.text(label, sysinfo.GPUName(g.Name))
		kind := "dedicated"
		mem := ""
		if g.BuiltIn {
			kind = "built in"
			mem = "shares system memory"
		} else if g.MemoryB > 0 {
			mem = sysinfo.RAMSize(g.MemoryB) + " of its own memory"
		}
		v := strings.Join(nonEmpty(kind, mem, driver(g, ctx.Width >= 100)), " · ")
		b.kv("", th.Base.Render(fit(ctx, v, b.valW())))
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
	th, ctx := b.ctx.Theme, b.ctx
	if !b.state("Display", sysinfo.SecDisplays) {
		return
	}
	ds := b.m.rep.Displays
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
		b.text(label, strings.Join(who, " · "))
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
			b.kv("", th.Base.Render(fit(ctx, strings.Join(mode, " · "), b.valW())))
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
	if !b.state("Windows & security", sysinfo.SecWindows) {
		return
	}
	w := b.m.rep.Windows
	b.text("Windows", w.WindowsText())
	if !w.Installed.IsZero() {
		b.text("Installed", w.Installed.Format("2 Jan 2006")+" (installed or last reset)")
	}
	act := ""
	switch w.Activated {
	case sysinfo.TriYes:
		act = ctx.Icons.Tick + " activated"
	case sysinfo.TriNo:
		act = ctx.Icons.Warn + " not activated"
	}
	if act != "" && w.Channel != "" {
		act += " · " + channelText(w.Channel)
	}
	b.text("Activation", act)
	switch w.OEMKeyInFirmware {
	case sysinfo.TriYes:
		b.text("OEM key", "a Windows key is stored in the firmware (not read)")
	case sysinfo.TriNo:
		b.text("OEM key", "none in the firmware")
	}
	b.text("Secure Boot", triText(w.SecureBoot, "on", "off"))
	tpm := ""
	switch {
	case w.TPM != "":
		tpm = "TPM " + w.TPM
	case w.TPMPresent == sysinfo.TriNo:
		tpm = "none found (or off in the firmware)"
	}
	b.text("TPM", tpm)
	managed := managedText(w)
	b.text("Managed", managed)
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

// triText is yes/no words, "" when unknown.
func triText(t sysinfo.Tri, yes, no string) string {
	switch t {
	case sysinfo.TriYes:
		return yes
	case sysinfo.TriNo:
		return no
	}
	return ""
}

// system is the System & firmware section.
func (b *builder) system(fs []sysinfo.Finding) {
	if !b.state("System & firmware", sysinfo.SecSystem) {
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
	if !b.state("Network & devices", sysinfo.SecNetwork, sysinfo.SecDevices) {
		return
	}
	wide := b.ctx.Width >= 100
	if !b.m.rep.Got(sysinfo.SecNetwork) || len(b.m.rep.Network) == 0 {
		b.text("Network", "")
	}
	for _, a := range b.m.rep.Network {
		v := a.Description
		if a.Connected {
			v += " · connected"
			if s := sysinfo.Speed(a.SpeedBps); s != "" {
				v += " " + s
			}
		} else {
			v += " · not connected"
		}
		if wide && a.MAC != "" {
			v += " · MAC " + a.MAC
		}
		b.text(a.Kind, v)
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
