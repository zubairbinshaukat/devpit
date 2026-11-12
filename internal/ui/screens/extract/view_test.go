package extract

import (
	"strings"
	"testing"
	"time"

	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
)

// longOut is an output folder too long for one line of an 80-column card.
const longOut = `C:\Users\Someone\AppData\Local\Temp\a-rather-long-folder-name-0123456789\001\Game-Backup`

func TestWrapWordsNeverSplitsAFileNameAtItsHyphen(t *testing.T) {
	text := "Devpit will unpack Game-Backup.zip into D:\\Downloads\\Game-Backup and hatchzip will cut it down."
	for width := 24; width <= 72; width++ {
		lines := wrapWords(text, width)
		for _, l := range lines {
			if len([]rune(l)) > width {
				t.Errorf("width %d: line %q is too wide", width, l)
			}
		}
		joined := strings.Join(lines, " ")
		if joined != text {
			t.Errorf("width %d: the words changed: %q", width, joined)
		}
	}
}

func TestALongFolderFoldsBetweenFolders(t *testing.T) {
	const out = `C:\Users\Someone\AppData\Local\Temp\TestFolder1424851070\001\Game-Backup`
	text := "Devpit will unpack Game-Backup.zip into " + out + " and nothing is removed."
	lines := wrapWords(text, 68)
	squeeze := func(s string) string { return strings.ReplaceAll(s, " ", "") }
	if squeeze(strings.Join(lines, "")) != squeeze(text) {
		t.Fatalf("folding lost or added text: %q", lines)
	}
	for _, l := range lines {
		if len(l) > 68 {
			t.Errorf("line %q is too wide", l)
		}
	}
	// The path's first piece ends at a separator, not in the middle of a
	// name, and the folder's own name stays in one piece.
	if len(lines) < 3 || !strings.HasSuffix(lines[1], `\001\`) || !strings.HasPrefix(lines[2], "Game-Backup ") {
		t.Errorf("the path folds inside a name: %q", lines)
	}
}

func TestPathLinesKeepTheFoldersOwnName(t *testing.T) {
	short := pathLines("The files are in", `D:\Downloads\Game-Backup`, 72, false)
	if len(short) != 1 || short[0] != `The files are in D:\Downloads\Game-Backup` {
		t.Errorf("a short path: %q", short)
	}
	long := pathLines("The files are in", longOut+`\and\deeper\still\Game-Backup`, 60, false)
	if len(long) != 2 || long[0] != "The files are in" {
		t.Fatalf("a long path: %q", long)
	}
	if !strings.HasSuffix(long[1], `\Game-Backup`) || !strings.HasPrefix(long[1], `  …\`) || len([]rune(long[1])) > 60 {
		t.Errorf("the cut path is %q", long[1])
	}
	plain := pathLines("The files are in", longOut+`\and\deeper\still\Game-Backup`, 60, true)
	if !strings.HasPrefix(plain[1], `  ...\`) {
		t.Errorf("the plain tier's cut path is %q", plain[1])
	}
}

// TestALongFolderStillFitsEveryCard runs the cards that name the output
// folder with one too long for a line, at the smallest window.
func TestALongFolderStillFitsEveryCard(t *testing.T) {
	ctx := testCtx(80, 24, icons.TierUnicode)
	base := newRig(t, scripted(planFits())).toCheck().m
	base.req.OutDir = longOut

	paused := base
	paused.state = statePaused
	paused.out = extractengine.Outcome{Kind: extractengine.KindPaused, CanResume: true, Last: sampleProgress()}

	done := base
	done.state = stateDone
	done.out = extractengine.Outcome{Kind: extractengine.KindDone, Elapsed: time.Minute, Last: sampleProgress()}

	failed := base
	failed.state = stateFailed
	failed.out = extractengine.Outcome{Kind: extractengine.KindFailed, Exit: 1, CanResume: true, Error: "something", Last: sampleProgress()}

	asking := base
	q, d := asking.runQuestion()
	asking.dialog.Question, asking.dialog.Detail = q, d
	asking.state = stateConfirm

	for name, m := range map[string]Model{"check": base, "paused": paused, "done": done, "failed": failed, "confirm": asking} {
		frame := strip(m.View(ctx))
		if !strings.Contains(strings.ReplaceAll(frame, "\n", ""), `Game-Backup`) {
			t.Errorf("%s does not name the folder:\n%s", name, frame)
		}
		lines := strings.Split(frame, "\n")
		if len(lines) > ctx.BodyHeight {
			t.Errorf("%s is %d lines tall:\n%s", name, len(lines), frame)
		}
		for _, l := range lines {
			if w := len([]rune(l)); w > ctx.Width {
				t.Errorf("%s has a line %d wide: %q", name, w, l)
			}
			if l != strings.TrimRight(l, " ") && !strings.ContainsAny(l, "│") {
				t.Errorf("%s pads a line with blanks: %q", name, l)
			}
		}
	}
}

func TestSmallWording(t *testing.T) {
	if got := rate(412_000_000); got != "412 MB/s" {
		t.Errorf("rate = %q", got)
	}
	if got := rate(41_200_000); got != "41.2 MB/s" {
		t.Errorf("rate = %q", got)
	}
	if got := commas(12408); got != "12,408" {
		t.Errorf("commas = %q", got)
	}
	if got := commas(1234567); got != "1,234,567" {
		t.Errorf("commas = %q", got)
	}
	for d, want := range map[time.Duration]string{
		18 * time.Second:               "18s",
		96 * time.Second:               "1m 36s",
		2*time.Hour + 5*time.Minute:    "2h 05m",
		6*time.Minute + 12*time.Second: "6m 12s",
	} {
		if got := clock(d); got != want {
			t.Errorf("clock(%s) = %q, want %q", d, got, want)
		}
	}
	if got := freeLabel(`d:\x`); got != "Free on D:" {
		t.Errorf("freeLabel = %q", got)
	}
	if got := about(0); strings.Contains(got, "0") {
		t.Errorf("a size the plan did not give reads as %q", got)
	}
	if got := hang("✗", "one two three four five six", 12); len(got) < 2 || !strings.HasPrefix(got[1], "  ") {
		t.Errorf("hang = %q", got)
	}
}
