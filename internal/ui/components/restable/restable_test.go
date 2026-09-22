package restable

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// update regenerates the golden file. The app package defines its own flag
// of the same name, but every package's tests are a separate binary, so the
// two never meet.
var update = flag.Bool("update", false, "rewrite the golden files")

// testNow is the moment every relative time in these tests is measured
// against, so "3 mo ago" means the same thing in ten years.
var testNow = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

// testContext builds the render context a screen would hand the table.
func testContext(w, h int) uictx.Context {
	cfg := config.Default()
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      icons.Unicode(),
		Config:     cfg,
		Width:      w,
		Height:     h,
		BodyHeight: h - 4,
	}
}

// press builds a printable key press.
func press(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// special builds a named key press.
func special(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

// sample is the fixture table: two projects, one of them active, one item of
// each risk tier, plus the three notes the age column can carry.
func sample() []scan.Item {
	day := 24 * time.Hour
	return []scan.Item{
		{
			Path: `D:\work\api\node_modules`, Name: "node_modules", Project: `D:\work\api`,
			Size: 1_288_490_188, Tier: scan.TierSafe, Kind: scan.KindProjectJunk,
			Rule: "node_modules", RestoreHint: "npm install brings it back",
			LastUsed: testNow.Add(-92 * day),
		},
		{
			Path: `D:\work\api\dist`, Name: "dist", Project: `D:\work\api`,
			Size: 83_886_080, Tier: scan.TierReview, Kind: scan.KindProjectJunk,
			Rule: "dist", RestoreHint: "npm run build remakes it",
			LastUsed: testNow.Add(-92 * day),
		},
		{
			Path: `D:\work\api\.next`, Name: ".next", Project: `D:\work\api`,
			Size: 41_943_040, Tier: scan.TierCareful, Kind: scan.KindProjectJunk,
			Rule: ".next", RestoreHint: "next build remakes it",
			LastUsed: testNow.Add(-92 * day),
		},
		{
			Path: `D:\work\web\node_modules`, Name: "node_modules", Project: `D:\work\web`,
			Size: 524_288_000, Tier: scan.TierSafe, Kind: scan.KindProjectJunk,
			Rule: "node_modules", RestoreHint: "npm install brings it back",
			LastUsed: testNow.Add(-2 * day), Active: true, HardLinkedToStore: true,
		},
		{
			Path: `D:\work\old\node_modules`, Name: "node_modules", Project: `D:\work\old`,
			Size: 12_582_912, Tier: scan.TierSafe, Kind: scan.KindProjectJunk,
			Rule: "node_modules", RestoreHint: "npm install brings it back",
			LastUsed: testNow.Add(-400 * day), Unverified: true,
		},
	}
}

// table returns a sized table over the fixture, with the scan finished.
func table() Model {
	return New().
		SetNow(func() time.Time { return testNow }).
		SetSize(100, 20).
		SetItems(sample()).
		SetStreaming(false)
}

func TestSelectedCountCountsTicksAndBytes(t *testing.T) {
	m := table()
	n, bytes := m.SelectedCount()
	// Only the two plain Safe items: the active one and the unverified one
	// are Safe too but are never pre-ticked.
	if n != 1 {
		t.Fatalf("SelectedCount ticked %d items, want 1", n)
	}
	if bytes != 1_288_490_188 {
		t.Errorf("SelectedCount reported %d bytes, want 1288490188", bytes)
	}
}

func TestToggleAllTicksEverythingThenNothing(t *testing.T) {
	m := table()
	m, _ = m.Update(press("a"))
	if n, _ := m.SelectedCount(); n != len(sample()) {
		t.Fatalf("a ticked %d items, want %d", n, len(sample()))
	}
	m, _ = m.Update(press("a"))
	if n, _ := m.SelectedCount(); n != 0 {
		t.Fatalf("a again left %d items ticked, want 0", n)
	}
}

func TestSpaceOnAGroupHeadingTicksTheWholeProject(t *testing.T) {
	m := table()
	m, _ = m.Update(press("a")) // everything on
	m, _ = m.Update(press("a")) // everything off
	m, _ = m.Update(press(" ")) // cursor starts on the first heading

	got := m.Selected()
	if len(got) == 0 {
		t.Fatal("space on a heading ticked nothing")
	}
	first := got[0].Project
	for _, it := range got {
		if it.Project != first {
			t.Fatalf("space on a heading ticked %s, outside the group %s", it.Path, first)
		}
	}
}

func TestArrowsFoldAndUnfoldAGroup(t *testing.T) {
	m := table()
	before := len(m.rows)

	m, _ = m.Update(special(tea.KeyLeft))
	if len(m.rows) >= before {
		t.Fatalf("← left %d rows, want fewer than %d", len(m.rows), before)
	}

	m, _ = m.Update(special(tea.KeyRight))
	if len(m.rows) != before {
		t.Fatalf("→ left %d rows, want %d back", len(m.rows), before)
	}
}

func TestSortCyclesAndReordersBySize(t *testing.T) {
	m := table()
	if got := m.Sort(); got != SortSize {
		t.Fatalf("a fresh table sorts by %s, want size", got)
	}
	m, _ = m.Update(press("s"))
	if got := m.Sort(); got != SortName {
		t.Fatalf("s moved the sort to %s, want name", got)
	}
	m, _ = m.Update(press("s"))
	if got := m.Sort(); got != SortAge {
		t.Fatalf("s again moved the sort to %s, want age", got)
	}
}

func TestStreamingDefersSortingUntilTheScanEnds(t *testing.T) {
	small := sample()[1] // 80 MB
	big := sample()[0]   // 1.2 GB

	m := New().SetNow(func() time.Time { return testNow }).SetSize(100, 20).SetStreaming(true)
	m = m.Append([]scan.Item{small})
	m = m.Append([]scan.Item{big})

	if got := m.groups[0].items[0]; m.items[got].Name != small.Name {
		t.Errorf("mid-scan the table reordered to %s; rows must not jump while the user reads them", m.items[got].Name)
	}

	m = m.SetStreaming(false)
	if got := m.groups[0].items[0]; m.items[got].Name != big.Name {
		t.Errorf("after the scan the biggest item is %s, want %s", m.items[got].Name, big.Name)
	}
}

func TestFilterNarrowsTheTable(t *testing.T) {
	m := table()
	m, _ = m.Update(press("/"))
	if !m.Filtering() {
		t.Fatal("/ did not open the filter")
	}
	for _, r := range "dist" {
		m, _ = m.Update(press(string(r)))
	}
	m, _ = m.Update(special(tea.KeyEnter))

	if m.Filtering() {
		t.Error("enter left the filter input focused")
	}
	if got := m.VisibleCount(); got != 1 {
		t.Fatalf("the filter left %d items visible, want 1", got)
	}
	if m.Len() != len(sample()) {
		t.Errorf("the filter dropped items from the table itself: %d left", m.Len())
	}
}

func TestOlderOnlyUsesTheConfiguredThreshold(t *testing.T) {
	m := table().SetOlderDays(30)
	m, _ = m.Update(press("o"))
	if !m.OlderOnly() {
		t.Fatal("o did not turn the older-than filter on")
	}
	// The active project was touched two days ago and must drop out.
	for _, it := range m.Selected() {
		if it.Active {
			t.Fatal("an item from the last two days survived the older-than filter")
		}
	}
	if got := m.VisibleCount(); got != 4 {
		t.Fatalf("older-than left %d items, want 4", got)
	}
}

func TestEnterProceeds(t *testing.T) {
	m := table()
	_, cmd := m.Update(special(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	if _, ok := cmd().(ProceedMsg); !ok {
		t.Fatalf("enter produced %T, want ProceedMsg", cmd())
	}
}

func TestOnlyVisibleRowsAreRendered(t *testing.T) {
	m := table().SetSize(100, 6)
	out := ansi.Strip(m.View(testContext(100, 30)))
	if got := len(strings.Split(out, "\n")); got > 6 {
		t.Fatalf("a 6-row table drew %d lines", got)
	}
}

func TestTooSmallATerminalGetsANotice(t *testing.T) {
	m := table()
	out := ansi.Strip(m.View(testContext(60, 20)))
	if !strings.Contains(out, "bigger window") {
		t.Fatalf("a 60×20 terminal did not get the resize notice:\n%s", out)
	}
}

// TestResultsTableGolden pins the whole layout at 100×30 with colour
// stripped: columns, alignment, the relative bar, the risk shapes and the
// age column, all in one artefact a reviewer can read.
func TestResultsTableGolden(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	m := table().SetSize(100, 20)
	got := ansi.Strip(m.View(testContext(100, 30)))

	path := filepath.Join("testdata", "results_100x30.golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the golden file: %v (run go test ./internal/ui/components/restable -update)", err)
	}
	if got != string(want) {
		t.Errorf("the results table changed.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}
