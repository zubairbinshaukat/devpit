package robocopy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
)

// The recorded_en_*.unilog fixtures are real /UNILOG output of robocopy on
// Windows 11 (English), run with exactly the flags CopyArgs and DryRunArgs
// build, between two local folders; only the folder prefix was replaced with
// C:\rec. See testdata/README.md. They are the byte-exact files robocopy
// wrote: UTF-16 little endian with a byte order mark, CRLF line ends.

// recorded reads a recorded log and decodes it the way a run does.
func recorded(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		t.Fatalf("%s is not the UTF-16 file robocopy writes", name)
	}
	return robocopy.DecodeLog(b)
}

// track feeds every line of text to a tracker for the recorded source.
func track(text string) robocopy.Progress {
	tr := robocopy.NewTrackerFor(`C:\rec\src`)
	for l := range strings.SplitSeq(text, "\n") {
		tr.Feed(l)
	}
	return tr.Snapshot()
}

func TestRecordedDryRunPlan(t *testing.T) {
	p := robocopy.ParseDryRun(recorded(t, "recorded_en_dry.unilog"), `C:\rec\src`, `C:\rec\dst`)
	if !p.SummaryFound || p.Files != 4 || p.Bytes != 1048616 {
		t.Errorf("plan = %+v, want 4 files, 1048616 bytes from the summary", p)
	}
	if p.LargestFile != 1048576 {
		t.Errorf("largest = %d", p.LargestFile)
	}
}

// TestRecordedCopyWithALockedFile is a copy in which one destination file was
// held open by another program. The log shows what real robocopy does that
// the constructed fixtures did not: the failed file's line comes before its
// error, its retry repeats the line with " Retrying..." glued on, another
// file's line gets "Waiting 1 seconds..." glued on, and a file that is only in
// the destination is listed like any other. The tracker must end with exactly
// the summary's numbers.
func TestRecordedCopyWithALockedFile(t *testing.T) {
	text := recorded(t, "recorded_en_copy_locked.unilog")
	s, ok := robocopy.ParseSummary(text)
	if !ok || s.Files.Copied != 3 || s.Files.Failed != 1 || s.Files.Extras != 1 || s.Bytes.Copied != 1048595 {
		t.Fatalf("summary = %+v %v", s, ok)
	}
	p := track(text)
	if p.FilesDone != s.Files.Copied || p.BytesDone != s.Bytes.Copied {
		t.Errorf("tracker = %d files %d bytes, summary says %d files %d bytes",
			p.FilesDone, p.BytesDone, s.Files.Copied, s.Bytes.Copied)
	}
	if len(p.Failed) != 1 || p.Failed[`C:\rec\src\locked.txt`] != 32 {
		t.Errorf("failed = %v, want locked.txt with error 32", p.Failed)
	}
	for _, r := range p.Recent {
		if strings.Contains(r, "locked.txt") || strings.Contains(r, "extra-in-dest") {
			t.Errorf("recent lists %q, which did not copy", r)
		}
	}
	if code := robocopy.DecodeExit(11); code.Success() || !code.SomeFailed {
		t.Error("exit 11 (the recorded code) must be a failure")
	}
}

func TestRecordedResumeSkipsFinishedFilesAndIgnoresExtras(t *testing.T) {
	text := recorded(t, "recorded_en_resume.unilog")
	s, _ := robocopy.ParseSummary(text)
	p := track(text)
	if p.FilesDone != 1 || p.BytesDone != 21 || s.Files.Skipped != 3 || p.BytesDone != s.Bytes.Copied {
		t.Errorf("tracker %+v, summary %+v", p, s)
	}
}

func TestRecordedFatalRun(t *testing.T) {
	text := recorded(t, "recorded_en_fatal.unilog")
	if _, ok := robocopy.ParseSummary(text); ok {
		t.Error("a run that could not start has no summary")
	}
	var found robocopy.Line
	for l := range strings.SplitSeq(text, "\n") {
		if pl := robocopy.ParseLine(l); pl.Kind == robocopy.KindError {
			found = pl
		}
	}
	if found.Code != 2 || found.Path != `C:\rec\nope\` {
		t.Errorf("error line = %+v", found)
	}
}

// TestRecordedMultiThreadBigFile is a 3 GB file and a small one copied with
// /MT:16. Robocopy wrote the big file's line only when it was finished (the
// log was polled while it ran) and the summary rows are wider than usual.
func TestRecordedMultiThreadBigFile(t *testing.T) {
	text := recorded(t, "recorded_en_mt_big.unilog")
	p := robocopy.NewTrackerFor(`C:\rec\tsrc`)
	for l := range strings.SplitSeq(text, "\n") {
		p.Feed(l)
	}
	got := p.Snapshot()
	s, ok := robocopy.ParseSummary(text)
	if !ok || got.FilesDone != 2 || got.BytesDone != 3221225477 || s.Bytes.Copied != got.BytesDone {
		t.Errorf("tracker %+v, summary %+v %v", got, s, ok)
	}
}

func TestTrackerIgnoresExtrasOutsideTheSource(t *testing.T) {
	tr := robocopy.NewTrackerFor(`\\h\s\`)
	tr.Feed("\t  \t\t       5\tD:\\dst\\extra.txt")
	tr.Feed("\t  \t\t       7\t\\\\h\\s\\a.txt")
	if p := tr.Snapshot(); p.FilesDone != 1 || p.BytesDone != 7 {
		t.Errorf("progress = %+v, want only the source file", p)
	}
}

func TestTrackerTakesBackAFileWhoseCopyFailedAndCountsARetryOnce(t *testing.T) {
	tr := robocopy.NewTracker()
	tr.Feed("\t\t 10\tD:\\s\\a.bin")
	tr.Feed("2026/09/29 10:15:02 ERROR 32 (0x00000020) Copying File D:\\s\\a.bin")
	if p := tr.Snapshot(); p.FilesDone != 0 || p.BytesDone != 0 || p.Failed[`D:\s\a.bin`] != 32 {
		t.Fatalf("after the failure: %+v", p)
	}
	tr.Feed("\t\t 10\tD:\\s\\a.bin Nouvelle tentative...")
	p := tr.Snapshot()
	if p.FilesDone != 1 || p.BytesDone != 10 || len(p.Failed) != 0 || p.Recent[0] != `D:\s\a.bin` {
		t.Errorf("after a retry that worked: %+v", p)
	}
}

func TestParseLineAcceptsAnErrorWordWithASpace(t *testing.T) {
	l := robocopy.ParseLine("29/09/2026 2:27:57 PM ERROR 5 (0x00000005) Copying File \\\\h\\s\\a")
	if l.Kind != robocopy.KindError || l.Code != 5 || l.Path != `\\h\s\a` {
		t.Errorf("ParseLine = %+v", l)
	}
}
