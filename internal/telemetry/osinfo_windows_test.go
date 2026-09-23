package telemetry_test

import (
	"regexp"
	"runtime"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/telemetry"
)

// TestSystemInfoReportsTheRealWindowsBuild pins the one value in a report
// that comes from a syscall: RtlGetVersion's major.minor.build, and nothing
// else.
func TestSystemInfoReportsTheRealWindowsBuild(t *testing.T) {
	name, version := telemetry.SystemInfo()
	if name != runtime.GOOS {
		t.Errorf("name = %q, want %q", name, runtime.GOOS)
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(version) {
		t.Errorf("version = %q, want major.minor.build", version)
	}
	if len(version) > telemetry.MaxOSVersionLen {
		t.Errorf("version is %d chars, over the %d the wire allows", len(version), telemetry.MaxOSVersionLen)
	}
}
