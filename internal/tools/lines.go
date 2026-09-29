package tools

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Line is one piece of a step's output, as [RunStepLines] delivers it.
type Line struct {
	// Text is the line with its terminator removed and already passed
	// through [CleanLine]. It is never empty: a line that cleans down to
	// nothing is not delivered at all.
	Text string
	// Transient is true when the line ended in a bare \r rather than \n or
	// \r\n. Package managers end a line that way when they are about to
	// redraw it in place (winget redraws its progress bar with \r and an
	// erase-line), so a transient line is a progress update to show and
	// then replace, not a log line to keep.
	Transient bool
}

// maxLineBytes is the longest token [splitOutputLines] ever waits for. A
// longer run of bytes with no terminator is handed over in chunks of this
// size instead, so a runaway line (a minified blob, a progress bar that
// never ends its line) can never grow the scanner's buffer past its limit:
// bufio.Scanner stops reading for good on bufio.ErrTooLong, and a reader
// that stops reading leaves the child blocked on a full pipe until the
// timeout kills it.
const maxLineBytes = 256 * 1024

// scanBufferMax is the scanner's buffer ceiling. It only has to be larger
// than [maxLineBytes] plus a two-byte terminator; the headroom is free,
// since the buffer only grows as far as the longest line actually needs.
const scanBufferMax = 1024 * 1024

// splitOutputLines is the bufio.SplitFunc [RunStepLines] reads a child's
// output with. It ends a token at \n, at \r\n, and at a bare \r (one not
// followed by \n), which bufio.ScanLines does not: ScanLines would glue
// every one of winget's in-place progress redraws into a single enormous
// "line" that only ends when the download does.
//
// Every token keeps its terminator ("text\n", "text\r\n", "text\r"), since
// a SplitFunc has no other way to tell its caller which one ended it and
// [lineFromToken] needs to know: a bare \r marks a transient line. A token
// with no terminator at all is either the final unterminated line at EOF or
// a [maxLineBytes] chunk of a runaway line.
//
// A \r that is the very last byte of data might be the first half of a
// \r\n whose \n has not been read yet, so unless the reader is at EOF the
// function asks for more data rather than guessing; guessing wrong would
// turn every ordinary Windows line into a transient one followed by an
// empty one whenever a read happened to end between the two bytes.
func splitOutputLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	i := bytes.IndexAny(data, "\r\n")
	if i >= maxLineBytes || (i < 0 && len(data) >= maxLineBytes) {
		return maxLineBytes, data[:maxLineBytes], nil
	}
	if i >= 0 {
		if data[i] == '\n' {
			return i + 1, data[:i+1], nil
		}
		// data[i] is '\r'. A run of them ending in \n is one line ending
		// (some tools write \r\r\n); only a run that does not end in \n
		// is a progress redraw.
		j := i
		for j < len(data) && data[j] == '\r' {
			j++
		}
		switch {
		case j < len(data) && data[j] == '\n':
			return j + 1, data[:j+1], nil
		case j < len(data):
			return i + 1, data[:i+1], nil
		case !atEOF:
			// The \r run ends the buffer: wait to see whether a \n follows.
			return 0, nil, nil
		default:
			return i + 1, data[:i+1], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// lineFromToken turns one [splitOutputLines] token into a [Line]: it drops
// the terminator, notes whether that terminator was a bare \r, and cleans
// the text. ok is false when nothing is left after cleaning, in which case
// the line is not worth delivering.
func lineFromToken(tok []byte) (line Line, ok bool) {
	s := string(tok)
	switch {
	case strings.HasSuffix(s, "\r\n"):
		s = s[:len(s)-2]
	case strings.HasSuffix(s, "\n"):
		s = s[:len(s)-1]
	case strings.HasSuffix(s, "\r"):
		s = s[:len(s)-1]
		line.Transient = true
	}
	line.Text = CleanLine(s)
	return line, line.Text != ""
}

// isProgressGlyph reports whether r is one of the block characters winget
// (and other Windows installers) draw progress bars with: the full block,
// the light/medium/dark shades, and the left-hand partial blocks
// U+2589–U+258F that make up a bar's fractional last cell.
func isProgressGlyph(r rune) bool {
	switch {
	case r == '█', r == '░', r == '▒', r == '▓':
		return true
	case r >= '▉' && r <= '▏':
		return true
	}
	return false
}

// isSpinnerOnly reports whether s is a single spinner frame, possibly
// padded with spaces: winget draws "-", "\", "|" and "/" in turn, each on
// its own \r-terminated redraw, while it works on something it cannot
// measure. Exactly one frame, not "any number of them": a winget table's
// separator is a whole line of "-" and must survive cleaning.
func isSpinnerOnly(s string) bool {
	frames := 0
	for _, r := range s {
		switch r {
		case '-', '\\', '|', '/':
			frames++
		case ' ', '\t':
		default:
			return false
		}
	}
	return frames == 1
}

// cursorColumnRE matches "move the cursor to column 1", which a redraw uses
// the way it would use a carriage return.
var cursorColumnRE = regexp.MustCompile(`\x1b\[[01]?G`)

// CleanLine turns one raw line of package-manager output into plain text
// fit to show in a log view or parse:
//
//   - if s still holds a \r (a caller that did not split on it), only what
//     follows the last one is kept, which is what a terminal would end up
//     showing after the redraws;
//   - ANSI escape sequences are stripped, and so are any other control
//     characters left behind;
//   - progress-bar block glyphs (█ ▒ ░ ▓ and the partial blocks) are
//     removed, and each run of whitespace and glyphs that contained at
//     least one glyph collapses to a single space, so "██▒▒  38 MB" reads
//     "38 MB" rather than keeping the bar's width as blank padding;
//   - a line that is only a spinner frame ("-", "\", "|", "/") becomes "";
//   - the result is trimmed.
//
// Whitespace that was not next to a stripped glyph is left exactly as it
// was. That matters: winget's tables are aligned with runs of spaces, and a
// caller reassembling a check step's output from cleaned lines must still
// be able to find the columns.
func CleanLine(s string) string {
	// Some consoles redraw a spinner with "cursor to column 0" (CSI 0G or
	// CSI G) rather than \r. Read it as the \r it means before the escape
	// codes are stripped, or the spinner frames end up glued to the front
	// of the real text.
	s = cursorColumnRE.ReplaceAllString(s, "\r")
	s = strings.TrimRight(s, "\r")
	if i := strings.LastIndexByte(s, '\r'); i >= 0 {
		s = s[i+1:]
	}
	s = ansi.Strip(s)

	var b strings.Builder
	b.Grow(len(s))
	// run holds a pending stretch of whitespace; glyph notes whether a
	// progress glyph was stripped inside it.
	var run strings.Builder
	glyph := false
	flush := func() {
		if glyph {
			b.WriteByte(' ')
		} else {
			b.WriteString(run.String())
		}
		run.Reset()
		glyph = false
	}
	for _, r := range s {
		switch {
		case isProgressGlyph(r):
			glyph = true
		case r == ' ' || r == '\t':
			run.WriteRune(r)
		case unicode.IsControl(r):
			// Stray C0/C1 controls (backspace, BEL, NUL) that ansi.Strip
			// leaves in place: invisible on a terminal, noise in a log.
		default:
			flush()
			b.WriteRune(r)
		}
	}
	flush()

	out := strings.TrimSpace(b.String())
	if isSpinnerOnly(out) {
		return ""
	}
	return out
}
