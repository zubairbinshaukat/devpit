package sysinfo

import (
	"encoding/binary"
	"math/bits"
)

// The LOGICAL_PROCESSOR_RELATIONSHIP values Devpit reads.
const (
	relProcessorCore    = 0
	relCache            = 2
	relProcessorPackage = 3
)

// cpuTopology is what GetLogicalProcessorInformationEx says.
type cpuTopology struct {
	Cores, Threads, PCores, ECores, Sockets int
	L2, L3                                  uint64
}

// parseLPIEx walks a SYSTEM_LOGICAL_PROCESSOR_INFORMATION_EX buffer.
// ptrSize is the size of KAFFINITY (8 on x64 and ARM64). A record that does
// not fit ends the walk; what came before is kept.
//
// Layouts (offsets from the record start):
//
//	Relationship DWORD @0, Size DWORD @4
//	PROCESSOR_RELATIONSHIP: Flags @8, EfficiencyClass @9, GroupCount WORD @30,
//	  GroupMask[] @32, each { KAFFINITY Mask; WORD Group; WORD Reserved[3] }
//	CACHE_RELATIONSHIP: Level @8, CacheSize DWORD @12, Type @16
func parseLPIEx(b []byte, ptrSize int) cpuTopology {
	var t cpuTopology
	classes := map[byte]int{}
	var maxClass byte
	for i := 0; i+8 <= len(b); {
		rel := binary.LittleEndian.Uint32(b[i:])
		size := int(binary.LittleEndian.Uint32(b[i+4:]))
		if size < 8 || i+size > len(b) {
			break
		}
		r := b[i : i+size]
		switch rel {
		case relProcessorCore:
			if len(r) < 32 {
				break
			}
			t.Cores++
			class := r[9]
			classes[class]++
			maxClass = max(maxClass, class)
			groups := int(binary.LittleEndian.Uint16(r[30:]))
			entry := ptrSize + 8
			for g := 0; g < groups; g++ {
				off := 32 + g*entry
				if off+ptrSize > len(r) {
					break
				}
				var mask uint64
				if ptrSize == 8 {
					mask = binary.LittleEndian.Uint64(r[off:])
				} else {
					mask = uint64(binary.LittleEndian.Uint32(r[off:]))
				}
				t.Threads += bits.OnesCount64(mask)
			}
		case relCache:
			if len(r) < 20 {
				break
			}
			size := uint64(binary.LittleEndian.Uint32(r[12:]))
			switch r[8] {
			case 2:
				t.L2 += size
			case 3:
				t.L3 += size
			}
		case relProcessorPackage:
			t.Sockets++
		}
		i += size
	}
	if len(classes) > 1 {
		t.PCores = classes[maxClass]
		t.ECores = t.Cores - t.PCores
	}
	return t
}
