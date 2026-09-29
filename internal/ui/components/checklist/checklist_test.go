package checklist_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/checklist"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

func ctx(set icons.Set) uictx.Context {
	return uictx.Context{Theme: theme.For(true), Icons: set, Width: 80, Height: 24, BodyHeight: 20}
}

// A selected row is a band exactly as wide as the list, whatever its text,
// so the highlight reads as one bar and never as a ragged block.
func TestSelectedRowFillsTheWidth(t *testing.T) {
	for _, set := range []icons.Set{icons.Nerd(), icons.Unicode(), icons.ASCII()} {
		for _, text := range []string{"git", strings.Repeat("Microsoft Visual C++ ", 8)} {
			out := checklist.Render(ctx(set), checklist.Line{Selected: true, Box: checklist.On, Text: text, Note: "winget"}, 60)
			if w := ansi.StringWidth(out); w != 60 {
				t.Errorf("%s %q: selected row is %d cells, want 60", set.Tier, text[:3], w)
			}
		}
	}
}

// Rows never outgrow the width, selected or not, and the box survives any
// amount of cutting.
func TestRowsNeverOverflowAndKeepTheirBox(t *testing.T) {
	c := ctx(icons.Unicode())
	long := strings.Repeat("x", 200)
	for _, sel := range []bool{false, true} {
		out := checklist.Render(c, checklist.Line{Selected: sel, Box: checklist.Off, Text: long}, 40)
		if w := ansi.StringWidth(out); w > 40 {
			t.Errorf("selected=%v: %d cells, want at most 40", sel, w)
		}
		if !strings.Contains(ansi.Strip(out), "[ ]") {
			t.Errorf("selected=%v: the box was cut: %q", sel, ansi.Strip(out))
		}
	}
}

// Ticked is green, unticked is grey, partial is the warning hue: the colour
// is the at-a-glance answer to "what will happen".
func TestBoxColours(t *testing.T) {
	c := ctx(icons.Unicode())
	th := c.Theme
	cases := []struct {
		box  checklist.Box
		want string
	}{
		{checklist.On, th.CheckOn.Render("[x]")},
		{checklist.Off, th.CheckOff.Render("[ ]")},
		{checklist.Some, th.Warning.Render("[-]")},
	}
	for _, cs := range cases {
		out := checklist.Render(c, checklist.Line{Box: cs.box, Text: "git"}, 0)
		if !strings.Contains(out, cs.want) {
			t.Errorf("box %d: %q lacks %q", cs.box, out, cs.want)
		}
	}
}

// BoxColumns names the columns the box is really drawn in.
func TestBoxColumnsMatchWhatIsDrawn(t *testing.T) {
	c := ctx(icons.Unicode())
	l := checklist.Line{Box: checklist.On, Indent: 2, Text: "git"}
	from, to := checklist.BoxColumns(c, l)
	plain := []rune(ansi.Strip(checklist.Render(c, l, 0)))
	if got := string(plain[from:to]); got != "[x]" {
		t.Errorf("columns %d..%d hold %q, want the box", from, to, got)
	}
}
