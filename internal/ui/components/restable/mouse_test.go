package restable

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// hit finds where a click on the glyph glyph of the first line holding text
// lands, by rendering a real frame: the row counts from the table's first
// line, the column is the glyph's cell on that line.
func hit(t *testing.T, m Model, text, glyph string) (row, col int) {
	t.Helper()
	lines := plainView(m)
	for i, l := range lines {
		if !strings.Contains(l, text) {
			continue
		}
		at := strings.Index(l, glyph)
		if at < 0 {
			t.Fatalf("line %q holds no %q", l, glyph)
		}
		return i, ansi.StringWidth(l[:at])
	}
	t.Fatalf("no line holds %q:\n%s", text, strings.Join(lines, "\n"))
	return 0, 0
}

// selectedUnder counts the ticked items in one project.
func selectedUnder(m Model, project string) int {
	n := 0
	for _, it := range m.Selected() {
		if it.Project == project {
			n++
		}
	}
	return n
}

// A click on a heading's tick box ticks or clears everything under it and
// moves the cursor there.
func TestClickOnAHeadingBoxTogglesTheProject(t *testing.T) {
	m := treeTable()
	if got := selectedUnder(m, yard+`\apps\web`); got != 1 {
		t.Fatalf("the fixture should start with apps/web ticked, got %d", got)
	}
	row, col := hit(t, m, "apps/web", "[x]")
	m, _ = m.Click(testContext(100, 30), row, col+1)
	if got := selectedUnder(m, yard+`\apps\web`); got != 0 {
		t.Errorf("a click on a ticked heading's box left %d ticked", got)
	}
	if r := m.rows[m.cursor]; r.kind != rowGroup || m.node(r).label.String() != "apps/web" {
		t.Errorf("the click left the cursor on %q", m.node(r).label.String())
	}
}

// A click on an item's box ticks just that item.
func TestClickOnAnItemBoxTicksIt(t *testing.T) {
	m := treeTable()
	row, col := hit(t, m, "dist", "[ ]")
	m, _ = m.Click(testContext(100, 30), row, col)
	found := false
	for _, it := range m.Selected() {
		if it.Name == "dist" {
			found = true
		}
	}
	if !found {
		t.Error("a click on dist's box did not tick it")
	}
}

// A click on a heading's fold arrow folds it, and a second unfolds it.
func TestClickOnTheArrowFolds(t *testing.T) {
	m := treeTable()
	full := len(m.rows)
	row, col := hit(t, m, "YardTerminal", "▾")
	m, _ = m.Click(testContext(100, 30), row, col)
	if len(m.rows) >= full {
		t.Fatalf("a click on the arrow left %d rows, want fewer than %d", len(m.rows), full)
	}
	// Folded, the arrow is the same glyph as the cursor's caret, so find it
	// from the label instead: it sits one space before it.
	row, col = hit(t, m, "YardTerminal", "YardTerminal")
	m, _ = m.Click(testContext(100, 30), row, col-2)
	if len(m.rows) != full {
		t.Fatalf("a second click on the arrow left %d rows, want %d", len(m.rows), full)
	}
}

// A click on a row's name only moves the cursor; the heading and the space
// below the last row do nothing.
func TestClickElsewhereMovesTheCursor(t *testing.T) {
	m := treeTable()
	before, _ := m.SelectedCount()
	row, col := hit(t, m, "packages/admin", "packages")
	m, _ = m.Click(testContext(100, 30), row, col)
	if got := m.node(m.rows[m.cursor]).label.String(); got != "packages/admin" {
		t.Errorf("a click on a heading's name put the cursor on %q", got)
	}
	if after, _ := m.SelectedCount(); after != before {
		t.Errorf("a click on a name changed the ticks from %d to %d", before, after)
	}

	cursor := m.cursor
	for _, r := range []int{0, -1, len(m.rows) + 5} {
		m, _ = m.Click(testContext(100, 30), r, 6)
		if m.cursor != cursor {
			t.Errorf("a click on row %d moved the cursor to %d", r, m.cursor)
		}
	}
}

// A click on a scrolled table lands on the row drawn there, not on the row
// that would be there unscrolled.
func TestClickHonoursTheScrollOffset(t *testing.T) {
	m := treeTable().SetSize(100, 5)
	m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.top == 0 {
		t.Fatal("the wheel did not scroll the window")
	}
	top := m.top
	m, _ = m.Click(testContext(100, 30), 1, 60)
	if m.cursor != top {
		t.Errorf("a click on the first body row put the cursor on %d, want the window top %d", m.cursor, top)
	}
}

// The wheel scrolls three rows at a time and stops at either end.
func TestWheelScrolls(t *testing.T) {
	m := treeTable()
	m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.cursor != wheelStep {
		t.Errorf("one notch down put the cursor on %d, want %d", m.cursor, wheelStep)
	}
	for range 20 {
		m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	}
	if m.cursor != len(m.rows)-1 {
		t.Errorf("the wheel stopped at %d, want the last row %d", m.cursor, len(m.rows)-1)
	}
	for range 20 {
		m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	}
	if m.cursor != 0 || m.top != 0 {
		t.Errorf("the wheel up stopped at cursor %d, top %d", m.cursor, m.top)
	}
}

// The cursor row is the band, not a reverse-video block: the bar glyph in
// the first cell, every piece drawn on the band with no reset-shaped holes,
// and exactly the table's width.
func TestCursorRowIsABandOfTheTablesWidth(t *testing.T) {
	ctx := testContext(100, 30)
	m := treeTable()
	for _, cursor := range []int{0, 1, 2} {
		m.cursor = cursor
		lines := strings.Split(m.View(ctx), "\n")
		row := lines[1+cursor]
		plain := ansi.Strip(row)
		if !strings.HasPrefix(plain, ctx.Icons.SelectBar) {
			t.Errorf("row %d does not start with the selection bar: %q", cursor, plain)
		}
		if w := ansi.StringWidth(plain); w != 100 {
			t.Errorf("row %d is %d cells, want the table's 100", cursor, w)
		}
		if strings.Contains(row, "\x1b[7m") || strings.Contains(row, ";7m") {
			t.Errorf("row %d is still drawn in reverse video", cursor)
		}
		for _, sgr := range strings.Split(row, "\x1b[")[1:] {
			end := strings.IndexByte(sgr, 'm')
			if end <= 0 {
				continue // a reset
			}
			if !strings.Contains(sgr[:end], "48;") {
				t.Errorf("row %d has a piece off the band: \\x1b[%s", cursor, sgr[:end+1])
			}
		}
	}
}

// Tick boxes wear the tick colours, not the row's: green ticked, grey not.
func TestTickBoxesWearTheirOwnColours(t *testing.T) {
	ctx := testContext(100, 30)
	m := treeTable()
	m.cursor = len(m.rows) - 1 // keep the band off the rows under test
	out := m.View(ctx)
	if !strings.Contains(out, ctx.Checkbox(true)) {
		t.Error("no ticked box is drawn in the ticked style")
	}
	if !strings.Contains(out, ctx.Checkbox(false)) {
		t.Error("no empty box is drawn in the empty style")
	}
	if !strings.Contains(out, ctx.CheckboxPartial()) {
		t.Error("no partial box is drawn in the partial style")
	}
}
