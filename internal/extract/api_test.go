package extract

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools"
)

// TestEngineHasNoUIImport pins that the engine never pulls in Bubble Tea or
// any UI package, on Windows and on Linux.
func TestEngineHasNoUIImport(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	for _, goos := range []string{"windows", "linux"} {
		for _, pkg := range []string{".", "./archive", "./extracttest"} {
			cmd := exec.Command(goBin, "list", "-deps", pkg)
			cmd.Env = append(cmd.Environ(), "GOOS="+goos)
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			for _, dep := range strings.Fields(string(out)) {
				for _, banned := range []string{"bubbletea", "bubbles", "lipgloss", "/internal/ui", "/internal/app", "/internal/elevate", "cobra"} {
					if strings.Contains(dep, banned) {
						t.Errorf("%s: %s imports %s", goos, pkg, dep)
					}
				}
			}
		}
	}
}

func TestSecretNeverPrintsOrMarshals(t *testing.T) {
	s := NewSecret("hunter2")
	for _, got := range []string{fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s", s, s, s, s), fmt.Sprintf("%+v", Request{Password: s})} {
		if strings.Contains(got, "hunter2") {
			t.Fatalf("printed the password: %q", got)
		}
	}
	if _, err := json.Marshal(Request{Password: s}); err == nil {
		t.Fatal("a request with a password marshalled")
	}
	if _, err := s.MarshalText(); err == nil {
		t.Fatal("MarshalText succeeded")
	}
	if s.Empty() || !NewSecret("").Empty() {
		t.Fatal("Empty")
	}
}

func TestUseToolVersions(t *testing.T) {
	cases := []struct {
		tool tools.Tool
		err  error
	}{
		{tools.Tool{Found: false}, ErrToolMissing},
		{tools.Tool{Found: true, Path: "hatchzip", Version: "0.1.10"}, ErrToolMissing},
		{tools.Tool{Found: true, Path: absPath(), Version: "0.1.9"}, ErrToolTooOld},
		{tools.Tool{Found: true, Path: absPath(), Version: ""}, ErrToolTooOld},
		{tools.Tool{Found: true, Path: absPath(), Version: "0.1.10"}, nil},
		{tools.Tool{Found: true, Path: absPath(), Version: "0.2.0"}, nil},
		{tools.Tool{Found: true, Path: absPath(), Version: "1.0.0-beta"}, nil},
	}
	for _, c := range cases {
		_, err := UseTool(c.tool)
		if !errors.Is(err, c.err) {
			t.Errorf("UseTool(%+v) = %v, want %v", c.tool, err, c.err)
		}
	}
}

func absPath() string {
	p, err := filepath.Abs("hatchzip.exe")
	if err != nil {
		panic(err)
	}
	return p
}

func TestParsePlanSample(t *testing.T) {
	text := "Game-Backup.zip  84.2 GB zip, 96.0 GB uncompressed in 12408 entries\r\n" +
		"  available                    31.5 GB\n" +
		"  standard extraction needs    96.0 GB\n" +
		"  required                     13.8 GB (+2.0 GB reserved)\n" +
		"  available after              15.7 GB\n" +
		"  note: the archive and the folder are on the same drive\n" +
		"something new in a later hatchzip\n"
	p := ParsePlan(text)
	if p.Name != "Game-Backup.zip" || p.Format != "zip" || p.ArchiveBytes != 84_200_000_000 ||
		p.TotalBytes != 96_000_000_000 || p.Entries != 12408 || p.Available != 31_500_000_000 ||
		p.StandardNeeds != 96_000_000_000 || p.Required != 13_800_000_000 || p.Reserved != 2_000_000_000 ||
		p.AvailableAfter != 15_700_000_000 || len(p.Notes) != 1 {
		t.Fatalf("plan = %+v", p)
	}
	if len(p.Unknown) != 1 || p.Unknown[0] != "something new in a later hatchzip" {
		t.Fatalf("unknown = %q", p.Unknown)
	}
}
