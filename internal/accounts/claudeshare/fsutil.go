package claudeshare

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// Every file call goes through fsPath, so long paths and names ending in a
// dot or a space work. Nothing here ever follows a link: walks use Lstat and
// treat a reparse point as a leaf that is never entered.

func lstat(p string) (os.FileInfo, error) { return os.Lstat(fsPath(p)) }

func exists(p string) bool {
	_, err := lstat(p)
	return err == nil
}

// isRealDir reports whether p is a folder that is not a link.
func isRealDir(p string) bool {
	fi, err := lstat(p)
	return err == nil && fi.IsDir() && !isReparse(fi)
}

func readDir(p string) ([]os.DirEntry, error) {
	ents, err := os.ReadDir(fsPath(p))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return ents, err
}

// nearestExisting returns p or its nearest parent that exists.
func nearestExisting(p string) string {
	for {
		if exists(p) {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

// treeStats is what a walk found, never counting through a link.
type treeStats struct {
	Files int
	Bytes int64
	Links int
}

// measure sizes p without following links. A link counts as one link and
// zero bytes, so shared content is never counted twice.
func measure(p string) treeStats {
	var st treeStats
	fi, err := lstat(p)
	if err != nil {
		return st
	}
	measureInto(p, fi, &st)
	return st
}

func measureInto(p string, fi os.FileInfo, st *treeStats) {
	switch {
	case isReparse(fi):
		st.Links++
	case fi.IsDir():
		ents, _ := readDir(p)
		for _, e := range ents {
			cp := filepath.Join(p, e.Name())
			cfi, err := lstat(cp)
			if err != nil {
				continue
			}
			measureInto(cp, cfi, st)
		}
	default:
		st.Files++
		st.Bytes += fi.Size()
	}
}

// walkEntry is one node of a tree, relative to its root.
type walkEntry struct {
	rel  string
	fi   os.FileInfo
	link bool
}

// walk lists p and everything below it, sorted, never entering a link.
func walk(p string) ([]walkEntry, error) {
	fi, err := lstat(p)
	if err != nil {
		return nil, err
	}
	var out []walkEntry
	var rec func(abs, rel string, fi os.FileInfo) error
	rec = func(abs, rel string, fi os.FileInfo) error {
		link := isReparse(fi)
		out = append(out, walkEntry{rel: rel, fi: fi, link: link})
		if link || !fi.IsDir() {
			return nil
		}
		ents, err := readDir(abs)
		if err != nil {
			return err
		}
		for _, e := range ents {
			cabs := filepath.Join(abs, e.Name())
			cfi, err := lstat(cabs)
			if err != nil {
				return err
			}
			if err := rec(cabs, filepath.Join(rel, e.Name()), cfi); err != nil {
				return err
			}
		}
		return nil
	}
	if err := rec(p, "", fi); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

// fingerprint is a cheap identity of a tree Devpit wrote: names, kinds,
// sizes and times. An undo removes a copy only while its fingerprint still
// matches, so a copy the person changed since is never removed.
func fingerprint(p string) (string, error) {
	ents, err := walk(p)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, e := range ents {
		kind := "f"
		switch {
		case e.link:
			kind = "l"
		case e.fi.IsDir():
			kind = "d"
		}
		size, mt := int64(0), int64(0)
		if kind == "f" {
			size, mt = e.fi.Size(), e.fi.ModTime().UnixNano()
		}
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00%d\n", strings.ToUpper(e.rel), kind, size, mt)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileHash(p string) (string, error) {
	f, err := os.Open(fsPath(p))
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

func hashBytes(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// sameContent reports whether a and b hold the same bytes: the same names
// (ignoring case, as NTFS does), the same kinds and the same file contents.
// A link anywhere inside makes the trees different: Devpit does not look
// through it.
func sameContent(a, b string) (bool, error) {
	ea, err := walk(a)
	if err != nil {
		return false, err
	}
	eb, err := walk(b)
	if err != nil {
		return false, err
	}
	if len(ea) != len(eb) {
		return false, nil
	}
	sortFold(ea)
	sortFold(eb)
	for i := range ea {
		x, y := ea[i], eb[i]
		if !strings.EqualFold(x.rel, y.rel) || x.link || y.link || x.fi.IsDir() != y.fi.IsDir() {
			return false, nil
		}
		if x.fi.IsDir() {
			continue
		}
		if x.fi.Size() != y.fi.Size() {
			return false, nil
		}
		ha, err := fileHash(filepath.Join(a, x.rel))
		if err != nil {
			return false, err
		}
		hb, err := fileHash(filepath.Join(b, y.rel))
		if err != nil {
			return false, err
		}
		if ha != hb {
			return false, nil
		}
	}
	return true, nil
}

func sortFold(e []walkEntry) {
	sort.Slice(e, func(i, j int) bool { return strings.ToUpper(e[i].rel) < strings.ToUpper(e[j].rel) })
}

// copyReport is what a copy did and left out.
type copyReport struct {
	Files int
	Bytes int64
	// SkippedLinks are links inside the source, which are never followed
	// and never copied.
	SkippedLinks []string
	// SkippedDenied are login files, which are never copied.
	SkippedDenied []string
}

// copyTree copies src (a file or a folder) to dst, which must not exist.
// It never follows a link: a link inside src is skipped and reported, and a
// login file is skipped wherever it is. It refuses to copy a folder into
// itself. Read-only files stay read-only.
func copyTree(src, dst string) (copyReport, error) {
	var rep copyReport
	fi, err := lstat(src)
	if err != nil {
		return rep, err
	}
	if isReparse(fi) {
		return rep, fmt.Errorf("%s is a link; Devpit copies the real folder, never through a link", src)
	}
	if _, err := lstat(dst); err == nil {
		return rep, fmt.Errorf("%s already exists; Devpit never copies over anything", dst)
	}
	if overlaps(src, dst) {
		return rep, fmt.Errorf("refusing to copy %s into itself (%s)", src, dst)
	}
	if rs, err1 := realOf(src); err1 == nil {
		if rd, err2 := realOf(filepath.Dir(dst)); err2 == nil && overlaps(rs, filepath.Join(rd, filepath.Base(dst))) {
			return rep, fmt.Errorf("refusing to copy %s into itself (%s leads inside it)", src, dst)
		}
	}
	return rep, copyNode(src, dst, fi, &rep)
}

func copyNode(src, dst string, fi os.FileInfo, rep *copyReport) error {
	if deniedAnywhere(fi.Name()) {
		rep.SkippedDenied = append(rep.SkippedDenied, src)
		return nil
	}
	if isReparse(fi) {
		rep.SkippedLinks = append(rep.SkippedLinks, src)
		return nil
	}
	if !fi.IsDir() {
		if !fi.Mode().IsRegular() {
			rep.SkippedLinks = append(rep.SkippedLinks, src)
			return nil
		}
		return copyFile(src, dst, fi, rep)
	}
	if err := os.Mkdir(fsPath(dst), 0o700); err != nil {
		return err
	}
	ents, err := readDir(src)
	if err != nil {
		return err
	}
	for _, e := range ents {
		cs := filepath.Join(src, e.Name())
		cfi, err := lstat(cs)
		if err != nil {
			return err
		}
		if err := copyNode(cs, filepath.Join(dst, e.Name()), cfi, rep); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, fi os.FileInfo, rep *copyReport) error {
	in, err := os.Open(fsPath(src))
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // read-only
	out, err := os.OpenFile(fsPath(dst), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}
	if err := os.Chtimes(fsPath(dst), fi.ModTime(), fi.ModTime()); err != nil {
		return err
	}
	if fi.Mode().Perm()&0o200 == 0 {
		if err := os.Chmod(fsPath(dst), 0o400); err != nil {
			return err
		}
	}
	rep.Files++
	rep.Bytes += n
	return nil
}

// removeTree removes a tree Devpit made (a copy, a temporary folder). It
// checks every level: a link found anywhere is removed as a link (the
// reparse point only) and never entered, so nothing outside the tree can be
// reached.
func removeTree(p string) error {
	fi, err := lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if isReparse(fi) {
		return removeLink(p, "")
	}
	if !fi.IsDir() {
		return removeFile(p, fi)
	}
	ents, err := readDir(p)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if err := removeTree(filepath.Join(p, e.Name())); err != nil {
			return err
		}
	}
	return os.Remove(fsPath(p))
}

func removeFile(p string, fi os.FileInfo) error {
	err := os.Remove(fsPath(p))
	if err != nil && fi.Mode().Perm()&0o200 == 0 {
		if cerr := os.Chmod(fsPath(p), 0o600); cerr == nil {
			err = os.Remove(fsPath(p))
		}
	}
	return err
}

// writeAtomic saves data at path through a temporary file in the same
// folder and a rename over the target.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, "."+filepath.Base(path)+".devpit-"+randHex(4))
	f, err := os.OpenFile(fsPath(tmp), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	_, err = f.Write(data)
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = retryInUse(func() error { return replaceFile(tmp, path) })
	}
	if err != nil {
		_ = os.Remove(fsPath(tmp))
		return fmt.Errorf("saving %s: %w", path, err)
	}
	return nil
}

// writeNew creates path with data; it fails if path exists.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(fsPath(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(fsPath(path))
	}
	return err
}

func readFile(p string) ([]byte, error) { return os.ReadFile(fsPath(p)) }

// moveNoReplace renames from to to without ever replacing anything,
// retrying briefly while a virus scanner looks at what was just written.
func moveNoReplace(from, to string) error {
	return retryInUse(func() error { return renameNoReplace(from, to) })
}

// retryInUse retries a rename briefly while a scanner holds the file.
func retryInUse(op func() error) error {
	var err error
	for range 10 {
		if err = op(); err == nil || !inUse(err) {
			return err
		}
		sleepBriefly()
	}
	return err
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// realOf resolves p through links and subst drives (nearest existing part).
func realOf(p string) (string, error) { return realPath(p) }

// equalFiles reports whether two files hold the same bytes.
func equalFiles(a, b string) (bool, error) {
	fa, err := lstat(a)
	if err != nil {
		return false, err
	}
	fb, err := lstat(b)
	if err != nil {
		return false, err
	}
	if fa.Size() != fb.Size() {
		return false, nil
	}
	da, err := readFile(a)
	if err != nil {
		return false, err
	}
	db, err := readFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(da, db), nil
}

// numbered inserts "-n" before a file's extension, or at the end of a
// folder name.
func numbered(name string, n int, isFile bool) string {
	if n <= 1 {
		return name
	}
	suffix := "-" + strconv.Itoa(n)
	if isFile {
		ext := filepath.Ext(name)
		if ext != "" && ext != name {
			return strings.TrimSuffix(name, ext) + suffix + ext
		}
	}
	return name + suffix
}

// withTag puts sep+tag before a file's extension, or at the end of a folder
// name: foo.from-work, notes.from-work.md, foo-work.
func withTag(name, sep, tag string, isFile bool) string {
	if isFile {
		ext := filepath.Ext(name)
		if ext != "" && ext != name {
			return strings.TrimSuffix(name, ext) + sep + tag + ext
		}
	}
	return name + sep + tag
}

func realPath(p string) (string, error) { return accounts.RealPath(p) }

// containsDenied reports whether p, or anything inside it, is a login or
// account file.
func containsDenied(p string) bool {
	ents, err := walk(p)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if deniedAnywhere(filepath.Base(filepath.Join(p, e.rel))) {
			return true
		}
	}
	return false
}

func sleepBriefly() { time.Sleep(50 * time.Millisecond) }
