package gitssh

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Scope selects which git config file a read or write applies to.
type Scope int

const (
	// ScopeGlobal reads/writes ~/.gitconfig (`git config --global`).
	ScopeGlobal Scope = iota
	// ScopeLocal reads/writes the repository's .git/config
	// (`git config --local`). The runner's working directory determines
	// which repository.
	ScopeLocal
)

func (s Scope) flag() string {
	if s == ScopeLocal {
		return "--local"
	}
	return "--global"
}

// Identity holds a Git user identity.
type Identity struct {
	Name  string
	Email string
}

// GitConfig reads a single git config key. If the key is simply unset, it
// returns "" with a nil error, matching git's own exit-1-on-unset
// behaviour. If git itself cannot be found, it returns a typed
// *NotInstalledError.
func GitConfig(ctx context.Context, key string, scope Scope, opts Options) (string, error) {
	out, err := opts.runner().Run(ctx, "git", "config", scope.flag(), key)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", &NotInstalledError{Tool: "git", Err: err}
		}
		var exitErr *ExitError
		if errors.As(err, &exitErr) && exitErr.Code == 1 {
			// `git config` exits 1 with no stderr when the key is unset.
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// SetGitConfig writes a single git config key.
func SetGitConfig(ctx context.Context, key, value string, scope Scope, opts Options) error {
	_, err := opts.runner().Run(ctx, "git", "config", scope.flag(), key, value)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return &NotInstalledError{Tool: "git", Err: err}
		}
		return err
	}
	return nil
}

// GitIdentity reads user.name and user.email from the given scope.
func GitIdentity(ctx context.Context, scope Scope, opts Options) (Identity, error) {
	name, err := GitConfig(ctx, "user.name", scope, opts)
	if err != nil {
		return Identity{}, err
	}
	email, err := GitConfig(ctx, "user.email", scope, opts)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Name: name, Email: email}, nil
}
