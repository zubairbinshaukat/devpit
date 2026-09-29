package robocopy

import "sort"

// recentKept is how many finished file names a [Tracker] remembers for the
// "just copied" list.
const recentKept = 8

// Tracker turns the lines of a running copy into progress. It counts
// finished files, not percentages: robocopy prints no per-file percentage in
// multi-thread mode, so the only reliable progress is "this file is done".
// It is not safe for concurrent use; [Run] feeds it from one goroutine.
type Tracker struct {
	filesDone int64
	bytesDone int64
	recent    []string
	failed    map[string]int
	retrying  bool
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

// NewTracker returns an empty tracker.
func NewTracker() *Tracker { return &Tracker{failed: map[string]int{}} }

// Feed reads one line of the log.
//
// A file line means the file is finished, so its size joins the total, its
// name joins the recent list, and it clears any earlier failure of the same
// path (a retry that worked). An error line marks the path failed and puts
// the tracker in the retrying state until something else finishes.
func (t *Tracker) Feed(line string) {
	l := ParseLine(line)
	switch l.Kind {
	case KindFile:
		t.filesDone++
		t.bytesDone += l.Size
		t.recent = append(t.recent, l.Path)
		if len(t.recent) > recentKept {
			t.recent = t.recent[len(t.recent)-recentKept:]
		}
		delete(t.failed, l.Path)
		t.retrying = false
	case KindError:
		key := l.Path
		if key == "" {
			key = "?"
		}
		t.failed[key] = l.Code
		t.retrying = true
	}
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
