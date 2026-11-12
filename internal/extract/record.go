package extract

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/share/store"
)

// recordFile is Devpit's record of the extractions it started, in
// config.CacheDir() (%LOCALAPPDATA%\devpit). It is the only file this
// package writes, and it never holds a password (rule 79).
const recordFile = "extract-jobs.json"

// fileRecords is the real RecordStore: one JSON file, written atomically by
// share/store.
type fileRecords struct {
	dir func() (string, error)
}

func (f fileRecords) path() (string, error) {
	dir := f.dir
	if dir == nil {
		dir = config.CacheDir
	}
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, recordFile), nil
}

// Load reads the records. A missing file is none; a damaged one is treated as
// none too (the next Save replaces it), so a broken record can never block an
// extraction: hatchzip's journal is what says an extraction is unfinished.
func (f fileRecords) Load() ([]Record, error) {
	p, err := f.path()
	if err != nil {
		return nil, err
	}
	var recs []Record
	if err := store.ReadJSON(p, &recs); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, store.ErrCorrupt) {
			return nil, nil
		}
		return nil, err
	}
	return recs, nil
}

// Save writes recs, or removes the file when there are none.
func (f fileRecords) Save(recs []Record) error {
	p, err := f.path()
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		return store.Remove(p)
	}
	return store.WriteJSON(p, recs)
}

// saveRecord adds (or replaces) the record of r. Start calls it before
// hatchzip starts, so a crash mid-run still leaves Devpit knowing about it.
func (e *Engine) saveRecord(r Request) error {
	if e.d.Records == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	recs, err := e.d.Records.Load()
	if err != nil {
		return err
	}
	out := make([]Record, 0, len(recs)+1)
	for _, rec := range recs {
		if !strings.EqualFold(rec.First, r.Set.First) {
			out = append(out, rec)
		}
	}
	out = append(out, Record{First: r.Set.First, OutDir: r.OutDir, Mode: r.Mode, Started: e.d.Now(), Tool: e.tool.Version})
	return e.d.Records.Save(out)
}

// removeRecord drops the record of first, if any.
func (e *Engine) removeRecord(first string) error {
	if e.d.Records == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	recs, err := e.d.Records.Load()
	if err != nil {
		return err
	}
	out := recs[:0:0]
	for _, rec := range recs {
		if !strings.EqualFold(rec.First, first) {
			out = append(out, rec)
		}
	}
	if len(out) == len(recs) {
		return nil
	}
	return e.d.Records.Save(out)
}
