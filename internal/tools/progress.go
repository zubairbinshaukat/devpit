package tools

import (
	"regexp"
	"strconv"
	"strings"
)

// Progress is what one output line says about how far a step has got. Any
// field can be missing: a download bar gives a percentage and sizes but no
// package, winget's "(3/15) Found ..." line gives a package and a counter
// but no percentage.
type Progress struct {
	// Percent is how far through the current download or install the line
	// says the step is, 0..100, or -1 when the line gives no percentage
	// and no pair of sizes to work one out from.
	Percent float64
	// Done and Total are the transferred and total sizes as the line
	// wrote them, number and unit ("38.1 MB", "90.2 MB"); empty when the
	// line has no "done / total" pair.
	Done, Total string
	// Index and Count are winget's "(3/15)" counter during an upgrade of
	// several packages; both 0 when absent.
	Index, Count int
	// Name and ID are the package winget announced on a line like
	// "(3/15) Found Docker Desktop [Docker.DockerDesktop] Version 4.91.0";
	// empty when absent.
	Name, ID string
}

// percentRE matches an explicit percentage, "7%", "42.5 %".
var percentRE = regexp.MustCompile(`(\d+(?:[.,]\d+)?)\s*%`)

// sizeUnits is the unit alternation shared by [sizePairRE]; longer units
// come first so "MiB" is not read as "M" followed by junk.
const sizeUnits = `(KiB|MiB|GiB|TiB|KB|kB|MB|GB|TB|B)`

// sizePairRE matches a "done / total" pair of sizes, "12.5 MB / 105 MB",
// each with its own unit since a download can cross one (999 KB / 1.2 MB).
var sizePairRE = regexp.MustCompile(`(\d+(?:[.,]\d+)?)\s*` + sizeUnits + `\s*/\s*(\d+(?:[.,]\d+)?)\s*` + sizeUnits + `\b`)

// counterRE matches winget's "(3/15)" counter at the start of a line.
var counterRE = regexp.MustCompile(`^\((\d+)/(\d+)\)`)

// foundRE matches winget's "(3/15) Found <Name> [<Id>]" announcement,
// optionally followed by " Version <v>". Only the English "Found" is
// recognised for the name and id; [counterRE] still picks the counter out
// of a localized line.
var foundRE = regexp.MustCompile(`^\(\d+/\d+\)\s+Found\s+(.+?)\s+\[([^\]\s]+)\](?:\s+Version\s+\S+)?`)

// unitBytes is how many bytes one of [sizeUnits] stands for. winget and
// most Windows tools print decimal-looking "MB" while meaning 1024², but
// all that matters here is that both sides of a pair use the same scale.
var unitBytes = map[string]float64{
	"B":   1,
	"KB":  1 << 10,
	"kB":  1 << 10,
	"KiB": 1 << 10,
	"MB":  1 << 20,
	"MiB": 1 << 20,
	"GB":  1 << 30,
	"GiB": 1 << 30,
	"TB":  1 << 40,
	"TiB": 1 << 40,
}

// ParseProgress reads what a line of step output says about progress. It
// accepts raw or already-cleaned text (it runs [CleanLine] itself) and
// recognises:
//
//   - an explicit percentage, "42%" or "42.5%", clamped to 0..100;
//   - a "done / total" pair of sizes, "12.5 MB / 105 MB" (B, KB, MB, GB,
//     TB and their KiB/MiB/GiB/TiB forms), from which Percent is worked
//     out when the line gives no explicit percentage;
//   - winget's "(i/n)" counter, and the "(i/n) Found <Name> [<Id>]" line
//     it prints as it starts on each package.
//
// ok is false when the line carries none of these, so a caller can keep
// showing the last progress it had rather than blanking it on every log
// line.
func ParseProgress(line string) (p Progress, ok bool) {
	s := CleanLine(line)
	p.Percent = -1

	if m := percentRE.FindStringSubmatch(s); m != nil {
		if v, err := parseNumber(m[1]); err == nil {
			p.Percent = clampPercent(v)
			ok = true
		}
	}

	if m := sizePairRE.FindStringSubmatch(s); m != nil {
		p.Done = m[1] + " " + m[2]
		p.Total = m[3] + " " + m[4]
		ok = true
		if p.Percent < 0 {
			done, derr := parseNumber(m[1])
			total, terr := parseNumber(m[3])
			if derr == nil && terr == nil {
				totalBytes := total * unitBytes[m[4]]
				if totalBytes > 0 {
					p.Percent = clampPercent(done * unitBytes[m[2]] / totalBytes * 100)
				}
			}
		}
	}

	if m := counterRE.FindStringSubmatch(s); m != nil {
		i, ierr := strconv.Atoi(m[1])
		n, nerr := strconv.Atoi(m[2])
		if ierr == nil && nerr == nil && n > 0 {
			p.Index, p.Count = i, n
			ok = true
		}
	}

	if m := foundRE.FindStringSubmatch(s); m != nil {
		p.Name, p.ID = m[1], m[2]
		ok = true
	}

	return p, ok
}

// parseNumber parses a decimal that may use a comma as its separator, as
// winget does under locales like de-DE ("12,5 MB").
func parseNumber(s string) (float64, error) {
	return strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
}

// clampPercent limits v to 0..100: a tool that reports "105%" for a
// download that grew, or a sizes pair where done briefly overshoots total,
// should still fill a bar exactly, not overflow it.
func clampPercent(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	}
	return v
}
