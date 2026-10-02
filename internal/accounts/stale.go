package accounts

import (
	"fmt"
	"os"
)

// StaleKind says why a rule's folder cannot be found.
type StaleKind string

const (
	// StaleDriveNotConnected: the drive or network share the folder is on is
	// not there right now (a USB drive unplugged, a share offline). The rule
	// may well work again later.
	StaleDriveNotConnected StaleKind = "drive not connected"
	// StaleFolderNotFound: the drive is there but the folder is not; it was
	// moved, renamed or deleted, or a file now has its name.
	StaleFolderNotFound StaleKind = "folder not found"
)

// StaleRule is a rule whose folder is missing. Devpit never removes one by
// itself: "Fix old rules" offers to remove or re-point it.
type StaleRule struct {
	Rule    Rule
	Kind    StaleKind
	Message string
}

// StaleFix is the one-sentence fix every screen and the command line give
// for a stale rule.
const StaleFix = "Open devpit › Accounts › Browse folders › Fix old rules to remove it or point it at the folder's new place."

// StaleRules lists the rules whose folder is missing. stat is os.Stat when
// nil; tests pass a fake.
func StaleRules(s *Store, stat func(string) (os.FileInfo, error)) []StaleRule {
	if stat == nil {
		stat = os.Stat
	}
	var out []StaleRule
	for _, r := range s.Rules {
		fi, err := stat(r.Folder)
		if err == nil && fi.IsDir() {
			continue
		}
		vol := FolderVolume(r.Folder)
		if _, verr := stat(vol); verr != nil {
			what := "drive " + trimRoot(vol)
			if IsNetworkPath(r.Folder) {
				what = "network share " + vol
			}
			out = append(out, StaleRule{Rule: r, Kind: StaleDriveNotConnected, Message: fmt.Sprintf(
				"%s: %s is not connected. The rule stays until you remove it, and works again when the drive is back.", r.Folder, what)})
			continue
		}
		msg := fmt.Sprintf("%s: folder not found. It may have been moved, renamed or deleted.", r.Folder)
		if err == nil {
			msg = fmt.Sprintf("%s: a file has this name now, not a folder.", r.Folder)
		}
		out = append(out, StaleRule{Rule: r, Kind: StaleFolderNotFound, Message: msg})
	}
	return out
}

// trimRoot turns "D:\" into "D:".
func trimRoot(vol string) string {
	if len(vol) == 3 && vol[1] == ':' {
		return vol[:2]
	}
	return vol
}
