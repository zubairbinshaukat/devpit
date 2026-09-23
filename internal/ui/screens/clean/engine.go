package clean

import (
	"context"
	"os"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	cleanengine "github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/telemetry"
	"github.com/zubairbinshaukat/devpit/internal/version"
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

// ReportFunc sends one opt-in usage-stats report for a finished cleanup.
// rules maps an item's lower-cased path to the scan rule that matched it, and
// only the rule names ever leave the machine. It is injected like every other
// outside-world call so a test can watch what the screen would send without a
// network, and so nothing in the screen has to know what a report looks like.
//
// The returned error exists for tests. The screen discards it: a failed
// report is never the user's problem.
type ReportFunc func(ctx context.Context, cfg config.Config, rep cleanengine.Report, rules map[string]string) error

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
	// Report sends the opt-in usage-stats report. It is only ever called
	// when [telemetry.Enabled] says so.
	Report ReportFunc
	// Getenv reads the environment, so a test can decide what the two
	// telemetry kill switches say without touching the process environment.
	Getenv func(string) string
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
		Report:    sendReport,
		Getenv:    os.Getenv,
	}
}

// sendReport posts one usage-stats report. Whether it may run at all is the
// screen's decision, taken before this is ever called.
func sendReport(ctx context.Context, cfg config.Config, rep cleanengine.Report, rules map[string]string) error {
	client := telemetry.New(telemetry.Options{
		Version:   version.Short(),
		InstallID: cfg.InstallID,
	})
	out, ok := client.Build(rep, rules)
	if !ok {
		return nil
	}
	return client.Send(ctx, out)
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
