package rules_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/scan/rules"
)

// fakeScoop builds a Scoop root with two apps, each holding two versions and
// a "current" junction pointing at the newer one, plus a persist directory
// full of the user's own data.
func fakeScoop(t *testing.T) (root string, ok bool) {
	t.Helper()
	root = t.TempDir()

	for _, app := range []string{"fd", "ripgrep"} {
		for _, version := range []string{"1.0.0", "2.0.0"} {
			dir := filepath.Join(root, "apps", app, version)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("creating %s: %v", dir, err)
			}
			if err := os.WriteFile(filepath.Join(dir, app+".exe"), []byte("x"), 0o644); err != nil {
				t.Fatalf("writing the app file: %v", err)
			}
		}
		link := filepath.Join(root, "apps", app, "current")
		if !makeJunction(t, link, filepath.Join(root, "apps", app, "2.0.0")) {
			return "", false
		}
	}

	persist := filepath.Join(root, "persist", "fd", "config.json")
	if err := os.MkdirAll(filepath.Dir(persist), 0o755); err != nil {
		t.Fatalf("creating the persist directory: %v", err)
	}
	if err := os.WriteFile(persist, []byte("{}"), 0o644); err != nil {
		t.Fatalf("writing the persist file: %v", err)
	}
	return root, true
}

// Safety rule 12: the version in use is never removed, and persist is never
// touched.
//
// Scoop keeps every installed version side by side and makes "current" a
// junction to the live one. Offering that version for deletion would break
// the app's shims; offering persist would delete the user's own settings,
// profiles and databases.
func TestScoopExcludesCurrentAndPersist(t *testing.T) {
	root, ok := fakeScoop(t)
	if !ok {
		t.Skip("this account cannot create a junction")
	}

	got := rules.ScoopOldVersionDirs(root)
	if len(got) != 2 {
		t.Fatalf("got %d old version directories, want 2 (the 1.0.0 of each app): %v", len(got), got)
	}
	for _, dir := range got {
		lower := strings.ToLower(filepath.ToSlash(dir))
		switch {
		case strings.Contains(lower, "/current"):
			t.Errorf("%s is the current junction and must never be offered", dir)
		case strings.Contains(lower, "/2.0.0"):
			t.Errorf("%s is the version in use and must never be offered", dir)
		case strings.Contains(lower, "/persist"):
			t.Errorf("%s is the user's own data and must never be offered", dir)
		case !strings.Contains(lower, "/1.0.0"):
			t.Errorf("%s is not a superseded version", dir)
		}
	}
}

// Reading the junction is the only honest way to know which version is live.
// When it cannot be read, the answer is to offer nothing for that app rather
// than to guess.
func TestScoopSkipsAnAppWithoutAReadableCurrent(t *testing.T) {
	root := t.TempDir()
	for _, version := range []string{"1.0.0", "2.0.0"} {
		if err := os.MkdirAll(filepath.Join(root, "apps", "mystery", version), 0o755); err != nil {
			t.Fatalf("creating a version directory: %v", err)
		}
	}

	if got := rules.ScoopOldVersionDirs(root); len(got) != 0 {
		t.Errorf("offered %v for an app whose current version cannot be determined", got)
	}
}

// ScoopCurrentVersion reads the junction and returns the version name.
func TestScoopCurrentVersion(t *testing.T) {
	root, ok := fakeScoop(t)
	if !ok {
		t.Skip("this account cannot create a junction")
	}

	got, found := rules.ScoopCurrentVersion(filepath.Join(root, "apps", "fd"))
	if !found {
		t.Fatal("the current junction could not be read")
	}
	if got != "2.0.0" {
		t.Errorf("current version = %q, want 2.0.0", got)
	}

	if _, found := rules.ScoopCurrentVersion(filepath.Join(root, "apps", "nothing-here")); found {
		t.Error("an app with no current junction reported one")
	}
}

// A missing or empty Scoop root produces nothing rather than an error.
func TestScoopWithNoInstallation(t *testing.T) {
	if got := rules.ScoopOldVersionDirs(""); got != nil {
		t.Errorf("got %v for an empty root, want nothing", got)
	}
	if got := rules.ScoopOldVersionDirs(filepath.Join(t.TempDir(), "not-scoop")); got != nil {
		t.Errorf("got %v for a missing root, want nothing", got)
	}
}

// The rules always include the download cache, and include old versions only
// when there are some.
func TestScoopRules(t *testing.T) {
	empty := rules.ScoopRules(filepath.Join(t.TempDir(), "missing"))
	if len(empty) != 1 || empty[0].Name != "scoop download cache" {
		t.Fatalf("got %d rules for a machine without Scoop, want just the cache rule", len(empty))
	}

	root, ok := fakeScoop(t)
	if !ok {
		t.Skip("this account cannot create a junction")
	}
	full := rules.ScoopRules(root)
	if len(full) != 2 {
		t.Fatalf("got %d rules, want the cache rule and the old versions rule", len(full))
	}
	for _, loc := range full[1].Locations {
		if strings.Contains(strings.ToLower(filepath.ToSlash(loc)), "/persist") {
			t.Errorf("the old versions rule targets persist: %q", loc)
		}
	}
}

// ScoopRoot honours %SCOOP% so a relocated installation is found.
func TestScoopRootHonoursTheEnvironment(t *testing.T) {
	want := filepath.Join(t.TempDir(), "scoop-elsewhere")
	t.Setenv("SCOOP", want)
	if got := rules.ScoopRoot(); got != want {
		t.Errorf("ScoopRoot() = %q, want %q", got, want)
	}
}
