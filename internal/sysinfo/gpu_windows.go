//go:build windows

package sysinfo

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Device setup classes.
func classDisplay() windows.GUID {
	return windows.GUID{Data1: 0x4d36e968, Data2: 0xe325, Data3: 0x11ce, Data4: [8]byte{0xbf, 0xc1, 0x08, 0x00, 0x2b, 0xe1, 0x03, 0x18}}
}

func classBluetooth() windows.GUID {
	return windows.GUID{Data1: 0xe0cbf06c, Data2: 0xcd8b, Data3: 0x4647, Data4: [8]byte{0xbb, 0x8a, 0x26, 0x3b, 0x43, 0xf0, 0xf9, 0x74}}
}

func classCamera() windows.GUID {
	return windows.GUID{Data1: 0xca3e7ab9, Data2: 0xb4c3, Data3: 0x4ae6, Data4: [8]byte{0x82, 0x51, 0x57, 0x9e, 0xf9, 0x33, 0x89, 0x0f}}
}

func classBiometric() windows.GUID {
	return windows.GUID{Data1: 0x53d29ef7, Data2: 0x377c, Data3: 0x4d14, Data4: [8]byte{0x86, 0x4b, 0xeb, 0x3a, 0x85, 0x76, 0x93, 0x59}}
}

// eachPresent calls f for every present device of a setup class, and
// returns how many there were.
func eachPresent(class windows.GUID, f func(set windows.DevInfo, d *windows.DevInfoData)) (int, error) {
	set, err := windows.SetupDiGetClassDevsEx(&class, "", 0, windows.DIGCF_PRESENT, 0, "")
	if err != nil {
		return 0, err
	}
	defer set.Close()
	n := 0
	for i := 0; i < 256; i++ {
		d, err := set.EnumDeviceInfo(i)
		if err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
				break
			}
			continue
		}
		n++
		if f != nil {
			f(set, d)
		}
	}
	return n, nil
}

// graphicsLive lists the present display adapters from their driver keys.
// Adapter memory comes from HardwareInformation.qwMemorySize (a QWORD),
// then MemorySize; never from Win32_VideoController, which stops at 4 GB.
func graphicsLive() ([]GPU, error) {
	var out []GPU
	_, err := eachPresent(classDisplay(), func(set windows.DevInfo, d *windows.DevInfoData) {
		h, err := set.OpenDevRegKey(d, windows.DICS_FLAG_GLOBAL, 0, windows.DIREG_DRV, windows.KEY_READ)
		if err != nil {
			return
		}
		k := registry.Key(h)
		defer k.Close()
		str := func(name string) string { v, _, _ := k.GetStringValue(name); return strings.TrimSpace(v) }
		name, hwid := str("DriverDesc"), str("MatchingDeviceId")
		if !realAdapter(name, hwid) {
			return
		}
		g := GPU{Name: name, VendorID: vendorOf(hwid), DriverVersion: str("DriverVersion"), DriverDate: str("DriverDate")}
		g.MemoryB = adapterMemory(k)
		g.BuiltIn = builtInGPU(g.VendorID, g.Name, g.MemoryB)
		out = append(out, g)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// adapterMemory reads the adapter's own memory size in bytes, 0 when the
// driver does not say (built-in graphics usually do not).
func adapterMemory(k registry.Key) uint64 {
	for _, name := range []string{"HardwareInformation.qwMemorySize", "HardwareInformation.MemorySize"} {
		if v, _, err := k.GetIntegerValue(name); err == nil && v > 0 {
			return v
		}
		if b, _, err := k.GetBinaryValue(name); err == nil {
			var v uint64
			for i := min(len(b), 8) - 1; i >= 0; i-- {
				v = v<<8 | uint64(b[i])
			}
			if v > 0 {
				return v
			}
		}
	}
	return 0
}

// devicesLive says which built-in devices are present.
func devicesLive() (Devices, error) {
	var dv Devices
	present := func(c windows.GUID) Tri {
		n, err := eachPresent(c, nil)
		if err != nil {
			return TriUnknown
		}
		return TriOf(n > 0)
	}
	dv.Bluetooth = present(classBluetooth())
	dv.Camera = present(classCamera())
	dv.Fingerprint = present(classBiometric())
	touchLive(&dv)
	return dv, nil
}
