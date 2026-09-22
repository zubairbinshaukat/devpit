package scan_test

import (
	"path/filepath"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// Tier and Kind are shown to the user as words, so the words are pinned.
func TestTierAndKindWords(t *testing.T) {
	tiers := map[scan.Tier]string{
		scan.TierSafe:    "Safe",
		scan.TierReview:  "Review",
		scan.TierCareful: "Careful",
		scan.Tier(99):    "Unknown",
	}
	for tier, want := range tiers {
		if got := tier.String(); got != want {
			t.Errorf("Tier(%d).String() = %q, want %q", tier, got, want)
		}
	}

	kinds := map[scan.Kind]string{
		scan.KindProjectJunk:  "Project junk",
		scan.KindPackageCache: "Package cache",
		scan.KindWinTemp:      "Windows temp",
		scan.KindEditorCache:  "Editor cache",
		scan.KindLargeFile:    "Large file",
		scan.KindScoop:        "Scoop",
		scan.KindDocker:       "Docker",
		scan.KindChoco:        "Chocolatey",
		scan.KindWinget:       "winget",
		scan.Kind(99):         "Unknown",
	}
	for kind, want := range kinds {
		if got := kind.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}

// Name matching ignores case, because Windows does. "Dist" and "dist" are one
// folder as far as the filesystem is concerned, and the rule table has to
// agree with the filesystem.
func TestRuleMatchesIgnoresCase(t *testing.T) {
	r := scan.Rule{Names: []string{"node_modules", "Dist"}}

	for _, name := range []string{"node_modules", "NODE_MODULES", "Node_Modules", "dist", "DIST", "Dist"} {
		if !r.Matches(name) {
			t.Errorf("Matches(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"node_module", "distribution", "src"} {
		if r.Matches(name) {
			t.Errorf("Matches(%q) = true, want false", name)
		}
	}
}

// A rule can carry a predicate for the cases a name list cannot express. The
// predicate is always handed a lower-cased name.
func TestRuleMatchesWithAPredicate(t *testing.T) {
	var seen string
	r := scan.Rule{Match: func(lower string) bool {
		seen = lower
		return lower == "cmake-build-debug"
	}}

	if !r.Matches("CMake-Build-Debug") {
		t.Error("the predicate was not given a lower-cased name, or did not match")
	}
	if seen != "cmake-build-debug" {
		t.Errorf("the predicate saw %q, want a lower-cased name", seen)
	}
}

// Safety rule 9 at the level of one rule: the marker has to be there.
func TestHasMarker(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "proj")
	junk := filepath.Join(project, "node_modules")
	mkdir(t, junk)

	r := scan.Rule{Markers: []string{"package.json"}}
	if r.HasMarker(junk) {
		t.Error("HasMarker found a package.json that is not there")
	}

	writeFile(t, filepath.Join(project, "package.json"), 2)
	if !r.HasMarker(junk) {
		t.Error("HasMarker did not find the package.json beside the folder")
	}
}

// A marker may be a glob, which is how .NET's bin and obj are gated on a
// project file whose name nobody knows in advance.
func TestHasMarkerWithAGlob(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "App")
	bin := filepath.Join(project, "bin")
	mkdir(t, bin)

	r := scan.Rule{Markers: []string{"*.csproj", "*.sln"}}
	if r.HasMarker(bin) {
		t.Error("HasMarker matched a glob with nothing to match")
	}

	writeFile(t, filepath.Join(project, "App.csproj"), 10)
	if !r.HasMarker(bin) {
		t.Error("HasMarker did not match App.csproj against *.csproj")
	}
}

// A marker can point at a specific file nested inside a subdirectory of the
// matched item's parent, not just a name directly beside it. Unity's rule
// uses this to key on ProjectSettings/ProjectVersion.txt rather than on the
// ProjectSettings folder existing at all, so a sibling folder that merely
// happens to be called "ProjectSettings" (or "Assets") cannot fake the
// match.
func TestHasMarkerWithASubpath(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "proj")
	library := filepath.Join(project, "Library")
	mkdir(t, library)
	mkdir(t, filepath.Join(project, "ProjectSettings"))

	r := scan.Rule{Markers: []string{"ProjectSettings/ProjectVersion.txt"}}
	if r.HasMarker(library) {
		t.Error("HasMarker matched on the ProjectSettings folder alone, without the file inside it")
	}

	writeFile(t, filepath.Join(project, "ProjectSettings", "ProjectVersion.txt"), 5)
	if !r.HasMarker(library) {
		t.Error("HasMarker did not find ProjectSettings/ProjectVersion.txt beside the folder")
	}
}

// A rule with no markers has nothing to prove, so it always matches.
func TestHasMarkerWithoutMarkers(t *testing.T) {
	r := scan.Rule{}
	if !r.HasMarker(filepath.Join(t.TempDir(), "anywhere")) {
		t.Error("a markerless rule demanded a marker")
	}
}

// Unity is recognised by what is inside the folder, not beside it.
func TestHasMarkerInside(t *testing.T) {
	dir := t.TempDir()
	library := filepath.Join(dir, "Library")
	writeFile(t, filepath.Join(library, "ArtifactDB"), 10)

	inside := scan.Rule{Markers: []string{"ArtifactDB"}, MarkersInside: true}
	if !inside.HasMarker(library) {
		t.Error("a MarkersInside rule did not look inside the folder")
	}

	beside := scan.Rule{Markers: []string{"ArtifactDB"}}
	if beside.HasMarker(library) {
		t.Error("an ordinary rule found a marker that is inside rather than beside")
	}
}

// Validate is what the rule table test calls, so its own rules are pinned.
func TestRuleValidate(t *testing.T) {
	cases := []struct {
		name string
		rule scan.Rule
		ok   bool
	}{
		{"complete", scan.Rule{Name: "x", Names: []string{"x"}, RestoreHint: "Run it again."}, true},
		{"no name", scan.Rule{Names: []string{"x"}, RestoreHint: "Run it again."}, false},
		{"no restore hint", scan.Rule{Name: "x", Names: []string{"x"}}, false},
		{"nothing to match", scan.Rule{Name: "x", RestoreHint: "Run it again."}, false},
		{"location only", scan.Rule{Name: "x", Locations: []string{`%TEMP%\x`}, RestoreHint: "It refills."}, true},
		{"size only", scan.Rule{Name: "x", MinSize: 1, RestoreHint: "Download it again."}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.rule.Validate()
			if tc.ok && err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
			if !tc.ok && err == nil {
				t.Error("Validate() = nil, want an error")
			}
		})
	}
}

// A command has to name an executable and explain how to undo it.
func TestCommandValid(t *testing.T) {
	if err := (scan.Command{RestoreHint: "x"}).Valid(); err == nil {
		t.Error("a command with no executable was accepted")
	}
	if err := (scan.Command{Exe: "docker"}).Valid(); err == nil {
		t.Error("a command with no restore hint was accepted")
	}
	good := scan.Command{Exe: "docker", Args: []string{"builder", "prune"}, RestoreHint: "It rebuilds."}
	if err := good.Valid(); err != nil {
		t.Errorf("Valid() = %v, want nil", err)
	}
}

// IsFileRule and IsLocationRule are how the walker decides what to do with a
// rule, so their answers are pinned.
func TestRuleClassification(t *testing.T) {
	dir := scan.Rule{Names: []string{"dist"}}
	file := scan.Rule{MinSize: 1024}
	loc := scan.Rule{Locations: []string{`%TEMP%\x`}}

	if dir.IsFileRule() || dir.IsLocationRule() {
		t.Error("a name rule was classified as a file or location rule")
	}
	if !file.IsFileRule() {
		t.Error("a size rule was not classified as a file rule")
	}
	if !loc.IsLocationRule() {
		t.Error("a location rule was not classified as one")
	}
}
