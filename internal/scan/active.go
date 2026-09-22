package scan

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Active reports when a project was last worked on and whether that falls
// inside the active window.
//
// "Last worked on" is the newest modification time among the project's
// top-level files plus its .git/index, which changes on every stage, commit
// and checkout. Sub-directories are deliberately not consulted: a build that
// ran last night would otherwise make a project abandoned two years ago look
// active, and node_modules touches its own mtime for reasons that have
// nothing to do with the developer.
//
// A project with no readable top-level files falls back to the directory's
// own modification time. activeDays of zero or less means seven.
func Active(projectDir string, activeDays int) (lastUsed time.Time, active bool) {
	if activeDays <= 0 {
		activeDays = 7
	}
	window := time.Duration(activeDays) * 24 * time.Hour

	var newest time.Time
	note := func(t time.Time) {
		if t.After(newest) {
			newest = t
		}
	}

	if entries, err := os.ReadDir(projectDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, ierr := entry.Info()
			if ierr != nil {
				continue
			}
			note(info.ModTime())
		}
	}
	if info, err := os.Lstat(filepath.Join(projectDir, ".git", "index")); err == nil {
		note(info.ModTime())
	}
	if newest.IsZero() {
		info, err := os.Lstat(projectDir)
		if err != nil {
			return time.Time{}, false
		}
		newest = info.ModTime()
	}

	return newest, time.Since(newest) <= window
}

// activeCache memoises Active for the length of one scan. A project with
// twenty matched directories would otherwise read its top-level listing
// twenty times.
type activeCache struct {
	mu   sync.Mutex
	days int
	seen map[string]activeEntry
}

type activeEntry struct {
	lastUsed time.Time
	active   bool
}

func newActiveCache(days int) *activeCache {
	return &activeCache{days: days, seen: make(map[string]activeEntry)}
}

// lookup returns Active for projectDir, computing it at most once per scan.
func (c *activeCache) lookup(projectDir string) (time.Time, bool) {
	key := normalizePath(projectDir)

	c.mu.Lock()
	if got, ok := c.seen[key]; ok {
		c.mu.Unlock()
		return got.lastUsed, got.active
	}
	c.mu.Unlock()

	lastUsed, active := Active(projectDir, c.days)

	c.mu.Lock()
	c.seen[key] = activeEntry{lastUsed: lastUsed, active: active}
	c.mu.Unlock()

	return lastUsed, active
}
