package shims

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// Someone replaced only devpit.exe: devpit-shim.exe is not next to it.
// Check says so as its own problem with the plain fix (and not "shim
// missing", whose fix would not work), and Ensure and Sync fail with
// ErrProgramMissing before writing anything.
func TestCheckReportsAMissingShimProgram(t *testing.T) {
	m, _, env := newManager(t)
	env["PATH"] = m.Dir
	if err := os.Remove(m.Source); err != nil {
		t.Fatal(err)
	}
	tools := []accounts.Tool{accounts.ToolClaude}
	issues := m.Check(tools)
	if !hasIssue(issues, IssueShimProgramMissing) || hasIssue(issues, IssueShimMissing) {
		t.Fatalf("issues = %+v", issues)
	}
	for _, is := range issues {
		if is.Kind == IssueShimProgramMissing && (is.Fix != ProgramMissingFix || !strings.Contains(is.Message, "devpit-shim.exe")) {
			t.Fatalf("issue = %+v", is)
		}
	}
	if issues := m.Check(nil); len(issues) != 0 {
		t.Fatalf("no tool needs a shim, so nothing to say: %+v", issues)
	}
	if _, err := m.Ensure(accounts.ToolClaude); !errors.Is(err, ErrProgramMissing) || !strings.Contains(err.Error(), ProgramMissingFix) {
		t.Fatalf("Ensure: %v", err)
	}
	s := accounts.NewStore()
	if err := s.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "work", Dir: `C:\a`}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Sync(s); !errors.Is(err, ErrProgramMissing) {
		t.Fatalf("Sync: %v", err)
	}
	if _, err := os.Stat(m.ShimPath(accounts.ToolClaude)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a shim appeared without its program")
	}
	if err := m.CheckSource(); !errors.Is(err, ErrProgramMissing) {
		t.Fatalf("CheckSource: %v", err)
	}
}

// After an update the shim copies are old devpit-shim.exe copies: Sync
// refreshes the ones in use, RefreshUnneeded the ones no rule needs any
// more, and nothing is written when they are current.
func TestShimCopiesAreRefreshedAfterAnUpdate(t *testing.T) {
	m, _, _ := newManager(t)
	s := accounts.NewStore()
	if err := s.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "work", Dir: `C:\a`}); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []accounts.Tool{accounts.ToolClaude, accounts.ToolVercel} {
		if _, err := m.Ensure(tool); err != nil { // vercel had a rule once
			t.Fatal(err)
		}
	}
	// Nothing to do while they are current.
	if created, _, err := m.Sync(s); err != nil || len(created) != 0 {
		t.Fatalf("current shims rewritten: %v %v", created, err)
	}
	if got, err := m.RefreshUnneeded(s); err != nil || len(got) != 0 {
		t.Fatalf("current shims rewritten: %v %v", got, err)
	}
	// Devpit is updated; the new shim program is a different size.
	if err := os.WriteFile(m.Source, []byte("shim v2, a little longer"), 0o700); err != nil { //nolint:gosec // a fake executable
		t.Fatal(err)
	}
	created, _, err := m.Sync(s)
	if err != nil || len(created) != 1 || created[0] != accounts.ToolClaude {
		t.Fatalf("sync: %v %v", created, err)
	}
	got, err := m.RefreshUnneeded(s)
	if err != nil || len(got) != 1 || got[0] != accounts.ToolVercel {
		t.Fatalf("refresh unneeded: %v %v", got, err)
	}
	for _, tool := range []accounts.Tool{accounts.ToolClaude, accounts.ToolVercel} {
		if b, _ := os.ReadFile(m.ShimPath(tool)); string(b) != "shim v2, a little longer" {
			t.Fatalf("%s shim = %q", tool, b)
		}
	}
	// The same size but other bytes is still out of date.
	if err := os.WriteFile(m.Source, []byte("shim v3, a little longer"), 0o700); err != nil { //nolint:gosec // a fake executable
		t.Fatal(err)
	}
	if created, _, _ := m.Sync(s); len(created) != 1 {
		t.Fatal("a same-size different shim was not replaced")
	}
}

// When a shim cannot be replaced and the new copy then cannot take its
// place either, the old shim is put back: a tool is never left without one.
func TestAFailedReplaceLeavesTheOldShimInPlace(t *testing.T) {
	m, _, _ := newManager(t)
	if _, err := m.Ensure(accounts.ToolClaude); err != nil {
		t.Fatal(err)
	}
	dst := m.ShimPath(accounts.ToolClaude)
	if err := os.WriteFile(m.Source, []byte("shim v2"), 0o700); err != nil { //nolint:gosec // a fake executable
		t.Fatal(err)
	}
	// Every rename onto the shim's own name fails, except putting the old
	// one back (which comes from a ".old-" name).
	m.rename = func(from, to string) error {
		if to == dst && !strings.Contains(from, ".old-") {
			return errors.New("access is denied")
		}
		return os.Rename(from, to)
	}
	if _, err := m.Ensure(accounts.ToolClaude); err == nil || !strings.Contains(err.Error(), "old shim is back") {
		t.Fatalf("Ensure: %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "shim v1" {
		t.Fatalf("shim = %q; the old one should be back", b)
	}
	entries, _ := os.ReadDir(m.Dir)
	if len(entries) != 1 {
		t.Fatalf("left behind: %v", entries)
	}
	m.rename = nil
	if created, err := m.Ensure(accounts.ToolClaude); err != nil || !created {
		t.Fatalf("a later run replaces it: %v %v", created, err)
	}
	if _, err := os.Stat(filepath.Join(m.Dir, "claude.exe")); err != nil {
		t.Fatal(err)
	}
}
