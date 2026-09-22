package rules_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/scan/rules"
)

// Safety rule 14: every rule has a non-empty restore hint, because the
// confirmation dialog shows it and because a cleanup nobody can undo is a
// cleanup nobody should be offered.
//
// This test walks the whole table rather than checking a sample. A rule added
// without a hint fails the build, which is the only way a rule like that
// never reaches a user.
func TestEveryRuleHasARestoreHint(t *testing.T) {
	for _, r := range rules.All() {
		if strings.TrimSpace(r.RestoreHint) == "" {
			t.Errorf("rule %q has no restore hint", r.Name)
		}
		if strings.TrimSpace(r.Description) == "" {
			t.Errorf("rule %q has no description", r.Name)
		}
	}
	for _, c := range rules.Commands() {
		if strings.TrimSpace(c.RestoreHint) == "" {
			t.Errorf("command %q has no restore hint", c.Name)
		}
		if strings.TrimSpace(c.Description) == "" {
			t.Errorf("command %q has no description", c.Name)
		}
	}
}

// Every rule must be usable: named, matchable and restorable.
func TestEveryRuleValidates(t *testing.T) {
	for _, r := range rules.All() {
		if err := r.Validate(); err != nil {
			t.Errorf("rule %q is not usable: %v", r.Name, err)
		}
	}
}

// Every command must name an executable and carry none of the arguments its
// own declaration forbids.
func TestEveryCommandIsValid(t *testing.T) {
	for _, c := range rules.Commands() {
		if err := c.Valid(); err != nil {
			t.Errorf("command %q: %v", c.Name, err)
		}
	}
}

// Safety rule 13: Docker volumes hold database contents, and
// `docker prune --volumes` is the most destructive command anywhere near
// this tool. Devpit never passes it.
func TestDockerNeverPrunesVolumes(t *testing.T) {
	forbidden := []string{"--volumes", "-v", "--all-volumes", "volume"}

	docker := rules.Docker()
	if len(docker) == 0 {
		t.Fatal("no Docker commands are declared at all")
	}
	for _, c := range docker {
		for _, arg := range c.Args {
			for _, bad := range forbidden {
				if strings.EqualFold(arg, bad) {
					t.Errorf("Docker command %q passes %q", c.Name, arg)
				}
			}
		}
		if len(c.ForbiddenArgs) == 0 {
			t.Errorf("Docker command %q does not declare its forbidden arguments", c.Name)
		}
		if err := c.Valid(); err != nil {
			t.Errorf("Docker command %q: %v", c.Name, err)
		}
	}
}

// A Docker command that somehow acquired --volumes is caught by the command's
// own validation, so the check is not only a test convention.
func TestValidCatchesAForbiddenArgument(t *testing.T) {
	bad := scan.Command{
		Name:          "Docker everything",
		Exe:           "docker",
		Args:          []string{"system", "prune", "--volumes"},
		ForbiddenArgs: []string{"--volumes"},
		RestoreHint:   "There is none, which is the point.",
	}
	var forbiddenErr *scan.ForbiddenArgError
	err := bad.Valid()
	if err == nil {
		t.Fatal("Valid accepted a command carrying a forbidden argument")
	}
	if !asForbidden(err, &forbiddenErr) {
		t.Fatalf("error = %v, want a ForbiddenArgError", err)
	}
	if forbiddenErr.Arg != "--volumes" {
		t.Errorf("Arg = %q, want --volumes", forbiddenErr.Arg)
	}
}

// Project() is the milestone 1 set: directories found by walking, nothing
// probed at a fixed address, and no file-size rules to slow the walk down.
func TestProjectRulesAreDirectoryRules(t *testing.T) {
	for _, r := range rules.Project() {
		if r.IsLocationRule() {
			t.Errorf("project rule %q is a location rule", r.Name)
		}
		if r.IsFileRule() {
			t.Errorf("project rule %q matches files by size", r.Name)
		}
		if len(r.Names) == 0 && r.Match == nil {
			t.Errorf("project rule %q has nothing to match a folder name against", r.Name)
		}
	}
}

// All() is a superset of Project(): a Full Scan never finds less than a
// Project Junk scan would.
func TestAllIsASupersetOfProject(t *testing.T) {
	all := map[string]bool{}
	for _, r := range rules.All() {
		all[r.Name] = true
	}
	for _, r := range rules.Project() {
		if !all[r.Name] {
			t.Errorf("rule %q is in Project() but not in All()", r.Name)
		}
	}
}

// findRule returns the named rule from All(), failing the test if it is
// missing.
func findRule(t *testing.T, name string) scan.Rule {
	t.Helper()
	for _, r := range rules.All() {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no rule named %q", name)
	return scan.Rule{}
}

// Finding 11: the Unity rule's markers used to be ["ProjectSettings",
// "Assets"], an any-of. A directory called "Assets" is generic enough that
// some other build tool's plain "Temp" or "Library" output folder, sitting
// next to an unrelated "Assets" folder, could satisfy that any-of and get
// swept up as Unity junk. Unity itself only ever creates
// ProjectSettings/ProjectVersion.txt at the root of a real project, so that
// is what the rule must require.
func TestUnityRuleNeedsProjectVersion(t *testing.T) {
	r := findRule(t, "unity library")

	if len(r.Markers) != 1 || r.Markers[0] != "ProjectSettings/ProjectVersion.txt" {
		t.Fatalf("unity library markers = %v, want exactly [%q]", r.Markers, "ProjectSettings/ProjectVersion.txt")
	}

	dir := t.TempDir()
	project := filepath.Join(dir, "SomeApp")
	library := filepath.Join(project, "Library")
	if err := os.MkdirAll(library, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// A plain "Assets" folder beside Library, with no Unity project marker,
	// must not be enough.
	if err := os.MkdirAll(filepath.Join(project, "Assets"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if r.HasMarker(library) {
		t.Fatal("unity rule matched on an Assets folder alone, without ProjectSettings/ProjectVersion.txt")
	}

	// Nor an empty ProjectSettings folder.
	if err := os.MkdirAll(filepath.Join(project, "ProjectSettings"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if r.HasMarker(library) {
		t.Fatal("unity rule matched on an empty ProjectSettings folder, without ProjectVersion.txt inside it")
	}

	// Only the real marker file is proof.
	if err := os.WriteFile(filepath.Join(project, "ProjectSettings", "ProjectVersion.txt"), []byte("7.0.0"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !r.HasMarker(library) {
		t.Fatal("unity rule did not match once ProjectSettings/ProjectVersion.txt was present")
	}
}

// Locations are written with %ENVVAR% placeholders rather than with a
// hard-coded C:\Users\somebody, so a machine with a relocated profile or a
// second drive still works.
func TestLocationsUsePlaceholders(t *testing.T) {
	for _, r := range rules.All() {
		for _, loc := range r.Locations {
			if filepath.IsAbs(loc) && !strings.Contains(loc, "%") {
				// Scoop's old version directories are resolved from the real
				// filesystem at call time, so they are absolute on purpose.
				if r.Name == "scoop old versions" {
					continue
				}
				t.Errorf("rule %q has the hard-coded location %q", r.Name, loc)
			}
		}
	}
}

// node_modules is the only rule allowed to list a match it could not verify.
// Everything else stays silent rather than guessing.
func TestOnlyNodeModulesAllowsUnverifiedMatches(t *testing.T) {
	for _, r := range rules.All() {
		if r.AllowUnverified && r.Name != "node_modules" {
			t.Errorf("rule %q lists unverified matches; only node_modules may", r.Name)
		}
	}
}

// Large files are the one category Devpit cannot reason about, so both rules
// are Careful: the Recycle Bin and a typed word.
func TestLargeFileRulesAreCareful(t *testing.T) {
	got := rules.LargeFiles()
	if len(got) == 0 {
		t.Fatal("no large file rules are declared")
	}
	for _, r := range got {
		if r.Tier != scan.TierCareful {
			t.Errorf("rule %q is %v, want Careful", r.Name, r.Tier)
		}
		if r.MinSize == 0 {
			t.Errorf("rule %q has no size threshold", r.Name)
		}
	}
}

// The pnpm store is hard-linked into every pnpm project on the machine, so it
// is Careful whatever else it looks like.
func TestPnpmStoreIsCareful(t *testing.T) {
	for _, r := range rules.Caches() {
		if r.Name == "pnpm store" {
			if r.Tier != scan.TierCareful {
				t.Errorf("the pnpm store rule is %v, want Careful", r.Tier)
			}
			return
		}
	}
	t.Error("there is no pnpm store rule")
}

// The Windows directory is never a location rule's address: the TUI does not
// run elevated, and %WINDIR%\Temp belongs to the elevated worker.
func TestNoRuleTargetsTheWindowsDirectory(t *testing.T) {
	for _, r := range rules.All() {
		for _, loc := range r.Locations {
			lower := strings.ToLower(loc)
			if strings.Contains(lower, "%windir%") || strings.Contains(lower, "%systemroot%") {
				t.Errorf("rule %q targets the Windows directory: %q", r.Name, loc)
			}
		}
	}
}

// asForbidden is errors.As without importing errors into a file that needs it
// exactly once.
func asForbidden(err error, target **scan.ForbiddenArgError) bool {
	if e, ok := err.(*scan.ForbiddenArgError); ok { //nolint:errorlint // The error is returned directly.
		*target = e
		return true
	}
	return false
}

// makeJunction creates a directory junction, reporting whether it worked.
func makeJunction(t *testing.T, link, target string) bool {
	t.Helper()
	if runtime.GOOS != "windows" {
		return os.Symlink(target, link) == nil
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Logf("mklink /J: %v: %s", err, out)
		return false
	}
	return true
}
