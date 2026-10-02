package accounts

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		want error
	}{
		{"work", nil},
		{"Work-2", nil},
		{"a", nil},
		{"9lives", nil},
		{strings.Repeat("a", MaxNameLen), nil},
		{strings.Repeat("a", MaxNameLen+1), ErrInvalidName},
		{"", ErrInvalidName},
		{"-work", ErrInvalidName},
		{"work-", ErrInvalidName},
		{"my work", ErrInvalidName},
		{"my_work", ErrInvalidName},
		{"wörk", ErrInvalidName},
		{"work.com", ErrInvalidName},
		{"default", ErrReservedName},
		{"Default", ErrReservedName},
		{"DEFAULT", ErrReservedName},
	}
	for _, c := range cases {
		err := ValidateName(c.name)
		if c.want == nil && err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", c.name, err)
		}
		if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("ValidateName(%q) = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestSuggestName(t *testing.T) {
	cases := []struct {
		email string
		taken []string
		want  string
	}{
		{"zubair@work.com", nil, "work"},
		{"zubair@Work.COM", nil, "work"},
		{"z@mail.acme.co.uk", nil, "acme"},
		{"z@acme.io", nil, "acme"},
		{"zubair@gmail.com", nil, "zubair"},
		{"zubair.shaukat@gmail.com", nil, "zubair-shaukat"},
		{"zubair+oss@gmail.com", nil, "oss"},
		{"zubair@proton.me", nil, "zubair"},
		{"12345+zubairbinshaukat@users.noreply.github.com", nil, "zubairbinshaukat"},
		{"zubair@work.com", []string{"work"}, "work-2"},
		{"zubair@work.com", []string{"WORK", "work-2"}, "work-3"},
		{"default@gmail.com", nil, "default-account"},
		{"x@default.com", nil, "default-account"},
		{"x@default.com", []string{"default-account"}, "default-account-2"},
		{"", nil, "account"},
		{"___@gmail.com", nil, "personal"},
		{strings.Repeat("a", 40) + "@gmail.com", []string{strings.Repeat("a", 32)}, strings.Repeat("a", 30) + "-2"},
	}
	for _, c := range cases {
		got := SuggestName(c.email, c.taken)
		if got != c.want {
			t.Errorf("SuggestName(%q, %v) = %q, want %q", c.email, c.taken, got, c.want)
		}
		if err := ValidateName(got); err != nil {
			t.Errorf("SuggestName(%q) = %q, which does not validate: %v", c.email, got, err)
		}
	}
}

func TestMatchName(t *testing.T) {
	names := []string{"default", "second", "secret-lab", "sec", "work"}
	cases := []struct {
		in, want string
		amb      []string
		unknown  bool
	}{
		{in: "work", want: "work"},
		{in: "WORK", want: "work"},
		{in: "wo", want: "work"},
		{in: "sec", want: "sec"}, // exact beats prefix
		{in: "seco", want: "second"},
		{in: "se", amb: []string{"second", "secret-lab", "sec"}},
		{in: "d", want: "default"},
		{in: "x", unknown: true},
		{in: "", unknown: true},
	}
	for _, c := range cases {
		got, err := MatchName(c.in, names)
		switch {
		case c.amb != nil:
			var amb *AmbiguousNameError
			if !errors.As(err, &amb) {
				t.Errorf("MatchName(%q) err = %v, want ambiguous", c.in, err)
				continue
			}
			if strings.Join(amb.Matches, ",") != strings.Join(c.amb, ",") {
				t.Errorf("MatchName(%q) candidates = %v, want %v", c.in, amb.Matches, c.amb)
			}
			for _, m := range c.amb {
				if !strings.Contains(err.Error(), m) {
					t.Errorf("message %q does not list %q", err, m)
				}
			}
		case c.unknown:
			var unk *UnknownNameError
			if !errors.As(err, &unk) {
				t.Errorf("MatchName(%q) err = %v, want unknown", c.in, err)
			}
		default:
			if err != nil || got != c.want {
				t.Errorf("MatchName(%q) = %q, %v; want %q", c.in, got, err, c.want)
			}
		}
	}
}

func TestDefaultCannotBeCreatedRenamedOrRemoved(t *testing.T) {
	s := NewStore()
	if err := s.AddAccount(Account{Tool: ToolClaude, Name: "Default"}); !errors.Is(err, ErrReservedName) {
		t.Fatalf("add default: %v", err)
	}
	if err := s.AddAccount(Account{Tool: ToolClaude, Name: "work", Dir: `C:\a\work`}); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameAccount(ToolClaude, "default", "x"); !errors.Is(err, ErrReservedName) {
		t.Fatalf("rename default: %v", err)
	}
	if err := s.RenameAccount(ToolClaude, "work", "DEFAULT"); !errors.Is(err, ErrReservedName) {
		t.Fatalf("rename to default: %v", err)
	}
	if _, err := s.RemoveAccount(ToolClaude, "default"); !errors.Is(err, ErrReservedName) {
		t.Fatalf("remove default: %v", err)
	}
	if err := s.AddAccount(Account{Tool: ToolClaude, Name: "WORK"}); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("case-insensitive clash: %v", err)
	}
	// The same name is fine for another tool.
	if err := s.AddAccount(Account{Tool: ToolVercel, Name: "work"}); err != nil {
		t.Fatalf("same name, other tool: %v", err)
	}
}
