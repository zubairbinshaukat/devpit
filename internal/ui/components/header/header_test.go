package header_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

func tabs() []header.Tab {
	return []header.Tab{
		{ID: "clean", Label: "Clean"},
		{ID: "ports", Label: "Ports"},
		{ID: "settings", Label: "Settings"},
	}
}

func ctx(width int) uictx.Context {
	return uictx.Context{Theme: theme.For(true), Icons: icons.Unicode(), Width: width, Height: 30, BodyHeight: 25}
}

// The bar is " Clean     Ports     Settings " with one blank column before
// the first label and two between labels, so a click lands on the label the
// user saw: every column of " Clean " is clean, the gap after it is nothing.
func TestTabAtMatchesWhatIsDrawn(t *testing.T) {
	h := header.New()
	h.Tabs = tabs()
	h.ShowTabs = true

	cases := []struct {
		x    int
		want string
		ok   bool
	}{
		{0, "", false},         // margin
		{1, "clean", true},     // leading space of " Clean "
		{4, "clean", true},     // inside the label
		{7, "clean", true},     // trailing space
		{8, "", false},         // gap
		{9, "", false},         // gap
		{10, "ports", true},    // " Ports " starts here
		{16, "ports", true},    // its last cell
		{17, "", false},        // gap
		{19, "settings", true}, // " Settings "
		{28, "settings", true}, // its last cell
		{29, "", false},        // past the end
		{99, "", false},
	}
	for _, c := range cases {
		got, ok := h.TabAt(c.x)
		if ok != c.ok || got.ID != c.want {
			t.Errorf("TabAt(%d) = %q,%v want %q,%v", c.x, got.ID, ok, c.want, c.ok)
		}
	}

	// And the drawn row really is that wide: the labels sit where TabAt
	// thinks they do.
	row := ansi.Strip(strings.Split(h.View(ctx(80)), "\n")[header.TabRow])
	if !strings.HasPrefix(row, "  Clean    Ports    Settings ") {
		t.Errorf("tab row = %q", row)
	}
}

func TestNextWrapsAndStartsFromTheEnds(t *testing.T) {
	h := header.New()
	h.Tabs = tabs()

	if got, _ := h.Next(1); got.ID != "clean" {
		t.Errorf("Next(1) from home = %q, want clean", got.ID)
	}
	if got, _ := h.Next(-1); got.ID != "settings" {
		t.Errorf("Next(-1) from home = %q, want settings", got.ID)
	}
	h.Active = "settings"
	if got, _ := h.Next(1); got.ID != "clean" {
		t.Errorf("Next(1) from the last tab = %q, want clean (wrap)", got.ID)
	}
	h.Active = "clean"
	if got, _ := h.Next(-1); got.ID != "settings" {
		t.Errorf("Next(-1) from the first tab = %q, want settings (wrap)", got.ID)
	}
	if _, ok := header.New().Next(1); ok {
		t.Error("Next reported a tab on a header with none")
	}
}

func TestUpdateNoticeBecomesAPill(t *testing.T) {
	h := header.New()
	h.Tabs = tabs()
	if strings.Contains(h.View(ctx(100)), "update") {
		t.Fatal("a fresh header already shows an update pill")
	}
	h, _ = h.Update(header.UpdateMsg{Version: "v0.2.0"})
	if h.UpdateAvailable() != "0.2.0" {
		t.Errorf("UpdateAvailable = %q, want 0.2.0 (no v)", h.UpdateAvailable())
	}
	first := strings.Split(h.View(ctx(100)), "\n")[0]
	if !strings.Contains(first, "update") || !strings.Contains(first, "v0.2.0") {
		t.Errorf("badge row lacks the update pill: %q", first)
	}
}

func TestHeaderIsFourRowsWithTabs(t *testing.T) {
	h := header.New()
	h.Tabs = tabs()
	h.ShowTabs = true
	h.Title = "Free Up Disk Space"
	for _, w := range []int{80, 100, 140} {
		if n := strings.Count(h.View(ctx(w)), "\n"); n != header.Rows-1 {
			t.Errorf("width %d: %d newlines, want %d", w, n, header.Rows-1)
		}
	}
}

// The tab row is separated from the badge row by exactly one blank row, so
// the header breathes, and the blank row is not a tab: clicking it opens
// nothing because the root model tests for [header.TabRow] only.
func TestOneBlankRowSitsAboveTheTabs(t *testing.T) {
	h := header.New()
	h.Tabs = tabs()
	h.ShowTabs = true
	h.Title = "Free Up Disk Space"
	lines := strings.Split(ansi.Strip(h.View(ctx(100))), "\n")
	if len(lines) != header.Rows {
		t.Fatalf("%d rows, want %d", len(lines), header.Rows)
	}
	if strings.TrimSpace(lines[1]) != "" {
		t.Errorf("row 1 = %q, want blank", lines[1])
	}
	if !strings.Contains(lines[header.TabRow], "Clean") {
		t.Errorf("tab row %d = %q, want the tabs", header.TabRow, lines[header.TabRow])
	}
}

// On home the header drops its tab row: the menu under it already is the
// list of sections. It is two rows, reports so, and no column is a tab.
func TestHomeHeaderHasNoTabRow(t *testing.T) {
	h := header.New()
	h.Tabs = tabs()
	if h.Height() != header.RowsCompact {
		t.Fatalf("Height = %d, want %d", h.Height(), header.RowsCompact)
	}
	out := h.View(ctx(100))
	if n := strings.Count(out, "\n"); n != header.RowsCompact-1 {
		t.Errorf("%d newlines, want %d", n, header.RowsCompact-1)
	}
	if strings.Contains(ansi.Strip(out), "Ports") {
		t.Errorf("home header still draws tabs:\n%s", ansi.Strip(out))
	}
	if _, ok := h.TabAt(4); ok {
		t.Error("TabAt found a tab on a header that draws none")
	}
}

// The digit range in the hint at the right of the tab row follows the
// number of tabs, so it can never go stale when a section is added.
func TestTabHintCountsTheTabs(t *testing.T) {
	h := header.New()
	h.Tabs = tabs()
	h.ShowTabs = true
	row := ansi.Strip(strings.Split(h.View(ctx(80)), "\n")[header.TabRow])
	if !strings.HasSuffix(strings.TrimRight(row, " "), "1–3") {
		t.Errorf("tab row = %q, want the hint to end in 1–3", row)
	}
	c := ctx(80)
	c.Icons = icons.ASCII()
	row = strings.Split(ansi.Strip(h.View(c)), "\n")[header.TabRow]
	if !strings.HasSuffix(strings.TrimRight(row, " "), "1-3") {
		t.Errorf("ascii tab row = %q, want the hint to end in 1-3", row)
	}
}

// A tab row wider than the terminal is cut to the terminal's width rather
// than wrapping onto the rule under it.
func TestTabRowNeverOverflows(t *testing.T) {
	h := header.New()
	h.Tabs = append(tabs(), header.Tab{ID: "a", Label: "Accounts"}, header.Tab{ID: "b", Label: "Ports & Net"})
	h.ShowTabs = true
	for _, w := range []int{30, 45, 80} {
		lines := strings.Split(ansi.Strip(h.View(ctx(w))), "\n")
		if len(lines) != header.Rows {
			t.Fatalf("width %d: %d rows, want %d", w, len(lines), header.Rows)
		}
		if got := ansi.StringWidth(lines[header.TabRow]); got > w {
			t.Errorf("width %d: tab row is %d wide: %q", w, got, lines[header.TabRow])
		}
	}
}

// The rule under the tab bar turns into an accent underline exactly as wide
// as the open tab, and starts where that tab does.
func TestRuleUnderlinesTheOpenTab(t *testing.T) {
	h := header.New()
	h.Tabs = tabs()
	h.ShowTabs = true
	h.Active = "ports"
	lines := strings.Split(ansi.Strip(h.View(ctx(80))), "\n")
	rule := []rune(lines[header.Rows-1])
	want := " Ports "
	start := 10
	for i := range len([]rune(want)) {
		if rule[start+i] != '━' {
			t.Fatalf("rule = %q, want a heavy stroke under %q at column %d", lines[header.Rows-1], want, start)
		}
	}
	if rule[start-1] == '━' || rule[start+len([]rune(want))] == '━' {
		t.Errorf("underline is wider than the tab: %q", lines[header.Rows-1])
	}
}

// A development build reads "dev", never a lone "v"; a release reads "v0.4.0"
// whether or not it was given with its "v".
func TestVersionLabel(t *testing.T) {
	for in, want := range map[string]string{"": "dev", " ": "dev", "dev": "dev", "0.4.0": "v0.4.0", "v0.4.0": "v0.4.0"} {
		if got := header.VersionLabel(in); got != want {
			t.Errorf("VersionLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
