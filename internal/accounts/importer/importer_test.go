package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
)

// A planted token goes into every login file and next to every value Devpit
// reads; it must never come out.
const planted = "sk-ant-oat01-PLANTEDtoken0123456789abcdefABCDEF0123456789"

// tmp is a temporary folder with a short name (see the adapters' tests:
// t.TempDir's long names look like tokens to accounts.Scrub).
func tmp(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dpi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// claudeAccFixture builds a ~/.claude-switch the way claude-acc writes it,
// with every edge case in the links file.
func claudeAccFixture(t *testing.T, links string) (home string) {
	t.Helper()
	home = tmp(t)
	sw := filepath.Join(home, ".claude-switch")
	write(t, filepath.Join(sw, "config"), "resume_hook=off\r\ndefault= work \r\n")
	write(t, filepath.Join(sw, "links"), links)
	write(t, filepath.Join(sw, "bin", "claude-acc.exe"), "MZ")
	work := filepath.Join(sw, "accounts", "work")
	write(t, filepath.Join(work, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"`+planted+`"}}`)
	write(t, filepath.Join(work, ".account-info.json"), `{"email":"zubair@work.com","uuid":"u-1","org":"Work Org","fetched_at":1,"token_hash":"PLANTEDhash01234","plan":"max"}`)
	write(t, filepath.Join(sw, "accounts", "my_oss", "settings.json"), "{}")
	write(t, filepath.Join(sw, "accounts", "Default", "settings.json"), "{}")
	write(t, filepath.Join(sw, "default.account-info.json"), `{"email":"zubair@gmail.com","token_hash":"PLANTEDhash99999"}`)
	return home
}

const fixtureLinks = "Q:\\Work\\api=work\r\n" +
	"\r\n" +
	"Q:\\Odd Name=x\\proj  =  my_oss  \r\n" + // a folder with '=' and spaces, trailing spaces
	"q:\\work\\API=my_oss\r\n" + // a duplicate of line 1 (other case): line 1 wins
	"Q:\\Gone=nobody\r\n" + // an account claude-acc does not have
	"Q:\\Plain=default\n" + // ~/.claude
	"relative\\path=work\n" + // not a full path
	"no equals sign here\n" +
	"=work\n" +
	"Q:\\Default=Default\n"

func TestParseLinksEdgeCases(t *testing.T) {
	links := parseLinks(fixtureLinks, map[string]bool{"work": true, "my_oss": true, "Default": true})
	type want struct {
		line    int
		folder  string
		account string
		usable  bool
	}
	wants := []want{
		{1, `Q:\Work\api`, "work", true},
		{3, `Q:\Odd Name=x\proj`, "my_oss", true},
		{4, `q:\work\API`, "my_oss", false},
		{5, `Q:\Gone`, "nobody", false},
		{6, `Q:\Plain`, "default", true},
		{7, `relative\path`, "work", false},
		{10, `Q:\Default`, "Default", true},
	}
	if len(links) != len(wants) {
		t.Fatalf("links = %+v", links)
	}
	for i, w := range wants {
		l := links[i]
		if l.Line != w.line || l.Folder != w.folder || l.Account != w.account || l.Usable() != w.usable {
			t.Errorf("link %d = %+v, want %+v", i, l, w)
		}
	}
	if !strings.Contains(links[2].Problem, "line 1") || !strings.Contains(links[3].Problem, "nobody") {
		t.Fatalf("problems = %q, %q", links[2].Problem, links[3].Problem)
	}
	if !links[0].FolderMissing {
		t.Fatal("Q:\\Work\\api does not exist and must be flagged")
	}
	if got := parseConfigDefault("resume_hook=off\ndefault=\n"); got != "" {
		t.Fatalf("empty default = %q", got)
	}
	if got := parseConfigDefault("default = second \r\n"); got != "second" {
		t.Fatalf("default = %q", got)
	}
}

type fakeGH struct {
	adapters.Adapter
	list []accounts.Account
	err  error
}

func (f fakeGH) Accounts(context.Context, *accounts.Store) ([]accounts.Account, error) {
	return f.list, f.err
}

func TestDetectFindsEverythingAndReadsNoLoginFile(t *testing.T) {
	home := claudeAccFixture(t, fixtureLinks)
	// Other Claude config folders.
	write(t, filepath.Join(home, ".claude", ".credentials.json"), planted)
	write(t, filepath.Join(home, ".claude-personal", ".credentials.json"), planted)
	write(t, filepath.Join(home, ".claude-mem", "data.db"), "x") // not a config folder
	write(t, filepath.Join(home, ".claude-x.lock", ".credentials.json"), planted)
	envDir := filepath.Join(tmp(t), "cfg-client")
	write(t, filepath.Join(envDir, "settings.json"), "{}")
	// A profile with claude-acc's line among lines that must never be kept.
	prof := filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	write(t, prof, "$env:OPENAI_API_KEY = '"+planted+"'\r\n\r\n# Claude Code Account Switcher\r\n"+
		"Invoke-Expression ((& 'C:\\Users\\you\\.claude-switch\\bin\\claude-acc.exe' init pwsh) -join \"`n\")\r\n"+
		"# claude-acc init pwsh runs on shell startup\r\n")

	env := map[string]string{"CLAUDE_CONFIG_DIR": envDir}
	d := Deps{
		Home: home, Getenv: func(k string) string { return env[k] },
		ProfileFiles: []string{prof, filepath.Join(home, "missing.ps1")},
		GitHub: fakeGH{list: []accounts.Account{
			{Tool: accounts.ToolGitHub, Name: "default"},
			{Tool: accounts.ToolGitHub, Name: "zubair-work", Label: "zubair-work", ImportedFrom: "detected"},
		}},
	}
	f, err := Detect(context.Background(), d, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := f.ClaudeAcc
	if c == nil || c.Default != "work" || c.DefaultEmail != "zubair@gmail.com" || len(c.Accounts) != 3 || c.Program == "" {
		t.Fatalf("claude-acc = %+v", c)
	}
	var work AccAccount
	for _, a := range c.Accounts {
		if a.Name == "work" {
			work = a
		}
	}
	if work.Email != "zubair@work.com" || work.Org != "Work Org" || work.Plan != "max" || !work.SignedIn {
		t.Fatalf("work = %+v", work)
	}
	if len(c.ProfileLines) != 1 || c.ProfileLines[0].Line != 4 || c.ProfileLines[0].HeaderLine != 3 || !strings.Contains(c.ProfileLines[0].Text, "init pwsh") {
		t.Fatalf("profile lines = %+v", c.ProfileLines)
	}
	if len(f.ConfigDirs) != 2 || !f.ConfigDirs[0].FromEnv || f.ConfigDirs[0].Suggested != "cfg-client" ||
		f.ConfigDirs[1].Suggested != "personal" || !f.ConfigDirs[1].SignedIn {
		t.Fatalf("config dirs = %+v", f.ConfigDirs)
	}
	if len(f.GitHub) != 1 || f.GitHub[0].Label != "zubair-work" {
		t.Fatalf("gh = %+v", f.GitHub)
	}
	if got := f.Summary(); got != "Found 3 Claude accounts and 4 folder links from claude-acc, 2 other Claude Code folders, plus 1 more GitHub account in gh." {
		t.Fatalf("summary = %q", got)
	}
	if s := fmt.Sprintf("%+v %+v", f, *c); strings.Contains(s, "PLANTED") {
		t.Fatalf("a login file or a token hash was read: %s", s)
	}
}

func TestDetectReportsUnreadableFiles(t *testing.T) {
	home := tmp(t)
	sw := filepath.Join(home, ".claude-switch")
	// A folder where a file should be cannot be read as one.
	_ = os.MkdirAll(filepath.Join(sw, "config"), 0o700)
	_ = os.MkdirAll(filepath.Join(sw, "links"), 0o700)
	f, err := Detect(context.Background(), Deps{Home: home, Getenv: func(string) string { return "" }, ProfileFiles: []string{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.ClaudeAcc == nil || len(f.Problems) != 2 || !f.Empty() {
		t.Fatalf("found = %+v", f)
	}
	// No claude-acc at all.
	f, _ = Detect(context.Background(), Deps{Home: tmp(t), Getenv: func(string) string { return "" }}, nil)
	if f.ClaudeAcc != nil || !f.Empty() || f.Summary() != "Found no accounts to bring over." {
		t.Fatalf("empty = %+v", f)
	}
}

func TestImportPlanApplyIsOneUndoableChangeAndIdempotent(t *testing.T) {
	home := claudeAccFixture(t, fixtureLinks)
	d := Deps{Home: home, Getenv: func(string) string { return "" }, ProfileFiles: []string{}}
	eng := accounts.NewEngine(accounts.PathsIn(tmp(t), tmp(t)))
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	f, err := Detect(context.Background(), d, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := eng.Load()
	p, err := PlanImport(res.Store, f, Options{ClaudeAcc: true, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if p.NoChange || len(p.Accounts) != 3 || p.Everywhere != "work" || p.DefaultEmail != "zubair@gmail.com" {
		t.Fatalf("plan = %+v", p)
	}
	names := map[string]accounts.Account{}
	for _, a := range p.Accounts {
		names[a.Name] = a
		if a.ImportedFrom != FromClaudeAcc || !strings.HasPrefix(a.Dir, filepath.Join(home, ".claude-switch", "accounts")) {
			t.Fatalf("account = %+v: it must stay where it is", a)
		}
	}
	if names["work"].Email != "zubair@work.com" || names["work"].Org != "Work Org" {
		t.Fatalf("work = %+v", names["work"])
	}
	// claude-acc's "my_oss" and "Default" are not Devpit names.
	if _, ok := names["my-oss"]; !ok || p.Renamed["my_oss"] != "my-oss" || p.Renamed["Default"] != "Default-2" {
		t.Fatalf("renamed = %+v names %v", p.Renamed, names)
	}
	rules := map[string]string{}
	for _, r := range p.Rules {
		rules[r.Folder] = r.Account
		if !r.FolderMissing {
			t.Fatalf("rule %+v: Q: folders do not exist and must be flagged", r)
		}
	}
	if len(rules) != 4 || rules[`Q:\Work\api`] != "work" || rules[`Q:\Odd Name=x\proj`] != "my-oss" || rules[`Q:\Plain`] != "default" || rules[`Q:\Default`] != "Default-2" {
		t.Fatalf("rules = %+v", rules)
	}
	notes := strings.Join(p.Notes, "\n")
	for _, want := range []string{"line 4", `"nobody"`, "line 7", "folder not found"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
	if !strings.Contains(strings.Join(p.Lines(), "\n"), "nobody signs in again") {
		t.Fatalf("lines = %q", p.Lines())
	}

	// A change made after the preview makes it stale.
	if _, err = eng.Edit("other", func(s *accounts.Store) error { return s.SetDefaultEmail(accounts.ToolGit, "a@b.c") }); err != nil {
		t.Fatal(err)
	}
	if _, err = ApplyImport(eng, p); !errors.Is(err, accounts.ErrStalePreview) {
		t.Fatalf("stale: %v", err)
	}
	if _, err = eng.Undo(); err != nil {
		t.Fatal(err)
	}

	entry, err := ApplyImport(eng, p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(entry.Summary, "3 accounts") {
		t.Fatalf("entry = %+v", entry.Summary)
	}
	res, _ = eng.Load()
	r, _ := accounts.Resolve(res.Store, accounts.ToolClaude, `Q:\Work\api\src`, accounts.ResolveOptions{})
	if r.Account.Name != "work" || r.Account.Dir != filepath.Join(home, ".claude-switch", "accounts", "work") {
		t.Fatalf("resolved %v", r)
	}

	// Twice changes nothing.
	f2, _ := Detect(context.Background(), d, res.Store)
	p2, err := PlanImport(res.Store, f2, Options{ClaudeAcc: true, Now: now})
	if err != nil || !p2.NoChange || len(p2.Accounts) != 0 || len(p2.Rules) != 0 {
		t.Fatalf("second plan = %+v %v", p2, err)
	}
	if _, err := ApplyImport(eng, p2); err == nil {
		t.Fatal("a no-op import must not be journalled")
	}

	// One undo takes all of it back, and touches no folder.
	if _, err := eng.Undo(); err != nil {
		t.Fatal(err)
	}
	res, _ = eng.Load()
	if len(res.Store.Accounts) != 0 || len(res.Store.Rules) != 0 || len(res.Store.Everywhere) != 0 {
		t.Fatalf("after undo = %+v", res.Store)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude-switch", "accounts", "work", ".credentials.json")); err != nil {
		t.Fatal("undo touched an account folder")
	}
}

func TestImportKeepsDevpitsOwnChoices(t *testing.T) {
	home := claudeAccFixture(t, "Q:\\Work\\api=work\n")
	d := Deps{Home: home, Getenv: func(string) string { return "" }, ProfileFiles: []string{}}
	s := accounts.NewStore()
	_ = s.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "work", Dir: `Q:\elsewhere\work`})
	_ = s.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "mine", Dir: `Q:\mine`})
	_ = s.SetRule(`Q:\Work\api`, accounts.ToolClaude, "mine")
	_ = s.SetEverywhere(accounts.ToolClaude, "mine")
	_ = s.SetDefaultEmail(accounts.ToolClaude, "me@x.com")
	f, _ := Detect(context.Background(), d, s)
	p, err := PlanImport(s, f, Options{ClaudeAcc: true})
	if err != nil {
		t.Fatal(err)
	}
	// "work" is taken by another folder: the import gets "work-2".
	if p.Renamed["work"] != "work-2" || len(p.Rules) != 0 || p.Everywhere != "" || p.DefaultEmail != "" {
		t.Fatalf("plan = %+v", p)
	}
	notes := strings.Join(p.Notes, "\n")
	if !strings.Contains(notes, "Kept Devpit's own rule") || !strings.Contains(notes, "Kept Devpit's own choice for everywhere") {
		t.Fatalf("notes = %s", notes)
	}
}

func TestImportConfigDirsAndGitHub(t *testing.T) {
	home := tmp(t)
	write(t, filepath.Join(home, ".claude-work", ".credentials.json"), planted)
	d := Deps{Home: home, Getenv: func(string) string { return "" }, GitHub: fakeGH{list: []accounts.Account{
		{Tool: accounts.ToolGitHub, Name: "zubair", Label: "zubair", ImportedFrom: "detected"},
	}}}
	s := accounts.NewStore()
	_ = s.AddAccount(accounts.Account{Tool: accounts.ToolGitHub, Name: "zubair", Label: "someone-else"})
	f, err := Detect(context.Background(), d, s)
	if err != nil {
		t.Fatal(err)
	}
	p, err := PlanImport(s, f, Options{ConfigDirs: []string{filepath.Join(home, ".claude-work")}, GitHub: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Accounts) != 2 || p.Accounts[0].Name != "work" || p.Accounts[0].ImportedFrom != FromDetected ||
		p.Accounts[1].Tool != accounts.ToolGitHub || p.Accounts[1].Name != "zubair-2" || p.Accounts[1].Label != "zubair" {
		t.Fatalf("accounts = %+v", p.Accounts)
	}
	if _, err := PlanImport(s, f, Options{ConfigDirs: []string{`Q:\not-found`}}); err == nil {
		t.Fatal("a folder that was not found must be refused")
	}
	if strings.Contains(fmt.Sprintf("%+v", p), "PLANTED") {
		t.Fatal("a login file was read")
	}
}

func TestRemovalGuide(t *testing.T) {
	home := claudeAccFixture(t, "")
	prof := filepath.Join(home, "p.ps1")
	write(t, prof, "Invoke-Expression (& 'C:\\x\\claude-acc.exe' init pwsh)\n")
	env := map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude-switch", "accounts", "work")}
	f, _ := Detect(context.Background(), Deps{Home: home, Getenv: func(k string) string { return env[k] }, ProfileFiles: []string{prof}}, nil)
	steps := RemovalGuide(f)
	if len(steps) != 5 {
		t.Fatalf("steps = %+v", steps)
	}
	all := ""
	for _, s := range steps {
		all += s.Title + "\n" + strings.Join(s.Detail, "\n") + "\n"
	}
	for _, want := range []string{"devpit accounts verify", prof + ", line 1:", "init pwsh", "CLAUDE_CONFIG_DIR=", "claude-acc.exe", "claude-acc remove", "Recycle Bin", "--purge", "does not edit your profile"} {
		if !strings.Contains(all, want) {
			t.Errorf("guide lacks %q:\n%s", want, all)
		}
	}
	if RemovalGuide(Found{}) != nil {
		t.Fatal("no claude-acc, no guide")
	}
}
