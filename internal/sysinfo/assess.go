package sysinfo

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Level is how much a finding matters.
type Level int

// The levels, mildest first.
const (
	Good Level = iota
	Note
	Warn
	Bad
)

// String is the level's word, the one the screen prints beside its glyph.
func (l Level) String() string {
	switch l {
	case Bad:
		return "check"
	case Warn:
		return "look"
	case Note:
		return "note"
	}
	return "fine"
}

// Finding is one line of the Buyer's check.
type Finding struct {
	Level Level
	// Area is the part of the PC it is about: "Battery", "Memory".
	Area string
	// Text is the finding in plain words.
	Text string
	// Thumb marks a rule of thumb rather than a fact: the screen and the
	// report say so.
	Thumb bool
	// Chip is the short label of a Good finding, for the row of ticks.
	Chip string
}

// windows10SupportEnded is the day Windows 10 stopped getting security
// updates for home and business users (Microsoft's lifecycle page; paid
// Extended Security Updates aside). Keep the date here and nowhere else.
func windows10SupportEnded() time.Time {
	return time.Date(2025, time.October, 14, 0, 0, 0, 0, time.UTC)
}

// Assess is the Buyer's check: what is worth a closer look on this PC,
// worst first, then the plain notes, then what is fine. A section that did
// not arrive, or a value the PC does not report, never produces a flag.
func Assess(r Report, now time.Time) []Finding {
	var out []Finding
	add := func(l Level, area, text string, thumb bool) {
		out = append(out, Finding{Level: l, Area: area, Text: text, Thumb: thumb})
	}
	good := func(area, text, chip string) {
		out = append(out, Finding{Level: Good, Area: area, Text: text, Chip: chip})
	}

	// Battery.
	if r.Got(SecBattery) {
		b := r.Battery
		if pct, ok := b.Health(); ok && b.Present == TriYes {
			switch {
			case pct < 60:
				add(Bad, "Battery", fmt.Sprintf("holds %d%% of its design charge: very worn, plan on a replacement.", pct), true)
			case pct < 80:
				add(Warn, "Battery", fmt.Sprintf("holds %d%% of its design charge: worn, expect noticeably shorter runtime.", pct), true)
			default:
				good("Battery", fmt.Sprintf("holds %d%% of its design charge.", pct), fmt.Sprintf("battery %d%%", pct))
			}
		}
		if b.Present == TriNo && r.Got(SecSystem) && r.System.Chassis.Portable() {
			add(Bad, "Battery", "this is a "+r.System.Chassis.String()+" but Windows found no battery.", false)
		}
	}

	// Drives.
	if r.Got(SecDisks) {
		healthy := 0
		for _, d := range r.Disks.Disks {
			name := diskName(d)
			switch d.Health {
			case HealthBad:
				add(Bad, "Storage", name+": Windows says the drive is unhealthy. Back up and replace it.", false)
			case HealthWarning:
				add(Bad, "Storage", name+": Windows warns about the drive's health. Back up first.", false)
			case HealthGood:
				healthy++
			}
			if d.Rel != nil {
				switch w := d.Rel.WearPct; {
				case w >= 90:
					add(Bad, "Storage", fmt.Sprintf("%s has used %d%% of its rated life.", name, w), false)
				case w >= 70:
					add(Warn, "Storage", fmt.Sprintf("%s has used %d%% of its rated life.", name, w), false)
				}
				if d.Rel.ReadErrors > 0 {
					add(Warn, "Storage", fmt.Sprintf("%s has %d uncorrected read errors.", name, d.Rel.ReadErrors), false)
				}
			}
			if d.Boot && d.Media == MediaHDD {
				add(Warn, "Storage", "Windows is on a spinning hard disk: an SSD would be much faster.", false)
			}
		}
		if healthy > 0 && healthy == len(r.Disks.Disks) {
			good("Storage", plural(healthy, "drive", "drives")+" healthy.", "drives healthy")
		}
	}

	// Memory.
	if r.Got(SecMemory) {
		m := r.Memory
		fitted, free, soldered := m.Fitted()
		if fitted == 1 && free >= 1 && !soldered {
			add(Warn, "Memory", "one stick and a free slot: memory runs single-channel, which is slower.", false)
		}
		if fitted >= 2 && mismatched(m.Slots) {
			add(Warn, "Memory", "the sticks differ in size or speed (mismatched).", false)
		}
		if m.InstalledB > 0 && m.InstalledB < 8*gib-gib/4 {
			add(Warn, "Memory", RAMSize(m.InstalledB)+" installed: 8 GB is a sensible minimum for Windows 11.", true)
		} else if m.InstalledB > 0 {
			good("Memory", RAMSize(m.InstalledB)+" memory.", RAMSize(m.InstalledB)+" memory")
		}
		if soldered {
			add(Note, "Memory", "soldered memory cannot be upgraded later.", false)
		}
		for _, s := range m.Slots {
			if !s.Empty && s.SpeedMTs > 0 && s.ConfiguredMTs > 0 && s.ConfiguredMTs < s.SpeedMTs {
				add(Note, "Memory", fmt.Sprintf("memory runs at %d MT/s, below its rated %d MT/s.", s.ConfiguredMTs, s.SpeedMTs), false)
				break
			}
		}
	}

	// Windows and security.
	if r.Got(SecWindows) {
		w := r.Windows
		switch w.Activated {
		case TriNo:
			add(Warn, "Windows", "Windows is not activated.", false)
		case TriYes:
			good("Windows", "Windows is activated.", "activated")
		}
		if strings.HasPrefix(w.Channel, "Volume") && w.DomainJoined == TriNo {
			add(Warn, "Windows", "a volume (company) licence on a PC that is not in a company domain: ask how Windows was activated.", true)
		}
		if managed := managedBy(w); managed != "" {
			add(Bad, "Windows", "managed by an organisation ("+managed+"): ask the seller for proof it was released; it may lock again after a reset.", false)
		}
		if w.Name == "Windows 10" && !now.Before(windows10SupportEnded()) {
			add(Warn, "Windows", "Windows 10 no longer gets security updates (support ended "+windows10SupportEnded().Format("2 Jan 2006")+").", false)
		}
		switch {
		case w.TPMPresent == TriNo:
			add(Warn, "Windows", "no TPM found (or it is off in the firmware): Windows 11 requires TPM 2.0.", false)
		case w.TPMPresent == TriYes && w.TPM == "1.2":
			add(Warn, "Windows", "TPM 1.2 only: Windows 11 requires TPM 2.0.", false)
		case w.TPM == "2.0":
			good("Windows", "TPM 2.0.", "TPM 2.0")
		}
		switch w.SecureBoot {
		case TriNo:
			add(Note, "Windows", "Secure Boot is off.", false)
		case TriYes:
			good("Windows", "Secure Boot on.", "Secure Boot")
		}
	}

	// System.
	if r.Got(SecSystem) {
		s := r.System
		if s.SerialPlaceholder && brandedMaker(s.Maker) {
			add(Warn, "System", "the serial number is a placeholder: worth asking, the motherboard may have been replaced.", true)
		}
		if !s.BIOSDate.IsZero() {
			add(Note, "System", "firmware dated "+s.BIOSDate.Format("Jan 2006")+": check the maker's site for a newer one.", false)
		}
	}
	if r.Got(SecCPU) && r.CPU.VirtFirmware == TriNo && r.Got(SecWindows) && r.Windows.Hypervisor == TriNo {
		add(Note, "Processor", "virtualization is off in the firmware (needed for WSL, Docker and virtual machines).", false)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Level > out[j].Level })
	return out
}

// managedBy names how the PC is managed, or "" when nothing says it is.
func managedBy(w Windows) string {
	var by []string
	if w.DomainJoined == TriYes {
		by = append(by, "joined to a domain")
	}
	if w.EntraJoined == TriYes {
		by = append(by, "Microsoft Entra")
	}
	if w.MDM == TriYes {
		by = append(by, "device management")
	}
	if w.Autopilot == TriYes {
		by = append(by, "Autopilot")
	}
	return strings.Join(by, ", ")
}

// mismatched reports whether the fitted socketed sticks differ in size or
// speed.
func mismatched(slots []Module) bool {
	var first *Module
	for i := range slots {
		s := &slots[i]
		if s.Empty || s.Soldered {
			continue
		}
		if first == nil {
			first = s
			continue
		}
		if (s.SizeB != 0 && first.SizeB != 0 && s.SizeB != first.SizeB) ||
			(s.SpeedMTs != 0 && first.SpeedMTs != 0 && s.SpeedMTs != first.SpeedMTs) {
			return true
		}
	}
	return false
}

// diskName is "Disk 0 (Samsung PM991)".
func diskName(d Disk) string {
	n := fmt.Sprintf("Disk %d", d.Number)
	if d.Model != "" {
		n += " (" + d.Model + ")"
	}
	return n
}

// plural is "1 drive", "2 drives".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Counts splits findings into the ones to look at (Bad and Warn) and the
// fine ones.
func Counts(fs []Finding) (look, fine int) {
	for _, f := range fs {
		switch f.Level {
		case Bad, Warn:
			look++
		case Good:
			fine++
		}
	}
	return look, fine
}
