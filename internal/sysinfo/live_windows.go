//go:build windows

package sysinfo

import (
	"context"
	"sync"
)

// Live reads this PC. The PowerShell run is made at most once per Live,
// by whichever section asks first, and shared by the others.
type Live struct {
	mu    sync.Mutex
	batch func() (batch, error)
}

// NewLive returns a reader of this PC. Make a new one to read again.
func NewLive() Source { return &Live{} }

// theBatch starts (once) and waits for the PowerShell run. The run lives on
// the first caller's context, which is the screen's: when the screen goes,
// the run is killed.
func (l *Live) theBatch(ctx context.Context) (batch, error) {
	l.mu.Lock()
	if l.batch == nil {
		l.batch = sync.OnceValues(func() (batch, error) {
			out, err := runScript(ctx, batchScript, BatchLimit)
			if err != nil {
				return batch{}, err
			}
			return parseBatch(out)
		})
	}
	f := l.batch
	l.mu.Unlock()
	return Bounded(ctx, BatchSectionLimit, func(context.Context) (batch, error) { return f() })
}

// System implements [Source].
func (l *Live) System(context.Context) (System, error) { return systemLive() }

// CPU implements [Source].
func (l *Live) CPU(context.Context) (CPU, error) { return cpuLive() }

// Memory implements [Source].
func (l *Live) Memory(context.Context) (Memory, error) { return memoryLive() }

// Graphics implements [Source].
func (l *Live) Graphics(context.Context) ([]GPU, error) { return graphicsLive() }

// Displays implements [Source].
func (l *Live) Displays(context.Context) ([]Display, error) { return displaysLive() }

// Volumes implements [Source].
func (l *Live) Volumes(context.Context) ([]Volume, error) { return volumesLive() }

// Battery implements [Source].
func (l *Live) Battery(context.Context) (Battery, error) { return batteryLive() }

// Network implements [Source].
func (l *Live) Network(context.Context) ([]Adapter, error) { return networkLive() }

// Devices implements [Source].
func (l *Live) Devices(context.Context) (Devices, error) { return devicesLive() }

// Disks implements [Source]: the drives come from the PowerShell run.
func (l *Live) Disks(ctx context.Context) (DiskInfo, error) {
	b, err := l.theBatch(ctx)
	if err != nil {
		return DiskInfo{Note: "PowerShell did not answer, so the drives and their health are not reported."}, err
	}
	return b.diskInfo(), nil
}

// Windows implements [Source]: native reads, plus activation and the
// hypervisor from the PowerShell run. A failed run leaves those unknown and
// says so in Note; everything else is still there.
func (l *Live) Windows(ctx context.Context) (Windows, error) {
	w := windowsNative()
	b, err := l.theBatch(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return w, ctx.Err()
		}
		w.Note = "PowerShell did not answer, so activation is not reported."
		return w, nil
	}
	w.Activated, w.Channel = b.licence()
	if b.HV != nil {
		w.Hypervisor = TriOf(*b.HV)
	}
	if b.Errors["lic"] != "" {
		w.Note = "Windows did not say whether it is activated."
	}
	return w, nil
}
