package menu_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// settingsLikeItems is a list the shape of the settings screen: twelve
// entries, every one of them two rows tall.
func settingsLikeItems() []menu.Item {
	items := make([]menu.Item, 12)
	for i := range items {
		items[i] = menu.Item{
			ID:    fmt.Sprintf("row%d", i),
			Title: fmt.Sprintf("Setting number %d", i),
			Desc:  fmt.Sprintf("What setting number %d is for", i),
		}
	}
	return items
}

// ctxAt builds a render context the size of a terminal, with the body height
// the app would hand a screen: the terminal minus the header and the footer.
func ctxAt(w, h int, ic icons.Set) uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      ic,
		Width:      w,
		Height:     h,
		BodyHeight: h - 5,
	}
}

// press builds the key events the menu answers to.
func press(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Text: string(code)} }

// TestMenuScrollsToKeepCursorVisible is the regression test for the bug the
// settings screen showed: a list longer than the terminal used to render every
// row, so the cursor walked off the bottom of the screen and the user was left
// pressing Down into nothing.
func TestMenuScrollsToKeepCursorVisible(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {60, 20}}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			ctx := ctxAt(size.w, size.h, icons.Unicode())
			m := menu.New(settingsLikeItems())

			for range 12 {
				m, _ = m.Update(press('j'))
			}
			if got, want := m.Cursor(), len(m.Items())-1; got != want {
				t.Fatalf("cursor = %d, want %d", got, want)
			}

			out := ansi.Strip(m.View(ctx))
			sel, _ := m.Selected()
			if !strings.Contains(out, sel.Title) {
				t.Errorf("the selected row %q is not on screen:\n%s", sel.Title, out)
			}
			if !strings.Contains(out, "▲") {
				t.Errorf("no marker for the rows scrolled off the top:\n%s", out)
			}
			if rows := strings.Count(out, "\n") + 1; rows > ctx.BodyHeight {
				t.Errorf("the menu drew %d rows into a body of %d:\n%s", rows, ctx.BodyHeight, out)
			}
		})
	}
}

// TestMenuKeepsEveryRowWhenItFits proves the windowing does nothing on a list
// that has room: no markers, every entry drawn.
func TestMenuKeepsEveryRowWhenItFits(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := menu.New(settingsLikeItems()[:4])

	out := ansi.Strip(m.View(ctx))
	for _, it := range m.Items() {
		if !strings.Contains(out, it.Title) {
			t.Errorf("%q is missing:\n%s", it.Title, out)
		}
	}
	if strings.ContainsAny(out, "▲▼") {
		t.Errorf("a list that fits should carry no scroll markers:\n%s", out)
	}
}

// TestMenuScrollMarkersFollowTheTier keeps the ascii tier free of anything a
// dumb terminal cannot draw.
func TestMenuScrollMarkersFollowTheTier(t *testing.T) {
	ctx := ctxAt(80, 24, icons.ASCII())
	m := menu.New(settingsLikeItems())
	for range 6 {
		m, _ = m.Update(press('j'))
	}

	out := ansi.Strip(m.View(ctx))
	if !strings.Contains(out, "^ ") || !strings.Contains(out, "v ") {
		t.Errorf("want ascii scroll markers:\n%s", out)
	}
	for _, r := range out {
		if r > 0x7F {
			t.Fatalf("the ascii tier drew %q (U+%04X):\n%s", r, r, out)
		}
	}
}

// TestMenuNeverExceedsItsWidth pins the other half of the layout contract: a
// long title, or a title and a hint together, are cut to the width the menu
// was given rather than wrapping inside the card that holds it.
func TestMenuNeverExceedsItsWidth(t *testing.T) {
	const width = 40
	ctx := ctxAt(100, 30, icons.Unicode())
	m := menu.New([]menu.Item{
		{ID: "a", Title: strings.Repeat("long title ", 8), Desc: strings.Repeat("and a longer description ", 4)},
		{ID: "b", Title: "Fix Stuck Ports & Apps", Desc: "Free busy ports", Hint: "Port 3000 busy? Kill it"},
	}).SetWidth(width)

	for i, line := range strings.Split(ansi.Strip(m.View(ctx)), "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Errorf("line %d is %d cells wide, want at most %d: %q", i, w, width, line)
		}
	}
}

// TestMenuHintIsDroppedWhenItCannotFit proves a narrow row keeps its title
// rather than a squeezed hint.
func TestMenuHintIsDroppedWhenItCannotFit(t *testing.T) {
	ctx := ctxAt(100, 30, icons.Unicode())
	m := menu.New([]menu.Item{
		{ID: "b", Title: "Fix Stuck Ports & Apps", Hint: "Port 3000 busy? Kill it"},
	}).SetWidth(28)

	out := ansi.Strip(m.View(ctx))
	if strings.Contains(out, "Port 3000") {
		t.Errorf("the hint should have been dropped:\n%s", out)
	}
	if !strings.Contains(out, "Fix Stuck Ports") {
		t.Errorf("the title should have survived:\n%s", out)
	}
}
