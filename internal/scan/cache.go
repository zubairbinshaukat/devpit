package scan

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CacheVersion is the on-disk format version of scan.gob. A file written by
// a different version is discarded rather than reinterpreted: a stale cache
// is worth exactly one rescan, and guessing at an old layout is worth less
// than that.
const CacheVersion = 1

// CacheFileName is the base name of the cache file inside the cache
// directory, which is %LOCALAPPDATA%\devpit by default.
const CacheFileName = "scan.gob"

// Cache reasons about the cache file.
var (
	// ErrNoCache means there is no usable cached scan for that root. It is an
	// ordinary outcome, not a failure: the caller simply scans.
	ErrNoCache = errors.New("scan: no cached scan for this folder")
	// ErrCorruptCache means the cache file could not be decoded. The file has
	// been renamed aside by the time this is returned.
	ErrCorruptCache = errors.New("scan: the cache file was unreadable and was moved aside")
)

// Cache is one root's last scan, shown immediately at startup with a "stale"
// badge while a fresh scan runs behind it. That is the whole trick behind
// being on screen in under 100 ms.
type Cache struct {
	// Version is the format version this entry was written with.
	Version int
	// Root is the folder that was scanned, as an absolute path.
	Root string
	// Items are the results of that scan.
	Items []Item
	// When is the moment the scan finished, used for the staleness badge.
	When time.Time
}

// Bytes returns the total size of the cached items.
func (c *Cache) Bytes() uint64 {
	var total uint64
	for i := range c.Items {
		total += c.Items[i].Size
	}
	return total
}

// cacheFile is the whole file: one entry per root, so scanning a second
// folder never throws away the first folder's results.
type cacheFile struct {
	Version int
	Entries map[string]Cache
}

// cacheKey normalises a root path into a map key. Windows paths are
// case-insensitive, so "D:\Work" and "d:\work" are one entry.
func cacheKey(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return strings.ToLower(normalizePath(abs))
}

// LoadCache returns the cached scan for root from <dir>/scan.gob.
//
// A missing file, a file from another format version, or a file with no entry
// for this root all come back as ErrNoCache. A file that exists but cannot be
// decoded is renamed to scan.gob.broken-<unix> and comes back as
// ErrCorruptCache, so a bad cache costs the user one rescan and a sentence,
// never a crash and never silence.
func LoadCache(dir, root string) (*Cache, error) {
	path := filepath.Join(dir, CacheFileName)

	data, err := os.ReadFile(path) //nolint:gosec // The path is Devpit's own cache directory.
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, ErrNoCache
	case err != nil:
		return nil, fmt.Errorf("scan: reading %s: %w", path, err)
	}

	file, derr := decodeCacheFile(data)
	if derr != nil {
		broken, rerr := quarantineCache(path)
		if rerr != nil {
			return nil, fmt.Errorf("scan: %s could not be decoded (%s) and could not be moved aside: %w", path, derr.Error(), rerr)
		}
		return nil, fmt.Errorf("scan: %s: %w", filepath.Base(broken), ErrCorruptCache)
	}

	entry, ok := file.Entries[cacheKey(root)]
	if !ok || entry.Version != CacheVersion {
		return nil, ErrNoCache
	}
	return &entry, nil
}

// SaveCache writes c into <dir>/scan.gob, replacing that root's entry and
// leaving every other root's entry alone.
//
// The write is atomic: a temporary file in the same directory followed by a
// rename, so a crash mid-write can never leave a half-encoded cache that the
// next start would have to quarantine.
func SaveCache(dir string, c *Cache) error {
	if c == nil {
		return errors.New("scan: nothing to save")
	}
	if strings.TrimSpace(c.Root) == "" {
		return errors.New("scan: a cache entry needs a root")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("scan: creating %s: %w", dir, err)
	}
	path := filepath.Join(dir, CacheFileName)

	file := cacheFile{Version: CacheVersion, Entries: map[string]Cache{}}
	if data, err := os.ReadFile(path); err == nil { //nolint:gosec // Devpit's own cache directory.
		if existing, derr := decodeCacheFile(data); derr == nil && existing.Version == CacheVersion {
			file.Entries = existing.Entries
		} else if derr != nil {
			if _, rerr := quarantineCache(path); rerr != nil {
				return fmt.Errorf("scan: moving the unreadable cache aside: %w", rerr)
			}
		}
	}

	entry := *c
	entry.Version = CacheVersion
	if entry.When.IsZero() {
		entry.When = time.Now()
	}
	file.Entries[cacheKey(entry.Root)] = entry

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(file); err != nil {
		return fmt.Errorf("scan: encoding the scan cache: %w", err)
	}

	tmp, err := os.CreateTemp(dir, CacheFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("scan: creating a temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Harmless once the rename below has succeeded.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("scan: writing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("scan: closing %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("scan: replacing %s: %w", path, err)
	}
	return nil
}

// decodeCacheFile decodes the whole cache file.
func decodeCacheFile(data []byte) (cacheFile, error) {
	var file cacheFile
	if len(data) == 0 {
		return file, errors.New("scan: the cache file is empty")
	}
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&file); err != nil {
		return cacheFile{}, fmt.Errorf("scan: decoding the scan cache: %w", err)
	}
	if file.Entries == nil {
		file.Entries = map[string]Cache{}
	}
	return file, nil
}

// quarantineCache renames a bad cache file aside and returns its new path.
func quarantineCache(path string) (string, error) {
	broken := fmt.Sprintf("%s.broken-%d", path, time.Now().Unix())
	if err := os.Rename(path, broken); err != nil {
		return "", err
	}
	return broken, nil
}
