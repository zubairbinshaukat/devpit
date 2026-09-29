package clean_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/clean"
)

// headerRows is a header height the router might give the screen. The tests
// click through it so a screen that forgot ctx.BodyRow would miss.
const headerRows = 3

// clickOn renders a real frame, finds the first line holding text, and
// clicks the first glyph on it, in terminal coordinates: the body line plus
// the header above it, and the glyph's cell on that line.
func clickOn(t *testing.T, h *harness, text, glyph string) {
	t.Helper()
	lines := strings.Split(ansi.Strip(h.view()), "\n")
	for y, l := range lines {
		if !strings.Contains(l, text) {
			continue
		}
		at := strings.Index(l, glyph)
		if at < 0 {
			t.Fatalf("line %q holds no %q", l, glyph)
		}
		h.send(tea.MouseClickMsg{X: ansi.StringWidth(l[:at]), Y: y + headerRows, Button: tea.MouseLeft})
		return
	}
	t.Fatalf("no line holds %q:\n%s", text, strings.Join(lines, "\n"))
}

// withHeader moves the harness's body below a header, the way the router
// draws it.
func withHeader(h *harness) {
	h.ctx.BodyTop = headerRows
}

// A click on a result's tick box ticks it, through the header and the
// screen's own heading line above the table.
func TestClickTicksAResult(t *testing.T) {
	h := start(t, baseEngines(
		item(`D:\work\api`, "node_modules", 1_200_000_000, scan.TierSafe),
		item(`D:\work\api`, "dist", 80_000_000, scan.TierReview),
	))
	withHeader(h)
	before := h.selection()

	clickOn(t, h, "dist", "[ ]")
	if got := h.selection(); got != before+1 {
		t.Fatalf("a click on dist's box left %d ticked, want %d", got, before+1)
	}

	// A right click, or one on the screen's heading line, changes nothing.
	lines := strings.Split(ansi.Strip(h.view()), "\n")
	for y, l := range lines {
		if strings.Contains(l, "dist") {
			h.send(tea.MouseClickMsg{X: 6, Y: y + headerRows, Button: tea.MouseRight})
		}
	}
	h.send(tea.MouseClickMsg{X: 6, Y: headerRows, Button: tea.MouseLeft})
	if got := h.selection(); got != before+1 {
		t.Errorf("a right click or a click on the heading changed the ticks to %d", got)
	}
}

// A click on a heading's fold arrow folds the project.
func TestClickFoldsAProject(t *testing.T) {
	h := start(t, baseEngines(
		item(`D:\work\api`, "node_modules", 1_200_000_000, scan.TierSafe),
		item(`D:\work\api`, "dist", 80_000_000, scan.TierReview),
	))
	withHeader(h)

	clickOn(t, h, " api ", "▾")
	if out := ansi.Strip(h.view()); strings.Contains(out, "dist") {
		t.Errorf("a click on the arrow did not fold the project:\n%s", out)
	}
}

// The wheel reaches the table.
func TestWheelMovesTheResultsCursor(t *testing.T) {
	h := start(t, baseEngines(
		item(`D:\work\api`, "node_modules", 1_200_000_000, scan.TierSafe),
		item(`D:\work\api`, "dist", 80_000_000, scan.TierReview),
		item(`D:\work\web`, "node_modules", 500_000_000, scan.TierSafe),
	))
	// Three rows down from the api heading is the web heading, whose one
	// pre-ticked item space clears. Without the wheel, space would land on
	// the partly ticked api heading and tick more instead.
	before := h.selection()
	h.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	h.key(" ")
	if got := h.selection(); got != before-1 {
		t.Errorf("space after a wheel notch left %d ticked, want %d: the wheel did not reach the table", got, before-1)
	}
}

// A click on a submenu row opens it, the same as Enter on it.
func TestClickOpensASubmenuRow(t *testing.T) {
	screen := clean.New().WithEngines(baseEngines()).WithClock(stepClock())
	h := newHarness(t, screen, testConfig())
	withHeader(h)

	clickOn(t, h, "Choose folder", "Choose")
	h.settle()
	if got := h.state(); got != "picker" {
		t.Fatalf("a click on Choose folder put the screen on %q, want picker", got)
	}
}
