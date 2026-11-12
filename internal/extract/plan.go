package extract

import (
	"regexp"
	"strconv"
	"strings"
)

// The plan parser (section 6.4). hatchzip's plan text is English, unversioned
// and not a stable format, so it is only ever used for display: the verdict
// comes from the exit code alone (rule 84), and every line the parser does not
// recognise is kept in Plan.Unknown and changes nothing.

const sizeRe = `(\d+(?:\.\d+)? [KMGT]?B)`

var (
	planHeader   = regexp.MustCompile(`^(\S.*?)(?: \(\+(\d+) more parts?\))?\s+` + sizeRe + ` (\S+), ` + sizeRe + ` uncompressed in ([\d,]+) entr(?:y|ies)$`)
	planResuming = regexp.MustCompile(`^\s*resuming: ` + sizeRe + ` of ` + sizeRe + ` remaining$`)
	planAvail    = regexp.MustCompile(`^\s*available\s+` + sizeRe + `$`)
	planStandard = regexp.MustCompile(`^\s*standard extraction needs\s+` + sizeRe + `$`)
	planRequired = regexp.MustCompile(`^\s*required\s+` + sizeRe + `(?: \(\+` + sizeRe + ` reserved\))?$`)
	planAfter    = regexp.MustCompile(`^\s*available after\s+` + sizeRe + `$`)
	planNote     = regexp.MustCompile(`^\s*note: (.+)$`)
	planShort    = regexp.MustCompile(`^Insufficient disk space: ` + sizeRe + ` more is required`)
	planAllDone  = regexp.MustCompile(`^All entries are already extracted`)
)

// ParsePlan reads check's stdout. Every line it does not recognise goes to
// Unknown and changes nothing; the verdict never comes from here (rule 84).
func ParsePlan(stdout string) Plan {
	var p Plan
	for _, line := range strings.Split(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m := planHeader.FindStringSubmatch(line); m != nil {
			p.Name = m[1]
			p.MoreParts, _ = strconv.Atoi(m[2])
			p.ArchiveBytes = parseSize(m[3])
			p.Format = m[4]
			p.TotalBytes = parseSize(m[5])
			p.Entries, _ = strconv.Atoi(strings.ReplaceAll(m[6], ",", ""))
		} else if m := planResuming.FindStringSubmatch(line); m != nil {
			p.Resuming = true
			p.LeftBytes = parseSize(m[1])
		} else if m := planStandard.FindStringSubmatch(line); m != nil {
			p.StandardNeeds = parseSize(m[1])
		} else if m := planRequired.FindStringSubmatch(line); m != nil {
			p.Required = parseSize(m[1])
			if m[2] != "" {
				p.Reserved = parseSize(m[2])
			}
		} else if m := planAfter.FindStringSubmatch(line); m != nil {
			p.AvailableAfter = parseSize(m[1])
		} else if m := planAvail.FindStringSubmatch(line); m != nil {
			p.Available = parseSize(m[1])
		} else if m := planNote.FindStringSubmatch(line); m != nil {
			p.Notes = append(p.Notes, m[1])
			if strings.Contains(strings.ToLower(m[1]), "enough space to keep") {
				p.KeepArchiveFits = true
			}
		} else if m := planShort.FindStringSubmatch(line); m != nil {
			p.ShortBy = parseSize(m[1])
		} else if planAllDone.MatchString(line) {
			p.AllDone = true
		} else {
			p.Unknown = append(p.Unknown, line)
		}
	}
	return p
}

// sizeUnits are hatchzip's units: base 1000, as it prints them.
var sizeUnits = map[string]float64{"B": 1, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12}

// parseSize reads hatchzip's "84.2 GB" (base 1000). Anything else is 0.
func parseSize(s string) int64 {
	num, unit, ok := strings.Cut(strings.TrimSpace(s), " ")
	if !ok {
		return 0
	}
	mult, known := sizeUnits[unit]
	if !known {
		return 0
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f < 0 || f > 1e6 {
		return 0
	}
	return int64(f*mult + 0.5)
}
