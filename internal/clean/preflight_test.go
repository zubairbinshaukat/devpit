package clean

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
)

// Safety rule 90: an archive with hatchzip's journal beside it (any part of
// its set) and hatchzip's own side files are refused with a plain reason;
// the same archive without a journal is not.
func TestPreflightRefusesAnArchiveWithAnUnfinishedExtraction(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	zip := write("Game-Backup.zip")
	if err := Preflight(Item{Path: zip}, Options{}); err != nil {
		t.Fatalf("an archive with no journal: %v", err)
	}
	journal := write("Game-Backup.zip" + archive.JournalSuffix)
	part2 := write("set.7z.002")
	write("set.7z.001")
	write("set.7z.001" + archive.JournalSuffix)
	tail := write("Game-Backup.zip" + archive.TailSuffix)
	partFile := write("textures.pak" + archive.PartSuffix)
	for _, p := range []string{zip, part2, journal, tail, partFile} {
		err := Preflight(Item{Path: p}, Options{})
		if !errors.Is(err, ErrUnfinishedExtraction) {
			t.Errorf("Preflight(%s) = %v, want ErrUnfinishedExtraction", filepath.Base(p), err)
		}
	}
	if err := checkUnfinishedExtraction(filepath.Join(dir, "notes.txt")); err != nil {
		t.Errorf("a plain file: %v", err)
	}
}
