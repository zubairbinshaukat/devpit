package netstat

import "time"

// Counters reads how many bytes an adapter has received and sent since it
// came up. winapi.InterfaceOctets is the real one.
type Counters func() (in, out uint64, err error)

// Meter turns two readings of an adapter's byte counters into a speed. It
// measures the network itself rather than robocopy's file lines, so a single
// 40 GB file that is still copying shows a real speed and a real
// "done so far", instead of nothing until the file ends.
//
// A Meter is used from one goroutine.
type Meter struct {
	read Counters
	now  func() time.Time

	started  bool
	baseIn   uint64
	lastIn   uint64
	lastTime time.Time
	rate     float64
}

// NewMeter starts a meter over read, using now as its clock.
func NewMeter(read Counters, now func() time.Time) *Meter {
	return &Meter{read: read, now: now}
}

// smoothing is the weight of the newest reading in the moving average. A
// quarter keeps the number steady while still following a real change in
// a few seconds.
const smoothing = 0.25

// Sample takes a reading and returns the smoothed speed in bytes per second
// and the bytes received since the first reading. A failed reading, or a
// counter that went backwards because the adapter reset, keeps the last
// value and tries again next time.
func (m *Meter) Sample() (rate float64, received uint64) {
	in, _, err := m.read()
	now := m.now()
	if err != nil {
		return m.rate, m.lastIn - m.baseIn
	}
	if !m.started || in < m.lastIn {
		m.started = true
		m.baseIn, m.lastIn, m.lastTime = in, in, now
		return m.rate, 0
	}
	if dt := now.Sub(m.lastTime).Seconds(); dt > 0 {
		inst := float64(in-m.lastIn) / dt
		if m.rate == 0 {
			m.rate = inst
		} else {
			m.rate += smoothing * (inst - m.rate)
		}
	}
	m.lastIn, m.lastTime = in, now
	return m.rate, in - m.baseIn
}

// ETA is the time left for remaining bytes at rate, or zero when the speed
// is not known yet.
func ETA(remaining int64, rate float64) time.Duration {
	if remaining <= 0 || rate < 1 {
		return 0
	}
	return time.Duration(float64(remaining) / rate * float64(time.Second))
}
