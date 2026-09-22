//go:build !windows

package fonts

import "errors"

// ErrUnsupported is returned by every stub in this file on non-Windows
// platforms. It mirrors internal/winapi's ErrUnsupported so
// `GOOS=linux go vet ./...` passes without pulling in the winapi package.
var ErrUnsupported = errors.New("fonts: not supported on this platform")

type stubRegistry struct{}

func newRegistry() Registry { return stubRegistry{} }

func (stubRegistry) Set(string, string) error         { return ErrUnsupported }
func (stubRegistry) Delete(string) error              { return ErrUnsupported }
func (stubRegistry) Get(string) (string, bool, error) { return "", false, ErrUnsupported }

type stubGDI struct{}

func newGDI() GDI { return stubGDI{} }

func (stubGDI) AddFontResource(string) error    { return ErrUnsupported }
func (stubGDI) RemoveFontResource(string) error { return ErrUnsupported }
func (stubGDI) BroadcastFontChange()            {}
