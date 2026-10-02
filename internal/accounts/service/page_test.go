package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/importer"
)

// Renaming an account renames every rule that names it, as one change one
// undo takes back.
func TestRenameAccountFollowsItsRulesAndUndoes(t *testing.T) {
	w := newWorld(t)
	w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	w.apply(accounts.Change{Tool: accounts.ToolClaude, Account: "work", Scope: accounts.FolderScope(w.work)})
	ctx := context.Background()

	p, err := w.s.PlanAccountEdit(ctx, AccountEdit{Tool: accounts.ToolClaude, Name: "wo", NewName: "office"})
	must(t, err)
	if p.TypedWord != "" || !strings.Contains(strings.Join(p.Sentences, " "), "will be called office") {
		t.Fatalf("rename preview: %+v", p)
	}
	last := drainEvents(w.s.ApplyAccountEdit(ctx, p))
	if last.State != accounts.StepDone {
		t.Fatalf("rename failed: %v", last.Err)
	}
	st, _, _ := w.s.Load()
	if _, ok := st.Account(accounts.ToolClaude, "office"); !ok || st.RulesUsing(accounts.ToolClaude, "office") == nil {
		t.Fatalf("not renamed with its rule: %+v", st)
	}
	if _, err := w.s.Undo(ctx, nil); err != nil {
		t.Fatal(err)
	}
	st, _, _ = w.s.Load()
	if _, ok := st.Account(accounts.ToolClaude, "work"); !ok {
		t.Fatal("undo did not bring the old name back")
	}
}

// Removing an account lists the rules that fall back, asks for a typed
// word, keeps the account's folder, and suggests the tool's own sign-out.
func TestRemoveAccountListsWhatFallsBackAndKeepsTheFolder(t *testing.T) {
	w := newWorld(t)
	a := w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	w.apply(accounts.Change{Tool: accounts.ToolClaude, Account: "work", Scope: accounts.FolderScope(w.work)})
	ctx := context.Background()

	p, err := w.s.PlanAccountEdit(ctx, AccountEdit{Tool: accounts.ToolClaude, Name: "work", Remove: true})
	must(t, err)
	if p.TypedWord != RemoveWord || len(p.FallsBack) != 1 || !accounts.SameFolder(p.FallsBack[0].Folder, w.work) ||
		!strings.Contains(p.FallsBack[0].Display, "like everywhere else") {
		t.Fatalf("remove preview: %+v", p)
	}
	if !strings.Contains(strings.Join(p.Before, " "), "claude auth logout") {
		t.Fatalf("no sign-out suggestion: %v", p.Before)
	}
	if last := drainEvents(w.s.ApplyAccountEdit(ctx, p)); last.State != accounts.StepDone {
		t.Fatalf("remove failed: %v", last.Err)
	}
	st, _, _ := w.s.Load()
	if _, ok := st.Account(accounts.ToolClaude, "work"); ok || len(st.Rules) != 0 {
		t.Fatalf("not removed with its rule: %+v", st)
	}
	if _, err := os.Stat(a.Dir); err != nil {
		t.Fatalf("the account folder (and its sign-in) went: %v", err)
	}
}

// A preview made before the store changed is refused.
func TestAccountEditRefusesAStalePreview(t *testing.T) {
	w := newWorld(t)
	w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	ctx := context.Background()
	p, err := w.s.PlanAccountEdit(ctx, AccountEdit{Tool: accounts.ToolClaude, Name: "work", NewName: "office"})
	must(t, err)
	w.addAccount(accounts.ToolClaude, "oss", "z@oss.com")
	last := drainEvents(w.s.ApplyAccountEdit(ctx, p))
	if last.State != accounts.StepFailed || !errors.Is(last.Err, accounts.ErrStalePreview) {
		t.Fatalf("a stale preview was applied: %+v", last)
	}
}

// Wrangler names its profile when the account is made, so a Cloudflare
// account is not renamed; the default account is neither.
func TestAccountEditRefusals(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, err := w.s.PlanAccountEdit(ctx, AccountEdit{Tool: accounts.ToolClaude, Name: "default", Remove: true}); !errors.Is(err, accounts.ErrReservedName) {
		t.Fatalf("the default account could be removed: %v", err)
	}
	_, err := w.s.Engine.AddAccount(accounts.Account{Tool: accounts.ToolCloudflare, Name: "acme"})
	must(t, err)
	if _, err := w.s.PlanAccountEdit(ctx, AccountEdit{Tool: accounts.ToolCloudflare, Name: "acme", NewName: "acme2"}); !errors.Is(err, accounts.ErrNotSupported) {
		t.Fatalf("a Wrangler profile could be renamed: %v", err)
	}
}

// Throwing an unnamed sign-in away removes only the placeholder folder the
// sign-in made.
func TestDiscardSignInRemovesOnlyThePlaceholderFolder(t *testing.T) {
	w := newWorld(t)
	tmp := PlaceholderName()
	dir := w.s.Deps.Paths.AccountDir(accounts.ToolClaude, tmp)
	must(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700))
	if _, err := w.s.DiscardSignIn(accounts.Account{Tool: accounts.ToolClaude, Name: tmp, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the placeholder folder is still there: %v", err)
	}
	keep := w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	if _, err := w.s.DiscardSignIn(keep); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep.Dir); err != nil {
		t.Fatal("a named account's folder was removed")
	}
	note, err := w.s.DiscardSignIn(accounts.Account{Tool: accounts.ToolGitHub, Name: tmp, Label: "zubair"})
	if err != nil || !strings.Contains(note, "gh auth logout") {
		t.Fatalf("gh's own login list was not explained: %q %v", note, err)
	}
}

// ApplyGroup applies several previews as one change, so one undo takes them
// all back.
func TestApplyGroupIsOneUndo(t *testing.T) {
	w := newWorld(t)
	w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	ctx := context.Background()
	var ps []accounts.Preview
	for _, f := range []string{w.work, filepath.Join(w.root, "Other")} {
		p, err := w.s.Plan(ctx, accounts.Change{Tool: accounts.ToolClaude, Account: "work", Scope: accounts.FolderScope(f)})
		must(t, err)
		ps = append(ps, p)
	}
	if last := drainEvents(w.s.ApplyGroup(ctx, ps)); last.State != accounts.StepDone || !last.Final {
		t.Fatalf("group failed: %+v", last)
	}
	info, err := w.s.UndoPreview()
	must(t, err)
	if len(info.Entries) != 2 {
		t.Fatalf("undo would take back %d changes, want the 2 made together", len(info.Entries))
	}
	if _, err := w.s.Undo(ctx, nil); err != nil {
		t.Fatal(err)
	}
	st, _, _ := w.s.Load()
	if len(st.Rules) != 0 {
		t.Fatalf("one undo left rules: %+v", st.Rules)
	}
}

// "Ignore" on the import card is remembered for exactly what was found.
func TestImportDismissalIsPerFinding(t *testing.T) {
	w := newWorld(t)
	f := importer.Found{ConfigDirs: []importer.ConfigDir{{Dir: `C:\Users\z\.claude-old`}}}
	if w.s.ImportDismissed(f) {
		t.Fatal("dismissed before anyone said so")
	}
	must(t, w.s.DismissImport(f))
	if !w.s.ImportDismissed(f) {
		t.Fatal("ignore was not remembered")
	}
	f.ConfigDirs = append(f.ConfigDirs, importer.ConfigDir{Dir: `C:\Users\z\.claude-new`})
	if w.s.ImportDismissed(f) {
		t.Fatal("a new finding was ignored too")
	}
}

// The small answers the page asks for: a name's verdict, what a tool
// supports here, and the risk of a live check.
func TestPageAnswers(t *testing.T) {
	w := newWorld(t)
	if err := w.s.CheckNewName(accounts.ToolClaude, "Default"); !errors.Is(err, accounts.ErrReservedName) {
		t.Fatalf("default was a free name: %v", err)
	}
	w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	if err := w.s.CheckNewName(accounts.ToolClaude, "WORK"); !errors.Is(err, accounts.ErrNameTaken) {
		t.Fatalf("a taken name was free: %v", err)
	}
	if c := w.s.Caps(context.Background(), accounts.ToolClaude); !c.FolderRules || !c.AddAccount {
		t.Fatalf("caps = %+v", c)
	}
	if risky, why := w.s.LiveCheckRisk(accounts.ToolClaude); !risky || !strings.Contains(why, "sign an idle account out") {
		t.Fatalf("risk = %v %q", risky, why)
	}
	stale, err := w.s.StaleRules()
	must(t, err)
	if len(stale) != 0 {
		t.Fatalf("stale = %+v", stale)
	}
}

// Sign in again runs the tool's own sign-in in the account's own folder,
// and is refused for a tool whose sign-in only adds logins.
func TestPrepareSignInAgain(t *testing.T) {
	w := newWorld(t)
	a := w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	oc, err := w.s.PrepareSignInAgain(context.Background(), accounts.ToolClaude, "work")
	must(t, err)
	if !slices.Equal(oc.Args, []string{"auth", "login"}) || !slices.Contains(oc.Env, "CLAUDE_CONFIG_DIR="+a.Dir) {
		t.Fatalf("sign in again = %v %v", oc.Args, oc.Env)
	}
	if _, err := w.s.PrepareSignInAgain(context.Background(), accounts.ToolGitHub, "default"); !errors.Is(err, accounts.ErrNotSupported) {
		t.Fatalf("gh's sign-in was offered as sign in again: %v", err)
	}
}

// drainEvents reads a run to its end and returns the last event.
func drainEvents(ch <-chan accounts.Event) accounts.Event {
	var last accounts.Event
	for ev := range ch {
		last = ev
	}
	return last
}
