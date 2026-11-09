package sysinfo

import "strings"

// gib is one gibibyte.
const gib = 1 << 30

// vendorOf returns the vendor in a hardware ID such as PCI\VEN_10DE&DEV_2520,
// upper case, or "".
func vendorOf(hwid string) string {
	u := strings.ToUpper(hwid)
	i := strings.Index(u, "VEN_")
	if i < 0 || i+8 > len(u) {
		return ""
	}
	return u[i+4 : i+8]
}

// realAdapter reports whether a display adapter is hardware worth listing:
// not Microsoft's fallback driver and not a virtual or remote screen.
func realAdapter(name, hwid string) bool {
	n, id := strings.ToLower(name), strings.ToLower(hwid)
	if !strings.HasPrefix(id, `pci\`) && !strings.HasPrefix(id, `acpi\`) {
		return false
	}
	for _, skip := range []string{"basic display", "basic render", "remote display", "virtual", "hyper-v", "indirect"} {
		if strings.Contains(n, skip) {
			return false
		}
	}
	return true
}

// builtInGPU is the rule of thumb for graphics in the processor rather than
// a card of its own. It is a guess and the screen says so.
func builtInGPU(vendor, name string, memB uint64) bool {
	n := strings.ToLower(name)
	switch vendor {
	case "10DE": // NVIDIA
		return false
	case "8086": // Intel: built in, except Arc cards
		return !strings.Contains(n, "arc a") && !strings.Contains(n, "arc(tm) a") && !strings.Contains(n, "arc b")
	case "1002", "1022": // AMD
		if strings.Contains(n, "radeon(tm) graphics") || strings.Contains(n, "radeon graphics") {
			return true
		}
		for _, igpu := range []string{"610m", "660m", "680m", "740m", "760m", "780m", "880m", "890m", "vega"} {
			if strings.Contains(n, igpu) {
				return true
			}
		}
		return memB > 0 && memB < gib
	case "QCOM":
		return true
	}
	return memB > 0 && memB < gib
}
