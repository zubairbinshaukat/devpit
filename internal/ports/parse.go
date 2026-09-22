package ports

import (
	"encoding/binary"
	"net"
)

// Row sizes, in bytes, of the MIB_*ROW_OWNER_PID structs iphlpapi.dll fills
// in. All fields are DWORD-or-smaller and 4-byte aligned, so there is no
// struct padding to account for.
const (
	tcpRowSize  = 24 // MIB_TCPROW_OWNER_PID
	tcp6RowSize = 56 // MIB_TCP6ROW_OWNER_PID
	udpRowSize  = 12 // MIB_UDPROW_OWNER_PID
	udp6RowSize = 28 // MIB_UDP6ROW_OWNER_PID
)

// tcpStateNames maps MIB_TCP_STATE values to the names Windows tools show.
var tcpStateNames = map[uint32]string{
	1:  "CLOSED",
	2:  "LISTEN",
	3:  "SYN_SENT",
	4:  "SYN_RCVD",
	5:  "ESTABLISHED",
	6:  "FIN_WAIT1",
	7:  "FIN_WAIT2",
	8:  "CLOSE_WAIT",
	9:  "CLOSING",
	10: "LAST_ACK",
	11: "TIME_WAIT",
	12: "DELETE_TCB",
}

func tcpStateName(s uint32) string {
	if name, ok := tcpStateNames[s]; ok {
		return name
	}
	return "UNKNOWN"
}

// port16 reads a Windows MIB port field: a DWORD whose low-order 16 bits
// hold the port in network (big-endian) byte order and whose high-order 16
// bits are reserved/unused. field must be at least 2 bytes.
func port16(field []byte) uint16 {
	return binary.BigEndian.Uint16(field[0:2])
}

// ipv4(field) reads a DWORD IPv4 address: the four bytes are already the
// address octets in order, regardless of host endianness.
func ipv4(field []byte) net.IP {
	ip := make(net.IP, 4)
	copy(ip, field[0:4])
	return ip
}

// ipv6 copies a 16-byte address field into an independent net.IP.
func ipv6(field []byte) net.IP {
	ip := make(net.IP, 16)
	copy(ip, field[0:16])
	return ip
}

// parseTCP4 parses a MIB_TCPTABLE_OWNER_PID buffer, as returned by
// GetExtendedTcpTable(AF_INET, TCP_TABLE_OWNER_PID_ALL), into Conns. It is a
// pure function over bytes so it can be unit tested on any OS.
func parseTCP4(buf []byte) []Conn {
	if len(buf) < 4 {
		return nil
	}
	n := binary.LittleEndian.Uint32(buf[0:4])
	conns := make([]Conn, 0, n)
	off := 4
	for i := uint32(0); i < n && off+tcpRowSize <= len(buf); i++ {
		row := buf[off : off+tcpRowSize]
		state := binary.LittleEndian.Uint32(row[0:4])
		conns = append(conns, Conn{
			Proto:      "tcp",
			LocalAddr:  ipv4(row[4:8]),
			LocalPort:  port16(row[8:12]),
			RemoteAddr: ipv4(row[12:16]),
			RemotePort: port16(row[16:20]),
			State:      tcpStateName(state),
			PID:        binary.LittleEndian.Uint32(row[20:24]),
		})
		off += tcpRowSize
	}
	return conns
}

// parseTCP6 parses a MIB_TCP6TABLE_OWNER_PID buffer, as returned by
// GetExtendedTcpTable(AF_INET6, TCP_TABLE_OWNER_PID_ALL), into Conns.
func parseTCP6(buf []byte) []Conn {
	if len(buf) < 4 {
		return nil
	}
	n := binary.LittleEndian.Uint32(buf[0:4])
	conns := make([]Conn, 0, n)
	off := 4
	for i := uint32(0); i < n && off+tcp6RowSize <= len(buf); i++ {
		row := buf[off : off+tcp6RowSize]
		state := binary.LittleEndian.Uint32(row[48:52])
		conns = append(conns, Conn{
			Proto:      "tcp6",
			LocalAddr:  ipv6(row[0:16]),
			LocalPort:  port16(row[20:24]),
			RemoteAddr: ipv6(row[24:40]),
			RemotePort: port16(row[44:48]),
			State:      tcpStateName(state),
			PID:        binary.LittleEndian.Uint32(row[52:56]),
		})
		off += tcp6RowSize
	}
	return conns
}

// parseUDP4 parses a MIB_UDPTABLE_OWNER_PID buffer, as returned by
// GetExtendedUdpTable(AF_INET, UDP_TABLE_OWNER_PID), into Conns. UDP has no
// connection state, so State is left empty.
func parseUDP4(buf []byte) []Conn {
	if len(buf) < 4 {
		return nil
	}
	n := binary.LittleEndian.Uint32(buf[0:4])
	conns := make([]Conn, 0, n)
	off := 4
	for i := uint32(0); i < n && off+udpRowSize <= len(buf); i++ {
		row := buf[off : off+udpRowSize]
		conns = append(conns, Conn{
			Proto:     "udp",
			LocalAddr: ipv4(row[0:4]),
			LocalPort: port16(row[4:8]),
			PID:       binary.LittleEndian.Uint32(row[8:12]),
		})
		off += udpRowSize
	}
	return conns
}

// parseUDP6 parses a MIB_UDP6TABLE_OWNER_PID buffer, as returned by
// GetExtendedUdpTable(AF_INET6, UDP_TABLE_OWNER_PID), into Conns.
func parseUDP6(buf []byte) []Conn {
	if len(buf) < 4 {
		return nil
	}
	n := binary.LittleEndian.Uint32(buf[0:4])
	conns := make([]Conn, 0, n)
	off := 4
	for i := uint32(0); i < n && off+udp6RowSize <= len(buf); i++ {
		row := buf[off : off+udp6RowSize]
		conns = append(conns, Conn{
			Proto:     "udp6",
			LocalAddr: ipv6(row[0:16]),
			LocalPort: port16(row[20:24]),
			PID:       binary.LittleEndian.Uint32(row[24:28]),
		})
		off += udp6RowSize
	}
	return conns
}
