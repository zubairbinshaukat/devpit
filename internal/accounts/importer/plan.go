package importer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// Options says what to bring over.
type Options struct {
	// ClaudeAcc imports claude-acc's accounts, links and default.
	ClaudeAcc bool
	// ConfigDirs are the other Claude Code folders to add, by Dir (from
	// Found.ConfigDirs).
	ConfigDirs []string
	// GitHub adds gh's detected accounts.
	GitHub bool
	// Now stamps the new accounts; time.Now when zero.
	Now time.Time
}

// PlannedRule is one folder rule an import adds.
type PlannedRule struct {
	Folder  string
	Tool    accounts.Tool
	Account string
	// FolderMissing: the folder is not there now; the rule is still made
	// (claude-acc had it) and flagged.
	FolderMissing bool
}

// Plan is the preview of an import: what it adds, in plain words, and what
// it leaves out and why. Nothing has changed yet. Apply it with
// [ApplyImport].
type Plan struct {
	// Accounts are the accounts it adds. Their Dir is where they already
	// are; nothing is copied or moved.
	Accounts []accounts.Account
	Rules    []PlannedRule
	// Everywhere is Claude Code's new "everywhere" account, or "" for no
	// change.
	Everywhere string
	// DefaultEmail is who ~/.claude is, recorded when the store has none.
	DefaultEmail string
	// Renamed maps a name from elsewhere to the Devpit name it gets.
	Renamed map[string]string
	// Sentences say what will happen; Notes say what is flagged or left
	// out, and why.
	Sentences []string
	Notes     []string
	// NoChange is set when everything is already in Devpit (running the
	// import twice changes nothing).
	NoChange bool
	// Summary is the journal's one-line description.
	Summary string
	// BeforeHash is the store the plan was built from; applying it to any
	// other store fails with accounts.ErrStalePreview.
	BeforeHash string
}

// Lines is the whole preview as text.
func (p Plan) Lines() []string {
	out := append([]string(nil), p.Sentences...)
	if len(p.Notes) > 0 {
		out = append(out, "")
		out = append(out, p.Notes...)
	}
	return out
}

// namer hands out Devpit names: unique per tool ignoring case, never
// "default".
type namer struct {
	s     *accounts.Store
	taken map[accounts.Tool][]string
}

func (n *namer) name(t accounts.Tool, want string) string {
	base := sanitize(want)
	if base == "" {
		base = "imported"
	}
	used := func(c string) bool {
		if accounts.IsDefault(c) {
			return true
		}
		if _, ok := n.s.Account(t, c); ok {
			return true
		}
		for _, x := range n.taken[t] {
			if strings.EqualFold(x, c) {
				return true
			}
		}
		return false
	}
	cand := base
	for i := 2; used(cand); i++ {
		suffix := "-" + strconv.Itoa(i)
		b := base
		if len(b)+len(suffix) > accounts.MaxNameLen {
			b = strings.TrimRight(b[:accounts.MaxNameLen-len(suffix)], "-")
		}
		cand = b + suffix
	}
	n.taken[t] = append(n.taken[t], cand)
	return cand
}

// storable drops display fields the store would refuse rather than
// failing the import.
func storable(a accounts.Account) accounts.Account {
	try := func(x accounts.Account) bool { return accounts.NewStore().AddAccount(x) == nil }
	if try(a) {
		return a
	}
	a.Org = ""
	if try(a) {
		return a
	}
	a.Label = ""
	if try(a) {
		return a
	}
	a.Email = ""
	return a
}

// claudeByDir finds a Claude Code account already in s with folder dir.
func claudeByDir(s *accounts.Store, dir string) (accounts.Account, bool) {
	for _, a := range s.AccountsFor(accounts.ToolClaude) {
		if a.Dir != "" && accounts.SameFolder(a.Dir, dir) {
			return a, true
		}
	}
	return accounts.Account{}, false
}

// PlanImport works out what importing f with opt would change in s, and
// says it in plain words. It changes nothing. Running it again after the
// import finds nothing to do.
func PlanImport(s *accounts.Store, f Found, opt Options) (Plan, error) {
	if s == nil {
		s = accounts.NewStore()
	}
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	p := Plan{BeforeHash: accounts.StoreHash(s), Renamed: map[string]string{}}
	nm := &namer{s: s, taken: map[accounts.Tool][]string{}}
	work := s.Clone() // what the store will look like, to check each step

	add := func(a accounts.Account) error {
		a = storable(a)
		if err := work.AddAccount(a); err != nil {
			return err
		}
		p.Accounts = append(p.Accounts, a)
		return nil
	}

	if opt.ClaudeAcc && f.ClaudeAcc != nil {
		c := f.ClaudeAcc
		accName := map[string]string{} // claude-acc name → Devpit name
		for _, acc := range c.Accounts {
			if existing, ok := claudeByDir(s, acc.Dir); ok {
				accName[acc.Name] = existing.Name
				continue
			}
			name := nm.name(accounts.ToolClaude, acc.Name)
			if name != acc.Name {
				p.Renamed[acc.Name] = name
				p.Notes = append(p.Notes, fmt.Sprintf("claude-acc's account %q is called %q in Devpit (names use letters, numbers and dashes, and are unique ignoring case).", acc.Name, name))
			}
			if err := add(accounts.Account{
				Tool: accounts.ToolClaude, Name: name, Email: acc.Email, Org: acc.Org, Dir: acc.Dir,
				Added: now.UTC(), ImportedFrom: FromClaudeAcc,
			}); err != nil {
				return Plan{}, fmt.Errorf("claude-acc's account %q: %w", acc.Name, err)
			}
			accName[acc.Name] = name
			if !acc.SignedIn {
				p.Notes = append(p.Notes, fmt.Sprintf("%s has no sign-in in its folder yet; sign in to it from Accounts.", name))
			}
		}
		devpitName := func(acc string) (string, bool) {
			if acc == accounts.DefaultName {
				return accounts.DefaultName, true
			}
			n, ok := accName[acc]
			return n, ok
		}

		for _, l := range c.Links {
			if !l.Usable() {
				p.Notes = append(p.Notes, fmt.Sprintf("Skipped claude-acc's link on line %d (%s → %s): %s.", l.Line, l.Folder, l.Account, l.Problem))
				continue
			}
			name, ok := devpitName(l.Account)
			if !ok {
				p.Notes = append(p.Notes, fmt.Sprintf("Skipped claude-acc's link on line %d (%s): claude-acc has no account called %q.", l.Line, l.Folder, l.Account))
				continue
			}
			if r, has := work.Rule(l.Normalized); has {
				if cur, set := r.Accounts[accounts.ToolClaude]; set {
					if !strings.EqualFold(cur, name) {
						p.Notes = append(p.Notes, fmt.Sprintf("Kept Devpit's own rule for %s (Claude Code uses %s there); claude-acc said %s.", l.Normalized, cur, name))
					}
					continue
				}
			}
			if err := work.SetRule(l.Normalized, accounts.ToolClaude, name); err != nil {
				p.Notes = append(p.Notes, fmt.Sprintf("Skipped claude-acc's link on line %d (%s): %v.", l.Line, l.Folder, err))
				continue
			}
			p.Rules = append(p.Rules, PlannedRule{Folder: l.Normalized, Tool: accounts.ToolClaude, Account: name, FolderMissing: l.FolderMissing})
			if l.FolderMissing {
				p.Notes = append(p.Notes, fmt.Sprintf("%s is not there now. Its rule is kept and shown as \"folder not found\".", l.Normalized))
			}
		}

		if c.Default != "" && c.Default != accounts.DefaultName {
			name, ok := devpitName(c.Default)
			cur := work.Everywhere[accounts.ToolClaude]
			switch {
			case !ok:
				p.Notes = append(p.Notes, fmt.Sprintf("claude-acc's default account %q has no folder, so claude-acc itself uses ~/.claude; Devpit keeps the default account everywhere.", c.Default))
			case cur == "" || accounts.IsDefault(cur):
				if err := work.SetEverywhere(accounts.ToolClaude, name); err != nil {
					return Plan{}, err
				}
				p.Everywhere = name
			case !strings.EqualFold(cur, name):
				p.Notes = append(p.Notes, fmt.Sprintf("Kept Devpit's own choice for everywhere (%s); claude-acc's default was %s.", cur, name))
			}
		}
		if c.DefaultEmail != "" && s.DefaultEmail[accounts.ToolClaude] == "" {
			if work.SetDefaultEmail(accounts.ToolClaude, c.DefaultEmail) == nil {
				p.DefaultEmail = c.DefaultEmail
			}
		}
	}

	for _, want := range opt.ConfigDirs {
		var cd *ConfigDir
		for i := range f.ConfigDirs {
			if accounts.SameFolder(f.ConfigDirs[i].Dir, want) {
				cd = &f.ConfigDirs[i]
			}
		}
		if cd == nil {
			return Plan{}, fmt.Errorf("%s is not one of the Claude Code folders found", want)
		}
		if _, ok := claudeByDir(work, cd.Dir); ok {
			continue
		}
		name := nm.name(accounts.ToolClaude, cd.Suggested)
		if err := add(accounts.Account{Tool: accounts.ToolClaude, Name: name, Dir: cd.Dir, Added: now.UTC(), ImportedFrom: FromDetected}); err != nil {
			return Plan{}, fmt.Errorf("%s: %w", cd.Dir, err)
		}
	}

	if opt.GitHub {
		for _, gh := range f.GitHub {
			dup := false
			for _, a := range work.AccountsFor(accounts.ToolGitHub) {
				if gh.Label != "" && strings.EqualFold(a.Label, gh.Label) {
					dup = true
				}
			}
			if dup {
				continue
			}
			want := gh.Name
			if want == "" {
				want = gh.Label
			}
			name := nm.name(accounts.ToolGitHub, want)
			a := gh
			a.Tool, a.Name, a.Dir, a.ImportedFrom = accounts.ToolGitHub, name, "", FromDetected
			if a.Added.IsZero() {
				a.Added = now.UTC()
			}
			if err := add(a); err != nil {
				return Plan{}, fmt.Errorf("gh account %s: %w", gh.Label, err)
			}
		}
	}

	p.sentences()
	return p, nil
}

func (p *Plan) sentences() {
	if len(p.Accounts) == 0 && len(p.Rules) == 0 && p.Everywhere == "" && p.DefaultEmail == "" {
		p.NoChange = true
		p.Sentences = []string{"Everything found is already in Devpit. Nothing will change."}
		p.Summary = "Import found nothing new"
		return
	}
	for _, a := range p.Accounts {
		p.Sentences = append(p.Sentences, fmt.Sprintf("Add the %s account %s, using its folder where it is: %s.", a.Tool.DisplayName(), a.Display(), firstNonEmpty(a.Dir, "no folder")))
	}
	if n := len(p.Rules); n > 0 {
		p.Sentences = append(p.Sentences, plural(n, "folder rule", "folder rules")+":")
		for _, r := range p.Rules {
			line := fmt.Sprintf("  %s   %s uses %s", r.Folder, r.Tool.DisplayName(), r.Account)
			if r.FolderMissing {
				line += " (folder not found)"
			}
			p.Sentences = append(p.Sentences, line)
		}
	}
	if p.Everywhere != "" {
		p.Sentences = append(p.Sentences, fmt.Sprintf("Everywhere else, Claude Code will use %s.", p.Everywhere))
	}
	if p.DefaultEmail != "" {
		p.Sentences = append(p.Sentences, fmt.Sprintf("Record that the default Claude Code account is %s.", p.DefaultEmail))
	}
	p.Sentences = append(p.Sentences, "No folder is copied, moved or deleted, so nobody signs in again. This is one change you can undo.")
	p.Summary = fmt.Sprintf("Imported %s and %s", plural(len(p.Accounts), "account", "accounts"), plural(len(p.Rules), "folder rule", "folder rules"))
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// ApplyImport makes the change p describes as one journalled change (one
// Undo takes it all back). It fails with accounts.ErrStalePreview when the
// store changed since the plan was made, and changes nothing.
func ApplyImport(eng *accounts.Engine, p Plan) (accounts.Entry, error) {
	if p.NoChange {
		return accounts.Entry{}, errors.New("this import would change nothing")
	}
	return eng.Edit(p.Summary, func(s *accounts.Store) error {
		if accounts.StoreHash(s) != p.BeforeHash {
			return accounts.ErrStalePreview
		}
		for _, a := range p.Accounts {
			if err := s.AddAccount(a); err != nil {
				return err
			}
		}
		for _, r := range p.Rules {
			if err := s.SetRule(r.Folder, r.Tool, r.Account); err != nil {
				return err
			}
		}
		if p.Everywhere != "" {
			if err := s.SetEverywhere(accounts.ToolClaude, p.Everywhere); err != nil {
				return err
			}
		}
		if p.DefaultEmail != "" {
			if err := s.SetDefaultEmail(accounts.ToolClaude, p.DefaultEmail); err != nil {
				return err
			}
		}
		return nil
	})
}
