package ports

import (
	"testing"
)

func TestDefaultDevPorts(t *testing.T) {
	ports := DefaultDevPorts()
	want := map[uint16]bool{
		3000: true, 3010: true, 4200: true, 5000: true, 5173: true, 5174: true,
		8000: true, 8080: true, 8081: true, 8888: true, 9000: true,
		19000: true, 19006: true,
	}
	got := make(map[uint16]bool, len(ports))
	for _, p := range ports {
		got[p] = true
	}
	for p := range want {
		if !got[p] {
			t.Errorf("DefaultDevPorts() missing %d", p)
		}
	}
	if len(ports) != 11+9+7 {
		t.Errorf("DefaultDevPorts() has %d entries, want %d", len(ports), 11+9+7)
	}
}

func TestBusyFiltersToListenOnRequestedPorts(t *testing.T) {
	conns := []Conn{
		{LocalPort: 3000, State: "LISTEN"},
		{LocalPort: 3000, State: "ESTABLISHED"}, // same port, not LISTEN
		{LocalPort: 4200, State: "LISTEN"},
		{LocalPort: 9999, State: "LISTEN"}, // not a requested port
	}
	busy := Busy(conns, []uint16{3000, 4200})
	if len(busy) != 2 {
		t.Fatalf("got %d busy conns, want 2: %+v", len(busy), busy)
	}
	for _, c := range busy {
		if c.State != "LISTEN" {
			t.Errorf("Busy returned a non-LISTEN conn: %+v", c)
		}
	}
}

func TestOffersTree(t *testing.T) {
	yes := []string{"npm", "npm.exe", "NPM.EXE", "pnpm", "yarn", "node", "Node.exe", "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "PWSH.EXE"}
	for _, name := range yes {
		if !OffersTree(name) {
			t.Errorf("OffersTree(%q) = false, want true", name)
		}
	}
	no := []string{"explorer.exe", "chrome.exe", "go.exe", "", "notnode"}
	for _, name := range no {
		if OffersTree(name) {
			t.Errorf("OffersTree(%q) = true, want false", name)
		}
	}
}

func TestProtectedRefusesPID0And4(t *testing.T) {
	for _, pid := range []uint32{0, 4} {
		protected, reason := Protected(Process{PID: pid, Name: "whatever.exe"})
		if !protected {
			t.Errorf("Protected(PID %d) = false, want true", pid)
		}
		if reason == "" {
			t.Errorf("Protected(PID %d) gave no reason (safety.md rule 16 requires one)", pid)
		}
	}
}

func TestProtectedRefusesSystem32Binaries(t *testing.T) {
	t.Setenv("WINDIR", `C:\Windows`)
	protected, reason := Protected(Process{PID: 1234, Name: "lsass.exe", Exe: `C:\Windows\System32\lsass.exe`})
	if !protected {
		t.Fatal("Protected() = false for a System32 binary, want true")
	}
	if reason == "" {
		t.Error("Protected() gave no reason for a System32 binary")
	}
}

func TestProtectedRefusesSystem32CaseInsensitively(t *testing.T) {
	t.Setenv("WINDIR", `C:\Windows`)
	protected, _ := Protected(Process{PID: 1234, Name: "x.exe", Exe: `c:\windows\system32\x.exe`})
	if !protected {
		t.Fatal("Protected() = false for a case-differing System32 path, want true")
	}
}

func TestProtectedRefusesKnownServiceHosts(t *testing.T) {
	t.Setenv("WINDIR", `C:\Windows`)
	for _, name := range []string{"svchost.exe", "SVCHOST.EXE", "services.exe"} {
		protected, reason := Protected(Process{PID: 1234, Name: name, Exe: `C:\Windows\` + name})
		if !protected {
			t.Errorf("Protected(%q) = false, want true", name)
		}
		if reason == "" {
			t.Errorf("Protected(%q) gave no reason", name)
		}
	}
}

func TestProtectedAllowsOrdinaryUserProcess(t *testing.T) {
	t.Setenv("WINDIR", `C:\Windows`)
	protected, reason := Protected(Process{
		PID: 5555, Name: "node.exe", Exe: `C:\Users\dev\AppData\Local\nvm\node.exe`,
	})
	if protected {
		t.Errorf("Protected() = true for an ordinary user process, reason: %q", reason)
	}
}

func TestProtectedExemptsShellHostsUnderSystem32(t *testing.T) {
	t.Setenv("WINDIR", `C:\Windows`)
	for _, name := range []string{"cmd.exe", "powershell.exe", "conhost.exe"} {
		protected, reason := Protected(Process{
			PID: 4321, Name: name, Exe: `C:\Windows\System32\` + name,
		})
		if protected {
			t.Errorf("Protected(%q under System32) = true (%q), want false: OffersTree lists it as a killable process-tree root", name, reason)
		}
	}
}

func TestProtectedDoesNotFalsePositiveOnSimilarPaths(t *testing.T) {
	t.Setenv("WINDIR", `C:\Windows`)
	// A directory that merely starts with "System32" as a substring, not a
	// path segment, must not be treated as being under System32.
	protected, _ := Protected(Process{
		PID: 5555, Name: "x.exe", Exe: `C:\Windows\System32Extra\x.exe`,
	})
	if protected {
		t.Error("Protected() = true for a look-alike path outside System32, want false")
	}
}
