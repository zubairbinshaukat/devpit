package gitssh

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

// fakeRunner is a [Runner] test double that never execs a real process.
type fakeRunner struct {
	output string
	err    error

	calledName string
	calledArgs []string
	calls      int
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	f.calls++
	f.calledName = name
	f.calledArgs = args
	return f.output, f.err
}

func TestGitConfig_ReturnsValue(t *testing.T) {
	fr := &fakeRunner{output: "Jane Doe\n"}

	got, err := GitConfig(context.Background(), "user.name", ScopeGlobal, Options{Runner: fr})
	if err != nil {
		t.Fatalf("GitConfig() error = %v", err)
	}
	if got != "Jane Doe" {
		t.Fatalf("GitConfig() = %q, want %q", got, "Jane Doe")
	}
	if fr.calledName != "git" {
		t.Fatalf("runner invoked with %q, want git", fr.calledName)
	}
	wantArgs := []string{"config", "--global", "user.name"}
	if len(fr.calledArgs) != len(wantArgs) {
		t.Fatalf("runner args = %v, want %v", fr.calledArgs, wantArgs)
	}
	for i, a := range wantArgs {
		if fr.calledArgs[i] != a {
			t.Fatalf("runner args = %v, want %v", fr.calledArgs, wantArgs)
		}
	}
}

func TestGitConfig_LocalScope(t *testing.T) {
	fr := &fakeRunner{output: "local@example.com\n"}

	_, err := GitConfig(context.Background(), "user.email", ScopeLocal, Options{Runner: fr})
	if err != nil {
		t.Fatalf("GitConfig() error = %v", err)
	}
	if len(fr.calledArgs) < 2 || fr.calledArgs[1] != "--local" {
		t.Fatalf("runner args = %v, want --local", fr.calledArgs)
	}
}

func TestGitConfig_UnsetKeyIsEmptyNotError(t *testing.T) {
	fr := &fakeRunner{output: "", err: &ExitError{Code: 1}}

	got, err := GitConfig(context.Background(), "user.name", ScopeGlobal, Options{Runner: fr})
	if err != nil {
		t.Fatalf("GitConfig() error = %v, want nil for an unset key", err)
	}
	if got != "" {
		t.Fatalf("GitConfig() = %q, want empty string", got)
	}
}

func TestGitConfig_GitNotInstalled(t *testing.T) {
	fr := &fakeRunner{err: &exec.Error{Name: "git", Err: exec.ErrNotFound}}

	_, err := GitConfig(context.Background(), "user.name", ScopeGlobal, Options{Runner: fr})
	var notInstalled *NotInstalledError
	if !errors.As(err, &notInstalled) {
		t.Fatalf("GitConfig() error = %v (%T), want *NotInstalledError", err, err)
	}
}

func TestSetGitConfig_InvokesGitConfigWithValue(t *testing.T) {
	fr := &fakeRunner{}

	err := SetGitConfig(context.Background(), "user.name", "Jane Doe", ScopeGlobal, Options{Runner: fr})
	if err != nil {
		t.Fatalf("SetGitConfig() error = %v", err)
	}
	wantArgs := []string{"config", "--global", "user.name", "Jane Doe"}
	if len(fr.calledArgs) != len(wantArgs) {
		t.Fatalf("runner args = %v, want %v", fr.calledArgs, wantArgs)
	}
	for i, a := range wantArgs {
		if fr.calledArgs[i] != a {
			t.Fatalf("runner args = %v, want %v", fr.calledArgs, wantArgs)
		}
	}
}

func TestGitIdentity_ReadsNameAndEmail(t *testing.T) {
	calls := 0
	responses := []string{"Jane Doe\n", "jane@example.com\n"}
	fr := &sequenceRunner{responses: responses, onCall: func() { calls++ }}

	id, err := GitIdentity(context.Background(), ScopeGlobal, Options{Runner: fr})
	if err != nil {
		t.Fatalf("GitIdentity() error = %v", err)
	}
	if id.Name != "Jane Doe" || id.Email != "jane@example.com" {
		t.Fatalf("GitIdentity() = %+v", id)
	}
	if calls != 2 {
		t.Fatalf("expected 2 runner calls, got %d", calls)
	}
}

// sequenceRunner returns each of responses in turn across successive calls.
type sequenceRunner struct {
	responses []string
	i         int
	onCall    func()
}

func (s *sequenceRunner) Run(_ context.Context, _ string, _ ...string) (string, error) {
	if s.onCall != nil {
		s.onCall()
	}
	out := s.responses[s.i]
	s.i++
	return out, nil
}
