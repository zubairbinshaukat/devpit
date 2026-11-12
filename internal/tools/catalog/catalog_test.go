package catalog_test

import (
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools/catalog"
	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
)

func TestLoadParsesEmbeddedCatalog(t *testing.T) {
	apps, err := catalog.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(apps) < 25 {
		t.Fatalf("len(apps) = %d, want at least 25", len(apps))
	}

	seen := make(map[string]bool, len(apps))
	for _, a := range apps {
		if a.Name == "" {
			t.Errorf("app with empty Name: %+v", a)
		}
		if a.Category == "" {
			t.Errorf("app %q: empty Category", a.Name)
		}
		if a.Scoop == "" && a.Winget == "" && a.Choco == "" {
			t.Errorf("app %q: no manager id set (scoop, winget and choco all empty)", a.Name)
		}
		if seen[a.Name] {
			t.Errorf("duplicate app name %q", a.Name)
		}
		seen[a.Name] = true
	}
}

func TestByCategoryGroupsAndCoversEveryApp(t *testing.T) {
	apps, err := catalog.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	byCat, err := catalog.ByCategory()
	if err != nil {
		t.Fatalf("ByCategory() error = %v", err)
	}

	total := 0
	for cat, list := range byCat {
		if cat == "" {
			t.Errorf("ByCategory has an empty category key")
		}
		total += len(list)
		for _, a := range list {
			if a.Category != cat {
				t.Errorf("app %q filed under %q, has Category %q", a.Name, cat, a.Category)
			}
		}
	}
	if total != len(apps) {
		t.Errorf("ByCategory total = %d, want %d (every app from Load)", total, len(apps))
	}
}

func TestHasManager(t *testing.T) {
	a := catalog.App{Scoop: "git", Winget: "", Choco: "git"}
	if !a.HasManager("scoop") {
		t.Error("HasManager(scoop) = false, want true")
	}
	if a.HasManager("winget") {
		t.Error("HasManager(winget) = true, want false")
	}
	if !a.HasManager("choco") {
		t.Error("HasManager(choco) = false, want true")
	}
	if a.HasManager("npm") {
		t.Error("HasManager(npm) = true, want false")
	}
}

func TestExpectedCategoriesPresent(t *testing.T) {
	byCat, err := catalog.ByCategory()
	if err != nil {
		t.Fatalf("ByCategory() error = %v", err)
	}
	want := []string{
		"Runtimes", "Editors", "Terminals & Shell", "VCS",
		"Containers", "Package Managers", "CLI Tools", "Fonts",
	}
	for _, cat := range want {
		if len(byCat[cat]) == 0 {
			t.Errorf("category %q missing or empty", cat)
		}
	}
}

// Safety rule 88: every bucket in the catalog is one Devpit knows, with the
// URL Devpit has on file for it.
func TestCatalogBucketsAreAllowlisted(t *testing.T) {
	apps, err := catalog.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	known := managers.KnownBuckets()
	withBucket := 0
	for _, a := range apps {
		if a.Bucket == "" {
			continue
		}
		withBucket++
		name, url, ok := a.ScoopBucket()
		if !ok {
			t.Errorf("app %q: bucket %q is not \"<name> <url>\"", a.Name, a.Bucket)
			continue
		}
		if known[name] != url {
			t.Errorf("app %q: bucket %q %q is not in managers.KnownBuckets with that URL", a.Name, name, url)
		}
		if a.Scoop == "" {
			t.Errorf("app %q: has a bucket but no scoop id", a.Name)
		}
		if !strings.HasPrefix(a.Scoop, name+"/") {
			t.Errorf("app %q: scoop id %q should be qualified with its bucket %q", a.Name, a.Scoop, name)
		}
	}
	if withBucket == 0 {
		t.Error("no app has a bucket; hatchzip should")
	}
}

func TestHatchzipIsListedUnderCLITools(t *testing.T) {
	byCat, err := catalog.ByCategory()
	if err != nil {
		t.Fatalf("ByCategory() error = %v", err)
	}
	for _, a := range byCat["CLI Tools"] {
		if a.Name == "hatchzip" {
			if a.Scoop != "xevrion/hatchzip" || !a.HasManager("scoop") || a.HasManager("winget") || a.HasManager("choco") {
				t.Errorf("hatchzip ids = %+v, want Scoop only", a)
			}
			if len(a.Detect) != 1 || a.Detect[0] != "hatchzip.exe" {
				t.Errorf("hatchzip Detect = %v", a.Detect)
			}
			return
		}
	}
	t.Error("hatchzip is not listed under CLI Tools")
}

func TestScoopBucketParsing(t *testing.T) {
	if _, _, ok := (catalog.App{}).ScoopBucket(); ok {
		t.Error("ScoopBucket() ok for an app without a bucket")
	}
	name, url, ok := catalog.App{Bucket: "a https://x.example/b"}.ScoopBucket()
	if !ok || name != "a" || url != "https://x.example/b" {
		t.Errorf("ScoopBucket() = %q %q %v", name, url, ok)
	}
}
