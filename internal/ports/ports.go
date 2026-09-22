// Package ports enumerates TCP/UDP connections, resolves the owning process
// (name, parent, descendants), and can terminate a process while refusing
// PIDs, system binaries and Windows services that must never be killed
// (docs/safety.md rule 16).
//
// Windows syscalls live in _windows.go files; _other.go stubs let the pure
// parsing and policy logic in this file build and test on every OS.
package ports

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Conn is one row from a Windows TCP or UDP connection table.
type Conn struct {
	// Proto is "tcp", "tcp6", "udp" or "udp6".
	Proto      string
	LocalAddr  net.IP
	LocalPort  uint16
	RemoteAddr net.IP
	RemotePort uint16
	// State is the TCP connection state ("LISTEN", "ESTABLISHED", ...).
	// UDP rows have no state and leave this empty.
	State string
	PID   uint32
}

// Process describes a process owning a connection, along with whether
// Devpit refuses to kill it and, if so, why.
type Process struct {
	PID             uint32
	ParentPID       uint32
	Name            string
	Exe             string
	Protected       bool
	ProtectedReason string
}

// DefaultDevPorts is the built-in list of ports "Busy dev ports" checks:
// 3000-3010, 4200, 5000, 5173, 5174, 8000, 8080, 8081, 8888, 9000 and
// 19000-19006 (plan.md section 8). Editable in Settings.
func DefaultDevPorts() []uint16 {
	ports := make([]uint16, 0, 11+9)
	for p := uint16(3000); p <= 3010; p++ {
		ports = append(ports, p)
	}
	ports = append(ports, 4200, 5000, 5173, 5174, 8000, 8080, 8081, 8888, 9000)
	for p := uint16(19000); p <= 19006; p++ {
		ports = append(ports, p)
	}
	return ports
}

// Busy returns the connections in conns that are in the LISTEN state on one
// of ports.
func Busy(conns []Conn, ports []uint16) []Conn {
	want := make(map[uint16]struct{}, len(ports))
	for _, p := range ports {
		want[p] = struct{}{}
	}
	var out []Conn
	for _, c := range conns {
		if c.State != "LISTEN" {
			continue
		}
		if _, ok := want[c.LocalPort]; ok {
			out = append(out, c)
		}
	}
	return out
}

// ByPort lists every connection (any protocol, any state) currently bound to
// port, local side only.
func ByPort(port uint16) ([]Conn, error) {
	conns, err := List(context.Background())
	if err != nil {
		return nil, err
	}
	var out []Conn
	for _, c := range conns {
		if c.LocalPort == port {
			out = append(out, c)
		}
	}
	return out, nil
}

// WaitFree re-queries port until nothing is LISTENing on it or timeout
// elapses, returning whether it ended up free. Callers pass a 2s timeout
// after a kill to confirm "Port 3000 is free" (plan.md section 8).
func WaitFree(ctx context.Context, port uint16, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	const pollInterval = 100 * time.Millisecond
	for {
		if conns, err := ByPort(port); err == nil {
			free := true
			for _, c := range conns {
				if c.State == "LISTEN" {
					free = false
					break
				}
			}
			if free {
				return true
			}
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(pollInterval):
		}
	}
}

// treeOfferNames are the parent process names (without ".exe") for which the
// UI offers "Kill process tree" instead of just the one PID.
var treeOfferNames = map[string]struct{}{
	"npm":        {},
	"pnpm":       {},
	"yarn":       {},
	"node":       {},
	"cmd":        {},
	"powershell": {},
	"pwsh":       {},
}

// OffersTree reports whether parentName (with or without ".exe", any case)
// is one of the process-tree-having tools Devpit offers to kill recursively.
func OffersTree(parentName string) bool {
	name := strings.ToLower(strings.TrimSpace(parentName))
	name = strings.TrimSuffix(name, ".exe")
	_, ok := treeOfferNames[name]
	return ok
}

// Protected reports whether p must never be killed, and why (safety.md rule
// 16: PID 0 and 4, anything under %WINDIR%\System32, and Windows services).
// The session-0 service check needs a live Windows API call and is resolved
// through the isSession0 platform hook; the rest is pure so it can be pinned
// with a test on every OS.
func Protected(p Process) (bool, string) {
	if p.PID == 0 || p.PID == 4 {
		return true, "PID " + strconv.FormatUint(uint64(p.PID), 10) + " is a core Windows System process and can never be terminated"
	}
	name := strings.ToLower(baseName(p.Name))
	if inSystem32(p.Exe) {
		if _, exempt := shellHostExceptions[name]; !exempt {
			return true, p.Exe + " is a Windows system binary under %WINDIR%\\System32 and is refused to protect the OS"
		}
	}
	switch name {
	case "svchost.exe":
		return true, "svchost.exe hosts one or more Windows services and is never killed directly"
	case "services.exe":
		return true, "services.exe is the Windows Service Control Manager and is never killed"
	}
	if ok, err := isSession0(p.PID); err == nil && ok {
		return true, "process runs in Windows session 0, the services/system session, not a user desktop session"
	}
	return false, ""
}

// shellHostExceptions are interactive command hosts that happen to live
// under %WINDIR%\System32 but that dev tools spawn constantly and that
// Devpit explicitly offers to kill as the root of a process tree (plan.md
// section 8 lists cmd and powershell as OffersTree parents). They are
// carved out of the blanket System32 protection; every other System32
// binary, including svchost.exe and services.exe checked separately below,
// stays protected.
var shellHostExceptions = map[string]struct{}{
	"cmd.exe":        {},
	"powershell.exe": {},
	"conhost.exe":    {},
}

// inSystem32 reports whether exe is a file under %WINDIR%\System32, matched
// case-insensitively on raw path strings (not filepath, so behaviour does
// not depend on the host OS the tests run on).
func inSystem32(exe string) bool {
	if exe == "" {
		return false
	}
	windir := os.Getenv("WINDIR")
	if windir == "" {
		windir = `C:\Windows`
	}
	windir = strings.TrimRight(windir, `\/`)
	sys32 := strings.ToLower(windir + `\System32`)
	e := strings.ToLower(strings.ReplaceAll(exe, "/", `\`))
	return e == sys32 || strings.HasPrefix(e, sys32+`\`)
}

// baseName returns the last path element of name, treating both '/' and '\'
// as separators.
func baseName(name string) string {
	name = strings.ReplaceAll(name, "/", `\`)
	if i := strings.LastIndexByte(name, '\\'); i >= 0 {
		return name[i+1:]
	}
	return name
}
