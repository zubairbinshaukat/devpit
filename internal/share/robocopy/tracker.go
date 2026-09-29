package robocopy

import (
	"sort"
	"strings"
)

// recentKept is how many finished file names a [Tracker] remembers for the
// "just copied" list.
const recentKept = 8

// latelyKept is how many of the latest counted files a [Tracker] can still
// take back. Robocopy writes a file's line and then, when the copy of that
// file failed, its error line straight after it (recorded on Windows 11 with
// /MT:16: testdata/recorded_en_copy_locked.unilog). A small window is enough
// and keeps the memory flat on a copy of millions of files.
const latelyKept = 64

// Tracker turns the lines of a running copy into progress. It counts
// finished files, not percentages: robocopy prints no per-file percentage in
// multi-thread mode, and it writes a file's line when the file's work is
// over, so a file line is the only reliable progress.
//
// Three things in a real log are not a finished file, and the tracker tells
// them apart:
//
//   - a file line followed by an error line for the same path: the attempt
//     failed. The file is taken back out of the count.
//   - the same file's line again when robocopy retries it, often with its
//     "Retrying..." message glued to the end of the path. It is matched to the
//     failed file, so a file that fails three times is not counted three
//     times.
//   - a line for a file that is only in the destination (an "extra"): its path
//     is outside the source, and it is ignored when the tracker knows the
//     source. Without this, every run re-counted every file the destination
//     already held, and a copy into a non-empty folder passed 100%.
//
// It is not safe for concurrent use; [Tool.Copy] feeds it from one goroutine.
type Tracker struct {
	src       string
	filesDone int64
	bytesDone int64
	recent    []string
	failed    map[string]int
	retrying  bool
	lately    []counted
}

// counted is one file the tracker counted and may still take back.
type counted struct {
	path string
	size int64
}

// Progress is what a screen shows, copied out of a [Tracker].
type Progress struct {
	// FilesDone and BytesDone are the files finished in this run.
	FilesDone, BytesDone int64
	// Recent are the last few finished files, newest last.
	Recent []string
	// Failed maps a path to the Win32 error number of its latest failure,
	// for files that have not since been copied.
	Failed map[string]int
	// Retrying is true from a failure until the next file finishes: robocopy
	// is waiting to try the file again.
	Retrying bool
}

// NewTracker returns an empty tracker that counts every file line.
func NewTracker() *Tracker { return &Tracker{failed: map[string]int{}} }

// NewTrackerFor returns an empty tracker that counts only files under
// srcRoot, the folder being copied from.
func NewTrackerFor(srcRoot string) *Tracker {
	t := NewTracker()
	t.src = trimSlash(srcRoot)
	return t
}

// Feed reads one line of the log.
func (t *Tracker) Feed(line string) {
	l := ParseLine(line)
	switch l.Kind {
	case KindFile:
		if t.src != "" {
			if _, ok := under(t.src, l.Path); !ok {
				return // an extra file that lives only in the destination
			}
		}
		path := t.retryOf(l.Path)
		if t.lateIndex(path) >= 0 {
			return // already counted and not taken back
		}
		t.filesDone++
		t.bytesDone += l.Size
		t.lately = append(t.lately, counted{path, l.Size})
		if len(t.lately) > latelyKept {
			t.lately = t.lately[len(t.lately)-latelyKept:]
		}
		t.recent = append(t.recent, path)
		if len(t.recent) > recentKept {
			t.recent = t.recent[len(t.recent)-recentKept:]
		}
		delete(t.failed, path)
		t.retrying = false
	case KindError:
		key := l.Path
		if key == "" {
			key = "?"
		}
		if i := t.lateIndex(key); i >= 0 {
			c := t.lately[i]
			t.filesDone--
			t.bytesDone -= c.size
			t.lately = append(t.lately[:i], t.lately[i+1:]...)
			for j := len(t.recent) - 1; j >= 0; j-- {
				if t.recent[j] == key {
					t.recent = append(t.recent[:j], t.recent[j+1:]...)
					break
				}
			}
		}
		t.failed[key] = l.Code
		t.retrying = true
	}
}

// lateIndex finds path among the files that can still be taken back.
func (t *Tracker) lateIndex(path string) int {
	for i := len(t.lately) - 1; i >= 0; i-- {
		if t.lately[i].path == path {
			return i
		}
	}
	return -1
}

// retryOf returns the failed file a retry line belongs to. With several
// threads robocopy glues its "Retrying..." and "Waiting 5 seconds..."
// messages, in the PC's language, to the end of whatever line it wrote
// last, so a retried file's line reads "...\locked.txt Retrying...". A
// Windows file name cannot end in a dot, so a path that does has a message
// on it; if a failed file's path is the start of it, it is that file. Any
// other path is returned as it came.
func (t *Tracker) retryOf(path string) string {
	if !strings.HasSuffix(path, ".") {
		return path
	}
	best := ""
	for k := range t.failed {
		if len(k) > len(best) && len(path) > len(k) && strings.HasPrefix(path, k) {
			best = k
		}
	}
	if best == "" {
		return path
	}
	return best
}

// Snapshot copies the current progress out.
func (t *Tracker) Snapshot() Progress {
	f := make(map[string]int, len(t.failed))
	for k, v := range t.failed {
		f[k] = v
	}
	return Progress{
		FilesDone: t.filesDone,
		BytesDone: t.bytesDone,
		Recent:    append([]string(nil), t.recent...),
		Failed:    f,
		Retrying:  t.retrying,
	}
}

// FailedPaths returns the failed paths, sorted, for the final list.
func (p Progress) FailedPaths() []string {
	out := make([]string, 0, len(p.Failed))
	for k := range p.Failed {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
