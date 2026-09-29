package activity_test

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/activity"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

func ctx(width int) uictx.Context {
	return uictx.Context{Theme: theme.For(true), Icons: icons.Unicode(), Width: width, Height: 30, BodyHeight: 25}
}

// rows is a run in every state, with the long names and long output lines a
// real winget run produces.
func rows() []activity.Row {
	return []activity.Row{
		{Label: "Claude", Old: "1.21459.0.0", New: "1.44121.2", State: activity.Done, Elapsed: 12 * time.Second},
		{Label: "Microsoft Visual C++ 2015-2022 Redistributable (x86) - 14.44.35211", Old: "14.44.35211.0", New: "14.51.36247.0", State: activity.Warn, Detail: "updated, restart needed", Elapsed: time.Minute},
		{Label: "Docker Desktop", Old: "4.87.0", New: "4.91.0", State: activity.Running, Percent: 42, Detail: "Downloading https://desktop.docker.com/win/main/amd64/239619/Docker%20Desktop%20Installer.exe", Elapsed: 3 * time.Second},
		{Label: "FFmpeg", Old: "8.1.2", New: "9.0.2", State: activity.Queued, Percent: -1},
		{Label: "Outlook for Windows", State: activity.Failed, Detail: "in use, close it and retry"},
		{Label: "Recordly 1.3.3", State: activity.Skipped, Detail: "unticked"},
	}
}

// Every row fits the width it was given, whatever the name or the output
// line: a row that wraps would break the one-line-per-job promise.
func TestRowsNeverOverflow(t *testing.T) {
	for _, w := range []int{60, 80, 100, 140} {
		out := activity.View(ctx(w), rows(), 3, w, 0)
		for _, line := range strings.Split(out, "\n") {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("width %d: line is %d cells: %q", w, got, ansi.Strip(line))
			}
		}
	}
}

// Each state wears its own mark, so a finished run reads at a glance.
func TestStateMarks(t *testing.T) {
	c := ctx(100)
	out := ansi.Strip(activity.View(c, rows(), 0, 100, 0))
	lines := strings.Split(out, "\n")
	want := []string{c.Icons.Tick, c.Icons.Warn, c.Icons.SpinnerFrame(0), c.Icons.Queued, c.Icons.Fail, c.Icons.Queued}
	for i, mark := range want {
		if !strings.HasPrefix(strings.TrimLeft(lines[i], " "), mark) {
			t.Errorf("row %d = %q, want it to start with %q", i, lines[i], mark)
		}
	}
	if !strings.Contains(lines[0], "1.21459.0.0 → 1.44121.2") {
		t.Errorf("done row lacks its version move: %q", lines[0])
	}
	if !strings.Contains(lines[2], "42%") || !strings.Contains(lines[2], "━") {
		t.Errorf("running row lacks its bar: %q", lines[2])
	}
	if !strings.Contains(lines[3], "queued") {
		t.Errorf("queued row lacks its label: %q", lines[3])
	}
}

// With reduced motion the spinner holds its first frame, so two ticks draw
// the same row.
func TestReducedMotionHoldsTheSpinner(t *testing.T) {
	c := ctx(100)
	c.ReducedMotion = true
	r := []activity.Row{{Label: "git", State: activity.Running, Percent: -1}}
	if activity.View(c, r, 0, 100, 0) != activity.View(c, r, 7, 100, 0) {
		t.Error("the spinner moved with reduced motion on")
	}
	c.ReducedMotion = false
	if activity.View(c, r, 0, 100, 0) == activity.View(c, r, 1, 100, 0) {
		t.Error("the spinner did not move between frames")
	}
}

// A run taller than the screen keeps the job in flight on it, with markers
// saying how much is hidden.
func TestWindowFollowsTheRunningJob(t *testing.T) {
	var rs []activity.Row
	for i := range 30 {
		st := activity.Done
		switch {
		case i == 20:
			st = activity.Running
		case i > 20:
			st = activity.Queued
		}
		rs = append(rs, activity.Row{Label: "pkg" + string(rune('a'+i%26)) + strings.Repeat("x", i/26), State: st, Percent: -1})
	}
	out := ansi.Strip(activity.View(ctx(80), rs, 0, 80, 8))
	lines := strings.Split(out, "\n")
	if len(lines) != 8 {
		t.Fatalf("drew %d lines, want 8:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "more") || !strings.Contains(lines[7], "more") {
		t.Errorf("missing the hidden-row markers:\n%s", out)
	}
	found := false
	for _, l := range lines {
		if strings.Contains(l, "pkgu") { // row 20
			found = true
		}
	}
	if !found {
		t.Errorf("the running row is off screen:\n%s", out)
	}
}

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                                    "",
		800 * time.Millisecond:               "0.8s",
		12 * time.Second:                     "12s",
		62 * time.Second:                     "1m 02s",
		time.Hour + 5*time.Minute + 3e9:      "1h 05m",
		6*time.Minute + 12*time.Second:       "6m 12s",
		9*time.Second + 990*time.Millisecond: "10.0s",
	}
	for d, want := range cases {
		if got := activity.Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
}

// The summary names every outcome that happened, and only those.
func TestDoneBox(t *testing.T) {
	c := ctx(100)
	tally := activity.Count(rows())
	if tally != (activity.Tally{Done: 1, Warn: 1, Failed: 1, Skipped: 1}) {
		t.Fatalf("Count = %+v", tally)
	}
	out := ansi.Strip(activity.DoneBox(c, "Updated", tally, 72*time.Second, 100))
	for _, want := range []string{"Updated 2", "1 needs a restart", "Skipped 1", "Failed 1", "1m 12s"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
	clean := ansi.Strip(activity.DoneBox(c, "Updated", activity.Tally{Done: 3}, 0, 100))
	if strings.Contains(clean, "Failed") || strings.Contains(clean, "Skipped") {
		t.Errorf("a clean run mentions outcomes that did not happen:\n%s", clean)
	}
}

// The header counts the job in flight, not the ones already done, and
// reaches 100% only when the last one finishes.
func TestHeader(t *testing.T) {
	c := ctx(100)
	out := ansi.Strip(activity.Header(c, "Updating", 2, 17, "winget", 124*time.Second, 100))
	for _, want := range []string{"Updating 3 of 17", "winget", "2m 04s", "12%"} {
		if !strings.Contains(out, want) {
			t.Errorf("header lacks %q:\n%s", want, out)
		}
	}
	done := ansi.Strip(activity.Header(c, "Updating", 17, 17, "", 0, 100))
	if !strings.Contains(done, "17 of 17") || !strings.Contains(done, "100%") {
		t.Errorf("finished header = %q", done)
	}
}
