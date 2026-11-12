//go:build windows

package extract_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/protect"
)

// Safety rule 82 on the real file system: a junction above the archive, a
// symbolic link to it, and an archive that is a junction are all refused.
func TestIdentifyRefusesLinksAndJunctions(t *testing.T) {
	fsys := extract.Real().FS
	root := t.TempDir()
	target := filepath.Join(root, "real")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	zip := filepath.Join(target, "a.zip")
	if err := os.WriteFile(zip, []byte("PK"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extract.Identify(zip, fsys, protect.List{}, os.Getenv); err != nil {
		t.Fatalf("the real file: %v", err)
	}
	junction := filepath.Join(root, "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, out)
	}
	if _, err := extract.Identify(filepath.Join(junction, "a.zip"), fsys, protect.List{}, os.Getenv); !errors.Is(err, extract.ErrReparsePoint) {
		t.Errorf("through a junction: %v", err)
	}
	jzip := filepath.Join(root, "dir.zip")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", jzip, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, out)
	}
	if _, err := extract.Identify(jzip, fsys, protect.List{}, os.Getenv); !errors.Is(err, extract.ErrReparsePoint) {
		t.Errorf("a junction named .zip: %v", err)
	}
	link := filepath.Join(root, "link.zip")
	if err := os.Symlink(zip, link); err != nil {
		t.Logf("symbolic links need Developer Mode or admin here: %v", err)
		return
	}
	if _, err := extract.Identify(link, fsys, protect.List{}, os.Getenv); !errors.Is(err, extract.ErrReparsePoint) {
		t.Errorf("a symbolic link: %v", err)
	}
}

// Safety rule 78: the real StatFS, polled as hard as the poller ever does,
// never makes a rename of a held part file fail (error 32 is what opening
// files in the folder causes, verified in research; spike X1 checked folder
// listing during real hatchzip runs). 5,000 renames, each of a file written
// through an open handle like hatchzip's .hatchzip-part.
func TestPollingDuringRenamesNeverCausesASharingViolation(t *testing.T) {
	fsys := extract.Real().FS
	dir := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var wg sync.WaitGroup
	var polls atomic.Int64
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				var walk func(string)
				walk = func(d string) {
					names, _ := fsys.ReadDirNames(d)
					for _, n := range names {
						p := filepath.Join(d, n)
						if fi, err := fsys.Lstat(p); err == nil && fi.IsDir() {
							walk(p)
						}
					}
				}
				walk(dir)
				polls.Add(1)
			}
		}()
	}
	buf := make([]byte, 4096)
	for i := range 5000 {
		final := filepath.Join(dir, "sub", "f"+strconv.Itoa(i)+".bin")
		part := final + archive.PartSuffix
		f, err := os.Create(part) // #nosec G304 -- test file in t.TempDir
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write(buf)
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(part, final); err != nil {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("rename %d failed while polling: %v", i, err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if polls.Load() < 10 {
		t.Fatalf("only %d polls ran", polls.Load())
	}
}

// The real record store round-trips under a temporary cache folder.
func TestRealDepsAreWired(t *testing.T) {
	d := extract.Real()
	if d.Runner == nil || d.FS == nil || d.Records == nil || d.Free == nil || d.KeepAwake == nil {
		t.Fatalf("deps = %+v", d)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	d.Sleep(ctx, time.Millisecond)
}
