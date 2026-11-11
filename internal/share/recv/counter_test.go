package recv_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
)

// fakeShareCounter is a per-share byte counter that grows by step on every
// read, or fails when err is set.
type fakeShareCounter struct {
	step   uint64
	err    error
	n      atomic.Uint64
	unc    atomic.Value
	opened atomic.Int32
	closed atomic.Int32
}

func (f *fakeShareCounter) open(unc string) (func() (uint64, error), func(), error) {
	f.opened.Add(1)
	f.unc.Store(unc)
	return func() (uint64, error) {
		if f.err != nil {
			return 0, f.err
		}
		return f.n.Add(f.step), nil
	}, func() { f.closed.Add(1) }, nil
}

// withCounters rebuilds r's session over an adapter that grows by adapterStep
// on every read (none when zero) and the given share counter (none when nil).
func withCounters(r *rig, adapterStep uint64, share func(string) (func() (uint64, error), func(), error)) {
	r.deps.Counters = nil
	if adapterStep > 0 {
		var in atomic.Uint64
		r.deps.Counters = func(string) (netstat.Counters, error) {
			return func() (uint64, uint64, error) { return in.Add(adapterStep), 0, nil }, nil
		}
	}
	r.deps.ShareCounter = share
	r.s = recv.New(r.deps)
}

// heldRun runs one robocopy step that reports file "a" (1000 bytes) of a
// larger copy and then holds for a while, as robocopy /MT does while a big
// file copies, and returns every event.
func heldRun(t *testing.T, r *rig, total, nFiles int64) []recv.Event {
	t.Helper()
	hold := make(chan struct{})
	r.runner.steps = []step{{text: files("a"), code: 1, hold: hold}}
	j := startJob(r, total, nFiles)
	go func() {
		time.Sleep(80 * time.Millisecond)
		close(hold)
	}()
	var mu sync.Mutex
	var events []recv.Event
	if _, err := r.s.Run(context.Background(), j, func(e recv.Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) < 5 {
		t.Fatalf("%d events, want a run long enough to tick", len(events))
	}
	return events
}

// The live case: the receiver's counter saw far more than the files on disk
// (other downloads on the adapter), and the bar said 100%, 0s left, while
// robocopy had reported none of the 8 files.
func TestProgressNeverReachesTheTotalBeforeRobocopySaysSo(t *testing.T) {
	const total, nFiles = 3000, 3
	for name, set := range map[string]func(r *rig){
		"adapter": func(r *rig) { withCounters(r, 10_000_000_000, nil) },
		"share": func(r *rig) {
			withCounters(r, 0, (&fakeShareCounter{step: 10_000_000_000}).open)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			set(r)
			events := heldRun(t, r, total, nFiles)
			sawSpeed := false
			for _, e := range events {
				if e.Phase != recv.PhaseCopying || e.DoneFiles >= e.TotalFiles {
					continue
				}
				if e.DoneBytes >= e.TotalBytes || e.DoneBytes > total*99/100 {
					t.Fatalf("%d of %d bytes with %d of %d files reported: the counter took the bar to the total",
						e.DoneBytes, e.TotalBytes, e.DoneFiles, e.TotalFiles)
				}
				if e.Speed >= 1 {
					sawSpeed = true
					if e.ETA <= 0 {
						t.Fatalf("ETA %v at %v B/s with %d of %d files done", e.ETA, e.Speed, e.DoneFiles, e.TotalFiles)
					}
				}
			}
			if !sawSpeed {
				t.Error("no event carried a speed")
			}
		})
	}
}

func TestTheShareCounterIsPreferredOverTheAdapter(t *testing.T) {
	r := newRig(t)
	share := &fakeShareCounter{step: 100}
	withCounters(r, 10_000_000_000, share.open) // the adapter also counts a big download
	events := heldRun(t, r, 1_000_000, 2)
	var maxDone int64
	sawSpeed := false
	for _, e := range events {
		if e.Phase != recv.PhaseCopying {
			continue
		}
		if e.Estimated {
			t.Fatalf("an event was marked estimated while the share counter read: %+v", e)
		}
		if e.Speed >= 1e9 {
			t.Fatalf("speed %v came from the adapter, not the share", e.Speed)
		}
		sawSpeed = sawSpeed || e.Speed > 0
		maxDone = max(maxDone, e.DoneBytes)
	}
	if !sawSpeed {
		t.Error("no event carried the share counter's speed")
	}
	if maxDone > 100_000 {
		t.Errorf("progress reached %d: the adapter's count was used", maxDone)
	}
	if got, _ := share.unc.Load().(string); got != `\\10.0.0.9\Games` {
		t.Errorf("share counter opened for %q, want the copy's own share", got)
	}
	if share.opened.Load() != 1 || share.closed.Load() != 1 {
		t.Errorf("share counter opened %d, closed %d; want one each", share.opened.Load(), share.closed.Load())
	}
}

func TestTheAdapterFallbackIsMarkedEstimated(t *testing.T) {
	cases := map[string]func(string) (func() (uint64, error), func(), error){
		"no share counter": nil,
		"open fails": func(string) (func() (uint64, error), func(), error) {
			return nil, nil, errors.New("SMB counters not installed")
		},
		"reads fail": (&fakeShareCounter{err: errors.New("no instance")}).open,
	}
	for name, share := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			withCounters(r, 50, share)
			sawSpeed := false
			for _, e := range heldRun(t, r, 1_000_000, 2) {
				if e.Phase != recv.PhaseCopying {
					continue
				}
				if !e.Estimated {
					t.Fatalf("an adapter-counted event was not marked estimated: %+v", e)
				}
				sawSpeed = sawSpeed || e.Speed > 0
			}
			if !sawSpeed {
				t.Error("the adapter fallback gave no speed")
			}
		})
	}

	t.Run("no counter at all", func(t *testing.T) {
		r := newRig(t)
		withCounters(r, 0, nil)
		for _, e := range heldRun(t, r, 1_000_000, 2) {
			if e.Estimated {
				t.Fatalf("robocopy's own lines were marked estimated: %+v", e)
			}
		}
	})
}
