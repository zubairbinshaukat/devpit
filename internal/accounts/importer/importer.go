// Package importer finds accounts already set up on this PC and turns them
// into Devpit accounts and rules, without moving anything:
//
//   - claude-acc (github.com/Nemo-Illusionist/claude-code-account-switcher):
//     its accounts in %USERPROFILE%\.claude-switch\accounts\<name>, its
//     folder links (links file) and its default account (config file);
//   - other Claude Code config folders: CLAUDE_CONFIG_DIR in this
//     environment and ~/.claude-* folders that hold a Claude sign-in;
//   - several accounts in gh, through the GitHub adapter's account list.
//
// Imported accounts stay where they are: the store's Dir points at them, so
// nobody signs in again. Every change goes through the Engine as one
// journalled, undoable change built from a preview ([PlanImport],
// [ApplyImport]). Identity comes from non-secret metadata only
// (claude-acc's .account-info.json email/org/plan); no tool is run for
// Claude Code, because a short-lived `claude auth status` can sign an idle
// account out (Claude Code issue #95822).
//
// What this package never does: open a .credentials.json or .claude.json
// (only their presence is checked), keep a line of a PowerShell profile
// other than claude-acc's own init line, edit a profile, or delete
// anything. Engine only: no Bubble Tea.
package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
)

// FromClaudeAcc is the ImportedFrom tag of accounts imported from claude-acc.
const FromClaudeAcc = "claude-acc"

// FromDetected is the ImportedFrom tag of other found accounts.
const FromDetected = "detected"

// Deps is what Detect reads. Nothing in it is global; tests build their own.
type Deps struct {
	// Home is the person's home folder.
	Home string
	// Getenv reads the environment; os.Getenv when nil.
	Getenv func(string) string
	// GitHub, when set, lists gh's accounts (the GitHub adapter). Its
	// Accounts may run gh; that is the only tool Detect can run.
	GitHub adapters.Adapter
	// ProfileFiles are the PowerShell profiles searched for claude-acc's
	// line; the usual ones (see [DefaultProfileFiles]) when nil.
	ProfileFiles []string
}

// DefaultDeps builds Deps from the adapters' Deps, with the real GitHub
// adapter.
func DefaultDeps(d adapters.Deps) Deps {
	gh, _ := adapters.For(d, accounts.ToolGitHub)
	return Deps{Home: d.Home, Getenv: d.Getenv, GitHub: gh}
}

func (d Deps) getenv(k string) string {
	if d.Getenv != nil {
		return d.Getenv(k)
	}
	return os.Getenv(k)
}

// Found is everything Detect found.
type Found struct {
	// ClaudeAcc is nil when %USERPROFILE%\.claude-switch is not there.
	ClaudeAcc *ClaudeAcc
	// ConfigDirs are other Claude Code config folders not yet in the store.
	ConfigDirs []ConfigDir
	// GitHub are gh accounts the store does not have yet.
	GitHub []accounts.Account
	// Problems are sentences about what could not be read.
	Problems []string
}

// Empty reports whether nothing was found.
func (f Found) Empty() bool {
	return (f.ClaudeAcc == nil || (len(f.ClaudeAcc.Accounts) == 0 && len(f.ClaudeAcc.Links) == 0)) &&
		len(f.ConfigDirs) == 0 && len(f.GitHub) == 0
}

// Summary is the one sentence for the first-open card: "Found 1 Claude
// account and 6 folder links from claude-acc."
func (f Found) Summary() string {
	var parts []string
	if c := f.ClaudeAcc; c != nil && (len(c.Accounts) > 0 || c.UsableLinks() > 0) {
		parts = append(parts, fmt.Sprintf("%s and %s from claude-acc",
			plural(len(c.Accounts), "Claude account", "Claude accounts"), plural(c.UsableLinks(), "folder link", "folder links")))
	}
	if n := len(f.ConfigDirs); n > 0 {
		parts = append(parts, plural(n, "other Claude Code folder", "other Claude Code folders"))
	}
	if n := len(f.GitHub); n > 0 {
		parts = append(parts, plural(n, "more GitHub account in gh", "more GitHub accounts in gh"))
	}
	if len(parts) == 0 {
		return "Found no accounts to bring over."
	}
	return "Found " + joinAnd(parts) + "."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func joinAnd(parts []string) string {
	switch len(parts) {
	case 1:
		return parts[0]
	case 2:
		return parts[0] + ", plus " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", plus " + parts[len(parts)-1]
}

// ConfigDir is a Claude Code config folder found outside claude-acc.
type ConfigDir struct {
	Dir string
	// FromEnv: found through CLAUDE_CONFIG_DIR in this environment.
	FromEnv bool
	// SignedIn: a .credentials.json is there (only its presence is
	// checked).
	SignedIn bool
	// Suggested is a Devpit name for it.
	Suggested string
}

// Detect looks for accounts to bring over. It reads claude-acc's own files
// and folder names, checks login files by name only, and asks the GitHub
// adapter (if any) for gh's accounts. It never changes anything. s is the
// current store, so folders and gh accounts Devpit already has are left
// out; nil is an empty store.
func Detect(ctx context.Context, d Deps, s *accounts.Store) (Found, error) {
	if s == nil {
		s = accounts.NewStore()
	}
	if d.Home == "" {
		return Found{}, errors.New("no home folder to look in")
	}
	var f Found
	acc, probs := readClaudeAcc(d)
	f.ClaudeAcc = acc
	f.Problems = append(f.Problems, probs...)
	if acc != nil {
		acc.ProfileLines, probs = findProfileLines(d)
		f.Problems = append(f.Problems, probs...)
	}
	f.ConfigDirs = findConfigDirs(d, s, acc)

	if d.GitHub != nil {
		list, err := d.GitHub.Accounts(ctx, s)
		if err != nil {
			f.Problems = append(f.Problems, "gh's accounts could not be listed: "+accounts.Scrub(err.Error()))
		}
		for _, a := range list {
			if a.ImportedFrom == FromDetected && !a.IsDefault() {
				f.GitHub = append(f.GitHub, a)
			}
		}
	}
	return f, nil
}

// findConfigDirs finds Claude Code config folders other than ~/.claude and
// claude-acc's: CLAUDE_CONFIG_DIR, then ~/.claude-* folders with a
// .credentials.json (by name; never opened). *.lock entries are skipped.
func findConfigDirs(d Deps, s *accounts.Store, acc *ClaudeAcc) []ConfigDir {
	var out []ConfigDir
	seen := func(dir string) bool {
		if accounts.SameFolder(dir, filepath.Join(d.Home, ".claude")) {
			return true
		}
		if acc != nil && accounts.FolderContains(acc.Dir, dir) {
			return true
		}
		for _, a := range s.AccountsFor(accounts.ToolClaude) {
			if a.Dir != "" && accounts.SameFolder(a.Dir, dir) {
				return true
			}
		}
		for _, c := range out {
			if accounts.SameFolder(c.Dir, dir) {
				return true
			}
		}
		return false
	}
	if env := d.getenv("CLAUDE_CONFIG_DIR"); env != "" && filepath.IsAbs(env) && isDir(env) && !seen(env) {
		out = append(out, ConfigDir{Dir: filepath.Clean(env), FromEnv: true, SignedIn: exists(filepath.Join(env, ".credentials.json")), Suggested: suggestFromFolder(env)})
	}
	entries, err := os.ReadDir(d.Home)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		low := strings.ToLower(name)
		if !strings.HasPrefix(low, ".claude-") || strings.HasSuffix(low, ".lock") || low == ".claude-switch" {
			continue
		}
		dir := filepath.Join(d.Home, name)
		if !plainDir(dir) || !exists(filepath.Join(dir, ".credentials.json")) || seen(dir) {
			continue
		}
		out = append(out, ConfigDir{Dir: dir, SignedIn: true, Suggested: suggestFromFolder(dir)})
	}
	return out
}

// suggestFromFolder names a config folder: ".claude-work" → "work".
func suggestFromFolder(dir string) string {
	base := filepath.Base(dir)
	low := strings.ToLower(base)
	for _, p := range []string{".claude-", "claude-", "."} {
		if strings.HasPrefix(low, p) {
			base = base[len(p):]
			break
		}
	}
	if n := sanitize(base); n != "" {
		return n
	}
	return "claude"
}

// sanitize turns a name from elsewhere into a Devpit name: letters and
// digits kept (and their case), anything else one dash, no dash at either
// end, at most accounts.MaxNameLen. "" if nothing is left.
func sanitize(s string) string {
	var b strings.Builder
	dash := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteByte(c)
			continue
		}
		dash = true
	}
	out := b.String()
	if len(out) > accounts.MaxNameLen {
		out = strings.TrimRight(out[:accounts.MaxNameLen], "-")
	}
	return out
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// plainDir is a real folder, not a link to one.
func plainDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir() && fi.Mode()&os.ModeSymlink == 0
}

// readSmall reads a file of at most limit bytes.
func readSmall(path string, limit int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%s is a folder", path)
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("%s is larger than expected", path)
	}
	return os.ReadFile(path) // #nosec G304 -- claude-acc's own non-secret files and PowerShell profiles
}
