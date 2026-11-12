// Package extracttest holds fakes for driving internal/extract in tests: a
// scripted hatchzip runner, an in-memory StatFS, a manual clock and an
// in-memory record store. Engine wires a real *extract.Engine over all of
// them, so the engine's own tests and the screens' tests run the whole
// lifecycle (check, ticket, start, progress, pause, each ending) without
// hatchzip, a disk or real time.
package extracttest

import (
	"context"
	"sync"
	"time"
)

// Epoch is the fake clock's start: a fixed instant, so no frame or record
// shows a real date.
func Epoch() time.Time { return time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC) }

// Clock is a manual clock. Sleep blocks until Advance moves time past the
// deadline or the context ends.
type Clock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*waiter
}

type waiter struct {
	at time.Time
	ch chan struct{}
}

// NewClock starts a clock at start.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

// Now is the fake time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep waits d of fake time, or until ctx ends.
func (c *Clock) Sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	w := &waiter{at: c.now.Add(d), ch: make(chan struct{})}
	c.waiters = append(c.waiters, w)
	c.mu.Unlock()
	select {
	case <-w.ch:
	case <-ctx.Done():
		c.mu.Lock()
		for i, x := range c.waiters {
			if x == w {
				c.waiters = append(c.waiters[:i], c.waiters[i+1:]...)
				break
			}
		}
		c.mu.Unlock()
	}
}

// Advance moves time forward by d and wakes every sleeper whose deadline has
// passed.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	keep := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			close(w.ch)
			continue
		}
		keep = append(keep, w)
	}
	c.waiters = keep
}

// Sleepers is how many goroutines are sleeping on the clock now.
func (c *Clock) Sleepers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}
