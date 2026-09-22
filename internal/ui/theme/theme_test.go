package theme_test

import (
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
)

// TestResolveAcceptsEveryPreset walks the list a settings screen cycles
// through and proves every name lands on a real theme.
func TestResolveAcceptsEveryPreset(t *testing.T) {
	for _, pref := range theme.Presets {
		for _, dark := range []bool{true, false} {
			if got := theme.Resolve(pref, dark); got == nil {
				t.Fatalf("Resolve(%q, %v) = nil", pref, dark)
			}
		}
	}
}

// TestResolveFollowsTheBackground pins the three preferences that pick a
// background rather than an accent.
func TestResolveFollowsTheBackground(t *testing.T) {
	cases := []struct {
		pref string
		dark bool
		want bool
	}{
		{theme.PrefAuto, true, true},
		{theme.PrefAuto, false, false},
		{theme.PrefDark, false, true},
		{theme.PrefLight, true, false},
		{"neon", true, true},
		{"", false, false},
	}
	for _, c := range cases {
		if got := theme.Resolve(c.pref, c.dark).IsDark; got != c.want {
			t.Errorf("Resolve(%q, %v).IsDark = %v, want %v", c.pref, c.dark, got, c.want)
		}
	}
}

// TestPresetsCarryTheirOwnAccent proves the three colour schemes are actually
// different: blue is not aqua, and mono has no accent at all.
func TestPresetsCarryTheirOwnAccent(t *testing.T) {
	aqua := theme.Resolve(theme.PrefAqua, true)
	blue := theme.Resolve(theme.PrefBlue, true)
	mono := theme.Resolve(theme.PrefMono, true)

	if aqua.Palette.Accent == blue.Palette.Accent {
		t.Error("the blue preset kept the aqua accent")
	}
	if mono.Palette.Accent != mono.Palette.Fg {
		t.Error("the mono preset should paint its accent in body colour")
	}
	if mono.Palette.Danger != mono.Palette.Fg || mono.Palette.Success != mono.Palette.Fg {
		t.Error("the mono preset should carry no meaning colours: risk reads by shape and word")
	}
	if theme.Resolve(theme.PrefAuto, true).Palette.Accent != aqua.Palette.Accent {
		t.Error("auto should keep Devpit's own accent")
	}
}

// TestDefaultThemeIsUnchanged keeps [theme.For], which every existing caller
// uses, on the aqua preset.
func TestDefaultThemeIsUnchanged(t *testing.T) {
	for _, dark := range []bool{true, false} {
		if got, want := theme.For(dark), theme.Resolve(theme.PrefAqua, dark); got != want {
			t.Errorf("For(%v) is not the aqua preset", dark)
		}
	}
}
