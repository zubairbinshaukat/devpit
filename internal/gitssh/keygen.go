package gitssh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultKeyPath returns the conventional private-key path for the given
// algorithm ("ed25519" or "rsa"; anything else falls back to ed25519),
// rooted at %USERPROFILE%\.ssh (or the OS home directory if USERPROFILE is
// unset, so the function still behaves sensibly on non-Windows platforms).
func DefaultKeyPath(alg string) string {
	name := "id_ed25519"
	if alg == "rsa" {
		name = "id_rsa"
	}
	return filepath.Join(sshDir(), name)
}

func sshDir() string {
	home := os.Getenv("USERPROFILE")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	return filepath.Join(home, ".ssh")
}

// KeyExists reports whether a file exists at path.
func KeyExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// KeygenOptions configures [Keygen].
type KeygenOptions struct {
	// Path is the private key path. Defaults to DefaultKeyPath(Algorithm)
	// when empty.
	Path string
	// Comment is passed as `-C` to ssh-keygen (typically an email address).
	Comment string
	// Algorithm is passed as `-t` to ssh-keygen. Defaults to "ed25519".
	Algorithm string
	// Force allows overwriting an existing key. Safety rule: without it,
	// Keygen refuses to run ssh-keygen at all when a key already exists.
	Force bool
}

// KeygenResult is the outcome of a successful key generation.
type KeygenResult struct {
	PublicKey string
	Path      string
}

// ExistsError reports that a key already exists at Path and Force was not
// set, so Keygen refused to touch it.
type ExistsError struct {
	Path string
}

func (e *ExistsError) Error() string {
	return fmt.Sprintf("gitssh: a key already exists at %s (pass Force to overwrite it)", e.Path)
}

// Keygen generates an SSH key pair with ssh-keygen.
//
// Safety rule (docs/safety.md #15): if the private key or its .pub file
// already exists and opts.Force is false, Keygen returns a typed
// *ExistsError and never runs ssh-keygen — an existing key is never
// silently overwritten.
func Keygen(ctx context.Context, opts KeygenOptions, ropts Options) (KeygenResult, error) {
	alg := opts.Algorithm
	if alg == "" {
		alg = "ed25519"
	}

	path := opts.Path
	if path == "" {
		path = DefaultKeyPath(alg)
	}
	pubPath := path + ".pub"

	if !opts.Force {
		if KeyExists(path) {
			return KeygenResult{}, &ExistsError{Path: path}
		}
		if KeyExists(pubPath) {
			return KeygenResult{}, &ExistsError{Path: pubPath}
		}
	} else {
		// ssh-keygen prompts "Overwrite (y/n)?" on stdin when the target
		// already exists, and nothing here wires up a TTY to answer it, so
		// an explicit Force removes the old files first rather than
		// letting the process hang.
		_ = os.Remove(path)
		_ = os.Remove(pubPath)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return KeygenResult{}, fmt.Errorf("gitssh: creating %s: %w", dir, err)
	}
	// Best-effort: MkdirAll only applies the mode to directories it
	// creates, not ones that already existed with looser permissions. 0700
	// is the correct, restrictive mode for ~/.ssh; gosec's G302 wants file
	// modes at 0600 or below, but that check does not distinguish
	// directories (which need the execute bit to be traversable) from
	// regular files.
	_ = os.Chmod(dir, 0o700) //nolint:gosec // directory, needs execute bit; 0700 is the correct restrictive mode.

	args := []string{"-t", alg, "-C", opts.Comment, "-f", path, "-N", ""}
	if _, err := ropts.runner().Run(ctx, "ssh-keygen", args...); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return KeygenResult{}, &NotInstalledError{Tool: "ssh-keygen", Err: err}
		}
		return KeygenResult{}, err
	}

	pub, err := PublicKey(pubPath)
	if err != nil {
		return KeygenResult{}, err
	}
	return KeygenResult{PublicKey: pub, Path: path}, nil
}

// PublicKey reads and returns the trimmed contents of the .pub file at
// path.
func PublicKey(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path is an SSH key path built from a user-chosen directory, not attacker input.
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
