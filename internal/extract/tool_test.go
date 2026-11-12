package extract

import (
	"errors"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/tools"
)

// Safety rule 83: a missing hatchzip or one older than 0.1.10 is refused.
func TestTooOldOrMissingHatchzipIsRefused(t *testing.T) {
	for _, c := range []struct {
		tool tools.Tool
		want error
	}{
		{tools.Tool{}, ErrToolMissing},
		{tools.Tool{Found: false, Path: absPath(), Version: "0.1.10"}, ErrToolMissing},
		{tools.Tool{Found: true, Path: absPath(), Version: "0.1.9"}, ErrToolTooOld},
		{tools.Tool{Found: true, Path: absPath(), Version: "0.0.99"}, ErrToolTooOld},
		{tools.Tool{Found: true, Path: absPath(), Version: "garbage"}, ErrToolTooOld},
	} {
		if _, err := UseTool(c.tool); !errors.Is(err, c.want) {
			t.Errorf("UseTool(%+v) = %v, want %v", c.tool, err, c.want)
		}
	}
}

// Safety rule 83: hatchzip runs only from an absolute path, never a bare
// name the system would look up in the current folder.
func TestABareToolNameIsRefused(t *testing.T) {
	for _, p := range []string{"hatchzip", "hatchzip.exe", `.\hatchzip.exe`, "bin/hatchzip"} {
		if _, err := UseTool(tools.Tool{Found: true, Path: p, Version: "0.1.10"}); !errors.Is(err, ErrToolMissing) {
			t.Errorf("UseTool(%q) = %v", p, err)
		}
	}
}

// Decision 14: 0.1.10 is a minimum, not a pin.
func TestNewerVersionsAreAllowed(t *testing.T) {
	for _, v := range []string{"0.1.10", "0.1.11", "0.2.0", "1.0.0", "v0.1.12", "0.10.0", "0.1.10-rc1"} {
		tool, err := UseTool(tools.Tool{Found: true, Path: absPath(), Version: v})
		if err != nil || tool.Version != v || tool.Path != absPath() {
			t.Errorf("UseTool(%q) = %+v, %v", v, tool, err)
		}
	}
}

// Safety rule 85 (version side): with no fixed release every version
// auto-resumes; from the fixed version on, none does.
// The gate itself (mayAutoResume) calls bug79Applies with the installed
// version; TestBug79ResumesWhileProgressing pins the engine side.
func TestNoAutoResumeOnceTheFixedVersionIsInstalled(t *testing.T) {
	if bug79FixedIn != "" {
		t.Logf("bug #79 is marked fixed in %s", bug79FixedIn)
	}
	for _, c := range []struct {
		version, fixed string
		want           bool
	}{
		{"0.1.10", "", true},
		{"9.9.9", "", true},
		{"0.1.10", "0.1.12", true},
		{"0.1.11", "0.1.12", true},
		{"0.1.12", "0.1.12", false},
		{"0.2.0", "0.1.12", false},
		{"weird", "0.1.12", true},
	} {
		if got := bug79Applies(c.version, c.fixed); got != c.want {
			t.Errorf("bug79Applies(%q, %q) = %v", c.version, c.fixed, got)
		}
	}
}

func TestProgressCaps(t *testing.T) {
	if capDone(0, 100, 100) != 99 || capDone(50, 10, 100) != 50 || capDone(0, -5, 0) != 0 || capDone(0, 5_000, 0) != 5_000 {
		t.Fatal("capDone")
	}
	if capItems(0, 10, 10) != 9 || capItems(5, 2, 10) != 5 || capItems(0, 3, 0) != 3 {
		t.Fatal("capItems")
	}
	if etaWhileRunning(0) != 0 || etaWhileRunning(time.Millisecond) != time.Second || etaWhileRunning(time.Minute) != time.Minute {
		t.Fatal("etaWhileRunning")
	}
}
