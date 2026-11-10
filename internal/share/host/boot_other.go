//go:build !windows

package host

import "time"

// bootTime is unknown outside Windows: the zero time.
func bootTime() time.Time { return time.Time{} }
