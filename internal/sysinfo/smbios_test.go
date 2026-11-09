package sysinfo

import (
	"encoding/binary"
	"testing"
)

// The tables here are synthetic, built field by field. Real dumps carry
// serial numbers and are never committed.

// rec is one structure for the builder: its type, its formatted bytes after
// the 4-byte header, and its strings.
type rec struct {
	typ     byte
	body    []byte
	strings []string
}

// buildSMBIOS assembles a RawSMBIOSData blob from records and an end marker.
func buildSMBIOS(recs ...rec) []byte {
	var d []byte
	for h, r := range recs {
		d = append(d, r.typ, byte(4+len(r.body)), byte(h), 0)
		d = append(d, r.body...)
		if len(r.strings) == 0 {
			d = append(d, 0, 0)
			continue
		}
		for _, s := range r.strings {
			if s == "" {
				s = " " // an empty string would end the set; firmware pads too
			}
			d = append(d, s...)
			d = append(d, 0)
		}
		d = append(d, 0)
	}
	d = append(d, 127, 4, 0xFF, 0xFF, 0, 0)
	head := []byte{0, 3, 3, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(head[4:], uint32(len(d)))
	return append(head, d...)
}

// put writes v little-endian at a formatted-area offset (header included).
func put(body []byte, off int, v any) {
	o := off - 4
	switch x := v.(type) {
	case byte:
		body[o] = x
	case uint16:
		binary.LittleEndian.PutUint16(body[o:], x)
	case uint32:
		binary.LittleEndian.PutUint32(body[o:], x)
	}
}

// array is a Type 16 with n devices of system memory.
func array(n uint16) rec {
	b := make([]byte, 0x17-4)
	put(b, 0x05, byte(3))
	put(b, 0x0D, n)
	return rec{typ: 16, body: b}
}

// device is a Type 17 (SMBIOS 3.3 length) with the given size word.
type dev struct {
	size        uint16
	ext         uint32
	ff, typ     byte
	speed, conf uint16
	loc, maker  string
	part, ser   string
}

func device(d dev) rec {
	b := make([]byte, 0x5C-4)
	put(b, 0x0C, d.size)
	put(b, 0x0E, d.ff)
	put(b, 0x10, byte(1)) // locator
	put(b, 0x11, byte(2)) // bank
	put(b, 0x12, d.typ)
	put(b, 0x15, d.speed)
	put(b, 0x17, byte(3)) // maker
	put(b, 0x18, byte(4)) // serial
	put(b, 0x1A, byte(5)) // part
	put(b, 0x1C, d.ext)
	put(b, 0x20, d.conf)
	return rec{typ: 17, body: b, strings: []string{d.loc, "BANK 0", d.maker, d.ser, d.part}}
}

func mustTable(t *testing.T, recs ...rec) smbiosTable {
	t.Helper()
	tb, err := parseRawSMBIOS(buildSMBIOS(recs...))
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

func TestParseMemorySlotsEmptyAndExtendedSize(t *testing.T) {
	tb := mustTable(t,
		array(4),
		device(dev{size: 0x4000, ff: 0x09, typ: 0x22, speed: 5600, conf: 4800, loc: "DIMM A1", maker: "80CE", part: "M323R2GA3BB0", ser: "ABCD1234"}),
		device(dev{size: 0, ff: 0x09, typ: 0x02, loc: "DIMM A2", maker: "Unknown", part: "NO DIMM", ser: "NO DIMM"}),
		device(dev{size: 0x7FFF, ext: 64 * 1024, ff: 0x09, typ: 0x22, speed: 5600, conf: 5600, loc: "DIMM B1", maker: "Kingston"}),
		device(dev{size: 0, ff: 0x09, loc: "DIMM B2"}),
	)
	m := memoryFrom(tb)
	if m.SlotCount != 4 || len(m.Slots) != 4 {
		t.Fatalf("slots = %d of %d, want 4 of 4", len(m.Slots), m.SlotCount)
	}
	a1 := m.Slots[0]
	if a1.SizeB != 16<<30 || a1.Type != "DDR5" || a1.FormFactor != "DIMM" || a1.SpeedMTs != 5600 || a1.ConfiguredMTs != 4800 {
		t.Fatalf("A1 = %+v", a1)
	}
	if a1.Maker != "Samsung" {
		t.Fatalf("a JEDEC ID should be named: %q", a1.Maker)
	}
	if a1.Soldered || a1.Empty {
		t.Fatal("a DIMM is socketed and fitted")
	}
	if a2 := m.Slots[1]; !a2.Empty || a2.Maker != "" || a2.Part != "" {
		t.Fatalf("empty slot carries data: %+v", a2)
	}
	if b1 := m.Slots[2]; b1.SizeB != 64<<30 {
		t.Fatalf("extended size = %d", b1.SizeB)
	}
}

func TestSolderedLPDDRCollapses(t *testing.T) {
	// Four LPDDR5 chips, form factor "row of chips", plus one chip whose
	// form factor is unknown: all soldered, one row.
	tb := mustTable(t,
		array(4),
		device(dev{size: 4096, ff: 0x0B, typ: 0x23, speed: 6400, conf: 6400, loc: "Controller0-ChannelA", maker: "Micron", part: "MT62F1G32D2DS"}),
		device(dev{size: 4096, ff: 0x0B, typ: 0x23, speed: 6400, conf: 6400, loc: "Controller0-ChannelB", maker: "Micron"}),
		device(dev{size: 4096, ff: 0x0B, typ: 0x23, speed: 6400, conf: 6400, loc: "Controller1-ChannelA", maker: "Micron"}),
		device(dev{size: 4096, ff: 0x02, typ: 0x23, speed: 6400, conf: 6400, loc: "Controller1-ChannelB", maker: "Micron"}),
	)
	m := memoryFrom(tb)
	if len(m.Slots) != 1 {
		t.Fatalf("want one soldered row, got %d: %+v", len(m.Slots), m.Slots)
	}
	s := m.Slots[0]
	if !s.Soldered || s.SizeB != 16<<30 || s.Type != "LPDDR5" || s.Serial != "" {
		t.Fatalf("soldered row = %+v", s)
	}
	if m.SlotCount != 0 {
		t.Fatalf("soldered chips are not sockets: SlotCount = %d", m.SlotCount)
	}

	// LPCAMM2 is LPDDR in a socket: the form factor wins.
	tb = mustTable(t, array(1), device(dev{size: 32768, ff: 0x11, typ: 0x23, loc: "CAMM"}))
	if m := memoryFrom(tb); len(m.Slots) != 1 || m.Slots[0].Soldered || m.Slots[0].FormFactor != "CAMM" {
		t.Fatalf("LPCAMM2 = %+v", m.Slots)
	}
}

func TestPlaceholderSerialsAreNotSet(t *testing.T) {
	for _, s := range []string{
		"To be filled by O.E.M.", "Default string", "System Serial Number", "Not Specified",
		"0123456789", "  ", "00000000", "SerNum0", "PartNum00", "Manufacturer01", "NO DIMM",
	} {
		if !isPlaceholder(s) {
			t.Errorf("%q should be a placeholder", s)
		}
	}
	for _, s := range []string{"5CD1234XYZ", "PF3ABCDE", "M471A2K43EB1-CWE", "Samsung", "Primary", "Lithium-ion", "联想", "SN5CD1234"} {
		if isPlaceholder(s) {
			t.Errorf("%q is a real value", s)
		}
	}

	t1 := make([]byte, 0x1B-4)
	put(t1, 0x04, byte(1))
	put(t1, 0x05, byte(2))
	put(t1, 0x07, byte(3))
	tb := mustTable(t, rec{typ: 1, body: t1, strings: []string{"Dell Inc.", "XPS 15 9520", "System Serial Number"}})
	s := systemFrom(tb)
	if s.Serial != "" || !s.SerialPlaceholder || s.Maker != "Dell Inc." {
		t.Fatalf("system = %+v", s)
	}
}

// A battery or firmware field that holds the field's own name, however it
// is spelt, is a placeholder: an HP battery reported "SerialNumber" and the
// page showed it as the serial.
func TestFieldNamesSpeltAnyWayAreNotSet(t *testing.T) {
	for _, s := range []string{
		"SerialNumber", "Serial Number", "serial_number", "SERIAL-NUMBER", "SerialNumber0", "Serial",
		"SN", "To Be Filled By O.E.M", "TOBEFILLEDBYOEM", "DefaultString", "Not Defined", "OEM_Define1",
		"System SKU", "Asset Tag", "Unique ID", "Battery Serial Number", "N.A.", "?", "--",
	} {
		if !isPlaceholder(s) {
			t.Errorf("%q should be a placeholder", s)
		}
		if got := clean(s); got != "" {
			t.Errorf("clean(%q) = %q, want it dropped", s, got)
		}
	}
	if got := clean("  5CD1234XYZ\x00"); got != "5CD1234XYZ" {
		t.Errorf("a real serial was changed: %q", got)
	}
}

func TestChassisTypes(t *testing.T) {
	for code, want := range map[byte]Chassis{
		8: ChassisLaptop, 9: ChassisLaptop, 10: ChassisLaptop, 14: ChassisLaptop,
		31: ChassisConvertible, 32: ChassisDetachable, 30: ChassisTablet,
		3: ChassisDesktop, 4: ChassisDesktop, 6: ChassisDesktop, 7: ChassisDesktop,
		13: ChassisAllInOne, 2: ChassisUnknown, 0x80 | 10: ChassisLaptop, 23: ChassisOther,
	} {
		if got := chassisOf(code); got != want {
			t.Errorf("chassis %d = %v, want %v", code, got, want)
		}
	}
	t3 := make([]byte, 0x09-4)
	put(t3, 0x05, byte(31))
	if c := systemFrom(mustTable(t, rec{typ: 3, body: t3})).Chassis; c != ChassisConvertible || !c.Portable() {
		t.Fatalf("type 3 chassis = %v", c)
	}
}

func TestTruncatedTableKeepsWhatCameBefore(t *testing.T) {
	blob := buildSMBIOS(array(2), device(dev{size: 8192, ff: 0x0D, typ: 0x1A, loc: "A"}))
	cut := blob[:len(blob)-30]
	binary.LittleEndian.PutUint32(cut[4:], uint32(len(cut)-8))
	tb, err := parseRawSMBIOS(cut)
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.of(16)) != 1 {
		t.Fatal("the array before the cut was lost")
	}
	if _, err := parseRawSMBIOS([]byte{1, 2}); err == nil {
		t.Fatal("a short blob should be an error")
	}
}
