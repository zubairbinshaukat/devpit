package extract

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/protect"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// Validation (rule 82). Only a regular local file is extracted: never a UNC
// path or a mapped network drive, never through a link or junction (the file
// itself or any folder above it), never under %WINDIR%, Program Files or
// ProgramData, never inside a login or account folder (rule 30). The output
// is a new folder directly beside the archive, never a drive root.
//
// Paths are judged as Windows paths on every platform (either slash), so the
// rules and their tests behave the same on Linux CI; the file system is only
// reached through [StatFS].

// ErrFirstPartMissing is a split set whose first part (or a part before the
// picked one) is not beside the picked one. It is also an [ErrNotAFile].
var ErrFirstPartMissing = fmt.Errorf("%w: the first part of the set is missing", ErrNotAFile)

var errFirstMissing = ErrFirstPartMissing

// errBadRequest is a request whose paths are not an absolute local archive
// and an output folder named after it. Check, Start and Restore refuse it
// before anything runs (rules 82 and 83).
var errBadRequest = errors.New("extract: the archive and its output folder must be absolute and local, and the folder must be named after the archive")

// systemFolderVars name the folders no archive is extracted from or into.
var systemFolderVars = []string{"WINDIR", "SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "ProgramData"}

// Validate applies rule 82 to a picked set and its output folder
// ([DefaultOutDir]). fsys is used for Lstat only; prot is the login and
// account folder list (protect.Default); getenv reads the system folders.
func Validate(set archive.Set, fsys StatFS, prot protect.List, getenv func(string) string) error {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	paths := append([]string{set.Picked, set.First}, set.Parts...)
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := validatePath(p, getenv, prot); err != nil {
			return err
		}
	}
	if set.First == "" || len(set.Parts) == 0 {
		return ErrNotAFile
	}
	for _, p := range set.Parts {
		if err := regularFile(fsys, p); err != nil {
			return err
		}
	}
	if err := noLinkAbove(fsys, set.First); err != nil {
		return err
	}
	out := DefaultOutDir(set, fsys)
	if !besideArchive(set.First, out) {
		return errBadRequest
	}
	return validatePath(out, getenv, prot)
}

// validatePath is the name-only half of rule 82 for one path.
func validatePath(p string, getenv func(string) string, prot protect.List) error {
	if isUNC(p) || winapi.IsNetworkDrive(p) {
		return ErrNetworkPath
	}
	if !isAbsPath(p) {
		return ErrNotAFile
	}
	for _, v := range systemFolderVars {
		if dir := strings.TrimSpace(getenv(v)); isAbsPath(dir) && !isRootPath(dir) && within(p, dir) {
			return fmt.Errorf("%w: %s holds Windows or installed programs", ErrProtected, dir)
		}
	}
	if hit, why := prot.Inside(p); hit {
		return fmt.Errorf("%w: it is inside %s", ErrProtected, why)
	}
	return nil
}

// regularFile: p exists and is a regular file by Lstat (not a link, not a
// junction or other reparse point, not a folder). A path with nothing at it
// is both an [ErrNotAFile] and an fs.ErrNotExist, so a caller can say
// "nothing is there" instead of "that is a folder".
func regularFile(fsys StatFS, p string) error {
	fi, err := fsys.Lstat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s: %w", ErrNotAFile, baseName(p), fs.ErrNotExist)
		}
		return err
	}
	switch m := fi.Mode(); {
	case m&fs.ModeSymlink != 0, m&fs.ModeIrregular != 0:
		return ErrReparsePoint
	case !m.IsRegular():
		return ErrNotAFile
	}
	return nil
}

// noLinkAbove refuses a file reached through a junction or symbolic link in
// any folder above it, up to (not including) the drive root.
func noLinkAbove(fsys StatFS, p string) error {
	for dir := parentPath(p); dir != "" && !isRootPath(dir); dir = parentPath(dir) {
		fi, err := fsys.Lstat(dir)
		if err != nil {
			return err
		}
		if m := fi.Mode(); m&fs.ModeSymlink != 0 || m&fs.ModeIrregular != 0 {
			return ErrReparsePoint
		}
	}
	return nil
}

// Identify is Classify + Lstat of every part + Validate.
func Identify(path string, fsys StatFS, prot protect.List, getenv func(string) string) (archive.Set, error) {
	path = strings.TrimSpace(strings.Trim(strings.TrimSpace(path), `"`))
	if isUNC(path) || winapi.IsNetworkDrive(path) {
		return archive.Set{}, ErrNetworkPath
	}
	f, first, ok, err := archive.Classify(path)
	if !ok {
		return archive.Set{}, err
	}
	if err := regularFile(fsys, path); err != nil {
		return archive.Set{}, err
	}
	set := probeSet(fsys, first)
	set.Picked, set.Format = path, f
	if _, err := fsys.Lstat(first); err != nil {
		return archive.Set{}, errFirstMissing
	}
	if !containsFold(set.Parts, path) {
		// A gap in the numbering (x.7z.001 and x.7z.003, no .002): hatchzip
		// would stop at the gap, so the set is not usable as picked.
		return archive.Set{}, errFirstMissing
	}
	if err := Validate(set, fsys, prot, getenv); err != nil {
		return archive.Set{}, err
	}
	return set, nil
}

// maxNumbered bounds the "<stem> (n)" search.
const maxNumbered = 99

// DefaultOutDir is decision 11: <archive folder>\<stem>. When that folder
// exists, holds something, and the set has no journal (a fresh run), the
// first free "<stem> (2)", "(3)" … is used instead, so a fresh run never
// writes into someone's non-empty folder. With a journal, <stem> as is: a
// resume without Devpit's record (spike X3; a different folder makes the
// check exit 1 and say where the extraction was started).
func DefaultOutDir(set archive.Set, fsys StatFS) string {
	first := set.First
	if first == "" {
		first = set.Picked
	}
	dir, stem := dirPath(first), archive.Stem(first)
	base := joinPath(dir, stem)
	if fsys == nil {
		return base
	}
	if _, err := fsys.Lstat(archive.JournalPath(first)); err == nil {
		return base
	}
	return numberedOut(fsys, dir, stem)
}

// numberedOut is the first usable of <dir>\<stem>, "<stem> (2)", "(3)" ….
func numberedOut(fsys StatFS, dir, stem string) string {
	base := joinPath(dir, stem)
	if usableOut(fsys, base) {
		return base
	}
	for n := 2; n <= maxNumbered; n++ {
		cand := joinPath(dir, stem+" ("+strconv.Itoa(n)+")")
		if usableOut(fsys, cand) {
			return cand
		}
	}
	return joinPath(dir, stem+" ("+strconv.Itoa(maxNumbered+1)+")")
}

// ErrNotAFolder is a chosen parent folder that is missing or not a folder.
var ErrNotAFolder = errors.New("that is not a folder")

// errNoStem is a set with no name to make a folder from.
var errNoStem = fmt.Errorf("%w: the archive has no name", ErrNotAFile)

// OutDirIn is the output folder when the person chooses where to extract:
// a new folder <parent>\<stem>, numbered " (2)", " (3)" … like
// [DefaultOutDir] when that exists and is not empty (rule 82). It refuses a
// parent that is missing or not a folder ([ErrNotAFolder]), a UNC path or
// network drive ([ErrNetworkPath]), a system, login or account folder
// ([ErrProtected], the same check as [Identify]), and a parent that is, or is
// reached through, a link or junction ([ErrReparsePoint]). A parent on a
// different drive from the archive is allowed; hatchzip then keeps the
// archive whole until the end of each part, and [SameDrive] tells the
// screen. The parent may be a drive root; the output never is.
//
// It always picks an empty or new folder, also for a set with a journal: a
// resume uses the folder the extraction was started with (Devpit's record,
// or [DefaultOutDir]), and hatchzip's check refuses any other.
func OutDirIn(parent string, set archive.Set, fsys StatFS) (string, error) {
	return outDirIn(parent, set, fsys, protect.Default(os.LookupEnv), os.Getenv)
}

func outDirIn(parent string, set archive.Set, fsys StatFS, prot protect.List, getenv func(string) string) (string, error) {
	parent = strings.TrimSpace(strings.Trim(strings.TrimSpace(parent), `"`))
	if isUNC(parent) || winapi.IsNetworkDrive(parent) {
		return "", ErrNetworkPath
	}
	if !isAbsPath(parent) {
		return "", ErrNotAFolder
	}
	if fsys == nil {
		return "", errNoRunner
	}
	first := set.First
	if first == "" {
		first = set.Picked
	}
	stem := ""
	if first != "" {
		stem = archive.Stem(first)
	}
	if strings.TrimSpace(stem) == "" {
		return "", errNoStem
	}
	if !isRootPath(parent) {
		parent = trimSep(parent)
	}
	if err := validatePath(parent, getenv, prot); err != nil {
		return "", err
	}
	fi, err := fsys.Lstat(parent)
	switch {
	case err != nil:
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %s does not exist", ErrNotAFolder, parent)
		}
		return "", err
	case fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 && !isRootPath(parent):
		return "", fmt.Errorf("%w: the folder is a link or junction", ErrReparsePoint)
	case !fi.IsDir():
		return "", ErrNotAFolder
	}
	if err := noLinkAbove(fsys, joinPath(parent, stem)); err != nil {
		if errors.Is(err, ErrReparsePoint) {
			return "", fmt.Errorf("%w: the folder is reached through a link or junction", ErrReparsePoint)
		}
		return "", err
	}
	out := numberedOut(fsys, parent, stem)
	if err := validatePath(out, getenv, prot); err != nil {
		return "", err
	}
	return out, nil
}

// SameDrive reports whether a and b are on the same drive (X:), or the same
// \\server\share. Paths without either (as on other systems) count as one
// drive.
func SameDrive(a, b string) bool {
	return strings.EqualFold(volumeOf(a), volumeOf(b))
}

// volumeOf is "X:", `\\server\share`, or "".
func volumeOf(p string) string {
	if isUNC(p) {
		q := norm(p)
		if strings.HasPrefix(q, `\\?\`) || strings.HasPrefix(q, `\\.\`) {
			q = `\\` + q[len(`\\?\UNC\`):]
		}
		parts := strings.SplitN(q[2:], `\`, 3)
		if len(parts) >= 2 {
			return `\\` + parts[0] + `\` + parts[1]
		}
		return q
	}
	p = stripDevice(p)
	if len(p) >= 2 && isLetter(p[0]) && p[1] == ':' {
		return p[:2]
	}
	return ""
}

// usableOut: the folder is missing, or an empty real folder.
func usableOut(fsys StatFS, p string) bool {
	fi, err := fsys.Lstat(p)
	if err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	if !fi.IsDir() || fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return false
	}
	names, err := fsys.ReadDirNames(p)
	return err == nil && len(names) == 0
}

// checkRequest is the argv-level guard every run passes before anything is
// started: absolute local paths only, and an output folder that is the
// archive's own <stem> folder (or "<stem> (n)") beside the archive or in a
// parent the person chose ([OutDirIn]), never a drive root (rules 82 and 83).
func checkRequest(r Request, needOut bool) error {
	if !isAbsPath(r.Set.First) || isUNC(r.Set.First) {
		return errBadRequest
	}
	if !needOut {
		return nil
	}
	if !isAbsPath(r.OutDir) || isUNC(r.OutDir) || winapi.IsNetworkDrive(r.OutDir) || !stemFolder(r.Set.First, r.OutDir) {
		return errBadRequest
	}
	return nil
}

// stemFolder: out is not a drive root and its name is the archive's stem,
// or the stem numbered " (n)" as DefaultOutDir and OutDirIn make it.
func stemFolder(first, out string) bool {
	out = trimSep(norm(out))
	name := baseName(out)
	if isRootPath(out) || name == "" {
		return false
	}
	stem := archive.Stem(first)
	if strings.EqualFold(name, stem) {
		return true
	}
	rest, ok := strings.CutPrefix(strings.ToLower(name), strings.ToLower(stem)+" (")
	if !ok {
		return false
	}
	num, ok := strings.CutSuffix(rest, ")")
	n, err := strconv.Atoi(num)
	return ok && err == nil && n >= 2 && num == strconv.Itoa(n)
}

// besideArchive: out is a direct child of the archive's folder and not a
// drive root.
func besideArchive(first, out string) bool {
	out = trimSep(norm(out))
	if isRootPath(out) || baseName(out) == "" {
		return false
	}
	return strings.EqualFold(trimSep(norm(parentPath(out))), trimSep(norm(dirPath(first))))
}

// Path helpers. Windows rules on every platform, either slash.

func norm(p string) string { return strings.ReplaceAll(p, "/", `\`) }

func trimSep(p string) string {
	for len(p) > 1 && strings.HasSuffix(p, `\`) && !isRootPath(p) {
		p = p[:len(p)-1]
	}
	return p
}

// isUNC is a \\server\share path, in any spelling.
func isUNC(p string) bool {
	p = norm(p)
	if strings.HasPrefix(p, `\\?\`) || strings.HasPrefix(p, `\\.\`) {
		return strings.HasPrefix(strings.ToUpper(p[4:]), `UNC\`)
	}
	return strings.HasPrefix(p, `\\`)
}

// stripDevice drops a \\?\ prefix from a drive path.
func stripDevice(p string) string {
	p = norm(p)
	if strings.HasPrefix(p, `\\?\`) && !isUNC(p) {
		return p[4:]
	}
	return p
}

// isAbsPath is X:\…, or / on other systems (where tests use real temp
// folders).
func isAbsPath(p string) bool {
	p = stripDevice(p)
	if len(p) >= 3 && isLetter(p[0]) && p[1] == ':' && p[2] == '\\' {
		return true
	}
	return strings.HasPrefix(p, `\`) && !strings.HasPrefix(p, `\\`)
}

func isLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// isRootPath is X:\ or \ (or /).
func isRootPath(p string) bool {
	p = stripDevice(p)
	if len(p) >= 2 && isLetter(p[0]) && p[1] == ':' {
		return strings.Trim(p[2:], `\`) == ""
	}
	return strings.Trim(p, `\`) == "" && p != ""
}

// within: p is parent or inside it, case-insensitively.
func within(p, parent string) bool {
	a := strings.ToLower(trimSep(stripDevice(p)))
	b := strings.ToLower(trimSep(stripDevice(parent)))
	if a == b {
		return true
	}
	if !strings.HasSuffix(b, `\`) {
		b += `\`
	}
	return strings.HasPrefix(a, b)
}

// splitPath cuts at the last slash or backslash: dir keeps no trailing
// separator unless it is a root.
func splitPath(p string) (dir, base string) {
	i := strings.LastIndexAny(p, `\/`)
	if i < 0 {
		return "", p
	}
	dir, base = p[:i], p[i+1:]
	if dir == "" || (len(dir) == 2 && dir[1] == ':') {
		dir = p[:i+1]
	}
	return dir, base
}

func dirPath(p string) string  { d, _ := splitPath(p); return d }
func baseName(p string) string { _, b := splitPath(p); return b }

// parentPath is the folder above p, or "" at a root.
func parentPath(p string) string {
	p = trimSep(p)
	if isRootPath(p) {
		return ""
	}
	return dirPath(p)
}

// joinPath joins with the separator dir already uses (backslash by default).
func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	sep := `\`
	if strings.Contains(dir, "/") && !strings.Contains(dir, `\`) {
		sep = "/"
	}
	if strings.HasSuffix(dir, `\`) || strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + sep + name
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(norm(x), norm(s)) {
			return true
		}
	}
	return false
}
