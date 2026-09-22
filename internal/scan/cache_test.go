package scan_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// A cache is only worth having if what comes back is what went in, down to
// the flags the results table pre-selects on.
func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	when := time.Now().Add(-2 * time.Hour).Truncate(time.Second)

	want := &scan.Cache{
		Root: `D:\work`,
		When: when,
		Items: []scan.Item{
			{
				Path: `D:\work\api\node_modules`, Name: "node_modules", Project: `D:\work\api`,
				Size: 1 << 33, Tier: scan.TierSafe, Kind: scan.KindProjectJunk,
				Rule: "node_modules", RestoreHint: "Run npm install.",
				Markers: []string{"package.json"}, LastUsed: when, ModTime: when,
			},
			{
				Path: `D:\work\orphan\node_modules`, Name: "node_modules",
				Unverified: true, Cloud: true, HardLinkedToStore: true,
				Tier: scan.TierReview, RestoreHint: "Run npm install.",
			},
		},
	}

	if err := scan.SaveCache(dir, want); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	got, err := scan.LoadCache(dir, `D:\work`)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}

	if got.Root != want.Root || !got.When.Equal(want.When) {
		t.Errorf("root or timestamp changed: got %q %v, want %q %v", got.Root, got.When, want.Root, want.When)
	}
	if got.Version != scan.CacheVersion {
		t.Errorf("Version = %d, want %d", got.Version, scan.CacheVersion)
	}
	if len(got.Items) != len(want.Items) {
		t.Fatalf("got %d items, want %d", len(got.Items), len(want.Items))
	}
	if got.Items[0].Size != 1<<33 {
		t.Errorf("a size over 4 GB did not survive the round trip: %d", got.Items[0].Size)
	}
	if !got.Items[1].Unverified || !got.Items[1].Cloud || !got.Items[1].HardLinkedToStore {
		t.Errorf("the flags did not survive the round trip: %+v", got.Items[1])
	}
	if got.Bytes() != got.Items[0].Size+got.Items[1].Size {
		t.Errorf("Bytes() = %d, want the sum of the item sizes", got.Bytes())
	}
}

// The cache file is keyed per root, so scanning a second folder does not
// throw away the first one's results.
func TestCacheKeepsOneEntryPerRoot(t *testing.T) {
	dir := t.TempDir()

	if err := scan.SaveCache(dir, &scan.Cache{Root: `D:\one`, Items: []scan.Item{{Path: "one"}}}); err != nil {
		t.Fatalf("SaveCache one: %v", err)
	}
	if err := scan.SaveCache(dir, &scan.Cache{Root: `D:\two`, Items: []scan.Item{{Path: "two"}}}); err != nil {
		t.Fatalf("SaveCache two: %v", err)
	}

	one, err := scan.LoadCache(dir, `D:\one`)
	if err != nil {
		t.Fatalf("LoadCache one: %v", err)
	}
	if len(one.Items) != 1 || one.Items[0].Path != "one" {
		t.Errorf("the first root's entry was overwritten: %+v", one.Items)
	}

	// Windows paths are case-insensitive, so a different casing is the same
	// entry rather than a second one.
	upper, err := scan.LoadCache(dir, `D:\ONE`)
	if err != nil {
		t.Fatalf("LoadCache with different casing: %v", err)
	}
	if len(upper.Items) != 1 {
		t.Errorf("a differently cased root did not find the same entry")
	}
}

// Asking for a root that was never scanned is an ordinary answer, not a
// failure: the caller simply scans.
func TestLoadCacheWithoutAFile(t *testing.T) {
	if _, err := scan.LoadCache(t.TempDir(), `D:\nothing`); !errors.Is(err, scan.ErrNoCache) {
		t.Errorf("error = %v, want ErrNoCache", err)
	}
}

func TestLoadCacheWithoutAnEntryForThisRoot(t *testing.T) {
	dir := t.TempDir()
	if err := scan.SaveCache(dir, &scan.Cache{Root: `D:\one`}); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	if _, err := scan.LoadCache(dir, `D:\elsewhere`); !errors.Is(err, scan.ErrNoCache) {
		t.Errorf("error = %v, want ErrNoCache", err)
	}
}

// Safety rule 20's shape, applied to the cache: a file that cannot be read is
// moved aside and named, never silently ignored and never fatal.
func TestCorruptCacheIsQuarantined(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, scan.CacheFileName)
	if err := os.WriteFile(path, []byte("this is not a gob stream at all"), 0o600); err != nil {
		t.Fatalf("writing a corrupt cache: %v", err)
	}

	_, err := scan.LoadCache(dir, `D:\work`)
	if !errors.Is(err, scan.ErrCorruptCache) {
		t.Fatalf("error = %v, want ErrCorruptCache", err)
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Error("the corrupt cache file is still in place")
	}

	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		t.Fatalf("reading the cache directory: %v", rerr)
	}
	var quarantined int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), scan.CacheFileName+".broken-") {
			quarantined++
		}
	}
	if quarantined != 1 {
		t.Errorf("found %d quarantined files, want exactly 1: %v", quarantined, entries)
	}
}

// Saving over a corrupt file moves it aside rather than merging into it.
func TestSaveOverACorruptCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, scan.CacheFileName)
	if err := os.WriteFile(path, []byte("rubbish"), 0o600); err != nil {
		t.Fatalf("writing a corrupt cache: %v", err)
	}

	if err := scan.SaveCache(dir, &scan.Cache{Root: `D:\work`, Items: []scan.Item{{Path: "x"}}}); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	got, err := scan.LoadCache(dir, `D:\work`)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if len(got.Items) != 1 {
		t.Errorf("got %d items, want 1", len(got.Items))
	}
}

// A save leaves no temporary files behind, so the cache directory never fills
// up with debris from interrupted runs.
func TestSaveCacheLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	if err := scan.SaveCache(dir, &scan.Cache{Root: `D:\work`}); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the cache directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != scan.CacheFileName {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("cache directory holds %v, want only %s", names, scan.CacheFileName)
	}
}

// A cache entry with no root is refused: it could never be looked up again.
func TestSaveCacheRefusesAnEmptyRoot(t *testing.T) {
	if err := scan.SaveCache(t.TempDir(), &scan.Cache{}); err == nil {
		t.Error("SaveCache accepted a cache entry with no root")
	}
	if err := scan.SaveCache(t.TempDir(), nil); err == nil {
		t.Error("SaveCache accepted a nil cache")
	}
}

// A saved cache that omits its timestamp is stamped on the way out, so the
// staleness badge always has something to show.
func TestSaveCacheStampsTheTime(t *testing.T) {
	dir := t.TempDir()
	before := time.Now().Add(-time.Second)
	if err := scan.SaveCache(dir, &scan.Cache{Root: `D:\work`}); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	got, err := scan.LoadCache(dir, `D:\work`)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if got.When.Before(before) {
		t.Errorf("When = %v, want a time from this run", got.When)
	}
}
