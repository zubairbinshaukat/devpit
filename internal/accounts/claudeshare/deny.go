package claudeshare

import (
	"fmt"
	"path/filepath"
	"strings"
)

// The deny-list makes cloning a login structurally impossible: every copy,
// link and move goes through [checkDenied] first, the tree copier skips
// these names at every depth, and the JSON merge touches nothing in a
// .claude.json but its mcpServers entries.

// deniedAnywhere reports whether a file or folder name is a login or account
// file, wherever it sits: .credentials.json and .claude.json (with every
// backup or temporary sibling such as .claude.json.backup).
func deniedAnywhere(name string) bool {
	low := strings.ToLower(strings.TrimRight(name, ". "))
	return low == ".credentials.json" || strings.HasPrefix(low, ".claude.json") ||
		strings.HasPrefix(low, ".credentials.json")
}

// deniedTop reports whether a name directly inside an account folder is
// per-account state that must never be shared: the login files above, their
// *.lock siblings, .oauth_refresh.lock, Devpit's own sharing record, and the
// backups, daemon, sessions, ide, statsig and shell-snapshots folders.
func deniedTop(name string) bool {
	if deniedAnywhere(name) {
		return true
	}
	low := strings.ToLower(strings.TrimRight(name, ". "))
	if strings.HasSuffix(low, ".lock") || low == stateFileName {
		return true
	}
	switch low {
	case "backups", "daemon", "sessions", "ide", "statsig", "shell-snapshots":
		return true
	}
	return false
}

// DeniedNames lists the deny-list for the docs and the item list's locked
// rows.
func DeniedNames() []string {
	return []string{
		".credentials.json", ".claude.json (and .claude.json.*)", "*.lock", ".oauth_refresh.lock",
		`backups\`, `daemon\`, `sessions\`, `ide\`, `statsig\`, `shell-snapshots\`,
	}
}

// checkDenied refuses a path that is, or is inside, a denied name of either
// account folder, or whose own name is a login file wherever it is.
func (r Roots) checkDenied(p string) error {
	if p == "" {
		return nil
	}
	if deniedAnywhere(filepath.Base(p)) {
		return fmt.Errorf("refusing to touch %s: Devpit never copies, links or moves a login or account file", p)
	}
	for _, root := range []string{r.DefaultHome, r.Target} {
		rel, ok := relInside(root, p)
		if !ok || rel == "" {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if deniedTop(parts[0]) {
			return fmt.Errorf("refusing to touch %s: it is per-account state that is never shared", p)
		}
		for _, part := range parts {
			if deniedAnywhere(part) {
				return fmt.Errorf("refusing to touch %s: Devpit never copies, links or moves a login or account file", p)
			}
		}
	}
	return nil
}

// relInside returns p relative to root when p is inside root ("" for root
// itself).
func relInside(root, p string) (string, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return rel, true
}
