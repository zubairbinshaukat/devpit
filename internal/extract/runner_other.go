//go:build !windows

package extract

import "github.com/zubairbinshaukat/devpit/internal/winapi"

// realRunner refuses outside Windows: hatchzip is only driven on Windows,
// and off Windows only the fakes run.
type realRunner struct{}

// Start implements Runner.
func (realRunner) Start(Spec) (Process, error) { return nil, winapi.ErrUnsupported }
