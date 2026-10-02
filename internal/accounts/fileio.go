package accounts

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// writeAtomic saves data at path through a temporary file in the same
// directory and a rename over the target, so a reader (the shim, a second
// Devpit) sees either the old file or the new one and never half of one.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	name := tmp.Name()
	// Harmless once the rename has succeeded.
	defer func() { _ = os.Remove(name) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("flushing %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", name, err)
	}
	if err := retry(func() error { return os.Rename(name, path) }); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// readShared reads path with sharing flags that never block a writer's
// rename (see openShared), retrying briefly while another process holds the
// file for a moment.
func readShared(path string) ([]byte, error) {
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

// retryFor bounds how long a rename or a read keeps trying while an
// antivirus scanner or another reader has the file open.
const retryFor = 2 * time.Second

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
