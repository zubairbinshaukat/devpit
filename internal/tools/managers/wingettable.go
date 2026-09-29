package managers

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// wingetTable is one fixed-width table read out of winget's output.
type wingetTable struct {
	// columns is how many columns the header had.
	columns int
	// rows holds each data row's cells, positionally: rows[i][0] is the
	// first column (Name), rows[i][1] the second (Id), and so on. Every
	// row has exactly columns cells, trimmed, and a non-empty Id.
	rows [][]string
	// introduced is true when the line just above the header is a
	// sentence ending in a colon. winget introduces its explicit-targeting
	// table that way in every language, and never its main one, which is
	// how that table is still recognised when it is the only one printed.
	introduced bool
}

// wingetMaxColumns is the most columns any winget table Devpit reads has:
// Name, Id, Version, Available, Source. A header that tokenizes into more
// words than this has a column label with a space in it (Korean's
// "사용 가능" for Available), and [mergeSplitHeaderWords] puts it back
// together.
const wingetMaxColumns = 5

// minSeparatorDashes is how long a run of "-" must be to count as the line
// winget draws under a table header. Anything shorter could be a spinner
// frame or a stray "--" in prose.
const minSeparatorDashes = 10

// cursorColumnRE matches the ANSI "cursor to column" sequences (CHA, ESC [
// G, with no column or column 0/1) that some winget builds use instead of a
// \r to go back to the start of the line before redrawing a spinner. They
// are rewritten to \r so the same last-\r rule covers both.
var cursorColumnRE = regexp.MustCompile("\x1b\\[[01]?G")

// normalizeWingetLines splits winget's captured output into lines as they
// would finally appear on a terminal, keeping every space: for each line it
// drops the \r of a \r\n, keeps only what follows the last remaining \r
// (winget draws its spinner, "   - \r   \\ \r", on the same line as
// whatever it prints next), strips ANSI sequences, and trims trailing
// blanks. Unlike [tools.CleanLine] it never collapses or trims leading
// spaces, since those are what the table's columns are made of.
func normalizeWingetLines(out string) []string {
	raw := strings.Split(out, "\n")
	lines := make([]string, len(raw))
	for i, line := range raw {
		line = strings.TrimRight(line, "\r")
		line = cursorColumnRE.ReplaceAllString(line, "\r")
		if j := strings.LastIndexByte(line, '\r'); j >= 0 {
			line = line[j+1:]
		}
		line = ansi.Strip(line)
		lines[i] = strings.TrimRight(line, " \t")
	}
	return lines
}

// isSeparatorLine reports whether line is the dashed rule winget draws
// between a table's header and its rows: nothing but at least
// [minSeparatorDashes] "-".
func isSeparatorLine(line string) bool {
	t := strings.TrimSpace(line)
	return len(t) >= minSeparatorDashes && strings.Trim(t, "-") == ""
}

// isSentenceLine reports whether line reads as prose rather than a table
// row: it ends in a full stop or colon, in ASCII or full-width form.
// winget's footers ("17 upgrades available.", "1 package(s) have version
// numbers that cannot be determined. ...") and the explicit-targeting
// table's introduction all do, in every language, while a row always ends
// in a Source or version.
func isSentenceLine(line string) bool {
	t := strings.TrimSpace(line)
	for _, end := range []string{".", ":", "。", "：", "!"} {
		if strings.HasSuffix(t, end) {
			return true
		}
	}
	return false
}

// parseWingetTables finds every fixed-width table in winget's output,
// without relying on the header's wording, so it reads a table the same on
// any display language:
//
//   - a table starts at a separator line ([isSeparatorLine]); the line
//     directly above it is the header;
//   - each column starts where a header word starts, measured in display
//     cells rather than bytes or runes, because a CJK header's words are
//     two cells wide each and winget pads by cells (see [headerStarts]);
//   - every data row is cut at those same cell offsets ([sliceWingetRow]);
//   - the table ends at a blank line, at a sentence ([isSentenceLine]), at
//     another separator, or at a row with no Id (a footer too short to
//     reach the Id column, "17 upgrades available").
//
// Scanning then carries on, so a second table (winget upgrade's
// explicit-targeting one) comes back as a second entry, in output order.
func parseWingetTables(out string) []wingetTable {
	lines := normalizeWingetLines(out)
	var tables []wingetTable
	for i := 1; i < len(lines); i++ {
		if !isSeparatorLine(lines[i]) {
			continue
		}
		header := lines[i-1]
		if strings.TrimSpace(header) == "" || isSeparatorLine(header) {
			continue
		}

		// Gather the table's candidate rows first: the header's word
		// starts may need checking against them before any row is cut.
		var raw []string
		j := i + 1
		for ; j < len(lines); j++ {
			line := lines[j]
			if strings.TrimSpace(line) == "" || isSentenceLine(line) || isSeparatorLine(line) {
				break
			}
			raw = append(raw, line)
		}

		starts := headerStarts(header)
		if len(starts) > wingetMaxColumns {
			starts = mergeSplitHeaderWords(starts, raw)
		}
		if len(starts) < 2 {
			i = j - 1
			continue
		}

		t := wingetTable{columns: len(starts)}
		if i >= 2 {
			intro := strings.TrimSpace(lines[i-2])
			t.introduced = strings.HasSuffix(intro, ":") || strings.HasSuffix(intro, "：")
		}
		for _, line := range raw {
			cells, ok := sliceWingetRow(line, starts)
			if !ok {
				break
			}
			t.rows = append(t.rows, cells)
		}
		tables = append(tables, t)
		i = j - 1
	}
	return tables
}

// headerStarts returns the display-cell column at which each
// space-separated word of header starts.
func headerStarts(header string) []int {
	var starts []int
	col := 0
	prevSpace := true
	for _, r := range header {
		space := r == ' ' || r == '\t'
		if !space && prevSpace {
			starts = append(starts, col)
		}
		prevSpace = space
		col += runeCells(r)
	}
	return starts
}

// mergeSplitHeaderWords drops the header word starts that are really the
// second word of a two-word column label rather than a column of their
// own. A real column start has a blank cell just before it on the data
// rows (winget pads every cell with at least one space); a word inside a
// label does not, since the values in that column run straight across it.
// A start is dropped when most of the rows that reach it have text in the
// cell before it; rows overflowing their columns are outvoted this way
// rather than trusted.
func mergeSplitHeaderWords(starts []int, rows []string) []int {
	occ := make([][]bool, len(rows))
	for i, r := range rows {
		occ[i] = occupiedCells(r)
	}
	kept := []int{starts[0]}
	for _, s := range starts[1:] {
		reach, inside := 0, 0
		for _, o := range occ {
			if s-1 < len(o) {
				reach++
				if o[s-1] {
					inside++
				}
			}
		}
		if reach > 0 && inside*2 > reach {
			continue
		}
		kept = append(kept, s)
	}
	return kept
}

// sliceWingetRow cuts one data row into len(starts) trimmed cells at the
// header's cell offsets. ok is false when the row has no Id (the second
// cell), which means it is not a package row at all.
//
// winget sizes its columns from the rows it has seen so far, so a later
// value can be wider than its column and push everything after it right.
// Such a row is spotted by a column start that falls in the middle of a
// word; it is then read from the right instead, since Id, Version,
// Available and Source never contain spaces: the last len(starts)-1 words
// are those columns and whatever comes before them is the Name.
func sliceWingetRow(line string, starts []int) (cells []string, ok bool) {
	cells = make([]string, len(starts))
	if misaligned(line, starts) {
		fields := strings.Fields(line)
		tail := len(starts) - 1
		if len(fields) < len(starts) {
			return nil, false
		}
		head := len(fields) - tail
		cells[0] = strings.Join(fields[:head], " ")
		copy(cells[1:], fields[head:])
	} else {
		for i, s := range starts {
			end := -1
			if i+1 < len(starts) {
				end = starts[i+1]
			}
			cells[i] = strings.TrimSpace(cutCells(line, s, end))
		}
	}
	if len(cells) < 2 || cells[1] == "" {
		return nil, false
	}
	return cells, true
}

// misaligned reports whether any column start after the first lands in the
// middle of a word of line, i.e. the cells on both sides of the boundary
// hold text.
func misaligned(line string, starts []int) bool {
	occ := occupiedCells(line)
	for _, s := range starts[1:] {
		if s > 0 && s < len(occ) && occ[s-1] && occ[s] {
			return true
		}
	}
	return false
}

// occupiedCells returns, for every display cell line covers, whether a
// non-space character sits in it. A double-width character marks both of
// its cells.
func occupiedCells(line string) []bool {
	var occ []bool
	for _, r := range line {
		filled := r != ' ' && r != '\t'
		for range runeCells(r) {
			occ = append(occ, filled)
		}
	}
	return occ
}

// cutCells returns the part of s whose characters start at display-cell
// columns from (inclusive) to to (exclusive); a negative to means the end
// of the line. A double-width character belongs to the column its first
// cell falls in. This is what lets a row be cut at a CJK header's offsets:
// byte or rune offsets would drift by one for every wide character to the
// left of the cut.
func cutCells(s string, from, to int) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		if to >= 0 && col >= to {
			break
		}
		if col >= from {
			b.WriteRune(r)
		}
		col += runeCells(r)
	}
	return b.String()
}

// runeCells is how many terminal cells r takes up: 2 for East Asian wide
// and full-width characters, 0 for combining marks, 1 otherwise. Width is
// measured per rune, not per grapheme cluster, which is also how winget
// pads its columns.
func runeCells(r rune) int {
	return ansi.StringWidthWc(string(r))
}
