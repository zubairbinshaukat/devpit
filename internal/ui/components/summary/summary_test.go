package summary_test

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/summary"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

func ctx(width int) uictx.Context {
	return uictx.Context{
		Theme: theme.For(true), Icons: icons.For(icons.TierUnicode), Config: config.Default(),
		Width: width, Height: 30, BodyHeight: 25,
	}
}

// lockedResult is a delete where two folders were held open, with the
// reasons the clean engine writes: each already names its folder.
func lockedResult() summary.Result {
	return summary.Result{
		Headline: "Freed 2.7 GB", FreedBytes: 2_700_000_000, Items: 3, Duration: 41 * time.Second,
		Skipped: []summary.Skipped{
			{Name: `shopfront\node_modules`, Reason: `Couldn't delete shopfront\node_modules — it's open in Code.exe. Close it and press R to retry.`},
			{Name: `mobile-app\node_modules`, Reason: `Couldn't delete mobile-app\node_modules — it's open in node.exe. Close it and press R to retry.`},
			{Name: `legacy\dist`, Reason: "access denied"},
		},
	}
}

// The card never runs past the terminal, however long a reason is.
func TestTheCardFitsTheTerminal(t *testing.T) {
	for _, w := range []int{80, 100, 140} {
		out := ansi.Strip(summary.New(lockedResult()).View(ctx(w)))
		for _, l := range strings.Split(out, "\n") {
			if ansi.StringWidth(l) > w {
				t.Errorf("width %d: line is %d wide: %q", w, ansi.StringWidth(l), l)
			}
		}
		// Nothing was cut: the retry instruction is all there.
		if joined := strings.Join(strings.Fields(strings.ReplaceAll(out, "│", " ")), " "); !strings.Contains(joined, "Close it and press R to retry.") {
			t.Errorf("width %d: the instruction was lost:\n%s", w, out)
		}
	}
}

// A reason that already names the folder is not preceded by the name again;
// a short reason still is.
func TestASkippedItemIsNamedOnce(t *testing.T) {
	out := strings.Join(strings.Fields(strings.ReplaceAll(ansi.Strip(summary.New(lockedResult()).View(ctx(200))), "│", " ")), " ")
	if n := strings.Count(out, `shopfront\node_modules`); n != 1 {
		t.Errorf(`shopfront\node_modules appears %d times, want once:%s`, n, out)
	}
	if !strings.Contains(out, `legacy\dist — access denied`) {
		t.Errorf("a reason without the name must follow the name:\n%s", out)
	}
}
