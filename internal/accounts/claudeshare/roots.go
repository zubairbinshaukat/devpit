package claudeshare

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// Roots says where everything is. Every path the package touches is derived
// from these, so tests point them at fixtures.
type Roots struct {
	// DefaultHome is the default account's config folder (~/.claude), the
	// shared home. Nothing in it ever moves.
	DefaultHome string
	// DefaultClaudeJSON is the default account's .claude.json, which lives
	// outside DefaultHome (~/.claude.json). Only its mcpServers are read.
	// Empty means the .claude.json beside DefaultHome.
	DefaultClaudeJSON string
	// Target is the account being set up: its CLAUDE_CONFIG_DIR folder.
	Target string
	// TargetName is that account's name ("work"), used in names such as
	// skills\foo.from-work.
	TargetName string
	// Env looks up environment variables (OneDrive folders). Nil means
	// none are set.
	Env func(string) (string, bool)
	// Now is time.Now unless a test sets it. It dates backup folders.
	Now func() time.Time
	// Lock is the accounts lock file. EnsureSkillLinks stays out of the way
	// while another Devpit holds it. Empty skips the check.
	Lock string

	// probe replaces the link check in tests.
	probe func(src, dst string) (bool, string)
}

// SourceName is how the default account is named in file names
// (<name>.from-default).
const SourceName = accounts.DefaultName

// RootsFor returns the roots for bringing the default account's setup (in
// the profile folder home) to acct.
func RootsFor(home string, acct accounts.Account) Roots {
	return Roots{
		DefaultHome:       filepath.Join(home, ".claude"),
		DefaultClaudeJSON: filepath.Join(home, ".claude.json"),
		Target:            acct.Dir,
		TargetName:        acct.Name,
		Env:               os.LookupEnv,
	}
}

// Errors returned for roots that cannot be worked on.
var (
	// ErrTargetMissing: the account's folder does not exist.
	ErrTargetMissing = errors.New("the account's folder does not exist")
	// ErrNotClaudeFolder: the folder does not look like a Claude Code
	// account folder.
	ErrNotClaudeFolder = errors.New("the folder does not look like a Claude Code account folder")
	// ErrSameFolder: the account is the default account, or one folder is
	// inside the other.
	ErrSameFolder = errors.New("the account folder and the default account's folder overlap")
)

// claudeMarkers are names that only a Claude Code config folder holds.
func claudeMarkers() []string {
	return []string{
		".claude.json", ".credentials.json", "settings.json", "settings.local.json",
		"CLAUDE.md", "projects", "skills", "agents", "commands", "plugins",
		"statsig", "todos", "shell-snapshots", "history.jsonl", "ide", "sessions",
		stateFileName,
	}
}

// check cleans the roots and refuses ones Devpit must not work on.
func (r Roots) check() (Roots, error) {
	if r.DefaultHome == "" || r.Target == "" {
		return r, errors.New("both the default account's folder and the account's folder are needed")
	}
	if !filepath.IsAbs(r.DefaultHome) || !filepath.IsAbs(r.Target) {
		return r, fmt.Errorf("folders must be full paths: %s, %s", r.DefaultHome, r.Target)
	}
	r.DefaultHome = filepath.Clean(r.DefaultHome)
	r.Target = filepath.Clean(r.Target)
	if r.DefaultClaudeJSON == "" {
		r.DefaultClaudeJSON = filepath.Join(filepath.Dir(r.DefaultHome), ".claude.json")
	}
	r.DefaultClaudeJSON = filepath.Clean(r.DefaultClaudeJSON)
	r.TargetName = suffixName(r.TargetName)
	if r.Now == nil {
		r.Now = time.Now
	}
	if accounts.IsNetworkPath(r.Target) {
		return r, fmt.Errorf("%s is a network path; Devpit only sets up account folders on this PC", r.Target)
	}
	if accounts.IsDriveRoot(r.Target) {
		return r, fmt.Errorf("%s is a drive root, not an account folder", r.Target)
	}
	if overlaps(r.DefaultHome, r.Target) {
		return r, fmt.Errorf("%s and %s: %w", r.Target, r.DefaultHome, ErrSameFolder)
	}
	if rd, err := accounts.RealPath(r.DefaultHome); err == nil {
		if rt, err := accounts.RealPath(r.Target); err == nil && overlaps(rd, rt) {
			return r, fmt.Errorf("%s and %s lead to the same place: %w", r.Target, r.DefaultHome, ErrSameFolder)
		}
	}
	fi, err := os.Lstat(fsPath(r.Target))
	if errors.Is(err, os.ErrNotExist) {
		return r, fmt.Errorf("%s: %w", r.Target, ErrTargetMissing)
	}
	if err != nil {
		return r, fmt.Errorf("reading %s: %w", r.Target, err)
	}
	if isReparse(fi) {
		return r, fmt.Errorf("%s is itself a link; Devpit works only on a real account folder", r.Target)
	}
	if !fi.IsDir() {
		return r, fmt.Errorf("%s is a file, not a folder: %w", r.Target, ErrNotClaudeFolder)
	}
	names, err := os.ReadDir(fsPath(r.Target))
	if err != nil {
		return r, fmt.Errorf("reading %s: %w", r.Target, err)
	}
	if len(names) > 0 && !hasMarker(names) {
		return r, fmt.Errorf("%s: %w (it has none of settings.json, .claude.json, skills, projects...)", r.Target, ErrNotClaudeFolder)
	}
	return r, nil
}

func hasMarker(names []os.DirEntry) bool {
	for _, n := range names {
		for _, m := range claudeMarkers() {
			if strings.EqualFold(n.Name(), m) {
				return true
			}
		}
	}
	return false
}

// overlaps reports whether a and b are the same folder or one holds the
// other.
func overlaps(a, b string) bool {
	if _, ok := relInside(a, b); ok {
		return true
	}
	if _, ok := relInside(b, a); ok {
		return true
	}
	return accounts.FolderContains(a, b) || accounts.FolderContains(b, a)
}

// suffixName makes an account name safe inside a file name.
func suffixName(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "account"
	}
	return s
}

// env reads an environment variable through r.Env.
func (r Roots) env(key string) string {
	if r.Env == nil {
		return ""
	}
	v, _ := r.Env(key)
	return strings.TrimSpace(v)
}

// TargetClaudeJSON is the account's own .claude.json, inside its folder.
func (r Roots) TargetClaudeJSON() string { return filepath.Join(r.Target, ".claude.json") }

func (r Roots) srcPath(rel ...string) string {
	return filepath.Join(append([]string{r.DefaultHome}, rel...)...)
}

func (r Roots) dstPath(rel ...string) string {
	return filepath.Join(append([]string{r.Target}, rel...)...)
}

// inSource reports whether p is strictly inside the default home.
func (r Roots) inSource(p string) bool { return strictlyInside(r.DefaultHome, p) }

// inTarget reports whether p is strictly inside the account folder.
func (r Roots) inTarget(p string) bool { return strictlyInside(r.Target, p) }

func strictlyInside(root, p string) bool {
	if p == "" {
		return false
	}
	rel, ok := relInside(root, p)
	return ok && rel != ""
}

// samePlace reports whether two paths name the same file or folder.
func samePlace(a, b string) bool {
	return accounts.SameFolder(a, b) || strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
