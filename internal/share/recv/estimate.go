package recv

import (
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
)

// estimateCapPercent is how far a byte counter alone may move the bar: at
// most this share of the bytes a run has left. Robocopy run with /MT logs a
// file only once it is done, so between file lines the bar follows a counter;
// a counter can run ahead of the files on disk (the adapter's counts other
// traffic too, and the share's counts reads robocopy repeats), so only
// robocopy's own lines and result take the bar to the total.
const estimateCapPercent = 99

// minETA is the shortest time left shown while robocopy has files to go. A
// copy whose counter is near its cap would otherwise say "0s left" for as
// long as the last big file takes.
const minETA = time.Second

// capEstimate limits counted, the bytes a counter saw in this run, to
// [estimateCapPercent] of remaining, the bytes the run had left when it
// started.
func capEstimate(counted uint64, remaining int64) int64 {
	if remaining <= 0 {
		return 0
	}
	limit := remaining/100*estimateCapPercent + remaining%100*estimateCapPercent/100
	if counted < uint64(limit) { //nolint:gosec // limit is positive here
		return int64(counted) //nolint:gosec // counted is below limit, an int64
	}
	return limit
}

// etaLeft is the time left for remaining bytes at speed. It is zero when the
// speed is not known yet, and never zero while robocopy still has files to
// report (filesLeft), whatever the bytes say.
func etaLeft(remaining int64, speed float64, filesLeft bool) time.Duration {
	if speed < 1 {
		return 0
	}
	eta := netstat.ETA(remaining, speed)
	if filesLeft {
		eta = max(eta, minETA)
	}
	return eta
}

// copyMeter measures a copy's bytes and speed. It prefers the share's own
// counter, which counts only this share, and falls back to the adapter's,
// which counts everything the adapter receives. It is used from one goroutine.
type copyMeter struct {
	share *netstat.Meter
	// shareRead is true once the share counter has read since the last
	// rebase; until then the adapter is used.
	shareRead bool
	adapter   *netstat.Meter
}

// newCopyMeter returns a meter over the share counter shareRead and the
// adapter counters; either may be nil.
func newCopyMeter(shareRead func() (uint64, error), adapter netstat.Counters, now func() time.Time) *copyMeter {
	c := &copyMeter{}
	if shareRead != nil {
		c.share = netstat.NewByteMeter(func() (uint64, error) {
			v, err := shareRead()
			if err == nil {
				c.shareRead = true
			}
			return v, err
		}, now)
	}
	if adapter != nil {
		c.adapter = netstat.NewMeter(adapter, now)
	}
	return c
}

// rebase makes the next sample the starting point of "counted" for both
// counters, keeping their smoothed speeds.
func (c *copyMeter) rebase() {
	c.shareRead = false
	if c.share != nil {
		c.share.Rebase()
	}
	if c.adapter != nil {
		c.adapter.Rebase()
	}
}

// sample reads both counters and returns the speed and the bytes counted since
// the last rebase, from the share counter when it reads and from the adapter
// otherwise. estimated is true when they come from the adapter. ok is false
// when there is no counter at all.
func (c *copyMeter) sample() (speed float64, counted uint64, estimated, ok bool) {
	var sr, ar float64
	var sc, ac uint64
	if c.share != nil {
		sr, sc = c.share.Sample()
	}
	if c.adapter != nil {
		ar, ac = c.adapter.Sample()
	}
	switch {
	case c.shareRead:
		return sr, sc, false, true
	case c.adapter != nil:
		return ar, ac, true, true
	}
	return 0, 0, false, false
}
