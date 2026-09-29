package update

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/activity"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// smallCtx is the smallest body Devpit draws in: an 80×24 terminal less the
// four-row section header and the two-row footer.
func smallCtx() uictx.Context {
	ctx := testCtx()
	ctx.Width, ctx.Height, ctx.BodyHeight = 80, 24, 18
	return ctx
}

// manyJobs is a finished or running run of n apps, every one failed with a
// next step, the tallest summary there can be.
func manyJobs(n int, state activity.State) []job {
	var winget managers.Manager
	for _, m := range managers.All() {
		if m.Name() == "winget" {
			winget = m
		}
	}
	jobs := make([]job, n)
	for i := range jobs {
		label := fmt.Sprintf("App %02d", i)
		jobs[i] = job{pkg: i, manager: winget, label: label, row: activity.Row{
			Label: label, State: state, Detail: "crashed (0xC0000409)", Percent: -1,
			Next: "Try again. If it crashes again, restart your PC and try once more.",
		}}
	}
	return jobs
}

// checkFits fails when out is taller than the body: the app clips a body to
// its height, so anything past it (the next steps, the last rows) is lost.
func checkFits(t *testing.T, out string, ctx uictx.Context, mustShow ...string) {
	t.Helper()
	out = ansi.Strip(out)
	if n := strings.Count(out, "\n") + 1; n > ctx.BodyHeight {
		t.Errorf("%d lines for a body of %d:\n%s", n, ctx.BodyHeight, out)
	}
	for _, s := range mustShow {
		if !strings.Contains(out, s) {
			t.Errorf("lacks %q:\n%s", s, out)
		}
	}
}

func TestSummaryFitsTheSmallestTerminal(t *testing.T) {
	for _, n := range []int{1, 5, 12, 30} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			m := New()
			m.state, m.jobs = stateSummary, manyJobs(n, activity.Failed)
			ctx := smallCtx()
			checkFits(t, m.View(ctx), ctx, "What to do next", "App 00")
		})
	}
}

func TestRunningFitsTheSmallestTerminal(t *testing.T) {
	for _, n := range []int{1, 12, 30} {
		m := New()
		m.state, m.jobs = stateRunning, manyJobs(n, activity.Queued)
		m.jobs[0].row.State = activity.Running
		ctx := smallCtx()
		checkFits(t, m.View(ctx), ctx, "App 00")
	}
}
