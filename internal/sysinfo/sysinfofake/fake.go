// Package sysinfofake holds made-up PCs for tests and documentation
// screenshots, and a [sysinfo.Source] that answers from one of them. Nothing
// here reads the real machine. Every serial number and MAC address in it is
// invented.
package sysinfofake

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
)

const gib = 1 << 30

// Source answers from a fixed report. A section listed in Fail returns that
// error; one with a channel in Block waits for the channel to close (or the
// context to end) first. Calls counts the reads, so a test can see a
// refresh read again.
type Source struct {
	R     sysinfo.Report
	Fail  map[sysinfo.Section]error
	Block map[sysinfo.Section]chan struct{}

	mu    *sync.Mutex
	calls *int
}

// New returns a source over r.
func New(r sysinfo.Report) Source {
	return Source{R: r, mu: &sync.Mutex{}, calls: new(int)}
}

// Calls is how many section reads were made.
func (s Source) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.calls
}

// wait counts the call, honours Block, and returns Fail's error.
func (s Source) wait(ctx context.Context, sec sysinfo.Section) error {
	if s.mu != nil {
		s.mu.Lock()
		*s.calls++
		s.mu.Unlock()
	}
	if ch := s.Block[sec]; ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.Fail[sec]
}

func answer[T any](ctx context.Context, s Source, sec sysinfo.Section, v T) (T, error) {
	if err := s.wait(ctx, sec); err != nil {
		var zero T
		return zero, err
	}
	return v, nil
}

// System implements sysinfo.Source.
func (s Source) System(ctx context.Context) (sysinfo.System, error) {
	return answer(ctx, s, sysinfo.SecSystem, s.R.System)
}

// CPU implements sysinfo.Source.
func (s Source) CPU(ctx context.Context) (sysinfo.CPU, error) {
	return answer(ctx, s, sysinfo.SecCPU, s.R.CPU)
}

// Memory implements sysinfo.Source.
func (s Source) Memory(ctx context.Context) (sysinfo.Memory, error) {
	return answer(ctx, s, sysinfo.SecMemory, s.R.Memory)
}

// Graphics implements sysinfo.Source.
func (s Source) Graphics(ctx context.Context) ([]sysinfo.GPU, error) {
	return answer(ctx, s, sysinfo.SecGraphics, s.R.GPUs)
}

// Displays implements sysinfo.Source.
func (s Source) Displays(ctx context.Context) ([]sysinfo.Display, error) {
	return answer(ctx, s, sysinfo.SecDisplays, s.R.Displays)
}

// Volumes implements sysinfo.Source.
func (s Source) Volumes(ctx context.Context) ([]sysinfo.Volume, error) {
	return answer(ctx, s, sysinfo.SecVolumes, s.R.Volumes)
}

// Disks implements sysinfo.Source.
func (s Source) Disks(ctx context.Context) (sysinfo.DiskInfo, error) {
	return answer(ctx, s, sysinfo.SecDisks, s.R.Disks)
}

// Battery implements sysinfo.Source.
func (s Source) Battery(ctx context.Context) (sysinfo.Battery, error) {
	return answer(ctx, s, sysinfo.SecBattery, s.R.Battery)
}

// Windows implements sysinfo.Source.
func (s Source) Windows(ctx context.Context) (sysinfo.Windows, error) {
	return answer(ctx, s, sysinfo.SecWindows, s.R.Windows)
}

// Network implements sysinfo.Source.
func (s Source) Network(ctx context.Context) ([]sysinfo.Adapter, error) {
	return answer(ctx, s, sysinfo.SecNetwork, s.R.Network)
}

// Devices implements sysinfo.Source.
func (s Source) Devices(ctx context.Context) (sysinfo.Devices, error) {
	return answer(ctx, s, sysinfo.SecDevices, s.R.Devices)
}

// ErrBlocked is the error a fake section gives for "PowerShell is blocked".
var ErrBlocked = errors.New("PowerShell did not run (it may be blocked on this PC)")

// All marks every section as arrived.
func All(r sysinfo.Report) sysinfo.Report {
	r.Have = map[sysinfo.Section]bool{}
	for _, s := range sysinfo.Sections() {
		r.Have[s] = true
	}
	if r.Errs == nil {
		r.Errs = map[sysinfo.Section]string{}
	}
	return r
}

// Good is a healthy laptop.
func Good() sysinfo.Report {
	return All(sysinfo.Report{
		System: sysinfo.System{
			Maker: "Contoso", Model: "Book 14 G3", Family: "Book", SKU: "CB14G3-16", Serial: "CB0000TEST",
			BoardMaker: "Contoso", BoardModel: "8A1F", BIOSVendor: "Contoso", BIOSVersion: "1.12.0",
			BIOSDate: time.Date(2025, 3, 4, 0, 0, 0, 0, time.UTC), Chassis: sysinfo.ChassisLaptop,
			Firmware: "UEFI", Uptime: 5 * time.Hour,
		},
		CPU: sysinfo.CPU{
			Name: "Intel(R) Core(TM) Ultra 7 155H", Arch: "x64", Cores: 16, Threads: 22, PCores: 6, ECores: 10,
			Sockets: 1, L2: 18 << 20, L3: 24 << 20, BaseMHz: 1400, VirtFirmware: sysinfo.TriYes,
		},
		Memory: sysinfo.Memory{
			Slots: []sysinfo.Module{
				{Locator: "DIMM A", SizeB: 16 * gib, Type: "DDR5", FormFactor: "SODIMM", SpeedMTs: 5600, ConfiguredMTs: 5600, Maker: "Samsung", Part: "M425R2GA3BB0-CWMOD", Serial: "SAMTEST1"},
				{Locator: "DIMM B", SizeB: 16 * gib, Type: "DDR5", FormFactor: "SODIMM", SpeedMTs: 5600, ConfiguredMTs: 5600, Maker: "Samsung", Part: "M425R2GA3BB0-CWMOD", Serial: "SAMTEST2"},
			},
			SlotCount: 2, InstalledB: 32 * gib, UsableB: 31*gib + 600<<20, InUseB: 12*gib + 300<<20,
		},
		GPUs: []sysinfo.GPU{{Name: "Intel(R) Arc(TM) Graphics", VendorID: "8086", DriverVersion: "32.0.101.6078", DriverDate: "9-12-2026", BuiltIn: true}},
		Displays: []sysinfo.Display{{
			Maker: "BOE", Product: "0BCA", BuiltIn: true, Connection: "built-in", Width: 2880, Height: 1800, RefreshHz: 120,
			NativeW: 2880, NativeH: 1800, MaxRefreshHz: 120, DiagonalIn: 14.0, HDR: sysinfo.TriYes, HDROn: sysinfo.TriNo, Serial: "EDIDTEST",
		}},
		Volumes: []sysinfo.Volume{{Letter: "C", FileSystem: "NTFS", TotalB: 952 * gib, FreeB: 611 * gib, WindowsIsHere: true}},
		Disks: sysinfo.DiskInfo{
			Disks: []sysinfo.Disk{{
				Number: 0, Model: "Samsung PM9A1 1TB", Serial: "S6TESTSERIAL", SizeB: 1024209543168, Media: sysinfo.MediaSSD,
				Bus: "NVMe", Health: sysinfo.HealthGood, Letters: []string{"C"}, Boot: true,
			}},
			BitLocker: map[string]sysinfo.Tri{"C": sysinfo.TriYes},
		},
		Battery: sysinfo.Battery{
			Present: sysinfo.TriYes, ChargePct: 81, OnAC: sysinfo.TriNo, Charging: sysinfo.TriNo, RatemW: -9100, TimeLeft: 6*time.Hour + 20*time.Minute,
			Packs: []sysinfo.Pack{{Name: "Primary", Maker: "Contoso", Chemistry: "Lithium-ion", Serial: "BATTEST", DesignmWh: 70000, FullmWh: 64400, Cycles: 112}},
		},
		Windows: sysinfo.Windows{
			Name: "Windows 11", Edition: "Professional", DisplayVersion: "24H2", Build: 26100, UBR: 2314,
			Installed: time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC), Activated: sysinfo.TriYes, Channel: "OEM:DM",
			OEMKeyInFirmware: sysinfo.TriYes, SecureBoot: sysinfo.TriYes, TPM: "2.0", TPMPresent: sysinfo.TriYes,
			Hypervisor: sysinfo.TriYes, DomainJoined: sysinfo.TriNo, EntraJoined: sysinfo.TriNo, MDM: sysinfo.TriNo,
		},
		Network: []sysinfo.Adapter{
			{Description: "Intel(R) Wi-Fi 7 BE200 320MHz", Kind: "Wi-Fi", SpeedBps: 1_200_000_000, Connected: true, MAC: "00-11-22-33-44-55"},
		},
		Devices: sysinfo.Devices{Bluetooth: sysinfo.TriYes, Camera: sysinfo.TriYes, Fingerprint: sysinfo.TriYes, Touch: sysinfo.TriNo, Pen: sysinfo.TriNo},
	})
}

// Flags is a used laptop with things to ask about: a worn battery, one stick
// and a free slot, Windows on a hard disk, not activated, and managed.
func Flags() sysinfo.Report {
	r := Good()
	r.Battery.Packs[0].FullmWh = 37800 // 54%
	r.Battery.Packs[0].Cycles = 913
	r.Memory.Slots = []sysinfo.Module{
		{Locator: "DIMM A", SizeB: 8 * gib, Type: "DDR4", FormFactor: "SODIMM", SpeedMTs: 3200, ConfiguredMTs: 2666, Maker: "SK hynix", Part: "HMA81GS6DJR8N-XN"},
		{Locator: "DIMM B", Empty: true},
	}
	r.Memory.InstalledB, r.Memory.UsableB, r.Memory.InUseB = 8*gib, 7*gib+700<<20, 5*gib+100<<20
	r.Disks.Disks[0] = sysinfo.Disk{
		Number: 0, Model: "ST1000LM035-1RK172", Serial: "WDTEST", SizeB: 1000204886016, Media: sysinfo.MediaHDD,
		Bus: "SATA", Health: sysinfo.HealthWarning, Letters: []string{"C"}, Boot: true,
	}
	r.Disks.BitLocker = map[string]sysinfo.Tri{"C": sysinfo.TriNo}
	r.Windows.Activated = sysinfo.TriNo
	r.Windows.Channel = "Retail"
	r.Windows.MDM = sysinfo.TriYes
	r.Windows.Autopilot = sysinfo.TriYes
	r.Windows.AutopilotTenant = "fabrikam.example"
	return r
}

// Desktop is a tower: no battery, four slots with two used, a graphics card
// and two screens.
func Desktop() sysinfo.Report {
	r := Good()
	r.System.Maker, r.System.Model, r.System.Chassis = "Fabrikam", "Studio T7", sysinfo.ChassisDesktop
	r.Battery = sysinfo.Battery{Present: sysinfo.TriNo, ChargePct: -1}
	r.Memory.Slots = []sysinfo.Module{
		{Locator: "DIMM_A1", Empty: true},
		{Locator: "DIMM_A2", SizeB: 16 * gib, Type: "DDR5", FormFactor: "DIMM", SpeedMTs: 6000, ConfiguredMTs: 6000, Maker: "G.Skill", Part: "F5-6000J3038F16G"},
		{Locator: "DIMM_B1", Empty: true},
		{Locator: "DIMM_B2", SizeB: 16 * gib, Type: "DDR5", FormFactor: "DIMM", SpeedMTs: 6000, ConfiguredMTs: 6000, Maker: "G.Skill", Part: "F5-6000J3038F16G"},
	}
	r.Memory.SlotCount = 4
	r.GPUs = []sysinfo.GPU{
		{Name: "NVIDIA GeForce RTX 4070 SUPER", VendorID: "10DE", DriverVersion: "32.0.15.6614", DriverDate: "10-2-2026", MemoryB: 12 * gib},
		{Name: "AMD Radeon(TM) Graphics", VendorID: "1002", DriverVersion: "31.0.24027.1012", BuiltIn: true, MemoryB: 512 << 20},
	}
	r.Displays = []sysinfo.Display{
		{Name: "DELL U2723QE", Maker: "Dell", Connection: "DisplayPort", Width: 3840, Height: 2160, RefreshHz: 60, NativeW: 3840, NativeH: 2160, MaxRefreshHz: 60, DiagonalIn: 27.0, HDR: sysinfo.TriYes, HDROn: sysinfo.TriNo},
		{Name: "LG ULTRAGEAR", Maker: "LG", Connection: "HDMI", Width: 2560, Height: 1440, RefreshHz: 60, NativeW: 2560, NativeH: 1440, MaxRefreshHz: 165, DiagonalIn: 27.0, HDR: sysinfo.TriNo},
	}
	r.Network = append(r.Network, sysinfo.Adapter{Description: "Realtek PCIe 2.5GbE Family Controller", Kind: "Ethernet", SpeedBps: 2_500_000_000, Connected: true, MAC: "00-11-22-33-44-66"})
	r.Devices.Camera, r.Devices.Fingerprint = sysinfo.TriNo, sysinfo.TriNo
	return r
}

// Missing is a PC that reports little: placeholder strings, no cycle count,
// no screen size, and PowerShell blocked.
func Missing() sysinfo.Report {
	r := Good()
	r.System = sysinfo.System{Chassis: sysinfo.ChassisUnknown, SerialPlaceholder: true, BIOSVersion: "1.0"}
	r.Battery.Packs[0].Cycles = 0
	r.Battery.Packs[0].Maker, r.Battery.Packs[0].Serial = "", ""
	r.Displays[0].DiagonalIn, r.Displays[0].Maker, r.Displays[0].NativeW, r.Displays[0].NativeH = 0, "", 0, 0
	r.Memory.Slots = nil
	r.Memory.SlotCount = 0
	r.Disks = sysinfo.DiskInfo{Note: "PowerShell did not answer, so the drives and their health are not reported."}
	r.Errs[sysinfo.SecDisks] = ErrBlocked.Error()
	r.Windows.Activated, r.Windows.Channel, r.Windows.Hypervisor = sysinfo.TriUnknown, "", sysinfo.TriUnknown
	r.Windows.Note = "PowerShell did not answer, so activation is not reported."
	r.Devices = sysinfo.Devices{}
	return r
}

// Soldered is a thin laptop with its memory on the board.
func Soldered() sysinfo.Report {
	r := Good()
	r.Memory.Slots = []sysinfo.Module{{Locator: "on the board", Soldered: true, SizeB: 16 * gib, Type: "LPDDR5", SpeedMTs: 6400, ConfiguredMTs: 6400, Maker: "Micron"}}
	r.Memory.SlotCount = 0
	r.Memory.InstalledB, r.Memory.UsableB = 16*gib, 15*gib+400<<20
	return r
}
