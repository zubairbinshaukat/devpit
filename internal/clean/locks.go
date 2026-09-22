package clean

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// maxLockProbeFiles bounds how many files under a locked directory are handed
// to the Restart Manager. The holder is almost always holding something near
// the top, and registering an entire node_modules would cost more than the
// answer is worth.
const maxLockProbeFiles = 256

// holdersFor asks the Restart Manager which processes hold the path open.
//
// The Restart Manager answers about files, not directories, so for a directory
// the walk collects a bounded sample of the files inside it and asks about
// those. An empty result is not an error: plenty of locks have no holder the
// Restart Manager can name, and Devpit says "locked by another program" rather
// than inventing one. Devpit never terminates a holder.
func holdersFor(path string) []Holder {
	probes := lockProbePaths(path)
	if len(probes) == 0 {
		return nil
	}
	found, err := winapi.LockHolders(probes)
	if err != nil {
		return nil
	}
	seen := make(map[uint32]struct{}, len(found))
	holders := make([]Holder, 0, len(found))
	for _, h := range found {
		if _, dup := seen[h.PID]; dup {
			continue
		}
		seen[h.PID] = struct{}{}
		holders = append(holders, Holder{PID: h.PID, Name: h.Name})
	}
	if len(holders) == 0 {
		return nil
	}
	return holders
}

// lockProbePaths returns the files to register with the Restart Manager for a
// given item: the item itself when it is a file, or a bounded sample of the
// files underneath it when it is a directory. It never follows a reparse
// point.
func lockProbePaths(path string) []string {
	info, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		return []string{path}
	}
	probes := make([]string, 0, maxLockProbeFiles)
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != path && d.Type()&fs.ModeSymlink != 0 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			return nil
		}
		probes = append(probes, p)
		if len(probes) >= maxLockProbeFiles {
			return filepath.SkipAll
		}
		return nil
	})
	return probes
}

// holderNames formats holders for a sentence: "Code.exe", or
// "Code.exe and node.exe", or "Code.exe, node.exe and java.exe".
func holderNames(holders []Holder) string {
	names := make([]string, 0, len(holders))
	seen := make(map[string]struct{}, len(holders))
	for _, h := range holders {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	switch len(names) {
	case 0:
		return "another program"
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}
