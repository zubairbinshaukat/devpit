package ports

import (
	"encoding/binary"
	"net"
	"testing"
)

// putPort writes port into a 4-byte MIB port field: the low-order word in
// network (big-endian) byte order, high-order word zero/reserved.
func putPort(field []byte, port uint16) {
	binary.BigEndian.PutUint16(field[0:2], port)
	field[2], field[3] = 0, 0
}

// tcp4Row is the hand-built content of one MIB_TCPROW_OWNER_PID row.
// localAddr/remoteAddr are the four IP octets, in order, exactly as they
// sit in memory for the DWORD address fields.
type tcp4Row struct {
	state      uint32
	localAddr  [4]byte
	localPort  uint16
	remoteAddr [4]byte
	remotePort uint16
	pid        uint32
}

func buildTCP4Buf(rows []tcp4Row) []byte {
	buf := make([]byte, 4+len(rows)*tcpRowSize)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(rows)))
	off := 4
	for _, r := range rows {
		row := buf[off : off+tcpRowSize]
		binary.LittleEndian.PutUint32(row[0:4], r.state)
		copy(row[4:8], r.localAddr[:])
		putPort(row[8:12], r.localPort)
		copy(row[12:16], r.remoteAddr[:])
		putPort(row[16:20], r.remotePort)
		binary.LittleEndian.PutUint32(row[20:24], r.pid)
		off += tcpRowSize
	}
	return buf
}

func TestParseTCP4(t *testing.T) {
	// One LISTEN row: 127.0.0.1:8080, remote 0.0.0.0:0, PID 1234.
	buf := buildTCP4Buf([]tcp4Row{
		{state: 2 /* LISTEN */, localAddr: [4]byte{127, 0, 0, 1}, localPort: 8080, pid: 1234},
	})

	conns := parseTCP4(buf)
	if len(conns) != 1 {
		t.Fatalf("got %d conns, want 1", len(conns))
	}
	c := conns[0]
	if c.Proto != "tcp" {
		t.Errorf("Proto = %q, want tcp", c.Proto)
	}
	if !c.LocalAddr.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Errorf("LocalAddr = %v, want 127.0.0.1", c.LocalAddr)
	}
	if c.LocalPort != 8080 {
		t.Errorf("LocalPort = %d, want 8080", c.LocalPort)
	}
	if !c.RemoteAddr.Equal(net.IPv4(0, 0, 0, 0)) {
		t.Errorf("RemoteAddr = %v, want 0.0.0.0", c.RemoteAddr)
	}
	if c.State != "LISTEN" {
		t.Errorf("State = %q, want LISTEN", c.State)
	}
	if c.PID != 1234 {
		t.Errorf("PID = %d, want 1234", c.PID)
	}
}

func TestParseTCP4MultipleRows(t *testing.T) {
	buf := buildTCP4Buf([]tcp4Row{
		{state: 2, localAddr: [4]byte{10, 0, 0, 1}, localPort: 3000, pid: 100},
		{
			state:     5, /* ESTABLISHED */
			localAddr: [4]byte{10, 0, 0, 2}, localPort: 443,
			remoteAddr: [4]byte{10, 0, 0, 1}, remotePort: 51000,
			pid: 200,
		},
	})

	conns := parseTCP4(buf)
	if len(conns) != 2 {
		t.Fatalf("got %d conns, want 2", len(conns))
	}
	if conns[0].State != "LISTEN" || conns[0].LocalPort != 3000 || conns[0].PID != 100 {
		t.Errorf("row 0 = %+v", conns[0])
	}
	if conns[1].State != "ESTABLISHED" || conns[1].LocalPort != 443 || conns[1].RemotePort != 51000 || conns[1].PID != 200 {
		t.Errorf("row 1 = %+v", conns[1])
	}
	if !conns[1].RemoteAddr.Equal(net.IPv4(10, 0, 0, 1)) {
		t.Errorf("row 1 RemoteAddr = %v, want 10.0.0.1", conns[1].RemoteAddr)
	}
}

func TestParseTCP4Empty(t *testing.T) {
	buf := buildTCP4Buf(nil)
	if conns := parseTCP4(buf); len(conns) != 0 {
		t.Fatalf("got %d conns, want 0", len(conns))
	}
}

func TestParseTCP4TruncatedBuffer(t *testing.T) {
	// Claims 3 rows but the buffer only actually holds 1; parsing must stop
	// at the buffer boundary instead of panicking or reading garbage.
	full := buildTCP4Buf([]tcp4Row{
		{state: 2, localAddr: [4]byte{1, 2, 3, 4}, localPort: 80, pid: 9},
	})
	binary.LittleEndian.PutUint32(full[0:4], 3)

	conns := parseTCP4(full)
	if len(conns) != 1 {
		t.Fatalf("got %d conns, want 1 (truncated buffer should not panic)", len(conns))
	}
}

func buildTCP6Buf(local, remote net.IP, localPort, remotePort uint16, state, pid uint32) []byte {
	buf := make([]byte, 4+tcp6RowSize)
	binary.LittleEndian.PutUint32(buf[0:4], 1)
	row := buf[4 : 4+tcp6RowSize]
	copy(row[0:16], local.To16())
	// dwLocalScopeId row[16:20] left zero
	putPort(row[20:24], localPort)
	copy(row[24:40], remote.To16())
	// dwRemoteScopeId row[40:44] left zero
	putPort(row[44:48], remotePort)
	binary.LittleEndian.PutUint32(row[48:52], state)
	binary.LittleEndian.PutUint32(row[52:56], pid)
	return buf
}

func TestParseTCP6(t *testing.T) {
	buf := buildTCP6Buf(net.ParseIP("::1"), net.ParseIP("::"), 8443, 0, 2, 555)

	conns := parseTCP6(buf)
	if len(conns) != 1 {
		t.Fatalf("got %d conns, want 1", len(conns))
	}
	c := conns[0]
	if c.Proto != "tcp6" {
		t.Errorf("Proto = %q, want tcp6", c.Proto)
	}
	if !c.LocalAddr.Equal(net.ParseIP("::1")) {
		t.Errorf("LocalAddr = %v, want ::1", c.LocalAddr)
	}
	if c.LocalPort != 8443 {
		t.Errorf("LocalPort = %d, want 8443", c.LocalPort)
	}
	if c.State != "LISTEN" {
		t.Errorf("State = %q, want LISTEN", c.State)
	}
	if c.PID != 555 {
		t.Errorf("PID = %d, want 555", c.PID)
	}
}

type udp4Row struct {
	localAddr [4]byte
	localPort uint16
	pid       uint32
}

func buildUDP4Buf(rows []udp4Row) []byte {
	buf := make([]byte, 4+len(rows)*udpRowSize)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(rows)))
	off := 4
	for _, r := range rows {
		row := buf[off : off+udpRowSize]
		copy(row[0:4], r.localAddr[:])
		putPort(row[4:8], r.localPort)
		binary.LittleEndian.PutUint32(row[8:12], r.pid)
		off += udpRowSize
	}
	return buf
}

func TestParseUDP4(t *testing.T) {
	buf := buildUDP4Buf([]udp4Row{
		{localAddr: [4]byte{0, 0, 0, 0}, localPort: 53, pid: 42},
	})

	conns := parseUDP4(buf)
	if len(conns) != 1 {
		t.Fatalf("got %d conns, want 1", len(conns))
	}
	c := conns[0]
	if c.Proto != "udp" {
		t.Errorf("Proto = %q, want udp", c.Proto)
	}
	if c.LocalPort != 53 {
		t.Errorf("LocalPort = %d, want 53", c.LocalPort)
	}
	if c.State != "" {
		t.Errorf("State = %q, want empty for UDP", c.State)
	}
	if c.PID != 42 {
		t.Errorf("PID = %d, want 42", c.PID)
	}
}

func TestParseUDP6(t *testing.T) {
	buf := make([]byte, 4+udp6RowSize)
	binary.LittleEndian.PutUint32(buf[0:4], 1)
	row := buf[4 : 4+udp6RowSize]
	copy(row[0:16], net.ParseIP("::1").To16())
	putPort(row[20:24], 5353)
	binary.LittleEndian.PutUint32(row[24:28], 77)

	conns := parseUDP6(buf)
	if len(conns) != 1 {
		t.Fatalf("got %d conns, want 1", len(conns))
	}
	c := conns[0]
	if c.Proto != "udp6" {
		t.Errorf("Proto = %q, want udp6", c.Proto)
	}
	if !c.LocalAddr.Equal(net.ParseIP("::1")) {
		t.Errorf("LocalAddr = %v, want ::1", c.LocalAddr)
	}
	if c.LocalPort != 5353 {
		t.Errorf("LocalPort = %d, want 5353", c.LocalPort)
	}
	if c.PID != 77 {
		t.Errorf("PID = %d, want 77", c.PID)
	}
}

func TestTCPStateNameUnknown(t *testing.T) {
	if got := tcpStateName(9999); got != "UNKNOWN" {
		t.Errorf("tcpStateName(9999) = %q, want UNKNOWN", got)
	}
}
