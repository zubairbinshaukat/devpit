package scan

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// fileID identifies a file's data rather than one of its names. Two hard
// links to the same bytes share a fileID, so the sizer can count them once.
type fileID struct {
	volume uint64
	index  uint64
}

// linkSet remembers which file identities a scan has already counted. It is
// shared across sizer workers, so every method is safe for concurrent use.
type linkSet struct {
	mu   sync.Mutex
	seen map[fileID]struct{}
}

func newLinkSet() *linkSet { return &linkSet{seen: make(map[fileID]struct{})} }

// firstTime records id and reports whether this is the first time the scan
// has seen it.
func (s *linkSet) firstTime(id fileID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.seen[id]; dup {
		return false
	}
	s.seen[id] = struct{}{}
	return true
}

// sizeResult is what sizing one matched directory produced.
type sizeResult struct {
	// Bytes is the space the tree occupies locally, with hard-linked data
	// counted once and cloud placeholders counted as nothing.
	Bytes uint64
	// Dirs is how many directories were read.
	Dirs uint64
	// Skipped counts reparse points and placeholders that were not descended.
	Skipped uint64
	// AccessDenied counts directories that could not be read at all.
	AccessDenied uint64
	// HardLinked reports that at least one file in the tree has more than one
	// name, so deleting the tree frees less than Bytes suggests.
	HardLinked bool
}

// sizeDir measures one matched directory. It is a plain iterative walk rather
// than a second fastwalk: the pool that calls it is already running one job
// per worker, so a second layer of parallelism would only add contention.
//
// The walk never descends into a reparse point and never counts a cloud
// placeholder, which is what keeps a junction inside node_modules from
// inflating the number by the size of whatever it points at.
func sizeDir(ctx context.Context, root string, dedupe bool, links *linkSet) sizeResult {
	var res sizeResult
	stack := []string{root}

	for len(stack) > 0 {
		if ctx.Err() != nil {
			return res
		}
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				res.AccessDenied++
			}
			res.Skipped++
			continue
		}
		res.Dirs++

		for _, entry := range entries {
			info, ierr := entry.Info()
			if ierr != nil {
				res.Skipped++
				continue
			}
			if isLeafMode(info.Mode()) || isReparseInfo(info) {
				res.Skipped++
				continue
			}
			if isCloudInfo(info) {
				res.Skipped++
				continue
			}
			if entry.IsDir() {
				stack = append(stack, filepath.Join(dir, entry.Name()))
				continue
			}
			if !info.Mode().IsRegular() {
				res.Skipped++
				continue
			}
			if dedupe {
				id, nlinks, ok := fileIdentity(filepath.Join(dir, entry.Name()))
				if ok && nlinks > 1 {
					res.HardLinked = true
					if !links.firstTime(id) {
						continue
					}
				}
			}
			res.Bytes += entrySize(info)
		}
	}
	return res
}

// looksHardLinked reports whether a matched directory is the kind of tree
// that shares data with a package store. pnpm builds node_modules out of
// hard links into .pnpm, so sizing one without deduplication reports several
// times the space that deleting it would actually free.
//
// This is why hard-link detection does not have to be on for every scan: the
// one layout where it matters announces itself.
func looksHardLinked(path string) bool {
	if !strings.EqualFold(filepath.Base(path), "node_modules") {
		return false
	}
	if _, err := os.Lstat(filepath.Join(path, ".pnpm")); err == nil {
		return true
	}
	return false
}
