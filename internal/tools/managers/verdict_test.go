package managers_test

import (
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
)

// hresult returns code the way Go's ExitCode reports a Windows HRESULT:
// the sign-extended int32.
func hresult(code uint32) int { return int(int32(code)) } //nolint:gosec // deliberate wrap, mirroring the OS.

func TestExplain(t *testing.T) {
	tests := []struct {
		name     string
		manager  string
		code     int
		lines    []string
		wantKind managers.VerdictKind
		wantText string
	}{
		{"winget ok", "winget", 0, []string{"Successfully installed"}, managers.VerdictOK, "updated"},
		{"winget in use signed", "winget", hresult(0x8A150101), nil, managers.VerdictInUse, "in use, close it and retry"},
		{"winget in use unsigned", "winget", 0x8A150101, nil, managers.VerdictInUse, "in use, close it and retry"},
		{"winget reboot to finish", "winget", hresult(0x8A150109), nil, managers.VerdictRestart, "updated, restart needed"},
		{"winget reboot for install", "winget", hresult(0x8A15010A), nil, managers.VerdictRestart, "updated, restart needed"},
		{"winget reboot initiated", "winget", hresult(0x8A15010B), nil, managers.VerdictRestart, "updated, restart needed"},
		{"winget update not applicable", "winget", hresult(0x8A15002B), nil, managers.VerdictUpToDate, "already up to date"},
		{"winget install already installed", "winget", hresult(0x8A15010D), nil, managers.VerdictUpToDate, "already up to date"},
		{"winget package already installed", "winget", hresult(0x8A150061), nil, managers.VerdictUpToDate, "already up to date"},
		{"winget pinned", "winget", hresult(0x8A150068), nil, managers.VerdictPinned, "pinned"},
		{"winget cancelled", "winget", hresult(0x8A15010C), nil, managers.VerdictCancelled, "cancelled"},
		{"winget install in progress", "winget", hresult(0x8A150102), nil, managers.VerdictFailed, "another install is running"},
		{"winget msi failed", "winget", hresult(0x8A150049), nil, managers.VerdictFailed, "msi install failed"},
		{"winget no applicable installer", "winget", hresult(0x8A150010), nil, managers.VerdictFailed, "no installer for this machine"},
		{"winget upgrade version unknown", "winget", hresult(0x8A150050), nil, managers.VerdictFailed, "installed version unknown, can't upgrade"},
		{"winget unmapped hresult", "winget", hresult(0x8A1500FF), nil, managers.VerdictFailed, "error 0x8A1500FF"},
		{"winget small code", "winget", 5, nil, managers.VerdictFailed, "exit code 5"},
		{"winget ok with restart hint", "winget", 0, []string{"Successfully installed. Restart your PC to finish installation."}, managers.VerdictRestart, "updated, restart needed"},
		{"winget ok with reboot hint", "winget", 0, []string{"A reboot is required"}, managers.VerdictRestart, "updated, restart needed"},
		{"shell restart is not a reboot", "winget", 0, []string{"Restart your terminal to use the new PATH"}, managers.VerdictOK, "updated"},
		{"restart hint ignored on failure", "winget", hresult(0x8A150049), []string{"Restart your PC"}, managers.VerdictFailed, "msi install failed"},
		{"timeout no output", "winget", -1, nil, managers.VerdictTimeout, "timed out"},
		{"timeout with output", "scoop", -1, []string{"Downloading..."}, managers.VerdictTimeout, "timed out"},
		{"scoop held", "scoop", 0, []string{"ERROR 'nodejs-lts' is held to version 20.10.0"}, managers.VerdictPinned, "held"},
		{"scoop running", "scoop", 0, []string{"ERROR Application \"git\" is still running. Close all instances and try again."}, managers.VerdictInUse, "in use, close it and retry"},
		{"scoop already latest", "scoop", 0, []string{"Latest versions for all apps are installed! For more information try 'scoop status'"}, managers.VerdictUpToDate, "already up to date"},
		{"scoop ok", "scoop", 0, []string{"'git' (2.43.0) was installed successfully!"}, managers.VerdictOK, "updated"},
		{"scoop failed", "scoop", 1, nil, managers.VerdictFailed, "exit code 1"},
		{"npm ebusy", "npm", 1, []string{"npm ERR! code EBUSY"}, managers.VerdictInUse, "in use, close it and retry"},
		{"npm failed", "npm", 1, []string{"npm ERR! code E404"}, managers.VerdictFailed, "exit code 1"},
		{"choco reboot code", "choco", 3010, nil, managers.VerdictRestart, "updated, restart needed"},
		{"choco pinned", "choco", 0, []string{"git is pinned. Skipping pinned package."}, managers.VerdictPinned, "pinned"},
		{"choco failed", "choco", 1, nil, managers.VerdictFailed, "exit code 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := managers.Explain(tt.manager, tt.code, tt.lines)
			if got.Kind != tt.wantKind || got.Text != tt.wantText {
				t.Errorf("Explain(%q, %d, %q) = {%v %q}, want {%v %q}", tt.manager, tt.code, tt.lines, got.Kind, got.Text, tt.wantKind, tt.wantText)
			}
		})
	}
}

func TestVerdictOK(t *testing.T) {
	for kind, want := range map[managers.VerdictKind]bool{
		managers.VerdictOK:         true,
		managers.VerdictRestart:    true,
		managers.VerdictUpToDate:   true,
		managers.VerdictInUse:      false,
		managers.VerdictPinned:     false,
		managers.VerdictCancelled:  false,
		managers.VerdictFailed:     false,
		managers.VerdictTimeout:    false,
		managers.VerdictCrashed:    false,
		managers.VerdictNeedsAdmin: false,
	} {
		if got := (managers.Verdict{Kind: kind}).OK(); got != want {
			t.Errorf("Verdict{%v}.OK() = %v, want %v", kind, got, want)
		}
	}
}
