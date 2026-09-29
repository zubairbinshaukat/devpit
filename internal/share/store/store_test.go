package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/share/store"
)

type doc struct {
	A string `json:"a"`
	B int    `json:"b"`
}

func TestWriteReadRoundTripAndIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "x.json")
	if err := store.WriteJSON(path, doc{"hi", 3}); err != nil {
		t.Fatal(err)
	}
	var got doc
	if err := store.ReadJSON(path, &got); err != nil || got != (doc{"hi", 3}) {
		t.Errorf("got %+v, %v", got, err)
	}
	if os.PathSeparator == '/' {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", info.Mode().Perm())
		}
	}
}

func TestOverwriteIsAtomicAndLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	for i := range 10 {
		if err := store.WriteJSON(path, doc{"v", i}); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("%d files, want 1", len(entries))
	}
	var got doc
	_ = store.ReadJSON(path, &got)
	if got.B != 9 {
		t.Errorf("last write lost: %+v", got)
	}
}

func TestMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	var d doc
	if err := store.ReadJSON(filepath.Join(dir, "none.json"), &d); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing = %v", err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.ReadJSON(bad, &d); !errors.Is(err, store.ErrCorrupt) {
		t.Errorf("corrupt = %v", err)
	}
}

// TestAReaderNeverMakesAWriteFail pins a bug seen on real Windows: os.Open
// shares a file without FILE_SHARE_DELETE, so a record that was being read
// at the moment of a save made the atomic rename fail with "Access is denied"
// (1759 of 2000 saves in the first measurement), and the resume record or the
// cleanup manifest silently kept its old contents, or never appeared.
func TestAReaderNeverMakesAWriteFail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.json")
	if err := store.WriteJSON(path, doc{"v", -1}); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	readErr := make(chan error, 1)
	go func() {
		var d doc
		for {
			select {
			case <-stop:
				readErr <- nil
				return
			default:
			}
			if err := store.ReadJSON(path, &d); err != nil {
				readErr <- err
				return
			}
		}
	}()
	for i := range 100 {
		if err := store.WriteJSON(path, doc{"v", i}); err != nil {
			close(stop)
			t.Fatalf("save %d failed while another reader had the file: %v", i, err)
		}
	}
	close(stop)
	if err := <-readErr; err != nil {
		t.Errorf("a read failed while saves were going on: %v", err)
	}
	var got doc
	if err := store.ReadJSON(path, &got); err != nil || got.B != 99 {
		t.Errorf("last save = %+v, %v", got, err)
	}
}

func TestRemoveMissingIsFine(t *testing.T) {
	if err := store.Remove(filepath.Join(t.TempDir(), "gone")); err != nil {
		t.Error(err)
	}
}

func TestWriteFailsCleanlyWhenTheDirectoryCannotBeMade(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteJSON(filepath.Join(file, "x.json"), doc{}); err == nil {
		t.Error("wrote under a file")
	}
}
