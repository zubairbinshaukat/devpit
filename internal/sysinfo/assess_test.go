package sysinfo_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
	"github.com/zubairbinshaukat/devpit/internal/sysinfo/sysinfofake"
)

var now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

// has reports a finding of this level whose text contains part.
func has(fs []sysinfo.Finding, l sysinfo.Level, part string) bool {
	for _, f := range fs {
		if f.Level == l && strings.Contains(f.Text, part) {
			return true
		}
	}
	return false
}

// flagged reports any Bad or Warn finding containing part.
func flagged(fs []sysinfo.Finding, part string) bool {
	return has(fs, sysinfo.Bad, part) || has(fs, sysinfo.Warn, part)
}

func battery(design, full uint64) sysinfo.Report {
	r := sysinfofake.Good()
	r.Battery.Packs[0].DesignmWh, r.Battery.Packs[0].FullmWh = design, full
	return r
}

func TestAssessEachRuleAtItsBoundary(t *testing.T) {
	// 1. Battery health: 80 good, 79 and 60 worn, 59 very worn.
	for _, c := range []struct {
		full uint64
		lvl  sysinfo.Level
		word string
	}{{80000, sysinfo.Good, "holds 80%"}, {79000, sysinfo.Warn, "worn"}, {60000, sysinfo.Warn, "worn"}, {59000, sysinfo.Bad, "very worn"}} {
		fs := sysinfo.Assess(battery(100000, c.full), now)
		if !has(fs, c.lvl, c.word) {
			t.Errorf("battery %d/100000: want %v %q in %+v", c.full, c.lvl, c.word, fs)
		}
	}
	for _, f := range sysinfo.Assess(battery(100000, 59000), now) {
		if f.Area == "Battery" && f.Level == sysinfo.Bad && !f.Thumb {
			t.Error("the battery threshold is a rule of thumb and must say so")
		}
	}

	// 2. A laptop with no battery.
	r := sysinfofake.Good()
	r.Battery = sysinfo.Battery{Present: sysinfo.TriNo}
	if !has(sysinfo.Assess(r, now), sysinfo.Bad, "no battery") {
		t.Error("laptop without a battery not flagged")
	}
	if d := sysinfofake.Desktop(); flagged(sysinfo.Assess(d, now), "battery") {
		t.Error("a desktop without a battery is normal")
	}

	// 3. Drive health; 4. wear 70/90 and read errors; 5. Windows on an HDD.
	r = sysinfofake.Good()
	r.Disks.Disks[0].Health = sysinfo.HealthBad
	if !has(sysinfo.Assess(r, now), sysinfo.Bad, "unhealthy") {
		t.Error("unhealthy drive")
	}
	for _, c := range []struct {
		wear int
		lvl  sysinfo.Level
	}{{69, sysinfo.Good}, {70, sysinfo.Warn}, {89, sysinfo.Warn}, {90, sysinfo.Bad}} {
		r = sysinfofake.Good()
		r.Disks.Disks[0].Rel = &sysinfo.Reliability{WearPct: c.wear, PowerOnHours: 10, ReadErrors: 0}
		fs := sysinfo.Assess(r, now)
		if c.lvl == sysinfo.Good {
			if flagged(fs, "rated life") {
				t.Errorf("wear %d flagged", c.wear)
			}
			continue
		}
		if !has(fs, c.lvl, "rated life") {
			t.Errorf("wear %d: want %v", c.wear, c.lvl)
		}
	}
	r = sysinfofake.Good()
	r.Disks.Disks[0].Rel = &sysinfo.Reliability{WearPct: 1, PowerOnHours: 10, ReadErrors: 3}
	if !has(sysinfo.Assess(r, now), sysinfo.Warn, "read errors") {
		t.Error("read errors")
	}
	r = sysinfofake.Good()
	r.Disks.Disks[0].Media = sysinfo.MediaHDD
	if !has(sysinfo.Assess(r, now), sysinfo.Warn, "spinning hard disk") {
		t.Error("Windows on a hard disk")
	}

	// 6 and 7. One stick and a free slot; mismatched sticks.
	fs := sysinfo.Assess(sysinfofake.Flags(), now)
	if !has(fs, sysinfo.Warn, "single-channel") {
		t.Error("one stick and a free slot")
	}
	if flagged(sysinfo.Assess(sysinfofake.Good(), now), "single-channel") || flagged(sysinfo.Assess(sysinfofake.Soldered(), now), "single-channel") {
		t.Error("single-channel claimed without one stick and a free slot")
	}
	r = sysinfofake.Good()
	r.Memory.Slots[1].SizeB = 8 << 30
	if !has(sysinfo.Assess(r, now), sysinfo.Warn, "mismatched") {
		t.Error("mismatched sticks")
	}

	// 8. Under 8 GB (a rule of thumb). 8 GB itself is fine.
	r = sysinfofake.Good()
	r.Memory.InstalledB = 4 << 30
	if !has(sysinfo.Assess(r, now), sysinfo.Warn, "sensible minimum") {
		t.Error("4 GB")
	}
	if flagged(fs, "sensible minimum") {
		t.Error("8 GB flagged")
	}

	// 9. Not activated; 10. volume licence off a domain; 11. managed.
	if !has(fs, sysinfo.Warn, "not activated") || !has(fs, sysinfo.Bad, "managed by an organisation") {
		t.Errorf("activation/managed: %+v", fs)
	}
	r = sysinfofake.Good()
	r.Windows.Channel = "Volume:GVLK"
	if !has(sysinfo.Assess(r, now), sysinfo.Warn, "volume") {
		t.Error("KMS licence on a home PC")
	}
	r.Windows.DomainJoined = sysinfo.TriYes
	if has(sysinfo.Assess(r, now), sysinfo.Warn, "volume") {
		t.Error("a volume licence in a domain is normal")
	}

	// 12. Windows 10 after the end of support, not before.
	r = sysinfofake.Good()
	r.Windows.Name = "Windows 10"
	if !has(sysinfo.Assess(r, now), sysinfo.Warn, "no longer gets security updates") {
		t.Error("Windows 10")
	}
	if has(sysinfo.Assess(r, time.Date(2025, 10, 13, 0, 0, 0, 0, time.UTC)), sysinfo.Warn, "security updates") {
		t.Error("Windows 10 flagged before its end of support")
	}

	// 13. No TPM 2.0.
	r = sysinfofake.Good()
	r.Windows.TPM, r.Windows.TPMPresent = "", sysinfo.TriNo
	if !has(sysinfo.Assess(r, now), sysinfo.Warn, "TPM 2.0") {
		t.Error("no TPM")
	}

	// 14. A branded maker with a placeholder serial; an unbranded one is not.
	r = sysinfofake.Good()
	r.System.Maker, r.System.Serial, r.System.SerialPlaceholder = "Dell Inc.", "", true
	if !has(sysinfo.Assess(r, now), sysinfo.Warn, "placeholder") {
		t.Error("placeholder serial on a Dell")
	}
	r.System.Maker = "Contoso"
	if flagged(sysinfo.Assess(r, now), "placeholder") {
		t.Error("placeholder serial on a self-built PC is normal")
	}

	// Worst first.
	for i := 1; i < len(fs); i++ {
		if fs[i].Level > fs[i-1].Level {
			t.Fatalf("not worst first: %+v", fs)
		}
	}
}

// Taking any section or any field away never adds a flag.
func TestMissingDataIsNeverAFlag(t *testing.T) {
	empty := sysinfo.Report{}
	if fs := sysinfo.Assess(empty, now); len(fs) != 0 {
		t.Fatalf("an empty report produced findings: %+v", fs)
	}
	none := sysinfofake.All(sysinfo.Report{})
	for _, f := range sysinfo.Assess(none, now) {
		if f.Level >= sysinfo.Warn {
			t.Errorf("every section arrived empty, yet: %+v", f)
		}
	}
	base := sysinfofake.Good()
	baseFlags := 0
	for _, f := range sysinfo.Assess(base, now) {
		if f.Level >= sysinfo.Warn {
			baseFlags++
		}
	}
	for _, s := range sysinfo.Sections() {
		r := sysinfofake.Good()
		r.Errs = map[sysinfo.Section]string{s: "could not read"}
		zeroSection(&r, s)
		n := 0
		for _, f := range sysinfo.Assess(r, now) {
			if f.Level >= sysinfo.Warn {
				n++
			}
		}
		if n > baseFlags {
			t.Errorf("losing %v added a flag", s)
		}
	}
	// A missing cycle count, missing EDID size and a blocked PowerShell.
	m := sysinfofake.Missing()
	m.Errs = map[sysinfo.Section]string{}
	for _, f := range sysinfo.Assess(m, now) {
		if f.Level >= sysinfo.Warn {
			t.Errorf("missing data flagged: %+v", f)
		}
	}
}

// zeroSection empties one section of r.
func zeroSection(r *sysinfo.Report, s sysinfo.Section) {
	v := reflect.ValueOf(r).Elem()
	field := map[sysinfo.Section]string{
		sysinfo.SecSystem: "System", sysinfo.SecCPU: "CPU", sysinfo.SecMemory: "Memory", sysinfo.SecGraphics: "GPUs",
		sysinfo.SecDisplays: "Displays", sysinfo.SecVolumes: "Volumes", sysinfo.SecDisks: "Disks", sysinfo.SecBattery: "Battery",
		sysinfo.SecWindows: "Windows", sysinfo.SecNetwork: "Network", sysinfo.SecDevices: "Devices",
	}[s]
	f := v.FieldByName(field)
	f.Set(reflect.Zero(f.Type()))
}

func TestReportOmitsSerialsAndMACsByDefault(t *testing.T) {
	r := sysinfofake.Desktop()
	r.Disks.Disks[0].Serial = "S6TESTSERIAL"
	secrets := []string{"CB0000TEST", "S6TESTSERIAL", "SAMTEST1", "00-11-22-33-44-55", "00-11-22-33-44-66"}
	md := sysinfo.Markdown(r, sysinfo.MarkdownOptions{Now: now, Version: "0.5.0"})
	for _, s := range secrets {
		if strings.Contains(md, s) {
			t.Errorf("the default report carries %q", s)
		}
	}
	if !strings.Contains(md, "Serial numbers: left out") {
		t.Error("the report should say serials were left out")
	}
	md = sysinfo.Markdown(r, sysinfo.MarkdownOptions{Now: now, Version: "0.5.0", Serials: true})
	for _, s := range []string{"CB0000TEST", "S6TESTSERIAL", "00-11-22-33-44-66"} {
		if !strings.Contains(md, s) {
			t.Errorf("asked for serials, %q is missing", s)
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(md), "\n") {
		if l != "" && !strings.HasPrefix(l, "## ") && !strings.HasPrefix(l, "- ") {
			t.Errorf("only headings and list lines are allowed: %q", l)
		}
	}
}

func TestFakeAndReportCarryTheWifiStandard(t *testing.T) {
	d := sysinfofake.Desktop()
	if d.Network[0].WifiStandard != "Wi-Fi 7" || d.Network[1].WifiStandard != "" {
		t.Fatalf("fake adapters: %+v", d.Network)
	}
	md := sysinfo.Markdown(d, sysinfo.MarkdownOptions{Now: now})
	if !strings.Contains(md, "- Wi-Fi standard: Wi-Fi 7\n") {
		t.Errorf("report lacks the Wi-Fi standard:\n%s", md)
	}
	if strings.Contains(md, "Ethernet standard") {
		t.Error("a wired adapter has no Wi-Fi standard line")
	}
	d.Network[0].WifiStandard = ""
	if md = sysinfo.Markdown(d, sysinfo.MarkdownOptions{Now: now}); strings.Contains(md, "standard") {
		t.Error("an unknown standard must leave the line out, not say not reported")
	}
}

func TestReportNeverHoldsTheProductKey(t *testing.T) {
	// No field anywhere in the report may be meant for a key.
	var walk func(reflect.Type, string)
	seen := map[reflect.Type]bool{}
	walk = func(tp reflect.Type, path string) {
		for tp.Kind() == reflect.Pointer || tp.Kind() == reflect.Slice || tp.Kind() == reflect.Map {
			tp = tp.Elem()
		}
		if tp.Kind() != reflect.Struct || seen[tp] {
			return
		}
		seen[tp] = true
		for i := range tp.NumField() {
			f := tp.Field(i)
			name := strings.ToLower(f.Name)
			if strings.Contains(name, "productkey") || name == "key" || strings.HasSuffix(name, "keybytes") {
				t.Errorf("%s.%s could hold a product key", path, f.Name)
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	walk(reflect.TypeOf(sysinfo.Report{}), "Report")
	md := sysinfo.Markdown(sysinfofake.Good(), sysinfo.MarkdownOptions{Now: now, Serials: true})
	if strings.Contains(strings.ToLower(md), "product key") {
		t.Error("the report mentions a product key")
	}
}

func TestGlanceReadsLikeAListing(t *testing.T) {
	g := sysinfo.Glance(sysinfofake.Good())
	want := map[string]string{
		"Processor": "Intel Core Ultra 7 155H, 16 cores / 22 threads",
		"Memory":    "32 GB DDR5 5600 MT/s, 2 of 2 slots used",
		"Storage":   "1 TB NVMe SSD",
		"Battery":   "92% health, 112 cycles, 70 Wh when new",
		"Windows":   "Windows 11 Pro 24H2, activated",
	}
	for _, l := range g {
		if w, ok := want[l.Label]; ok && l.Value != w {
			t.Errorf("%s = %q, want %q", l.Label, l.Value, w)
		}
	}
	if sysinfo.AdvertisedSize(512110190592) != "512 GB" || sysinfo.WindowsSize(512110190592) != "477 GB" {
		t.Errorf("sizes: %s %s", sysinfo.AdvertisedSize(512110190592), sysinfo.WindowsSize(512110190592))
	}
	g = sysinfo.Glance(sysinfofake.Soldered())
	for _, l := range g {
		if l.Label == "Memory" && l.Value != "16 GB LPDDR5 6400 MT/s, soldered" {
			t.Errorf("soldered memory = %q", l.Value)
		}
	}
}
