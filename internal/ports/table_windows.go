//go:build windows

package ports

import (
	"context"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// iphlpapi.dll is not wrapped by golang.org/x/sys/windows for the extended
// TCP/UDP owner-PID tables, so the two procs Devpit needs are declared here.
var (
	modIphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTCPTable = modIphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtendedUDPTable = modIphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	afINET  = 2  // AF_INET
	afINET6 = 23 // AF_INET6

	tcpTableOwnerPIDAll = 5 // TCP_TABLE_OWNER_PID_ALL
	udpTableOwnerPID    = 1 // UDP_TABLE_OWNER_PID

	initialTableBufSize  = 8 * 1024
	maxTableGrowAttempts = 12
)

// getExtendedTable calls proc (GetExtendedTcpTable or GetExtendedUdpTable)
// for the given address family and table class, growing the buffer on
// ERROR_INSUFFICIENT_BUFFER (122) until it fits.
func getExtendedTable(proc *windows.LazyProc, family, tableClass uint32) ([]byte, error) {
	size := uint32(initialTableBufSize)
	for attempt := 0; attempt < maxTableGrowAttempts; attempt++ {
		buf := make([]byte, size)
		r1, _, _ := proc.Call(
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)),
			0, // bOrder: no need for the table sorted
			uintptr(family),
			uintptr(tableClass),
			0, // Reserved
		)
		switch windows.Errno(r1) {
		case 0:
			// len(buf) is bounded by the uint32 size that allocated it, so
			// this conversion never overflows.
			if size > uint32(len(buf)) { //nolint:gosec // bounded by the uint32 size used to allocate buf
				size = uint32(len(buf)) //nolint:gosec // bounded by the uint32 size used to allocate buf
			}
			return buf[:size], nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			// size now holds the required byte count; retry with it.
			continue
		default:
			return nil, fmt.Errorf("ports: iphlpapi table call failed: %w", windows.Errno(r1))
		}
	}
	return nil, fmt.Errorf("ports: iphlpapi table buffer did not converge after %d attempts", maxTableGrowAttempts)
}

// List enumerates every TCP v4/v6 and UDP v4/v6 connection with its owning
// PID, via GetExtendedTcpTable(TCP_TABLE_OWNER_PID_ALL) and
// GetExtendedUdpTable(UDP_TABLE_OWNER_PID) (plan.md section 8).
func List(ctx context.Context) ([]Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	tcp4, err := getExtendedTable(procGetExtendedTCPTable, afINET, tcpTableOwnerPIDAll)
	if err != nil {
		return nil, fmt.Errorf("tcp4 table: %w", err)
	}
	tcp6, err := getExtendedTable(procGetExtendedTCPTable, afINET6, tcpTableOwnerPIDAll)
	if err != nil {
		return nil, fmt.Errorf("tcp6 table: %w", err)
	}
	udp4, err := getExtendedTable(procGetExtendedUDPTable, afINET, udpTableOwnerPID)
	if err != nil {
		return nil, fmt.Errorf("udp4 table: %w", err)
	}
	udp6, err := getExtendedTable(procGetExtendedUDPTable, afINET6, udpTableOwnerPID)
	if err != nil {
		return nil, fmt.Errorf("udp6 table: %w", err)
	}

	conns := make([]Conn, 0, 64)
	conns = append(conns, parseTCP4(tcp4)...)
	conns = append(conns, parseTCP6(tcp6)...)
	conns = append(conns, parseUDP4(udp4)...)
	conns = append(conns, parseUDP6(udp6)...)
	return conns, nil
}
