package host

import (
	"errors"
	"testing"
	"time"
)

// counter is a fake byte counter and clock.
type counter struct {
	n    uint64
	fail bool
	now  time.Time
}

func (c *counter) read() (uint64, error) {
	if c.fail {
		return 0, errors.New("adapter gone")
	}
	return c.n, nil
}
func (c *counter) clock() time.Time { return c.now }

func newCounterTraffic(start uint64) (*traffic, *counter) {
	c := &counter{n: start, now: time.Unix(1000, 0)}
	return newTraffic(c.read, SourceAdapter, c.clock), c
}

func TestBaselineExcludesBytesSentBeforeTheShare(t *testing.T) {
	tr, c := newCounterTraffic(7_000_000_000) // sent long before sharing
	p := tr.sample()
	if p.Sent != 0 || p.Visible() || p.Flowing {
		t.Fatalf("first sample = %+v, want nothing sent", p)
	}
	c.n += 2048
	c.now = c.now.Add(time.Second)
	if p = tr.sample(); p.Sent != 2048 || !p.Visible() || p.Source != SourceAdapter {
		t.Errorf("sample = %+v, want 2048 sent from the adapter", p)
	}
}

func TestFlowingBecomesIdleAfterFlowWindow(t *testing.T) {
	tr, c := newCounterTraffic(0)
	tr.sample()
	c.n += 1 << 20
	c.now = c.now.Add(time.Second)
	if p := tr.sample(); !p.Flowing || p.IdleFor != 0 {
		t.Fatalf("while growing = %+v", p)
	}
	c.now = c.now.Add(FlowWindow) // exactly the window: still flowing
	if p := tr.sample(); !p.Flowing || p.IdleFor != FlowWindow {
		t.Errorf("at the window = %+v", p)
	}
	c.now = c.now.Add(time.Second)
	p := tr.sample()
	if p.Flowing || p.IdleFor != FlowWindow+time.Second {
		t.Errorf("past the window = %+v, want idle", p)
	}
	if p.Rate <= 0 {
		t.Errorf("the smoothed rate is %v; Flowing must not depend on it", p.Rate)
	}
}

func TestActiveSpanFreezesWhenIdle(t *testing.T) {
	tr, c := newCounterTraffic(0)
	tr.sample()
	for range 3 { // growth at 1s, 2s, 3s
		c.n += 1000
		c.now = c.now.Add(time.Second)
		tr.sample()
	}
	if p := tr.sample(); p.Active != 2*time.Second {
		t.Fatalf("active = %v, want first growth to last growth (2s)", p.Active)
	}
	c.now = c.now.Add(time.Minute)
	if p := tr.sample(); p.Active != 2*time.Second || p.IdleFor != time.Minute {
		t.Errorf("idle: active %v, idle %v; want active frozen at 2s", p.Active, p.IdleFor)
	}
	c.n += 1000
	c.now = c.now.Add(time.Second)
	if p := tr.sample(); p.Active != time.Minute+3*time.Second {
		t.Errorf("after it moves again: active = %v", p.Active)
	}
}

func TestAReadErrorKeepsTheLastValue(t *testing.T) {
	tr, c := newCounterTraffic(0)
	tr.sample()
	c.n += 5000
	c.now = c.now.Add(time.Second)
	good := tr.sample()
	c.fail = true
	c.now = c.now.Add(time.Second)
	p := tr.sample()
	if p.Sent != good.Sent || p.Rate != good.Rate || p.Sent != 5000 {
		t.Errorf("after an error = %+v, want %+v", p, good)
	}
	c.fail = false
	c.n += 100
	c.now = c.now.Add(time.Second)
	if p = tr.sample(); p.Sent != 5100 {
		t.Errorf("after recovering sent = %d, want 5100", p.Sent)
	}
}

func TestACounterResetKeepsWhatWasSent(t *testing.T) {
	tr, c := newCounterTraffic(0)
	tr.sample()
	c.n = 9000
	c.now = c.now.Add(time.Second)
	tr.sample()
	c.n = 10 // the adapter reset
	c.now = c.now.Add(time.Second)
	if p := tr.sample(); p.Sent != 9000 {
		t.Fatalf("after the reset sent = %d, want 9000 kept", p.Sent)
	}
	c.n = 510
	c.now = c.now.Add(time.Second)
	if p := tr.sample(); p.Sent != 9500 {
		t.Errorf("sent = %d, want 9500", p.Sent)
	}
}

func TestTotalArrivesLaterWithoutResettingSent(t *testing.T) {
	tr, c := newCounterTraffic(0)
	tr.sample()
	c.n += 4096
	c.now = c.now.Add(time.Second)
	if p := tr.sample(); p.TotalKnown || p.Total != 0 || p.Sent != 4096 {
		t.Fatalf("before the measure = %+v", p)
	}
	tr.setTotal(FolderSize{Bytes: 1 << 30, Files: 12})
	p := tr.sample()
	if !p.TotalKnown || p.TotalPartial || p.Total != 1<<30 || p.Sent != 4096 {
		t.Errorf("after the measure = %+v, want the total and the same Sent", p)
	}
}

func TestPartialFolderMeasureMarksTheTotalPartial(t *testing.T) {
	tr, _ := newCounterTraffic(0)
	tr.setTotal(FolderSize{Bytes: 500, Files: 2, Skipped: 1})
	if p := tr.sample(); !p.TotalKnown || !p.TotalPartial || p.Total != 500 {
		t.Errorf("progress = %+v, want a known but partial total", p)
	}
}

func TestTheShareCounterIsPreferredOverTheAdapterForSending(t *testing.T) {
	tr, adapter := newCounterTraffic(0)
	share := &counter{now: adapter.now}
	tr.preferShare(share.read)
	tr.sample()
	adapter.n += 10_000_000_000 // other traffic on the adapter
	share.n += 2048
	adapter.now = adapter.now.Add(time.Second)
	p := tr.sample()
	if p.Source != SourceShare || !p.Exact() || p.Sent != 2048 {
		t.Errorf("progress = %+v, want 2048 sent, exact, from the share", p)
	}
	if !p.Flowing || p.Rate > 1e6 {
		t.Errorf("rate %v flowing %v, want the share's speed", p.Rate, p.Flowing)
	}
}

func TestTheSenderUsesTheAdapterUntilTheShareCounterReads(t *testing.T) {
	tr, adapter := newCounterTraffic(0)
	share := &counter{fail: true}
	tr.preferShare(share.read)
	tr.sample()
	adapter.n += 3000
	adapter.now = adapter.now.Add(time.Second)
	if p := tr.sample(); p.Source != SourceAdapter || p.Exact() || p.Sent != 3000 {
		t.Fatalf("share unreadable: progress = %+v, want the adapter's 3000, not exact", p)
	}
	share.fail = false // the share's instance appeared
	share.n = 100
	adapter.now = adapter.now.Add(time.Second)
	tr.sample() // its baseline
	share.n += 500
	adapter.now = adapter.now.Add(time.Second)
	if p := tr.sample(); p.Source != SourceShare || p.Sent != 500 {
		t.Errorf("share readable: progress = %+v, want the share's 500, exact", p)
	}
}
