package extract

import (
	"context"

	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
)

// Unfinished extractions (section 6.9). Devpit's own record says which
// extractions it started; hatchzip's journal beside the archive says whether
// one is still unfinished. The side files are hatchzip's: Devpit only ever
// Lstats them, and never deletes, moves or edits them (rule 81).

// Pending lists Devpit's records whose journal still exists. A record whose
// journal is gone is dropped from the store (hatchzip finished, or the user
// cleaned up); side files are never touched.
func (e *Engine) Pending(ctx context.Context) []Unfinished {
	if e.d.Records == nil || e.d.FS == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	recs, err := e.d.Records.Load()
	if err != nil {
		return nil
	}
	var (
		keep []Record
		out  []Unfinished
	)
	for i, rec := range recs {
		if ctx.Err() != nil {
			// Keep what was not looked at.
			keep = append(keep, recs[i:]...)
			break
		}
		if _, err := e.d.FS.Lstat(archive.JournalPath(rec.First)); err != nil {
			continue
		}
		keep = append(keep, rec)
		out = append(out, Unfinished{Set: probeSet(e.d.FS, rec.First), OutDir: rec.OutDir, Mode: rec.Mode, Started: rec.Started})
	}
	if len(keep) != len(recs) {
		_ = e.d.Records.Save(keep)
	}
	return out
}

// Forget drops Devpit's record only. The journal and every other side file
// stay where they are.
func (e *Engine) Forget(first string) error { return e.removeRecord(first) }

// probeSet rebuilds a set from its first volume by Lstat of each volume name,
// stopping at the first one missing or not a regular file.
func probeSet(fsys StatFS, first string) archive.Set {
	f, _, _, _ := archive.Classify(first)
	set := archive.Set{Picked: first, First: first, Format: f}
	for n := 1; n <= 10_000; n++ {
		name, ok := archive.Volume(first, n)
		if !ok {
			break
		}
		fi, err := fsys.Lstat(name)
		if err != nil || !fi.Mode().IsRegular() {
			break
		}
		set.Parts = append(set.Parts, name)
		set.Bytes += fi.Size()
	}
	if len(set.Parts) == 0 {
		set.Parts = []string{first}
	}
	return set
}
