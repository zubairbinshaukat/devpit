package rules

import (
	"path/filepath"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// Size thresholds for the large file rules, in bytes.
const (
	// LargeInstallerBytes is the size past which a downloaded installer or
	// disk image is worth mentioning.
	LargeInstallerBytes = 256 << 20 // 256 MB
	// LargeFileBytes is the size past which any file is worth mentioning.
	LargeFileBytes = 1 << 30 // 1 GB
)

// installerExtensions are the file types that are almost always a download
// somebody has already installed and forgotten.
var installerExtensions = map[string]struct{}{
	".iso":  {},
	".msi":  {},
	".msix": {},
	".appx": {},
	".vhd":  {},
	".vhdx": {},
	".vmdk": {},
	".img":  {},
	".dmg":  {},
	".7z":   {},
}

// LargeFiles returns the two large-file rules.
//
// Both are Careful. A large file is the one category where Devpit genuinely
// cannot tell what it is looking at: a 4 GB file might be a forgotten Windows
// ISO or the only copy of a dataset. Careful means the Recycle Bin and a
// typed word, and the rules are ordered so the more specific one wins.
//
// The rules are not in Project(): walking every file in a projects folder to
// compare sizes is a different job from pruning on a directory name, and the
// Project Junk scan should stay as fast as it is.
func LargeFiles() []scan.Rule {
	return []scan.Rule{
		{
			Name:    "large installer or disk image",
			Kind:    scan.KindLargeFile,
			MinSize: LargeInstallerBytes,
			Match: func(lowerName string) bool {
				_, ok := installerExtensions[strings.ToLower(filepath.Ext(lowerName))]
				return ok
			},
			Tier:        scan.TierCareful,
			RestoreHint: "Download it again from wherever it came from. Check it is not a disk image something still uses.",
			Description: "A downloaded installer, archive or virtual disk image.",
		},
		{
			Name:        "large file",
			Kind:        scan.KindLargeFile,
			MinSize:     LargeFileBytes,
			Tier:        scan.TierCareful,
			RestoreHint: "There is no way to restore this except from your own backup. Look at it before you tick it.",
			Description: "A single file over a gigabyte.",
		},
	}
}
