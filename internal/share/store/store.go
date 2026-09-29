// Package store keeps the two small files file sharing leaves on disk: the
// manifest of what a share created, and the record of a copy that can be
// resumed. Both are JSON, both are written atomically, and neither ever holds
// a password.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// WriteJSON saves v as indented JSON at path. It writes a temporary file in
// the same directory and renames it over the target, so a crash leaves either
// the old file or the new one and never half of one. The file is private to
// the user (mode 0600).
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", filepath.Base(path), err)
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temporary file: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("closing %s: %w", name, err)
	}
	if err := retry(func() error { return os.Rename(name, path) }); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// retryFor bounds how long a rename or a read keeps trying while another
// process has the file open for a moment.
const retryFor = 2 * time.Second

// retry runs op until it succeeds, fails for a reason that will not go away,
// or [retryFor] has passed. On Windows an antivirus scanner or a reader
// that opened the file without FILE_SHARE_DELETE makes a rename over it fail
// with "Access is denied" for a few milliseconds; that is not a reason to
// lose the record.
func retry(op func() error) error {
	deadline := time.Now().Add(retryFor)
	wait := 2 * time.Millisecond
	for {
		err := op()
		if err == nil || !transient(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(wait)
		wait = min(wait*2, 100*time.Millisecond)
	}
}

// readFile reads path through [openShared], so reading never blocks a
// writer's rename.
func readFile(path string) ([]byte, error) {
	var b []byte
	err := retry(func() error {
		f, err := openShared(path)
		if err != nil {
			return err
		}
		defer f.Close() //nolint:errcheck // read-only handle
		b, err = io.ReadAll(f)
		return err
	})
	return b, err
}

// ReadJSON loads path into v. A missing file returns an error that satisfies
// errors.Is(err, os.ErrNotExist), and a file that is not valid JSON returns
// [ErrCorrupt], so callers can tell "nothing saved" from "saved but broken".
func ReadJSON(path string, v any) error {
	b, err := readFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w: %w", filepath.Base(path), ErrCorrupt, err)
	}
	return nil
}

// ErrCorrupt means a saved file exists but cannot be read as JSON.
var ErrCorrupt = errors.New("the saved file is damaged")

// Remove deletes path. A file that is already gone is not an error.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
