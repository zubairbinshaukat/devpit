package sysinfo

import (
	"context"
	"errors"
	"time"
)

// ErrUnsupported is what every read returns on an operating system other
// than Windows.
var ErrUnsupported = errors.New("only Windows can be read")

// ErrTimeout is what [Bounded] returns when a read takes longer than its
// limit.
var ErrTimeout = errors.New("took too long to answer")

// Source reads one section at a time. [NewLive] is the real machine; tests
// pass a fake.
type Source interface {
	System(ctx context.Context) (System, error)
	CPU(ctx context.Context) (CPU, error)
	Memory(ctx context.Context) (Memory, error)
	Graphics(ctx context.Context) ([]GPU, error)
	Displays(ctx context.Context) ([]Display, error)
	Volumes(ctx context.Context) ([]Volume, error)
	Disks(ctx context.Context) (DiskInfo, error)
	Battery(ctx context.Context) (Battery, error)
	Windows(ctx context.Context) (Windows, error)
	Network(ctx context.Context) ([]Adapter, error)
	Devices(ctx context.Context) (Devices, error)
}

// Limits a reader keeps to, so a stuck call can never hold the page.
const (
	// NativeLimit bounds one section read from Windows directly.
	NativeLimit = 3 * time.Second
	// BatchLimit bounds the one PowerShell run, which is killed after it.
	BatchLimit = 20 * time.Second
	// BatchSectionLimit bounds a section that waits for that run.
	BatchSectionLimit = BatchLimit + 2*time.Second
	// ReliabilityLimit bounds the elevated read of the drives' counters.
	ReliabilityLimit = 30 * time.Second
)

// Bounded runs f on its own goroutine and returns its answer, or ErrTimeout
// after d, or the context's error when ctx ends first. A call that never
// returns is left behind (it holds only its own goroutine); the caller is
// never held by it.
func Bounded[T any](ctx context.Context, d time.Duration, f func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	type answer struct {
		v   T
		err error
	}
	ch := make(chan answer, 1)
	go func() {
		v, err := f(ctx)
		ch <- answer{v, err}
	}()
	select {
	case a := <-ch:
		return a.v, a.err
	case <-ctx.Done():
		var zero T
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return zero, ErrTimeout
		}
		return zero, ctx.Err()
	}
}
