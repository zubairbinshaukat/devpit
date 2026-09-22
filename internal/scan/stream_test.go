package scan_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// The batcher groups items by time rather than by count, so a scan that finds
// ten thousand folders still costs the UI one message every interval.
func TestBatcherEmitsAtIntervalAndFlushesOnClose(t *testing.T) {
	in := make(chan scan.Item)

	var mu sync.Mutex
	var batches []scan.Batch

	done := make(chan struct{})
	go func() {
		defer close(done)
		scan.Batcher(t.Context(), in, 20*time.Millisecond, func(b scan.Batch) {
			mu.Lock()
			batches = append(batches, b)
			mu.Unlock()
		})
	}()

	// Two items, then long enough for a tick, so they arrive as one batch.
	in <- scan.Item{Path: "a"}
	in <- scan.Item{Path: "b"}
	time.Sleep(80 * time.Millisecond)

	// A third item and an immediate close: the flush on close must deliver
	// it rather than dropping it.
	in <- scan.Item{Path: "c"}
	close(in)
	<-done

	mu.Lock()
	defer mu.Unlock()

	if len(batches) < 2 {
		t.Fatalf("got %d batches, want at least 2 (one on the tick, one on close): %+v", len(batches), batches)
	}
	var seen []string
	for _, b := range batches {
		if len(b.Items) == 0 {
			t.Error("an empty batch was emitted")
		}
		for _, it := range b.Items {
			seen = append(seen, it.Path)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("delivered %v, want exactly a, b and c", seen)
	}
	for i, want := range []string{"a", "b", "c"} {
		if seen[i] != want {
			t.Errorf("item %d = %q, want %q: order was not preserved", i, seen[i], want)
		}
	}
}

// Closing an empty channel emits nothing. A screen that receives a batch
// should always have something to draw.
func TestBatcherEmitsNothingForAnEmptyScan(t *testing.T) {
	in := make(chan scan.Item)
	close(in)

	called := false
	scan.Batcher(t.Context(), in, time.Millisecond, func(scan.Batch) { called = true })
	if called {
		t.Error("an empty scan produced a batch")
	}
}

// A cancelled scan still delivers what it had. Throwing away the last batch
// because the user pressed Esc would lose real findings.
func TestBatcherFlushesOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	in := make(chan scan.Item, 4)
	in <- scan.Item{Path: "a"}

	var got []scan.Item
	done := make(chan struct{})
	go func() {
		defer close(done)
		scan.Batcher(ctx, in, time.Hour, func(b scan.Batch) { got = append(got, b.Items...) })
	}()

	// Give the batcher a moment to take the item off the channel, then stop.
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	if len(got) != 1 || got[0].Path != "a" {
		t.Errorf("a cancelled batcher delivered %v, want the one pending item", got)
	}
}

// BatcherFunc attaches the counters to every batch, so the footer and the
// rows it sits under are always describing the same moment.
func TestBatcherFuncAttachesStats(t *testing.T) {
	in := make(chan scan.Item, 2)
	in <- scan.Item{Path: "a", Size: 10}
	close(in)

	var got scan.Batch
	scan.BatcherFunc(t.Context(), in, time.Millisecond, func() scan.Stats {
		return scan.Stats{Found: 1, Bytes: 10}
	}, func(b scan.Batch) { got = b })

	if got.Stats.Found != 1 || got.Stats.Bytes != 10 {
		t.Errorf("batch stats = %+v, want Found 1 and Bytes 10", got.Stats)
	}
}

// Stream is the same batcher with a channel on the front, for callers that
// would rather range than pass a callback.
func TestStreamClosesWhenTheScanEnds(t *testing.T) {
	in := make(chan scan.Item, 3)
	in <- scan.Item{Path: "a"}
	in <- scan.Item{Path: "b"}
	close(in)

	var total int
	for batch := range scan.Stream(t.Context(), in, 5*time.Millisecond, nil) {
		total += len(batch.Items)
	}
	if total != 2 {
		t.Errorf("Stream delivered %d items, want 2", total)
	}
}

// A batcher with no emit function does nothing rather than panicking.
func TestBatcherWithoutAnEmitFunction(t *testing.T) {
	in := make(chan scan.Item)
	close(in)
	scan.Batcher(t.Context(), in, time.Millisecond, nil)
}
