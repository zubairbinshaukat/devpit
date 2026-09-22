package managers

import "strings"

// Winget drives the winget package manager. Installs and updates run as the
// current user; each package prompts for UAC individually unless the
// caller is already elevated.
type Winget struct{}

// Name implements [Manager].
func (Winget) Name() string { return "winget" }

// ListCmd implements [Manager].
func (Winget) ListCmd() []string {
	return []string{"winget", "list", "--accept-source-agreements"}
}

// OutdatedCmd implements [Manager].
func (Winget) OutdatedCmd() []string {
	return []string{"winget", "upgrade", "--accept-source-agreements"}
}

// UpgradeAllCmds implements [Manager]. A single step covers every package;
// --disable-interactivity keeps a package's own installer from popping a
// prompt Devpit can't answer.
func (Winget) UpgradeAllCmds() [][]string {
	return [][]string{
		{"winget", "upgrade", "--all", "--accept-source-agreements", "--disable-interactivity"},
	}
}

// InstallCmd implements [Manager]. id is a winget "Publisher.Id".
func (Winget) InstallCmd(id string) []string {
	return []string{
		"winget", "install", "--id", id, "-e",
		"--accept-source-agreements", "--accept-package-agreements",
	}
}

// NeedsElevation implements [Manager].
func (Winget) NeedsElevation() bool { return false }

// noPackagesPhrases are the messages winget prints instead of a table when
// there is nothing to report, across the list and upgrade commands.
var noPackagesPhrases = []string{
	"no installed package found",
	"no applicable update found",
	"no available upgrade",
}

// ParseList implements [Manager]. winget's output is a fixed-width table:
//
//	Name                 Id                      Version      Source
//	-----------------------------------------------------------------
//	7-Zip                7zip.7zip               23.01        winget
//	Git                  Git.Git                 2.43.0        winget
//
// Column boundaries are found from the header row's word offsets, then
// every following row is sliced at those offsets: winget pads each cell
// with spaces to a fixed width per invocation rather than using a
// delimiter, so a delimiter-based split would break on any value
// containing a space.
func (Winget) ParseList(out string) []Installed {
	rows := parseWingetTable(out, []string{"Name", "Id", "Version"})
	if rows == nil {
		return nil
	}
	result := make([]Installed, 0, len(rows))
	for _, r := range rows {
		result = append(result, Installed{
			Name:    r["Name"],
			ID:      r["Id"],
			Version: r["Version"],
		})
	}
	return result
}

// ParseOutdated implements [Manager]. winget upgrade's table adds an
// "Available" column holding the version an upgrade would install:
//
//	Name            Id              Version      Available    Source
//	------------------------------------------------------------------
//	Git             Git.Git         2.42.0       2.43.0       winget
func (Winget) ParseOutdated(out string) []Outdated {
	rows := parseWingetTable(out, []string{"Name", "Id", "Version", "Available"})
	if rows == nil {
		return nil
	}
	result := make([]Outdated, 0, len(rows))
	for _, r := range rows {
		result = append(result, Outdated{
			Name:    r["Name"],
			ID:      r["Id"],
			Current: r["Version"],
			Latest:  r["Available"],
		})
	}
	return result
}

// parseWingetTable finds the header line containing every column in want,
// in order, tokenizes it to learn every column's name and start offset
// (not only the wanted ones, so a value in an unwanted trailing column
// never bleeds into the last wanted column), slices every following data
// row at those offsets, and returns one map[column]value per row. It stops
// at the first blank line after the table starts, and returns nil if
// winget reported no packages or no header line was found at all.
func parseWingetTable(out string, want []string) []map[string]string {
	lower := strings.ToLower(out)
	for _, phrase := range noPackagesPhrases {
		if strings.Contains(lower, phrase) {
			return nil
		}
	}

	lines := strings.Split(out, "\n")
	headerIdx := -1
	var cols []string
	var offsets []int
	for i, line := range lines {
		names, offs := tokenizeHeader(line)
		if containsInOrder(names, want) {
			headerIdx, cols, offsets = i, names, offs
			break
		}
	}
	if headerIdx == -1 {
		return nil
	}

	var rows []map[string]string
	// headerIdx+1 is the dashed separator line; data starts after that.
	for _, line := range lines[headerIdx+2:] {
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.TrimSpace(trimmed) == "" {
			break
		}
		rows = append(rows, sliceRow(trimmed, cols, offsets))
	}
	return rows
}

// tokenizeHeader splits a header line into whitespace-separated column
// names and each one's byte offset in the line.
func tokenizeHeader(line string) (names []string, offsets []int) {
	n := len(line)
	i := 0
	for i < n {
		for i < n && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		start := i
		for i < n && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		names = append(names, line[start:i])
		offsets = append(offsets, start)
	}
	return names, offsets
}

// containsInOrder reports whether every entry of want appears in cols, in
// the same relative order, as an exact token match (not merely a
// substring, so "Id" never matches inside "Available").
func containsInOrder(cols, want []string) bool {
	j := 0
	for _, c := range cols {
		if j < len(want) && c == want[j] {
			j++
		}
	}
	return j == len(want)
}

// sliceRow cuts line at each known column's offset (using the next
// column's offset, or end of line for the last column) and trims the
// result, returning every column in names, not only a caller's subset.
func sliceRow(line string, names []string, offsets []int) map[string]string {
	row := make(map[string]string, len(names))
	for i, name := range names {
		start := offsets[i]
		if start > len(line) {
			row[name] = ""
			continue
		}
		end := len(line)
		if i+1 < len(offsets) && offsets[i+1] < end {
			end = offsets[i+1]
		}
		row[name] = strings.TrimSpace(line[start:end])
	}
	return row
}
