package telemetry

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

// SystemInfo returns "windows" and the running build as "major.minor.build",
// e.g. "10.0.26200".
//
// RtlGetVersion is used rather than GetVersionEx because the latter lies to a
// binary without a compatibility manifest, reporting 6.2 on Windows 10 and 11.
func SystemInfo() (name, version string) {
	v := windows.RtlGetVersion()
	return runtime.GOOS, fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}
