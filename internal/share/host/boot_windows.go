package host

import (
	"time"

	"golang.org/x/sys/windows"
)

// bootTime is when Windows last started, from the tick count. After a
// shutdown with fast startup the count may carry on, which makes the start
// look earlier than it was: a manifest is then treated as written after it,
// the safe side of [Manifest.cleanupRequest].
func bootTime() time.Time { return time.Now().Add(-windows.DurationSinceBoot()) }
