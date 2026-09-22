package network

import (
	"context"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// PingResult summarizes a completed ping run. Raw holds every line of the
// underlying command's output, in order, for display or troubleshooting.
type PingResult struct {
	Sent, Received      int
	MinMs, AvgMs, MaxMs float64
	Raw                 []string
}

var (
	packetsRe = regexp.MustCompile(`Packets:\s*Sent\s*=\s*(\d+),\s*Received\s*=\s*(\d+)`)
	timesRe   = regexp.MustCompile(`Minimum\s*=\s*(\d+(?:\.\d+)?)ms,\s*Maximum\s*=\s*(\d+(?:\.\d+)?)ms,\s*Average\s*=\s*(\d+(?:\.\d+)?)ms`)
)

// Ping runs the platform ping tool against host, streaming each output
// line to onLine as it arrives (onLine may be nil), and returns a parsed
// summary. count is the number of echo requests to send; it defaults to 4
// when zero or negative.
func Ping(ctx context.Context, host string, count int, onLine func(string), opts Options) (PingResult, error) {
	if count <= 0 {
		count = 4
	}

	countFlag := "-c"
	if runtime.GOOS == "windows" {
		countFlag = "-n"
	}

	out, runErr := opts.runner().Run(ctx, onLine, "ping", countFlag, strconv.Itoa(count), host)

	lines := splitLines(out)
	result := parsePingSummary(lines)
	result.Raw = lines

	if runErr != nil && result.Sent == 0 && result.Received == 0 {
		// No parseable summary at all: this is a real failure (bad host,
		// missing binary, killed process), not just packet loss.
		return result, runErr
	}
	return result, nil
}

// parsePingSummary is a pure function over ping's textual output, so it can
// be unit-tested against fixed sample text without running any process. It
// understands the Windows `ping` summary format, including the all-loss
// case where the "Approximate round trip times" block is absent.
func parsePingSummary(lines []string) PingResult {
	var result PingResult
	for _, line := range lines {
		if m := packetsRe.FindStringSubmatch(line); m != nil {
			result.Sent, _ = strconv.Atoi(m[1])
			result.Received, _ = strconv.Atoi(m[2])
			continue
		}
		if m := timesRe.FindStringSubmatch(line); m != nil {
			result.MinMs, _ = strconv.ParseFloat(m[1], 64)
			result.MaxMs, _ = strconv.ParseFloat(m[2], 64)
			result.AvgMs, _ = strconv.ParseFloat(m[3], 64)
		}
	}
	return result
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
