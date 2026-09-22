package gitssh

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestKeygen_RefusesToOverwriteExistingPrivateKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(path, []byte("existing-private-key"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	fr := &fakeRunner{}
	_, err := Keygen(context.Background(), KeygenOptions{Path: path}, Options{Runner: fr})

	var existsErr *ExistsError
	if !errors.As(err, &existsErr) {
		t.Fatalf("Keygen() error = %v (%T), want *ExistsError", err, err)
	}
	if fr.calls != 0 {
		t.Fatalf("ssh-keygen runner was invoked %d times, want 0 (safety.md #15: never overwrite without force)", fr.calls)
	}
	// The original file must be untouched.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back key: %v", err)
	}
	if string(got) != "existing-private-key" {
		t.Fatalf("private key file was modified: %q", got)
	}
}

func TestKeygen_RefusesToOverwriteExistingPublicKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")
	pub := path + ".pub"
	if err := os.WriteFile(pub, []byte("existing-public-key"), 0o644); err != nil { //nolint:gosec // test fixture permissions are irrelevant.
		t.Fatalf("setup: %v", err)
	}

	fr := &fakeRunner{}
	_, err := Keygen(context.Background(), KeygenOptions{Path: path}, Options{Runner: fr})

	var existsErr *ExistsError
	if !errors.As(err, &existsErr) {
		t.Fatalf("Keygen() error = %v (%T), want *ExistsError", err, err)
	}
	if fr.calls != 0 {
		t.Fatalf("ssh-keygen runner was invoked %d times, want 0", fr.calls)
	}
}

func TestKeygen_ForceOverwritesExistingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")
	pub := path + ".pub"
	if err := os.WriteFile(path, []byte("old-private"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// The fake runner stands in for ssh-keygen: it writes the .pub file
	// itself, the way the real tool would, so PublicKey can read it back.
	fr := &writingRunner{writePath: pub, writeContent: "ssh-ed25519 AAAA... comment\n"}

	result, err := Keygen(context.Background(), KeygenOptions{Path: path, Force: true}, Options{Runner: fr})
	if err != nil {
		t.Fatalf("Keygen() error = %v", err)
	}
	if fr.calls != 1 {
		t.Fatalf("ssh-keygen runner was invoked %d times, want 1", fr.calls)
	}
	if result.PublicKey != "ssh-ed25519 AAAA... comment" {
		t.Fatalf("Keygen() PublicKey = %q", result.PublicKey)
	}
	if result.Path != path {
		t.Fatalf("Keygen() Path = %q, want %q", result.Path, path)
	}
}

func TestKeygen_NoExistingKeyRunsNormally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")
	pub := path + ".pub"

	fr := &writingRunner{writePath: pub, writeContent: "ssh-ed25519 AAAA... me@example.com\n"}

	result, err := Keygen(context.Background(), KeygenOptions{Path: path, Comment: "me@example.com"}, Options{Runner: fr})
	if err != nil {
		t.Fatalf("Keygen() error = %v", err)
	}
	if fr.calls != 1 {
		t.Fatalf("ssh-keygen runner was invoked %d times, want 1", fr.calls)
	}
	if result.PublicKey == "" {
		t.Fatal("Keygen() PublicKey is empty")
	}
}

func TestKeygen_SSHKeygenNotInstalled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")

	fr := &fakeRunner{err: &exec.Error{Name: "ssh-keygen", Err: exec.ErrNotFound}}
	_, err := Keygen(context.Background(), KeygenOptions{Path: path}, Options{Runner: fr})

	var notInstalled *NotInstalledError
	if !errors.As(err, &notInstalled) {
		t.Fatalf("Keygen() error = %v (%T), want *NotInstalledError", err, err)
	}
}

func TestDefaultKeyPath(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\tester`)

	if got, want := DefaultKeyPath("ed25519"), filepath.Join(`C:\Users\tester`, ".ssh", "id_ed25519"); got != want {
		t.Fatalf("DefaultKeyPath(ed25519) = %q, want %q", got, want)
	}
	if got, want := DefaultKeyPath("rsa"), filepath.Join(`C:\Users\tester`, ".ssh", "id_rsa"); got != want {
		t.Fatalf("DefaultKeyPath(rsa) = %q, want %q", got, want)
	}
}

func TestKeyExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id_ed25519")

	if KeyExists(path) {
		t.Fatal("KeyExists() = true before the file was created")
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if !KeyExists(path) {
		t.Fatal("KeyExists() = false after the file was created")
	}
}

// writingRunner simulates ssh-keygen by writing the .pub file it is asked
// to produce, without executing anything.
type writingRunner struct {
	writePath    string
	writeContent string
	calls        int
}

func (w *writingRunner) Run(_ context.Context, _ string, _ ...string) (string, error) {
	w.calls++
	if err := os.WriteFile(w.writePath, []byte(w.writeContent), 0o600); err != nil {
		return "", err
	}
	return "", nil
}
