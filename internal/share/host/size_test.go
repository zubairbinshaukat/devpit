package host

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestMeasureSumsRegularFilesAndCountsThem(t *testing.T) {
	fsys := fstest.MapFS{
		"a.txt":         {Data: make([]byte, 10)},
		"sub/b.txt":     {Data: make([]byte, 200)},
		"sub/deep/c.md": {Data: make([]byte, 3000)},
	}
	got, err := measureFS(context.Background(), fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	if got != (FolderSize{Bytes: 3210, Files: 3}) {
		t.Errorf("size = %+v, want 3210 bytes in 3 files and nothing skipped", got)
	}
}

func TestMeasureNeverCountsALink(t *testing.T) {
	fsys := fstest.MapFS{
		"real.bin": {Data: make([]byte, 100)},
		"link.bin": {Data: make([]byte, 9999), Mode: fs.ModeSymlink},
		"junction": {Mode: fs.ModeIrregular},
		"dirlink":  {Mode: fs.ModeSymlink | fs.ModeDir}, // never entered, even if it claims to be a folder
	}
	got, err := measureFS(context.Background(), fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	if got.Bytes != 100 || got.Files != 1 || got.Skipped != 3 {
		t.Errorf("size = %+v, want only real.bin counted and 3 links skipped", got)
	}
}

// unreadableFS fails to list one folder.
type unreadableFS struct {
	fs.FS
	bad string
}

func (u unreadableFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == u.bad {
		return nil, fs.ErrPermission
	}
	return fs.ReadDir(u.FS, name)
}

func TestMeasureSkipsAnUnreadableFolderAndSaysSo(t *testing.T) {
	fsys := unreadableFS{FS: fstest.MapFS{
		"ok/a.txt":     {Data: make([]byte, 50)},
		"locked/b.txt": {Data: make([]byte, 70)},
		"top.txt":      {Data: make([]byte, 5)},
	}, bad: "locked"}
	got, err := measureFS(context.Background(), fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	if got.Bytes != 55 || got.Files != 2 || got.Skipped != 1 {
		t.Errorf("size = %+v, want 55 bytes in 2 files and 1 skipped folder", got)
	}
}

func TestMeasureStopsWhenCancelled(t *testing.T) {
	fsys := fstest.MapFS{"a/x": {Data: []byte("x")}, "b/y": {Data: []byte("y")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := measureFS(ctx, fsys, ".")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got.Files != 0 {
		t.Errorf("a cancelled walk still counted %+v", got)
	}
}

func TestMeasureOfAnEmptyFolderIsKnownZero(t *testing.T) {
	got, err := measureFS(context.Background(), fstest.MapFS{"empty": {Mode: fs.ModeDir}}, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if got != (FolderSize{}) {
		t.Errorf("size = %+v, want a finished zero", got)
	}
}

func TestMeasureFolderReadsARealFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), make([]byte, 12), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := MeasureFolder(context.Background(), dir)
	if err != nil || got.Bytes != 12 || got.Files != 1 {
		t.Errorf("size = %+v, %v", got, err)
	}
}
