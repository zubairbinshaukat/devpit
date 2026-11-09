package sysinfo

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// RAMSize is memory as shops write it: binary gigabytes called GB.
func RAMSize(b uint64) string {
	if b == 0 {
		return ""
	}
	g := float64(b) / gib
	if g >= 1 {
		if math.Abs(g-math.Round(g)) < 0.05 {
			return strconv.Itoa(int(math.Round(g))) + " GB"
		}
		return strconv.FormatFloat(g, 'f', 1, 64) + " GB"
	}
	return strconv.Itoa(int(math.Round(float64(b)/(1<<20)))) + " MB"
}

// WindowsSize is a size the way Explorer shows it: binary units called GB
// and TB.
func WindowsSize(b uint64) string {
	g := float64(b) / gib
	switch {
	case g >= 1000:
		return strconv.FormatFloat(g/1024, 'f', 1, 64) + " TB"
	case g >= 10:
		return strconv.Itoa(int(math.Round(g))) + " GB"
	case g >= 1:
		return strconv.FormatFloat(g, 'f', 1, 64) + " GB"
	}
	return strconv.Itoa(int(math.Round(float64(b)/(1<<20)))) + " MB"
}

// AdvertisedSize is a drive's size as printed on the box: decimal units,
// snapped to the usual capacities when it is within 3% of one.
func AdvertisedSize(b uint64) string {
	if b == 0 {
		return ""
	}
	gb := float64(b) / 1e9
	best, bestDiff := gb, 0.03
	for _, c := range []float64{32, 64, 120, 128, 240, 250, 256, 480, 500, 512, 960, 1000, 2000, 4000, 8000, 16000} {
		if d := math.Abs(gb-c) / c; d < bestDiff {
			best, bestDiff = c, d
		}
	}
	gb = best
	if gb >= 1000 {
		tb := gb / 1000
		if tb == math.Trunc(tb) {
			return strconv.Itoa(int(tb)) + " TB"
		}
		return strconv.FormatFloat(tb, 'f', 1, 64) + " TB"
	}
	return strconv.Itoa(int(math.Round(gb))) + " GB"
}

// Clock is a clock speed in GHz: "2.3 GHz".
func Clock(mhz int) string {
	if mhz <= 0 {
		return ""
	}
	return strconv.FormatFloat(math.Round(float64(mhz)/100)/10, 'f', 1, 64) + " GHz"
}

// Speed is a link speed: "300 Mbps", "2.5 Gbps".
func Speed(bps uint64) string {
	switch {
	case bps == 0:
		return ""
	case bps >= 1e9:
		return strings.TrimSuffix(strconv.FormatFloat(float64(bps)/1e9, 'f', 1, 64), ".0") + " Gbps"
	}
	return strconv.Itoa(int(math.Round(float64(bps)/1e6))) + " Mbps"
}

// Duration is "3 h 20 min", "45 min", "2 days".
func Duration(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d >= 48*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + " days"
	case d >= time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		if m == 0 {
			return fmt.Sprintf("%d h", h)
		}
		return fmt.Sprintf("%d h %d min", h, m)
	}
	return strconv.Itoa(max(1, int(d.Minutes()))) + " min"
}

// CoresText is "8 cores, 16 threads" or "14 cores (6 performance + 8
// efficient), 20 threads".
func (c CPU) CoresText() string {
	if c.Cores == 0 {
		return ""
	}
	s := strconv.Itoa(c.Cores) + " cores"
	if c.PCores > 0 && c.ECores > 0 {
		s += fmt.Sprintf(" (%d performance + %d efficient)", c.PCores, c.ECores)
	}
	if c.Threads > 0 {
		s += ", " + strconv.Itoa(c.Threads) + " threads"
	}
	return s
}

// ModuleText is "16 GB DDR4 3200 MT/s SODIMM", leaving out what is missing.
func (m Module) ModuleText() string {
	parts := []string{}
	if s := RAMSize(m.SizeB); s != "" {
		parts = append(parts, s)
	}
	if m.Type != "" {
		parts = append(parts, m.Type)
	}
	if m.SpeedMTs > 0 {
		parts = append(parts, strconv.Itoa(m.SpeedMTs)+" MT/s")
	}
	if m.FormFactor != "" {
		parts = append(parts, m.FormFactor)
	}
	return strings.Join(parts, " ")
}

// Fitted is the slots with memory in them, and Free the empty sockets.
func (m Memory) Fitted() (fitted, free int, soldered bool) {
	for _, s := range m.Slots {
		switch {
		case s.Soldered:
			soldered = true
		case s.Empty:
			free++
		default:
			fitted++
		}
	}
	return fitted, free, soldered
}
