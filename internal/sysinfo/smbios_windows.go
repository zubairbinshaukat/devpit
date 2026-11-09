//go:build windows

package sysinfo

import (
	"errors"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// kernel32 procs golang.org/x/sys/windows does not wrap.
var (
	modKernel32                            = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemFirmwareTable             = modKernel32.NewProc("GetSystemFirmwareTable")
	procGlobalMemoryStatusEx               = modKernel32.NewProc("GlobalMemoryStatusEx")
	procGetPhysicallyInstalledSystemMemory = modKernel32.NewProc("GetPhysicallyInstalledSystemMemory")
	procGetLogicalProcessorInformationEx   = modKernel32.NewProc("GetLogicalProcessorInformationEx")
	procIsProcessorFeaturePresent          = modKernel32.NewProc("IsProcessorFeaturePresent")
	procGetFirmwareType                    = modKernel32.NewProc("GetFirmwareType")
	procGetTickCount64                     = modKernel32.NewProc("GetTickCount64")
	procGetSystemPowerStatus               = modKernel32.NewProc("GetSystemPowerStatus")
)

// Firmware table providers and IDs.
const (
	sigRSMB = 0x52534D42 // 'RSMB', the raw SMBIOS table
	sigACPI = 0x41435049 // 'ACPI'
	idMSDM  = 0x4D44534D // 'MSDM', the OEM Windows key table
)

// errNoTable is a firmware table that is not there.
var errNoTable = errors.New("not in the firmware")

// firmwareTable reads one firmware table.
func firmwareTable(provider, id uint32) ([]byte, error) {
	if err := procGetSystemFirmwareTable.Find(); err != nil {
		return nil, err
	}
	n, _, err := procGetSystemFirmwareTable.Call(uintptr(provider), uintptr(id), 0, 0)
	if n == 0 {
		if errors.Is(err, windows.ERROR_NOT_FOUND) || err == nil || errors.Is(err, windows.Errno(0)) {
			return nil, errNoTable
		}
		return nil, err
	}
	b := make([]byte, n)
	got, _, err := procGetSystemFirmwareTable.Call(uintptr(provider), uintptr(id), uintptr(unsafe.Pointer(&b[0])), n)
	if got == 0 || got > n {
		return nil, err
	}
	return b[:got], nil
}

// msdmPresent says whether the firmware has a Windows key table. Only the
// size is looked at; the bytes are zeroed before the buffer is dropped, so
// the key is never kept.
func msdmPresent() Tri {
	b, err := firmwareTable(sigACPI, idMSDM)
	if errors.Is(err, errNoTable) {
		return TriNo
	}
	if err != nil {
		return TriUnknown
	}
	ok := len(b) >= 36
	clear(b)
	return TriOf(ok)
}

// readSMBIOS reads and parses the SMBIOS table.
func readSMBIOS() (smbiosTable, error) {
	b, err := firmwareTable(sigRSMB, 0)
	if err != nil {
		return smbiosTable{}, err
	}
	return parseRawSMBIOS(b)
}

// systemLive reads the PC's identity from SMBIOS, falling back to the
// registry copy Windows keeps (which has no serial).
func systemLive() (System, error) {
	var s System
	t, err := readSMBIOS()
	if err == nil {
		s = systemFrom(t)
	}
	if s.Maker == "" || s.Model == "" || s.BIOSVersion == "" {
		if k, kerr := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\BIOS`, registry.QUERY_VALUE); kerr == nil {
			get := func(name string) string { v, _, _ := k.GetStringValue(name); return clean(v) }
			fill := func(dst *string, name string) {
				if *dst == "" {
					*dst = get(name)
				}
			}
			fill(&s.Maker, "SystemManufacturer")
			fill(&s.Model, "SystemProductName")
			fill(&s.Family, "SystemFamily")
			fill(&s.SKU, "SystemSKU")
			fill(&s.BoardMaker, "BaseBoardManufacturer")
			fill(&s.BoardModel, "BaseBoardProduct")
			fill(&s.BIOSVendor, "BIOSVendor")
			fill(&s.BIOSVersion, "BIOSVersion")
			if s.BIOSDate.IsZero() {
				s.BIOSDate = parseBIOSDate(get("BIOSReleaseDate"))
			}
			_ = k.Close()
			err = nil
		}
	}
	if err != nil {
		return s, err
	}
	s.Firmware = firmwareType()
	if procGetTickCount64.Find() == nil {
		ms, _, _ := procGetTickCount64.Call()
		if ms < 1<<40 {
			s.Uptime = (time.Duration(int64(ms)) * time.Millisecond).Truncate(time.Minute)
		}
	}
	return s, nil
}

// firmwareType is "UEFI", "Legacy BIOS" or "".
func firmwareType() string {
	if procGetFirmwareType.Find() != nil {
		return ""
	}
	var ft uint32
	r, _, _ := procGetFirmwareType.Call(uintptr(unsafe.Pointer(&ft)))
	if r == 0 {
		return ""
	}
	switch ft {
	case 1:
		return "Legacy BIOS"
	case 2:
		return "UEFI"
	}
	return ""
}

// cpuLive reads the processor.
func cpuLive() (CPU, error) {
	var c CPU
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err == nil {
		name, _, _ := k.GetStringValue("ProcessorNameString")
		c.Name = strings.Join(strings.Fields(name), " ")
		if mhz, _, merr := k.GetIntegerValue("~MHz"); merr == nil && mhz < 20000 {
			c.BaseMHz = int(mhz)
		}
		_ = k.Close()
	}
	c.Arch = archName()
	if procGetLogicalProcessorInformationEx.Find() == nil {
		var n uint32
		_, _, _ = procGetLogicalProcessorInformationEx.Call(0xFFFF, 0, uintptr(unsafe.Pointer(&n)))
		if n > 0 && n < 1<<22 {
			b := make([]byte, n)
			r, _, _ := procGetLogicalProcessorInformationEx.Call(0xFFFF, uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&n)))
			if r != 0 {
				tp := parseLPIEx(b[:n], int(unsafe.Sizeof(uintptr(0))))
				c.Cores, c.Threads, c.PCores, c.ECores, c.Sockets = tp.Cores, tp.Threads, tp.PCores, tp.ECores, tp.Sockets
				c.L2, c.L3 = tp.L2, tp.L3
			}
		}
	}
	if procIsProcessorFeaturePresent.Find() == nil {
		r, _, _ := procIsProcessorFeaturePresent.Call(21) // PF_VIRT_FIRMWARE_ENABLED
		c.VirtFirmware = TriOf(r != 0)
	}
	if c.Name == "" && c.Cores == 0 {
		return c, errors.New("Windows did not describe the processor")
	}
	return c, nil
}

// archName is the processor architecture Windows runs on.
func archName() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("PROCESSOR_ARCHITECTURE")
	switch strings.ToUpper(v) {
	case "AMD64":
		return "x64"
	case "ARM64":
		return "ARM64"
	case "X86":
		return "x86"
	}
	return ""
}

// memoryStatusEx is MEMORYSTATUSEX (64 bytes).
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// memoryLive reads the slots from SMBIOS and the totals from Windows.
func memoryLive() (Memory, error) {
	var m Memory
	t, serr := readSMBIOS()
	if serr == nil {
		m = memoryFrom(t)
	}
	if procGetPhysicallyInstalledSystemMemory.Find() == nil {
		var kb uint64
		if r, _, _ := procGetPhysicallyInstalledSystemMemory.Call(uintptr(unsafe.Pointer(&kb))); r != 0 {
			m.InstalledB = kb << 10
		}
	}
	if procGlobalMemoryStatusEx.Find() == nil {
		ms := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
		if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms))); r != 0 {
			m.UsableB = ms.TotalPhys
			if ms.TotalPhys >= ms.AvailPhys {
				m.InUseB = ms.TotalPhys - ms.AvailPhys
			}
		}
	}
	if m.InstalledB == 0 {
		var sum uint64
		for _, s := range m.Slots {
			sum += s.SizeB
		}
		m.InstalledB = sum
	}
	if serr != nil && m.UsableB == 0 {
		return m, serr
	}
	return m, nil
}
