package host

import (
	"sync"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
)

// ProgressSource says where the byte count in a [Progress] comes from, so a
// screen can word it honestly.
type ProgressSource string

const (
	// SourceShare is a count of the bytes served by the share itself.
	SourceShare ProgressSource = "share"
	// SourceAdapter is the sending adapter's out counter: everything that
	// adapter sent, not only this share. It is an estimate.
	SourceAdapter ProgressSource = "adapter"
)

// FlowWindow is how long after the last growth of the sent count a transfer
// still counts as flowing.
const FlowWindow = 5 * time.Second

// Progress is what a share has sent so far. Devpit is not in the data path (Windows
// serves the share), so this is an estimate read from a byte counter.
type Progress struct {
	// Source is where Sent comes from.
	Source ProgressSource
	// Sent is the bytes sent since the share came up. The first good reading
	// is the starting point, so what the PC sent before is not counted.
	Sent uint64
	// Rate is the smoothed speed in bytes per second.
	Rate float64
	// Flowing is true when Sent grew within [FlowWindow]. It follows the raw
	// growth, not the smoothed Rate, which fades out slowly.
	Flowing bool
	// Active is the time from the first growth of Sent to the latest one. It
	// stops counting while nothing moves.
	Active time.Duration
	// IdleFor is the time since Sent last grew; zero if it never has.
	IdleFor time.Duration
	// Total is the size of the shared folder in bytes, when TotalKnown.
	Total int64
	// TotalKnown is true once the folder has been measured.
	TotalKnown bool
	// TotalPartial is true when the measure left something out (links,
	// unreadable folders), so Total may be short.
	TotalPartial bool
}

// Visible reports whether there is anything to show: some bytes have been sent.
func (p Progress) Visible() bool { return p.Sent > 0 }

// traffic turns a byte counter into a [Progress]. It is safe for concurrent
// use: a screen samples while the folder measure stores its result.
type traffic struct {
	mu     sync.Mutex
	meter  *netstat.Meter
	now    func() time.Time
	source ProgressSource

	prev      uint64 // the meter's last reading
	sent      uint64 // bytes since the baseline, kept across a counter reset
	firstGrow time.Time
	lastGrow  time.Time

	total        int64
	totalKnown   bool
	totalPartial bool
}

// newTraffic starts counting over read. Nothing is read until the first
// [traffic.sample], which sets the baseline.
func newTraffic(read func() (uint64, error), src ProgressSource, now func() time.Time) *traffic {
	return &traffic{meter: netstat.NewByteMeter(read, now), now: now, source: src}
}

// sample takes one reading and returns the progress as of now.
func (t *traffic) sample() Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	rate, seen := t.meter.Sample()
	now := t.now()
	switch {
	case seen > t.prev:
		t.sent += seen - t.prev
		t.prev = seen
		if t.firstGrow.IsZero() {
			t.firstGrow = now
		}
		t.lastGrow = now
	case seen < t.prev:
		// The counter went back (the adapter reset): count again from here,
		// keeping what was already sent.
		t.prev = seen
	}
	p := Progress{
		Source: t.source, Sent: t.sent, Rate: rate,
		Total: t.total, TotalKnown: t.totalKnown, TotalPartial: t.totalPartial,
	}
	if !t.lastGrow.IsZero() {
		p.IdleFor = max(now.Sub(t.lastGrow), 0)
		p.Flowing = p.IdleFor <= FlowWindow
		p.Active = t.lastGrow.Sub(t.firstGrow)
	}
	return p
}

// setTotal stores the measured folder size. It does not touch what was sent.
func (t *traffic) setTotal(s FolderSize) {
	t.mu.Lock()
	t.total, t.totalKnown, t.totalPartial = s.Bytes, true, s.Skipped > 0
	t.mu.Unlock()
}
