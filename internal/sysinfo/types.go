package sysinfo

import "time"

// Tri is a yes/no answer that may not be known.
type Tri int8

// The three answers. The zero value is unknown, so a field nobody filled in
// never claims anything.
const (
	TriUnknown Tri = iota
	TriYes
	TriNo
)

// TriOf turns a bool into a known answer.
func TriOf(b bool) Tri {
	if b {
		return TriYes
	}
	return TriNo
}

// Section is one part of the report, read on its own.
type Section int

// The sections, in the order the page reads them.
const (
	SecSystem Section = iota
	SecCPU
	SecMemory
	SecGraphics
	SecDisplays
	SecVolumes
	SecDisks
	SecBattery
	SecWindows
	SecNetwork
	SecDevices
)

// Sections lists every section once.
func Sections() []Section {
	return []Section{
		SecSystem, SecCPU, SecMemory, SecGraphics, SecDisplays, SecVolumes,
		SecDisks, SecBattery, SecWindows, SecNetwork, SecDevices,
	}
}

// String names a section for errors and tests.
func (s Section) String() string {
	switch s {
	case SecSystem:
		return "system"
	case SecCPU:
		return "processor"
	case SecMemory:
		return "memory"
	case SecGraphics:
		return "graphics"
	case SecDisplays:
		return "displays"
	case SecVolumes:
		return "volumes"
	case SecDisks:
		return "disks"
	case SecBattery:
		return "battery"
	case SecWindows:
		return "windows"
	case SecNetwork:
		return "network"
	case SecDevices:
		return "devices"
	}
	return "unknown"
}

// Chassis is the kind of case the firmware says the PC is in.
type Chassis int

// The chassis kinds Devpit tells apart.
const (
	ChassisUnknown Chassis = iota
	ChassisLaptop
	ChassisConvertible
	ChassisDetachable
	ChassisTablet
	ChassisDesktop
	ChassisAllInOne
	ChassisOther
)

// Portable reports whether the case is one that should have a battery.
func (c Chassis) Portable() bool {
	switch c {
	case ChassisLaptop, ChassisConvertible, ChassisDetachable, ChassisTablet:
		return true
	}
	return false
}

// String is the chassis in a few words.
func (c Chassis) String() string {
	switch c {
	case ChassisLaptop:
		return "laptop"
	case ChassisConvertible:
		return "convertible laptop"
	case ChassisDetachable:
		return "detachable laptop"
	case ChassisTablet:
		return "tablet"
	case ChassisDesktop:
		return "desktop"
	case ChassisAllInOne:
		return "all-in-one"
	case ChassisOther:
		return "other"
	}
	return ""
}

// System is who made the PC and its firmware.
type System struct {
	Maker, Model, Family, SKU string
	// Serial is private: shown on screen, exported only after a yes. It is
	// empty when the firmware holds a placeholder; SerialPlaceholder then
	// says so.
	Serial            string
	SerialPlaceholder bool
	BoardMaker        string
	BoardModel        string
	BoardSerial       string
	BIOSVendor        string
	BIOSVersion       string
	// BIOSDate is the firmware's release date, zero when not reported.
	BIOSDate time.Time
	Chassis  Chassis
	// Firmware is "UEFI", "Legacy BIOS" or "".
	Firmware string
	// Uptime is the time since Windows last started (Fast Startup counts a
	// shutdown as a sleep, so it can be days).
	Uptime time.Duration
}

// CPU is the processor.
type CPU struct {
	Name string
	// Arch is "x64", "ARM64", "x86" or "".
	Arch string
	// Cores and Threads are 0 when not reported. PCores and ECores are set
	// only when the processor has two kinds of core.
	Cores, Threads, PCores, ECores, Sockets int
	// L2 and L3 are the total cache sizes in bytes.
	L2, L3 uint64
	// BaseMHz is the clock Windows reads at start, close to the base clock.
	BaseMHz int
	// VirtFirmware is IsProcessorFeaturePresent(PF_VIRT_FIRMWARE_ENABLED).
	// It reads "no" while Windows' own hypervisor is running, so the screen
	// pairs it with [Windows.Hypervisor].
	VirtFirmware Tri
}

// Module is one memory slot.
type Module struct {
	// Locator is the slot's name on the board, such as "DIMM A1".
	Locator string
	Bank    string
	// Empty is a slot with nothing in it.
	Empty bool
	SizeB uint64
	// Type is "DDR4", "LPDDR5" and so on, or "".
	Type string
	// FormFactor is "SODIMM", "DIMM", "CAMM" or "".
	FormFactor string
	// Soldered is memory on the board that cannot be taken out. A soldered
	// row may stand for several chips the firmware lists one by one.
	Soldered bool
	// SpeedMTs is the rated speed, ConfiguredMTs what it runs at now.
	SpeedMTs, ConfiguredMTs int
	Maker, Part             string
	// Serial is private.
	Serial string
}

// Memory is the memory: slots from the firmware, totals from Windows.
type Memory struct {
	// Slots lists every slot the firmware reports, empty ones included, with
	// soldered chips folded into one row.
	Slots []Module
	// SlotCount is how many sockets the board says it has, or 0.
	SlotCount int
	// InstalledB is the memory fitted, UsableB what Windows can use and
	// InUseB what is in use right now. 0 is not reported.
	InstalledB, UsableB, InUseB uint64
}

// GPU is one graphics adapter.
type GPU struct {
	Name string
	// VendorID is the PCI vendor, "10DE", "8086", "1002", or "QCOM".
	VendorID      string
	DriverVersion string
	DriverDate    string
	// MemoryB is the adapter's own memory, 0 when not reported.
	MemoryB uint64
	// BuiltIn is a rule-of-thumb guess from the vendor, the name and the
	// memory: graphics in the processor rather than a card of its own.
	BuiltIn bool
}

// Display is one active screen.
type Display struct {
	// Name is the monitor's own name, or "" (built-in panels usually have
	// none).
	Name string
	// Maker is the panel maker's name, or its three-letter code.
	Maker string
	// Product is the EDID product code in hex, or "".
	Product string
	BuiltIn bool
	// Connection is "HDMI", "DisplayPort", "built-in" and so on.
	Connection string
	// Width, Height and RefreshHz are the mode in use now.
	Width, Height int
	RefreshHz     int
	// NativeW and NativeH are the panel's own resolution from its EDID, and
	// MaxRefreshHz the highest refresh Windows offers at it. 0 is not
	// reported.
	NativeW, NativeH int
	MaxRefreshHz     int
	// DiagonalIn is the screen size in inches, 0 when the EDID says nothing.
	DiagonalIn float64
	// HDR is whether the screen can show HDR, and HDROn whether it does now.
	HDR, HDROn Tri
	// Serial is private.
	Serial string
}

// Volume is one drive letter.
type Volume struct {
	Letter        string
	Label         string
	FileSystem    string
	TotalB, FreeB uint64
	Removable     bool
	WindowsIsHere bool
	BitLocker     Tri
}

// Media is what a physical disk is made of.
type Media int

// The media kinds.
const (
	MediaUnknown Media = iota
	MediaHDD
	MediaSSD
)

// Health is what Windows says about a disk.
type Health int

// The disk health answers.
const (
	HealthUnknown Health = iota
	HealthGood
	HealthWarning
	HealthBad
)

// Disk is one physical drive.
type Disk struct {
	// Number is the disk number Windows uses ("Disk 0").
	Number int
	Model  string
	// Serial is private.
	Serial string
	SizeB  uint64
	Media  Media
	// Bus is "NVMe", "SATA", "USB" and so on, or "".
	Bus    string
	Health Health
	// Letters are the drive letters on it.
	Letters []string
	// Boot is the disk Windows started from.
	Boot bool
	// Rel is the wear and error counters, nil until read (they need admin).
	Rel *Reliability
}

// Reliability is the counters a drive keeps about itself. -1 means the
// drive did not report that one. Temperature is deliberately not here.
type Reliability struct {
	// Disk is the disk number it belongs to.
	Disk int `json:"disk"`
	// WearPct is the share of rated life used, 0 to 100 (NVMe "percentage
	// used").
	WearPct int `json:"wear"`
	// PowerOnHours is how long the drive has been on.
	PowerOnHours int64 `json:"hours"`
	// ReadErrors is the uncorrected read error count.
	ReadErrors int64 `json:"read_errors"`
}

// DiskInfo is the PowerShell half of storage: the drives and the BitLocker
// state of each letter.
type DiskInfo struct {
	Disks []Disk
	// BitLocker maps a letter ("C") to whether BitLocker protects it.
	BitLocker map[string]Tri
	// Note says why part of it is missing, in plain words, or "".
	Note string
}

// Pack is one battery.
type Pack struct {
	Name, Maker, Chemistry string
	// Serial is private.
	Serial string
	// DesignmWh and FullmWh are the capacity it was made with and what it
	// holds now. When Relative is set they are not in mWh, and only the
	// ratio means anything.
	DesignmWh, FullmWh uint64
	Relative           bool
	// Cycles is the charge cycle count, 0 when not reported.
	Cycles int
}

// Battery is every battery and the charge now.
type Battery struct {
	// Present is no when Windows says there is no battery at all.
	Present Tri
	Packs   []Pack
	// ChargePct is the charge now, -1 when not reported.
	ChargePct int
	OnAC      Tri
	Charging  Tri
	// RatemW is how fast it charges (positive) or drains (negative), 0 when
	// not reported.
	RatemW int
	// TimeLeft is Windows' estimate on battery, TimeToFull an estimate while
	// charging; 0 when not known.
	TimeLeft, TimeToFull time.Duration
}

// Windows is the installed Windows, its licence and who manages it.
type Windows struct {
	// Name is "Windows 11" or "Windows 10", worked out from the build.
	Name           string
	Edition        string
	DisplayVersion string
	Build, UBR     int
	// Installed is when Windows was installed or last reset.
	Installed time.Time
	Activated Tri
	// Channel is the licence channel: "OEM:DM", "Retail", "Volume:GVLK",
	// "Volume:MAK" and so on, or "".
	Channel string
	// OEMKeyInFirmware says whether the firmware carries a Windows key
	// table. The key itself is never read.
	OEMKeyInFirmware Tri
	SecureBoot       Tri
	// TPM is "2.0", "1.2" or "" with TPMPresent no.
	TPM        string
	TPMPresent Tri
	Hypervisor Tri
	// Domain is the domain the PC is joined to, or "".
	Domain          string
	DomainJoined    Tri
	EntraJoined     Tri
	MDM             Tri
	Autopilot       Tri
	AutopilotTenant string
	// Note says why part of it is missing, in plain words, or "".
	Note string
}

// Adapter is one physical network adapter.
type Adapter struct {
	// Description is the adapter's own name, which usually names its Wi-Fi
	// standard ("Wi-Fi 6 AX201").
	Description string
	// Kind is "Wi-Fi", "Ethernet" or "Other".
	Kind      string
	SpeedBps  uint64
	Connected bool
	// MAC is private.
	MAC string
}

// Devices is what else is built in.
type Devices struct {
	Bluetooth, Camera, Fingerprint Tri
	Touch, Pen                     Tri
	MaxTouches                     int
}

// Report is everything read, one section at a time. A section that failed
// has its zero value and a reason in Errs.
type Report struct {
	Read     time.Time
	System   System
	CPU      CPU
	Memory   Memory
	GPUs     []GPU
	Displays []Display
	Volumes  []Volume
	Disks    DiskInfo
	Battery  Battery
	Windows  Windows
	Network  []Adapter
	Devices  Devices
	// Have marks the sections that arrived, failed or not.
	Have map[Section]bool
	// Errs holds the reason for each section that could not be read.
	Errs map[Section]string
}

// Got reports whether a section arrived without an error.
func (r Report) Got(s Section) bool { return r.Have[s] && r.Errs[s] == "" }
