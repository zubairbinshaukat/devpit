package sysinfo

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Line is one label and its value.
type Line struct {
	Label, Value string
}

// NotReported is the words for a value the PC does not give.
const NotReported = "not reported by this PC"

// CPUName tidies the processor name: no (R), (TM), "CPU" or "@ 2.30GHz".
func CPUName(name string) string {
	s := name
	for _, junk := range []string{"(R)", "(r)", "(TM)", "(tm)", "®", "™"} {
		s = strings.ReplaceAll(s, junk, "")
	}
	if i := strings.Index(s, " @ "); i > 0 {
		s = s[:i]
	}
	s = strings.ReplaceAll(s, " CPU", "")
	s = strings.ReplaceAll(s, " Processor", "")
	return strings.Join(strings.Fields(s), " ")
}

// GPUName tidies an adapter name the same way.
func GPUName(name string) string { return CPUName(name) }

// EditionName is the edition as people say it: "Pro", "Home".
func EditionName(id string) string {
	switch id {
	case "Professional":
		return "Pro"
	case "Core":
		return "Home"
	case "CoreSingleLanguage":
		return "Home Single Language"
	case "CoreCountrySpecific":
		return "Home China"
	case "ProfessionalWorkstation":
		return "Pro for Workstations"
	case "ProfessionalEducation":
		return "Pro Education"
	case "EnterpriseS":
		return "Enterprise LTSC"
	case "IoTEnterpriseS":
		return "IoT Enterprise LTSC"
	}
	return id
}

// WindowsText is "Windows 11 Pro 24H2 (build 26100.2314)".
func (w Windows) WindowsText() string {
	parts := []string{}
	if w.Name != "" {
		parts = append(parts, w.Name)
	}
	if e := EditionName(w.Edition); e != "" {
		parts = append(parts, e)
	}
	if w.DisplayVersion != "" {
		parts = append(parts, w.DisplayVersion)
	}
	s := strings.Join(parts, " ")
	if w.Build > 0 {
		b := strconv.Itoa(w.Build)
		if w.UBR > 0 {
			b += "." + strconv.Itoa(w.UBR)
		}
		s += " (build " + b + ")"
	}
	return strings.TrimSpace(s)
}

// MediaText is "NVMe SSD", "SATA HDD", "USB drive".
func (d Disk) MediaText() string {
	kind := ""
	switch d.Media {
	case MediaSSD:
		kind = "SSD"
	case MediaHDD:
		kind = "hard disk"
	}
	switch {
	case d.Bus != "" && kind != "":
		return d.Bus + " " + kind
	case kind != "":
		return kind
	case d.Bus != "":
		return d.Bus + " drive"
	}
	return "drive"
}

// String is the word for a drive's health.
func (h Health) String() string {
	switch h {
	case HealthGood:
		return "healthy"
	case HealthWarning:
		return "warning"
	case HealthBad:
		return "unhealthy"
	}
	return "health not reported"
}

// Glance is the at-a-glance list, worded like a shop listing. A section
// that did not arrive is left out; one that arrived empty says so.
func Glance(r Report) []Line {
	var out []Line
	add := func(label, value string) {
		if value == "" {
			value = NotReported
		}
		out = append(out, Line{label, value})
	}
	if r.Have[SecCPU] {
		add("Processor", glanceCPU(r.CPU))
	}
	if r.Have[SecMemory] {
		add("Memory", glanceMemory(r.Memory))
	}
	if r.Have[SecDisks] {
		add("Storage", glanceStorage(r.Disks))
	}
	if r.Have[SecGraphics] {
		add("Graphics", glanceGraphics(r.GPUs))
	}
	if r.Have[SecDisplays] {
		add("Display", glanceDisplays(r.Displays))
	}
	if r.Have[SecBattery] {
		add("Battery", glanceBattery(r.Battery))
	}
	if r.Have[SecWindows] {
		add("Windows", glanceWindows(r.Windows))
	}
	return out
}

func glanceCPU(c CPU) string {
	parts := []string{}
	if c.Name != "" {
		parts = append(parts, CPUName(c.Name))
	}
	if c.Cores > 0 {
		s := fmt.Sprintf("%d cores", c.Cores)
		if c.Threads > 0 {
			s += fmt.Sprintf(" / %d threads", c.Threads)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

func glanceMemory(m Memory) string {
	if m.InstalledB == 0 && len(m.Slots) == 0 {
		return ""
	}
	s := RAMSize(m.InstalledB)
	var kind string
	for _, sl := range m.Slots {
		if !sl.Empty && sl.Type != "" {
			kind = sl.Type
			if sl.SpeedMTs > 0 {
				kind += " " + strconv.Itoa(sl.SpeedMTs) + " MT/s"
			}
			break
		}
	}
	if kind != "" {
		s += " " + kind
	}
	fitted, free, soldered := m.Fitted()
	switch {
	case soldered && fitted+free == 0:
		s += ", soldered"
	case fitted+free > 0:
		s += fmt.Sprintf(", %d of %d slots used", fitted, fitted+free)
		if soldered {
			s += " plus soldered"
		}
	}
	return strings.TrimSpace(s)
}

func glanceStorage(di DiskInfo) string {
	var parts []string
	for _, d := range di.Disks {
		if d.Bus == "USB" {
			continue
		}
		parts = append(parts, strings.TrimSpace(AdvertisedSize(d.SizeB)+" "+d.MediaText()))
	}
	return strings.Join(parts, " + ")
}

func glanceGraphics(gs []GPU) string {
	var parts []string
	for _, g := range gs {
		s := GPUName(g.Name)
		if !g.BuiltIn && g.MemoryB > 0 {
			s += " " + RAMSize(g.MemoryB)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " + ")
}

func glanceDisplays(ds []Display) string {
	var parts []string
	for _, d := range ds {
		var p []string
		if d.DiagonalIn > 0 {
			p = append(p, strconv.FormatFloat(d.DiagonalIn, 'f', 1, 64)+`"`)
		}
		w, h := d.NativeW, d.NativeH
		if w == 0 {
			w, h = d.Width, d.Height
		}
		if w > 0 {
			p = append(p, fmt.Sprintf("%dx%d", w, h))
		}
		if hz := max(d.MaxRefreshHz, d.RefreshHz); hz > 0 {
			p = append(p, strconv.Itoa(hz)+" Hz")
		}
		if d.BuiltIn {
			p = append(p, "built-in")
		} else if d.Connection != "" {
			p = append(p, "on "+d.Connection)
		}
		parts = append(parts, strings.Join(p, " "))
	}
	return strings.Join(parts, " + ")
}

func glanceBattery(b Battery) string {
	switch b.Present {
	case TriNo:
		return "none"
	case TriUnknown:
		return ""
	}
	var p []string
	if pct, ok := b.Health(); ok {
		p = append(p, fmt.Sprintf("%d%% health", pct))
	}
	if n, ok := b.Cycles(); ok {
		p = append(p, plural(n, "cycle", "cycles"))
	}
	if design, _, rel := b.Totals(); design > 0 && !rel {
		p = append(p, fmt.Sprintf("%.0f Wh when new", float64(design)/1000))
	}
	return strings.Join(p, ", ")
}

func glanceWindows(w Windows) string {
	s := strings.TrimSpace(w.Name + " " + EditionName(w.Edition) + " " + w.DisplayVersion)
	switch w.Activated {
	case TriYes:
		s += ", activated"
	case TriNo:
		s += ", not activated"
	}
	return strings.TrimPrefix(s, ", ")
}

// Title is the PC's name: "HP OMEN by HP Laptop 16-b0xxx", or "This PC".
func (s System) Title() string {
	t := strings.TrimSpace(s.Maker + " " + s.Model)
	if s.Maker != "" && strings.HasPrefix(strings.ToLower(s.Model), strings.ToLower(s.Maker)) {
		t = s.Model
	}
	if t == "" {
		return "This PC"
	}
	return t
}

// MarkdownOptions shapes the export.
type MarkdownOptions struct {
	// Serials includes serial numbers and MAC addresses. Off by default.
	Serials bool
	// Now dates the report.
	Now time.Time
	// Version is Devpit's version.
	Version string
	// Findings is the Buyer's check to include, usually [Assess]'s answer.
	Findings []Finding
}

// Markdown writes the report as "## Heading" sections of "- Label: value"
// lines and nothing else. Serial numbers and MAC addresses are left out
// unless opts.Serials; the product key is never in the report at all.
func Markdown(r Report, opts MarkdownOptions) string {
	var b strings.Builder
	head := func(h string) { fmt.Fprintf(&b, "\n## %s\n\n", h) }
	line := func(label, value string) {
		if value == "" {
			value = NotReported
		}
		fmt.Fprintf(&b, "- %s: %s\n", label, oneLine(value))
	}
	private := func(label, value string) {
		if opts.Serials && value != "" {
			line(label, value)
		}
	}
	fmt.Fprintf(&b, "## PC specs: %s\n\n", r.System.Title())
	line("Read", opts.Now.Format("2 Jan 2006 15:04"))
	line("Made with", strings.TrimSpace("Devpit "+opts.Version))
	if !opts.Serials {
		line("Serial numbers", "left out")
	}

	if len(opts.Findings) > 0 {
		head("Buyer's check")
		for _, f := range opts.Findings {
			text := f.Text
			if f.Thumb {
				text += " (rule of thumb)"
			}
			line(f.Area+" ("+f.Level.String()+")", upper(text))
		}
	}
	if g := Glance(r); len(g) > 0 {
		head("At a glance")
		for _, l := range g {
			line(l.Label, l.Value)
		}
	}

	if r.Got(SecBattery) && r.Battery.Present != TriUnknown {
		head("Battery")
		bt := r.Battery
		if bt.Present == TriNo {
			line("Battery", "none")
		}
		for i, p := range bt.Packs {
			label := "Battery"
			if len(bt.Packs) > 1 {
				label = fmt.Sprintf("Battery %d", i+1)
			}
			line(label, strings.TrimSpace(p.Maker+" "+p.Name+" "+p.Chemistry))
			if pct, ok := healthPct(p.DesignmWh, p.FullmWh); ok {
				line(label+" health", fmt.Sprintf("%d%%", pct))
			}
			if !p.Relative && p.DesignmWh > 0 {
				line(label+" capacity", fmt.Sprintf("%d mWh now, %d mWh when new", p.FullmWh, p.DesignmWh))
			}
			if p.Cycles > 0 {
				line(label+" cycles", strconv.Itoa(p.Cycles))
			} else {
				line(label+" cycles", NotReported)
			}
			private(label+" serial", p.Serial)
		}
	}
	if r.Got(SecMemory) {
		head("Memory")
		m := r.Memory
		line("Installed", RAMSize(m.InstalledB))
		line("Usable", RAMSize(m.UsableB))
		for i, s := range m.Slots {
			label := s.Locator
			if label == "" {
				label = fmt.Sprintf("Slot %d", i+1)
			}
			switch {
			case s.Empty:
				line(label, "empty")
			case s.Soldered:
				line(label, strings.TrimSpace(s.ModuleText()+" soldered, cannot be upgraded"))
			default:
				line(label, strings.TrimSpace(s.ModuleText()+" "+s.Maker+" "+s.Part))
			}
			private(label+" serial", s.Serial)
		}
	}
	if r.Got(SecDisks) || r.Got(SecVolumes) {
		head("Storage")
		for _, d := range r.Disks.Disks {
			label := fmt.Sprintf("Disk %d", d.Number)
			line(label, fmt.Sprintf("%s, %s %s (Windows shows %s), %s", d.Model, AdvertisedSize(d.SizeB), d.MediaText(), WindowsSize(d.SizeB), d.Health))
			if d.Rel != nil {
				if d.Rel.WearPct >= 0 {
					line(label+" wear", fmt.Sprintf("%d%% of rated life used", d.Rel.WearPct))
				}
				if d.Rel.PowerOnHours >= 0 {
					line(label+" hours on", strconv.FormatInt(d.Rel.PowerOnHours, 10))
				}
			}
			private(label+" serial", d.Serial)
		}
		for _, v := range r.Volumes {
			bl := ""
			switch r.Disks.BitLocker[v.Letter] {
			case TriYes:
				bl = ", BitLocker on"
			case TriNo:
				bl = ", BitLocker off"
			}
			line(v.Letter+":", fmt.Sprintf("%s free of %s %s%s", WindowsSize(v.FreeB), WindowsSize(v.TotalB), v.FileSystem, bl))
		}
	}
	if r.Got(SecCPU) {
		head("Processor")
		c := r.CPU
		line("Name", CPUName(c.Name))
		line("Cores", c.CoresText())
		line("Base clock", Clock(c.BaseMHz))
		line("Architecture", c.Arch)
	}
	if r.Got(SecGraphics) {
		head("Graphics")
		for _, g := range r.GPUs {
			kind := "dedicated"
			if g.BuiltIn {
				kind = "built-in (rule of thumb)"
			}
			mem := ""
			if !g.BuiltIn && g.MemoryB > 0 {
				mem = ", " + RAMSize(g.MemoryB)
			}
			line(GPUName(g.Name), kind+mem+", driver "+g.DriverVersion)
		}
	}
	if r.Got(SecDisplays) {
		head("Display")
		for i, d := range r.Displays {
			line(fmt.Sprintf("Screen %d", i+1), glanceDisplays([]Display{d}))
			private(fmt.Sprintf("Screen %d serial", i+1), d.Serial)
		}
	}
	if r.Got(SecWindows) {
		head("Windows and security")
		w := r.Windows
		line("Windows", w.WindowsText())
		line("Activated", triWord(w.Activated))
		line("Licence channel", w.Channel)
		line("Secure Boot", triWord(w.SecureBoot))
		line("TPM", w.TPM)
		if m := managedBy(w); m != "" {
			line("Managed by an organisation", m)
		}
	}
	if r.Got(SecSystem) {
		head("System and firmware")
		s := r.System
		line("Maker", s.Maker)
		line("Model", s.Model)
		line("Type", s.Chassis.String())
		line("Firmware", strings.TrimSpace(s.BIOSVendor+" "+s.BIOSVersion+" "+dateOrEmpty(s.BIOSDate)))
		private("Serial number", s.Serial)
	}
	if r.Got(SecNetwork) && len(r.Network) > 0 {
		head("Network")
		for _, a := range r.Network {
			line(a.Kind, a.Description)
			if a.WifiStandard != "" {
				line(a.Kind+" standard", a.WifiStandard)
			}
			private(a.Kind+" MAC address", a.MAC)
		}
	}
	return strings.TrimLeft(b.String(), "\n")
}

// triWord is yes, no or not reported.
func triWord(t Tri) string {
	switch t {
	case TriYes:
		return "yes"
	case TriNo:
		return "no"
	}
	return ""
}

// dateOrEmpty formats a date, "" for the zero time.
func dateOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2 Jan 2006")
}

// oneLine keeps a value on one Markdown line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// upper starts a sentence with a capital.
func upper(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
