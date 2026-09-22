package scan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charlievieth/fastwalk"
)

// Errors Run reports before it walks anything.
var (
	// ErrNoRoots means Options.Roots was empty.
	ErrNoRoots = errors.New("scan: no folder to scan")
	// ErrNoRules means Options.Rules was empty, so nothing could match.
	ErrNoRules = errors.New("scan: no rules to match against")
	// ErrNetworkRoot means a root is a UNC path or a mapped network drive.
	// Devpit refuses those unless Options.AllowNetwork says otherwise.
	ErrNetworkRoot = errors.New("scan: network paths are not scanned by default")
)

// maxWorkers caps the worker count. Past this point a walk is bound by the
// filesystem, not by goroutines, and on a spinning disk it is already bound
// by seeks well before here.
const maxWorkers = 32

// progressInterval is how often the progress callback fires. It is fast
// enough to look live and slow enough that the UI never spends its frame
// budget on counters.
const progressInterval = 100 * time.Millisecond

// Run walks opts.Roots and sends every reclaimable item it finds on out.
//
// Channel semantics, stated plainly because getting them wrong deadlocks a
// UI: Run does not create out and never closes it. The caller owns the
// channel, must drain it for as long as Run is running, and closes it after
// Run has returned. Run blocks until the walk and every outstanding size job
// have finished, so when it returns, nothing else will be sent.
//
// progress may be nil. When it is not, it is called from a single goroutine
// roughly every 100 ms with a snapshot of the counters, and once more with
// the final Stats before Run returns.
//
// Two things are deliberately not reported. A path is sent at most once, even
// when two roots overlap or two rules name the same folder. And a match that
// measures zero bytes is dropped, unless it is a cloud placeholder, whose
// zero means the bytes are on a server rather than absent.
//
// Cancelling ctx stops the walk as soon as the workers notice. Everything
// already sent on out stays valid, the returned Stats describe what was
// actually done, and the error is ctx.Err(). A partial scan is a real result,
// not a failure to be thrown away.
func Run(ctx context.Context, opts Options, out chan<- Item, progress func(Stats)) (Stats, error) {
	start := time.Now()

	roots, err := prepareRoots(opts)
	if err != nil {
		return Stats{}, err
	}
	if len(opts.Rules) == 0 {
		return Stats{}, ErrNoRules
	}

	w := &walk{
		opts:    opts,
		table:   newRuleTable(opts.Rules),
		filters: newFilters(opts),
		active:  newActiveCache(opts.ActiveDays),
		links:   newLinkSet(),
		out:     out,
		start:   start,
		emitted: make(map[string]struct{}),
	}

	stop := w.watchCancel(ctx)
	defer stop()

	workers := opts.workers()
	w.targets = make(chan target, workers*4)

	var sizers sync.WaitGroup
	for range workers {
		sizers.Add(1)
		go func() {
			defer sizers.Done()
			w.sizeLoop(ctx)
		}()
	}

	stopProgress := w.reportProgress(progress)

	walkErr := w.walkRoots(roots, workers)
	w.probeLocations(ctx)

	close(w.targets)
	sizers.Wait()
	stopProgress()

	stats := w.stats(time.Since(start))
	if progress != nil {
		progress(stats)
	}

	if cerr := ctx.Err(); cerr != nil {
		return stats, cerr
	}
	return stats, walkErr
}

// workers returns the configured worker count, or min(32, 2*NumCPU).
//
// TODO(milestone 2): drop to 4 workers on a spinning disk, detected with
// IOCTL_STORAGE_QUERY_PROPERTY / StorageDeviceSeekPenaltyProperty. Until that
// lands every volume is assumed to be solid state, which is the right guess
// on a developer machine and only costs seeks on the ones where it is wrong.
func (o Options) workers() int {
	if o.Workers > 0 {
		return o.Workers
	}
	n := runtime.NumCPU() * 2
	if n > maxWorkers {
		n = maxWorkers
	}
	if n < 1 {
		n = 1
	}
	return n
}

// target is one matched directory or file handed from the walker to a sizer.
type target struct {
	path     string
	rule     *Rule
	verified bool
	cloud    bool
	size     uint64
	isFile   bool
	modTime  time.Time
}

// walk holds everything one call to Run needs. Counters are atomic because
// fastwalk calls the callback from every worker goroutine at once.
type walk struct {
	opts    Options
	table   *ruleTable
	filters filters
	active  *activeCache
	links   *linkSet
	out     chan<- Item
	targets chan target
	start   time.Time

	// emitted keeps one scan from listing the same path twice.
	emittedMu sync.Mutex
	emitted   map[string]struct{}

	cancelled atomic.Bool

	dirsChecked  atomic.Uint64
	found        atomic.Uint64
	skipped      atomic.Uint64
	accessDenied atomic.Uint64
	bytes        atomic.Uint64
}

// stats snapshots the counters.
func (w *walk) stats(elapsed time.Duration) Stats {
	return Stats{
		DirsChecked:  w.dirsChecked.Load(),
		Found:        w.found.Load(),
		Skipped:      w.skipped.Load(),
		AccessDenied: w.accessDenied.Load(),
		Bytes:        w.bytes.Load(),
		Elapsed:      elapsed,
	}
}

// watchCancel flips an atomic flag when ctx is done, so the hot path in the
// walk callback reads a bool instead of taking the context's mutex on every
// directory entry. The returned function stops the watcher.
func (w *walk) watchCancel(ctx context.Context) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			w.cancelled.Store(true)
		case <-done:
		}
	}()
	return func() { close(done) }
}

// reportProgress starts the progress ticker and returns a function that stops
// it and waits for it to exit.
func (w *walk) reportProgress(progress func(Stats)) func() {
	if progress == nil {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(progressInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				progress(w.stats(time.Since(w.start)))
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

// walkRoots walks every root in turn. Each root is walked in parallel
// internally, so walking two roots at once would only divide the same budget.
func (w *walk) walkRoots(roots []string, workers int) error {
	conf := fastwalk.Config{
		Follow:     false, // Rule 8: a reparse point is a leaf, never a door.
		Sort:       fastwalk.SortNone,
		NumWorkers: workers,
	}

	var firstErr error
	for _, root := range roots {
		if w.cancelled.Load() {
			break
		}
		err := fastwalk.Walk(&conf, root, w.visit)
		switch {
		case err == nil, errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		case firstErr == nil:
			firstErr = fmt.Errorf("scan: walking %s: %w", root, err)
		}
	}
	return firstErr
}

// visit is the fastwalk callback. It runs on every walk worker at once, so it
// touches nothing that is not atomic or immutable.
//
// Return values matter more than they look. fastwalk only understands SkipDir
// from a directory entry; returning it for a file turns into a hard error
// that aborts the whole walk. So every non-directory path out of this
// function returns nil.
func (w *walk) visit(path string, d fs.DirEntry, err error) error {
	if w.cancelled.Load() {
		return context.Canceled
	}
	if err != nil {
		w.skipped.Add(1)
		if errors.Is(err, fs.ErrPermission) {
			w.accessDenied.Add(1)
		}
		// Never SkipDir here. fastwalk reports a directory it could not read
		// by calling back a second time with the error, and whatever that
		// call returns becomes the walk's own error: a SkipDir would abort
		// the entire scan over one unreadable folder. A name with a trailing
		// dot, which only the \\?\ prefix can open, is enough to trigger it.
		return nil
	}

	info, ierr := d.Info()
	if ierr != nil {
		w.skipped.Add(1)
		if d.IsDir() {
			return fastwalk.SkipDir
		}
		return nil
	}

	// Rule 8. A junction, a symlink or an AppExecLink is a leaf. Go reports
	// those without ModeDir, so fastwalk hands them here as ordinary entries
	// and never enqueues them; the attribute check is the second guard for
	// anything Go classified differently.
	if isLeafMode(info.Mode()) || isReparseInfo(info) {
		w.skipped.Add(1)
		return nil
	}

	if !d.IsDir() {
		return w.visitFile(path, d, info)
	}
	return w.visitDir(path, d, info)
}

// visitDir handles one directory: filter it, match it, prune it.
func (w *walk) visitDir(path string, d fs.DirEntry, info fs.FileInfo) error {
	name := d.Name()
	lower := strings.ToLower(name)

	if w.filters.skipDir(path, lower) {
		w.skipped.Add(1)
		return fastwalk.SkipDir
	}
	w.dirsChecked.Add(1)

	rule, verified := w.table.matchDir(lower, path)
	if rule == nil {
		return nil
	}

	// Rule 17. A placeholder directory holds nothing locally. Record it as a
	// zero-byte cloud item rather than reading it, which would download it.
	if isCloudInfo(info) {
		w.skipped.Add(1)
		w.emit(target{path: path, rule: rule, verified: verified, cloud: true, modTime: info.ModTime()})
		return fastwalk.SkipDir
	}

	w.emit(target{path: path, rule: rule, verified: verified, modTime: info.ModTime()})

	// Rule: prune on match. Nothing below a matched directory is ever listed
	// separately, which is what stops a project vendored inside another
	// project's node_modules appearing as its own row.
	return fastwalk.SkipDir
}

// visitFile handles one non-directory entry, which only matters to the large
// file rules.
func (w *walk) visitFile(path string, d fs.DirEntry, info fs.FileInfo) error {
	if len(w.table.files) == 0 {
		return nil
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if isCloudInfo(info) {
		w.skipped.Add(1)
		return nil
	}
	if w.filters.skipPath(path) {
		w.skipped.Add(1)
		return nil
	}
	size := entrySize(info)
	rule, ok := w.table.matchFile(strings.ToLower(d.Name()), size)
	if !ok {
		return nil
	}
	w.emit(target{
		path:     path,
		rule:     rule,
		verified: true,
		size:     size,
		isFile:   true,
		modTime:  info.ModTime(),
	})
	return nil
}

// emit hands a matched target to the sizer pool, or drops it if the scan is
// being cancelled. It never blocks forever: the pool always drains.
//
// A path is emitted at most once per scan. Two roots can overlap, and two
// rules can name the same fixed location: %TEMP% and %LOCALAPPDATA%\Temp are
// usually the same folder, and listing it twice would let a user tick the
// same gigabytes in two rows and be told they freed them twice.
func (w *walk) emit(t target) {
	if w.cancelled.Load() {
		return
	}
	if !w.firstTime(t.path) {
		return
	}
	w.targets <- t
}

// firstTime records a path and reports whether this scan has seen it before.
func (w *walk) firstTime(path string) bool {
	key := strings.ToLower(normalizePath(path))

	w.emittedMu.Lock()
	defer w.emittedMu.Unlock()
	if _, dup := w.emitted[key]; dup {
		return false
	}
	w.emitted[key] = struct{}{}
	return true
}

// probeLocations checks the fixed addresses of the location rules. Package
// caches and Windows temp folders live at known paths, so looking them up is
// a stat rather than a search.
func (w *walk) probeLocations(ctx context.Context) {
	for _, rule := range w.table.locs {
		for _, loc := range resolveLocations(rule) {
			if ctx.Err() != nil || w.cancelled.Load() {
				return
			}
			info, err := os.Lstat(loc)
			if err != nil || !info.IsDir() {
				continue
			}
			if isLeafMode(info.Mode()) || isReparseInfo(info) {
				w.skipped.Add(1)
				continue
			}
			if w.filters.skipPath(loc) {
				w.skipped.Add(1)
				continue
			}
			if !rule.HasMarker(loc) {
				continue
			}
			w.dirsChecked.Add(1)
			w.emit(target{path: loc, rule: rule, verified: true, modTime: info.ModTime()})
		}
	}
}

// sizeLoop is one sizer worker: take a matched target, measure it, turn it
// into an Item and send it on.
func (w *walk) sizeLoop(ctx context.Context) {
	for t := range w.targets {
		item, ok := w.measure(ctx, t)
		if !ok {
			continue
		}
		select {
		case w.out <- item:
			w.found.Add(1)
			w.bytes.Add(item.Size)
		case <-ctx.Done():
			return
		}
	}
}

// measure turns a matched target into a finished Item.
func (w *walk) measure(ctx context.Context, t target) (Item, bool) {
	if ctx.Err() != nil {
		return Item{}, false
	}

	project := filepath.Dir(t.path)
	lastUsed, active := w.active.lookup(project)

	item := Item{
		Path:          t.path,
		Name:          filepath.Base(t.path),
		Project:       project,
		Tier:          t.rule.Tier,
		Kind:          t.rule.Kind,
		Rule:          t.rule.Name,
		RestoreHint:   t.rule.RestoreHint,
		Markers:       append([]string(nil), t.rule.Markers...),
		MarkersInside: t.rule.MarkersInside,
		LastUsed:      lastUsed,
		Active:        active,
		Unverified:    !t.verified,
		Cloud:         t.cloud,
		ModTime:       t.modTime,
	}

	switch {
	case t.cloud:
		item.Size = 0
	case t.isFile:
		item.Size = t.size
	default:
		dedupe := w.opts.DedupeHardLinks || looksHardLinked(t.path)
		res := sizeDir(ctx, t.path, dedupe, w.links)
		item.Size = res.Bytes
		item.HardLinkedToStore = res.HardLinked
		if res.Skipped > 0 {
			w.skipped.Add(res.Skipped)
		}
		if res.AccessDenied > 0 {
			w.accessDenied.Add(res.AccessDenied)
		}
	}

	if ctx.Err() != nil {
		return Item{}, false
	}
	// An empty folder frees nothing. Reporting it would fill the results
	// table with rows worth zero bytes that a user has to read past to reach
	// the ones that matter. Cloud placeholders are the exception: their zero
	// is a fact about where the bytes live, not an absence of them.
	if item.Size == 0 && !item.Cloud {
		return Item{}, false
	}
	return item, true
}

// prepareRoots cleans, de-duplicates and vets the requested roots.
func prepareRoots(opts Options) ([]string, error) {
	if len(opts.Roots) == 0 {
		return nil, ErrNoRoots
	}
	seen := make(map[string]struct{}, len(opts.Roots))
	out := make([]string, 0, len(opts.Roots))

	for _, raw := range opts.Roots {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		abs, err := filepath.Abs(raw)
		if err != nil {
			return nil, fmt.Errorf("scan: resolving %s: %w", raw, err)
		}
		abs = filepath.Clean(abs)

		if !opts.AllowNetwork && (pathIsUNC(abs) || isRemoteDrive(abs)) {
			return nil, fmt.Errorf("scan: %s: %w", raw, ErrNetworkRoot)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("scan: opening %s: %w", raw, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("scan: %s: %w", raw, ErrNotADirectory)
		}

		key := strings.ToLower(abs)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, abs)
	}
	if len(out) == 0 {
		return nil, ErrNoRoots
	}
	return out, nil
}
