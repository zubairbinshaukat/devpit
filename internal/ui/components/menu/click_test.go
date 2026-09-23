package menu_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
)

func threeItems() []menu.Item {
	return []menu.Item{
		{ID: "a", Title: "Alpha", Desc: "first"},
		{ID: "b", Title: "Bravo", Desc: "second"},
		{ID: "c", Title: "Charlie", Desc: "third", Disabled: true},
	}
}

// With room to spare the menu draws a blank line between entries, so the
// rows are: 0-1 Alpha, 2 blank, 3-4 Bravo, 5 blank, 6-7 Charlie.
func TestRowAtFollowsTheDrawnLayout(t *testing.T) {
	ctx := ctxAt(80, 24, icons.Unicode())
	m := menu.New(threeItems())

	cases := []struct {
		row  int
		want int
		ok   bool
	}{
		{-1, 0, false},
		{0, 0, true},
		{1, 0, true},
		{2, 0, false},
		{3, 1, true},
		{4, 1, true},
		{5, 0, false},
		{6, 2, true},
		{7, 2, true},
		{8, 0, false},
	}
	for _, c := range cases {
		got, ok := m.RowAt(ctx, c.row)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("RowAt(%d) = %d,%v want %d,%v", c.row, got, ok, c.want, c.ok)
		}
	}
}

func TestClickMovesTheCursorAndSelects(t *testing.T) {
	ctx := ctxAt(80, 24, icons.Unicode())
	m := menu.New(threeItems())

	m, cmd := m.Click(ctx, 4)
	if m.Cursor() != 1 {
		t.Fatalf("cursor = %d, want 1", m.Cursor())
	}
	if cmd == nil {
		t.Fatal("a click on an enabled row produced no selection")
	}
	sel, ok := cmd().(menu.SelectedMsg)
	if !ok || sel.ID != "b" || sel.Index != 1 {
		t.Errorf("selection = %+v, want b/1", sel)
	}

	// A disabled row takes the cursor but does not select.
	m, cmd = m.Click(ctx, 6)
	if m.Cursor() != 2 || cmd != nil {
		t.Errorf("disabled row: cursor=%d cmd=%v, want 2 and nil", m.Cursor(), cmd)
	}

	// Empty space does nothing at all.
	before := m.Cursor()
	m, cmd = m.Click(ctx, 2)
	if m.Cursor() != before || cmd != nil {
		t.Errorf("blank row moved the cursor or selected")
	}
}

// A scrolling list spends its first row on the "n more" marker, so row 0
// is nobody's and the first drawn item starts on row 1.
func TestRowAtSkipsTheScrollMarker(t *testing.T) {
	ctx := ctxAt(80, 24, icons.Unicode())
	m := menu.New(settingsLikeItems())
	for range 11 {
		m, _ = m.Update(press('j'))
	}
	if _, ok := m.RowAt(ctx, 0); ok {
		t.Error("row 0 of a scrolled menu is the marker, not an item")
	}
	if _, ok := m.RowAt(ctx, 1); !ok {
		t.Error("row 1 of a scrolled menu should be the first drawn item")
	}
}

func TestWheelMovesTheCursor(t *testing.T) {
	m := menu.New(threeItems())
	m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.Cursor() != 1 {
		t.Fatalf("after wheel down cursor = %d, want 1", m.Cursor())
	}
	m, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.Cursor() != 0 {
		t.Errorf("after wheel up cursor = %d, want 0", m.Cursor())
	}
}
