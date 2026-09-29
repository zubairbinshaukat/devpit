package robocopy_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
)

// locales are the languages the constructed fixtures cover.
var locales = []string{"en", "de", "fr", "ja", "tr"}

// fixture reads a testdata file.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseLineFileLines(t *testing.T) {
	tests := []struct {
		name string
		line string
		want robocopy.Line
	}{
		{
			"no class column", "\t\t   1048576\t\\\\192.168.1.5\\Games\\a.bin",
			robocopy.Line{Kind: robocopy.KindFile, Size: 1048576, Path: `\\192.168.1.5\Games\a.bin`},
		},
		{
			"with a class word", "\t  New File  \t\t  1234\tD:\\src\\a.txt",
			robocopy.Line{Kind: robocopy.KindFile, Size: 1234, Path: `D:\src\a.txt`},
		},
		{"crlf", "\t\t 5\t\\\\h\\s\\a\r\n", robocopy.Line{Kind: robocopy.KindFile, Size: 5, Path: `\\h\s\a`}},
		{"unicode path", "\t\t 9\t\\\\h\\s\\写真\\ü.txt", robocopy.Line{Kind: robocopy.KindFile, Size: 9, Path: `\\h\s\写真\ü.txt`}},
		{"a sentence with a tab is not a file", "Some sentence\twith a tab", robocopy.Line{}},
		{"size is not a number", "\t\t 1.2 g\t\\\\h\\s\\a", robocopy.Line{}},
		{"path is not absolute", "\t\t 12\trelative\\a", robocopy.Line{}},
		{"blank", "", robocopy.Line{}},
		{"rule", strings.Repeat("-", 78), robocopy.Line{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := robocopy.ParseLine(tt.line); got.Kind != tt.want.Kind || got.Size != tt.want.Size || got.Path != tt.want.Path {
				t.Errorf("ParseLine = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseLineErrorLinesAreRecognisedInEveryLanguage(t *testing.T) {
	for _, loc := range locales {
		t.Run(loc, func(t *testing.T) {
			var found robocopy.Line
			for l := range strings.SplitSeq(fixture(t, "copy_"+loc+".log"), "\n") {
				if pl := robocopy.ParseLine(l); pl.Kind == robocopy.KindError {
					found = pl
				}
			}
			if found.Code != 112 {
				t.Fatalf("error code = %d, want 112", found.Code)
			}
			if !strings.HasSuffix(found.Path, `\c.bin`) || !strings.HasPrefix(found.Path, `D:\`) {
				t.Errorf("error path = %q, want the D:\\...\\c.bin path", found.Path)
			}
		})
	}
}

func TestParseSummaryAcrossLanguages(t *testing.T) {
	for _, loc := range locales {
		t.Run(loc, func(t *testing.T) {
			s, ok := robocopy.ParseSummary(fixture(t, "copy_"+loc+".log"))
			if !ok {
				t.Fatal("no summary found")
			}
			if s.Files != (robocopy.Counts{Total: 4, Copied: 3, Failed: 1}) {
				t.Errorf("files = %+v", s.Files)
			}
			if s.Bytes.Copied != 3145740 || s.Dirs.Skipped != 1 {
				t.Errorf("bytes = %+v dirs = %+v", s.Bytes, s.Dirs)
			}
		})
	}
}

func TestParseSummaryNeedsThreeRows(t *testing.T) {
	if _, ok := robocopy.ParseSummary("nothing here\n\t\t 5\t\\\\h\\s\\a\n"); ok {
		t.Error("a log with no summary reported one")
	}
}

func TestParseDryRunPlan(t *testing.T) {
	for _, loc := range []string{"en", "tr"} {
		t.Run(loc, func(t *testing.T) {
			dst := map[string]string{"en": `D:\dst`, "tr": `D:\Hedef`}[loc]
			p := robocopy.ParseDryRun(fixture(t, "dry_"+loc+".log"), `\\192.168.1.5\Games`, dst)
			if !p.SummaryFound || p.Files != 2 || p.Bytes != 5368710354 {
				t.Errorf("plan totals = %d files, %d bytes, summary %v", p.Files, p.Bytes, p.SummaryFound)
			}
			if p.OverFAT32Count != 1 || len(p.OverFAT32) != 1 || !strings.HasSuffix(p.OverFAT32[0], "big.iso") {
				t.Errorf("FAT32 = %d %v, want big.iso", p.OverFAT32Count, p.OverFAT32)
			}
			if p.LargestFile != 5368709120 {
				t.Errorf("largest = %d", p.LargestFile)
			}
			if p.LongPathCount != 1 {
				t.Errorf("long paths = %d, want 1", p.LongPathCount)
			}
		})
	}
}

func TestParseDryRunWithoutSummaryFallsBackToTheListing(t *testing.T) {
	text := "\t\t 100\t\\\\h\\s\\a\n\t\t 50\t\\\\h\\s\\dir\\b\n\t\t 7\tD:\\dst\\extra\n"
	p := robocopy.ParseDryRun(text, `\\h\s`, `D:\dst`)
	if p.SummaryFound || p.Files != 2 || p.Bytes != 150 {
		t.Errorf("plan = %+v, want the two source files", p)
	}
}

func TestParseDryRunIgnoresASiblingWithTheSamePrefix(t *testing.T) {
	// \\h\s2 is not inside \\h\s, however much the text starts the same.
	p := robocopy.ParseDryRun("\t\t 100\t\\\\h\\s2\\a\n", `\\h\s`, `D:\dst`)
	if p.Files != 0 {
		t.Errorf("counted a file outside the source: %+v", p)
	}
}

func TestDecodeExit(t *testing.T) {
	tests := []struct {
		code    int
		success bool
		bits    string
	}{
		{0, true, ""},
		{1, true, "copied"},
		{2, true, "extras"},
		{3, true, "copied extras"},
		{4, true, "mismatch"},
		{5, true, "copied mismatch"},
		{6, true, "extras mismatch"},
		{7, true, "copied extras mismatch"},
		{8, false, "failed"},
		{9, false, "copied failed"},
		{15, false, "copied extras mismatch failed"},
		{16, false, "fatal"},
		{17, false, "copied fatal"},
	}
	for _, tt := range tests {
		e := robocopy.DecodeExit(tt.code)
		var bits []string
		if e.Copied {
			bits = append(bits, "copied")
		}
		if e.Extras {
			bits = append(bits, "extras")
		}
		if e.Mismatches {
			bits = append(bits, "mismatch")
		}
		if e.SomeFailed {
			bits = append(bits, "failed")
		}
		if e.Fatal {
			bits = append(bits, "fatal")
		}
		if got := strings.Join(bits, " "); got != tt.bits {
			t.Errorf("code %d bits = %q, want %q", tt.code, got, tt.bits)
		}
		if e.Success() != tt.success {
			t.Errorf("code %d success = %v, want %v", tt.code, e.Success(), tt.success)
		}
		if e.Describe() == "" {
			t.Errorf("code %d has no description", tt.code)
		}
	}
	if robocopy.DecodeExit(-1).Success() {
		t.Error("a start failure (-1) counted as success")
	}
}

func TestArgs(t *testing.T) {
	got := strings.Join(robocopy.CopyArgs(`\\h\s\`, `D:\Games\`, `C:\log.txt`), " ")
	for _, want := range []string{`\\h\s D:\Games /E`, "/MT:16", "/Z", "/R:3", "/W:5", "/NP", "/BYTES", "/NJH", "/NDL", "/FP", "/NC", "/UNILOG:C:\\log.txt", "/XJ"} {
		if !strings.Contains(got, want) {
			t.Errorf("copy args %q lack %q", got, want)
		}
	}
	if strings.Contains(got, `\ `) && strings.Contains(got, `s\ `) {
		t.Errorf("a path kept its trailing backslash: %q", got)
	}
	dry := strings.Join(robocopy.DryRunArgs(`\\h\s`, `D:\x`, `L`), " ")
	if !strings.Contains(dry, "/L") || !strings.Contains(dry, "/R:0") || strings.Contains(dry, "/MT") {
		t.Errorf("dry run args wrong: %q", dry)
	}
}

func TestLongPath(t *testing.T) {
	long := `D:\` + strings.Repeat("a", 250)
	if got := robocopy.LongPath(long); got != `\\?\`+long {
		t.Errorf("long local path = %q", got[:12])
	}
	if got := robocopy.LongPath(`\\h\s\` + strings.Repeat("a", 250)); !strings.HasPrefix(got, `\\?\UNC\h\s`) {
		t.Errorf("long UNC path = %q", got[:14])
	}
	if got := robocopy.LongPath(`D:\short`); got != `D:\short` {
		t.Errorf("short path changed to %q", got)
	}
	if got := robocopy.LongPath(`\\?\` + long); got != `\\?\`+long {
		t.Error("an already prefixed path was prefixed again")
	}
}

func TestTrackerOnEveryLanguage(t *testing.T) {
	for _, loc := range locales {
		t.Run(loc, func(t *testing.T) {
			tr := robocopy.NewTracker()
			retryingAfterError := false
			for l := range strings.SplitSeq(fixture(t, "copy_"+loc+".log"), "\n") {
				tr.Feed(l)
				if robocopy.ParseLine(l).Kind == robocopy.KindError {
					retryingAfterError = tr.Snapshot().Retrying
				}
			}
			p := tr.Snapshot()
			if p.FilesDone != 3 || p.BytesDone != 3145740 {
				t.Errorf("done = %d files %d bytes", p.FilesDone, p.BytesDone)
			}
			if !retryingAfterError {
				t.Error("an error line did not put the tracker in the retrying state")
			}
			if p.Retrying {
				t.Error("a finished file after the error did not clear the retrying state")
			}
			if len(p.Failed) != 1 {
				t.Fatalf("failed = %v, want the one file", p.Failed)
			}
			for path, code := range p.Failed {
				if code != 112 || !strings.HasSuffix(path, `\c.bin`) {
					t.Errorf("failed entry = %q %d", path, code)
				}
			}
			if len(p.FailedPaths()) != 1 {
				t.Errorf("FailedPaths = %v", p.FailedPaths())
			}
		})
	}
}

func TestTrackerClearsAFailureWhenTheRetryWorks(t *testing.T) {
	tr := robocopy.NewTracker()
	tr.Feed("2026/09/29 10:15:02 ERROR 32 (0x00000020) Copying File D:\\dst\\a.bin")
	tr.Feed("\t\t 10\tD:\\dst\\a.bin")
	if p := tr.Snapshot(); len(p.Failed) != 0 || p.Retrying {
		t.Errorf("after a successful retry: %+v", p)
	}
}

func TestTrackerKeepsOnlyTheRecentFiles(t *testing.T) {
	tr := robocopy.NewTracker()
	for i := range 20 {
		tr.Feed("\t\t 1\t\\\\h\\s\\f" + string(rune('a'+i)))
	}
	p := tr.Snapshot()
	if len(p.Recent) != 8 || !strings.HasSuffix(p.Recent[7], `\ft`) {
		t.Errorf("recent = %v", p.Recent)
	}
}

// utf16le encodes text the way /UNILOG writes it: a byte order mark, then
// UTF-16 little endian.
func utf16le(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// memLog is a log that grows, for the tail and run tests.
type memLog struct {
	mu sync.Mutex
	b  []byte
}

func (m *memLog) append(b []byte) {
	m.mu.Lock()
	m.b = append(m.b, b...)
	m.mu.Unlock()
}

func (m *memLog) ReadFrom(off int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if off >= int64(len(m.b)) {
		return nil, nil
	}
	return append([]byte(nil), m.b[off:]...), nil
}

func TestTailSurvivesReadsThatSplitCharactersAndLines(t *testing.T) {
	text := "\t\t 5\t\\\\h\\s\\写真\\😀.txt\r\nsecond line\r\nthird"
	raw := utf16le(text)
	// Every possible split point, including in the middle of a two-byte
	// character and of a surrogate pair.
	for cut := 1; cut < len(raw); cut++ {
		log := &memLog{}
		tail := robocopy.NewTail(log)
		var lines []string
		log.append(raw[:cut])
		l1, err := tail.Poll()
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, l1...)
		log.append(raw[cut:])
		l2, _ := tail.Poll()
		lines = append(lines, l2...)
		if len(lines) != 2 || !strings.HasSuffix(lines[0], "😀.txt\r") || lines[1] != "second line\r" {
			t.Fatalf("cut %d: lines = %q", cut, lines)
		}
	}
}

func TestDecodeLogKeepsTheLastLineWithoutANewline(t *testing.T) {
	got := robocopy.DecodeLog(utf16le("one\ntwo"))
	if got != "one\ntwo\n" {
		t.Errorf("DecodeLog = %q", got)
	}
	if got := robocopy.DecodeLog([]byte("plain ascii\nlog\n")); got != "plain ascii\nlog\n" {
		t.Errorf("a log with no byte order mark = %q", got)
	}
}

func TestFileLogReadsFromAnOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	fl := robocopy.FileLog{Path: path}
	if b, err := fl.ReadFrom(0); err != nil || len(b) != 0 {
		t.Fatalf("a missing log = %q, %v; want an empty read", b, err)
	}
	if err := os.WriteFile(path, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := fl.ReadFrom(6)
	if err != nil || string(b) != "world" {
		t.Errorf("ReadFrom(6) = %q, %v", b, err)
	}
}

// fakeRunner plays robocopy: it appends a script of log chunks to the log,
// waiting between them, then returns an exit code.
type fakeRunner struct {
	log    *memLog
	chunks [][]byte
	code   int
	err    error
	wait   chan struct{}

	mu   sync.Mutex
	args [][]string
}

func (f *fakeRunner) Run(ctx context.Context, args []string) (int, error) {
	f.mu.Lock()
	f.args = append(f.args, args)
	f.mu.Unlock()
	for _, c := range f.chunks {
		f.log.append(c)
		select {
		case <-time.After(5 * time.Millisecond):
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}
	if f.wait != nil {
		select {
		case <-f.wait:
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}
	return f.code, f.err
}

func TestCopyReportsProgressWhileRunningAndTheSummaryAtTheEnd(t *testing.T) {
	log := &memLog{}
	text := strings.ReplaceAll(fixture(t, "copy_de.log"), "\n", "\r\n")
	// Feed the log in awkward 37-byte chunks (an odd number, so UTF-16
	// characters are split), the way a growing file is seen.
	raw := utf16le(text)
	var chunks [][]byte
	for i := 0; i < len(raw); i += 37 {
		chunks = append(chunks, raw[i:min(i+37, len(raw))])
	}
	fr := &fakeRunner{log: log, chunks: chunks, code: 9}
	tool := robocopy.Tool{Runner: fr, Poll: 2 * time.Millisecond}

	var updates []robocopy.Progress
	res, err := tool.Copy(context.Background(), `\\h\s`, `D:\dst`, "log", log, func(p robocopy.Progress) { updates = append(updates, p) })
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) < 2 {
		t.Errorf("got %d progress updates, want several while it ran", len(updates))
	}
	for i := 1; i < len(updates); i++ {
		if updates[i].BytesDone < updates[i-1].BytesDone {
			t.Fatal("progress went backwards")
		}
	}
	if res.Exit.Code != 9 || res.Exit.Success() || !res.Exit.SomeFailed {
		t.Errorf("exit = %+v", res.Exit)
	}
	if !res.HasSummary || res.Summary.Files.Failed != 1 {
		t.Errorf("summary = %+v %v", res.Summary, res.HasSummary)
	}
	if res.Progress.FilesDone != 3 || len(res.Progress.Failed) != 1 {
		t.Errorf("progress = %+v", res.Progress)
	}
	if got := strings.Join(fr.args[0], " "); !strings.Contains(got, "/UNILOG:log") {
		t.Errorf("robocopy was not told to write the log: %q", got)
	}
}

func TestCopyStopsWhenTheContextIsCancelled(t *testing.T) {
	log := &memLog{}
	fr := &fakeRunner{log: log, wait: make(chan struct{})}
	tool := robocopy.Tool{Runner: fr, Poll: time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := tool.Copy(ctx, `\\h\s`, `D:\d`, "log", log, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestCopyReportsAStartFailure(t *testing.T) {
	fr := &fakeRunner{log: &memLog{}, code: -1, err: errors.New("cannot start")}
	_, err := robocopy.Tool{Runner: fr, Poll: time.Millisecond}.Copy(context.Background(), "a", "b", "l", fr.log, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot start") {
		t.Errorf("err = %v", err)
	}
}

func TestDryRunReturnsThePlan(t *testing.T) {
	log := &memLog{}
	log.append(utf16le(fixture(t, "dry_en.log")))
	fr := &fakeRunner{log: &memLog{}, code: 1}
	p, err := robocopy.Tool{Runner: fr}.DryRun(context.Background(), `\\192.168.1.5\Games`, `D:\dst`, "log", log)
	if err != nil {
		t.Fatal(err)
	}
	if p.Files != 2 || p.OverFAT32Count != 1 {
		t.Errorf("plan = %+v", p)
	}
}

func TestDryRunFailureCarriesTheLogTail(t *testing.T) {
	log := &memLog{}
	log.append(utf16le("2026/09/29 10:15:02 ERROR 53 (0x00000035) Accessing Source Directory \\\\h\\s\\\nThe network path was not found.\n"))
	fr := &fakeRunner{log: &memLog{}, code: 16}
	_, err := robocopy.Tool{Runner: fr}.DryRun(context.Background(), `\\h\s`, `D:\d`, "log", log)
	var re *robocopy.RunError
	if !errors.As(err, &re) || !re.Exit.Fatal || !strings.Contains(re.Log, "53") {
		t.Errorf("err = %v, want a RunError with the log tail", err)
	}
}
