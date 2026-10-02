package accounts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LoadResult is what [LoadFile] returns: a usable store plus anything the
// person should be told about how it was obtained.
type LoadResult struct {
	// Store is never nil.
	Store *Store
	// Path is the file read, or the file that will be written.
	Path string
	// Created is true when there was no file (or it was moved aside) and
	// the store is empty.
	Created bool
	// Warning is a sentence for a person when the file was unusable and was
	// moved aside. It is set once: the next load finds no file.
	Warning string
}

// ReadFile parses accounts.toml and changes nothing on disk, ever: no
// quarantine, no lock. A missing file is an empty store. This is what the
// shim calls; any error makes the shim run the tool untouched.
func ReadFile(path string) (*Store, error) {
	data, err := readShared(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewStore(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	s, err := Parse(data)
	if err != nil {
		var inv *InvalidError
		if errors.As(err, &inv) {
			inv.Path = path
		}
		return nil, err
	}
	return s, nil
}

// LoadFile reads accounts.toml for the app. Like config.Load, it never hands
// back an unusable store: a missing file is an empty store with Created set;
// a file that is not TOML is renamed to accounts.toml.broken-<unix> and an
// empty store comes back with Warning set. A file with an unknown key or a
// bad value ([*InvalidError]) or from a newer Devpit ([ErrNewerSchema]) is
// left exactly where it is and returned as an error, so nothing is ever
// partly applied or silently dropped.
func LoadFile(path string) (LoadResult, error) {
	res := LoadResult{Store: NewStore(), Path: path}
	data, err := readShared(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		res.Created = true
		return res, nil
	case err != nil:
		return res, fmt.Errorf("reading %s: %w", path, err)
	}
	s, perr := Parse(data)
	if perr == nil {
		res.Store = s
		return res, nil
	}
	if !errors.Is(perr, errSyntax) {
		var inv *InvalidError
		if errors.As(perr, &inv) {
			inv.Path = path
		}
		return res, perr
	}
	broken, qerr := Quarantine(path)
	if qerr != nil {
		res.Warning = fmt.Sprintf(
			"Your accounts file at %s could not be read and could not be moved aside, so every tool uses its default account for now. Read error: %v. Move error: %v.",
			path, perr, qerr)
		return res, nil
	}
	res.Warning = fmt.Sprintf(
		"Your accounts file could not be read, so it was moved to %s and Devpit started with no rules. Your sign-ins were not touched; every tool uses its default account until you add rules again.",
		filepath.Base(broken))
	res.Created = true
	return res, nil
}

// Quarantine renames a damaged file aside as <name>.broken-<unix> and
// returns the new path.
func Quarantine(path string) (string, error) {
	broken := fmt.Sprintf("%s.broken-%d", path, time.Now().Unix())
	if err := os.Rename(path, broken); err != nil {
		return "", err
	}
	return broken, nil
}

// SaveFile writes the store atomically. Callers that change the store go
// through [Engine], which holds the lock and journals the change; SaveFile
// itself takes no lock.
func SaveFile(path string, s *Store) error {
	s.Version = SchemaVersion
	data, err := Encode(s)
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}
