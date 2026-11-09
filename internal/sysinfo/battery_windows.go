//go:build windows

package sysinfo

import (
	"encoding/binary"
	"unsafe"

	"golang.org/x/sys/windows"
)

// guidDeviceBattery is GUID_DEVICE_BATTERY.
func guidDeviceBattery() windows.GUID {
	return windows.GUID{Data1: 0x72631e54, Data2: 0x78a4, Data3: 0x11d0, Data4: [8]byte{0xbc, 0xf7, 0x00, 0xaa, 0x00, 0xb7, 0xb3, 0x2a}}
}

// Battery IOCTLs: CTL_CODE(FILE_DEVICE_BATTERY, 0x10/0x11/0x13,
// METHOD_BUFFERED, FILE_READ_ACCESS).
const (
	ioctlBatteryQueryTag    = 0x294040
	ioctlBatteryQueryInfo   = 0x294044
	ioctlBatteryQueryStatus = 0x29404C
)

// BATTERY_QUERY_INFORMATION_LEVEL values.
const (
	batteryInformation    = 0
	batteryDeviceName     = 4
	batteryManufactureNam = 6
	batterySerialNumber   = 8
)

// batteryQueryInformation is BATTERY_QUERY_INFORMATION (12 bytes).
type batteryQueryInformation struct {
	Tag    uint32
	Level  int32
	AtRate int32
}

// batteryInformationT is BATTERY_INFORMATION (36 bytes).
type batteryInformationT struct {
	Capabilities        uint32
	Technology          uint8
	Reserved            [3]uint8
	Chemistry           [4]uint8
	DesignedCapacity    uint32
	FullChargedCapacity uint32
	DefaultAlert1       uint32
	DefaultAlert2       uint32
	CriticalBias        uint32
	CycleCount          uint32
}

// batteryWaitStatus is BATTERY_WAIT_STATUS (20 bytes).
type batteryWaitStatus struct {
	Tag, Timeout, PowerState, LowCapacity, HighCapacity uint32
}

// batteryStatusT is BATTERY_STATUS (16 bytes).
type batteryStatusT struct {
	PowerState, Capacity, Voltage uint32
	Rate                          int32
}

// systemPowerStatus is SYSTEM_POWER_STATUS (12 bytes).
type systemPowerStatus struct {
	ACLineStatus, BatteryFlag, BatteryLifePercent, SystemStatusFlag uint8
	BatteryLifeTime, BatteryFullLifeTime                            uint32
}

// batteryLive reads every battery the system lists.
func batteryLive() (Battery, error) {
	b := Battery{ChargePct: -1}
	var sps systemPowerStatus
	spsOK := false
	if procGetSystemPowerStatus.Find() == nil {
		r, _, _ := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&sps)))
		spsOK = r != 0
	}
	guid := guidDeviceBattery()
	paths, err := windows.CM_Get_Device_Interface_List("", &guid, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil && !spsOK {
		return b, err
	}
	var state uint32
	var capNow, fullSum uint64
	var rate int32
	rateKnown := false
	for _, p := range paths {
		pk, st, ok := readPack(p)
		if !ok {
			continue
		}
		b.Packs = append(b.Packs, pk)
		state |= st.PowerState
		if st.Capacity != batteryUnknownCapacity {
			capNow += uint64(st.Capacity)
		}
		fullSum += pk.FullmWh
		if st.Rate != -1<<31 { // BATTERY_UNKNOWN_RATE
			rate += st.Rate
			rateKnown = true
		}
	}
	switch {
	case len(b.Packs) > 0:
		b.Present = TriYes
	case spsOK && sps.BatteryFlag&128 != 0:
		b.Present = TriNo
	case spsOK && sps.BatteryFlag != 255:
		b.Present = TriNo
	}
	if b.Present != TriYes {
		return b, nil
	}
	pct := -1
	life := int64(-1)
	if spsOK {
		pct = int(sps.BatteryLifePercent)
		if sps.BatteryLifeTime != 0xFFFFFFFF {
			life = int64(sps.BatteryLifeTime)
		}
		if sps.ACLineStatus == 1 {
			state |= batteryPowerOnLine
		}
	}
	chargeFrom(&b, state, capNow, fullSum, rate, rateKnown, pct, life)
	return b, nil
}

// readPack opens one battery and reads its information and status.
func readPack(path string) (Pack, batteryStatusT, bool) {
	var pk Pack
	var st batteryStatusT
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return pk, st, false
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return pk, st, false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var wait, tag, n uint32
	if err := windows.DeviceIoControl(h, ioctlBatteryQueryTag, (*byte)(unsafe.Pointer(&wait)), 4,
		(*byte)(unsafe.Pointer(&tag)), 4, &n, nil); err != nil || tag == 0 {
		return pk, st, false
	}
	q := batteryQueryInformation{Tag: tag, Level: batteryInformation}
	var bi batteryInformationT
	if err := windows.DeviceIoControl(h, ioctlBatteryQueryInfo, (*byte)(unsafe.Pointer(&q)), uint32(unsafe.Sizeof(q)),
		(*byte)(unsafe.Pointer(&bi)), uint32(unsafe.Sizeof(bi)), &n, nil); err != nil {
		return pk, st, false
	}
	pk.Relative = bi.Capabilities&batteryCapacityRelative != 0
	if bi.DesignedCapacity != batteryUnknownCapacity {
		pk.DesignmWh = uint64(bi.DesignedCapacity)
	}
	if bi.FullChargedCapacity != batteryUnknownCapacity {
		pk.FullmWh = uint64(bi.FullChargedCapacity)
	}
	pk.Cycles = int(bi.CycleCount)
	pk.Chemistry = chemistry(string(bi.Chemistry[:]))
	pk.Name = clean(queryBatteryString(h, tag, batteryDeviceName))
	pk.Maker = clean(queryBatteryString(h, tag, batteryManufactureNam))
	pk.Serial = clean(queryBatteryString(h, tag, batterySerialNumber))

	ws := batteryWaitStatus{Tag: tag}
	_ = windows.DeviceIoControl(h, ioctlBatteryQueryStatus, (*byte)(unsafe.Pointer(&ws)), uint32(unsafe.Sizeof(ws)),
		(*byte)(unsafe.Pointer(&st)), uint32(unsafe.Sizeof(st)), &n, nil)
	return pk, st, true
}

// queryBatteryString reads one of the battery's text fields, "" when the
// battery does not have it.
func queryBatteryString(h windows.Handle, tag uint32, level int32) string {
	q := batteryQueryInformation{Tag: tag, Level: level}
	var buf [256]byte
	var n uint32
	if err := windows.DeviceIoControl(h, ioctlBatteryQueryInfo, (*byte)(unsafe.Pointer(&q)), uint32(unsafe.Sizeof(q)),
		&buf[0], uint32(len(buf)), &n, nil); err != nil || n < 2 {
		return ""
	}
	u := make([]uint16, n/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(buf[2*i:])
	}
	return windows.UTF16ToString(u)
}
