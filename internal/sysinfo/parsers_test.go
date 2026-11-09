package sysinfo

import (
	"context"
	"encoding/binary"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// buildEDID is a synthetic 128-byte EDID: maker, product, a first detailed
// timing of w x h at about hz, a size in mm and a name descriptor.
func buildEDID(maker string, product uint16, w, h, hz, wmm, hmm int, name string) []byte {
	b := make([]byte, 128)
	copy(b, []byte{0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0})
	id := uint16(maker[0]-'A'+1)<<10 | uint16(maker[1]-'A'+1)<<5 | uint16(maker[2]-'A'+1)
	binary.BigEndian.PutUint16(b[8:], id)
	binary.LittleEndian.PutUint16(b[10:], product)
	b[21], b[22] = byte(wmm/10), byte(hmm/10)
	hB, vB := 160, 40
	clock := (w + hB) * (h + vB) * hz / 10000
	d := b[54:]
	binary.LittleEndian.PutUint16(d[0:], uint16(clock))
	d[2], d[3], d[4] = byte(w), byte(hB), byte((w>>8)<<4|(hB>>8))
	d[5], d[6], d[7] = byte(h), byte(vB), byte((h>>8)<<4|(vB>>8))
	d[12], d[13], d[14] = byte(wmm), byte(hmm), byte((wmm>>8)<<4|(hmm>>8))
	n := b[72:]
	n[3] = 0xFC
	copy(n[5:], (name + "\n             ")[:13])
	s := b[90:]
	s[3] = 0xFF
	copy(s[5:], "ABC123456789\n")
	return b
}

func TestEDIDNativeModeSizeName(t *testing.T) {
	e, err := parseEDID(buildEDID("CMN", 0x1602, 1920, 1080, 144, 355, 199, "Panel X"))
	if err != nil {
		t.Fatal(err)
	}
	if e.Maker != "CMN" || pnpMaker(e.Maker) != "Innolux" || e.Product != "1602" {
		t.Fatalf("maker/product = %q %q", e.Maker, e.Product)
	}
	if e.NativeW != 1920 || e.NativeH != 1080 || e.NativeHz < 143 || e.NativeHz > 145 {
		t.Fatalf("native = %dx%d@%d", e.NativeW, e.NativeH, e.NativeHz)
	}
	if e.Name != "Panel X" || e.Serial != "ABC123456789" {
		t.Fatalf("name/serial = %q %q", e.Name, e.Serial)
	}
	if d := e.diagonal(); d < 16.0 || d > 16.1 {
		t.Fatalf("diagonal = %v, want about 16.0", d)
	}
}

func TestBadEDIDIsPartialNotFatal(t *testing.T) {
	if _, err := parseEDID([]byte{1, 2, 3}); err == nil {
		t.Fatal("a short block should be an error")
	}
	b := buildEDID("AUO", 1, 2560, 1600, 60, 0, 0, "")
	for i := 54 + 12; i <= 54+14; i++ {
		b[i] = 0 // no size in the timing
	}
	b[21], b[22] = 0, 0 // nor in the header
	e, err := parseEDID(b)
	if err != nil {
		t.Fatal(err)
	}
	if e.diagonal() != 0 || e.NativeW != 2560 {
		t.Fatalf("size should be not reported while the mode is kept: %+v", e)
	}
	b[8], b[9] = 0, 0 // nonsense maker letters
	if e, _ := parseEDID(b); e.Maker != "" {
		t.Fatalf("bad maker code kept: %q", e.Maker)
	}
}

func TestBatteryHealthClampsAndRelativeUnits(t *testing.T) {
	b := Battery{Packs: []Pack{{DesignmWh: 83028, FullmWh: 64523}}}
	if pct, ok := b.Health(); !ok || pct != 78 {
		t.Fatalf("health = %d %v, want 78", pct, ok)
	}
	b = Battery{Packs: []Pack{{DesignmWh: 50000, FullmWh: 52000}}}
	if pct, _ := b.Health(); pct != 100 {
		t.Fatalf("a new battery over its design should read 100, got %d", pct)
	}
	b = Battery{Packs: []Pack{{DesignmWh: 40000, FullmWh: 30000}, {DesignmWh: 20000, FullmWh: 20000}}}
	if pct, _ := b.Health(); pct != 83 {
		t.Fatalf("two packs sum: got %d", pct)
	}
	b = Battery{Packs: []Pack{{DesignmWh: 100, FullmWh: 54, Relative: true}}}
	_, _, rel := b.Totals()
	if pct, ok := b.Health(); !ok || pct != 54 || !rel {
		t.Fatalf("relative units: %d %v %v", pct, ok, rel)
	}
	if _, ok := (Battery{Packs: []Pack{{DesignmWh: 0, FullmWh: 5}}}).Health(); ok {
		t.Fatal("no design capacity is not reported, not 0%")
	}
	if _, ok := (Battery{Packs: []Pack{{DesignmWh: batteryUnknownCapacity, FullmWh: 5}}}).Health(); ok {
		t.Fatal("unknown capacity is not reported")
	}
}

func TestZeroCyclesIsNotReported(t *testing.T) {
	if _, ok := (Battery{Packs: []Pack{{Cycles: 0}}}).Cycles(); ok {
		t.Fatal("zero cycles must be not reported")
	}
	if n, ok := (Battery{Packs: []Pack{{Cycles: 154}}}).Cycles(); !ok || n != 154 {
		t.Fatalf("cycles = %d %v", n, ok)
	}
}

func TestChargeEstimates(t *testing.T) {
	var b Battery
	chargeFrom(&b, batteryPowerOnLine|batteryCharging, 30000, 60000, 30000, true, 50, -1)
	if b.TimeToFull != time.Hour || b.TimeLeft != 0 || b.Charging != TriYes {
		t.Fatalf("charging = %+v", b)
	}
	b = Battery{}
	chargeFrom(&b, 0, 30000, 60000, -10000, true, 50, 3*3600)
	if b.TimeLeft != 3*time.Hour || b.TimeToFull != 0 || b.OnAC != TriNo {
		t.Fatalf("on battery = %+v", b)
	}
	b = Battery{}
	chargeFrom(&b, batteryPowerOnLine|batteryCharging, 30000, 60000, 0, false, 255, -1)
	if b.TimeToFull != 0 || b.ChargePct != -1 {
		t.Fatalf("unknown rate and charge: %+v", b)
	}
}

func TestGPUBuiltInHeuristicTable(t *testing.T) {
	for _, c := range []struct {
		hwid, name string
		mem        uint64
		want       bool
	}{
		{`PCI\VEN_10DE&DEV_2520`, "NVIDIA GeForce RTX 3060 Laptop GPU", 6 * gib, false},
		{`PCI\VEN_8086&DEV_9A60`, "Intel(R) UHD Graphics", 0, true},
		{`PCI\VEN_8086&DEV_56A0`, "Intel(R) Arc(TM) A770 Graphics", 16 * gib, false},
		{`PCI\VEN_1002&DEV_1681`, "AMD Radeon(TM) Graphics", 512 << 20, true},
		{`PCI\VEN_1002&DEV_15BF`, "AMD Radeon 780M Graphics", 0, true},
		{`PCI\VEN_1002&DEV_73DF`, "AMD Radeon RX 6700 XT", 12 * gib, false},
		{`ACPI\VEN_QCOM&DEV_043A`, "Qualcomm(R) Adreno(TM) X1-85 GPU", 0, true},
		{`PCI\VEN_1A03&DEV_2000`, "ASPEED Graphics Family", 64 << 20, true},
	} {
		if got := builtInGPU(vendorOf(c.hwid), c.name, c.mem); got != c.want {
			t.Errorf("%s: built-in = %v, want %v", c.name, got, c.want)
		}
	}
	if realAdapter("Microsoft Basic Display Adapter", `PCI\VEN_1414`) || realAdapter("Parsec Virtual Display", `ROOT\Parsec`) {
		t.Fatal("fallback and virtual adapters must be skipped")
	}
	if !realAdapter("Intel(R) UHD Graphics", `pci\ven_8086&dev_9a60`) {
		t.Fatal("a real adapter was skipped")
	}
}

func TestParseLPIExHybridCores(t *testing.T) {
	core := func(class byte, threads int) []byte {
		r := make([]byte, 48)
		binary.LittleEndian.PutUint32(r[0:], relProcessorCore)
		binary.LittleEndian.PutUint32(r[4:], 48)
		r[9] = class
		binary.LittleEndian.PutUint16(r[30:], 1)
		binary.LittleEndian.PutUint64(r[32:], (1<<threads)-1)
		return r
	}
	cache := func(level byte, size uint32) []byte {
		r := make([]byte, 48)
		binary.LittleEndian.PutUint32(r[0:], relCache)
		binary.LittleEndian.PutUint32(r[4:], 48)
		r[8] = level
		binary.LittleEndian.PutUint32(r[12:], size)
		return r
	}
	pkg := make([]byte, 48)
	binary.LittleEndian.PutUint32(pkg[0:], relProcessorPackage)
	binary.LittleEndian.PutUint32(pkg[4:], 48)
	var b []byte
	for range 2 {
		b = append(b, core(1, 2)...)
	}
	for range 4 {
		b = append(b, core(0, 1)...)
	}
	b = append(b, cache(2, 1<<20)...)
	b = append(b, cache(2, 2<<20)...)
	b = append(b, cache(3, 12<<20)...)
	b = append(b, pkg...)
	b = append(b, 0, 0, 0) // a torn tail is ignored
	tp := parseLPIEx(b, 8)
	if tp.Cores != 6 || tp.Threads != 8 || tp.PCores != 2 || tp.ECores != 4 || tp.Sockets != 1 {
		t.Fatalf("topology = %+v", tp)
	}
	if tp.L2 != 3<<20 || tp.L3 != 12<<20 {
		t.Fatalf("caches = %d %d", tp.L2, tp.L3)
	}
	if one := parseLPIEx(core(0, 2), 8); one.PCores != 0 || one.ECores != 0 {
		t.Fatal("one kind of core has no P/E split")
	}
}

func TestParseBatchSingleObjectArraysAndBlockErrors(t *testing.T) {
	// Windows PowerShell 5.1 writes a one-item list as a bare object.
	out := []byte("WARNING: something\r\n" + `{"disks":{"id":"0","name":"NVMe KXG6","serial":"  1234_ABCD ","media":4,"bus":17,"health":0,"size":512110190592},` +
		`"parts":[{"disk":0,"letter":"","boot":false},{"disk":0,"letter":"C","boot":true}],` +
		`"bitlocker":{"letter":"C","value":2},"lic":{"status":1,"channel":"Volume:GVLK"},"hv":true,` +
		`"errors":{"rel":"Access denied"}}`)
	b, err := parseBatch(out)
	if err != nil {
		t.Fatal(err)
	}
	di := b.diskInfo()
	if len(di.Disks) != 1 {
		t.Fatalf("disks = %+v", di.Disks)
	}
	d := di.Disks[0]
	if d.Number != 0 || d.Media != MediaSSD || d.Bus != "NVMe" || d.Health != HealthGood || !d.Boot || len(d.Letters) != 1 || d.Letters[0] != "C" {
		t.Fatalf("disk = %+v", d)
	}
	if d.Rel != nil {
		t.Fatal("no counters were read")
	}
	if di.BitLocker["C"] != TriNo {
		t.Fatalf("bitlocker = %v", di.BitLocker)
	}
	if act, ch := b.licence(); act != TriYes || ch != "Volume:GVLK" {
		t.Fatalf("licence = %v %q", act, ch)
	}
	if b.HV == nil || !*b.HV {
		t.Fatal("hypervisor flag lost")
	}

	// A blocked block is a note, never a guess.
	b, err = parseBatch([]byte(`{"errors":{"disks":"blocked","lic":"blocked","bitlocker":"blocked"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if di := b.diskInfo(); len(di.Disks) != 0 || di.Note == "" {
		t.Fatalf("blocked disks = %+v", di)
	}
	if act, _ := b.licence(); act != TriUnknown {
		t.Fatal("a blocked licence query must be unknown")
	}
	if _, err := parseBatch([]byte("garbage")); err == nil {
		t.Fatal("no JSON should be an error")
	}
}

func TestReliabilityRoundTripAndRanges(t *testing.T) {
	rs, err := parseReliability([]byte(`{"disk":0,"wear":3,"hours":1200,"read_errors":0}`))
	if err != nil || len(rs) != 1 || rs[0].WearPct != 3 {
		t.Fatalf("one object: %+v %v", rs, err)
	}
	rs, _ = parseReliability([]byte(`[{"disk":0,"wear":250,"hours":-5,"read_errors":-1}]`))
	if rs[0].WearPct != -1 || rs[0].PowerOnHours != -1 {
		t.Fatalf("out of range values should be not reported: %+v", rs)
	}
	s, err := EncodeReliability([]Reliability{{Disk: 1, WearPct: 10, PowerOnHours: 5, ReadErrors: 0}})
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeReliability(s)
	if err != nil || back[0].Disk != 1 || back[0].WearPct != 10 {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	di := MergeReliability(DiskInfo{Disks: []Disk{{Number: 1}, {Number: 2}}}, back)
	if di.Disks[0].Rel == nil || di.Disks[1].Rel != nil {
		t.Fatal("counters landed on the wrong disk")
	}
	if _, err := DecodeReliability(strings.Repeat("x", maxReliabilityBytes+1)); err == nil {
		t.Fatal("an oversized answer must be refused")
	}
}

// The batch and the admin read may only call cmdlets that read.
func TestScriptCallsOnlyReadCmdlets(t *testing.T) {
	allowed := map[string]bool{
		"Get-CimInstance": true, "Get-PhysicalDisk": true, "Get-StorageReliabilityCounter": true,
		"ConvertTo-Json": true, "New-Object": true, "ForEach-Object": true,
	}
	cmdlet := regexp.MustCompile(`\b[A-Z][a-z]+-[A-Z][A-Za-z]+\b`)
	for name, s := range map[string]string{"batch": batchScript, "reliability": reliabilityScript} {
		for _, c := range cmdlet.FindAllString(s, -1) {
			if !allowed[c] {
				t.Errorf("%s script calls %s, which is not on the read-only list", name, c)
			}
		}
		for _, banned := range []string{"Set-", "Remove-", "Invoke-", "Start-", "Stop-", "New-Item", "Out-File", "iex", "Enable-", "Disable-", "Clear-", "Format-", "Initialize-", "Repair-", "Optimize-", "Reset-", "Update-", "Write-", "Add-", "manage-bde", "PartialProductKey |", "ProductKey\""} {
			if strings.Contains(s, banned) {
				t.Errorf("%s script contains %q", name, banned)
			}
		}
		if n := strings.Count(s, "New-Object"); n > 0 && n != strings.Count(s, "New-Object -ComObject Shell.Application") {
			t.Errorf("%s script makes an object other than Shell.Application", name)
		}
	}
	if strings.Contains(reliabilityScript, "Temperature") || strings.Contains(batchScript, "Temperature") {
		t.Error("temperature is never read")
	}
}

func TestBoundedReturnsWhenTheCallHangs(t *testing.T) {
	hang := make(chan struct{})
	defer close(hang)
	start := time.Now()
	_, err := Bounded(context.Background(), 50*time.Millisecond, func(context.Context) (int, error) {
		<-hang
		return 1, nil
	})
	if !errors.Is(err, ErrTimeout) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, cerr := Bounded(ctx, time.Minute, func(context.Context) (int, error) { <-hang; return 0, nil }); !errors.Is(cerr, context.Canceled) {
		t.Fatalf("cancelled: %v", cerr)
	}
	v, err := Bounded(context.Background(), time.Second, func(context.Context) (int, error) { return 7, nil })
	if v != 7 || err != nil {
		t.Fatalf("answer = %d %v", v, err)
	}
}

func TestSysinfoHasNoNetworkOrUIImport(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	for _, goos := range []string{"windows", "linux"} {
		// golang.org/x/sys/windows itself imports net for its socket types, so
		// net is checked on sysinfo's own imports and the rest on everything.
		direct := exec.Command(goBin, "list", "-f", "{{join .Imports \" \"}}", ".") //nolint:gosec // test
		direct.Env = append(direct.Environ(), "GOOS="+goos)
		own, err := direct.Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range strings.Fields(string(own)) {
			if imp == "net" || strings.HasPrefix(imp, "net/") || imp == "golang.org/x/sys/windows/svc" {
				t.Errorf("%s: sysinfo imports %s", goos, imp)
			}
		}
		cmd := exec.Command(goBin, "list", "-deps", ".") //nolint:gosec // test
		cmd.Env = append(cmd.Environ(), "GOOS="+goos)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, dep := range strings.Fields(string(out)) {
			if strings.HasPrefix(dep, "net/http") || strings.HasPrefix(dep, "net/rpc") || strings.HasPrefix(dep, "net/smtp") || dep == "crypto/tls" {
				t.Errorf("%s: sysinfo imports %s", goos, dep)
			}
			for _, banned := range []string{"bubbletea", "lipgloss", "/internal/ui", "/internal/app", "/internal/elevate", "cobra"} {
				if strings.Contains(dep, banned) {
					t.Errorf("%s: sysinfo imports %s", goos, dep)
				}
			}
		}
	}
}

func TestPlaceholderMakerHelpers(t *testing.T) {
	if !brandedMaker("HP") || !brandedMaker("LENOVO") || brandedMaker("To be filled") {
		t.Fatal("branded maker table")
	}
	if chemistry("LION") != "Lithium-ion" || chemistry("\x00\x00") != "" {
		t.Fatal("chemistry names")
	}
	if atoiOr("12", -1) != 12 || atoiOr("x", -1) != -1 {
		t.Fatal("atoiOr")
	}
}
