package sysinfo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// edidInfo is what Devpit reads from a screen's EDID block.
type edidInfo struct {
	// Maker is the three-letter PNP code, Product the product code in hex.
	Maker, Product string
	// Name is the monitor name descriptor, Serial the serial descriptor.
	Name, Serial string
	// NativeW, NativeH and NativeHz are the first detailed timing: the
	// panel's preferred mode.
	NativeW, NativeH, NativeHz int
	// WidthMM and HeightMM are the picture size, 0 when not given.
	WidthMM, HeightMM int
}

// errEDID is a block that is not an EDID at all.
var errEDID = errors.New("the screen's identity block is not readable")

// parseEDID reads the 128-byte base block. A block with a bad header is an
// error; a block with odd fields is read as far as it makes sense, so one
// bad value never loses the rest.
func parseEDID(b []byte) (edidInfo, error) {
	var e edidInfo
	if len(b) < 128 || binary.BigEndian.Uint64(b[0:8]) != 0x00FFFFFFFFFFFF00 {
		return e, errEDID
	}
	id := binary.BigEndian.Uint16(b[8:10])
	letters := []byte{
		byte('A' - 1 + (id>>10)&0x1F),
		byte('A' - 1 + (id>>5)&0x1F),
		byte('A' - 1 + id&0x1F),
	}
	if okPNP(letters) {
		e.Maker = string(letters)
	}
	if p := binary.LittleEndian.Uint16(b[10:12]); p != 0 {
		e.Product = fmt.Sprintf("%04X", p)
	}
	cmW, cmH := int(b[21]), int(b[22])
	for i, off := range []int{54, 72, 90, 108} {
		var d [18]byte
		copy(d[:], b[off:off+18])
		clock := int(binary.LittleEndian.Uint16(d[0:2]))
		if clock != 0 {
			if i != 0 && e.NativeW != 0 {
				continue
			}
			hA := int(d[2]) | int(d[4]&0xF0)<<4
			hB := int(d[3]) | int(d[4]&0x0F)<<8
			vA := int(d[5]) | int(d[7]&0xF0)<<4
			vB := int(d[6]) | int(d[7]&0x0F)<<8
			e.NativeW, e.NativeH = hA, vA
			if tot := (hA + hB) * (vA + vB); tot > 0 {
				e.NativeHz = int(math.Round(float64(clock) * 10000 / float64(tot)))
			}
			e.WidthMM = int(d[12]) | int(d[14]&0xF0)<<4
			e.HeightMM = int(d[13]) | int(d[14]&0x0F)<<8
			continue
		}
		text := descriptorText(d[5:18])
		switch d[3] {
		case 0xFC:
			e.Name = text
		case 0xFF:
			e.Serial = clean(text)
		}
	}
	// A size in mm that is an aspect ratio (16x9) or nonsense is no size;
	// fall back to the centimetres in the header.
	if e.WidthMM < 100 || e.HeightMM < 50 {
		e.WidthMM, e.HeightMM = cmW*10, cmH*10
	}
	if e.WidthMM < 100 || e.HeightMM < 50 {
		e.WidthMM, e.HeightMM = 0, 0
	}
	return e, nil
}

// diagonal is the screen size in inches, 0 when unknown.
func (e edidInfo) diagonal() float64 {
	if e.WidthMM == 0 || e.HeightMM == 0 {
		return 0
	}
	in := math.Hypot(float64(e.WidthMM), float64(e.HeightMM)) / 25.4
	return math.Round(in*10) / 10
}

// okPNP reports whether three letters are a real PNP code.
func okPNP(l []byte) bool {
	for _, c := range l {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// descriptorText is a display descriptor's text: up to 13 bytes, ended by a
// line feed and padded with spaces.
func descriptorText(b []byte) string {
	s := string(b)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	for _, r := range s {
		if r < 0x20 || r > 0x7E {
			return ""
		}
	}
	return s
}
