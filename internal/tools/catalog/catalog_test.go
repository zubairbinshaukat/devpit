package catalog_test

import (
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools/catalog"
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
