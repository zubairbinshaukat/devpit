package rules_test

import (
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/scan/rules"
)

// fileRuleTakes mirrors scan's matchFile for one rule and a file big enough:
// a file rule with no names and no Match takes every file.
func fileRuleTakes(r scan.Rule, lowerName string) bool {
	if len(r.Names) == 0 && r.Match == nil {
		return true
	}
	return r.Matches(lowerName)
}

// Safety rule 90: Full Scan never lists one of hatchzip's side files,
// however big, so a clean-up cannot break an unfinished extraction.
func TestLargeFilesNeverListHatchzipSideFiles(t *testing.T) {
	sides := []string{
		"game-backup.zip.hatchzip", "game-backup.zip.hatchzip-tail", "game-backup.zip.hatchzip-folders",
		"textures.pak.hatchzip-part", "disk.iso.hatchzip-part", "set.7z.001.hatchzip", "x.7z.hatchzip-part",
	}
	all := append(rules.LargeFiles(), rules.All()...)
	for _, r := range all {
		if !r.IsFileRule() {
			continue
		}
		for _, name := range sides {
			for _, n := range []string{name, strings.ToUpper(name)} {
				if fileRuleTakes(r, strings.ToLower(n)) {
					t.Errorf("rule %q would list %s", r.Name, n)
				}
			}
		}
	}
	// Ordinary big files are still listed.
	var large scan.Rule
	for _, r := range rules.LargeFiles() {
		if r.Name == "large file" {
			large = r
		}
	}
	for _, name := range []string{"dataset.bin", "game-backup.zip", "movie.mkv", "hatchzip.exe"} {
		if !fileRuleTakes(large, name) {
			t.Errorf("the large file rule no longer lists %s", name)
		}
	}
}
