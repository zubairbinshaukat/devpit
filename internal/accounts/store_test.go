package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// planExample is the accounts.toml from plan section 3.3.
const planExample = `version = 1

[[account]]
tool = "claude"
name = "work"
email = "zubair@work.com"
dir = 'C:\Users\OMEN\.devpit\accounts\claude\work'

[everywhere]
claude = "default"

[[rule]]
folder = 'C:\Work'
claude = "work"
github = "work"
`

func TestThePlansExampleParses(t *testing.T) {
	s, err := Parse([]byte(planExample))
	must(t, err)
	if len(s.Accounts) != 1 || s.Accounts[0].Dir != `C:\Users\OMEN\.devpit\accounts\claude\work` {
		t.Fatalf("accounts = %+v", s.Accounts)
	}
	if len(s.Rules) != 1 || s.Rules[0].Accounts[ToolClaude] != "work" || s.Rules[0].Accounts[ToolGitHub] != "work" {
		t.Fatalf("rules = %+v", s.Rules)
	}
	// github = "work" names an account that is not listed: not an error in
	// the file, a problem the resolver reports.
	r, _ := Resolve(s, ToolGitHub, `C:\Work`, ResolveOptions{})
	if !r.IsDefault() || len(r.Problems) != 1 {
		t.Fatalf("got %v %+v", r, r.Problems)
	}
}

func TestEncodeParseRoundTrip(t *testing.T) {
	s := fixture(t)
	s.Accounts[0].Added = time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC)
	s.Accounts[1].ImportedFrom = "claude-acc"
	s.Accounts[1].Org = "Zubair's Org"
	must(t, s.SetDefaultEmail(ToolClaude, "zubair@gmail.com"))
	must(t, s.SetRule(`C:\It's here`, ToolClaude, "work"))
	must(t, s.SetEverywhere(ToolClaude, "personal"))
	data, err := Encode(s)
	must(t, err)
	back, err := Parse(data)
	must(t, err)
	again, err := Encode(back)
	must(t, err)
	if string(again) != string(data) {
		t.Fatalf("round trip changed the file:\n%s\n---\n%s", data, again)
	}
	if w, _ := back.Account(ToolClaude, "work"); !w.Added.Equal(s.Accounts[0].Added) {
		t.Fatalf("added = %v", w.Added)
	}
	text := string(data)
	for _, want := range []string{"folder = 'C:\\Work'", `folder = "C:\\It's here"`, `claude = "work"`, "[everywhere]", "imported_from = \"claude-acc\"", "[default_email]", `org = "Zubair's Org"`} {
		if !strings.Contains(text, want) {
			t.Errorf("encoded file lacks %q:\n%s", want, text)
		}
	}
	if StoreHash(back) != StoreHash(s) {
		t.Fatal("hash differs after a round trip")
	}
}

func TestNoTokenCanBeStored(t *testing.T) {
	tokens := []string{
		"ghp_" + strings.Repeat("A1b2", 9),
		"sk-ant-oat01-" + strings.Repeat("x9Y", 20),
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"github_pat_" + strings.Repeat("Ab12", 10),
		"sbp_" + strings.Repeat("0a", 20),
	}
	for _, tok := range tokens {
		for _, a := range []Account{
			{Tool: ToolClaude, Name: "x", Email: tok + "@x.com"},
			{Tool: ToolClaude, Name: "x", Label: tok},
			{Tool: ToolClaude, Name: "x", Dir: `C:\a\` + tok},
		} {
			if err := NewStore().AddAccount(a); err == nil {
				t.Errorf("an account holding %q was accepted: %+v", tok[:8], a)
			}
		}
		s := NewStore()
		s.Rules = []Rule{{Folder: `C:\` + tok, Accounts: map[Tool]string{ToolClaude: "default"}}}
		if _, err := Encode(s); err == nil {
			t.Errorf("a rule folder holding %q was encoded", tok[:8])
		}
	}
	// No free-form key can be added by hand either.
	for _, extra := range []string{
		"token = \"x\"\n",
		"[[account]]\ntool = \"claude\"\nname = \"w\"\ntoken = \"abc\"\n",
		"[everywhere]\nclaude = \"default\"\nsecret = \"abc\"\n",
	} {
		_, err := Parse([]byte("version = 1\n" + extra))
		var inv *InvalidError
		if !errors.As(err, &inv) {
			t.Errorf("an unknown key was not refused: %q → %v", extra, err)
		}
	}
	// A rule key that is not a tool is refused.
	if _, err := Parse([]byte("version = 1\n[[rule]]\nfolder = 'C:\\x'\ntoken = \"abc\"\n")); err == nil {
		t.Error("a rule with an unknown key was accepted")
	}
}

func TestValidationProblems(t *testing.T) {
	bad := []string{
		"[[account]]\ntool = \"nope\"\nname = \"w\"\n",
		"[[account]]\ntool = \"claude\"\nname = \"default\"\n",
		"[[account]]\ntool = \"claude\"\nname = \"bad name\"\n",
		"[[account]]\ntool = \"claude\"\nname = \"w\"\n[[account]]\ntool = \"claude\"\nname = \"W\"\n",
		"[[account]]\ntool = \"claude\"\nname = \"w\"\nemail = \"not an email\"\n",
		"[[account]]\ntool = \"claude\"\nname = \"w\"\ndir = 'relative'\n",
		"[[rule]]\nfolder = 'C:\\x'\nclaude = \"a b\"\n",
		"[[rule]]\nfolder = 'C:\\x'\nclaude = \"a\"\n[[rule]]\nfolder = 'c:\\X\\'\nclaude = \"b\"\n",
		"[[rule]]\nclaude = \"a\"\n",
		"[everywhere]\nnope = \"a\"\n",
	}
	for _, b := range bad {
		if _, err := Parse([]byte("version = 1\n" + b)); err == nil {
			t.Errorf("accepted:\n%s", b)
		}
	}
}

func TestNewerSchemaIsRefusedNotDowngraded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StoreFileName)
	must(t, os.WriteFile(path, []byte("version = 2\n[[account]]\ntool = \"claude\"\nname = \"w\"\nfuture = 1\n"), 0o600))
	_, err := LoadFile(path)
	if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("a newer file must be left where it is")
	}
	if _, err := ReadFile(path); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("ReadFile err = %v", err)
	}
}

func TestCorruptFileIsQuarantinedAndReportedOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StoreFileName)
	must(t, os.WriteFile(path, []byte("this is [not toml"), 0o600))

	// The shim's read never moves anything.
	if _, err := ReadFile(path); err == nil {
		t.Fatal("ReadFile accepted a broken file")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("ReadFile moved the file")
	}

	res, err := LoadFile(path)
	must(t, err)
	if res.Warning == "" || !res.Created || len(res.Store.Accounts) != 0 {
		t.Fatalf("res = %+v", res)
	}
	matches, _ := filepath.Glob(path + ".broken-*")
	if len(matches) != 1 {
		t.Fatalf("quarantined files = %v", matches)
	}
	res, err = LoadFile(path)
	must(t, err)
	if res.Warning != "" {
		t.Fatal("the warning was shown twice")
	}
}

func TestInvalidFileIsLeftAloneAndReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StoreFileName)
	must(t, os.WriteFile(path, []byte("version = 1\nsecret = \"x\"\n"), 0o600))
	_, err := LoadFile(path)
	var inv *InvalidError
	if !errors.As(err, &inv) || inv.Path != path || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("an invalid file must not be moved")
	}
}

func TestSaveIsAtomicAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StoreFileName)
	must(t, SaveFile(path, fixture(t)))
	must(t, SaveFile(path, fixture(t)))
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("files = %v", entries)
	}
	s, err := ReadFile(path)
	must(t, err)
	if len(s.Rules) != 5 {
		t.Fatalf("rules = %d", len(s.Rules))
	}
	if s2, err := ReadFile(filepath.Join(dir, "missing.toml")); err != nil || len(s2.Accounts) != 0 {
		t.Fatalf("a missing file is an empty store: %v", err)
	}
}

func TestRenameAndRemoveFollowRules(t *testing.T) {
	s := fixture(t)
	must(t, s.SetEverywhere(ToolClaude, "oss"))
	must(t, s.RenameAccount(ToolClaude, "oss", "opensource"))
	if s.Everywhere[ToolClaude] != "opensource" || len(s.RulesUsing(ToolClaude, "opensource")) != 2 {
		t.Fatalf("after rename: %+v %+v", s.Everywhere, s.Rules)
	}
	used, err := s.RemoveAccount(ToolClaude, "OPENSOURCE")
	must(t, err)
	if len(used) != 2 {
		t.Fatalf("used = %+v", used)
	}
	if _, ok := s.Everywhere[ToolClaude]; ok {
		t.Fatal("everywhere still names the removed account")
	}
	if len(s.RulesUsing(ToolClaude, "opensource")) != 0 {
		t.Fatal("rules still name the removed account")
	}
	if _, ok := s.Rule(`\\nas\projects`); ok {
		t.Fatal("a rule left with no tools should be removed")
	}
	if r, ok := s.Rule(`C:\Work`); !ok || r.Accounts[ToolGitHub] != "work" {
		t.Fatal("other tools' rules must stay")
	}
}

func TestManages(t *testing.T) {
	s := NewStore()
	if s.Manages(ToolClaude) {
		t.Fatal("empty store manages nothing")
	}
	must(t, s.SetRule(`C:\x`, ToolVercel, "default"))
	if s.Manages(ToolClaude) || !s.Manages(ToolVercel) {
		t.Fatal("a rule makes Devpit manage that tool only")
	}
	must(t, s.SetDefaultEmail(ToolClaude, "z@x.com"))
	if s.Manages(ToolClaude) {
		t.Fatal("a recorded default email is not management")
	}
	if err := s.SetDefaultEmail(ToolClaude, "ghp_"+strings.Repeat("A1", 18)+"@x.com"); err == nil {
		t.Fatal("a token was accepted as an email")
	}
}

func TestNeedsShim(t *testing.T) {
	s := NewStore()
	if s.NeedsShim(ToolClaude) {
		t.Fatal("nothing set: no shim")
	}
	must(t, s.AddAccount(Account{Tool: ToolClaude, Name: "work", Dir: `C:\a`}))
	if !s.NeedsShim(ToolClaude) || s.NeedsShim(ToolVercel) {
		t.Fatal("a second account needs a shim, for that tool only")
	}
	must(t, s.SetRule(`C:\x`, ToolGit, "default"))
	if s.NeedsShim(ToolGit) {
		t.Fatal("git is never shimmed")
	}
	must(t, s.SetRule(`C:\x`, ToolVercel, "default"))
	if !s.NeedsShim(ToolVercel) {
		t.Fatal("a first rule needs a shim")
	}
}

func TestToolNames(t *testing.T) {
	for in, want := range map[string]Tool{"gh": ToolGitHub, "GitHub": ToolGitHub, "wrangler": ToolCloudflare, "claude.exe": ToolClaude} {
		if got, err := ParseTool(in); err != nil || got != want {
			t.Errorf("ParseTool(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseTool("npm"); err == nil {
		t.Error("ParseTool(npm) accepted")
	}
	if tool, ok := ToolForShim("GH.EXE"); !ok || tool != ToolGitHub {
		t.Error("ToolForShim(gh.exe)")
	}
	if _, ok := ToolForShim("git.exe"); ok {
		t.Error("git has no shim")
	}
	if _, ok := ToolForShim("convex"); ok {
		t.Error("convex has no shim")
	}
}

func TestStaleRules(t *testing.T) {
	s := NewStore()
	for _, f := range []string{`C:\Here`, `C:\Gone`, `E:\Usb\proj`, `\\nas\off\x`, `C:\IsAFile`} {
		must(t, s.SetRule(f, ToolClaude, "default"))
	}
	exists := map[string]bool{`C:\`: true, `C:\Here`: true, `C:\IsAFile`: true}
	stat := func(p string) (os.FileInfo, error) {
		if exists[p] {
			return fakeInfo{dir: p != `C:\IsAFile`}, nil
		}
		return nil, os.ErrNotExist
	}
	got := map[string]StaleKind{}
	for _, r := range StaleRules(s, stat) {
		got[r.Rule.Folder] = r.Kind
		if r.Message == "" {
			t.Errorf("no message for %s", r.Rule.Folder)
		}
	}
	want := map[string]StaleKind{
		`C:\Gone`: StaleFolderNotFound, `C:\IsAFile`: StaleFolderNotFound,
		`E:\Usb\proj`: StaleDriveNotConnected, `\\nas\off\x`: StaleDriveNotConnected,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	if len(s.Rules) != 5 {
		t.Fatal("stale rules must never be removed by the check")
	}
}

type fakeInfo struct{ dir bool }

func (f fakeInfo) Name() string       { return "x" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() os.FileMode  { return map[bool]os.FileMode{true: os.ModeDir, false: 0}[f.dir] }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.dir }
func (f fakeInfo) Sys() any           { return nil }

func TestScrub(t *testing.T) {
	hidden := []string{
		"token: ghp_" + strings.Repeat("A1b2", 9),
		`{"accessToken": "sk-ant-oat01-abcDEF123456789_xyz-0987654321abcdefABCDEF"}`,
		"Authorization: Bearer abcdef123456",
		"password=hunter22",
		"refresh 1//0gAbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_AbCdEfGhIjKl",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJl",
		"sbp_" + strings.Repeat("0a", 20),
		"AKIAABCDEFGHIJKLMNOP",
	}
	for _, s := range hidden {
		if !LooksSecret(s) || !strings.Contains(Scrub(s), Hidden) {
			t.Errorf("not hidden: %q → %q", s, Scrub(s))
		}
	}
	kept := []string{
		"zubair@work.com",
		`C:\Users\OMEN\.devpit\accounts\claude\work`,
		"Not logged in. Run claude auth login to authenticate.",
		"Two folders with the same login can sign each other out when tokens refresh.",
		"In C:\\Work and every folder inside it, Claude Code will use work (zubair@work.com).",
		"2.1.287 (Claude Code)",
		"https://claude.ai/login",
		"Password is required",
		"12345+zubairbinshaukat@users.noreply.github.com",
	}
	for _, s := range kept {
		if LooksSecret(s) {
			t.Errorf("hidden but harmless: %q → %q", s, Scrub(s))
		}
	}
}

func TestIdentityCannotHoldAToken(t *testing.T) {
	tok := "ghp_" + strings.Repeat("Z9y8", 9)
	id := NewIdentity(IdentityFields{State: StateSignedIn, Email: "a@b.com", Org: tok, Note: "token=" + tok})
	all := id.Fields()
	for _, v := range []string{all.Email, all.Org, all.Note, all.Login} {
		if strings.Contains(v, tok) {
			t.Fatalf("identity kept the token: %+v", all)
		}
	}
	data, err := id.MarshalJSON()
	must(t, err)
	if strings.Contains(string(data), tok) || !strings.Contains(string(data), `"state":"signed in"`) {
		t.Fatalf("json = %s", data)
	}
	if (Identity{}).State() != StateUnknown {
		t.Fatal("zero identity state")
	}
}

func TestProtectedRootsListsAccountFolders(t *testing.T) {
	cfg, acc := t.TempDir(), t.TempDir()
	t.Setenv("DEVPIT_CONFIG_DIR", cfg)
	t.Setenv(EnvAccountsDir, acc)
	s := NewStore()
	must(t, s.AddAccount(Account{Tool: ToolClaude, Name: "imported", Dir: `D:\Old\claude-acc\work`}))
	must(t, SaveFile(filepath.Join(cfg, StoreFileName), s))
	roots := ProtectedRoots()
	if len(roots) != 3 || roots[0] != acc || roots[1] != filepath.Join(filepath.Dir(acc), "git") || roots[2] != `D:\Old\claude-acc\work` {
		t.Fatalf("roots = %v", roots)
	}
}
