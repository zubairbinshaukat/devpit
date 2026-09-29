package tools

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

// oneByteReader hands its data over one byte per Read, so every possible
// buffer boundary gets exercised — in particular the one between the \r
// and the \n of a \r\n.
type oneByteReader struct{ s string }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.s == "" {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.s[0]
	r.s = r.s[1:]
	return 1, nil
}

// scanTokens runs splitOutputLines over r and returns every raw token.
func scanTokens(t *testing.T, r io.Reader) []string {
	t.Helper()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), scanBufferMax)
	sc.Split(splitOutputLines)
	var toks []string
	for sc.Scan() {
		toks = append(toks, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	return toks
}

func TestSplitOutputLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"lf", "a\nb\n", []string{"a\n", "b\n"}},
		{"crlf", "a\r\nb\r\n", []string{"a\r\n", "b\r\n"}},
		{"bare cr", "10%\r20%\r30%\n", []string{"10%\r", "20%\r", "30%\n"}},
		{"cr then crlf", "bar\rdone\r\n", []string{"bar\r", "done\r\n"}},
		{"trailing cr at eof", "a\nlast\r", []string{"a\n", "last\r"}},
		{"unterminated at eof", "a\nlast", []string{"a\n", "last"}},
		{"empty lines kept as tokens", "\n\r\n\r", []string{"\n", "\r\n", "\r"}},
		{"empty input", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, mode := range []string{"whole", "one byte"} {
				var r io.Reader = strings.NewReader(tt.in)
				if mode == "one byte" {
					r = &oneByteReader{s: tt.in}
				}
				got := scanTokens(t, r)
				if strings.Join(got, "|") != strings.Join(tt.want, "|") || len(got) != len(tt.want) {
					t.Errorf("%s: tokens = %q, want %q", mode, got, tt.want)
				}
			}
		})
	}
}

func TestSplitOutputLinesCRLFAcrossBoundary(t *testing.T) {
	// Directly: a \r ending the buffer must not be taken as a bare \r
	// until the reader is at EOF.
	adv, tok, err := splitOutputLines([]byte("line\r"), false)
	if adv != 0 || tok != nil || err != nil {
		t.Fatalf("split(\"line\\r\", false) = %d, %q, %v; want a request for more data", adv, tok, err)
	}
	adv, tok, _ = splitOutputLines([]byte("line\r"), true)
	if adv != 5 || string(tok) != "line\r" {
		t.Fatalf("split(\"line\\r\", true) = %d, %q; want the transient token", adv, tok)
	}

	// End to end, one byte at a time: no transient lines may appear.
	sc := bufio.NewScanner(&oneByteReader{s: "one\r\ntwo\r\n"})
	sc.Split(splitOutputLines)
	for sc.Scan() {
		line, ok := lineFromToken(sc.Bytes())
		if ok && line.Transient {
			t.Errorf("line %q marked transient; a split \\r\\n is not a bare \\r", line.Text)
		}
	}
}

func TestSplitOutputLinesLongLines(t *testing.T) {
	// Longer than bufio.Scanner's default 64 KiB token, under maxLineBytes:
	// delivered whole.
	long := strings.Repeat("x", 200*1024)
	got := scanTokens(t, strings.NewReader(long+"\nend\n"))
	if len(got) != 2 || got[0] != long+"\n" || got[1] != "end\n" {
		t.Fatalf("got %d tokens (first len %d), want the long line whole then \"end\"", len(got), len(got[0]))
	}

	// Far longer than the scanner's buffer ceiling, with no terminator:
	// chunked rather than failing with bufio.ErrTooLong, and nothing lost.
	huge := strings.Repeat("y", 3*scanBufferMax)
	got = scanTokens(t, strings.NewReader(huge+"\ntail\n"))
	if last := got[len(got)-1]; last != "tail\n" {
		t.Fatalf("last token = %q, want %q", last, "tail\n")
	}
	total := 0
	for _, tok := range got[:len(got)-1] {
		if len(tok) > maxLineBytes+2 {
			t.Errorf("token of %d bytes exceeds maxLineBytes", len(tok))
		}
		total += len(tok)
	}
	if total != len(huge)+1 {
		t.Errorf("chunks hold %d bytes, want %d", total, len(huge)+1)
	}
}

func TestLineFromToken(t *testing.T) {
	tests := []struct {
		tok       string
		want      string
		transient bool
		ok        bool
	}{
		{"hello\n", "hello", false, true},
		{"hello\r\n", "hello", false, true},
		{"  ██████▒▒▒▒  38.1 MB / 90.2 MB\r", "38.1 MB / 90.2 MB", true, true},
		{"   - \r", "", true, false},
		{"\r\n", "", false, false},
		{"tail", "tail", false, true},
	}
	for _, tt := range tests {
		line, ok := lineFromToken([]byte(tt.tok))
		if ok != tt.ok || line.Text != tt.want || (ok && line.Transient != tt.transient) {
			t.Errorf("lineFromToken(%q) = %+v, %v; want {%q %v}, %v", tt.tok, line, ok, tt.want, tt.transient, tt.ok)
		}
	}
}

func TestCleanLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Successfully installed", "Successfully installed"},
		{"trims", "   padded  \t", "padded"},
		{"ansi", "\x1b[32mgreen\x1b[0m text\x1b[K", "green text"},
		{"full bar", "  ██████████████████████████████  90.2 MB / 90.2 MB", "90.2 MB / 90.2 MB"},
		{"partial bar", "  ██████████▌▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒  38.1 MB / 90.2 MB", "38.1 MB / 90.2 MB"},
		{"shades", "░░▓▓ 50%", "50%"},
		{"glyph between words", "a█b", "a b"},
		{"spinner dash", "   - ", ""},
		{"spinner backslash", `  \`, ""},
		{"spinner pipe", "|", ""},
		{"spinner slash", " / ", ""},
		{"separator survives", "----------", "----------"},
		{"table spacing kept", "Git             Git.Git         2.42.0", "Git             Git.Git         2.42.0"},
		{"after last cr", "   - \r   \\ \rName  Id", "Name  Id"},
		{"trailing cr ignored", "done\r", "done"},
		{"controls", "bell\a and\x00 nul", "bell and nul"},
		{"only glyphs", "▒▒▒▒▒▒▒▒", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanLine(tt.in); got != tt.want {
				t.Errorf("CleanLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// A spinner redrawn with "cursor to column 1" instead of \r must not end up
// glued to the front of the table header that follows it.
func TestCleanLineTreatsCursorColumnAsCarriageReturn(t *testing.T) {
	got := CleanLine("\x1b[0G   - \x1b[0G   \\ \x1b[0GName           Id")
	if got != "Name           Id" {
		t.Errorf("CleanLine = %q, want the header alone", got)
	}
	if got := CleanLine("\x1b[G  | \x1b[GDone"); got != "Done" {
		t.Errorf("CleanLine(CSI G) = %q, want Done", got)
	}
}

// A run of \r ending in \n is one ordinary line ending, not a redraw.
func TestSplitTreatsCRRunBeforeLFAsLineEnd(t *testing.T) {
	sc := bufio.NewScanner(strings.NewReader("one\r\r\ntwo\r\r\r\nthree\rfour\n"))
	sc.Split(splitOutputLines)
	var got []string
	var transient []bool
	for sc.Scan() {
		l, ok := lineFromToken(sc.Bytes())
		if ok {
			got = append(got, l.Text)
			transient = append(transient, l.Transient)
		}
	}
	want := []string{"one", "two", "three", "four"}
	wantT := []bool{false, false, true, false}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("lines = %q, want %q", got, want)
	}
	for i := range wantT {
		if transient[i] != wantT[i] {
			t.Errorf("line %q transient = %v, want %v", got[i], transient[i], wantT[i])
		}
	}
}
