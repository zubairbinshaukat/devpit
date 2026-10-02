package scan_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/protect"
	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/scan/rules"
)

// homeEnv is a protect.LookupEnv naming home as the user profile, so a test
// can protect a fake profile without touching the real one.
func homeEnv(home string) protect.LookupEnv {
	return func(k string) (string, bool) {
		switch k {
		case "USERPROFILE":
			return home, true
		case "APPDATA":
			return filepath.Join(home, "AppData", "Roaming"), true
		}
		return "", false
	}
}

// fakeProfile writes a profile whose login folders hold things every rule
// would call junk: a Node project with node_modules inside .claude (Claude
// Code's own local install has exactly this shape), a pycache inside .ssh, a
// venv inside the GitHub CLI folder, and one ordinary project the scan must
// still find.
func fakeProfile(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "me")

	writeFile(t, filepath.Join(home, ".claude", "local", "package.json"), 40)
	writeFile(t, filepath.Join(home, ".claude", "local", "node_modules", "dep", "index.js"), 900)
	writeFile(t, filepath.Join(home, ".ssh", "__pycache__", "x.pyc"), 300)
	writeFile(t, filepath.Join(home, ".devpit", "accounts", "claude", "work", "package.json"), 40)
	writeFile(t, filepath.Join(home, ".devpit", "accounts", "claude", "work", "node_modules", "d", "i.js"), 400)
	roaming := filepath.Join(home, "AppData", "Roaming")
	writeFile(t, filepath.Join(roaming, "GitHub CLI", "requirements.txt"), 10)
	writeFile(t, filepath.Join(roaming, "GitHub CLI", ".venv", "lib", "x.py"), 300)

	writeFile(t, filepath.Join(home, "work", "api", "package.json"), 40)
	writeFile(t, filepath.Join(home, "work", "api", "node_modules", "dep", "index.js"), 700)
	return home
}

// assertOnlyTheProject fails unless the one ordinary project was found and
// nothing inside a login folder was.
func assertOnlyTheProject(t *testing.T, home string, items []scan.Item) {
	t.Helper()
	got := relPaths(t, home, items)
	if !contains(got, "work/api/node_modules") {
		t.Errorf("the ordinary project was not found; got %v", got)
	}
	for _, rel := range got {
		lower := strings.ToLower(rel)
		for _, guarded := range []string{".claude", ".ssh", ".devpit", "appdata/roaming/github cli"} {
			if strings.HasPrefix(lower, guarded) {
				t.Errorf("reported %s, which is inside %s", rel, guarded)
			}
		}
	}
}

// Safety rule 30, the scanner half: a scan of the whole profile never
// descends into a login or account folder and never reports what is inside
// one, whatever the rule.
func TestScanNeverReportsJunkInsideALoginFolder(t *testing.T) {
	t.Parallel()
	home := fakeProfile(t)
	writeFile(t, filepath.Join(home, ".claude", "projects", "huge.jsonl"), 4096)
	writeFile(t, filepath.Join(home, "work", "huge.iso"), 4096)

	opts := scan.Options{
		Roots:   []string{home},
		Workers: 4,
		Rules: append(rules.Project(), scan.Rule{
			Name:        "test large file",
			Kind:        scan.KindLargeFile,
			MinSize:     1024,
			Tier:        scan.TierCareful,
			RestoreHint: "Download it again.",
		}),
		Protect: protect.Default(homeEnv(home)),
	}
	items, _, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertOnlyTheProject(t, home, items)
	if !contains(relPaths(t, home, items), "work/huge.iso") {
		t.Error("the large file rule found nothing outside the login folders either")
	}
}

// The built-in list is applied when the caller passes no Protect at all,
// which is what every existing caller does. The profile comes from the
// environment the scan runs in.
func TestScanAppliesTheBuiltInListWithoutBeingAsked(t *testing.T) {
	home := fakeProfile(t)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))

	items, _, err := runScan(t, projectOptions(home))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertOnlyTheProject(t, home, items)
}

// A scan that starts inside a login folder finds nothing.
func TestScanRootedInsideALoginFolderFindsNothing(t *testing.T) {
	t.Parallel()
	home := fakeProfile(t)
	opts := projectOptions(filepath.Join(home, ".claude"))
	opts.Protect = protect.Default(homeEnv(home))

	items, _, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("reported %v from inside .claude", relPaths(t, home, items))
	}
}

// An account root registered at runtime, anywhere on disk, is protected the
// same way: nothing inside it is reported, and a junk-looking folder that
// holds it is not reported either, because deleting that would delete the
// account with it.
func TestScanNeverReportsJunkInsideARegisteredAccountRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// An imported account that lives outside the profile.
	acct := filepath.Join(root, "imported", "claude-work")
	writeFile(t, filepath.Join(acct, "package.json"), 40)
	writeFile(t, filepath.Join(acct, "node_modules", "dep", "index.js"), 500)

	// An account folder that sits, oddly, inside a folder named like junk.
	writeFile(t, filepath.Join(root, "proj", "package.json"), 40)
	nested := filepath.Join(root, "proj", "node_modules", "acct")
	writeFile(t, filepath.Join(nested, ".credentials.json"), 50)
	writeFile(t, filepath.Join(root, "proj", "node_modules", "dep", "index.js"), 300)

	// A control project.
	writeFile(t, filepath.Join(root, "web", "package.json"), 40)
	writeFile(t, filepath.Join(root, "web", "node_modules", "dep", "index.js"), 200)

	opts := projectOptions(root)
	opts.Protect = protect.List{}.With(acct, nested)
	items, _, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := relPaths(t, root, items)
	if !contains(got, "web/node_modules") {
		t.Errorf("the control project was not found; got %v", got)
	}
	for _, rel := range got {
		lower := strings.ToLower(rel)
		if strings.HasPrefix(lower, "imported/") {
			t.Errorf("reported %s, inside a registered account root", rel)
		}
		if lower == "proj/node_modules" {
			t.Errorf("reported %s, which holds a registered account root", rel)
		}
		if strings.HasPrefix(lower, "proj/node_modules/acct") {
			t.Errorf("reported %s, inside a registered account root", rel)
		}
	}
}

// A location rule whose fixed address is, holds or sits inside a login
// folder is never probed into a result.
func TestLocationRulesNeverReachALoginFolder(t *testing.T) {
	home := fakeProfile(t)
	writeFile(t, filepath.Join(home, "cache", "blob.bin"), 512)
	t.Setenv("DEVPIT_TEST_PROTECT_HOME", home)

	opts := scan.Options{
		Roots:   []string{filepath.Join(home, "work")},
		Workers: 2,
		Rules: []scan.Rule{{
			Name: "test locations",
			Kind: scan.KindPackageCache,
			Locations: []string{
				`%DEVPIT_TEST_PROTECT_HOME%`,
				`%DEVPIT_TEST_PROTECT_HOME%\.ssh`,
				`%DEVPIT_TEST_PROTECT_HOME%\.claude\local`,
				`%DEVPIT_TEST_PROTECT_HOME%\cache`,
			},
			Tier:        scan.TierSafe,
			RestoreHint: "It refills itself.",
		}},
		Protect: protect.Default(homeEnv(home)),
	}
	items, _, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := relPaths(t, home, items)
	if len(got) != 1 || !strings.EqualFold(got[0], "cache") {
		t.Errorf("got %v, want only the cache folder", got)
	}
}

// Verify refuses a login folder, a path inside one and a path that holds
// one, so a stale result can never be deleted through it.
func TestVerifyRefusesLoginFolders(t *testing.T) {
	home := fakeProfile(t)
	t.Setenv("USERPROFILE", home)

	for _, p := range []string{
		filepath.Join(home, ".claude", "local", "node_modules"),
		filepath.Join(home, ".claude"),
		home,
	} {
		it := scan.Item{Path: p, Name: filepath.Base(p), Rule: "node_modules"}
		if err := scan.Verify(it); !errors.Is(err, scan.ErrProtectedPath) {
			t.Errorf("Verify(%s) = %v, want ErrProtectedPath", p, err)
		}
	}
}

// A cached scan from before a folder was protected never brings an item
// inside it back onto the screen.
func TestCachedItemsInsideALoginFolderAreDropped(t *testing.T) {
	home := fakeProfile(t)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()

	keep := scan.Item{Path: filepath.Join(home, "work", "api", "node_modules"), Name: "node_modules", Size: 700}
	drop := scan.Item{Path: filepath.Join(home, ".claude", "local", "node_modules"), Name: "node_modules", Size: 900}
	if err := scan.SaveCache(dir, &scan.Cache{Root: home, Items: []scan.Item{keep, drop}}); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	c, err := scan.LoadCache(dir, home)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if len(c.Items) != 1 || c.Items[0].Path != keep.Path {
		t.Errorf("cached items = %v, want only %s", c.Items, keep.Path)
	}
}
