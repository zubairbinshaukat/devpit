//go:build !windows

package sysinfo

import "context"

// Live is the reader of this PC; off Windows every section is unsupported.
type Live struct{}

// NewLive returns a reader that answers [ErrUnsupported].
func NewLive() Source { return Live{} }

// System implements [Source].
func (Live) System(context.Context) (System, error) { return System{}, ErrUnsupported }

// CPU implements [Source].
func (Live) CPU(context.Context) (CPU, error) { return CPU{}, ErrUnsupported }

// Memory implements [Source].
func (Live) Memory(context.Context) (Memory, error) { return Memory{}, ErrUnsupported }

// Graphics implements [Source].
func (Live) Graphics(context.Context) ([]GPU, error) { return nil, ErrUnsupported }

// Displays implements [Source].
func (Live) Displays(context.Context) ([]Display, error) { return nil, ErrUnsupported }

// Volumes implements [Source].
func (Live) Volumes(context.Context) ([]Volume, error) { return nil, ErrUnsupported }

// Disks implements [Source].
func (Live) Disks(context.Context) (DiskInfo, error) { return DiskInfo{}, ErrUnsupported }

// Battery implements [Source].
func (Live) Battery(context.Context) (Battery, error) { return Battery{}, ErrUnsupported }

// Windows implements [Source].
func (Live) Windows(context.Context) (Windows, error) { return Windows{}, ErrUnsupported }

// Network implements [Source].
func (Live) Network(context.Context) ([]Adapter, error) { return nil, ErrUnsupported }

// Devices implements [Source].
func (Live) Devices(context.Context) (Devices, error) { return Devices{}, ErrUnsupported }

// ReadReliability is unsupported off Windows.
func ReadReliability(context.Context) ([]Reliability, error) { return nil, ErrUnsupported }
