package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// committedPage is the generated page as checked in, found from this
// package's directory (go test runs in it), two levels below the repo root.
var committedPage = filepath.Join("..", "..", filepath.FromSlash(defaultOut))

// TestCommittedCommandLinePageIsFresh fails when the checked-in Command line
// page no longer matches what the command tree renders. Someone changed a
// flag or a command and forgot `task docs:cli`; the fix is to run it and
// commit the result. Without this the page would drift, which is exactly what
// generating it was meant to prevent.
func TestCommittedCommandLinePageIsFresh(t *testing.T) {
	got, err := os.ReadFile(committedPage)
	if err != nil {
		t.Fatalf("read %s: %v (run `task docs:cli`)", committedPage, err)
	}
	// Git may check the file out with CRLF on Windows; the content is what
	// matters, not the line endings.
	have := strings.ReplaceAll(string(got), "\r\n", "\n")
	if want := Page(); have != want {
		t.Fatalf("%s is stale: run `task docs:cli` and commit the result", filepath.ToSlash(defaultOut))
	}
}

// TestPageNeverListsInternalFlags pins that the hidden elevated-helper flags
// stay off the public page. They are internal, and documenting them would
// invite users to run a mode that only Devpit itself should start.
func TestPageNeverListsInternalFlags(t *testing.T) {
	page := Page()
	for _, hidden := range []string{"--elevated-worker", "--pipe"} {
		if strings.Contains(page, hidden) {
			t.Errorf("page mentions hidden flag %s", hidden)
		}
	}
}

// TestPageIsDeterministic pins that two renders are byte-identical, so
// regenerating with no source change can never produce a diff.
func TestPageIsDeterministic(t *testing.T) {
	first := Page()
	if first != Page() {
		t.Fatal("two renders of the page differ")
	}
}
