package clean_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// TestFilterTypingDoesNotTriggerTheScreensKeys is the one place the screen's
// own bindings and a text box compete: a "d" typed into the filter must
// filter, not open the delete confirmation.
func TestFilterTypingDoesNotTriggerTheScreensKeys(t *testing.T) {
	h := start(t, baseEngines(
		item(`D:\work\api`, "node_modules", 1_200_000_000, scan.TierSafe),
		item(`D:\work\api`, "dist", 80_000_000, scan.TierReview),
	))

	h.key("/")
	for _, r := range "dist" {
		h.key(string(r))
	}
	h.settle()

	if got := h.state(); got != "results" {
		t.Fatalf("typing into the filter put the screen on %q, want results", got)
	}

	h.send(keyPress(13)) // enter commits the filter
	h.settle()

	if got := h.model().Table().VisibleCount(); got != 1 {
		t.Fatalf("the filter left %d rows, want 1", got)
	}
	if out := ansi.Strip(h.view()); !strings.Contains(out, "filter: dist") {
		t.Errorf("the filter is not shown:\n%s", out)
	}
}
