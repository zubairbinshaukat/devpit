package accounts

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// SchemaVersion is the accounts.toml schema this Devpit reads and writes. A
// file with a higher version is refused, never partly applied.
const SchemaVersion = 1

// Account is one named account of one tool. It holds who the account is and
// where its folder is, and nothing else: there is no free-form field, and
// [Store.Validate] refuses any value that looks like a secret, so a token
// cannot end up in accounts.toml.
type Account struct {
	// Tool is the tool the account belongs to.
	Tool Tool `json:"tool"`
	// Name is unique per tool, ignoring case. "default" is never stored.
	Name string `json:"name"`
	// Email is the address the tool reports for this account, if any.
	Email string `json:"email,omitempty"`
	// Label is a short display name where an email is not the identity: a
	// GitHub login, or the name Git commits under.
	Label string `json:"label,omitempty"`
	// Org is the organisation the tool reported at sign-in, for display.
	Org string `json:"org,omitempty"`
	// Dir is the account's own folder for tools that keep one per account
	// (Claude Code, Vercel, Supabase). Empty for the default account.
	Dir string `json:"dir,omitempty"`
	// Added is when Devpit first recorded the account.
	Added time.Time `json:"added,omitzero"`
	// ImportedFrom names where the account came from when Devpit did not
	// create it ("claude-acc", "detected").
	ImportedFrom string `json:"imported_from,omitempty"`
}

// IsDefault reports whether a is the tool's default account.
func (a Account) IsDefault() bool { return IsDefault(a.Name) }

// Display is how a screen or a sentence names the account: "work
// (zubair@work.com)", "work (zubairbinshaukat)" or just "work".
func (a Account) Display() string {
	who := a.Email
	if who == "" {
		who = a.Label
	}
	if who == "" {
		return a.Name
	}
	return a.Name + " (" + who + ")"
}

// Rule is one folder rule: in Folder and every folder inside it, each tool
// listed uses the named account. Tools not listed are not affected.
type Rule struct {
	Folder   string          `json:"folder"`
	Accounts map[Tool]string `json:"accounts"`
}

// Store is the whole of accounts.toml in memory.
type Store struct {
	Version    int             `json:"version"`
	Accounts   []Account       `json:"accounts,omitempty"`
	Everywhere map[Tool]string `json:"everywhere,omitempty"`
	Rules      []Rule          `json:"rules,omitempty"`
	// DefaultEmail records who each tool's default account was the last
	// time the person checked it (sign-in, import, or an explicit Verify),
	// so screens can name it without asking the tool. It may be stale.
	DefaultEmail map[Tool]string `json:"default_email,omitempty"`
}

// NewStore returns an empty store: every tool on its default account.
func NewStore() *Store {
	return &Store{Version: SchemaVersion, Everywhere: map[Tool]string{}, DefaultEmail: map[Tool]string{}}
}

// Clone returns a deep copy.
func (s *Store) Clone() *Store {
	if s == nil {
		return NewStore()
	}
	out := &Store{
		Version:    s.Version,
		Accounts:   slices.Clone(s.Accounts),
		Everywhere: make(map[Tool]string, len(s.Everywhere)),
	}
	for k, v := range s.Everywhere {
		out.Everywhere[k] = v
	}
	out.DefaultEmail = make(map[Tool]string, len(s.DefaultEmail))
	for k, v := range s.DefaultEmail {
		out.DefaultEmail[k] = v
	}
	for _, r := range s.Rules {
		cp := Rule{Folder: r.Folder, Accounts: make(map[Tool]string, len(r.Accounts))}
		for k, v := range r.Accounts {
			cp.Accounts[k] = v
		}
		out.Rules = append(out.Rules, cp)
	}
	return out
}

// AccountsFor returns the named accounts of one tool, in stored order. The
// default account is not included; see [Store.Names].
func (s *Store) AccountsFor(t Tool) []Account {
	var out []Account
	for _, a := range s.Accounts {
		if a.Tool == t {
			out = append(out, a)
		}
	}
	return out
}

// Names returns "default" followed by the tool's named accounts.
func (s *Store) Names(t Tool) []string {
	out := []string{DefaultName}
	for _, a := range s.AccountsFor(t) {
		out = append(out, a.Name)
	}
	return out
}

// Account returns the account called name (any case) for t. "default"
// always exists and comes back as an Account with only Tool and Name set.
func (s *Store) Account(t Tool, name string) (Account, bool) {
	if IsDefault(name) {
		return Account{Tool: t, Name: DefaultName}, true
	}
	for _, a := range s.Accounts {
		if a.Tool == t && strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return Account{}, false
}

// FindAccount resolves what a person typed (a full name or a short form
// matching one name) to an account of t. See [MatchName].
func (s *Store) FindAccount(t Tool, input string) (Account, error) {
	name, err := MatchName(input, s.Names(t))
	if err != nil {
		return Account{}, err
	}
	a, _ := s.Account(t, name)
	return a, nil
}

// CheckNewName reports whether name can be used for a new account of t.
func (s *Store) CheckNewName(t Tool, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if _, ok := s.Account(t, name); ok {
		return fmt.Errorf("%q: %w (%s)", name, ErrNameTaken, t.DisplayName())
	}
	return nil
}

// AddAccount adds a after checking its name and every field.
func (s *Store) AddAccount(a Account) error {
	if !a.Tool.Known() {
		return fmt.Errorf("unknown tool %q", a.Tool)
	}
	if err := s.CheckNewName(a.Tool, a.Name); err != nil {
		return err
	}
	if err := validateAccountFields(a); err != nil {
		return err
	}
	s.Accounts = append(s.Accounts, a)
	return nil
}

// RenameAccount renames an account and every rule and "everywhere" choice
// that names it. The default account cannot be renamed, and no account can
// be renamed to "default".
func (s *Store) RenameAccount(t Tool, from, to string) error {
	if IsDefault(from) {
		return fmt.Errorf("the default account cannot be renamed: %w", ErrReservedName)
	}
	a, ok := s.Account(t, from)
	if !ok {
		return &UnknownNameError{Input: from, Known: s.Names(t)}
	}
	if !strings.EqualFold(a.Name, to) {
		if err := s.CheckNewName(t, to); err != nil {
			return err
		}
	} else if err := ValidateName(to); err != nil {
		return err
	}
	for i := range s.Accounts {
		if s.Accounts[i].Tool == t && strings.EqualFold(s.Accounts[i].Name, a.Name) {
			s.Accounts[i].Name = to
		}
	}
	if v, ok := s.Everywhere[t]; ok && strings.EqualFold(v, a.Name) {
		s.Everywhere[t] = to
	}
	for _, r := range s.Rules {
		if v, ok := r.Accounts[t]; ok && strings.EqualFold(v, a.Name) {
			r.Accounts[t] = to
		}
	}
	return nil
}

// RulesUsing returns the rules that name account name for t.
func (s *Store) RulesUsing(t Tool, name string) []Rule {
	var out []Rule
	for _, r := range s.Rules {
		if v, ok := r.Accounts[t]; ok && strings.EqualFold(v, name) {
			out = append(out, r)
		}
	}
	return out
}

// RemoveAccount removes an account and every reference to it: rules stop
// naming it for t (a rule left with no tools is removed) and an "everywhere"
// choice of it goes back to default. It returns the rules that named it, as
// they were, so a preview can say what each folder falls back to. The
// default account cannot be removed.
func (s *Store) RemoveAccount(t Tool, name string) ([]Rule, error) {
	if IsDefault(name) {
		return nil, fmt.Errorf("the default account cannot be removed: %w", ErrReservedName)
	}
	a, ok := s.Account(t, name)
	if !ok {
		return nil, &UnknownNameError{Input: name, Known: s.Names(t)}
	}
	used := s.Clone().RulesUsing(t, a.Name)
	s.Accounts = slices.DeleteFunc(s.Accounts, func(x Account) bool {
		return x.Tool == t && strings.EqualFold(x.Name, a.Name)
	})
	if v, ok := s.Everywhere[t]; ok && strings.EqualFold(v, a.Name) {
		delete(s.Everywhere, t)
	}
	for _, r := range s.Rules {
		if v, ok := r.Accounts[t]; ok && strings.EqualFold(v, a.Name) {
			delete(r.Accounts, t)
		}
	}
	s.dropEmptyRules()
	return used, nil
}

// SetEverywhere makes name the account t uses wherever no folder rule says
// otherwise. "default" clears the choice.
func (s *Store) SetEverywhere(t Tool, name string) error {
	if !t.Known() {
		return fmt.Errorf("unknown tool %q", t)
	}
	a, ok := s.Account(t, name)
	if !ok {
		return &UnknownNameError{Input: name, Known: s.Names(t)}
	}
	if s.Everywhere == nil {
		s.Everywhere = map[Tool]string{}
	}
	if a.IsDefault() {
		delete(s.Everywhere, t)
		return nil
	}
	s.Everywhere[t] = a.Name
	return nil
}

// Rule returns the rule for exactly folder (not a parent), if there is one.
func (s *Store) Rule(folder string) (Rule, bool) {
	if i := s.ruleIndex(folder); i >= 0 {
		return s.Rules[i], true
	}
	return Rule{}, false
}

func (s *Store) ruleIndex(folder string) int {
	k := folderKey(folder)
	if k == "" {
		return -1
	}
	for i, r := range s.Rules {
		if folderKey(r.Folder) == k {
			return i
		}
	}
	return -1
}

// SetRule makes t use account name in folder and every folder inside it.
// folder must be absolute; it is stored normalized. "default" is a real
// choice here: it overrides an outer rule for this folder.
func (s *Store) SetRule(folder string, t Tool, name string) error {
	if !t.Known() {
		return fmt.Errorf("unknown tool %q", t)
	}
	norm, err := NormalizeFolder(folder, "")
	if err != nil {
		return err
	}
	a, ok := s.Account(t, name)
	if !ok {
		return &UnknownNameError{Input: name, Known: s.Names(t)}
	}
	if i := s.ruleIndex(norm); i >= 0 {
		s.Rules[i].Accounts[t] = a.Name
		return nil
	}
	s.Rules = append(s.Rules, Rule{Folder: norm, Accounts: map[Tool]string{t: a.Name}})
	s.sortRules()
	return nil
}

// ClearRule removes t from the rule on exactly folder. A rule left with no
// tools is removed. It reports whether anything changed.
func (s *Store) ClearRule(folder string, t Tool) bool {
	i := s.ruleIndex(folder)
	if i < 0 {
		return false
	}
	if _, ok := s.Rules[i].Accounts[t]; !ok {
		return false
	}
	delete(s.Rules[i].Accounts, t)
	s.dropEmptyRules()
	return true
}

func (s *Store) dropEmptyRules() {
	s.Rules = slices.DeleteFunc(s.Rules, func(r Rule) bool { return len(r.Accounts) == 0 })
}

// sortRules orders rules outer before inner, then alphabetically, so the
// file reads top-down the way the resolver walks.
func (s *Store) sortRules() {
	slices.SortStableFunc(s.Rules, func(a, b Rule) int {
		ka, kb := folderKey(a.Folder), folderKey(b.Folder)
		return strings.Compare(ka, kb)
	})
}

// NeedsShim reports whether t needs a shim: it can be shimmed, and it has a
// second account or a rule, so which account to use depends on the folder.
// A tool with nothing set needs no shim, and gets none.
func (s *Store) NeedsShim(t Tool) bool {
	if t.ShimName() == "" {
		return false
	}
	if len(s.AccountsFor(t)) > 0 {
		return true
	}
	if v, ok := s.Everywhere[t]; ok && !IsDefault(v) {
		return true
	}
	for _, r := range s.Rules {
		if _, ok := r.Accounts[t]; ok {
			return true
		}
	}
	return false
}

// Manages reports whether Devpit has anything at all for t: a named
// account, a rule, or an "everywhere" choice. When it does, Devpit's answer
// wins over a variable left in the terminal by something else (the shim
// clears CLAUDE_CONFIG_DIR for the default account); when it does not, the
// tool is left fully untouched.
func (s *Store) Manages(t Tool) bool {
	if len(s.AccountsFor(t)) > 0 {
		return true
	}
	if _, ok := s.Everywhere[t]; ok {
		return true
	}
	for _, r := range s.Rules {
		if _, ok := r.Accounts[t]; ok {
			return true
		}
	}
	return false
}

// SetDefaultEmail records who t's default account is ("" forgets it).
func (s *Store) SetDefaultEmail(t Tool, email string) error {
	if !t.Known() {
		return fmt.Errorf("unknown tool %q", t)
	}
	if s.DefaultEmail == nil {
		s.DefaultEmail = map[Tool]string{}
	}
	if email == "" {
		delete(s.DefaultEmail, t)
		return nil
	}
	if err := validEmail(email); err != nil {
		return err
	}
	s.DefaultEmail[t] = email
	return nil
}

// AccountDirs returns the folder of every account that has one.
func (s *Store) AccountDirs() []string {
	var out []string
	for _, a := range s.Accounts {
		if a.Dir != "" {
			out = append(out, a.Dir)
		}
	}
	return out
}

// InvalidError lists what is wrong with a store, one plain sentence each.
// The file it came from is left exactly as it is.
type InvalidError struct {
	Path     string
	Problems []string
}

func (e *InvalidError) Error() string {
	where := "accounts.toml"
	if e.Path != "" {
		where = e.Path
	}
	return fmt.Sprintf("%s has %d problem(s) and was not changed: %s", where, len(e.Problems), strings.Join(e.Problems, "; "))
}

// ErrNewerSchema is wrapped by the error for a file from a newer Devpit.
var ErrNewerSchema = errors.New("accounts.toml was written by a newer Devpit")

// Validate checks every field of every entry. It does not check that rules
// name accounts that exist: such a rule is skipped by the resolver and shown
// as a problem, never a reason to refuse the whole file.
func (s *Store) Validate() error {
	var probs []string
	add := func(format string, args ...any) { probs = append(probs, fmt.Sprintf(format, args...)) }

	if s.Version > SchemaVersion {
		return fmt.Errorf("%w (schema version %d; this Devpit understands %d). Update Devpit", ErrNewerSchema, s.Version, SchemaVersion)
	}
	seen := map[string]bool{}
	for i, a := range s.Accounts {
		where := fmt.Sprintf("account %d", i+1)
		if !a.Tool.Known() {
			add("%s: unknown tool %q", where, a.Tool)
			continue
		}
		if IsDefault(a.Name) {
			add("%s (%s): %q is reserved and is never listed", where, a.Tool, a.Name)
		} else if !validName(a.Name) {
			add("%s (%s): the name %q is not valid; %v", where, a.Tool, a.Name, ErrInvalidName)
		}
		k := string(a.Tool) + "/" + strings.ToLower(a.Name)
		if seen[k] {
			add("%s: %s already has an account called %q", where, a.Tool.DisplayName(), a.Name)
		}
		seen[k] = true
		if err := validateAccountFields(a); err != nil {
			add("%s (%s %s): %v", where, a.Tool, a.Name, err)
		}
	}
	for t, v := range s.Everywhere {
		if !t.Known() {
			add("everywhere: unknown tool %q", t)
			continue
		}
		if !IsDefault(v) && !validName(v) {
			add("everywhere: %s = %q is not a valid account name", t, v)
		}
	}
	for t, v := range s.DefaultEmail {
		if !t.Known() {
			add("default_email: unknown tool %q", t)
			continue
		}
		if err := validEmail(v); err != nil {
			add("default_email: %s: %v", t, err)
		}
	}
	folders := map[string]bool{}
	for i, r := range s.Rules {
		where := fmt.Sprintf("rule %d", i+1)
		norm, err := NormalizeFolder(r.Folder, "")
		if err != nil {
			add("%s: %v", where, err)
			continue
		}
		if hasKnownToken(r.Folder) {
			add("%s: the folder looks like it holds a secret", where)
		}
		k := strings.ToUpper(norm)
		if folders[k] {
			add("%s: there is already a rule for %s; merge the two", where, norm)
		}
		folders[k] = true
		if len(r.Accounts) == 0 {
			add("%s (%s): names no tool", where, norm)
		}
		for t, v := range r.Accounts {
			if !t.Known() {
				add("%s (%s): unknown tool %q", where, norm, t)
				continue
			}
			if !IsDefault(v) && !validName(v) {
				add("%s (%s): %s = %q is not a valid account name", where, norm, t, v)
			}
		}
	}
	if len(probs) > 0 {
		return &InvalidError{Problems: probs}
	}
	return nil
}

// validateAccountFields checks the free-text-looking fields against tight
// patterns, and refuses anything that looks like a secret.
func validateAccountFields(a Account) error {
	if a.Email != "" {
		if err := validEmail(a.Email); err != nil {
			return err
		}
	}
	if a.Label != "" {
		if err := validLabel(a.Label); err != nil {
			return err
		}
	}
	if a.Org != "" {
		if err := validLabel(a.Org); err != nil {
			return fmt.Errorf("org: %w", err)
		}
	}
	if a.Dir != "" {
		norm, err := NormalizeFolder(a.Dir, "")
		if err != nil {
			return fmt.Errorf("folder: %w", err)
		}
		if len(norm) > 1024 {
			return errors.New("folder: the path is too long")
		}
		if hasKnownToken(a.Dir) {
			return errors.New("folder: the path looks like it holds a secret")
		}
	}
	if a.ImportedFrom != "" && !validTag(a.ImportedFrom) {
		return fmt.Errorf("imported_from %q may use only lower-case letters, digits and dashes (at most 32)", a.ImportedFrom)
	}
	return nil
}

// validEmail is deliberately loose about what an address may be and strict
// about what it may not: one @, no spaces or control characters, at most 254
// bytes, and nothing that looks like a secret.
func validEmail(e string) error {
	if len(e) > 254 || !utf8.ValidString(e) {
		return fmt.Errorf("email %q is too long or not text", e)
	}
	local, domain, ok := strings.Cut(e, "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") {
		return fmt.Errorf("email %q is not an address", e)
	}
	for _, r := range e {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '"' || r == '\'' || r == '<' || r == '>' {
			return fmt.Errorf("email %q has a character an address cannot have", e)
		}
	}
	if LooksSecret(e) {
		return errors.New("email: the value looks like a token, not an address")
	}
	return nil
}

// validLabel accepts a person's or account's display name: at most 64
// characters of letters, digits, spaces and . ' - , ( ) @ _, and nothing
// that looks like a secret.
func validLabel(l string) error {
	if utf8.RuneCountInString(l) > 64 || !utf8.ValidString(l) {
		return fmt.Errorf("label %q is longer than 64 characters", l)
	}
	for _, r := range l {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || strings.ContainsRune(".'-,()@_+", r) {
			continue
		}
		return fmt.Errorf("label %q has a character a name cannot have (%q)", l, r)
	}
	if LooksSecret(l) {
		return errors.New("label: the value looks like a token, not a name")
	}
	return nil
}

// validTag is a short lower-case identifier.
func validTag(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}
