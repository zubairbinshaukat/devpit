package sysinfo

import (
	"encoding/binary"
	"errors"
	"strings"
	"time"
)

// smbiosStruct is one structure of the SMBIOS table: its formatted area
// (header included, so offsets match the specification) and its strings.
type smbiosStruct struct {
	typ     byte
	f       []byte
	strings []string
}

// smbiosTable is a parsed table.
type smbiosTable struct {
	major, minor byte
	structs      []smbiosStruct
}

// errSMBIOS is a table too short or broken to read.
var errSMBIOS = errors.New("the firmware's hardware table could not be read")

// parseRawSMBIOS reads what GetSystemFirmwareTable('RSMB') returns: an
// 8-byte RawSMBIOSData header, then the structures. A structure cut off at
// the end stops the walk; everything before it is kept.
func parseRawSMBIOS(b []byte) (smbiosTable, error) {
	if len(b) < 8 {
		return smbiosTable{}, errSMBIOS
	}
	t := smbiosTable{major: b[1], minor: b[2]}
	n := int(binary.LittleEndian.Uint32(b[4:8]))
	d := b[8:]
	if n < len(d) {
		d = d[:n]
	}
	for i := 0; i+4 <= len(d); {
		typ, l := d[i], int(d[i+1])
		if l < 4 || i+l > len(d) {
			break
		}
		s := smbiosStruct{typ: typ, f: d[i : i+l]}
		j := i + l
		start := j
		for {
			if j+1 >= len(d) {
				j = len(d)
				break
			}
			if d[j] == 0 && d[j+1] == 0 {
				break
			}
			if d[j] == 0 {
				s.strings = append(s.strings, string(d[start:j]))
				start = j + 1
			}
			j++
		}
		if j > start && j <= len(d) {
			s.strings = append(s.strings, string(d[start:j]))
		}
		t.structs = append(t.structs, s)
		if typ == 127 {
			break
		}
		i = j + 2
	}
	if len(t.structs) == 0 {
		return t, errSMBIOS
	}
	return t, nil
}

// of returns every structure of one type.
func (t smbiosTable) of(typ byte) []smbiosStruct {
	var out []smbiosStruct
	for _, s := range t.structs {
		if s.typ == typ {
			out = append(out, s)
		}
	}
	return out
}

func (s smbiosStruct) byteAt(off int) (byte, bool) {
	if off < len(s.f) {
		return s.f[off], true
	}
	return 0, false
}

func (s smbiosStruct) word(off int) (uint16, bool) {
	if off+2 <= len(s.f) {
		return binary.LittleEndian.Uint16(s.f[off:]), true
	}
	return 0, false
}

func (s smbiosStruct) dword(off int) (uint32, bool) {
	if off+4 <= len(s.f) {
		return binary.LittleEndian.Uint32(s.f[off:]), true
	}
	return 0, false
}

// str is the string a formatted-area byte points at, trimmed, "" when none.
func (s smbiosStruct) str(off int) string {
	i, ok := s.byteAt(off)
	if !ok || i == 0 || int(i) > len(s.strings) {
		return ""
	}
	return strings.TrimSpace(s.strings[i-1])
}

// memoryType names an SMBIOS memory type code.
func memoryType(code byte) string {
	switch code {
	case 0x18:
		return "DDR3"
	case 0x1A:
		return "DDR4"
	case 0x1B:
		return "LPDDR"
	case 0x1C:
		return "LPDDR2"
	case 0x1D:
		return "LPDDR3"
	case 0x1E:
		return "LPDDR4"
	case 0x22:
		return "DDR5"
	case 0x23:
		return "LPDDR5"
	}
	return ""
}

// formFactor names a form factor, and says whether it is fixed to the board
// (soldered) or socketed, or unknown.
func formFactor(code byte) (name string, soldered Tri) {
	switch code {
	case 0x09:
		return "DIMM", TriNo
	case 0x0D:
		return "SODIMM", TriNo
	case 0x11:
		// LPCAMM2 is LPDDR in a socket: the form factor wins over the type.
		return "CAMM", TriNo
	case 0x05, 0x0B, 0x10:
		return "", TriYes
	}
	return "", TriUnknown
}

// memoryFrom reads the slots from Type 16 (the array) and Type 17 (one per
// device). Soldered chips are folded into one row.
func memoryFrom(t smbiosTable) Memory {
	var m Memory
	for _, a := range t.of(16) {
		use, _ := a.byteAt(0x05)
		if use != 0x03 { // system memory only
			continue
		}
		if n, ok := a.word(0x0D); ok {
			m.SlotCount += int(n)
		}
	}
	var soldered []Module
	for _, d := range t.of(17) {
		mod := moduleFrom(d)
		if mod.Soldered && !mod.Empty {
			soldered = append(soldered, mod)
			continue
		}
		if mod.Soldered && mod.Empty {
			// An empty "chip" position is not a slot anyone can fill.
			continue
		}
		m.Slots = append(m.Slots, mod)
	}
	if len(soldered) > 0 {
		folded := soldered[0]
		folded.Locator, folded.Bank, folded.Serial = "on the board", "", ""
		for _, s := range soldered[1:] {
			folded.SizeB += s.SizeB
			if folded.Part == "" {
				folded.Part = s.Part
			}
		}
		m.Slots = append([]Module{folded}, m.Slots...)
		// Soldered positions are not sockets: count only the others.
		sockets := 0
		for _, s := range m.Slots[1:] {
			if !s.Soldered {
				sockets++
			}
		}
		if m.SlotCount > 0 {
			m.SlotCount = sockets
		}
	}
	return m
}

// moduleFrom reads one Type 17 structure.
func moduleFrom(d smbiosStruct) Module {
	mod := Module{Locator: d.str(0x10), Bank: d.str(0x11)}
	size, _ := d.word(0x0C)
	switch {
	case size == 0:
		mod.Empty = true
	case size == 0xFFFF:
		// Something is fitted, its size is not reported.
	case size == 0x7FFF:
		if ext, ok := d.dword(0x1C); ok {
			mod.SizeB = uint64(ext&0x7FFFFFFF) << 20
		}
	case size&0x8000 != 0:
		mod.SizeB = uint64(size&0x7FFF) << 10
	default:
		mod.SizeB = uint64(size) << 20
	}
	ffCode, _ := d.byteAt(0x0E)
	typeCode, _ := d.byteAt(0x12)
	mod.Type = memoryType(typeCode)
	name, sold := formFactor(ffCode)
	mod.FormFactor = name
	switch sold {
	case TriYes:
		mod.Soldered = true
	case TriUnknown:
		// No usable form factor: LPDDR without a socket is soldered.
		mod.Soldered = strings.HasPrefix(mod.Type, "LPDDR")
	}
	if v, ok := d.word(0x15); ok && v != 0 {
		if v == 0xFFFF {
			if ext, ok := d.dword(0x54); ok {
				mod.SpeedMTs = int(ext)
			}
		} else {
			mod.SpeedMTs = int(v)
		}
	}
	if v, ok := d.word(0x20); ok && v != 0 {
		if v == 0xFFFF {
			if ext, ok := d.dword(0x58); ok {
				mod.ConfiguredMTs = int(ext)
			}
		} else {
			mod.ConfiguredMTs = int(v)
		}
	}
	maker := clean(d.str(0x17))
	if n, ok := jedecMaker(maker); ok {
		maker = n
	}
	mod.Maker = maker
	mod.Part = clean(d.str(0x1A))
	mod.Serial = clean(d.str(0x18))
	if mod.Empty {
		mod.Maker, mod.Part, mod.Serial, mod.Type, mod.SpeedMTs, mod.ConfiguredMTs = "", "", "", "", 0, 0
	}
	return mod
}

// chassisOf maps the Type 3 chassis code.
func chassisOf(code byte) Chassis {
	switch code & 0x7F {
	case 8, 9, 10, 14:
		return ChassisLaptop
	case 31:
		return ChassisConvertible
	case 32:
		return ChassisDetachable
	case 30:
		return ChassisTablet
	case 3, 4, 5, 6, 7, 15, 16, 35, 36:
		return ChassisDesktop
	case 13:
		return ChassisAllInOne
	case 0, 1, 2:
		return ChassisUnknown
	}
	return ChassisOther
}

// systemFrom reads Types 0 to 3.
func systemFrom(t smbiosTable) System {
	var s System
	if b := t.of(0); len(b) > 0 {
		s.BIOSVendor = clean(b[0].str(0x04))
		s.BIOSVersion = clean(b[0].str(0x05))
		s.BIOSDate = parseBIOSDate(b[0].str(0x08))
	}
	if sy := t.of(1); len(sy) > 0 {
		s.Maker = clean(sy[0].str(0x04))
		s.Model = clean(sy[0].str(0x05))
		raw := sy[0].str(0x07)
		s.Serial = clean(raw)
		s.SerialPlaceholder = s.Serial == ""
		s.SKU = clean(sy[0].str(0x19))
		s.Family = clean(sy[0].str(0x1A))
	}
	if bb := t.of(2); len(bb) > 0 {
		s.BoardMaker = clean(bb[0].str(0x04))
		s.BoardModel = clean(bb[0].str(0x05))
		s.BoardSerial = clean(bb[0].str(0x07))
	}
	if c := t.of(3); len(c) > 0 {
		code, _ := c[0].byteAt(0x05)
		s.Chassis = chassisOf(code)
	}
	return s
}

// parseBIOSDate reads the "MM/DD/YYYY" (or "MM/DD/YY") firmware date.
func parseBIOSDate(s string) time.Time {
	for _, layout := range []string{"01/02/2006", "01/02/06", "2006-01-02", "20060102"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}
