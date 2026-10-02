package accounts

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ScopeKind is where a change applies.
type ScopeKind string

const (
	// ScopeFolder: a folder and every folder inside it.
	ScopeFolder ScopeKind = "folder"
	// ScopeEverywhere: wherever no folder rule says otherwise.
	ScopeEverywhere ScopeKind = "everywhere"
	// ScopeOnce: one command, nothing saved.
	ScopeOnce ScopeKind = "once"
)

// Scope is where a change applies. Folder is set for ScopeFolder only.
type Scope struct {
	Kind   ScopeKind
	Folder string
}

// FolderScope, EverywhereScope and OnceScope build the three scopes.
func FolderScope(folder string) Scope { return Scope{Kind: ScopeFolder, Folder: folder} }

// EverywhereScope is the "everywhere" scope.
func EverywhereScope() Scope { return Scope{Kind: ScopeEverywhere} }

// OnceScope is the "just this once" scope.
func OnceScope() Scope { return Scope{Kind: ScopeOnce} }

// Change is one thing a person asked for: tool uses account in scope. With
// Remove set (folder scope only), the folder's own rule for tool is removed
// instead, and Account is ignored.
type Change struct {
	Tool    Tool
	Account string
	Scope   Scope
	Remove  bool
}

// ErrStalePreview is returned when the store changed between the preview
// and the apply: the person agreed to something that is no longer what
// would happen, so they must see a new preview.
var ErrStalePreview = errors.New("the accounts changed since this preview was shown; look at the new preview before applying")

// normalize checks c against s and returns it with the folder normalized and
// the account name spelled as stored.
func (c Change) normalize(s *Store) (Change, error) {
	if !c.Tool.Known() {
		return c, fmt.Errorf("unknown tool %q", c.Tool)
	}
	switch c.Scope.Kind {
	case ScopeFolder:
		norm, err := NormalizeFolder(c.Scope.Folder, "")
		if err != nil {
			return c, err
		}
		// A rule is kept under the folder's long name: a folder typed or
		// reached through an 8.3 short name (C:\Users\RUNNER~1) is the same
		// folder, and the tools match their long names.
		c.Scope.Folder = LongPath(norm)
	case ScopeEverywhere, ScopeOnce:
		c.Scope.Folder = ""
		if c.Remove {
			return c, errors.New("only a folder rule can be removed")
		}
	default:
		return c, fmt.Errorf("unknown scope %q", c.Scope.Kind)
	}
	if c.Remove {
		c.Account = ""
		return c, nil
	}
	a, err := s.FindAccount(c.Tool, c.Account)
	if err != nil {
		return c, err
	}
	c.Account = a.Name
	return c, nil
}

// ApplyTo returns a copy of s with the change made. A once change returns
// an unchanged copy: it is never saved.
func (c Change) ApplyTo(s *Store) (*Store, error) {
	c, err := c.normalize(s)
	if err != nil {
		return nil, err
	}
	out := s.Clone()
	switch c.Scope.Kind {
	case ScopeFolder:
		if c.Remove {
			out.ClearRule(c.Scope.Folder, c.Tool)
			return out, nil
		}
		if err := out.SetRule(c.Scope.Folder, c.Tool, c.Account); err != nil {
			return nil, err
		}
	case ScopeEverywhere:
		if err := out.SetEverywhere(c.Tool, c.Account); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// FileEdit is one file a change writes, with the exact lines.
type FileEdit struct {
	Path string
	// Action is "adds", "changes" or "removes", in plain words.
	Action string
	Lines  []string
}

// KeptRule is a folder rule that still wins after a change, listed so the
// person knows those folders will not follow it.
type KeptRule struct {
	Folder  string
	Account string
	Display string
}

// Preview is everything a person sees before saying yes: the plain-words
// sentences, the folder rules that keep winning, and the exact file edits.
// The sentences are built here so the command line and the screens say
// exactly the same thing.
type Preview struct {
	Change Change
	// Sentences are the plain-words answer to "what will this do?".
	Sentences []string
	// KeptIntro introduces Kept ("2 folders keep their own rule and will
	// not change:"), or is "" when Kept is empty.
	KeptIntro string
	Kept      []KeptRule
	// Edits are the files written, accounts.toml first.
	Edits []FileEdit
	// Warnings are extra sentences (home folder, a missing account folder).
	Warnings []string
	// NoChange is set when the change would change nothing.
	NoChange bool
	// DriveRoot is set when the rule covers a whole drive or share, which
	// needs a second confirmation; SecondConfirm is its question.
	DriveRoot     bool
	SecondConfirm string
	// Summary is the journal's one-line description.
	Summary string
	// BeforeHash identifies the store this preview was built from. Applying
	// against any other store fails with ErrStalePreview.
	BeforeHash string
	// Group, when set by the caller before applying, ties this change to
	// others made by the same command so one Undo takes them all back (see
	// NewGroup).
	Group string
}

// PreviewInput is what [BuildPreview] needs.
type PreviewInput struct {
	Store  *Store
	Change Change
	// StorePath is shown in the edits; accounts.toml when empty.
	StorePath string
	// Defaults says who each tool's default account is, so a sentence can
	// say "default (zubair@gmail.com)". Missing entries say just "default".
	Defaults map[Tool]string
	// Home is the person's home folder, to warn about a rule on it.
	Home string
	// Display overrides how an account is named (Git: "Zubair <z@x>").
	Display func(Account) string
	// Uses overrides the main verb phrase. It gets the tool's display name
	// and the account's display and returns, for example, "Git will commit
	// as Zubair <zubair@work.com>". The default is "<Tool> will use <acct>".
	Uses func(tool, account string) string
	// Edits are the adapter's own file edits, listed after accounts.toml.
	Edits []FileEdit
}

// BuildPreview works out what a change will do and says it in plain words.
func BuildPreview(in PreviewInput) (Preview, error) {
	s := in.Store
	if s == nil {
		s = NewStore()
	}
	c, err := in.Change.normalize(s)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Change: c, BeforeHash: StoreHash(s)}
	tool := c.Tool.DisplayName()

	display := func(name string) string {
		a, ok := s.Account(c.Tool, name)
		if !ok {
			return name
		}
		if in.Display != nil {
			return in.Display(a)
		}
		if a.IsDefault() {
			who := in.Defaults[c.Tool]
			if who == "" {
				who = s.DefaultEmail[c.Tool]
			}
			if who != "" {
				return DefaultName + " (" + who + ")"
			}
			return DefaultName
		}
		return a.Display()
	}
	uses := func(acct string) string {
		if in.Uses != nil {
			return in.Uses(tool, acct)
		}
		return tool + " will use " + acct
	}
	everywhereName := DefaultName
	if v, ok := s.Everywhere[c.Tool]; ok {
		if a, ok := s.Account(c.Tool, v); ok {
			everywhereName = a.Name
		}
	}
	storePath := in.StorePath
	if storePath == "" {
		storePath = StoreFileName
	}

	switch c.Scope.Kind {
	case ScopeOnce:
		p.Sentences = []string{"Just this once, " + uses(display(c.Account)) + ". Nothing is saved."}
		p.Summary = fmt.Sprintf("%s used %s once", tool, c.Account)
		return p, nil

	case ScopeEverywhere:
		p.Summary = fmt.Sprintf("%s uses %s everywhere", tool, c.Account)
		if strings.EqualFold(everywhereName, c.Account) {
			p.NoChange = true
			p.Sentences = []string{fmt.Sprintf("%s already uses %s everywhere. Nothing will change.", tool, display(c.Account))}
			return p, nil
		}
		p.Sentences = []string{"Everywhere, " + uses(display(c.Account)) + "."}
		for _, r := range s.Rules {
			if name, ok := r.Accounts[c.Tool]; ok {
				if _, exists := s.Account(c.Tool, name); exists {
					p.Kept = append(p.Kept, KeptRule{Folder: r.Folder, Account: name, Display: display(name)})
				}
			}
		}
		p.KeptIntro = keptIntro(len(p.Kept), false)
		after, aerr := c.ApplyTo(s)
		if aerr != nil {
			return Preview{}, aerr
		}
		lines := []string{"[everywhere]"}
		for _, t := range Tools() {
			if v, ok := after.Everywhere[t]; ok {
				lines = append(lines, string(t)+" = "+tomlString(v))
			}
		}
		action := "changes"
		if len(after.Everywhere) == 0 {
			action, lines = "removes", []string{"[everywhere]", string(c.Tool) + " = " + tomlString(everywhereName)}
		}
		p.Edits = append([]FileEdit{{Path: storePath, Action: action, Lines: lines}}, in.Edits...)
		return p, nil
	}

	// Folder scope.
	folder := c.Scope.Folder
	existing, hasRule := s.Rule(folder)
	current, hasTool := "", false
	if hasRule {
		current, hasTool = existing.Accounts[c.Tool]
	}
	outer := outerRule(s, c.Tool, folder)

	if c.Remove {
		p.Summary = fmt.Sprintf("Removed the %s rule on %s", tool, folder)
		if !hasTool {
			p.NoChange = true
			p.Sentences = []string{fmt.Sprintf("%s has no %s rule of its own. Nothing will change.", folder, tool)}
			return p, nil
		}
		fallback := fmt.Sprintf("%s, like everywhere else", display(everywhereName))
		if outer != nil {
			fallback = fmt.Sprintf("%s, from the rule on %s", display(outer.Accounts[c.Tool]), outer.Folder)
		}
		p.Sentences = []string{fmt.Sprintf("%s will no longer have its own %s rule. It and every folder inside it will use %s.", folder, tool, fallback)}
	} else {
		p.Summary = fmt.Sprintf("%s uses %s in %s", tool, c.Account, folder)
		if hasTool && strings.EqualFold(current, c.Account) {
			p.NoChange = true
			p.Sentences = []string{fmt.Sprintf("%s already uses %s in %s. Nothing will change.", tool, display(c.Account), folder)}
			return p, nil
		}
		p.Sentences = []string{fmt.Sprintf("In %s and every folder inside it, %s.", folder, uses(display(c.Account)))}
		if outer != nil {
			p.Sentences = append(p.Sentences, fmt.Sprintf("The rest of %s stays %s.", outer.Folder, display(outer.Accounts[c.Tool])))
		}
		p.Sentences = append(p.Sentences, fmt.Sprintf("Everywhere else stays %s.", display(everywhereName)))
	}

	for _, r := range s.Rules {
		name, ok := r.Accounts[c.Tool]
		if !ok || SameFolder(r.Folder, folder) || !FolderContains(folder, r.Folder) {
			continue
		}
		if _, exists := s.Account(c.Tool, name); exists {
			p.Kept = append(p.Kept, KeptRule{Folder: r.Folder, Account: name, Display: display(name)})
		}
	}
	p.KeptIntro = keptIntro(len(p.Kept), true)

	if IsDriveRoot(folder) && !c.Remove {
		p.DriveRoot = true
		p.SecondConfirm = fmt.Sprintf("This rule covers every folder on %s. Apply it to the whole drive?", folder)
	}
	if in.Home != "" && SameFolder(in.Home, folder) && !c.Remove {
		p.Warnings = append(p.Warnings, "This is your home folder, so the rule covers every project inside it.")
	}

	after, err := c.ApplyTo(s)
	if err != nil {
		return Preview{}, err
	}
	edit := FileEdit{Path: storePath}
	if r, ok := after.Rule(folder); ok {
		edit.Action = "adds"
		if hasRule {
			edit.Action = "changes"
		}
		edit.Lines = ruleLines(r)
	} else {
		edit.Action = "removes"
		edit.Lines = ruleLines(existing)
	}
	p.Edits = append([]FileEdit{edit}, in.Edits...)
	return p, nil
}

// outerRule is the innermost rule for tool that strictly contains folder
// and names an account that exists, or nil.
func outerRule(s *Store, tool Tool, folder string) *Rule {
	var best *Rule
	bestDepth := -1
	for i, r := range s.Rules {
		name, ok := r.Accounts[tool]
		if !ok || SameFolder(r.Folder, folder) || !FolderContains(r.Folder, folder) {
			continue
		}
		if _, exists := s.Account(tool, name); !exists {
			continue
		}
		if d := folderDepth(r.Folder); d > bestDepth {
			best, bestDepth = &s.Rules[i], d
		}
	}
	return best
}

func keptIntro(n int, inside bool) string {
	where := ""
	if inside {
		where = " inside it"
	}
	switch n {
	case 0:
		return ""
	case 1:
		return "1 folder" + where + " keeps its own rule and will not change:"
	}
	return fmt.Sprintf("%d folders%s keep their own rule and will not change:", n, where)
}

// Lines renders the whole preview as text, the way the command line prints
// it and the confirm dialog shows it.
func (p Preview) Lines() []string {
	out := append([]string(nil), p.Sentences...)
	if len(p.Kept) > 0 {
		out = append(out, p.KeptIntro)
		w := 0
		for _, k := range p.Kept {
			w = max(w, len([]rune(k.Folder)))
		}
		for _, k := range p.Kept {
			out = append(out, "  "+padRight(k.Folder, w)+"   "+k.Display)
		}
	}
	out = append(out, p.Warnings...)
	if len(p.Edits) > 0 && !p.NoChange {
		out = append(out, "", "Devpit will write:")
		for _, e := range p.Edits {
			out = append(out, "  "+e.Path+" ("+e.Action+")")
			for _, l := range e.Lines {
				out = append(out, "    "+l)
			}
		}
	}
	return out
}

// Text is Lines joined with newlines.
func (p Preview) Text() string { return strings.Join(p.Lines(), "\n") }

func padRight(s string, w int) string {
	if n := len([]rune(s)); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// StoreHash identifies a store's content: the SHA-256 of its encoding.
// Previews, the journal and undo use it to notice changes made since.
func StoreHash(s *Store) string {
	data, err := Encode(s)
	if err != nil {
		data = []byte(err.Error())
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
