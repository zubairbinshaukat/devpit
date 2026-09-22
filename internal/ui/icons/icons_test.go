package icons_test

import (
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
)

// TestEveryGlyphIsOneCell is the invariant the whole icon system rests on:
// columns only line up if every glyph occupies exactly one terminal cell.
// The tick boxes in the unicode and ascii tiers are the documented exception,
// because "[x]" is three cells by construction.
func TestEveryGlyphIsOneCell(t *testing.T) {
	for _, tier := range icons.Tiers() {
		set := icons.For(tier)
		t.Run(string(tier), func(t *testing.T) {
			for _, g := range set.Glyphs() {
				if g == set.Checked || g == set.Unchecked {
					continue
				}
				if w := ansi.StringWidth(g); w != 1 {
					t.Errorf("glyph %q (U+%04X) is %d cells, want 1", g, firstRune(g), w)
				}
			}
		})
	}
}

func TestTickBoxWidths(t *testing.T) {
	cases := []struct {
		tier icons.Tier
		want int
	}{
		{icons.TierNerd, 1},
		{icons.TierUnicode, 3},
		{icons.TierASCII, 3},
	}
	for _, c := range cases {
		set := icons.For(c.tier)
		for name, g := range map[string]string{"Checked": set.Checked, "Unchecked": set.Unchecked} {
			if w := ansi.StringWidth(g); w != c.want {
				t.Errorf("%s.%s = %q, %d cells, want %d", c.tier, name, g, w, c.want)
			}
		}
	}
}

// TestNerdGlyphsAreBMPPrivateUse keeps older terminals safe: nothing from a
// supplementary plane, which is where nf-md lives.
func TestNerdGlyphsAreBMPPrivateUse(t *testing.T) {
	set := icons.Nerd()
	shared := map[string]bool{set.Safe: true, set.Review: true, set.Careful: true, set.BarFull: true, set.BarEmpty: true}

	for _, g := range set.Glyphs() {
		if shared[g] {
			continue // geometric shapes, deliberately shared with the other tiers
		}
		r := firstRune(g)
		if utf8.RuneCountInString(g) != 1 {
			t.Errorf("nerd glyph %q should be a single rune", g)
			continue
		}
		if r < 0xE000 || r > 0xF8FF {
			t.Errorf("nerd glyph U+%04X is outside the BMP private use area", r)
		}
	}
}

func TestExtensionTable(t *testing.T) {
	set := icons.Nerd()
	table := set.ExtensionGlyphs()
	if len(table) < 40 {
		t.Errorf("the extension table has %d entries, expected at least 40", len(table))
	}

	for key, g := range table {
		if w := ansi.StringWidth(g); w != 1 {
			t.Errorf("extension %q maps to %q, %d cells, want 1", key, g, w)
		}
		r := firstRune(g)
		if r < 0xE000 || r > 0xF8FF {
			t.Errorf("extension %q maps to U+%04X, outside the BMP private use area", key, r)
		}
		if key != lower(key) {
			t.Errorf("extension key %q must be lower case", key)
		}
	}
}

func TestFileIcon(t *testing.T) {
	nerd := icons.Nerd()

	cases := []struct {
		name string
		want string
	}{
		{"package.json", nerd.ExtensionGlyphs()["package.json"]},
		{"PACKAGE.JSON", nerd.ExtensionGlyphs()["package.json"]},
		{"Dockerfile", nerd.ExtensionGlyphs()["dockerfile"]},
		{"main.go", nerd.ExtensionGlyphs()[".go"]},
		{"App.TSX", nerd.ExtensionGlyphs()[".tsx"]},
		{"something.unknown", nerd.File},
		{"noextension", nerd.File},
	}
	for _, c := range cases {
		if got := nerd.FileIcon(c.name); got != c.want {
			t.Errorf("FileIcon(%q) = %q, want %q", c.name, got, c.want)
		}
	}

	// The non-nerd tiers have no table and always answer with the generic
	// file glyph.
	uni := icons.Unicode()
	if got := uni.FileIcon("package.json"); got != uni.File {
		t.Errorf("unicode FileIcon = %q, want %q", got, uni.File)
	}
}

// TestEmojiAreTwoCells guards the rule that emoji never sit in an aligned
// column: they are all double width and the allow-list is closed.
func TestEmojiAreTwoCells(t *testing.T) {
	if len(icons.AllowedEmoji) != 5 {
		t.Fatalf("the emoji allow-list has %d entries, want 5", len(icons.AllowedEmoji))
	}
	for _, e := range icons.AllowedEmoji {
		if w := ansi.StringWidth(e); w != 2 {
			t.Errorf("emoji %q is %d cells, want 2", e, w)
		}
		if utf8.RuneCountInString(e) != 1 {
			t.Errorf("emoji %q must be a single codepoint", e)
		}
		if !icons.IsAllowedEmoji(e) {
			t.Errorf("emoji %q is not recognised by IsAllowedEmoji", e)
		}
	}
	if icons.IsAllowedEmoji("\U0001F4A9") {
		t.Error("IsAllowedEmoji accepted something outside the list")
	}
	if got := icons.Emoji(false, icons.EmojiFlag); got != "" {
		t.Errorf("Emoji(false, ...) = %q, want empty", got)
	}
	if got := icons.Emoji(true, icons.EmojiFlag); got != icons.EmojiFlag {
		t.Errorf("Emoji(true, ...) = %q", got)
	}
}

func TestToolGlyphsOnlyExistInNerdTier(t *testing.T) {
	nerd := icons.Nerd()
	for name, g := range map[string]string{
		"Node": nerd.Node, "Docker": nerd.Docker, "Git": nerd.Git, "Windows": nerd.Windows,
		"Python": nerd.Python, "Rust": nerd.Rust, "Go": nerd.Go, "Trash": nerd.Trash,
		"Gear": nerd.Gear, "Globe": nerd.Globe, "Key": nerd.Key, "Update": nerd.Update,
		"Package": nerd.Package, "Clock": nerd.Clock,
	} {
		if g == "" {
			t.Errorf("nerd tier is missing the %s glyph", name)
		}
	}

	for _, set := range []icons.Set{icons.Unicode(), icons.ASCII()} {
		if set.Node != "" || set.Docker != "" || set.Gear != "" || set.Package != "" {
			t.Errorf("%s tier should have no tool glyphs", set.Tier)
		}
	}
}

func TestRiskShapesAreIdenticalAcrossTiers(t *testing.T) {
	want := icons.Unicode()
	for _, tier := range icons.Tiers() {
		set := icons.For(tier)
		if set.Safe != want.Safe || set.Review != want.Review || set.Careful != want.Careful {
			t.Errorf("%s tier risk shapes differ; risk must read the same everywhere", tier)
		}
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name  string
		prefs icons.Prefs
		env   map[string]string
		want  icons.Tier
	}{
		{
			name:  "explicit tier always wins",
			prefs: icons.Prefs{Tier: icons.TierNerd},
			env:   map[string]string{"TERM": "dumb", "NO_COLOR": "1"},
			want:  icons.TierNerd,
		},
		{
			name:  "explicit ascii wins over an installed font",
			prefs: icons.Prefs{Tier: icons.TierASCII, FontInstalled: true},
			env:   map[string]string{"WT_SESSION": "abc"},
			want:  icons.TierASCII,
		},
		{
			name:  "--ascii forces the lowest tier",
			prefs: icons.Prefs{Tier: icons.TierAuto, ForceASCII: true, FontInstalled: true},
			env:   map[string]string{"WT_SESSION": "abc"},
			want:  icons.TierASCII,
		},
		{
			name:  "TERM=dumb forces ascii",
			prefs: icons.Prefs{Tier: icons.TierAuto, FontInstalled: true},
			env:   map[string]string{"TERM": "dumb"},
			want:  icons.TierASCII,
		},
		{
			name:  "NO_COLOR outside Windows Terminal forces ascii",
			prefs: icons.Prefs{Tier: icons.TierAuto},
			env:   map[string]string{"NO_COLOR": "1", "TERM": "xterm-256color"},
			want:  icons.TierASCII,
		},
		{
			name:  "NO_COLOR inside Windows Terminal keeps unicode",
			prefs: icons.Prefs{Tier: icons.TierAuto},
			env:   map[string]string{"NO_COLOR": "1", "WT_SESSION": "abc"},
			want:  icons.TierUnicode,
		},
		{
			name:  "an installed font alone is not enough for nerd",
			prefs: icons.Prefs{Tier: icons.TierAuto, FontInstalled: true},
			env:   map[string]string{"WT_SESSION": "abc"},
			want:  icons.TierUnicode,
		},
		{
			name:  "a confirmed probe opts into nerd",
			prefs: icons.Prefs{Tier: icons.TierAuto, GlyphsConfirmed: true},
			env:   map[string]string{"TERM": "xterm-256color"},
			want:  icons.TierNerd,
		},
		{
			name:  "Windows Terminal alone means unicode",
			prefs: icons.Prefs{Tier: icons.TierAuto},
			env:   map[string]string{"WT_SESSION": "abc"},
			want:  icons.TierUnicode,
		},
		{
			name:  "a bare console falls back to ascii",
			prefs: icons.Prefs{Tier: icons.TierAuto},
			env:   map[string]string{},
			want:  icons.TierASCII,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := icons.Resolve(c.prefs, icons.MapEnv(c.env))
			if got.Tier != c.want {
				t.Errorf("Resolve = %s, want %s", got.Tier, c.want)
			}
		})
	}
}

func TestResolveToleratesANilEnviron(t *testing.T) {
	if got := icons.Resolve(icons.Prefs{Tier: icons.TierAuto}, nil); got.Tier != icons.TierASCII {
		t.Errorf("Resolve with a nil Environ = %s, want ascii", got.Tier)
	}
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func lower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + 32
		}
	}
	return string(out)
}
