package host

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
)

// progRig is a manager over fakes whose byte counter and folder measure the
// test controls.
type progRig struct {
	m        *Manager
	admin    *fakeAdmin
	out      atomic.Uint64 // the fake adapter's out counter
	opened   atomic.Int32  // counters opened
	closed   atomic.Int32  // counters released
	measures atomic.Int32  // folder measures started

	// measureCtx is the context the last measure got; measureDone is closed
	// when a measure that blocks has returned.
	mu          sync.Mutex
	measureCtx  context.Context
	measureRoot string
	measureDone chan struct{}
	size        FolderSize
	block       bool // the measure waits for its context instead of answering
}

func newProgRig(t *testing.T, src ProgressSource) *progRig {
	t.Helper()
	unregisterAll()
	r := &progRig{
		admin:       &fakeAdmin{sidLines: []string{"SID=S-1-5-21-1-2-3-1004"}},
		measureDone: make(chan struct{}),
	}
	clock := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	var cmu sync.Mutex
	r.m = NewManager(Deps{
		Launch: func(context.Context) (Admin, error) { return r.admin, nil },
		Net:    fakeNet{adapters: []Adapter{lan}, cat: CategoryPrivate},
		Dir:    t.TempDir(),
		Rand:   seeded(11),
		Now: func() time.Time {
			cmu.Lock()
			defer cmu.Unlock()
			clock = clock.Add(time.Second)
			return clock
		},
		KeepAwake: func() (func(), error) { return func() {}, nil },
		PID:       4321,
		Getenv:    env,
		Counter: func(share string, a Adapter) (func() (uint64, error), ProgressSource, func()) {
			r.opened.Add(1)
			if a.Index != lan.Index || share == "" {
				t.Errorf("the counter was opened for %q on adapter %d", share, a.Index)
			}
			return func() (uint64, error) { return r.out.Load(), nil }, src, func() { r.closed.Add(1) }
		},
		Measure: func(ctx context.Context, root string) (FolderSize, error) {
			r.measures.Add(1)
			r.mu.Lock()
			r.measureCtx, r.measureRoot = ctx, root
			block, size := r.block, r.size
			r.mu.Unlock()
			if block {
				<-ctx.Done()
				close(r.measureDone)
				return FolderSize{}, ctx.Err()
			}
			return size, nil
		},
	})
	t.Cleanup(unregisterAll)
	return r
}

func (r *progRig) start(t *testing.T) {
	t.Helper()
	if _, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestProgressIsZeroBeforeStartAndAfterStop(t *testing.T) {
	r := newProgRig(t, SourceAdapter)
	if p := r.m.Progress(); p != (Progress{}) {
		t.Fatalf("before Start = %+v", p)
	}
	r.out.Store(1000)
	r.start(t)
	r.out.Add(5000)
	if p := r.m.Progress(); p.Sent != 5000 || !p.Visible() {
		t.Fatalf("while sharing = %+v, want 5000 sent since the share came up", p)
	}
	if err := r.m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p := r.m.Progress(); p != (Progress{}) {
		t.Errorf("after Stop = %+v", p)
	}
	if r.opened.Load() != 1 || r.closed.Load() != 1 {
		t.Errorf("counter opened %d, released %d; want one each", r.opened.Load(), r.closed.Load())
	}
}

func TestStopCancelsTheFolderMeasure(t *testing.T) {
	r := newProgRig(t, SourceAdapter)
	r.block = true
	r.start(t)
	waitFor(t, "the measure to start", func() bool { return r.measures.Load() == 1 })
	r.mu.Lock()
	ctx, root := r.measureCtx, r.measureRoot
	r.mu.Unlock()
	if root != `D:\Games` {
		t.Errorf("measured %q, want the shared folder", root)
	}
	if ctx.Err() != nil {
		t.Fatal("the measure was cancelled while sharing")
	}
	if err := r.m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.measureDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the folder measure was still running after Stop")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Errorf("measure context error = %v", ctx.Err())
	}
}

func TestShutdownAllCancelsTheFolderMeasure(t *testing.T) {
	r := newProgRig(t, SourceAdapter)
	r.block = true
	r.start(t)
	waitFor(t, "the measure to start", func() bool { return r.measures.Load() == 1 })
	if err := ShutdownAll(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.measureDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the folder measure was still running after ShutdownAll")
	}
}

func TestTheMeasureDoesNotEndWithTheCallersContext(t *testing.T) {
	r := newProgRig(t, SourceAdapter)
	r.block = true
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := r.m.Start(ctx, Options{Path: `D:\Games`, Adapter: lan}, nil); err != nil {
		t.Fatal(err)
	}
	cancel() // the screen's command finished
	waitFor(t, "the measure to start", func() bool { return r.measures.Load() == 1 })
	r.mu.Lock()
	mctx := r.measureCtx
	r.mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	if mctx.Err() != nil {
		t.Error("the caller's context ended the folder measure")
	}
	_ = r.m.Stop(context.Background())
}

func TestAFailedStartCancelsTheMeasure(t *testing.T) {
	// A start that fails at the last operation never reaches the point where
	// the counter is opened or a measure begins, and nothing is left running.
	r := newProgRig(t, SourceAdapter)
	r.admin.failOp, r.admin.failErr = elevate.ShareOpShare, errors.New("share refused")
	if _, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil); err == nil {
		t.Fatal("Start should have failed")
	}
	if p := r.m.Progress(); p != (Progress{}) {
		t.Errorf("progress after a failed start = %+v", p)
	}
	if r.opened.Load() != r.closed.Load() {
		t.Errorf("counter opened %d times, released %d", r.opened.Load(), r.closed.Load())
	}
	if r.measures.Load() != 0 {
		t.Errorf("%d folder measures started for a share that never came up", r.measures.Load())
	}
	if r.m.Active() {
		t.Error("the manager is still active")
	}
}

func TestAStopThatFailsStillEndsTheProgressWork(t *testing.T) {
	r := newProgRig(t, SourceAdapter)
	r.block = true
	r.start(t)
	waitFor(t, "the measure to start", func() bool { return r.measures.Load() == 1 })
	r.admin.mu.Lock()
	r.admin.failOp, r.admin.failErr = elevate.ShareOpStop, errors.New("worker stuck")
	r.admin.mu.Unlock()
	if err := r.m.Stop(context.Background()); err == nil {
		t.Fatal("Stop should have failed")
	}
	select {
	case <-r.measureDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the measure outlived a failed Stop")
	}
	if r.closed.Load() != 1 {
		t.Errorf("counter released %d times", r.closed.Load())
	}
	if p := r.m.Progress(); p != (Progress{}) {
		t.Errorf("progress = %+v, want zero once the work is ended", p)
	}
}

func TestProgressNeedsNoAdminAndAddsNoWorkerOperation(t *testing.T) {
	r := newProgRig(t, SourceAdapter)
	r.size = FolderSize{Bytes: 4096, Files: 2}
	r.start(t)
	before := r.admin.opNames()
	if before != "firewall,account,grant,share" {
		t.Fatalf("operations = %s", before)
	}
	// The worker is gone as far as Progress is concerned: it must not need it.
	r.admin.mu.Lock()
	r.admin.failOp, r.admin.failErr = elevate.ShareOpStop, errors.New("no worker")
	r.admin.mu.Unlock()
	for i := 0; i < 20; i++ {
		r.out.Add(100)
		_ = r.m.Progress()
	}
	if after := r.admin.opNames(); after != before {
		t.Errorf("Progress sent worker operations: %s -> %s", before, after)
	}
	waitFor(t, "the folder size", func() bool { return r.m.Progress().TotalKnown })
	if p := r.m.Progress(); p.Total != 4096 || p.TotalPartial || p.Sent != 2000 {
		t.Errorf("progress = %+v", p)
	}
	r.admin.mu.Lock()
	r.admin.failOp = ""
	r.admin.mu.Unlock()
	_ = r.m.Stop(context.Background())
}

func TestProgressIsSafeFromManyGoroutines(t *testing.T) {
	r := newProgRig(t, SourceAdapter)
	r.size = FolderSize{Bytes: 10, Files: 1}
	r.start(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				r.out.Add(1)
				_ = r.m.Progress()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(time.Millisecond)
		_ = r.m.Stop(context.Background())
	}()
	wg.Wait()
}

func TestProgressSourceDefaultsToTheAdapter(t *testing.T) {
	d := Deps{}.withDefaults()
	read, src, closeFn := d.Counter("Games-abcd", lan)
	if src != SourceAdapter {
		t.Errorf("default source = %q, want %q", src, SourceAdapter)
	}
	if read == nil || closeFn == nil {
		t.Fatal("the default counter has no reader or no release")
	}
	_, _ = read() // an error on a machine without that adapter is fine
	closeFn()
	if d.Measure == nil {
		t.Error("no default folder measure")
	}

	r := newProgRig(t, SourceAdapter)
	r.start(t)
	if p := r.m.Progress(); p.Source != SourceAdapter {
		t.Errorf("progress source = %q", p.Source)
	}
	_ = r.m.Stop(context.Background())

	r2 := newProgRig(t, SourceShare)
	r2.start(t)
	if p := r2.m.Progress(); p.Source != SourceShare {
		t.Errorf("a per-share counter reported source %q", p.Source)
	}
	_ = r2.m.Stop(context.Background())
}
