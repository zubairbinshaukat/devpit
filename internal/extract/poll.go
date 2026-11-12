package extract

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
)

// The progress poller (section 6.5) is safety-critical: while hatchzip runs,
// Devpit never opens a file in the output folder or the archive (rule 78).
// Opening a file there makes hatchzip's rename fail with error 32 (verified in
// research); Lstat is handle-free, and spike X1 showed that listing folder
// names (a directory handle) during three in-place runs of a 5,000-file zip,
// even in a 1 ms loop, never did. So the poller uses exactly two things:
// StatFS.Lstat and StatFS.ReadDirNames.
//
// How bytes are counted. At the start the output folder is listed once and
// every regular file's size is its baseline. A final-named file that was there
// already is frozen and adds nothing; on a resume, the bytes extracted before
// come from the plan instead ("resuming: <left> of <total>"). A new final
// name adds its whole size, and is stat'ed until its size is the same on two
// ticks, then frozen (hatchzip writes "<entry>.hatchzip-part", then renames,
// so a final name is complete). A part file adds what it grew past its
// baseline; one that shrinks below it is being written again from zero
// (spike X8: a pause leaves a full-size part, and resume rewrites it), and
// counts from zero. When the final name of a part appears, the part's bytes
// are dropped, so a part renamed to its final name is counted once.

// freeEvery is how often the output volume's free space is read.
const freeEvery = 5 * time.Second

// maxPollDepth bounds how deep the poller lists folders.
const maxPollDepth = 64

type pentry struct {
	path   string
	part   bool // name ends in .hatchzip-part
	dir    bool
	leaf   bool // a link or junction: listed, never entered, never sized
	fresh  bool // appeared after the start
	gone   bool
	frozen bool
	base   int64
	size   int64
}

// contrib is what the entry adds to "done" now.
func (e *pentry) contrib() int64 {
	if e.dir || e.leaf {
		return 0
	}
	return max(0, e.size-e.base)
}

// poller tracks one output folder. It is used from one goroutine at a time.
type poller struct {
	fsys    StatFS
	root    string
	entries map[string]*pentry         // lower-cased path
	order   []*pentry                  // discovery order, for stable answers
	cur     *pentry                    // the part being written
	onErr   func(op string, err error) // errors other than "not there", for the run log
}

func newPoller(fsys StatFS, root string) *poller {
	return &poller{fsys: fsys, root: root, entries: map[string]*pentry{}}
}

// report passes an error to onErr unless it is a path that is not there,
// which is normal (the folder not made yet, a part renamed).
func (p *poller) report(op string, err error) {
	if p.onErr != nil && err != nil && !errors.Is(err, fs.ErrNotExist) {
		p.onErr(op, err)
	}
}

func pkey(p string) string { return strings.ToLower(norm(p)) }

// rescan lists the folder tree's names and Lstats only names not seen
// before. initial marks the baseline listing at the start.
func (p *poller) rescan(initial bool) {
	p.list(p.root, 0, initial)
}

func (p *poller) list(dir string, depth int, initial bool) {
	if depth > maxPollDepth {
		return
	}
	names, err := p.fsys.ReadDirNames(dir)
	if err != nil {
		p.report("list", err)
		return
	}
	for _, name := range names {
		path := joinPath(dir, name)
		k := pkey(path)
		if e, ok := p.entries[k]; ok {
			if e.gone && !e.dir {
				e.gone = false // the same name again: Lstat it on the next tick
			}
			if e.dir {
				p.list(path, depth+1, initial)
			}
			continue
		}
		fi, err := p.fsys.Lstat(path)
		if err != nil {
			p.report("lstat", err)
			continue
		}
		e := &pentry{path: path, fresh: !initial}
		switch m := fi.Mode(); {
		case m&(fs.ModeSymlink|fs.ModeIrregular) != 0:
			e.leaf = true
		case fi.IsDir():
			e.dir = true
		case m.IsRegular():
			e.part = strings.HasSuffix(strings.ToLower(name), archive.PartSuffix)
			e.size = fi.Size()
			if initial {
				e.base = e.size
				e.frozen = !e.part
			}
		default:
			e.leaf = true
		}
		p.entries[k] = e
		p.order = append(p.order, e)
		if e.dir {
			p.list(path, depth+1, initial)
		}
	}
}

// stat Lstats every part file and every final name not frozen yet, and
// picks the part being written.
func (p *poller) stat() {
	var (
		best     *pentry
		bestGrew int64
	)
	for _, e := range p.order {
		if e.dir || e.leaf || e.frozen || e.gone {
			continue
		}
		fi, err := p.fsys.Lstat(e.path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				e.gone = true // keeps its last contribution
			}
			p.report("lstat", err)
			continue
		}
		size := fi.Size()
		if e.part {
			if size < e.base {
				e.base = 0 // written again from zero
			}
			if grew := size - e.size; grew > bestGrew && !p.superseded(e) {
				best, bestGrew = e, grew
			}
		} else if size == e.size {
			e.frozen = true
		}
		e.size = size
	}
	switch {
	case best != nil:
		p.cur = best
	case p.cur != nil && (p.cur.gone || p.superseded(p.cur)):
		p.cur = nil
	}
}

// current is the base name of the part being written, or "".
func (p *poller) current() string {
	if p.cur == nil {
		return ""
	}
	return partName(p.cur.path)
}

// superseded: the part's final name exists, so its bytes are counted there.
func (p *poller) superseded(e *pentry) bool {
	if !e.part {
		return false
	}
	_, ok := p.entries[pkey(e.path[:len(e.path)-len(archive.PartSuffix)])]
	return ok
}

// sum is the bytes written since the start and the new final entries.
func (p *poller) sum() (bytes int64, items int) {
	for _, e := range p.order {
		switch {
		case e.part:
			if !p.superseded(e) {
				bytes += e.contrib()
			}
		default:
			bytes += e.contrib()
			if e.fresh {
				items++
			}
		}
	}
	return bytes, items
}

// partName is the base name of a part file without its suffix.
func partName(path string) string {
	b := baseName(path)
	return b[:len(b)-len(archive.PartSuffix)]
}

// tracker turns poller readings into Progress for one Job (every run of it,
// when it auto-resumes).
type tracker struct {
	e          *Engine
	req        Request
	onProgress func(Progress)
	start      time.Time
	pl         *poller
	resumeBase int64
	meter      *netstat.Meter
	lg         *runLog
	first      attemptMark // before the first run started (rule 85)

	// tickMu guards the poller and the fields below: the loop goroutine
	// ticks, and the job measures an attempt once its process has ended.
	tickMu     sync.Mutex
	raw        int64 // bytes since the start, read by meter
	rawMax     int64 // the most raw has been: a part written again never lowers it
	lastRescan time.Time
	lastFree   time.Time
	idle       int

	mu  sync.Mutex
	cur Progress
}

// attemptMark is how far the job was at one moment: what rule 85 compares
// between the start and the end of one hatchzip run.
type attemptMark struct {
	extracted int64 // bytes written since the job started, a high-water mark
	archive   int64 // in place: Lstat sum of the parts; 0 when the archive is kept
}

// progressed reports whether the run between the two marks extracted
// anything or made the archive smaller. An archive that cannot be sized now
// (0) is not "smaller".
func (a attemptMark) progressed(since attemptMark) bool {
	return a.extracted > since.extracted || (a.archive > 0 && a.archive < since.archive)
}

// mark reads where the job is now without polling.
func (tr *tracker) mark() attemptMark {
	tr.tickMu.Lock()
	defer tr.tickMu.Unlock()
	return attemptMark{extracted: tr.rawMax, archive: tr.archiveBytes()}
}

// measure lists and sizes the output folder once more, now that a run has
// ended, and reads where the job is. It uses Lstat and name listing only
// (rule 78), and no hatchzip is running when it is called.
func (tr *tracker) measure() attemptMark {
	tr.tick(true)
	return tr.mark()
}

// archiveBytes is the Lstat sum of the archive's parts in place, else 0.
func (tr *tracker) archiveBytes() int64 {
	var n int64
	if tr.req.Mode == ModeInPlace {
		for _, part := range tr.req.Set.Parts {
			if fi, err := tr.e.d.FS.Lstat(part); err == nil && fi.Mode().IsRegular() {
				n += fi.Size()
			}
		}
	}
	return n
}

// setLog sends the poller's errors and a progress sample every 10 s to lg.
// It is called before the loop starts.
func (tr *tracker) setLog(lg *runLog) {
	tr.lg = lg
	if lg != nil {
		tr.pl.onErr = lg.pollErr
	}
}

func (e *Engine) newTracker(r Request, plan Plan, onProgress func(Progress)) *tracker {
	tr := &tracker{e: e, req: r, onProgress: onProgress, start: e.d.Now(), pl: newPoller(e.d.FS, r.OutDir)}
	tr.cur.TotalBytes = plan.TotalBytes
	tr.cur.TotalItems = plan.Entries
	if plan.Resuming && plan.LeftBytes <= plan.TotalBytes {
		tr.resumeBase = plan.TotalBytes - plan.LeftBytes
	}
	tr.cur.DoneBytes = capDone(0, tr.resumeBase, plan.TotalBytes)
	tr.meter = netstat.NewByteMeter(func() (uint64, error) { return uint64(max(tr.raw, 0)), nil }, e.d.Now)
	tr.pl.rescan(true)
	tr.lastRescan = tr.start
	tr.first = tr.mark()
	return tr
}

// emit applies f to the current progress and reports it.
func (tr *tracker) emit(f func(*Progress)) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	f(&tr.cur)
	tr.cur.Elapsed = tr.e.d.Now().Sub(tr.start)
	if tr.onProgress != nil {
		tr.onProgress(tr.cur)
	}
}

func (tr *tracker) last() Progress {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.cur
}

// loop polls every Deps.Poll until ctx ends.
func (tr *tracker) loop(ctx context.Context) {
	for {
		tr.e.d.Sleep(ctx, tr.e.d.Poll)
		if ctx.Err() != nil {
			return
		}
		tr.tick(false)
	}
}

// tick is one poll. rescan lists the folder names again whatever the time
// since the last listing.
func (tr *tracker) tick(rescan bool) {
	tr.tickMu.Lock()
	defer tr.tickMu.Unlock()
	now := tr.e.d.Now()
	if rescan || now.Sub(tr.lastRescan) >= tr.e.d.Rescan {
		tr.pl.rescan(false)
		tr.lastRescan = now
	}
	tr.pl.stat()
	bytes, items := tr.pl.sum()
	tr.raw = bytes
	tr.rawMax = max(tr.rawMax, bytes)
	rate, _ := tr.meter.Sample()

	archBytes := tr.archiveBytes()
	readFree := tr.e.d.Free != nil && (tr.lastFree.IsZero() || now.Sub(tr.lastFree) >= freeEvery)
	var free uint64
	if readFree {
		tr.lastFree = now
		// The output volume: with a chosen parent it can differ from the
		// archive's.
		if ds, _, err := tr.e.freeAt(tr.req.OutDir); err == nil {
			free = ds.FreeBytes
		} else {
			tr.lg.pollErr("free space", err)
		}
	}

	tr.emit(func(pr *Progress) {
		prev := pr.DoneBytes
		pr.DoneBytes = capDone(prev, tr.resumeBase+bytes, pr.TotalBytes)
		pr.DoneItems = capItems(pr.DoneItems, items, pr.TotalItems)
		if pr.DoneBytes > prev {
			tr.idle = 0
		} else {
			tr.idle++
		}
		switch pr.Phase {
		case PhaseStarting, PhaseWriting, PhaseFinishing:
			pr.Phase = PhaseWriting
			if tr.idle >= 2 && pr.TotalBytes > 0 && pr.DoneBytes >= pr.TotalBytes*90/100 {
				pr.Phase = PhaseFinishing
			}
		}
		pr.Current = tr.pl.current()
		pr.ArchiveBytes = archBytes
		if readFree {
			pr.FreeBytes = free
		}
		pr.Speed = rate
		pr.ETA = etaWhileRunning(netstat.ETA(pr.TotalBytes-pr.DoneBytes, rate))
	})
	tr.lg.sample(now, tr.last())
}
