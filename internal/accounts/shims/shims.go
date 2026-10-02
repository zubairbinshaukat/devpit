// Package shims manages Devpit's shim folder,
// %LOCALAPPDATA%\Programs\devpit\shims: one copy of devpit-shim.exe per
// tool that needs one (claude.exe, gh.exe…), and that folder at the front
// of the user PATH.
//
// Nothing is created up front. A shim appears when a tool gets its second
// account or its first rule (accounts.Store.NeedsShim), so a tool with no
// rules has no footprint. Everything this package adds is written to a
// record (shims.json in Devpit's settings folder) so cleanup removes
// exactly that and nothing else.
package shims

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/launch"
	"github.com/zubairbinshaukat/devpit/internal/config"
)

// SourceName is the shim binary shipped next to devpit.exe.
const SourceName = "devpit-shim.exe"

// RecordName is the record of what Devpit added, in the settings folder.
const RecordName = "shims.json"

// EnvShimDir relocates the shim folder, for tests and portable installs.
// While it is set, Devpit leaves the user PATH alone: whoever set it puts
// that folder on PATH themselves.
const EnvShimDir = "DEVPIT_SHIM_DIR"

// DefaultDir returns %LOCALAPPDATA%\Programs\devpit\shims, or
// DEVPIT_SHIM_DIR.
func DefaultDir() (string, error) {
	if d := os.Getenv(EnvShimDir); d != "" {
		return d, nil
	}
	base, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
	if err != nil {
		return "", fmt.Errorf("shims: locating %%LOCALAPPDATA%%: %w", err)
	}
	return filepath.Join(base, "Programs", "devpit", "shims"), nil
}

// PathStore reads and writes the user's own PATH (HKCU\Environment\Path),
// keeping its registry type: REG_EXPAND_SZ stays REG_EXPAND_SZ.
type PathStore interface {
	// Read returns the stored value unexpanded, whether it is
	// REG_EXPAND_SZ, and whether it exists at all.
	Read() (value string, expand, exists bool, err error)
	// Write stores value with the given type.
	Write(value string, expand bool) error
}

// Manager does the work. Every field has a real default from New; tests
// set their own.
type Manager struct {
	// Dir is the shim folder.
	Dir string
	// Source is devpit-shim.exe, next to devpit.exe.
	Source string
	// Record is shims.json.
	Record string
	// Path is the user PATH in the registry. nil means Devpit does not
	// manage the PATH (DEVPIT_SHIM_DIR is set): adding and removing the
	// entry do nothing, and Check does not ask for it.
	Path PathStore
	// Broadcast tells running programs the environment changed
	// (WM_SETTINGCHANGE). nil does nothing.
	Broadcast func()
	// Getenv reads the current process's environment.
	Getenv func(string) string

	// rename is os.Rename when nil; tests make it fail to check that a
	// shim is never left missing.
	rename func(oldpath, newpath string) error
}

// New returns a Manager with the real locations.
func New() (*Manager, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Dir()
	if err != nil {
		return nil, err
	}
	m := &Manager{
		Dir:       dir,
		Source:    filepath.Join(filepath.Dir(exe), SourceName),
		Record:    filepath.Join(cfg, RecordName),
		Path:      UserPath(),
		Broadcast: BroadcastEnvironment,
		Getenv:    os.Getenv,
	}
	if os.Getenv(EnvShimDir) != "" {
		m.Path, m.Broadcast = nil, nil
	}
	return m, nil
}

// record is what Devpit added.
type record struct {
	// PathEntry is the folder Devpit put on the user PATH, if it did.
	PathEntry string `json:"path_entry,omitempty"`
	// Shims are the shim files Devpit created.
	Shims []shimRecord `json:"shims,omitempty"`
}

type shimRecord struct {
	Tool   accounts.Tool `json:"tool"`
	File   string        `json:"file"`
	SHA256 string        `json:"sha256"`
}

func (m *Manager) readRecord() (record, error) {
	var r record
	data, err := os.ReadFile(m.Record)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return record{}, fmt.Errorf("the record of Devpit's shims (%s) is damaged: %w", m.Record, err)
	}
	return r, nil
}

func (m *Manager) writeRecord(r record) error {
	if r.PathEntry == "" && len(r.Shims) == 0 {
		if err := os.Remove(m.Record); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(m.Record, data)
}

// ShimPath is where the shim for t lives.
func (m *Manager) ShimPath(t accounts.Tool) string {
	return filepath.Join(m.Dir, t.ShimName()+".exe")
}

// ErrProgramMissing is wrapped by every error that comes from
// devpit-shim.exe missing next to devpit.exe (someone replaced only
// devpit.exe by hand). ProgramMissingFix is what to do about it.
var ErrProgramMissing = accounts.ErrShimProgramMissing

// ProgramMissingFix is the one-sentence fix for ErrProgramMissing.
const ProgramMissingFix = "Run the installer again, or put devpit-shim.exe next to devpit.exe."

// source is devpit-shim.exe as it is now: its hash and size, worked out once
// per call that needs them.
type source struct {
	sum  string
	size int64
}

// readSource hashes devpit-shim.exe, or returns an error wrapping
// ErrProgramMissing (with the fix) when it is not there.
func (m *Manager) readSource() (source, error) {
	fi, err := os.Stat(m.Source)
	if err != nil {
		return source{}, fmt.Errorf("%w (%s). %s", ErrProgramMissing, m.Source, ProgramMissingFix)
	}
	sum, err := fileHash(m.Source)
	if err != nil {
		return source{}, fmt.Errorf("reading %s: %w", m.Source, err)
	}
	return source{sum: sum, size: fi.Size()}, nil
}

// CheckSource returns nil when devpit-shim.exe is next to devpit.exe, and an
// error wrapping ErrProgramMissing, with the fix, when it is not.
func (m *Manager) CheckSource() error {
	if _, err := os.Stat(m.Source); err != nil {
		return fmt.Errorf("%w (%s). %s", ErrProgramMissing, m.Source, ProgramMissingFix)
	}
	return nil
}

// Ensure creates the shim for t if it is missing, or replaces it if it is
// an older copy of devpit-shim.exe. created is true when a file was written.
func (m *Manager) Ensure(t accounts.Tool) (bool, error) {
	if t.ShimName() == "" {
		return false, fmt.Errorf("%s is never shimmed", t.DisplayName())
	}
	src, err := m.readSource()
	if err != nil {
		return false, err
	}
	return m.ensure(t, src)
}

// ensure is Ensure with devpit-shim.exe already read. A copy whose size
// differs from the source is out of date without hashing it.
func (m *Manager) ensure(t accounts.Tool, src source) (bool, error) {
	if t.ShimName() == "" {
		return false, fmt.Errorf("%s is never shimmed", t.DisplayName())
	}
	dst := m.ShimPath(t)
	if fi, err := os.Stat(dst); err == nil && fi.Size() == src.size {
		if have, err := fileHash(dst); err == nil && have == src.sum {
			return false, m.remember(t, dst, src.sum)
		}
	}
	if err := os.MkdirAll(m.Dir, 0o755); err != nil { //nolint:gosec // a program folder others may read
		return false, err
	}
	m.sweepOld()
	if err := copyExe(m.Source, dst, m.rename); err != nil {
		return false, err
	}
	return true, m.remember(t, dst, src.sum)
}

func (m *Manager) remember(t accounts.Tool, file, sum string) error {
	r, err := m.readRecord()
	if err != nil {
		return err
	}
	r.Shims = slices.DeleteFunc(r.Shims, func(s shimRecord) bool { return s.Tool == t })
	r.Shims = append(r.Shims, shimRecord{Tool: t, File: file, SHA256: sum})
	return m.writeRecord(r)
}

// Remove deletes the shim for t, if Devpit made it.
func (m *Manager) Remove(t accounts.Tool) error {
	r, err := m.readRecord()
	if err != nil {
		return err
	}
	dst := m.ShimPath(t)
	if err := m.removeShim(dst, r); err != nil {
		return err
	}
	r.Shims = slices.DeleteFunc(r.Shims, func(s shimRecord) bool { return s.Tool == t })
	return m.writeRecord(r)
}

// removeShim removes a shim file only if it is one of Devpit's: its hash is
// the recorded one or the current devpit-shim.exe's. A file someone else
// put there is left alone.
func (m *Manager) removeShim(path string, r record) error {
	have, err := fileHash(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	ours := false
	if src, err := fileHash(m.Source); err == nil && src == have {
		ours = true
	}
	for _, s := range r.Shims {
		if strings.EqualFold(s.File, path) && s.SHA256 == have {
			ours = true
		}
	}
	if !ours {
		return fmt.Errorf("%s is not a copy of Devpit's shim, so it was left alone", path)
	}
	if err := os.Remove(path); err != nil {
		// A running shim cannot be deleted, but it can be renamed aside and
		// swept up next time.
		old := path + ".old-" + randHex()
		if rerr := os.Rename(path, old); rerr != nil {
			return fmt.Errorf("removing %s: %w (is the tool running?)", path, err)
		}
	}
	return nil
}

// Refresh replaces every recorded shim that is an older devpit-shim.exe
// (after a Devpit update) and returns the tools refreshed.
func (m *Manager) Refresh() ([]accounts.Tool, error) {
	r, err := m.readRecord()
	if err != nil {
		return nil, err
	}
	if len(r.Shims) == 0 {
		return nil, nil
	}
	src, err := m.readSource()
	if err != nil {
		return nil, err
	}
	var out []accounts.Tool
	for _, s := range r.Shims {
		created, err := m.ensure(s.Tool, src)
		if err != nil {
			return out, err
		}
		if created {
			out = append(out, s.Tool)
		}
	}
	return out, nil
}

// RefreshUnneeded refreshes the recorded shims of tools that no longer need
// one in st (Sync already keeps the needed ones current): a shim with no
// rule still starts its tool, so it must not stay an old copy after an
// update. It returns the tools refreshed.
func (m *Manager) RefreshUnneeded(st *accounts.Store) ([]accounts.Tool, error) {
	r, err := m.readRecord()
	if err != nil {
		return nil, err
	}
	var todo []accounts.Tool
	for _, s := range r.Shims {
		if !st.NeedsShim(s.Tool) {
			todo = append(todo, s.Tool)
		}
	}
	if len(todo) == 0 {
		return nil, nil
	}
	src, err := m.readSource()
	if err != nil {
		return nil, err
	}
	var out []accounts.Tool
	for _, t := range todo {
		created, err := m.ensure(t, src)
		if err != nil {
			return out, err
		}
		if created {
			out = append(out, t)
		}
	}
	return out, nil
}

// Sync makes the shims match the store: a shim for every tool that needs
// one, and the shim folder on the user PATH when any shim exists. It never
// removes a shim (a shim with no rule just starts the tool untouched);
// cleanup does that.
func (m *Manager) Sync(s *accounts.Store) (created []accounts.Tool, pathChanged bool, err error) {
	m.sweepOld() // shims renamed aside while running, by an earlier refresh
	var src *source
	for _, t := range accounts.Tools() {
		if !s.NeedsShim(t) {
			continue
		}
		if src == nil {
			got, serr := m.readSource()
			if serr != nil {
				return created, false, serr
			}
			src = &got
		}
		c, ensureErr := m.ensure(t, *src)
		if ensureErr != nil {
			return created, false, ensureErr
		}
		if c {
			created = append(created, t)
		}
	}
	r, err := m.readRecord()
	if err != nil || len(r.Shims) == 0 {
		return created, false, err
	}
	changed, err := m.AddToPath()
	return created, changed, err
}

// copyExe copies src to dst through a temporary file and a rename. If dst
// is running (Windows will not overwrite it), the old file is renamed aside
// first and removed on a later run.
func copyExe(src, dst string, rename func(oldpath, newpath string) error) error {
	if rename == nil {
		rename = os.Rename
	}
	in, err := os.Open(src) // #nosec G304 -- devpit-shim.exe next to devpit.exe
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // read-only
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := rename(name, dst); err != nil {
		// A running shim cannot be replaced, but it can be renamed aside
		// (and swept up by a later run). If the new copy then cannot take its
		// place, the old one goes back, so the tool always has a shim.
		old := dst + ".old-" + randHex()
		if rerr := rename(dst, old); rerr != nil {
			return fmt.Errorf("replacing %s: %w (is the tool running?)", dst, err)
		}
		if ferr := rename(name, dst); ferr != nil {
			if berr := rename(old, dst); berr != nil {
				return fmt.Errorf("replacing %s: %w; putting the old shim back from %s also failed: %w", dst, ferr, old, berr)
			}
			return fmt.Errorf("replacing %s: %w; the old shim is back in place", dst, ferr)
		}
	}
	return nil
}

// sweepOld removes shims renamed aside while they were running.
func (m *Manager) sweepOld() {
	entries, err := os.ReadDir(m.Dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".exe.old-") {
			_ = os.Remove(filepath.Join(m.Dir, e.Name()))
		}
	}
}

// AddToPath puts the shim folder first on the user PATH, keeping every
// other entry exactly as it was and the value's registry type. It is
// idempotent: already first means no write. Running programs are told the
// environment changed; terminals that are already open keep their old PATH.
func (m *Manager) AddToPath() (bool, error) {
	if m.Path == nil {
		return false, nil
	}
	val, expand, exists, err := m.Path.Read()
	if err != nil {
		return false, err
	}
	if !exists {
		expand = true
	}
	next, changed := PrependEntry(val, m.Dir, m.getenv)
	if !changed {
		return false, m.notePathEntry()
	}
	if err := m.Path.Write(next, expand); err != nil {
		return false, err
	}
	if m.Broadcast != nil {
		m.Broadcast()
	}
	return true, m.notePathEntry()
}

func (m *Manager) notePathEntry() error {
	r, err := m.readRecord()
	if err != nil {
		return err
	}
	if r.PathEntry == m.Dir {
		return nil
	}
	r.PathEntry = m.Dir
	return m.writeRecord(r)
}

// RemoveFromPath takes the shim folder off the user PATH, every spelling of
// it, and leaves every other entry exactly as it was.
func (m *Manager) RemoveFromPath() (bool, error) {
	if m.Path == nil {
		return false, nil
	}
	val, expand, exists, err := m.Path.Read()
	if err != nil || !exists {
		return false, err
	}
	next, changed := RemoveEntry(val, m.Dir, m.getenv)
	if changed {
		if err = m.Path.Write(next, expand); err != nil {
			return false, err
		}
		if m.Broadcast != nil {
			m.Broadcast()
		}
	}
	r, err := m.readRecord()
	if err != nil {
		return changed, err
	}
	r.PathEntry = ""
	return changed, m.writeRecord(r)
}

// Added lists what this package added, from its record: the shim files and
// the PATH entry ("" when Devpit did not add one). Cleanup removes exactly
// these.
func (m *Manager) Added() (files []string, pathEntry string, err error) {
	r, err := m.readRecord()
	if err != nil {
		return nil, "", err
	}
	for _, s := range r.Shims {
		files = append(files, s.File)
	}
	return files, r.PathEntry, nil
}

// CleanupReport says what Cleanup removed and what it left.
type CleanupReport struct {
	Removed []string
	Kept    []string // with the reason
	PathOff bool
}

// Cleanup removes everything this package added and nothing else: the
// recorded shims that are still Devpit's, the shim folder if it is then
// empty, and the PATH entry if Devpit added it.
func (m *Manager) Cleanup() (CleanupReport, error) {
	var rep CleanupReport
	r, err := m.readRecord()
	if err != nil {
		return rep, err
	}
	for _, s := range r.Shims {
		if err := m.removeShim(s.File, r); err != nil {
			rep.Kept = append(rep.Kept, err.Error())
			continue
		}
		rep.Removed = append(rep.Removed, s.File)
	}
	m.sweepOld()
	if entries, err := os.ReadDir(m.Dir); err == nil && len(entries) == 0 {
		_ = os.Remove(m.Dir)
	}
	if r.PathEntry != "" {
		changed, err := m.RemoveFromPath()
		if err != nil {
			return rep, err
		}
		rep.PathOff = changed
	}
	return rep, m.writeRecord(record{})
}

func (m *Manager) getenv(k string) string {
	if m.Getenv != nil {
		return m.Getenv(k)
	}
	return os.Getenv(k)
}

// IssueKind names something Check found.
type IssueKind string

const (
	IssueShimMissing IssueKind = "shim missing"
	IssueStaleShim   IssueKind = "shim out of date"
	IssueShadowed    IssueKind = "real tool ahead of the shim"
	IssueNotOnPath   IssueKind = "shim folder not on PATH"
	IssueNewTerminal IssueKind = "new terminal needed"
	// IssueShimProgramMissing: devpit-shim.exe is not next to devpit.exe, so
	// no shim can be made or refreshed.
	IssueShimProgramMissing IssueKind = "shim program missing"
)

// Issue is one thing Check found, in plain words, with the fix.
type Issue struct {
	Kind    IssueKind
	Tool    accounts.Tool
	Message string
	Fix     string
}

// Check looks at the shims of the given tools (the ones that need one) and
// reports: a missing shim, a shim older than this Devpit's, the shim folder
// missing from the saved user PATH, missing from this process's PATH (a
// terminal opened before Devpit added it), and a real tool found ahead of
// the shim on this process's PATH.
func (m *Manager) Check(tools []accounts.Tool) []Issue {
	var out []Issue
	want, _ := fileHash(m.Source)
	noSource := m.CheckSource() != nil
	if noSource && len(tools) > 0 {
		out = append(out, Issue{
			Kind:    IssueShimProgramMissing,
			Message: "devpit-shim.exe is missing next to devpit.exe (" + m.Source + "), so Devpit cannot make or refresh the shims folder rules need.",
			Fix:     ProgramMissingFix,
		})
	}

	// With no PathStore (DEVPIT_SHIM_DIR) the person keeps the PATH; only
	// this terminal's PATH is checked below.
	if m.Path != nil && len(tools) > 0 {
		if val, _, _, err := m.Path.Read(); err == nil && !HasEntry(val, m.Dir, m.getenv) {
			out = append(out, Issue{
				Kind:    IssueNotOnPath,
				Message: "Devpit's shim folder is not on your PATH, so folder rules are not applied.",
				Fix:     "Open Accounts in devpit, or make any account change, and Devpit puts " + m.Dir + " first on your user PATH.",
			})
		}
	}
	procPath := m.getenv("PATH")
	onProc := HasEntry(procPath, m.Dir, m.getenv)
	if !onProc && len(tools) > 0 {
		out = append(out, Issue{
			Kind:    IssueNewTerminal,
			Message: "This terminal was opened before Devpit added its shim folder to PATH.",
			Fix:     "Open a new terminal; rules apply there.",
		})
	}
	for _, t := range tools {
		if t.ShimName() == "" {
			continue
		}
		dst := m.ShimPath(t)
		have, err := fileHash(dst)
		if err != nil && noSource {
			continue // the missing program above is the cause and the fix
		}
		if err != nil {
			out = append(out, Issue{
				Kind: IssueShimMissing, Tool: t,
				Message: fmt.Sprintf("The shim for %s is missing (%s).", t.DisplayName(), dst),
				Fix:     "Open Accounts in devpit, or make any account change, and Devpit creates it again.",
			})
			continue
		}
		if want != "" && have != want {
			out = append(out, Issue{
				Kind: IssueStaleShim, Tool: t,
				Message: fmt.Sprintf("The shim for %s is from an older Devpit.", t.DisplayName()),
				Fix:     "Open Accounts in devpit, or make any account change, and Devpit refreshes it.",
			})
		}
		if !onProc {
			continue
		}
		first, err := launch.Find(t.ShimName(), launch.FindOptions{Path: procPath, PathExt: m.getenv("PATHEXT")})
		if err == nil && !strings.EqualFold(filepath.Dir(first), filepath.Clean(m.Dir)) {
			out = append(out, Issue{
				Kind: IssueShadowed, Tool: t,
				Message: fmt.Sprintf("%s is found at %s before Devpit's shim, so folder rules do not apply to it.", t.DisplayName(), first),
				Fix: fmt.Sprintf("Windows puts the system PATH before the user PATH. Move %s after %s in PATH, or remove it from the system PATH and install %s for your user only.",
					filepath.Dir(first), m.Dir, t.DisplayName()),
			})
		}
	}
	return out
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- Devpit's own shim files
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // read-only
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := io.Copy(tmp, bytes.NewReader(data)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
