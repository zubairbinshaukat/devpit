//go:build !windows

package telemetry

import "runtime"

// SystemInfo returns the OS name and an empty version. Only Windows has a
// version string the wire contract asks for; elsewhere the field is sent
// empty rather than filled with something the server cannot read.
func SystemInfo() (name, version string) { return runtime.GOOS, "" }
