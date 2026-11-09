package sysinfo

import (
	"strings"
	"time"
)

// Battery capability and power-state bits (batclass.h).
const (
	batteryCapacityRelative = 0x40000000
	batteryPowerOnLine      = 0x00000001
	batteryCharging         = 0x00000004
	batteryUnknownRate      = 0x80000000
	batteryUnknownCapacity  = 0xFFFFFFFF
)

// Totals sums every pack's capacity. Relative is set when any pack does not
// report mWh, and then only the health ratio means anything.
func (b Battery) Totals() (design, full uint64, relative bool) {
	for _, p := range b.Packs {
		design += p.DesignmWh
		full += p.FullmWh
		relative = relative || p.Relative
	}
	return design, full, relative
}

// Health is what the batteries hold now against what they were made to
// hold, in percent. A battery that holds more than its design (new ones
// can) is 100: as new. ok is false when the PC does not report it.
func (b Battery) Health() (pct int, ok bool) {
	design, full, _ := b.Totals()
	return healthPct(design, full)
}

// healthPct is full/design in percent, clamped to 100.
func healthPct(design, full uint64) (int, bool) {
	if design == 0 || full == 0 || design == batteryUnknownCapacity || full == batteryUnknownCapacity {
		return 0, false
	}
	pct := min((full*100+design/2)/design, 100)
	return int(pct), true
}

// Cycles is the charge cycle count over all packs, and whether any pack
// reported one. Zero is "not reported": many batteries never count.
func (b Battery) Cycles() (int, bool) {
	n := 0
	for _, p := range b.Packs {
		n += p.Cycles
	}
	return n, n > 0
}

// chemistry spells the four-byte battery chemistry as people say it.
func chemistry(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(strings.Trim(raw, "\x00")))
	switch s {
	case "LION", "LI-I", "LIION":
		return "Lithium-ion"
	case "LIP", "LIPO", "LI-P":
		return "Lithium polymer"
	case "NIMH":
		return "Nickel metal hydride"
	case "NICD":
		return "Nickel cadmium"
	case "PBAC":
		return "Lead acid"
	case "":
		return ""
	}
	return s
}

// chargeFrom fills the charge-now fields from IOCTL_BATTERY_QUERY_STATUS
// (summed over packs) and GetSystemPowerStatus.
//
// lifeSec is BatteryLifeTime: -1 (0xFFFFFFFF) while charging or unknown.
// Time to full is (full - now) / rate, only while the rate is positive.
func chargeFrom(b *Battery, state uint32, capNow, full uint64, rate int32, rateKnown bool, pct int, lifeSec int64) {
	b.ChargePct = -1
	if pct >= 0 && pct <= 100 {
		b.ChargePct = pct
	}
	b.OnAC = TriOf(state&batteryPowerOnLine != 0)
	b.Charging = TriOf(state&batteryCharging != 0)
	if rateKnown {
		b.RatemW = int(rate)
	}
	if lifeSec > 0 && b.Charging != TriYes {
		b.TimeLeft = time.Duration(lifeSec) * time.Second
	}
	if b.Charging == TriYes && rateKnown && rate > 0 && full > capNow {
		b.TimeToFull = time.Duration(float64(full-capNow) / float64(rate) * float64(time.Hour)).Round(time.Minute)
	}
}
