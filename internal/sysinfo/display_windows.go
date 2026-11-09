//go:build windows

package sysinfo

import (
	"errors"
	"math"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// user32 procs for the display configuration.
var (
	modUser32                       = windows.NewLazySystemDLL("user32.dll")
	procGetDisplayConfigBufferSizes = modUser32.NewProc("GetDisplayConfigBufferSizes")
	procQueryDisplayConfig          = modUser32.NewProc("QueryDisplayConfig")
	procDisplayConfigGetDeviceInfo  = modUser32.NewProc("DisplayConfigGetDeviceInfo")
	procEnumDisplaySettingsW        = modUser32.NewProc("EnumDisplaySettingsW")
	procGetSystemMetrics            = modUser32.NewProc("GetSystemMetrics")
)

const (
	qdcOnlyActivePaths = 0x2

	dcGetSourceName        = 1
	dcGetTargetName        = 2
	dcGetAdvancedColorInfo = 9

	outputInternal = 0x80000000

	enumCurrentSettings = 0xFFFFFFFF
)

// luid is LUID.
type luid struct {
	Low  uint32
	High int32
}

// dcRational is DISPLAYCONFIG_RATIONAL.
type dcRational struct{ Num, Den uint32 }

// dcPathInfo is DISPLAYCONFIG_PATH_INFO (72 bytes).
type dcPathInfo struct {
	SrcAdapter     luid
	SrcID          uint32
	SrcModeIdx     uint32
	SrcFlags       uint32
	TgtAdapter     luid
	TgtID          uint32
	TgtModeIdx     uint32
	OutputTech     uint32
	Rotation       uint32
	Scaling        uint32
	Refresh        dcRational
	ScanLine       uint32
	TargetAvail    int32
	TgtStatusFlags uint32
	Flags          uint32
}

// dcModeInfo is DISPLAYCONFIG_MODE_INFO (64 bytes); the union is not read.
type dcModeInfo struct {
	InfoType  uint32
	ID        uint32
	AdapterID luid
	Union     [48]byte
}

// dcHeader is DISPLAYCONFIG_DEVICE_INFO_HEADER (20 bytes).
type dcHeader struct {
	Type      uint32
	Size      uint32
	AdapterID luid
	ID        uint32
}

// dcTargetName is DISPLAYCONFIG_TARGET_DEVICE_NAME (420 bytes).
type dcTargetName struct {
	Header           dcHeader
	Flags            uint32
	OutputTech       uint32
	EdidMaker        uint16
	EdidProduct      uint16
	ConnectorInst    uint32
	FriendlyName     [64]uint16
	MonitorDevicPath [128]uint16
}

// dcSourceName is DISPLAYCONFIG_SOURCE_DEVICE_NAME (84 bytes).
type dcSourceName struct {
	Header  dcHeader
	GDIName [32]uint16
}

// dcAdvancedColor is DISPLAYCONFIG_GET_ADVANCED_COLOR_INFO (32 bytes).
type dcAdvancedColor struct {
	Header   dcHeader
	Value    uint32
	Encoding uint32
	Bits     uint32
}

// devMode is the part of DEVMODEW the display reads use; the rest is
// padding so the size is the documented 220 bytes.
type devMode struct {
	DeviceName    [32]uint16
	SpecVersion   uint16
	DriverVersion uint16
	Size          uint16
	DriverExtra   uint16
	Fields        uint32
	_             [16]byte
	_             [10]byte
	FormName      [32]uint16
	LogPixels     uint16
	BitsPerPel    uint32
	PelsWidth     uint32
	PelsHeight    uint32
	DisplayFlags  uint32
	Frequency     uint32
	_             [32]byte
}

// displaysLive lists the active screens.
func displaysLive() ([]Display, error) {
	if err := procQueryDisplayConfig.Find(); err != nil {
		return nil, err
	}
	var np, nm uint32
	if r, _, _ := procGetDisplayConfigBufferSizes.Call(qdcOnlyActivePaths, uintptr(unsafe.Pointer(&np)), uintptr(unsafe.Pointer(&nm))); r != 0 {
		return nil, windows.Errno(r)
	}
	if np == 0 {
		return nil, nil
	}
	paths := make([]dcPathInfo, np)
	modes := make([]dcModeInfo, max(nm, 1))
	if r, _, _ := procQueryDisplayConfig.Call(qdcOnlyActivePaths, uintptr(unsafe.Pointer(&np)), uintptr(unsafe.Pointer(&paths[0])),
		uintptr(unsafe.Pointer(&nm)), uintptr(unsafe.Pointer(&modes[0])), 0); r != 0 {
		return nil, windows.Errno(r)
	}
	var out []Display
	for _, p := range paths[:np] {
		out = append(out, displayFor(p))
	}
	if len(out) == 0 {
		return nil, errors.New("Windows listed no screens")
	}
	return out, nil
}

// displayFor reads one path's screen.
func displayFor(p dcPathInfo) Display {
	var d Display
	tn := dcTargetName{Header: dcHeader{Type: dcGetTargetName, Size: uint32(unsafe.Sizeof(dcTargetName{})), AdapterID: p.TgtAdapter, ID: p.TgtID}}
	tech := p.OutputTech
	devPath := ""
	if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&tn))); r == 0 {
		d.Name = strings.TrimSpace(windows.UTF16ToString(tn.FriendlyName[:]))
		tech = tn.OutputTech
		devPath = windows.UTF16ToString(tn.MonitorDevicPath[:])
	}
	d.BuiltIn = tech == outputInternal || tech == 6 || tech == 11 || tech == 13
	d.Connection = connectionName(tech)
	if p.Refresh.Den != 0 {
		d.RefreshHz = int(math.Round(float64(p.Refresh.Num) / float64(p.Refresh.Den)))
	}

	ac := dcAdvancedColor{Header: dcHeader{Type: dcGetAdvancedColorInfo, Size: uint32(unsafe.Sizeof(dcAdvancedColor{})), AdapterID: p.TgtAdapter, ID: p.TgtID}}
	if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&ac))); r == 0 {
		d.HDR = TriOf(ac.Value&1 != 0)
		d.HDROn = TriOf(ac.Value&2 != 0)
	}

	if e, ok := edidFor(devPath); ok {
		d.Maker = pnpMaker(e.Maker)
		d.Product = e.Product
		d.Serial = e.Serial
		d.NativeW, d.NativeH = e.NativeW, e.NativeH
		d.DiagonalIn = e.diagonal()
		if d.Name == "" {
			d.Name = e.Name
		}
	}

	sn := dcSourceName{Header: dcHeader{Type: dcGetSourceName, Size: uint32(unsafe.Sizeof(dcSourceName{})), AdapterID: p.SrcAdapter, ID: p.SrcID}}
	if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&sn))); r == 0 {
		modesFor(&d, &sn.GDIName[0])
	}
	return d
}

// modesFor fills the mode in use and the highest refresh at the native
// resolution from EnumDisplaySettingsW.
func modesFor(d *Display, gdi *uint16) {
	dm := devMode{Size: uint16(unsafe.Sizeof(devMode{}))}
	if r, _, _ := procEnumDisplaySettingsW.Call(uintptr(unsafe.Pointer(gdi)), enumCurrentSettings, uintptr(unsafe.Pointer(&dm))); r != 0 {
		d.Width, d.Height = int(dm.PelsWidth), int(dm.PelsHeight)
		if d.RefreshHz == 0 && dm.Frequency > 1 {
			d.RefreshHz = int(dm.Frequency)
		}
	}
	w, h := d.NativeW, d.NativeH
	if w == 0 {
		w, h = d.Width, d.Height
	}
	best := 0
	for i := uint32(0); i < 4096; i++ {
		m := devMode{Size: uint16(unsafe.Sizeof(devMode{}))}
		if r, _, _ := procEnumDisplaySettingsW.Call(uintptr(unsafe.Pointer(gdi)), uintptr(i), uintptr(unsafe.Pointer(&m))); r == 0 {
			break
		}
		if int(m.PelsWidth) == w && int(m.PelsHeight) == h && int(m.Frequency) > best {
			best = int(m.Frequency)
		}
	}
	d.MaxRefreshHz = best
}

// edidFor reads the EDID Windows keeps for a monitor device path such as
// \\?\DISPLAY#CMN1602#4&25...&UID8388688#{e6f07b5f-...}.
func edidFor(devPath string) (edidInfo, bool) {
	parts := strings.Split(strings.TrimPrefix(devPath, `\\?\`), "#")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "DISPLAY") {
		return edidInfo{}, false
	}
	for _, s := range parts[1:3] {
		if strings.ContainsAny(s, `\/`) || s == "" || s == ".." {
			return edidInfo{}, false
		}
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Enum\DISPLAY\`+parts[1]+`\`+parts[2]+`\Device Parameters`, registry.QUERY_VALUE)
	if err != nil {
		return edidInfo{}, false
	}
	defer k.Close()
	b, _, err := k.GetBinaryValue("EDID")
	if err != nil {
		return edidInfo{}, false
	}
	e, err := parseEDID(b)
	return e, err == nil
}

// connectionName names DISPLAYCONFIG_VIDEO_OUTPUT_TECHNOLOGY.
func connectionName(t uint32) string {
	switch t {
	case outputInternal, 6, 11, 13:
		return "built-in"
	case 0:
		return "VGA"
	case 4:
		return "DVI"
	case 5:
		return "HDMI"
	case 10:
		return "DisplayPort"
	case 18:
		return "USB-C"
	case 15:
		return "Miracast"
	}
	return ""
}

// touchLive reads touch and pen support (SM_DIGITIZER, SM_MAXIMUMTOUCHES).
func touchLive(dv *Devices) {
	if procGetSystemMetrics.Find() != nil {
		return
	}
	dig, _, _ := procGetSystemMetrics.Call(94)
	touches, _, _ := procGetSystemMetrics.Call(95)
	dv.Touch = TriOf(dig&0x03 != 0)
	dv.Pen = TriOf(dig&0x0C != 0)
	dv.MaxTouches = int(touches)
}
