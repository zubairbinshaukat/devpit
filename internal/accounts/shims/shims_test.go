package shims

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

func getenv(m map[string]string) func(string) string {
	return func(k string) string { return m[strings.ToUpper(k)] }
}

func TestPrependAndRemoveKeepEveryOtherEntryExactly(t *testing.T) {
	env := getenv(map[string]string{"LOCALAPPDATA": `C:\Users\z\AppData\Local`})
	dir := `C:\Users\z\AppData\Local\Programs\devpit\shims`
	cases := []struct {
		in, prepended, removed string
		changed                bool
	}{
		{"", dir, "", true},
		{`%USERPROFILE%\bin;C:\x;;D:\y;`, dir + `;%USERPROFILE%\bin;C:\x;;D:\y;`, `%USERPROFILE%\bin;C:\x;;D:\y;`, true},
		{dir + `;C:\x`, dir + `;C:\x`, `C:\x`, false},
		{`C:\x;` + strings.ToUpper(dir) + `\;D:\y`, dir + `;C:\x;D:\y`, `C:\x;D:\y`, true},
		{`%LOCALAPPDATA%\Programs\devpit\shims;C:\x`, `%LOCALAPPDATA%\Programs\devpit\shims;C:\x`, `C:\x`, false},
		{`"` + dir + `";C:\x;` + dir, `"` + dir + `";C:\x`, `C:\x`, true},
	}
	for _, c := range cases {
		got, changed := PrependEntry(c.in, dir, env)
		if got != c.prepended || changed != c.changed {
			t.Errorf("PrependEntry(%q) = %q, %v; want %q, %v", c.in, got, changed, c.prepended, c.changed)
		}
		again, changedAgain := PrependEntry(got, dir, env)
		if again != got || changedAgain {
			t.Errorf("PrependEntry is not idempotent on %q", got)
		}
		if rem, _ := RemoveEntry(got, dir, env); rem != c.removed {
			t.Errorf("RemoveEntry(%q) = %q; want %q", got, rem, c.removed)
		}
	}
	if !HasEntry(`a;%LOCALAPPDATA%\Programs\devpit\shims\`, dir, env) || HasEntry(`C:\Users\z\AppData\Local\Programs\devpit`, dir, env) {
		t.Error("HasEntry")
	}
}

type fakePath struct {
	value  string
	expand bool
	exists bool
	writes int
}

func (f *fakePath) Read() (string, bool, bool, error) { return f.value, f.expand, f.exists, nil }
func (f *fakePath) Write(v string, expand bool) error {
	f.value, f.expand, f.exists = v, expand, true
	f.writes++
	return nil
}

func newManager(t *testing.T) (*Manager, *fakePath, map[string]string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "install", SourceName)
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("shim v1"), 0o700); err != nil { //nolint:gosec // a fake executable
		t.Fatal(err)
	}
	p := &fakePath{value: `%USERPROFILE%\bin;C:\tools`, expand: true, exists: true}
	env := map[string]string{"USERPROFILE": `C:\Users\z`, "PATHEXT": ".EXE;.CMD"}
	broadcasts := 0
	m := &Manager{
		Dir:       filepath.Join(root, "shims"),
		Source:    src,
		Record:    filepath.Join(root, "config", RecordName),
		Path:      p,
		Broadcast: func() { broadcasts++ },
		Getenv:    getenv(env),
	}
	return m, p, env
}

func TestEnsureCopiesOnlyWhenNeededAndRefreshesStaleShims(t *testing.T) {
	m, _, _ := newManager(t)
	created, err := m.Ensure(accounts.ToolClaude)
	if err != nil || !created {
		t.Fatalf("first Ensure: %v %v", created, err)
	}
	if b, _ := os.ReadFile(filepath.Join(m.Dir, "claude.exe")); string(b) != "shim v1" {
		t.Fatalf("shim = %q", b)
	}
	if created, _ := m.Ensure(accounts.ToolClaude); created {
		t.Fatal("a current shim must not be rewritten")
	}
	if _, err = m.Ensure(accounts.ToolGit); err == nil {
		t.Fatal("git is never shimmed")
	}
	if _, err = m.Ensure(accounts.ToolGitHub); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(m.Dir, "gh.exe")); err != nil {
		t.Fatal("GitHub's shim is gh.exe")
	}

	// Devpit is updated: the source changes, the shims are stale.
	if err = os.WriteFile(m.Source, []byte("shim v2"), 0o700); err != nil { //nolint:gosec // a fake executable
		t.Fatal(err)
	}
	issues := m.Check([]accounts.Tool{accounts.ToolClaude})
	if !hasIssue(issues, IssueStaleShim) {
		t.Fatalf("issues = %+v", issues)
	}
	refreshed, err := m.Refresh()
	if err != nil || len(refreshed) != 2 {
		t.Fatalf("refreshed = %v %v", refreshed, err)
	}
	if b, _ := os.ReadFile(filepath.Join(m.Dir, "claude.exe")); string(b) != "shim v2" {
		t.Fatalf("shim = %q", b)
	}
}

func TestPathIsChangedOnceAndKeepsItsType(t *testing.T) {
	m, p, _ := newManager(t)
	changed, err := m.AddToPath()
	if err != nil || !changed || p.value != m.Dir+`;%USERPROFILE%\bin;C:\tools` || !p.expand {
		t.Fatalf("after add: %q expand=%v changed=%v err=%v", p.value, p.expand, changed, err)
	}
	if changed, _ := m.AddToPath(); changed || p.writes != 1 {
		t.Fatal("adding twice must not write twice")
	}
	p.expand = false // a REG_SZ PATH stays REG_SZ
	if changed, _ := m.RemoveFromPath(); !changed || p.value != `%USERPROFILE%\bin;C:\tools` || p.expand {
		t.Fatalf("after remove: %q expand=%v", p.value, p.expand)
	}
	// No PATH value at all: one is created as REG_EXPAND_SZ.
	p.exists, p.value, p.expand = false, "", false
	if _, err := m.AddToPath(); err != nil || p.value != m.Dir || !p.expand {
		t.Fatalf("new value: %q %v", p.value, p.expand)
	}
}

func TestCheckFindsEveryProblem(t *testing.T) {
	m, _, env := newManager(t)
	tools := []accounts.Tool{accounts.ToolClaude}

	issues := m.Check(tools)
	for _, k := range []IssueKind{IssueShimMissing, IssueNotOnPath, IssueNewTerminal} {
		if !hasIssue(issues, k) {
			t.Errorf("missing %q in %+v", k, issues)
		}
	}
	if _, err := m.Ensure(accounts.ToolClaude); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddToPath(); err != nil {
		t.Fatal(err)
	}

	// The real claude sits in a folder that comes before the shim folder in
	// this process's PATH (the system PATH, say).
	if runtime.GOOS == "windows" {
		realDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(realDir, "claude.exe"), []byte("real"), 0o700); err != nil { //nolint:gosec // a fake executable
			t.Fatal(err)
		}
		env["PATH"] = realDir + ";" + m.Dir
		issues = m.Check(tools)
		if len(issues) != 1 || issues[0].Kind != IssueShadowed || !strings.Contains(issues[0].Message, realDir) {
			t.Fatalf("issues = %+v", issues)
		}
		env["PATH"] = m.Dir + ";" + realDir
	} else {
		env["PATH"] = m.Dir
	}
	if issues := m.Check(tools); len(issues) != 0 {
		t.Fatalf("all fine, but: %+v", issues)
	}
	if issues := m.Check(nil); len(issues) != 0 {
		t.Fatalf("no tools need a shim, but: %+v", issues)
	}
}

func TestCleanupRemovesExactlyWhatDevpitAdded(t *testing.T) {
	m, p, _ := newManager(t)
	for _, tool := range []accounts.Tool{accounts.ToolClaude, accounts.ToolVercel} {
		if _, err := m.Ensure(tool); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.AddToPath(); err != nil {
		t.Fatal(err)
	}
	// Something else lives in the folder, and someone replaced vercel.exe.
	foreign := filepath.Join(m.Dir, "notes.txt")
	if err := os.WriteFile(foreign, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Dir, "vercel.exe"), []byte("not devpit"), 0o700); err != nil { //nolint:gosec // a fake executable
		t.Fatal(err)
	}

	rep, err := m.Cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 1 || !strings.HasSuffix(rep.Removed[0], "claude.exe") || len(rep.Kept) != 1 || !rep.PathOff {
		t.Fatalf("report = %+v", rep)
	}
	for _, keep := range []string{foreign, filepath.Join(m.Dir, "vercel.exe")} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("cleanup removed %s, which Devpit did not make", keep)
		}
	}
	if p.value != `%USERPROFILE%\bin;C:\tools` {
		t.Fatalf("PATH = %q", p.value)
	}
	if _, err := os.Stat(m.Record); !os.IsNotExist(err) {
		t.Fatal("the record should be gone")
	}
}

func TestSyncCreatesShimsOnlyForToolsThatNeedThem(t *testing.T) {
	m, p, _ := newManager(t)
	s := accounts.NewStore()
	created, changed, err := m.Sync(s)
	if err != nil || len(created) != 0 || changed || p.writes != 0 {
		t.Fatalf("empty store: %v %v %v", created, changed, err)
	}
	if err = s.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "work", Dir: `C:\a`}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRule(`C:\x`, accounts.ToolGit, "default"); err != nil {
		t.Fatal(err)
	}
	created, changed, err = m.Sync(s)
	if err != nil || len(created) != 1 || created[0] != accounts.ToolClaude || !changed {
		t.Fatalf("sync: %v %v %v", created, changed, err)
	}
	entries, _ := os.ReadDir(m.Dir)
	if len(entries) != 1 {
		t.Fatalf("shim folder = %v", entries)
	}
}

func hasIssue(issues []Issue, k IssueKind) bool {
	for _, i := range issues {
		if i.Kind == k {
			return true
		}
	}
	return false
}
