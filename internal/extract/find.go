package extract

import (
	"context"
	"io/fs"
	"sort"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
)

// FindArchives lists supported archive sets of at least minBytes under dirs,
// down to depth, using only directory names and Lstat, stopping at
// maxEntries or when ctx ends. Sets with a journal are marked.
//
// depth counts folder levels: 1 is the files directly in each dir, 3 also
// looks two folders down. Every name read counts toward maxEntries. Links and
// junctions are never entered, and a link is never listed as an archive.
// Any part of a split set finds the whole set once, keyed by its first
// volume; a set whose first volume is missing is skipped. An unfinished set
// is listed whatever its size, because in place it has already shrunk.
// Side files (rule 81) are never listed. The result is newest first.
func FindArchives(ctx context.Context, fsys StatFS, dirs []string, minBytes int64, depth, maxEntries int) []Found {
	if fsys == nil {
		return nil
	}
	depth = max(depth, 1)
	f := finder{ctx: ctx, fsys: fsys, left: maxEntries, seen: map[string]bool{}}
	if maxEntries <= 0 {
		f.left = 1 << 30
	}
	visited := map[string]bool{}
	for _, d := range dirs {
		k := strings.ToLower(trimSep(norm(d)))
		if d == "" || visited[k] {
			continue
		}
		visited[k] = true
		f.walk(d, depth)
	}
	var out []Found
	for _, s := range f.sets {
		if s.Unfinished || s.Set.Bytes >= minBytes {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Mod.Equal(out[j].Mod) {
			return out[i].Mod.After(out[j].Mod)
		}
		return strings.ToLower(out[i].Set.First) < strings.ToLower(out[j].Set.First)
	})
	return out
}

type finder struct {
	ctx  context.Context
	fsys StatFS
	left int
	seen map[string]bool // first volumes, lower-cased
	sets []Found
}

func (f *finder) done() bool { return f.left <= 0 || f.ctx.Err() != nil }

func (f *finder) walk(dir string, depth int) {
	if f.done() {
		return
	}
	names, err := f.fsys.ReadDirNames(dir)
	if err != nil {
		return
	}
	sort.Strings(names)
	var sub []string
	for _, name := range names {
		if f.done() {
			return
		}
		f.left--
		p := joinPath(dir, name)
		supported := archive.IsSupported(name) && !archive.IsSideFile(name)
		if !supported && depth <= 1 {
			continue // a deeper folder is never entered, so it is never stat'ed
		}
		fi, err := f.fsys.Lstat(p)
		if err != nil {
			continue
		}
		m := fi.Mode()
		if m&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			continue
		}
		if fi.IsDir() {
			sub = append(sub, p)
			continue
		}
		if supported && m.IsRegular() {
			f.add(p)
		}
	}
	if depth > 1 {
		for _, s := range sub {
			f.walk(s, depth-1)
		}
	}
}

// add records the set p belongs to, once.
func (f *finder) add(p string) {
	first := archive.FirstVolume(p)
	k := strings.ToLower(norm(first))
	if f.seen[k] {
		return
	}
	f.seen[k] = true
	set := probeSet(f.fsys, first)
	if len(set.Parts) == 0 || !strings.EqualFold(norm(set.Parts[0]), norm(first)) || !f.exists(first) {
		return
	}
	found := Found{Set: set}
	for _, part := range set.Parts {
		if fi, err := f.fsys.Lstat(part); err == nil && fi.ModTime().After(found.Mod) {
			found.Mod = fi.ModTime()
		}
	}
	_, jerr := f.fsys.Lstat(archive.JournalPath(first))
	found.Unfinished = jerr == nil
	f.sets = append(f.sets, found)
}

func (f *finder) exists(p string) bool {
	fi, err := f.fsys.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}
