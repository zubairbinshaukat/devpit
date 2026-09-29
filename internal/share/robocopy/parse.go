package robocopy

import (
	"regexp"
	"strconv"
	"strings"
)

// LineKind says what a line of robocopy output is.
type LineKind int

// The kinds of line.
const (
	// KindOther is anything the tracker does not need: blank lines, rules,
	// translated messages.
	KindOther LineKind = iota
	// KindFile is a file that robocopy handled: a size, then a full path.
	KindFile
	// KindError is a failed operation: a stamp, a word, an error number and
	// its hex form, then the operation and the path.
	KindError
)

// Line is one parsed line of the log.
type Line struct {
	// Kind is the shape the line has.
	Kind LineKind
	// Size is the file's size in bytes, for KindFile.
	Size int64
	// Path is the file's full path, for KindFile and KindError.
	Path string
	// Code is the Win32 error number, for KindError.
	Code int
}

// absPath matches the start of an absolute path: a drive or a UNC share.
var absPath = regexp.MustCompile(`^(?:[A-Za-z]:[\\/]|\\\\)`)

// errorLine is the shape of an error line, with the word after the time
// being whatever the language says "ERROR" is:
//
//	2026/09/29 10:15:02 ERROR 112 (0x00000070) Copying File D:\dst\a.bin
//	29.09.2026 10:15:02 FEHLER 112 (0x00000070) Kopieren von Datei D:\dst\a.bin
//
// The stamp has a date in either order and a time. The number and its
// eight-digit hex form in brackets are the part that never changes.
var errorLine = regexp.MustCompile(
	`^\s*\d{1,4}[/.\-]\d{1,2}[/.\-]\d{1,4}\s+\d{1,2}:\d{2}:\d{2}\s+\S.*?\s(\d+)\s+\(0x[0-9A-Fa-f]{8}\)\s*(.*)$`)

// drivePathInText finds the start of a path inside a translated sentence.
var drivePathInText = regexp.MustCompile(`(?:[A-Za-z]:\\|\\\\)`)

// ParseLine classifies one line of the log.
//
// A file line is tab-separated. The last column is the path and the one
// before it is the size in bytes (a class word may come before them; with
// /NC it does not, and it is never needed). Requiring the size to be all
// digits and the path to be absolute is what keeps a translated sentence
// that happens to contain a tab from being mistaken for a file.
func ParseLine(line string) Line {
	line = strings.TrimRight(line, "\r\n")
	if m := errorLine.FindStringSubmatch(line); m != nil {
		code, _ := strconv.Atoi(m[1])
		path := ""
		if loc := drivePathInText.FindStringIndex(m[2]); loc != nil {
			path = strings.TrimSpace(m[2][loc[0]:])
		}
		return Line{Kind: KindError, Code: code, Path: path}
	}
	if !strings.Contains(line, "\t") {
		return Line{}
	}
	cols := strings.Split(line, "\t")
	if len(cols) < 2 {
		return Line{}
	}
	path := strings.TrimSpace(cols[len(cols)-1])
	sizeText := strings.TrimSpace(cols[len(cols)-2])
	if !absPath.MatchString(path) || sizeText == "" {
		return Line{}
	}
	size, err := strconv.ParseInt(sizeText, 10, 64)
	if err != nil || size < 0 {
		return Line{}
	}
	return Line{Kind: KindFile, Size: size, Path: path}
}

// Counts is one row of the summary table.
type Counts struct {
	// Total is everything robocopy looked at.
	Total int64
	// Copied is what it copied, or would copy with /L.
	Copied int64
	// Skipped is what was already there.
	Skipped int64
	// Mismatch is what differed in kind, such as a file where a folder is.
	Mismatch int64
	// Failed is what could not be copied.
	Failed int64
	// Extras is what is in the destination but not the source.
	Extras int64
}

// Summary is the table robocopy prints at the end. Its labels are translated
// and its rows are always in the same order: folders, files, bytes.
type Summary struct {
	// Dirs, Files and Bytes are the three rows.
	Dirs, Files, Bytes Counts
}

// summaryRow is a row with a label and exactly six whole numbers. The label
// is anything that starts with a letter or symbol; the colon may be the
// ASCII one or a full-width one, with or without a space before it (French
// puts one).
var summaryRow = regexp.MustCompile(
	`^\s*[^\d\s].*?\s*[:：]\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s*$`)

// ParseSummary reads the last three six-number rows of text. The times row
// has colons inside its numbers and the speed rows have one, so neither
// matches. ok is false when the run did not get as far as a summary, for
// example when robocopy was killed.
func ParseSummary(text string) (Summary, bool) {
	var rows []Counts
	for line := range strings.SplitSeq(text, "\n") {
		m := summaryRow.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		var n [6]int64
		for i := range n {
			n[i], _ = strconv.ParseInt(m[i+1], 10, 64)
		}
		rows = append(rows, Counts{n[0], n[1], n[2], n[3], n[4], n[5]})
	}
	if len(rows) < 3 {
		return Summary{}, false
	}
	last := rows[len(rows)-3:]
	return Summary{Dirs: last[0], Files: last[1], Bytes: last[2]}, true
}

// Plan is what a dry run found: how much a copy would move, and what is
// likely to go wrong on the way.
type Plan struct {
	// Files and Bytes are what would be copied: the Copied column of the
	// summary, so files already at the destination do not count.
	Files, Bytes int64
	// LargestFile is the biggest file listed, in bytes.
	LargestFile int64
	// OverFAT32 holds up to sampleMax files larger than 4 GB minus one byte,
	// which a FAT32 disk cannot store.
	OverFAT32 []string
	// OverFAT32Count is how many such files there are in all.
	OverFAT32Count int
	// LongPaths holds up to sampleMax destination paths of 260 characters or
	// more, and LongPathCount how many there are in all.
	LongPaths     []string
	LongPathCount int
	// DiskBytes is what the listed files take on disk, each rounded up to a
	// whole 4 KB cluster, the NTFS default. A hundred thousand files of 100
	// bytes are 10 MB of data but 400 MB of disk, and a check on the bytes
	// alone let such a copy start and fail near the end.
	DiskBytes int64
	// SummaryFound is false when the dry run ended without a summary, in
	// which case Files and Bytes come from the listing and may include files
	// that are already at the destination.
	SummaryFound bool
}

// FAT32Limit is the largest file a FAT32 disk can hold: 4 GiB minus a byte.
const FAT32Limit = 4<<30 - 1

// MaxPath is Windows' classic path length limit, in characters.
const MaxPath = 260

// ClusterSize is the allocation unit [Plan.DiskBytes] rounds each file up
// to: 4 KB, the NTFS default for volumes up to 16 TB. A disk with bigger
// clusters needs more, so it is a floor, not an exact figure.
const ClusterSize = 4096

// sampleMax caps the example paths kept in a [Plan].
const sampleMax = 5

// ParseDryRun reads the log of a dry run. srcRoot and dstRoot map each listed
// file to the path it would get, to find those that would be too long.
// Lines whose path is not under srcRoot are ignored: they are extras that
// live in the destination.
func ParseDryRun(text, srcRoot, dstRoot string) Plan {
	var p Plan
	src := trimSlash(srcRoot)
	dst := trimSlash(dstRoot)
	var listFiles, listBytes int64
	for line := range strings.SplitSeq(text, "\n") {
		l := ParseLine(line)
		if l.Kind != KindFile {
			continue
		}
		rel, ok := under(src, l.Path)
		if !ok {
			continue
		}
		listFiles++
		listBytes += l.Size
		p.DiskBytes += (l.Size + ClusterSize - 1) / ClusterSize * ClusterSize
		p.LargestFile = max(p.LargestFile, l.Size)
		if l.Size > FAT32Limit {
			p.OverFAT32Count++
			if len(p.OverFAT32) < sampleMax {
				p.OverFAT32 = append(p.OverFAT32, l.Path)
			}
		}
		target := dst + `\` + rel
		if len([]rune(target)) >= MaxPath {
			p.LongPathCount++
			if len(p.LongPaths) < sampleMax {
				p.LongPaths = append(p.LongPaths, target)
			}
		}
	}
	if s, ok := ParseSummary(text); ok {
		p.Files, p.Bytes, p.SummaryFound = s.Files.Copied, s.Bytes.Copied, true
	} else {
		p.Files, p.Bytes = listFiles, listBytes
	}
	return p
}

// under reports whether path is inside root, comparing case-insensitively
// and rune by rune, and returns the part of path below root. Slicing the
// path by the root's byte length would go wrong the moment a letter's upper
// and lower case are different lengths in UTF-8, as in Turkish.
func under(root, path string) (rel string, ok bool) {
	rr, pr := []rune(root), []rune(path)
	if len(pr) <= len(rr)+1 {
		return "", false
	}
	if !strings.EqualFold(string(pr[:len(rr)]), root) {
		return "", false
	}
	if sep := pr[len(rr)]; sep != '\\' && sep != '/' {
		return "", false
	}
	return string(pr[len(rr)+1:]), true
}
