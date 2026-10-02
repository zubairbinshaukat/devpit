package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// ClaudeAcc is what claude-acc keeps in %USERPROFILE%\.claude-switch. The
// formats are claude-acc's own (src/config.rs, checked at upstream commit
// 1b87f85, 2026-10-01):
//
//   - config: key=value lines, split at the first '=', both sides trimmed;
//     "default=<name>", where an empty value means ~/.claude. Other keys
//     (resume_hook…) are claude-acc's own settings and are ignored;
//   - links: "<absolute folder>=<account>" lines. claude-acc splits at the
//     first '=', so a folder with '=' in it never worked there; account
//     names cannot contain '=', so Devpit splits at the last one and gets
//     the folder right. "default" means ~/.claude. claude-acc writes
//     one line per folder (the first one wins if a hand edit made two);
//   - accounts\<name>\: a full Claude Code config folder per account. A name
//     uses ASCII letters, digits, '-' and '_'; "default" is reserved;
//   - accounts\<name>\.account-info.json and default.account-info.json:
//     claude-acc's cache of who the account is. Only email, org and plan
//     are read; token_hash and everything else are not;
//   - no comment syntax and no quoting in either file.
//
// claude-acc applies a link by setting $env:CLAUDE_CONFIG_DIR in the
// PowerShell session on every change of folder (a LocationChangedAction
// installed by its profile line); a link to an account whose folder is
// gone falls back to ~/.claude.
type ClaudeAcc struct {
	// Dir is %USERPROFILE%\.claude-switch.
	Dir string
	// Program is bin\claude-acc.exe when it is there.
	Program string
	// Default is claude-acc's default account name; "" means ~/.claude.
	Default string
	// DefaultMissing is set when Default names an account whose folder is
	// gone (claude-acc then uses ~/.claude).
	DefaultMissing bool
	// DefaultEmail is who ~/.claude is, from default.account-info.json.
	DefaultEmail string
	Accounts     []AccAccount
	Links        []AccLink
	// ProfileLines are claude-acc's init lines in PowerShell profiles.
	ProfileLines []ProfileLine
	// SessionVar is CLAUDE_CONFIG_DIR in this environment when it points
	// into claude-acc's accounts: the variable its profile line set.
	SessionVar string
}

// UsableLinks counts the links Devpit can turn into rules.
func (c *ClaudeAcc) UsableLinks() int {
	n := 0
	for _, l := range c.Links {
		if l.Usable() {
			n++
		}
	}
	return n
}

// AccAccount is one claude-acc account.
type AccAccount struct {
	// Name is claude-acc's name for it.
	Name string
	// Dir is its config folder, where it stays.
	Dir string
	// Email, Org and Plan come from its .account-info.json, when claude-acc
	// wrote one.
	Email, Org, Plan string
	// SignedIn: a .credentials.json is there (only its presence is
	// checked).
	SignedIn bool
}

// AccLink is one line of claude-acc's links file.
type AccLink struct {
	// Line is the 1-based line number in the links file.
	Line int
	// Folder is the folder as written; Normalized is Devpit's spelling,
	// "" when it is not a usable absolute folder.
	Folder, Normalized string
	// Account is claude-acc's account name, or "default".
	Account string
	// Problem says why the link is skipped: an unknown account, a folder
	// that is not a path, a duplicate. Empty for a usable link.
	Problem string
	// FolderMissing: the folder is not there now. The link is still
	// imported, and flagged.
	FolderMissing bool
}

// Usable reports whether the link becomes a rule.
func (l AccLink) Usable() bool { return l.Problem == "" }

// accountInfo is the part of claude-acc's .account-info.json Devpit reads.
// token_hash is deliberately not a field.
type accountInfo struct {
	Email string `json:"email"`
	Org   string `json:"org"`
	Plan  string `json:"plan"`
}

func readAccountInfo(path string) accountInfo {
	data, err := readSmall(path, 64<<10)
	if err != nil {
		return accountInfo{}
	}
	var ai accountInfo
	if json.Unmarshal(data, &ai) != nil {
		return accountInfo{}
	}
	ai.Email, ai.Org, ai.Plan = clean(ai.Email), clean(ai.Org), clean(ai.Plan)
	return ai
}

// clean drops a value that looks like a secret or is not one short line.
func clean(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 || strings.ContainsAny(s, "\r\n") || accounts.LooksSecret(s) {
		return ""
	}
	return s
}

// readClaudeAcc reads ~/.claude-switch. It returns nil when there is no
// such folder.
func readClaudeAcc(d Deps) (*ClaudeAcc, []string) {
	base := filepath.Join(d.Home, ".claude-switch")
	if !isDir(base) {
		return nil, nil
	}
	c := &ClaudeAcc{Dir: base}
	var probs []string
	if p := filepath.Join(base, "bin", "claude-acc.exe"); exists(p) {
		c.Program = p
	}

	// Accounts: one folder each.
	accDir := filepath.Join(base, "accounts")
	entries, err := os.ReadDir(accDir)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		probs = append(probs, fmt.Sprintf("claude-acc's accounts folder (%s) could not be read.", accDir))
	}
	known := map[string]bool{}
	for _, e := range entries {
		dir := filepath.Join(accDir, e.Name())
		if !plainDir(dir) {
			continue
		}
		ai := readAccountInfo(filepath.Join(dir, ".account-info.json"))
		c.Accounts = append(c.Accounts, AccAccount{
			Name: e.Name(), Dir: dir, Email: ai.Email, Org: ai.Org, Plan: ai.Plan,
			SignedIn: exists(filepath.Join(dir, ".credentials.json")),
		})
		known[e.Name()] = true
	}
	c.DefaultEmail = readAccountInfo(filepath.Join(base, "default.account-info.json")).Email

	// config: default=<name>.
	if data, err := readSmall(filepath.Join(base, "config"), 64<<10); err == nil {
		c.Default = parseConfigDefault(string(data))
		if c.Default != "" && c.Default != accounts.DefaultName && !known[c.Default] {
			c.DefaultMissing = true
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		probs = append(probs, "claude-acc's config file could not be read, so its default account is unknown.")
	}

	// links.
	if data, err := readSmall(filepath.Join(base, "links"), 1<<20); err == nil {
		c.Links = parseLinks(string(data), known)
	} else if !errors.Is(err, os.ErrNotExist) {
		probs = append(probs, "claude-acc's links file could not be read, so no folder links were found.")
	}

	if v := d.getenv("CLAUDE_CONFIG_DIR"); v != "" && accounts.FolderContains(accDir, v) {
		c.SessionVar = v
	}
	return c, probs
}

// parseConfigDefault returns the value of the first "default" key, as
// claude-acc's get_setting does ("" for an empty value).
func parseConfigDefault(content string) string {
	for _, line := range strings.Split(content, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == "default" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// parseLinks reads the links file. known is the set of claude-acc account
// names whose folder exists.
func parseLinks(content string, known map[string]bool) []AccLink {
	var out []AccLink
	firstFor := map[string]int{} // folder key → line of the link that wins
	for i, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}
		at := strings.LastIndexByte(line, '=')
		if at < 0 {
			continue // claude-acc skips a line without '=' too
		}
		l := AccLink{Line: i + 1, Folder: strings.TrimSpace(line[:at]), Account: strings.TrimSpace(line[at+1:])}
		switch {
		case l.Folder == "" || l.Account == "":
			continue // claude-acc skips these
		case strings.HasPrefix(l.Folder, "#"):
			l.Problem = "it is not a folder path"
		}
		if l.Problem == "" {
			norm, err := accounts.NormalizeFolder(l.Folder, "")
			if err != nil {
				l.Problem = "it is not a full folder path"
			} else {
				l.Normalized = norm
			}
		}
		if l.Problem == "" && l.Account != accounts.DefaultName && !known[l.Account] {
			l.Problem = fmt.Sprintf("claude-acc has no account called %q", l.Account)
		}
		if l.Problem == "" {
			key := strings.ToUpper(l.Normalized)
			if first, dup := firstFor[key]; dup {
				l.Problem = fmt.Sprintf("line %d already links this folder, and that one wins", first)
			} else {
				firstFor[key] = l.Line
			}
		}
		if l.Problem == "" && !isDir(l.Normalized) {
			l.FolderMissing = true
		}
		out = append(out, l)
	}
	return out
}
