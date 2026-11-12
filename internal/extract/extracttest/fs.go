package extracttest

import (
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// FakeFS is an in-memory StatFS whose sizes a Script can grow and rename.
// Paths are compared case-insensitively with either slash, so Windows-style
// paths behave the same on every OS. It records every call, for tests that
// pin what the engine may do to the file system.
type FakeFS struct {
	mu    sync.Mutex
	nodes map[string]*node
	calls []string
}

type node struct {
	name string // display path, as first added
	dir  bool
	link bool
	size int64
	mod  time.Time
}

// NewFakeFS is an empty file system.
func NewFakeFS() *FakeFS { return &FakeFS{nodes: map[string]*node{}} }

func key(p string) string {
	return strings.ToLower(clean(p))
}

func clean(p string) string {
	p = path.Clean(strings.ReplaceAll(p, `\`, "/"))
	return strings.TrimSuffix(p, "/")
}

func parentOf(p string) string {
	d := path.Dir(clean(p))
	if d == "." || d == "/" || d == clean(p) {
		return ""
	}
	return d
}

func (f *FakeFS) mkdirs(p string) {
	for d := parentOf(p); d != ""; d = parentOf(d) {
		if _, ok := f.nodes[key(d)]; ok {
			return
		}
		f.nodes[key(d)] = &node{name: d, dir: true, mod: Epoch()}
	}
}

// AddFile adds (or resizes) a regular file and any missing parent folders.
func (f *FakeFS) AddFile(p string, size int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mkdirs(p)
	f.nodes[key(p)] = &node{name: clean(p), size: size, mod: Epoch()}
}

// AddDir adds a folder and its parents.
func (f *FakeFS) AddDir(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mkdirs(p)
	f.nodes[key(p)] = &node{name: clean(p), dir: true, mod: Epoch()}
}

// AddLink adds a symbolic link (or junction): Lstat reports ModeSymlink.
func (f *FakeFS) AddLink(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mkdirs(p)
	f.nodes[key(p)] = &node{name: clean(p), link: true, mod: Epoch()}
}

// SetSize changes a file's size, adding it when missing.
func (f *FakeFS) SetSize(p string, size int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.nodes[key(p)]; ok {
		n.size = size
		return
	}
	f.mkdirs(p)
	f.nodes[key(p)] = &node{name: clean(p), size: size, mod: Epoch()}
}

// Rename moves a file (not a folder tree) to a new name.
func (f *FakeFS) Rename(from, to string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.nodes[key(from)]
	if !ok {
		return
	}
	delete(f.nodes, key(from))
	f.mkdirs(to)
	n.name = clean(to)
	f.nodes[key(to)] = n
}

// Remove deletes p and everything under it.
func (f *FakeFS) Remove(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := key(p)
	for nk := range f.nodes {
		if nk == k || strings.HasPrefix(nk, k+"/") {
			delete(f.nodes, nk)
		}
	}
}

// Exists reports whether p is present (without recording a call).
func (f *FakeFS) Exists(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.nodes[key(p)]
	return ok
}

// Size is a file's size, or -1 when missing (without recording a call).
func (f *FakeFS) Size(p string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.nodes[key(p)]; ok {
		return n.size
	}
	return -1
}

// Calls lists every StatFS call made so far, as "lstat <path>" or
// "readdir <path>".
func (f *FakeFS) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Lstat implements extract.StatFS.
func (f *FakeFS) Lstat(name string) (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "lstat "+name)
	n, ok := f.nodes[key(name)]
	if !ok {
		return nil, &fs.PathError{Op: "lstat", Path: name, Err: fs.ErrNotExist}
	}
	return info{n: *n}, nil
}

// ReadDirNames implements extract.StatFS.
func (f *FakeFS) ReadDirNames(dir string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "readdir "+dir)
	k := key(dir)
	if n, ok := f.nodes[k]; !ok || !n.dir {
		return nil, &fs.PathError{Op: "readdir", Path: dir, Err: fs.ErrNotExist}
	}
	var names []string
	for nk, n := range f.nodes {
		if parentOf(nk) == k {
			names = append(names, path.Base(n.name))
		}
	}
	sort.Strings(names)
	return names, nil
}

type info struct{ n node }

func (i info) Name() string { return path.Base(i.n.name) }
func (i info) Size() int64  { return i.n.size }
func (i info) Mode() fs.FileMode {
	switch {
	case i.n.dir:
		return fs.ModeDir | 0o755
	case i.n.link:
		return fs.ModeSymlink | 0o777
	}
	return 0o644
}
func (i info) ModTime() time.Time { return i.n.mod }
func (i info) IsDir() bool        { return i.n.dir }
func (i info) Sys() any           { return nil }
