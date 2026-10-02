package adapters

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

func TestGitQuoteEscapesOrRefuses(t *testing.T) {
	for in, want := range map[string]string{
		`Zubair`:          `"Zubair"`,
		`Zu "Z" bair`:     `"Zu \"Z\" bair"`,
		`a#b;c`:           `"a#b;c"`,
		`C:\x\y`:          `"C:\\x\\y"`,
		`  spaced  `:      `"  spaced  "`,
		"Zübair 日本":       `"Zübair 日本"`,
		`trailing\`:       `"trailing\\"`,
		`"; [user] x = y`: `"\"; [user] x = y"`,
	} {
		got, err := gitQuote(in)
		if err != nil || got != want {
			t.Errorf("gitQuote(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"a\nb", "a\rb", "a\x00b", "a\tb", "x\n[user]\n\temail = evil@x", "\x7f", string([]byte{0xff})} {
		if _, err := gitQuote(bad); !errors.Is(err, errUnsafeValue) {
			t.Errorf("gitQuote(%q) was not refused: %v", bad, err)
		}
	}
}

// TestGitQuoteRoundTripsThroughRealGit proves that what Devpit writes is
// read back by Git as exactly the value, with nothing added: odd values
// cannot inject config.
func TestGitQuoteRoundTripsThroughRealGit(t *testing.T) {
	needGit(t)
	dir := t.TempDir()
	for i, v := range []string{`Zu "Z" bair`, `a#b;c`, `C:\x\y\`, `  spaced  `, "Zübair 日本", `"; [user] email = evil@x`, `\"`, `#`, `;`} {
		q, err := gitQuote(v)
		if err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(dir, "c"+string(rune('a'+i)))
		_ = os.WriteFile(f, []byte("[user]\n    name = "+q+"\n"), 0o600)
		w := &gitWorld{t: t}
		if got := w.gitOutRaw(dir, "config", "--file", f, "--get", "user.name"); got != v {
			t.Errorf("git read %q back as %q", v, got)
		}
		if got := w.gitOutRaw(dir, "config", "--file", f, "--get-all", "user.email"); got != "" {
			t.Errorf("%q injected user.email = %q", v, got)
		}
	}
}

func TestGitdirPattern(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Work`:                 "C:/Work/",
		`c:\work\`:                "C:/work/",
		`C:\`:                     "C:/",
		`D:\My Projects\ü`:        "D:/My Projects/ü/",
		`C:\Work\[old]`:           `C:/Work/\[old]/`,
		`\\server\share\proj`:     "//server/share/proj/",
		`\\server\share`:          "//server/share/",
		`"C:\Work\with space" `:   "C:/Work/with space/",
		`\\?\C:\long\path\folder`: "C:/long/path/folder/",
	} {
		got, err := gitdirPattern(in)
		if err != nil || got != want {
			t.Errorf("gitdirPattern(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := gitdirPattern(`relative\path`); err == nil {
		t.Error("a relative folder must be refused")
	}
}

func TestSSHCommandQuoting(t *testing.T) {
	got := sshCommand(`C:\Users\John Smith\.ssh\id_o'neil`)
	want := `ssh -i 'C:/Users/John Smith/.ssh/id_o'\''neil' -o IdentitiesOnly=yes`
	if got != want {
		t.Fatalf("sshCommand = %s\nwant          %s", got, want)
	}
	q, err := gitQuote(got)
	if err != nil || !strings.HasPrefix(q, `"ssh -i '`) {
		t.Fatalf("quoted = %s %v", q, err)
	}
}

func TestWinDepth(t *testing.T) {
	for in, want := range map[string]int{`C:\`: 0, `C:\Work`: 1, `C:\Work\api\x`: 3, `\\srv\share`: 0, `\\srv\share\a\b`: 2} {
		if got := winDepth(in); got != want {
			t.Errorf("winDepth(%s) = %d, want %d", in, got, want)
		}
	}
}

func TestParseHelperChain(t *testing.T) {
	devpit := filepath.Join(t.TempDir(), "git")
	rec := func(scope, file, key, val string) []string {
		return []string{scope, "file:" + file, key + "\n" + val}
	}
	var recs []string
	recs = append(recs, rec("system", "C:/git/etc/gitconfig", "credential.helper", `!"C:/git/git-credential-manager.exe"`)...)
	recs = append(recs, rec("global", "C:/u/.gitconfig", "credential.https://gitlab.com.helper", "glab")...)
	recs = append(recs, rec("global", "C:/u/.gitconfig", "credential.helper", "store")...)
	recs = append(recs, rec("global", filepath.Join(devpit, "rules.gitconfig"), "credential.https://github.com.helper", "")...)
	recs = append(recs, rec("global", filepath.Join(devpit, "rules.gitconfig"), "credential.https://github.com.helper", "!devpit")...)
	got := parseHelperChain(recs, devpit)
	if !slices.Equal(got, []string{`!"C:/git/git-credential-manager.exe"`, "store"}) {
		t.Fatalf("chain = %q", got)
	}
	// A github.com reset in the person's own config clears what came before.
	recs = append(recs, rec("global", "C:/u/.gitconfig", "credential.https://github.com.helper", "")...)
	recs = append(recs, rec("global", "C:/u/.gitconfig", "credential.https://github.com.helper", "!gh auth git-credential")...)
	if got = parseHelperChain(recs, devpit); !slices.Equal(got, []string{"!gh auth git-credential"}) {
		t.Fatalf("chain after reset = %q", got)
	}
	// A repo's own lines are not "before Devpit".
	if got = parseHelperChain(rec("local", "C:/r/.git/config", "credential.helper", "x"), devpit); len(got) != 0 {
		t.Fatalf("local = %q", got)
	}
}

func TestHelperKeyMatchesGitHub(t *testing.T) {
	yes := []string{"credential.helper", "credential.https://github.com.helper", "credential.https://GitHub.com/.helper", "credential.github.com.helper", "credential.https://github.com:443.helper"}
	no := []string{
		"credential.https://gitlab.com.helper", "credential.http://github.com.helper", "credential.https://user@github.com.helper",
		"credential.https://github.com/org.helper", "credential.https://*.github.com.helper", "credential.username", "core.helper",
	}
	for _, k := range yes {
		if !helperKeyMatchesGitHub(k) {
			t.Errorf("%s should apply to github.com", k)
		}
	}
	for _, k := range no {
		if helperKeyMatchesGitHub(k) {
			t.Errorf("%s should not apply to github.com pushes", k)
		}
	}
}

func TestCheckArgsAllowsDevpitsGitCommands(t *testing.T) {
	for _, args := range [][]string{
		{"config", "--show-origin", "--show-scope", "-z", "--get-regexp", `^credential\..*helper$`},
		{"config", "--file", `C:\Users\z\.gitconfig`, "--fixed-value", "--unset-all", "include.path", "C:/Users/z/.devpit/git/rules.gitconfig"},
		{"credential", "fill"},
	} {
		if err := CheckArgs("git", args); err != nil {
			t.Errorf("%v refused: %v", args, err)
		}
	}
}

func TestNewGitIdentityRefusesInjection(t *testing.T) {
	s := accounts.NewStore()
	if _, err := NewGitIdentity(s, "work", "Zubair\n[core]\n\tsshCommand = evil", "z@work.com", fixedNow()); err == nil {
		t.Fatal("a line break in the name must be refused")
	}
	if _, err := NewGitIdentity(s, "work", "Zubair", "z@work.com\n[x]", fixedNow()); err == nil {
		t.Fatal("a line break in the email must be refused")
	}
	if _, err := NewGitIdentity(s, "default", "Zubair", "z@work.com", fixedNow()); !errors.Is(err, accounts.ErrReservedName) {
		t.Fatalf("default: %v", err)
	}
	a, err := NewGitIdentity(s, "work", "  Zubair O'Neil  ", "z#1;x@work.com", fixedNow())
	if err != nil || a.Label != "Zubair O'Neil" || a.Email != "z#1;x@work.com" {
		t.Fatalf("%+v %v", a, err)
	}
	data, err := identityFile(a, GitSigning{Key: `C:\keys\id.pub`, Format: "ssh", Sign: true})
	if err != nil {
		t.Fatal(err)
	}
	if classifyGitFile(data) != gitFileOurs {
		t.Fatal("a fresh file must classify as Devpit's")
	}
	for _, want := range []string{`name = "Zubair O'Neil"`, `email = "z#1;x@work.com"`, `signingkey = "C:\\keys\\id.pub"`, "format = ssh", "gpgsign = true"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("identity file lacks %q:\n%s", want, data)
		}
	}
	if err := (GitSigning{Format: "pgp"}).validate(); err == nil {
		t.Error("an unknown gpg.format must be refused")
	}
}

func TestClassifyGitFile(t *testing.T) {
	data := sealGitFile([]string{gitHeader + " x", "[user]", "    email = \"a@b\""})
	if classifyGitFile(data) != gitFileOurs {
		t.Fatal("ours")
	}
	if classifyGitFile(bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))) != gitFileOurs {
		t.Fatal("CRLF from an editor that kept the content is still ours")
	}
	if classifyGitFile(append(data, []byte("[core]\n")...)) != gitFileEdited {
		t.Fatal("a line added at the end is an edit")
	}
	if classifyGitFile(bytes.Replace(data, []byte("a@b"), []byte("x@b"), 1)) != gitFileEdited {
		t.Fatal("a changed value is an edit")
	}
	if classifyGitFile([]byte("[user]\n")) != gitFileForeign {
		t.Fatal("foreign")
	}
}

func TestGitCapabilitiesAndNoJustOnce(t *testing.T) {
	w := newGitWorld(t)
	c := w.git.Capabilities(context.Background())
	if !c.FolderRules || !c.Everywhere || c.JustOnce || !c.AddAccount || !strings.Contains(c.Why, "just this once") {
		t.Fatalf("caps = %+v", c)
	}
	w.addIdentity("work", "Zubair", "zubair@work.com")
	_, err := w.git.Plan(accounts.PreviewInput{Store: w.store(), Change: accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.OnceScope()}})
	if !errors.Is(err, accounts.ErrNotSupported) {
		t.Fatalf("once: %v", err)
	}
	if _, err := w.git.Launch(accounts.Account{Tool: accounts.ToolGit, Name: "work"}); !errors.Is(err, accounts.ErrNotSupported) {
		t.Fatalf("launch: %v", err)
	}
	if l, err := w.git.Launch(accounts.Account{Tool: accounts.ToolGit, Name: "default"}); err != nil || !l.IsZero() {
		t.Fatalf("default launch: %+v %v", l, err)
	}
}

func TestGitMissingOrTooOld(t *testing.T) {
	f := &FakeRunner{Missing: map[string]bool{"git": true}}
	a := newGit(Deps{Runner: f, Home: t.TempDir()})
	if c := a.Capabilities(context.Background()); c.FolderRules || c.Why != "Git is not installed." {
		t.Fatalf("missing: %+v", c)
	}
	id, err := a.WhoAmI(context.Background(), accounts.Account{Tool: accounts.ToolGit, Name: "default"})
	if !errors.Is(err, accounts.ErrNotFoundTool) || id.State() != accounts.StateNotInstalled {
		t.Fatalf("who: %v %v", id.State(), err)
	}
	// A Git that runs but never applies the probe's includeIf is too old.
	old := &FakeRunner{Func: func(c Cmd) (Result, error) {
		if len(c.Args) > 0 && c.Args[0] == "--version" {
			return Result{Stdout: []byte("git version 2.9.0\n")}, nil
		}
		return Result{}, nil
	}}
	a = newGit(Deps{Runner: old, Home: t.TempDir()})
	if c := a.Capabilities(context.Background()); c.FolderRules || !strings.Contains(c.Why, "2.9.0") || !strings.Contains(c.Why, "2.13") {
		t.Fatalf("old: %+v", c)
	}
}

func TestGitProbeOnThisPC(t *testing.T) {
	needGit(t)
	c, err := probeGit(context.Background(), Deps{Runner: ExecRunner{}})
	if err != nil || !c.includeIf {
		t.Fatalf("probe = %+v, %v", c, err)
	}
	t.Logf("git %s: worktree/i: supported = %v", c.install.Version, c.worktree)
}

// TestGitPlanApplyUndoRoundTrip is the main flow: the preview shows the
// exact lines, Apply writes exactly those, and Undo puts the global config
// back byte for byte and removes Devpit's files.
func TestGitPlanApplyUndoRoundTrip(t *testing.T) {
	w := newGitWorld(t)
	w.addIdentity("work", "Zubair", "zubair@work.com")
	p, err := w.git.Plan(accounts.PreviewInput{
		Store: w.store(), Change: accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(`C:\Work`)},
		Defaults: map[accounts.Tool]string{accounts.ToolGit: "base@example.invalid"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Sentences[0] != `In C:\Work and every folder inside it, Git will commit as Zubair <zubair@work.com>.` ||
		p.Sentences[1] != "Everywhere else stays default (base@example.invalid)." {
		t.Fatalf("sentences = %q", p.Sentences)
	}
	text := p.Text()
	for _, want := range []string{
		"~/.devpit/git/work.gitconfig (adds)", `email = "zubair@work.com"`,
		"~/.devpit/git/rules.gitconfig (adds)", `[includeIf "gitdir/i:C:/Work/"]`, `path = "work.gitconfig"`,
		"(adds)", "[include]", "rules.gitconfig\"",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("preview lacks %q:\n%s", want, text)
		}
	}
	evs := collect(t, Apply(context.Background(), w.eng, w.git, p))
	if fin := last(evs); fin.State != accounts.StepDone {
		t.Fatalf("apply: %+v %v", fin, fin.Err)
	}
	// What was written is exactly what the preview showed.
	rules, _ := os.ReadFile(filepath.Join(GitDir(w.deps), gitRulesFile))
	for _, e := range p.Edits[1:] {
		if strings.HasSuffix(e.Path, gitRulesFile) && !slices.Equal(e.Lines, fileLines(rules)) {
			t.Fatalf("rules.gitconfig differs from the preview:\n%s\nvs\n%q", rules, e.Lines)
		}
	}
	g := w.readGlobal()
	if !bytes.HasPrefix(g, w.orig) || !bytes.HasSuffix(g, []byte("rules.gitconfig\"\n")) {
		t.Fatalf("global = %s", g)
	}
	if got := w.gitOut(w.tmp, "config", "--global", "--includes", "user.email"); got != "base@example.invalid" {
		t.Fatalf("outside any rule Git says %s", got)
	}

	if _, err := w.eng.Undo(); err != nil {
		t.Fatal(err)
	}
	if got := w.readGlobal(); !bytes.Equal(got, w.orig) {
		t.Fatalf("undo did not restore the exact bytes:\n%q\nwant\n%q", got, w.orig)
	}
	for _, f := range w.devpitFiles() {
		if strings.HasSuffix(f, ".gitconfig") {
			t.Fatalf("undo left %s", f)
		}
	}
}

func TestGitIncludeAlreadyPresentAndLastRuleRemovesEverything(t *testing.T) {
	w := newGitWorld(t)
	w.addIdentity("work", "Zubair", "zubair@work.com")
	// The include is already there (a second Devpit, or put back by hand).
	g := gitSync{d: w.deps, dir: GitDir(w.deps), global: w.global}
	pre := append(append([]byte{}, w.orig...), []byte(strings.Join(g.includeBlock(), "\n")+"\n")...)
	_ = os.WriteFile(w.global, pre, 0o600)
	p := w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(`C:\Work`)})
	for _, e := range p.Edits {
		if strings.Contains(e.Path, "global.gitconfig") {
			t.Fatalf("the include was already there, yet the preview edits the global config: %+v", e)
		}
	}
	if !bytes.Equal(w.readGlobal(), pre) {
		t.Fatal("the global config changed although the include was there")
	}

	// From a clean start: rule on, rule off → the include and the files go.
	_ = os.WriteFile(w.global, w.orig, 0o600)
	_ = os.RemoveAll(GitDir(w.deps))
	w2 := newGitWorld(t)
	w2.addIdentity("work", "Zubair", "zubair@work.com")
	w2.change(w2.git, accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(`C:\Work`)})
	w2.change(w2.git, accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(`D:\Other`)})
	p = w2.change(w2.git, accounts.Change{Tool: accounts.ToolGit, Scope: accounts.FolderScope(`C:\Work`), Remove: true})
	if strings.Contains(p.Text(), "global.gitconfig") {
		t.Fatalf("one rule is left; the include must stay:\n%s", p.Text())
	}
	p = w2.change(w2.git, accounts.Change{Tool: accounts.ToolGit, Scope: accounts.FolderScope(`D:\Other`), Remove: true})
	if !strings.Contains(p.Text(), "global.gitconfig (removes)") {
		t.Fatalf("the last rule must remove the include:\n%s", p.Text())
	}
	if got := w2.readGlobal(); !bytes.Equal(got, w2.orig) {
		t.Fatalf("global after the last rule:\n%q", got)
	}
	for _, f := range w2.devpitFiles() {
		if strings.HasSuffix(f, ".gitconfig") || f == gitExtrasFile {
			t.Fatalf("left behind: %s", f)
		}
	}
}

func TestGitIncludeGoesLastEvenWithAnEarlierIncludeSection(t *testing.T) {
	w := newGitWorld(t)
	own := []byte("[include]\n\tpath = mine.gitconfig\n[user]\n\temail = base@example.invalid\n")
	_ = os.WriteFile(w.global, own, 0o600)
	w.addIdentity("work", "Zubair", "zubair@work.com")
	w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(`C:\Work`)})
	st, err := w.git.IncludeStatus(context.Background())
	if err != nil || !st.Present || len(st.After) != 0 {
		t.Fatalf("include status = %+v %v", st, err)
	}
	// A line added after it later is reported.
	_ = os.WriteFile(w.global, append(w.readGlobal(), []byte("[user]\n\temail = late@example.invalid\n")...), 0o600)
	st, _ = w.git.IncludeStatus(context.Background())
	if !slices.Equal(st.After, []string{"user.email"}) {
		t.Fatalf("after = %+v", st)
	}
}

func TestGitHandEditedRulesFileStopsUndoAndApply(t *testing.T) {
	w := newGitWorld(t)
	w.addIdentity("work", "Zubair", "zubair@work.com")
	w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(`C:\Work`)})
	rules := filepath.Join(GitDir(w.deps), gitRulesFile)
	b, _ := os.ReadFile(rules)
	_ = os.WriteFile(rules, append(b, []byte("[includeIf \"gitdir/i:C:/Mine/\"]\n    path = mine.gitconfig\n")...), 0o600)

	if _, err := w.eng.Undo(); !errors.Is(err, accounts.ErrChangedByHand) {
		t.Fatalf("undo after a hand edit: %v", err)
	}
	_, err := w.git.Plan(accounts.PreviewInput{Store: w.store(), Change: accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(`D:\Other`)}})
	if !errors.Is(err, accounts.ErrChangedByHand) || !strings.Contains(err.Error(), "changed by hand") {
		t.Fatalf("plan after a hand edit: %v", err)
	}
	// A file Devpit did not write is never touched either.
	_ = os.WriteFile(rules, b, 0o600)
	_ = os.WriteFile(filepath.Join(GitDir(w.deps), "notes.gitconfig"), []byte("[user]\n"), 0o600)
	w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Scope: accounts.FolderScope(`C:\Work`), Remove: true})
	if _, err := os.Stat(filepath.Join(GitDir(w.deps), "notes.gitconfig")); err != nil {
		t.Fatal("a file Devpit did not write was removed")
	}
}

func TestGitStalePreviewIsRefused(t *testing.T) {
	w := newGitWorld(t)
	w.addIdentity("work", "Zubair", "zubair@work.com")
	p, err := w.git.Plan(accounts.PreviewInput{Store: w.store(), Change: accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(`C:\Work`)}})
	if err != nil {
		t.Fatal(err)
	}
	// The global config gains Devpit's include behind the preview's back.
	g := gitSync{d: w.deps, dir: GitDir(w.deps), global: w.global}
	_ = os.WriteFile(w.global, append(w.readGlobal(), []byte(strings.Join(g.includeBlock(), "\n")+"\n")...), 0o600)
	fin := last(collect(t, Apply(context.Background(), w.eng, w.git, p)))
	if fin.State != accounts.StepFailed || !errors.Is(fin.Err, accounts.ErrStalePreview) {
		t.Fatalf("stale apply = %+v", fin)
	}
	if len(w.devpitFiles()) != 0 {
		t.Fatalf("a refused apply left files: %v", w.devpitFiles())
	}
}

func TestGitSettingsSigningAndRepair(t *testing.T) {
	w := newGitWorld(t)
	w.addIdentity("work", "Zubair", "zubair@work.com")
	w.change(w.git, accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.EverywhereScope()})
	p, err := w.git.PlanSettings(w.store(), GitSettingsChange{Identity: "work", Signing: &GitSigning{Key: "~/.ssh/id_work.pub", Format: "ssh", Sign: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(p.Lines(), "\n"), "gpgsign = true") {
		t.Fatalf("preview:\n%s", strings.Join(p.Lines(), "\n"))
	}
	fin := last(collect(t, w.git.ApplySettings(context.Background(), w.eng, p)))
	if fin.State != accounts.StepDone || fin.EntryID == "" {
		t.Fatalf("apply = %+v", fin)
	}
	if got := w.gitOut(w.tmp, "config", "--global", "--includes", "commit.gpgsign"); got != "true" {
		t.Fatalf("gpgsign = %q", got)
	}
	// Everywhere: the identity applies outside any rule, after the
	// person's own [user] lines, which were not edited.
	if got := w.gitOut(w.tmp, "config", "--global", "--includes", "user.email"); got != "zubair@work.com" {
		t.Fatalf("everywhere email = %q", got)
	}
	if !bytes.HasPrefix(w.readGlobal(), w.orig) {
		t.Fatal("the person's own lines were edited")
	}
	// Deleting a file by hand: a repair writes it back.
	_ = os.Remove(filepath.Join(GitDir(w.deps), "work.gitconfig"))
	p, err = w.git.PlanSettings(w.store(), GitSettingsChange{})
	if err != nil || p.NoChange || !strings.Contains(strings.Join(p.Lines(), "\n"), "work.gitconfig (adds)") {
		t.Fatalf("repair = %+v %v", p, err)
	}
}

func fixedNow() time.Time { return time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC) }

func TestGitGlobalConfigWithASecretIsLeftAlone(t *testing.T) {
	w := newGitWorld(t)
	secret := baseGlobal + "[url \"https://x-access-token:" + "ghp_" + strings.Repeat("Ab1", 12) + "@github.com/\"]\n\tinsteadOf = https://github.com/\n"
	_ = os.WriteFile(w.global, []byte(secret), 0o600)
	w.addIdentity("work", "Zubair", "zubair@work.com")
	_, err := w.git.Plan(accounts.PreviewInput{Store: w.store(), Change: accounts.Change{Tool: accounts.ToolGit, Account: "work", Scope: accounts.FolderScope(`C:\Work`)}})
	if err == nil || !strings.Contains(err.Error(), "[include]") || strings.Contains(accounts.Scrub(err.Error()), "ghp_") {
		t.Fatalf("plan: %v", err)
	}
	if got, _ := os.ReadFile(w.global); string(got) != secret {
		t.Fatal("the file was changed")
	}
}
