package scan

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOneDriveFallbackProbesUserProfile covers finding 7: some OneDrive
// installs (corporate images, scripted setups) never set the
// OneDrive/OneDriveConsumer/OneDriveCommercial environment variables even
// though a sync root exists on disk. Without a fallback, oneDriveRoots
// silently returns an empty list and the scan walks straight into the
// OneDrive folder, which downloads every placeholder it touches.
func TestOneDriveFallbackProbesUserProfile(t *testing.T) {
	t.Setenv("OneDrive", "")
	t.Setenv("OneDriveConsumer", "")
	t.Setenv("OneDriveCommercial", "")

	profile := t.TempDir()
	t.Setenv("USERPROFILE", profile)

	root := filepath.Join(profile, "OneDrive - Contoso")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// A file that merely starts with "OneDrive" must not be treated as a
	// root: only directories matching the glob count.
	if err := os.WriteFile(filepath.Join(profile, "OneDrive.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := Options{}.oneDriveRoots()

	want := normalizePath(root)
	var found bool
	for _, r := range got {
		if r == want {
			found = true
		}
		if r == normalizePath(filepath.Join(profile, "OneDrive.txt")) {
			t.Errorf("oneDriveRoots() included a non-directory match: %v", got)
		}
	}
	if !found {
		t.Fatalf("oneDriveRoots() = %v, want it to include the USERPROFILE fallback %q", got, want)
	}
}

// TestOneDriveEnvVarsPreferredOverFallback covers the common case where the
// environment variables are set: the fallback still runs, but the result is
// deduplicated rather than listing the same root twice.
func TestOneDriveEnvVarsPreferredOverFallback(t *testing.T) {
	profile := t.TempDir()
	root := filepath.Join(profile, "OneDrive")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	t.Setenv("USERPROFILE", profile)
	t.Setenv("OneDrive", root)
	t.Setenv("OneDriveConsumer", "")
	t.Setenv("OneDriveCommercial", "")

	got := Options{}.oneDriveRoots()

	want := normalizePath(root)
	count := 0
	for _, r := range got {
		if r == want {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("oneDriveRoots() = %v, want exactly one entry for %q, got %d", got, want, count)
	}
}
