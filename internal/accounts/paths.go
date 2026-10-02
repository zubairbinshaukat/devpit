package accounts

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zubairbinshaukat/devpit/internal/config"
)

// File names inside the config directory.
const (
	StoreFileName   = "accounts.toml"
	JournalFileName = "accounts-journal.jsonl"
	LockFileName    = "accounts.lock"
)

// EnvAccountsDir relocates %USERPROFILE%\.devpit\accounts, for tests and
// portable installs, the way DEVPIT_CONFIG_DIR relocates the settings.
const EnvAccountsDir = "DEVPIT_ACCOUNTS_DIR"

// Paths is where the accounts feature keeps its files.
type Paths struct {
	// ConfigDir is %APPDATA%\devpit (or DEVPIT_CONFIG_DIR).
	ConfigDir string
	// Store is accounts.toml.
	Store string
	// Journal is accounts-journal.jsonl.
	Journal string
	// Lock is the lock file two Devpit windows share.
	Lock string
	// AccountsDir is %USERPROFILE%\.devpit\accounts: the account folders
	// Devpit creates, one per tool and name. Outside the cache, outside
	// Roaming and outside OneDrive, and never removed by uninstall.
	AccountsDir string
}

// PathsIn returns the paths for a given config directory and accounts
// directory. Tests pass two temporary directories.
func PathsIn(configDir, accountsDir string) Paths {
	return Paths{
		ConfigDir:   configDir,
		Store:       filepath.Join(configDir, StoreFileName),
		Journal:     filepath.Join(configDir, JournalFileName),
		Lock:        filepath.Join(configDir, LockFileName),
		AccountsDir: accountsDir,
	}
}

// DefaultPaths returns the real locations, honouring DEVPIT_CONFIG_DIR and
// DEVPIT_ACCOUNTS_DIR. It creates nothing.
func DefaultPaths() (Paths, error) {
	dir, err := config.Dir()
	if err != nil {
		return Paths{}, err
	}
	acc, err := AccountsDir()
	if err != nil {
		return Paths{}, err
	}
	return PathsIn(dir, acc), nil
}

// StorePath returns the path of accounts.toml alone: all the shim needs.
func StorePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, StoreFileName), nil
}

// AccountsDir returns %USERPROFILE%\.devpit\accounts, or DEVPIT_ACCOUNTS_DIR.
func AccountsDir() (string, error) {
	if d := os.Getenv(EnvAccountsDir); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("accounts: locating your home folder: %w", err)
	}
	return filepath.Join(home, ".devpit", "accounts"), nil
}

// AccountDir is the folder Devpit creates for a new account:
// <AccountsDir>\<tool>\<name>.
func (p Paths) AccountDir(t Tool, name string) string {
	return filepath.Join(p.AccountsDir, string(t), name)
}

// GitDir is the folder Devpit's Git files live in (rules.gitconfig, one file
// per identity, devpit.json with signing, SSH-key and credential-helper
// settings): next to the accounts folder, so DEVPIT_ACCOUNTS_DIR moves both.
func (p Paths) GitDir() string {
	if p.AccountsDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(p.AccountsDir), "git")
}

// ProtectedRoots lists the folders that hold sign-ins and must never be
// selected by Clean: Devpit's accounts folder, its Git folder, and every
// account folder in the store, wherever it lives (imported accounts stay
// where they were). It reads the store without changing anything and never
// fails: what it cannot read it leaves out, and the accounts and Git
// folders are always listed. See [Protected] for the whole list.
func ProtectedRoots() []string {
	p, err := DefaultPaths()
	if err != nil {
		return nil
	}
	return p.ProtectedRoots()
}

// ProtectedRoots is [ProtectedRoots] for these paths.
func (p Paths) ProtectedRoots() []string {
	out := []string{p.AccountsDir, p.GitDir()}
	if s, err := ReadFile(p.Store); err == nil {
		out = append(out, s.AccountDirs()...)
	}
	return out
}
