package winapi

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Safety rule 80: Ctrl+Break never goes to group 0 (every process on the
// console, Devpit and the user's shell included), a negative id, or Devpit.
func TestCtrlBreakRefusesGroupZeroAndItself(t *testing.T) {
	for _, pid := range []int{0, -1, -42, os.Getpid()} {
		if err := SendCtrlBreak(pid); !errors.Is(err, ErrNotAGroup) {
			t.Errorf("SendCtrlBreak(%d) = %v", pid, err)
		}
	}
}

// Safety rule 82: UNC is a network path on every platform; a local drive is
// not.
func TestNetworkDriveDetection(t *testing.T) {
	for _, p := range []string{`\\server\share\a.zip`, `//server/share/a.zip`, `\\?\UNC\server\share\a.zip`, `\\.\unc\server\share`} {
		if !IsNetworkDrive(p) {
			t.Errorf("IsNetworkDrive(%q) = false", p)
		}
	}
	local := []string{`C:\`, `C:\Users\x\a.zip`, `\\?\C:\a.zip`, "relative.zip"}
	if runtime.GOOS == "windows" {
		local = append(local, filepath.Join(os.Getenv("SystemRoot"), "System32"))
	}
	for _, p := range local {
		if IsNetworkDrive(p) {
			t.Errorf("IsNetworkDrive(%q) = true", p)
		}
	}
}
