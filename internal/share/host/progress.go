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
	// SourceShare is the share's own byte counter: the bytes every PC read
	// from this share, and nothing else this PC sent. It is exact for the
	// share (several PCs copying at once are added together).
	SourceShare ProgressSource = "share"
	// SourceAdapter is the sending adapter's out counter: everything that
	// adapter sent, not only this share. It is an estimate.
	SourceAdapter ProgressSource = "adapter"
)

// FlowWindow is how long after the last growth of the sent count a transfer
// still counts as flowing.
const FlowWindow = 5 * time.Second

// Progress is what a share has sent so far. Devpit is not in the data path
// (Windows serves the share), so this is read from a byte counter: the share's
// own when Windows has one for it, the adapter's otherwise (see Source).
type Progress struct {
	// Source is where Sent comes from. [Progress.Exact] says whether it counts
	// this share only.
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

// Exact reports whether Sent and Rate count this share alone ([SourceShare]),
// rather than everything the adapter sent ([SourceAdapter]).
func (p Progress) Exact() bool { return p.Source == SourceShare }

// tally is one byte counter's running count since its baseline.
type tally struct {
	meter *netstat.Meter
	// read is true once the counter has given a good reading.
	read bool
	prev uint64 // the meter's last reading
	sent uint64 // bytes since the baseline, kept across a counter reset
}

func newTally(read func() (uint64, error), now func() time.Time) *tally {
	t := &tally{}
	t.meter = netstat.NewByteMeter(func() (uint64, error) {
		v, err := read()
		if err == nil {
			t.read = true
		}
		return v, err
	}, now)
	return t
}

// sample takes one reading and reports the speed and whether the count grew.
func (t *tally) sample() (rate float64, grew bool) {
	rate, seen := t.meter.Sample()
	switch {
	case seen > t.prev:
		t.sent += seen - t.prev
		t.prev = seen
		grew = true
	case seen < t.prev:
		// The counter went back (the adapter reset): count again from here,
		// keeping what was already sent.
		t.prev = seen
	}
	return rate, grew
}

// traffic turns a byte counter into a [Progress]. It is safe for concurrent
// use: a screen samples while the folder measure stores its result.
type traffic struct {
	mu     sync.Mutex
	now    func() time.Time
	source ProgressSource

	counter *tally
	// share is this share's own counter, preferred over counter once it has
	// read; nil when there is none.
	share *tally

	firstGrow time.Time
	lastGrow  time.Time

	total        int64
	totalKnown   bool
	totalPartial bool
}

// newTraffic starts counting over read. Nothing is read until the first
// [traffic.sample], which sets the baseline.
func newTraffic(read func() (uint64, error), src ProgressSource, now func() time.Time) *traffic {
	return &traffic{counter: newTally(read, now), now: now, source: src}
}

// preferShare adds the share's own counter. Once it gives a good reading,
// Sent and Rate come from it and Source is [SourceShare]. Call it before the
// first sample.
func (t *traffic) preferShare(read func() (uint64, error)) {
	t.mu.Lock()
	t.share = newTally(read, t.now)
	t.mu.Unlock()
}

// sample takes one reading and returns the progress as of now.
func (t *traffic) sample() Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	rate, grew := t.counter.sample()
	sent, src := t.counter.sent, t.source
	if t.share != nil {
		srate, sgrew := t.share.sample()
		if t.share.read {
			rate, grew, sent, src = srate, sgrew, t.share.sent, SourceShare
		}
	}
	now := t.now()
	if grew {
		if t.firstGrow.IsZero() {
			t.firstGrow = now
		}
		t.lastGrow = now
	}
	p := Progress{
		Source: src, Sent: sent, Rate: rate,
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
