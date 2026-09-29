package host

import (
	"context"
	"errors"
	"sync"
	"time"
)

// registry holds every manager that may have a share up, so the program can
// stop them all on its way out, whichever way it leaves.
var registry struct {
	mu   sync.Mutex
	list []*Manager
}

// register remembers m.
func register(m *Manager) {
	registry.mu.Lock()
	registry.list = append(registry.list, m)
	registry.mu.Unlock()
}

// unregisterAll forgets every manager; tests use it to start clean.
func unregisterAll() {
	registry.mu.Lock()
	registry.list = nil
	registry.mu.Unlock()
}

// shutdownTimeout bounds how long leaving Devpit waits for a share to come
// down. The worker cleans up on its own when the pipe closes, so a stuck
// step here does not leave anything behind.
const shutdownTimeout = 30 * time.Second

// ShutdownAll stops every running share. The program calls it once after the
// screen loop has ended, which covers quitting from the UI, Ctrl+C and a
// termination signal alike, since the loop ends in all three. It returns the
// errors joined, and never panics if none is running.
func ShutdownAll() error {
	registry.mu.Lock()
	list := append([]*Manager(nil), registry.list...)
	registry.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	var errs []error
	for _, m := range list {
		if err := m.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
