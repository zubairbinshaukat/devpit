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
	if !strings.Contains(got, "no output for 3m 05s") || !strings.Contains(got, "press s to skip") {
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
	if n != 2 {
		t.Errorf("lines = %d, want 2 (heading and one step)", n)
	}
	plain := ansi.Strip(block)
	if !strings.Contains(plain, "What to do next") || !strings.Contains(plain, "Git: Retry: winget") {
		t.Errorf("block = %q", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if ansi.StringWidth(line) > 60 {
			t.Errorf("line too wide: %q", line)
		}
	}
}
