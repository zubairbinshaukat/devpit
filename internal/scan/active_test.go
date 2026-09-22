package scan_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// touch sets a path's modification time.
func touch(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("setting the time on %s: %v", path, err)
	}
}

// A project touched today is active and must never be pre-ticked, however
// much space its node_modules is using.
func TestActiveProjectIsRecognised(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), 40)

	lastUsed, active := scan.Active(dir, 7)
	if !active {
		t.Errorf("a project written a moment ago is not active; last used %v", lastUsed)
	}
	if time.Since(lastUsed) > time.Minute {
		t.Errorf("last used = %v, want roughly now", lastUsed)
	}
}

// A project nobody has opened in months is not active.
func TestAbandonedProjectIsNotActive(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "package.json")
	writeFile(t, file, 40)

	old := time.Now().Add(-90 * 24 * time.Hour)
	touch(t, file, old)
	touch(t, dir, old)

	lastUsed, active := scan.Active(dir, 7)
	if active {
		t.Errorf("a project last touched %v is reported active", lastUsed)
	}
	if time.Since(lastUsed) < 80*24*time.Hour {
		t.Errorf("last used = %v, want roughly 90 days ago", lastUsed)
	}
}

// .git/index changes on every stage, commit and checkout, which makes it the
// best single signal that somebody is working in a repository even when no
// top-level file has been edited.
func TestGitIndexCountsAsActivity(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "package.json")
	writeFile(t, file, 40)
	index := filepath.Join(dir, ".git", "index")
	writeFile(t, index, 10)

	old := time.Now().Add(-60 * 24 * time.Hour)
	touch(t, file, old)
	touch(t, dir, old)

	if _, active := scan.Active(dir, 7); !active {
		t.Error("a repository with a freshly written .git/index is not active")
	}

	touch(t, index, old)
	if _, active := scan.Active(dir, 7); active {
		t.Error("a repository whose index is two months old is still reported active")
	}
}

// Sub-directories are deliberately not consulted. A build that ran last night
// must not make a project abandoned two years ago look alive.
func TestSubdirectoriesDoNotCountAsActivity(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "package.json")
	writeFile(t, file, 40)
	writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), 100)

	old := time.Now().Add(-400 * 24 * time.Hour)
	touch(t, file, old)
	touch(t, dir, old)

	if _, active := scan.Active(dir, 7); active {
		t.Error("a fresh node_modules made an abandoned project look active")
	}
}

// The window is configurable, and zero means the default of seven days.
func TestActiveWindowIsConfigurable(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "package.json")
	writeFile(t, file, 40)

	tenDaysAgo := time.Now().Add(-10 * 24 * time.Hour)
	touch(t, file, tenDaysAgo)
	touch(t, dir, tenDaysAgo)

	if _, active := scan.Active(dir, 7); active {
		t.Error("ten days ago is inside a seven-day window")
	}
	if _, active := scan.Active(dir, 30); !active {
		t.Error("ten days ago is outside a thirty-day window")
	}
	if _, active := scan.Active(dir, 0); active {
		t.Error("a window of zero did not fall back to seven days")
	}
}

// A folder that is not there produces no time and no claim of activity,
// rather than an error the caller has to handle.
func TestActiveOnAMissingFolder(t *testing.T) {
	lastUsed, active := scan.Active(filepath.Join(t.TempDir(), "gone"), 7)
	if active || !lastUsed.IsZero() {
		t.Errorf("a missing folder reported last used %v, active %v", lastUsed, active)
	}
}

// A project with only sub-directories at the top level falls back to the
// folder's own time rather than reporting nothing.
func TestActiveFallsBackToTheFolderTime(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "main.go"), 10)

	lastUsed, active := scan.Active(dir, 7)
	if lastUsed.IsZero() {
		t.Error("a folder with no top-level files reported no time at all")
	}
	if !active {
		t.Error("a folder created a moment ago is not active")
	}
}
