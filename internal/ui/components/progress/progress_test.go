package progress_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/progress"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

func testContext() uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      icons.Unicode(),
		Config:     config.Default(),
		Width:      100,
		Height:     30,
		BodyHeight: 26,
	}
}

func TestFractionAndPercent(t *testing.T) {
	cases := []struct {
		done, total int
		fraction    float64
		percent     int
	}{
		{0, 0, 0, 0},
		{0, 10, 0, 0},
		{5, 10, 0.5, 50},
		{10, 10, 1, 100},
		{11, 10, 1, 100},
		{-1, 10, 0, 0},
	}
	for _, tc := range cases {
		m := progress.New("Deleting").SetCounts(tc.done, tc.total)
		if got := m.Fraction(); got != tc.fraction {
			t.Errorf("%d/%d Fraction = %v, want %v", tc.done, tc.total, got, tc.fraction)
		}
		if got := m.Percent(); got != tc.percent {
			t.Errorf("%d/%d Percent = %d, want %d", tc.done, tc.total, got, tc.percent)
		}
	}
}

// TestTerminalBarIsIndeterminateUntilTheTotalIsKnown keeps the taskbar
// honest: a scan cannot count directories before it has walked them, so it
// must not claim to be at zero percent of a known total.
func TestTerminalBarIsIndeterminateUntilTheTotalIsKnown(t *testing.T) {
	scanning := progress.New("Scanning").SetCounts(120, 0)
	if got := scanning.TerminalBar(); got.State != tea.ProgressBarIndeterminate {
		t.Errorf("a scan reported %v, want indeterminate", got.State)
	}

	deleting := progress.New("Deleting").SetCounts(3, 12)
	bar := deleting.TerminalBar()
	if bar.State != tea.ProgressBarDefault {
		t.Errorf("a delete reported %v, want the default state", bar.State)
	}
	if bar.Value != 25 {
		t.Errorf("a delete reported %d%%, want 25", bar.Value)
	}
}

func TestViewShowsBarCountersAndPath(t *testing.T) {
	m := progress.New("Deleting").
		SetWidth(100).
		SetCounts(3, 12).
		SetCurrent(`D:\work\api\node_modules`)

	out := ansi.Strip(m.View(testContext()))
	for _, want := range []string{"Deleting", "█", "░", "25%", "3/12", `D:\work\api\node_modules`} {
		if !strings.Contains(out, want) {
			t.Errorf("the bar is missing %q:\n%s", want, out)
		}
	}
}

func TestLongPathsAreTruncated(t *testing.T) {
	long := `D:\work\` + strings.Repeat("deep\\", 40) + "node_modules"
	m := progress.New("Scanning").SetWidth(60).SetCurrent(long)

	out := ansi.Strip(m.View(testContext()))
	for _, line := range strings.Split(out, "\n") {
		if ansi.StringWidth(line) > 60 {
			t.Fatalf("a line ran to %d cells in a 60-cell box:\n%s", ansi.StringWidth(line), line)
		}
	}
	if !strings.Contains(out, "…") {
		t.Error("the path was cut without an ellipsis to show it")
	}
}

func TestNoteIsShownBesideTheCounters(t *testing.T) {
	m := progress.New("Scanning").SetWidth(100).SetCounts(5, 0)
	m.Note = "stale · rescanning"
	if out := ansi.Strip(m.View(testContext())); !strings.Contains(out, "stale · rescanning") {
		t.Errorf("the note is missing:\n%s", out)
	}
}
