package activity_test

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/activity"
)

func TestSkipGateNeedsTwoPresses(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var g activity.SkipGate

	g, fire := g.Press(2, t0)
	if fire || !g.Armed(2, t0) {
		t.Fatalf("first press: fire=%v armed=%v, want armed and not fired", fire, g.Armed(2, t0))
	}
	g, fire = g.Press(2, t0.Add(2*time.Second))
	if !fire {
		t.Fatal("second press inside the window did not fire")
	}
	if g.Armed(2, t0.Add(2*time.Second)) {
		t.Error("gate stayed armed after firing")
	}
}

func TestSkipGateExpiresChangesJobAndDisarms(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	g, _ := activity.SkipGate{}.Press(1, t0)
	if _, fire := g.Press(1, t0.Add(activity.SkipWindow+time.Second)); fire {
		t.Error("a press after the window fired")
	}
	if _, fire := g.Press(2, t0.Add(time.Second)); fire {
		t.Error("a press for a different job fired")
	}
	if _, fire := g.Disarm().Press(1, t0.Add(time.Second)); fire {
		t.Error("a press after Disarm fired")
	}
}

func TestStuckRowHintsAtSkip(t *testing.T) {
	c := ctx(100)
	r := activity.Row{Label: "Docker Desktop", State: activity.Running, Percent: -1, Detail: "Downloading", Stuck: 3*time.Minute + 5*time.Second}
	got := ansi.Strip(activity.RowView(c, r, 0, 100, 20))
	if !strings.Contains(got, "no output for 3m 05s") || !strings.Contains(got, "press s twice to skip") {
		t.Errorf("row = %q", got)
	}
	if strings.Contains(got, "Downloading") {
		t.Errorf("the hint should replace the latest line: %q", got)
	}
	r.Stuck = 0
	if got := ansi.Strip(activity.RowView(c, r, 0, 100, 20)); strings.Contains(got, "press s") {
		t.Errorf("a row with output shows the hint: %q", got)
	}
}

func TestQueuedRowShowsWhatItWaitsFor(t *testing.T) {
	r := activity.Row{Label: "Contoso App", Old: "1", New: "2", State: activity.Queued, Percent: -1, Detail: "needs admin rights, retrying at the end"}
	got := ansi.Strip(activity.RowView(ctx(100), r, 0, 100, 10))
	if !strings.Contains(got, "needs admin rights, retrying at the end") || !strings.Contains(got, "queued") {
		t.Errorf("row = %q", got)
	}
}

func TestNextSteps(t *testing.T) {
	c := ctx(60)
	if block, n := activity.NextSteps(c, rows(), 60); block != "" || n != 0 {
		t.Errorf("rows with no next step gave %q", block)
	}
	rs := []activity.Row{
		{Label: "Git", State: activity.Skipped, Detail: "skipped by you", Next: "Retry: winget upgrade --id Git.Git -e --silent --accept-package-agreements"},
		{Label: "Node", State: activity.Done},
	}
	block, n := activity.NextSteps(c, rs, 60)
	plain := ansi.Strip(block)
	lines := strings.Split(plain, "\n")
	if n != len(lines) || n < 3 {
		t.Errorf("lines = %d for %d drawn; a step wider than the screen must wrap:\n%s", n, len(lines), plain)
	}
	if !strings.Contains(plain, "What to do next") || !strings.Contains(plain, "Git: Retry: winget") {
		t.Errorf("block = %q", plain)
	}
	var words []string
	for _, line := range lines[1:] {
		if ansi.StringWidth(line) > 60 {
			t.Errorf("line too wide: %q", line)
		}
		words = append(words, strings.Fields(line)...)
	}
	// Every character of the command is on screen, in order, and no word
	// was split: it can be typed from what is shown.
	want := "Git: Retry: winget upgrade --id Git.Git -e --silent --accept-package-agreements"
	if got := strings.Join(words, " "); got != want {
		t.Errorf("wrapped step reads %q, want %q", got, want)
	}
	if strings.Contains(plain, "…") {
		t.Errorf("a step was cut: %q", plain)
	}
}

func TestFitNextStepsDropsWholeStepsOnly(t *testing.T) {
	c := ctx(40)
	var rs []activity.Row
	for _, name := range []string{"Alpha", "Bravo", "Charlie", "Delta"} {
		rs = append(rs, activity.Row{Label: name, State: activity.Failed, Next: "Run: winget upgrade --id Contoso." + name + " -e --silent"})
	}
	block, n := activity.NextSteps(c, rs, 40)
	for maxLines := 2; maxLines <= n; maxLines++ {
		got, h := activity.FitNextSteps(c, block, n, maxLines)
		plain := ansi.Strip(got)
		if h > maxLines || h != len(strings.Split(plain, "\n")) {
			t.Fatalf("max %d: height %d for\n%s", maxLines, h, plain)
		}
		// Each kept step is complete: its command ends in "-silent".
		for _, name := range []string{"Alpha", "Bravo", "Charlie", "Delta"} {
			if i := strings.Index(plain, name+":"); i >= 0 && maxLines > 3 && !strings.Contains(plain[i:], "--silent") {
				t.Errorf("max %d: step %s is shown half:\n%s", maxLines, name, plain)
			}
		}
		if maxLines < n && !strings.Contains(plain, "more") {
			t.Errorf("max %d: nothing says steps were left out:\n%s", maxLines, plain)
		}
	}
	if got, h := activity.FitNextSteps(c, block, n, 1); got != "" || h != 0 {
		t.Errorf("no room should drop the block, got %d lines", h)
	}
	if got, h := activity.FitNextSteps(c, block, n, n); got != block || h != n {
		t.Error("a block that fits must come back unchanged")
	}
}
