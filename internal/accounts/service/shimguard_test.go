package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/shims"
)

// Someone replaced only devpit.exe, so devpit-shim.exe is missing. The
// accounts page says so as its own problem with the plain fix and its page,
// and a change that needs a shim stops before anything is written, at the
// preview and again at apply. "Just this once" and removing a rule still
// work, and so does a rule for a tool whose shim is already there.
func TestMissingShimProgramStopsTheChangeBeforeWriting(t *testing.T) {
	w := newWorld(t)
	w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	w.addAccount(accounts.ToolVercel, "work", "z@work.com")
	ready, err := w.s.Plan(context.Background(), accounts.Change{Tool: accounts.ToolClaude, Account: "work", Scope: accounts.FolderScope(w.work)})
	must(t, err)
	must(t, os.Remove(w.s.Shims.Source))
	before, err := w.s.UndoPreview()
	must(t, err)

	ts, err := w.s.Status(context.Background(), accounts.ToolClaude, w.work)
	must(t, err)
	var got *Problem
	for i, p := range ts.Problems {
		if p.Kind == string(shims.IssueShimProgramMissing) {
			got = &ts.Problems[i]
		}
		if p.Kind == string(shims.IssueShimMissing) {
			t.Errorf("the missing program is the cause; %q would send the person the wrong way", p.Kind)
		}
	}
	if got == nil || got.Fix != shims.ProgramMissingFix || got.Docs == "" {
		t.Fatalf("problems = %+v", ts.Problems)
	}

	_, err = w.s.Plan(context.Background(), accounts.Change{Tool: accounts.ToolClaude, Account: "work", Scope: accounts.FolderScope(w.work)})
	if !errors.Is(err, shims.ErrProgramMissing) || !strings.Contains(err.Error(), "Run the installer again, or put devpit-shim.exe next to devpit.exe") {
		t.Fatalf("plan: %v", err)
	}
	var last accounts.Event
	for ev := range w.s.Apply(context.Background(), ready) {
		last = ev
	}
	if last.State != accounts.StepFailed || !errors.Is(last.Err, shims.ErrProgramMissing) {
		t.Fatalf("apply: %+v", last)
	}
	st, _, _ := w.s.Load()
	if len(st.Rules) != 0 || len(st.Everywhere) != 0 {
		t.Fatalf("a rule was written: %+v", st.Rules)
	}
	if after, err := w.s.UndoPreview(); err != nil || strings.Join(after.Lines, "\n") != strings.Join(before.Lines, "\n") {
		t.Fatalf("something was journalled: %v %v", after.Lines, err)
	}

	// Just this once needs no shim.
	if _, err := w.s.Plan(context.Background(), accounts.Change{Tool: accounts.ToolClaude, Account: "work", Scope: accounts.OnceScope()}); err != nil {
		t.Fatalf("once: %v", err)
	}
	// With the shim already in place the rule works, so it is allowed.
	must(t, os.MkdirAll(w.s.Shims.Dir, 0o700))
	must(t, os.WriteFile(w.s.Shims.ShimPath(accounts.ToolVercel), []byte("shim v1"), 0o600))
	p := w.apply(accounts.Change{Tool: accounts.ToolVercel, Account: "work", Scope: accounts.FolderScope(w.work)})
	if p.NoChange {
		t.Fatal("no change")
	}
	// Removing a rule needs no new shim.
	w.apply(accounts.Change{Tool: accounts.ToolVercel, Scope: accounts.FolderScope(w.work), Remove: true})
}

// The Accounts page's sync refreshes shim copies after an update, says so,
// and does nothing when they are current.
func TestSyncShimsRefreshesOldCopies(t *testing.T) {
	w := newWorld(t)
	w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	w.apply(accounts.Change{Tool: accounts.ToolClaude, Account: "work", Scope: accounts.FolderScope(w.work)})
	w.env["PATH"] = w.s.Shims.Dir
	var steps []string
	w.s.SyncShims(func(ev accounts.Event) { steps = append(steps, ev.Step) })
	if len(steps) != 0 {
		t.Fatalf("current shims, but: %v", steps)
	}
	must(t, os.WriteFile(w.s.Shims.Source, []byte("shim v2, the update"), 0o600))
	w.s.SyncShims(func(ev accounts.Event) { steps = append(steps, ev.Step) })
	if len(steps) != 1 || steps[0] != "Updated Devpit's shim for Claude Code" {
		t.Fatalf("steps = %v", steps)
	}
	if b, _ := os.ReadFile(w.s.Shims.ShimPath(accounts.ToolClaude)); string(b) != "shim v2, the update" {
		t.Fatalf("shim = %q", b)
	}
	// Without the program, the page says what to do instead of failing.
	must(t, os.Remove(w.s.Shims.Source))
	var warn []string
	w.s.SyncShims(func(ev accounts.Event) {
		if ev.State == accounts.StepWarning {
			warn = append(warn, ev.Detail)
		}
	})
	if len(warn) != 1 || !strings.Contains(warn[0], shims.ProgramMissingFix) {
		t.Fatalf("warnings = %v", warn)
	}
}
