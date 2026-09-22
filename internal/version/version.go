// Package version holds the build identity of the Devpit binary.
//
// All three variables are overwritten at link time by GoReleaser with
// -ldflags "-X github.com/zubairbinshaukat/devpit/internal/version.Version=..."
// so nothing here performs work at startup.
package version

import "fmt"

// Build information. Values are injected at link time; the defaults below are
// what a plain "go build" produces.
var (
	// Version is the semantic version of this build, without a leading "v".
	Version = "dev"
	// Commit is the git commit this build came from.
	Commit = "none"
	// Date is the RFC3339 build timestamp.
	Date = "unknown"
)

// Short returns just the version string, e.g. "1.2.3" or "dev".
func Short() string { return Version }

// String returns the full build identity, e.g. "1.2.3 (abc1234, 2026-09-22)".
func String() string {
	return fmt.Sprintf("%s (%s, %s)", Version, Commit, Date)
}
