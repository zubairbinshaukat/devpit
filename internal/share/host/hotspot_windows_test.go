//go:build windows

package host

import (
	"context"
	"testing"
)

// TestTheHotspotProbeRunsOnThisPC runs the real, read-only probe once. It
// changes nothing (see TestTheHotspotProbeOnlyReads). The saved password is
// never logged, only its length.
func TestTheHotspotProbeRunsOnThisPC(t *testing.T) {
	if testing.Short() {
		t.Skip("starts PowerShell")
	}
	f, err := RunHotspotProbe(context.Background())
	if err != nil {
		t.Skipf("the probe gave no answer on this PC: %v", err)
	}
	t.Logf("noProfile=%v capability=%s state=%d ssidLen=%d passwordLen=%d band=%q 5GHz=%v 6GHz=%v session=%v",
		f.NoProfile, f.Capability.Key(), f.State, len(f.SSID), len(f.Password), f.Band, f.Band5, f.Band6, f.Session)
}
