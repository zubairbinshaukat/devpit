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

// The bar is " Clean   Ports   Settings " with one blank column before the
// first label and between labels, so a click lands on the label the user
// saw: every column of " Clean " is clean, the gap after it is nothing.
func TestTabAtMatchesWhatIsDrawn(t *testing.T) {
	h := header.New()
	h.Tabs = tabs()

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
		{9, "ports", true},     // " Ports " starts here
		{15, "ports", true},    // its last cell
		{16, "", false},        // gap
		{17, "settings", true}, // " Settings "
		{26, "settings", true}, // its last cell
		{27, "", false},        // past the end
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
	if !strings.HasPrefix(row, "  Clean   Ports   Settings ") {
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

func TestHeaderIsAlwaysThreeRows(t *testing.T) {
	h := header.New()
	h.Tabs = tabs()
	h.Title = "Free Up Disk Space"
	for _, w := range []int{80, 100, 140} {
		if n := strings.Count(h.View(ctx(w)), "\n"); n != header.Rows-1 {
			t.Errorf("width %d: %d newlines, want %d", w, n, header.Rows-1)
		}
	}
}
