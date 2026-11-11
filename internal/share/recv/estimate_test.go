package recv

import (
	"errors"
	"testing"
	"time"
)

func TestAnEstimateIsCappedBelowWhatIsLeft(t *testing.T) {
	cases := []struct {
		counted   uint64
		remaining int64
		want      int64
	}{
		{500, 1000, 500},
		{5_000_000_000, 1000, 990},                // far above: 99% of what is left
		{1 << 63, 56_700_000_000, 56_133_000_000}, // the live case, any size
		{10, 0, 0},
		{10, -5, 0},
		{10, 1, 0}, // one byte left: the counter alone never finishes it
		{^uint64(0), 200, 198},
	}
	for _, c := range cases {
		got := capEstimate(c.counted, c.remaining)
		if got != c.want {
			t.Errorf("capEstimate(%d, %d) = %d, want %d", c.counted, c.remaining, got, c.want)
		}
		if c.remaining > 0 && got >= c.remaining {
			t.Errorf("capEstimate(%d, %d) = %d reached what is left", c.counted, c.remaining, got)
		}
	}
}

func TestETAIsNeverZeroBeforeTheLastFile(t *testing.T) {
	const mb = 1 << 20
	if got := etaLeft(0, 27*mb, true); got < minETA {
		t.Errorf("no bytes left by the counter but files to go: ETA = %v, want at least %v", got, minETA)
	}
	if got := etaLeft(10, 27*mb, true); got < minETA {
		t.Errorf("a few bytes left at a high speed: ETA = %v, want at least %v", got, minETA)
	}
	if got := etaLeft(270*mb, 27*mb, true); got != 10*time.Second {
		t.Errorf("ETA = %v, want 10s", got)
	}
	if got := etaLeft(0, 27*mb, false); got != 0 {
		t.Errorf("every file done: ETA = %v, want 0", got)
	}
	if got := etaLeft(270*mb, 0, true); got != 0 {
		t.Errorf("speed not known yet: ETA = %v, want 0 (unknown)", got)
	}
}

func TestTheCopyMeterPrefersTheShareAndRebasesBoth(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	var share, adapterIn uint64
	shareErr := errors.New("no instance")
	m := newCopyMeter(func() (uint64, error) {
		if shareErr != nil {
			return 0, shareErr
		}
		return share, nil
	}, func() (uint64, uint64, error) { return adapterIn, 0, nil }, clock)

	m.rebase()
	m.sample()
	now = now.Add(time.Second)
	adapterIn += 9000
	if _, n, est, ok := m.sample(); !ok || !est || n != 9000 {
		t.Fatalf("share unreadable: counted %d estimated %v ok %v, want the adapter's 9000, estimated", n, est, ok)
	}

	shareErr = nil
	m.rebase()
	m.sample() // the baseline of both
	now = now.Add(time.Second)
	share += 100
	adapterIn += 1 << 40
	if sp, n, est, ok := m.sample(); !ok || est || n != 100 || sp != 100 {
		t.Fatalf("share readable: speed %v counted %d estimated %v, want the share's 100 B/s and 100, exact", sp, n, est)
	}

	none := newCopyMeter(nil, nil, clock)
	none.rebase()
	if _, _, est, ok := none.sample(); ok || est {
		t.Errorf("no counters: ok %v estimated %v, want neither", ok, est)
	}
}
