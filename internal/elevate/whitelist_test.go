package elevate

import (
	"path/filepath"
	"testing"
)

func TestValidateRemovePathAllowsOnlyTheThreeRoots(t *testing.T) {
	t.Setenv("WINDIR", `C:\Windows`)
	t.Setenv("LOCALAPPDATA", `C:\Users\dev\AppData\Local`)
	t.Setenv("PROGRAMDATA", `C:\ProgramData`)

	allowed := []string{
		filepath.Join(`C:\Windows`, "Temp"),
		filepath.Join(`C:\Windows`, "Temp", "leftover.tmp"),
		filepath.Join(`C:\Users\dev\AppData\Local`, "CrashDumps"),
		filepath.Join(`C:\Users\dev\AppData\Local`, "CrashDumps", "app.dmp"),
		filepath.Join(`C:\ProgramData`, "Microsoft", "Windows", "WER", "ReportQueue"),
	}
	for _, p := range allowed {
		if err := validateRemovePath(p); err != nil {
			t.Errorf("validateRemovePath(%q) = %v, want nil", p, err)
		}
	}

	refused := []string{
		"",
		`C:\Users\dev\Documents\important.docx`,
		`C:\Windows\System32\kernel32.dll`,
		// A sibling that merely shares a prefix must not slip through.
		`C:\Windows\TempSomethingElse\file`,
		filepath.Join(`C:\ProgramData`, "Microsoft", "Windows", "Start Menu"),
	}
	for _, p := range refused {
		if err := validateRemovePath(p); err == nil {
			t.Errorf("validateRemovePath(%q) = nil, want a refusal", p)
		}
	}
}

func TestValidateRemovePathRefusesEverythingWhenEnvUnset(t *testing.T) {
	t.Setenv("WINDIR", "")
	t.Setenv("LOCALAPPDATA", "")
	t.Setenv("PROGRAMDATA", "")

	if err := validateRemovePath(`C:\Windows\Temp\leftover.tmp`); err == nil {
		t.Fatal("validateRemovePath succeeded with no cleanup roots configured, want a refusal")
	}
}
