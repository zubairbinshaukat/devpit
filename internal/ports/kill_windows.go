//go:build windows

package ports

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// Kill terminates pid via OpenProcess(PROCESS_TERMINATE) + TerminateProcess.
// It refuses protected PIDs (safety.md rule 16) with a *ProtectedError, and
// turns an access-denied failure into a *AccessDeniedError so the UI can
// offer the elevated worker instead.
func Kill(pid uint32) error {
	if protected, reason, ok := lookupProtected(pid); ok && protected {
		return &ProtectedError{PID: pid, Reason: reason}
	}

	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return &AccessDeniedError{PID: pid, Err: err}
		}
		return fmt.Errorf("ports: OpenProcess(%d, PROCESS_TERMINATE): %w", pid, err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // best-effort cleanup

	if err := windows.TerminateProcess(h, 1); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return &AccessDeniedError{PID: pid, Err: err}
		}
		return fmt.Errorf("ports: TerminateProcess(%d): %w", pid, err)
	}
	return nil
}

// lookupProtected resolves pid's Protected status via Lookup. When the
// process can no longer be found in a Toolhelp snapshot (it may have raced
// with the caller and already exited, or an untrusted caller-provided PID),
// ok is false and Kill falls through to OpenProcess, which will itself fail
// harmlessly for a PID that does not exist. The hard-coded PIDs 0 and 4
// still refuse even when the snapshot lookup itself fails.
func lookupProtected(pid uint32) (protected bool, reason string, ok bool) {
	p, err := Lookup(pid)
	if err != nil {
		if pid == 0 || pid == 4 {
			return true, fmt.Sprintf("PID %d is a core Windows System process and can never be terminated", pid), true
		}
		return false, "", false
	}
	protected, reason = Protected(p)
	return protected, reason, true
}

// KillTree kills every descendant of pid first (deepest descendants before
// their parents), then pid itself. A protected descendant is skipped rather
// than aborting the whole tree; other errors stop it immediately.
func KillTree(pid uint32) error {
	descendants := Tree(pid)
	for i := len(descendants) - 1; i >= 0; i-- {
		if err := Kill(descendants[i].PID); err != nil {
			var protErr *ProtectedError
			if errors.As(err, &protErr) {
				continue
			}
			return fmt.Errorf("ports: kill tree: descendant PID %d: %w", descendants[i].PID, err)
		}
	}
	return Kill(pid)
}
