package clean

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	cleanengine "github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// batchEvery is how often the scan stream and the delete progress channel are
// cut into messages. Below about 100 ms the update loop spends its whole
// frame budget on counters; above about 250 ms the screen looks stuck.
const batchEvery = 150 * time.Millisecond

// ScanFunc is [scan.Run]. It is a field on the model rather than a direct
// call so tests can drive the whole screen without walking a disk.
type ScanFunc func(ctx context.Context, opts scan.Options, out chan<- scan.Item, progress func(scan.Stats)) (scan.Stats, error)

// CleanFunc is [clean.Run], the only thing in Devpit that deletes anything.
// It is injected for the same reason: no test ever removes a real file.
type CleanFunc func(ctx context.Context, items []cleanengine.Item, opts cleanengine.Options, progress func(cleanengine.Progress)) cleanengine.Report

// RetryFunc is [clean.Retry], used by the R key when an item was locked.
type RetryFunc func(ctx context.Context, it cleanengine.Item, opts cleanengine.Options, progress func(cleanengine.Progress)) cleanengine.Result

// SweepFunc is [clean.Sweep], which finishes tombstones an interrupted run
// left behind.
type SweepFunc func(ctx context.Context, roots []string, opts cleanengine.Options, progress func(cleanengine.Progress)) cleanengine.Report

// CacheLoader returns the last scan of a root, or an error. The cache
// directory is the loader's business, not the screen's.
type CacheLoader func(root string) (*scan.Cache, error)

// CacheSaver stores a finished scan.
type CacheSaver func(c *scan.Cache) error

// Engines is every outside-world entry point the screen uses. The zero value
// is not usable; [DefaultEngines] returns the real ones and tests pass fakes.
type Engines struct {
	// Scan walks the filesystem.
	Scan ScanFunc
	// Clean deletes.
	Clean CleanFunc
	// Retry deletes one item again after the user closed the program that
	// held it.
	Retry RetryFunc
	// Sweep finishes tombstones from an interrupted run.
	Sweep SweepFunc
	// LoadCache and SaveCache back the "stale · rescanning" badge.
	LoadCache CacheLoader
	SaveCache CacheSaver
	// Verify re-checks an item immediately before it is deleted. It is
	// carried on every clean.Item the screen builds, which is how safety
	// rule 10 reaches the delete engine.
	Verify func(scan.Item) error
}

// DefaultEngines returns the real engines.
func DefaultEngines() Engines {
	return Engines{
		Scan:      scan.Run,
		Clean:     cleanengine.Run,
		Retry:     cleanengine.Retry,
		Sweep:     cleanengine.Sweep,
		LoadCache: loadCache,
		SaveCache: saveCache,
		Verify:    scan.Verify,
	}
}

// loadCache reads the cached scan for root from Devpit's cache directory.
func loadCache(root string) (*scan.Cache, error) {
	dir, err := config.CacheDir()
	if err != nil {
		return nil, err
	}
	return scan.LoadCache(dir, root)
}

// saveCache writes a finished scan into Devpit's cache directory.
func saveCache(c *scan.Cache) error {
	dir, err := config.EnsureCacheDir()
	if err != nil {
		return err
	}
	return scan.SaveCache(dir, c)
}

// statsBox carries the scanner's counters from its goroutine to the stream
// batcher. The scanner calls set from its own workers, so the lock is not
// decoration.
type statsBox struct {
	mu sync.Mutex
	s  scan.Stats
}

func (b *statsBox) set(s scan.Stats) {
	b.mu.Lock()
	b.s = s
	b.mu.Unlock()
}

func (b *statsBox) get() scan.Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.s
}

// Messages the screen sends itself. They are unexported: nothing outside this
// package has any business producing them.
type (
	// scanBatchMsg carries one batch of newly found items.
	scanBatchMsg struct{ batch scan.Batch }
	// scanStreamClosedMsg says every item has been delivered.
	scanStreamClosedMsg struct{}
	// scanDoneMsg says the walker itself returned.
	scanDoneMsg struct {
		stats scan.Stats
		err   error
	}
	// cacheLoadedMsg carries the previous scan of this root, if there was
	// one. A miss is an ordinary outcome and arrives with a nil cache.
	cacheLoadedMsg struct {
		root  string
		cache *scan.Cache
	}
	// cacheSavedMsg reports the outcome of writing the cache.
	cacheSavedMsg struct{ err error }
	// deleteProgressMsg carries the most recent delete progress in the last
	// batchEvery window.
	deleteProgressMsg struct{ p cleanengine.Progress }
	// deleteStreamClosedMsg says the delete emitted its last progress.
	deleteStreamClosedMsg struct{}
	// deleteDoneMsg carries the finished report.
	deleteDoneMsg struct{ report cleanengine.Report }
	// retryDoneMsg carries the results of retrying the locked items.
	retryDoneMsg struct{ results []cleanengine.Result }
	// sweepDoneMsg carries the report of a tombstone sweep.
	sweepDoneMsg struct{ report cleanengine.Report }
)

// waitBatch reads one batch of scan results, blocking on its own goroutine
// the way every tea.Cmd does. Update never blocks; this is the whole reason
// the channel read lives in a command.
func waitBatch(ch <-chan scan.Batch) tea.Cmd {
	return func() tea.Msg {
		b, ok := <-ch
		if !ok {
			return scanStreamClosedMsg{}
		}
		return scanBatchMsg{batch: b}
	}
}

// waitScanDone reports the walker's return value.
func waitScanDone(ch <-chan scanDoneMsg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// waitDeleteProgress reads delete progress and coalesces it.
//
// The delete engine calls its progress function once per item and once per
// top-level child, which on a big node_modules is thousands of calls a
// second. This takes the first one, then keeps the most recent for
// batchEvery before returning it, so the update loop sees at most one message
// every 150 ms however fast the disk is.
func waitDeleteProgress(ch <-chan cleanengine.Progress) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return deleteStreamClosedMsg{}
		}
		deadline := time.After(batchEvery)
		for {
			select {
			case next, open := <-ch:
				if !open {
					return deleteProgressMsg{p: p}
				}
				p = next
			case <-deadline:
				return deleteProgressMsg{p: p}
			}
		}
	}
}

// waitReport reports a finished delete or sweep.
func waitReport(ch <-chan cleanengine.Report, wrap func(cleanengine.Report) tea.Msg) tea.Cmd {
	return func() tea.Msg { return wrap(<-ch) }
}
