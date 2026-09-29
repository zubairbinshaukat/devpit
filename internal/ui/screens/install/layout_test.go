package install

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

// failedRows is n apps that all failed with a next step: the tallest summary.
func failedRows(n int, state activity.State) []activity.Row {
	rows := make([]activity.Row, n)
	for i := range rows {
		rows[i] = activity.Row{
			Label: fmt.Sprintf("App %02d", i), State: state, Detail: "crashed (0xC0000409)", Percent: -1,
			Next: "Try again. If it crashes again, restart your PC and try once more.",
		}
	}
	return rows
}

// checkFits fails when out is taller than the body: the app clips a body to
// its height, so the next steps and the key hint under them would be lost.
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
			m.state, m.jobs = stateSummary, failedRows(n, activity.Failed)
			ctx := smallCtx()
			checkFits(t, m.View(ctx), ctx, "What to do next", "done")
		})
	}
}

func TestRunningFitsTheSmallestTerminal(t *testing.T) {
	for _, n := range []int{1, 12, 30} {
		m := New()
		m.manager = managers.All()[0]
		m.state, m.jobs, m.runTotal = stateRunning, failedRows(n, activity.Queued), n
		m.jobs[0].State = activity.Running
		ctx := smallCtx()
		checkFits(t, m.View(ctx), ctx, "App 00")
	}
}
