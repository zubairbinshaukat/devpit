package accounts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine(PathsIn(t.TempDir(), t.TempDir()))
	e.LockWait = 200 * time.Millisecond
	return e
}

func load(t *testing.T, e *Engine) *Store {
	t.Helper()
	s, err := ReadFile(e.Paths.Store)
	must(t, err)
	return s
}

func addWork(s *Store) error {
	return s.AddAccount(Account{Tool: ToolClaude, Name: "work", Email: "z@work.com", Dir: `C:\a\work`})
}

func TestEditAndUndoRoundTrip(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.Edit("Added Claude Code account work", addWork); err != nil {
		t.Fatal(err)
	}
	ent, err := e.Edit("Claude Code uses work in C:\\Work", func(s *Store) error { return s.SetRule(`C:\Work`, ToolClaude, "work") })
	must(t, err)
	if ent.State != EntryDone || ent.ID == "" {
		t.Fatalf("entry = %+v", ent)
	}
	if len(load(t, e).Rules) != 1 {
		t.Fatal("rule not saved")
	}

	res, err := e.Undo()
	must(t, err)
	if res.Entry.Summary != `Claude Code uses work in C:\Work` || len(load(t, e).Rules) != 0 || len(load(t, e).Accounts) != 1 {
		t.Fatalf("after first undo: %+v", load(t, e))
	}
	if _, err = e.Undo(); err != nil {
		t.Fatal(err)
	}
	if len(load(t, e).Accounts) != 0 {
		t.Fatal("second undo should remove the account")
	}
	if _, err = e.Undo(); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("third undo: %v", err)
	}
	hist, _, err := e.History()
	must(t, err)
	if len(hist) != 2 || hist[0].State != EntryUndone || hist[1].State != EntryUndone {
		t.Fatalf("history = %+v", hist)
	}
}

func TestFileEffectIsUndoneExactly(t *testing.T) {
	e := newTestEngine(t)
	dir := t.TempDir()
	existing := filepath.Join(dir, "rules.gitconfig")
	created := filepath.Join(dir, "work.gitconfig")
	must(t, os.WriteFile(existing, []byte("[include]\n\tpath = old\n"), 0o600))

	txn, err := e.Begin("Git commits as work in C:\\Work", nil)
	must(t, err)
	must(t, txn.WriteFile(existing, "rules", []byte("[include]\n\tpath = new\n")))
	must(t, txn.WriteFile(created, "identity", []byte("[user]\n\temail = z@work.com\n")))
	must(t, txn.MakeDir(filepath.Join(dir, "made"), "a folder"))
	if _, err = txn.Commit(); err != nil {
		t.Fatal(err)
	}

	res, err := e.Undo()
	must(t, err)
	if b, _ := os.ReadFile(existing); string(b) != "[include]\n\tpath = old\n" {
		t.Fatalf("existing file = %q", b)
	}
	if _, err := os.Stat(created); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file the change created must be removed by undo")
	}
	if _, err := os.Stat(filepath.Join(dir, "made")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an empty folder the change created must be removed")
	}
	if len(res.Notes) != 0 {
		t.Fatalf("notes = %v", res.Notes)
	}
}

func TestUndoStopsWhenAFileWasEditedByHand(t *testing.T) {
	e := newTestEngine(t)
	target := filepath.Join(t.TempDir(), "rules.gitconfig")
	txn, err := e.Begin("change", func(s *Store) error { return addWork(s) })
	must(t, err)
	must(t, txn.WriteFile(target, "rules", []byte("devpit wrote this\n")))
	_, err = txn.Commit()
	must(t, err)

	must(t, os.WriteFile(target, []byte("devpit wrote this\nand the person added this\n"), 0o600))
	_, err = e.Undo()
	var ce *ChangedError
	if !errors.As(err, &ce) || !errors.Is(err, ErrChangedByHand) || ce.Path != target {
		t.Fatalf("err = %v", err)
	}
	// Nothing changed: the file keeps the edit, the store keeps the account.
	if b, _ := os.ReadFile(target); !strings.Contains(string(b), "person added") {
		t.Fatal("undo clobbered a hand edit")
	}
	if len(load(t, e).Accounts) != 1 {
		t.Fatal("undo changed the store although it stopped")
	}
	hist, _, _ := e.History()
	if hist[0].State != EntryDone {
		t.Fatalf("state = %s", hist[0].State)
	}
}

func TestUndoStopsWhenTheStoreWasEditedByHand(t *testing.T) {
	e := newTestEngine(t)
	_, err := e.Edit("add", addWork)
	must(t, err)
	s := load(t, e)
	must(t, s.SetRule(`C:\Hand`, ToolClaude, "work"))
	must(t, SaveFile(e.Paths.Store, s))
	if _, err := e.Undo(); !errors.Is(err, ErrChangedByHand) {
		t.Fatalf("err = %v", err)
	}
	if len(load(t, e).Rules) != 1 {
		t.Fatal("the hand edit was lost")
	}
}

// A crash after the intent and the side effect, before the store was saved:
// the next start rolls the side effect back.
func TestCrashBetweenIntentAndSideEffectIsRolledBack(t *testing.T) {
	e := newTestEngine(t)
	target := filepath.Join(t.TempDir(), "rules.gitconfig")
	must(t, os.WriteFile(target, []byte("before\n"), 0o600))

	txn, err := e.Begin("crashes", addWork)
	must(t, err)
	must(t, txn.WriteFile(target, "rules", []byte("after\n")))
	txn.release() // the process dies here: no Commit, no Rollback

	e2 := NewEngine(e.Paths)
	rec, err := e2.Recover()
	must(t, err)
	if len(rec) != 1 || rec[0].Outcome != "rolled back" {
		t.Fatalf("recovered = %+v", rec)
	}
	if b, _ := os.ReadFile(target); string(b) != "before\n" {
		t.Fatalf("file = %q", b)
	}
	if len(load(t, e2).Accounts) != 0 {
		t.Fatal("the store must stay as it was before the change")
	}
	// Nothing left to recover, and the rolled-back change is not undoable.
	if rec, _ := e2.Recover(); len(rec) != 0 {
		t.Fatalf("recovered twice: %+v", rec)
	}
	if _, err := e2.Undo(); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("undo: %v", err)
	}
}

// A crash after the intent was written but before the side effect even
// started: nothing to put back, the entry is just closed.
func TestCrashBeforeTheSideEffectStarted(t *testing.T) {
	e := newTestEngine(t)
	txn, err := e.Begin("crashes early", addWork)
	must(t, err)
	txn.release()
	rec, err := NewEngine(e.Paths).Recover()
	must(t, err)
	if len(rec) != 1 || rec[0].Outcome != "rolled back" {
		t.Fatalf("recovered = %+v", rec)
	}
}

// A crash after Commit wrote "committed", with or without the store saved
// yet: the next start finishes it (saving the store when needed), and it
// can then be undone.
func TestCrashAfterCommitIsFinished(t *testing.T) {
	for _, saved := range []bool{true, false} {
		e := newTestEngine(t)
		target := filepath.Join(t.TempDir(), "x.gitconfig")
		txn, err := e.Begin("almost done", addWork)
		must(t, err)
		must(t, txn.WriteFile(target, "x", []byte("new\n")))
		txn.entries[txn.idx].Committed = true
		must(t, txn.save())
		if saved {
			must(t, SaveFile(e.Paths.Store, txn.After()))
		}
		txn.release()

		e2 := NewEngine(e.Paths)
		rec, err := e2.Recover()
		must(t, err)
		if len(rec) != 1 || rec[0].Outcome != "finished" {
			t.Fatalf("saved=%v: recovered = %+v", saved, rec)
		}
		if res, _ := e2.Load(); len(res.Store.AccountsFor(ToolClaude)) != 1 {
			t.Fatalf("saved=%v: the store was not finished", saved)
		}
		if _, err := e2.Undo(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("undo after a finished recovery must remove the file")
		}
	}
}

// Without "committed", a change is rolled back however far it got, even
// when every side effect is done and the store happens to match: a change
// that leaves accounts.toml as it was (sharing Claude Code's setup) can no
// longer be taken for a finished one.
func TestUncommittedChangeIsAlwaysRolledBack(t *testing.T) {
	e := newTestEngine(t)
	target := filepath.Join(t.TempDir(), "x.gitconfig")
	txn, err := e.Begin("no store change", nil)
	must(t, err)
	must(t, txn.WriteFile(target, "x", []byte("new\n")))
	txn.release()
	rec, err := NewEngine(e.Paths).Recover()
	must(t, err)
	if len(rec) != 1 || rec[0].Outcome != "rolled back" {
		t.Fatalf("recovered = %+v", rec)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the side effect was not put back")
	}
}

// An entry written before "committed" existed is still read, and recovered
// the old way, except that one with no store change is rolled back.
func TestOldJournalEntriesStillRecover(t *testing.T) {
	e := newTestEngine(t)
	txn, err := e.Begin("old style", addWork)
	must(t, err)
	txn.entries[txn.idx].V = 0
	must(t, txn.save())
	must(t, SaveFile(e.Paths.Store, txn.After()))
	txn.release()
	rec, err := NewEngine(e.Paths).Recover()
	must(t, err)
	if len(rec) != 1 || rec[0].Outcome != "finished" {
		t.Fatalf("recovered = %+v", rec)
	}
}

// A missing accounts.toml is an empty store, never a file changed by hand:
// a change that never created it is rolled back, and one undone after the
// file was deleted is refused only if the change had saved something.
func TestMissingStoreCountsAsEmpty(t *testing.T) {
	e := newTestEngine(t)
	target := filepath.Join(t.TempDir(), "x.txt")
	txn, err := e.Begin("share", nil)
	must(t, err)
	must(t, txn.WriteFile(target, "x", []byte("x\n")))
	txn.release()
	if _, serr := os.Stat(e.Paths.Store); !errors.Is(serr, os.ErrNotExist) {
		t.Fatal("the store should not exist yet")
	}
	rec, err := NewEngine(e.Paths).Recover()
	if err != nil || len(rec) != 1 || rec[0].Outcome != "rolled back" {
		t.Fatalf("recover with no accounts.toml: %+v %v", rec, err)
	}

	// Done, then accounts.toml deleted: the change saved an empty store, so
	// undo still works.
	txn, err = e.Begin("share again", nil)
	must(t, err)
	must(t, txn.WriteFile(target, "x", []byte("x\n")))
	_, err = txn.Commit()
	must(t, err)
	must(t, os.Remove(e.Paths.Store))
	if _, err := e.Undo(); err != nil {
		t.Fatalf("undo with accounts.toml missing: %v", err)
	}
}

// Begin recovers first, so a crashed change never blocks the next one.
func TestBeginRecoversFirst(t *testing.T) {
	e := newTestEngine(t)
	txn, err := e.Begin("crashes", addWork)
	must(t, err)
	txn.release()
	if _, err := NewEngine(e.Paths).Edit("next", addWork); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryStopsOnAHandEditedFile(t *testing.T) {
	e := newTestEngine(t)
	target := filepath.Join(t.TempDir(), "rules")
	txn, err := e.Begin("crashes", addWork)
	must(t, err)
	must(t, txn.WriteFile(target, "rules", []byte("devpit\n")))
	txn.release()
	must(t, os.WriteFile(target, []byte("hand edit\n"), 0o600))
	if _, err := NewEngine(e.Paths).Recover(); !errors.Is(err, ErrChangedByHand) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "hand edit\n" {
		t.Fatal("recovery clobbered a hand edit")
	}
}

func TestRollbackPutsEverythingBack(t *testing.T) {
	e := newTestEngine(t)
	target := filepath.Join(t.TempDir(), "f")
	txn, err := e.Begin("fails half way", addWork)
	must(t, err)
	must(t, txn.WriteFile(target, "f", []byte("x\n")))
	must(t, txn.Rollback())
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rollback left the file")
	}
	if _, err := e.Undo(); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("undo after rollback: %v", err)
	}
}

func TestJournalNeverHoldsASecretOrALoginFile(t *testing.T) {
	e := newTestEngine(t)
	dir := t.TempDir()
	txn, err := e.Begin("x", nil)
	must(t, err)
	defer func() { _ = txn.Rollback() }()
	if err := txn.WriteFile(filepath.Join(dir, ".credentials.json"), "x", []byte("{}")); err == nil {
		t.Fatal("wrote a login file")
	}
	if err := txn.WriteFile(filepath.Join(dir, "hosts.yml"), "x", []byte("a: b")); err == nil {
		t.Fatal("wrote gh's hosts.yml")
	}
	tok := "ghp_" + strings.Repeat("Q1w2", 9)
	if err := txn.WriteFile(filepath.Join(dir, "x.gitconfig"), "x", []byte("token = "+tok)); err == nil {
		t.Fatal("wrote a token")
	}
	old := filepath.Join(dir, "old.conf")
	must(t, os.WriteFile(old, []byte("secret="+tok), 0o600))
	if err := txn.WriteFile(old, "x", []byte("clean")); err == nil {
		t.Fatal("copied a file holding a token into the journal")
	}
	if b, _ := os.ReadFile(e.Paths.Journal); strings.Contains(string(b), tok) {
		t.Fatal("the journal holds the token")
	}
}

func TestJournalKeepsTheLastTwenty(t *testing.T) {
	e := newTestEngine(t)
	for i := 0; i < JournalLimit+5; i++ {
		folder := `C:\P` + itoa(i)
		if _, err := e.Edit("rule "+itoa(i), func(s *Store) error { return s.SetRule(folder, ToolVercel, "default") }); err != nil {
			t.Fatal(err)
		}
	}
	hist, _, err := e.History()
	must(t, err)
	if len(hist) != JournalLimit || hist[0].Summary != "rule 24" {
		t.Fatalf("kept %d, newest %q", len(hist), hist[0].Summary)
	}
}

func TestCorruptJournalIsQuarantined(t *testing.T) {
	e := newTestEngine(t)
	must(t, os.MkdirAll(e.Paths.ConfigDir, 0o700))
	must(t, os.WriteFile(e.Paths.Journal, []byte("{not json\n"), 0o600))
	hist, warn, err := e.History()
	must(t, err)
	if len(hist) != 0 || warn == "" {
		t.Fatalf("hist=%v warn=%q", hist, warn)
	}
	if _, err := e.Edit("works again", addWork); err != nil {
		t.Fatal(err)
	}
}

func TestStalePreviewIsRefused(t *testing.T) {
	e := newTestEngine(t)
	_, err := e.Edit("add", addWork)
	must(t, err)
	p, err := BuildPreview(PreviewInput{Store: load(t, e), Change: Change{Tool: ToolClaude, Account: "work", Scope: FolderScope(`C:\Work`)}})
	must(t, err)
	_, err = e.Edit("someone else", func(s *Store) error { return s.SetRule(`C:\Other`, ToolClaude, "work") })
	must(t, err)
	if _, err = e.BeginPreview(p); !errors.Is(err, ErrStalePreview) {
		t.Fatalf("err = %v", err)
	}
	// A fresh preview applies.
	p, _ = BuildPreview(PreviewInput{Store: load(t, e), Change: Change{Tool: ToolClaude, Account: "work", Scope: FolderScope(`C:\Work`)}})
	txn, err := e.BeginPreview(p)
	must(t, err)
	_, err = txn.Commit()
	must(t, err)
	if r, ok := load(t, e).Rule(`C:\Work`); !ok || r.Accounts[ToolClaude] != "work" {
		t.Fatal("not applied")
	}
	if _, err := e.BeginPreview(Preview{Change: Change{Scope: OnceScope()}}); err == nil {
		t.Fatal("a once change must not be saved")
	}
}

func TestAddAccountKeepsTheSignInFolderOnUndo(t *testing.T) {
	e := newTestEngine(t)
	dir := e.Paths.AccountDir(ToolClaude, "work")
	must(t, os.MkdirAll(dir, 0o700))
	must(t, os.WriteFile(filepath.Join(dir, ".claude.json"), []byte("{}"), 0o600))
	_, err := e.AddAccount(Account{Tool: ToolClaude, Name: "work", Dir: dir})
	must(t, err)
	res, err := e.Undo()
	must(t, err)
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("undo deleted an account folder")
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "was kept") {
		t.Fatalf("notes = %v", res.Notes)
	}
}

func TestASecondDevpitCannotChangeAccountsWhileTheFirstHoldsThem(t *testing.T) {
	p := PathsIn(t.TempDir(), t.TempDir())
	first, second := NewEngine(p), NewEngine(p)
	second.LockWait = 50 * time.Millisecond
	must(t, first.Hold())
	if err := second.Hold(); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Hold: %v", err)
	}
	if _, err := second.Edit("x", addWork); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Edit: %v", err)
	}
	// The holder can still change things itself.
	if _, err := first.Edit("x", addWork); err != nil {
		t.Fatal(err)
	}
	// Reading is never blocked.
	if _, err := second.Load(); err != nil {
		t.Fatal(err)
	}
	first.Release()
	if _, err := second.Edit("y", func(s *Store) error { return s.SetEverywhere(ToolClaude, "work") }); err != nil {
		t.Fatal(err)
	}
}

func TestScrubErrorKeepsSentinelsOnly(t *testing.T) {
	tok := "ghp_" + strings.Repeat("M3n4", 9)
	err := ScrubError(errors.Join(ErrTimeout, errors.New("gh said "+tok)))
	if strings.Contains(err.Error(), tok) || !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	ev := Failed("step", err)
	if !ev.Final || ev.State != StepFailed || strings.Contains(ev.Err.Error(), tok) {
		t.Fatalf("event = %+v", ev)
	}
}
