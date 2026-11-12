package extract

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"

	"github.com/zubairbinshaukat/devpit/internal/config"
)

// The run log's files (rule 91). This file is the only place the package
// creates or removes a log, and it only ever touches names that match
// logName inside the log folder: <config.CacheDir>\logs, never the archive's
// folder or the output folder.

const (
	// logSubdir is the log folder inside config.CacheDir().
	logSubdir = "logs"
	// logKeep is how many run logs are kept; older ones are deleted.
	logKeep = 30
)

// logName is every name a run log can have: extract-<YYYYMMDD>-<HHMMSS>-<stem>.log,
// the stem reduced to safe characters, with "-<n>" when that second already
// has a log of the same stem. Rotation deletes nothing else.
var logName = regexp.MustCompile(`^extract-\d{8}-\d{6}-[A-Za-z0-9._-]+\.log$`)

// defaultLogDir is Real's Deps.LogDir.
func defaultLogDir() (string, error) {
	d, err := config.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, logSubdir), nil
}

// errLogName is a base name that would not match logName (a bug).
var errLogName = errors.New("extract log: bad file name")

// openLogFile creates a new log named base (+ "-n" when taken) in dir, and
// returns it with its path. It never opens an existing file.
func openLogFile(dir, base string) (*os.File, string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	for n := 1; n <= 99; n++ {
		name := base + ".log"
		if n > 1 {
			name = base + "-" + strconv.Itoa(n) + ".log"
		}
		if !logName.MatchString(name) {
			return nil, "", errLogName
		}
		p := filepath.Join(dir, name)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- a new file named by logName in Devpit's own log folder
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return f, p, nil
	}
	return nil, "", fs.ErrExist
}

// rotateLogs keeps the newest keep run logs in dir and deletes the older
// ones. Only regular files whose names match logName are counted or deleted;
// anything else in the folder is left alone. Names sort by time because the
// timestamp comes first.
func rotateLogs(dir string, keep int) error {
	names, err := osFS{}.ReadDirNames(dir)
	if err != nil {
		return err
	}
	var logs []string
	for _, name := range names {
		if !logName.MatchString(name) {
			continue
		}
		if fi, err := os.Lstat(filepath.Join(dir, name)); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		logs = append(logs, name)
	}
	if len(logs) <= keep {
		return nil
	}
	slices.Sort(logs)
	var first error
	for _, name := range logs[:len(logs)-keep] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && first == nil {
			first = err
		}
	}
	return first
}
