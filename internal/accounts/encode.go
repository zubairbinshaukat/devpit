package accounts

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// fileV1 is accounts.toml as TOML sees it. Rules are maps because each
// tool's key sits next to "folder" in the same table.
type fileV1 struct {
	Version    int                 `toml:"version"`
	Account    []fileAccount       `toml:"account"`
	Everywhere map[string]string   `toml:"everywhere"`
	Rule       []map[string]string `toml:"rule"`
	// DefaultEmail is [default_email]: who each tool's default account is.
	DefaultEmail map[string]string `toml:"default_email"`
}

type fileAccount struct {
	Tool         string    `toml:"tool"`
	Name         string    `toml:"name"`
	Email        string    `toml:"email"`
	Label        string    `toml:"label"`
	Org          string    `toml:"org"`
	Dir          string    `toml:"dir"`
	Added        time.Time `toml:"added"`
	ImportedFrom string    `toml:"imported_from"`
}

// errSyntax marks a file that is not TOML at all, or not the right shape:
// the kind of damage that is quarantined.
var errSyntax = errors.New("accounts.toml could not be read")

// Parse decodes accounts.toml. An unknown key anywhere is refused rather
// than ignored, so nothing unexpected (a pasted token, say) can sit in the
// file unnoticed; that and every other field problem comes back as an
// [*InvalidError]. A file that is not TOML wraps errSyntax.
func Parse(data []byte) (*Store, error) {
	// The version first, leniently: a newer file may well have keys this
	// Devpit does not know, and the person needs to hear "newer", not
	// "unknown key".
	var head struct {
		Version int `toml:"version"`
	}
	if err := toml.Unmarshal(data, &head); err == nil && head.Version > SchemaVersion {
		return nil, fmt.Errorf("%w (schema version %d; this Devpit understands %d). Update Devpit, or move the file aside to start fresh",
			ErrNewerSchema, head.Version, SchemaVersion)
	}
	var f fileV1
	dec := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			var probs []string
			for _, e := range strict.Errors {
				probs = append(probs, fmt.Sprintf("unknown key %q", strings.Join(e.Key(), ".")))
			}
			return nil, &InvalidError{Problems: probs}
		}
		return nil, fmt.Errorf("%w: %w", errSyntax, err)
	}
	if f.Version == 0 {
		f.Version = SchemaVersion
	}
	s := &Store{Version: f.Version, Everywhere: map[Tool]string{}}
	if s.Version > SchemaVersion {
		return nil, fmt.Errorf("%w (schema version %d; this Devpit understands %d). Update Devpit, or move the file aside to start fresh",
			ErrNewerSchema, s.Version, SchemaVersion)
	}
	for _, a := range f.Account {
		s.Accounts = append(s.Accounts, Account{
			Tool: Tool(a.Tool), Name: a.Name, Email: a.Email, Label: a.Label, Org: a.Org,
			Dir: a.Dir, Added: a.Added, ImportedFrom: a.ImportedFrom,
		})
	}
	for k, v := range f.Everywhere {
		s.Everywhere[Tool(k)] = v
	}
	s.DefaultEmail = map[Tool]string{}
	for k, v := range f.DefaultEmail {
		s.DefaultEmail[Tool(k)] = v
	}
	var probs []string
	for i, m := range f.Rule {
		r := Rule{Accounts: map[Tool]string{}}
		for k, v := range m {
			if k == "folder" {
				r.Folder = v
				continue
			}
			r.Accounts[Tool(k)] = v
		}
		if r.Folder == "" {
			probs = append(probs, fmt.Sprintf("rule %d has no folder", i+1))
			continue
		}
		if norm, err := NormalizeFolder(r.Folder, ""); err == nil {
			r.Folder = norm
		}
		s.Rules = append(s.Rules, r)
	}
	if err := s.Validate(); err != nil {
		var inv *InvalidError
		if errors.As(err, &inv) {
			inv.Problems = append(probs, inv.Problems...)
		}
		return nil, err
	}
	if len(probs) > 0 {
		return nil, &InvalidError{Problems: probs}
	}
	return s, nil
}

// Encode writes the store as accounts.toml. The output is deterministic:
// accounts by tool then name, tools in display order, rules outer before
// inner. It refuses a store that does not pass [Store.Validate].
func Encode(s *Store) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("# Devpit accounts: which account each tool uses, and where.\n")
	b.WriteString("# Names, emails and folders only. Devpit never stores a token here.\n")
	b.WriteString("version = " + strconv.Itoa(SchemaVersion) + "\n")

	accts := slices.Clone(s.Accounts)
	slices.SortStableFunc(accts, func(x, y Account) int {
		if c := x.Tool.order() - y.Tool.order(); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})
	for _, a := range accts {
		b.WriteString("\n[[account]]\n")
		kv(&b, "tool", string(a.Tool))
		kv(&b, "name", a.Name)
		if a.Email != "" {
			kv(&b, "email", a.Email)
		}
		if a.Label != "" {
			kv(&b, "label", a.Label)
		}
		if a.Org != "" {
			kv(&b, "org", a.Org)
		}
		if a.Dir != "" {
			kv(&b, "dir", a.Dir)
		}
		if !a.Added.IsZero() {
			b.WriteString("added = " + a.Added.UTC().Truncate(time.Second).Format(time.RFC3339) + "\n")
		}
		if a.ImportedFrom != "" {
			kv(&b, "imported_from", a.ImportedFrom)
		}
	}

	if len(s.Everywhere) > 0 {
		b.WriteString("\n[everywhere]\n")
		for _, t := range Tools() {
			if v, ok := s.Everywhere[t]; ok {
				kv(&b, string(t), v)
			}
		}
	}

	if len(s.DefaultEmail) > 0 {
		b.WriteString("\n[default_email]\n")
		for _, t := range Tools() {
			if v, ok := s.DefaultEmail[t]; ok {
				kv(&b, string(t), v)
			}
		}
	}

	rules := s.Clone()
	rules.sortRules()
	for _, r := range rules.Rules {
		b.WriteString("\n")
		b.WriteString(strings.Join(ruleLines(r), "\n"))
		b.WriteString("\n")
	}
	return b.Bytes(), nil
}

// ruleLines is one rule as it appears in the file. Previews show exactly
// these lines.
func ruleLines(r Rule) []string {
	lines := []string{"[[rule]]", "folder = " + tomlString(r.Folder)}
	for _, t := range Tools() {
		if v, ok := r.Accounts[t]; ok {
			lines = append(lines, string(t)+" = "+tomlString(v))
		}
	}
	return lines
}

func kv(b *bytes.Buffer, k, v string) {
	b.WriteString(k + " = " + tomlString(v) + "\n")
}

// tomlString quotes v for TOML: a path becomes a literal string ('C:\Work',
// no escapes, which is what a person would type) when it can, and anything
// else a basic string with escapes.
func tomlString(v string) string {
	literal := strings.Contains(v, `\`) && !strings.ContainsAny(v, "'\r\n")
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			literal = false
		}
	}
	if literal {
		return "'" + v + "'"
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
