//go:build !windows

package host

import (
	"context"
	"errors"
	"time"
)

// runHotspotProbe has no hotspot to read outside Windows.
func runHotspotProbe(context.Context, string, time.Duration) (string, error) {
	return "", errors.New("hotspot mode needs Windows")
}
