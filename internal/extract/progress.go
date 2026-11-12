package extract

import "time"

// The bar's two promises (rule 84), re-stated from share/recv's capEstimate
// and etaLeft (decision 8): the bar never goes backwards and never reaches
// 100% before hatchzip exits 0, and the time left never reads "0s" while it
// is still running.

// capPercent is how far the bar may go before exit 0.
const capPercent = 99

// capDone is the next DoneBytes: never below prev, never above 99% of total
// (or above done's own value when that is lower).
func capDone(prev, done, total int64) int64 {
	if total > 0 {
		done = min(done, total*capPercent/100)
	}
	return max(prev, max(done, 0))
}

// capItems is the next DoneItems: never below prev, at most total-1 until
// exit 0.
func capItems(prev, items, total int) int {
	if total > 0 {
		items = min(items, total-1)
	}
	return max(prev, max(items, 0))
}

// etaWhileRunning floors a known ETA at one second. Zero stays zero: it means
// the speed is not known yet, which the screen shows as nothing.
func etaWhileRunning(eta time.Duration) time.Duration {
	if eta <= 0 {
		return 0
	}
	return max(eta, time.Second)
}
