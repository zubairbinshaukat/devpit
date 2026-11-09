package netstat_test

import (
	"errors"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
)

func TestSendMeterReadsOutNotIn(t *testing.T) {
	now := time.Unix(1000, 0)
	var in, out uint64
	m := netstat.NewSendMeter(func() (uint64, uint64, error) { return in, out, nil }, func() time.Time { return now })
	m.Sample()

	// Bytes received must not count as bytes sent.
	in += 900_000_000
	now = now.Add(time.Second)
	if r, got := m.Sample(); r != 0 || got != 0 {
		t.Fatalf("received bytes moved a send meter: %v, %d", r, got)
	}

	out += 40_000_000
	now = now.Add(time.Second)
	if r, got := m.Sample(); r != 40_000_000 || got != 40_000_000 {
		t.Errorf("sent bytes = %v, %d; want 40 MB/s and 40 MB", r, got)
	}
}

func TestSendMeterSurvivesCounterReset(t *testing.T) {
	now := time.Unix(1000, 0)
	var out uint64
	m := netstat.NewSendMeter(func() (uint64, uint64, error) { return 0, out, nil }, func() time.Time { return now })
	m.Sample()
	out += 100_000_000
	now = now.Add(time.Second)
	rate, _ := m.Sample()

	out = 5 // the adapter reset
	now = now.Add(time.Second)
	if r, got := m.Sample(); r != rate || got != 0 {
		t.Errorf("after a reset = %v, %d; want the old rate and 0 sent", r, got)
	}
	out += 1000
	now = now.Add(time.Second)
	if _, got := m.Sample(); got != 1000 {
		t.Errorf("sent after the reset = %d, want 1000", got)
	}
}

func TestByteMeterKeepsTheLastValueOnAnError(t *testing.T) {
	now := time.Unix(0, 0)
	var n uint64
	fail := false
	m := netstat.NewByteMeter(func() (uint64, error) {
		if fail {
			return 0, errors.New("gone")
		}
		return n, nil
	}, func() time.Time { return now })
	m.Sample()
	n, now = 5000, now.Add(time.Second)
	r, got := m.Sample()
	fail = true
	now = now.Add(time.Second)
	if r2, got2 := m.Sample(); r2 != r || got2 != got || got2 != 5000 {
		t.Errorf("after a failed read = %v, %d; want %v, %d", r2, got2, r, got)
	}
}
