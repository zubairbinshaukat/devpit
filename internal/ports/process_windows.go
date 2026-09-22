//go:build windows

package ports

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// snapshotProcesses lists every running process via a Toolhelp32 snapshot,
// giving PID, parent PID and exe base name in one shot.
func snapshotProcesses() ([]windows.ProcessEntry32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("ports: CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snap) //nolint:errcheck // best-effort cleanup

	var entries []windows.ProcessEntry32
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return entries, nil
		}
		return nil, fmt.Errorf("ports: Process32First: %w", err)
	}
	for {
		entries = append(entries, entry)
		entry = windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(entry))}
		if err := windows.Process32Next(snap, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, fmt.Errorf("ports: Process32Next: %w", err)
		}
	}
	return entries, nil
}

// queryImagePath opens pid with the minimal PROCESS_QUERY_LIMITED_INFORMATION
// right and asks for its full executable path via QueryFullProcessImageNameW.
func queryImagePath(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", fmt.Errorf("ports: OpenProcess(%d): %w", pid, err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // best-effort cleanup

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf)) //nolint:gosec // len(buf) is the fixed constant windows.MAX_PATH (260)
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", fmt.Errorf("ports: QueryFullProcessImageName(%d): %w", pid, err)
	}
	return windows.UTF16ToString(buf[:size]), nil
}

// isSession0 reports whether pid runs in session 0, the non-interactive
// session that hosts Windows services, via ProcessIdToSessionId.
func isSession0(pid uint32) (bool, error) {
	var sessionID uint32
	if err := windows.ProcessIdToSessionId(pid, &sessionID); err != nil {
		return false, fmt.Errorf("ports: ProcessIdToSessionId(%d): %w", pid, err)
	}
	return sessionID == 0, nil
}

// toProcess fills in a Process from a snapshot entry, resolving the exe
// path and Protected status. Missing image-path access (already exited,
// or another user's process) leaves Exe empty; the process is still
// reported by PID/ParentPID/Name.
func toProcess(entry windows.ProcessEntry32) Process {
	p := Process{
		PID:       entry.ProcessID,
		ParentPID: entry.ParentProcessID,
		Name:      windows.UTF16ToString(entry.ExeFile[:]),
	}
	if exe, err := queryImagePath(entry.ProcessID); err == nil {
		p.Exe = exe
		if base := baseName(exe); base != "" {
			p.Name = base
		}
	}
	p.Protected, p.ProtectedReason = Protected(p)
	return p
}

// Lookup resolves pid to its Process: name, parent PID, full exe path and
// Protected status.
func Lookup(pid uint32) (Process, error) {
	entries, err := snapshotProcesses()
	if err != nil {
		return Process{}, err
	}
	for _, entry := range entries {
		if entry.ProcessID == pid {
			return toProcess(entry), nil
		}
	}
	return Process{}, fmt.Errorf("ports: process %d not found", pid)
}

// Tree returns every descendant of pid (children, grandchildren, ...),
// parents before their own children. It never includes pid itself.
func Tree(pid uint32) []Process {
	entries, err := snapshotProcesses()
	if err != nil {
		return nil
	}
	childrenOf := make(map[uint32][]windows.ProcessEntry32, len(entries))
	for _, e := range entries {
		childrenOf[e.ParentProcessID] = append(childrenOf[e.ParentProcessID], e)
	}

	var out []Process
	var walk func(parent uint32, seen map[uint32]bool)
	walk = func(parent uint32, seen map[uint32]bool) {
		for _, child := range childrenOf[parent] {
			// Toolhelp32 can theoretically recycle a PID mid-walk; guard
			// against a cycle turning this into an infinite loop.
			if seen[child.ProcessID] {
				continue
			}
			seen[child.ProcessID] = true
			out = append(out, toProcess(child))
			walk(child.ProcessID, seen)
		}
	}
	walk(pid, map[uint32]bool{pid: true})
	return out
}
