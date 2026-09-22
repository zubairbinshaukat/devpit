package scan

import (
	"context"
	"time"
)

// Batch is a group of items found since the last emission, plus the counters
// as they stood at that moment.
//
// Batching exists because the alternative does not work: a scan of a real
// projects folder finds thousands of items, and one message per item would
// spend the whole frame budget in the update loop. The results table wants
// one message every 150 ms holding everything that arrived in between.
type Batch struct {
	// Items are the items found since the previous batch. It is never empty
	// except in the final batch emitted when the channel closes with nothing
	// pending, which is not emitted at all.
	Items []Item
	// Stats are the counters at the moment the batch was cut, or the zero
	// value when Batcher was called without a stats source.
	Stats Stats
}

// Batcher drains in, groups what arrives into batches no more often than
// every, and hands each batch to emit.
//
// It is deliberately free of any Bubble Tea dependency: emit is an ordinary
// function, and a screen wraps it in whatever a tea.Cmd needs. That is the
// whole reason this helper is in the engine package rather than in the UI.
//
// Batcher returns when in is closed or ctx is done. On close it flushes
// whatever is still pending, so the last handful of items is never lost; on
// cancellation it also flushes, because a cancelled scan's partial results
// are still results. emit is called only from Batcher's own goroutine, never
// concurrently, and never with an empty Items slice.
func Batcher(ctx context.Context, in <-chan Item, every time.Duration, emit func(Batch)) {
	BatcherFunc(ctx, in, every, nil, emit)
}

// BatcherFunc is Batcher with a stats source. stats is called once per batch,
// on Batcher's goroutine, and its result is attached to the batch so the
// footer's counters and the rows it is showing always agree.
func BatcherFunc(ctx context.Context, in <-chan Item, every time.Duration, stats func() Stats, emit func(Batch)) {
	if emit == nil {
		return
	}
	if every <= 0 {
		every = 150 * time.Millisecond
	}

	var pending []Item

	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := Batch{Items: pending}
		if stats != nil {
			batch.Stats = stats()
		}
		pending = nil
		emit(batch)
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case item, ok := <-in:
			if !ok {
				flush()
				return
			}
			pending = append(pending, item)
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			flush()
			return
		}
	}
}

// Stream is the channel-returning form of Batcher, for callers that would
// rather range over batches than supply a callback. The returned channel is
// closed when the batcher finishes, which is the signal that the scan is
// over and every item has been delivered.
func Stream(ctx context.Context, in <-chan Item, every time.Duration, stats func() Stats) <-chan Batch {
	out := make(chan Batch, 1)
	go func() {
		defer close(out)
		BatcherFunc(ctx, in, every, stats, func(b Batch) {
			select {
			case out <- b:
			case <-ctx.Done():
			}
		})
	}()
	return out
}
