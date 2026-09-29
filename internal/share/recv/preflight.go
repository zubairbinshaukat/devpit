package recv

import (
	"context"
	"fmt"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
)

// WarningKind names a problem the check found.
type WarningKind string

// The problems the check looks for.
const (
	// WarnNoSpace means the destination does not have room. It blocks.
	WarnNoSpace WarningKind = "no-space"
	// WarnFAT32 means the destination is FAT32 and some file is over 4 GB.
	// It blocks.
	WarnFAT32 WarningKind = "fat32"
	// WarnLongPaths means some destination paths pass 260 characters. It
	// warns, and does not block: robocopy copes, some programs do not.
	WarnLongPaths WarningKind = "long-paths"
	// WarnNothing means there is nothing to copy. It blocks.
	WarnNothing WarningKind = "nothing"
)

// Warning is one problem, in words a person can act on.
type Warning struct {
	// Kind is which problem.
	Kind WarningKind
	// Text says what is wrong.
	Text string
	// Blocks is true when the copy must not start.
	Blocks bool
}

// Check is the answer to "can I copy this here".
type Check struct {
	// Plan is what the dry run found.
	Plan robocopy.Plan
	// FreeBytes is the free space at the destination, 0 when unknown.
	FreeBytes uint64
	// FileSystem is the destination's file system, when known.
	FileSystem string
	// Warnings are the problems, blocking ones first.
	Warnings []Warning
}

// OK reports whether the copy may start: no blocking warning.
func (c Check) OK() bool {
	for _, w := range c.Warnings {
		if w.Blocks {
			return false
		}
	}
	return true
}

// Preflight runs a dry run of copying \\host\share into dest and checks the
// free space, the file system and the path lengths. It changes nothing on
// either PC. The dry run is what gives the exact total, so the progress bar
// later has a real "of 80 GB" to count towards.
func (s *Session) Preflight(ctx context.Context, host, share, dest string) (Check, error) {
	src := netstat.UNC(host, share)
	logPath, err := s.newLogPath("dry")
	if err != nil {
		return Check{}, err
	}
	plan, err := s.deps.Robo.DryRun(ctx, src, dest, logPath, s.deps.Log(logPath))
	if err != nil {
		return Check{}, ExplainPreflight(err)
	}
	c := Check{Plan: plan}
	if s.deps.FreeSpace != nil {
		if free, ferr := s.deps.FreeSpace(dest); ferr == nil {
			c.FreeBytes = free
		}
	}
	if s.deps.FileSystem != nil {
		if fs, ferr := s.deps.FileSystem(dest); ferr == nil {
			c.FileSystem = fs
		}
	}
	c.Warnings = warnings(c)
	return c, nil
}

// warnings turns the numbers into sentences, blocking ones first.
func warnings(c Check) []Warning {
	var out []Warning
	p := c.Plan
	if p.Files == 0 {
		out = append(out, Warning{
			WarnNothing,
			"There is nothing new to copy. Everything is already at the destination, or the shared folder is empty.", true,
		})
	}
	if c.FreeBytes > 0 && uint64(max(p.Bytes, 0)) > c.FreeBytes { //nolint:gosec // Bytes is never negative
		out = append(out, Warning{WarnNoSpace, fmt.Sprintf(
			"This needs %s. The destination has only %s free. Free some space or choose another disk.",
			FormatBytes(p.Bytes), FormatBytes(int64(c.FreeBytes))), true}) //nolint:gosec // free space fits int64
	}
	if strings.EqualFold(c.FileSystem, "FAT32") && p.OverFAT32Count > 0 {
		out = append(out, Warning{WarnFAT32, fmt.Sprintf(
			"The destination is FAT32. %d file(s) are bigger than 4 GB and cannot be stored there. Choose an NTFS or exFAT disk.",
			p.OverFAT32Count), true})
	}
	if p.LongPathCount > 0 {
		out = append(out, Warning{WarnLongPaths, fmt.Sprintf(
			"%d file(s) will have a path longer than 260 characters. They copy, but some programs cannot open them. A shorter destination folder helps.",
			p.LongPathCount), false})
	}
	return out
}

// FormatBytes writes a size the way the screens do: "80.0 GB", "512 MB",
// "12 KB". It uses powers of 1024 and the short names, like Windows.
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	suffix := []string{"KB", "MB", "GB", "TB", "PB"}[exp]
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, suffix)
	}
	return fmt.Sprintf("%.1f %s", v, suffix)
}
