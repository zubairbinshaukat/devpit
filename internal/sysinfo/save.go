package sysinfo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReportFileName is "PC specs - <Maker Model> - <date>.md", with anything
// Windows will not take in a file name removed.
func ReportFileName(title string, day time.Time) string {
	var b strings.Builder
	for _, r := range title {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			continue
		}
		b.WriteRune(r)
	}
	t := strings.Trim(strings.Join(strings.Fields(b.String()), " "), ". ")
	if len(t) > 80 {
		t = strings.TrimSpace(t[:80])
	}
	if t == "" {
		t = "This PC"
	}
	return "PC specs - " + t + " - " + day.Format("2006-01-02") + ".md"
}

// SaveNew writes data to dir/name and never replaces a file that is there:
// a taken name becomes "name (2).md", "name (3).md" and so on. The final
// name is claimed first (created exclusively), the data goes to a temporary
// file beside it, and the temporary file is then renamed over the claim, so
// a half-written report is never left under the real name. It returns the
// full path written.
func SaveNew(dir, name string, data []byte) (string, error) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	ext := filepath.Ext(name)
	for n := 1; n < 100; n++ {
		cand := name
		if n > 1 {
			cand = fmt.Sprintf("%s (%d)%s", base, n, ext)
		}
		path := filepath.Join(dir, cand)
		claim, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_ = claim.Close()
		if err := writeThenRename(dir, path, data); err != nil {
			_ = os.Remove(path)
			return "", err
		}
		return path, nil
	}
	return "", errors.New("too many reports with that name already")
}

// writeThenRename writes data to a temporary file in dir and renames it to
// path, which the caller has just created and owns.
func writeThenRename(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".devpit-specs-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
