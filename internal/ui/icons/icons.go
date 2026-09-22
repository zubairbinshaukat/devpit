// Package icons provides Devpit's three glyph tiers and the rule that picks
// one.
//
// Windows Terminal ships Cascadia Mono, which has no Nerd Font glyphs, and no
// program can ask a terminal which fonts it has. So icons are tiered:
//
//	nerd     opt-in: the user installed the icon font or confirmed the probe
//	unicode  the default on a capable terminal
//	ascii    dumb terminals, old conhost, or --ascii
//
// Two invariants hold across the whole package and are enforced by tests:
//
//   - Every non-empty glyph is exactly one terminal cell wide, so columns line
//     up in every tier. The one exception is the tick box in the unicode and
//     ascii tiers, which is the three-cell "[x]" / "[ ]".
//   - Nerd glyphs are BMP private-use only (nf-fa, nf-dev, nf-seti, nf-oct).
//     Nothing from nf-md, which lives in a supplementary plane that older
//     terminals render as two cells or not at all.
package icons

import "strings"

// Tier names an icon set. TierAuto is only ever a preference; [Resolve] turns
// it into one of the three concrete tiers.
type Tier string

// The icon tiers.
const (
	TierAuto    Tier = "auto"
	TierNerd    Tier = "nerd"
	TierUnicode Tier = "unicode"
	TierASCII   Tier = "ascii"
)

// Set is one complete tier. Every field is a glyph ready to print; tool and
// object glyphs are empty in the non-nerd tiers, and callers must treat an
// empty glyph as "draw nothing, take no columns".
type Set struct {
	// Tier records which tier this set is, for settings screens and tests.
	Tier Tier

	// Structure.
	Folder     string
	FolderOpen string
	File       string

	// Status.
	Tick string
	Fail string
	Warn string

	// Selection. Three cells wide in the unicode and ascii tiers.
	Checked   string
	Unchecked string

	// Risk tiers. Shape plus colour plus word, never colour alone.
	Safe    string
	Review  string
	Careful string

	// Tools. Empty outside the nerd tier.
	Node    string
	Docker  string
	Git     string
	Windows string
	Python  string
	Rust    string
	Go      string

	// Objects. Empty outside the nerd tier.
	Trash   string
	Gear    string
	Globe   string
	Key     string
	Update  string
	Package string
	Clock   string

	// Bars for the relative-size column.
	BarFull  string
	BarEmpty string

	// Cursor is the caret drawn beside the selected row.
	Cursor string

	// extensions maps a lower-cased file name or extension to a glyph. It is
	// nil outside the nerd tier.
	extensions map[string]string
}

// Nerd returns the opt-in Nerd Font tier.
func Nerd() Set {
	return Set{
		Tier:       TierNerd,
		Folder:     "", // nf-fa-folder
		FolderOpen: "", // nf-fa-folder_open
		File:       "", // nf-fa-file
		Tick:       "", // nf-fa-check
		Fail:       "", // nf-fa-close
		Warn:       "", // nf-fa-warning
		Checked:    "", // nf-fa-check_square_o
		Unchecked:  "", // nf-fa-square_o
		Safe:       "●",
		Review:     "◆",
		Careful:    "▲",
		Node:       "", // nf-dev-nodejs_small
		Docker:     "", // nf-linux-docker
		Git:        "", // nf-dev-git
		Windows:    "", // nf-dev-windows
		Python:     "", // nf-dev-python
		Rust:       "", // nf-dev-rust
		Go:         "", // nf-dev-go
		Trash:      "", // nf-fa-trash
		Gear:       "", // nf-fa-cog
		Globe:      "", // nf-fa-globe
		Key:        "", // nf-fa-key
		Update:     "", // nf-fa-refresh
		Package:    "", // nf-oct-package
		Clock:      "", // nf-fa-clock_o
		BarFull:    "█",
		BarEmpty:   "░",
		Cursor:     "", // nf-fa-chevron_right
		extensions: nerdExtensions,
	}
}

// Unicode returns the default tier: box-drawing and geometric shapes that
// every modern Windows console font has.
func Unicode() Set {
	return Set{
		Tier:       TierUnicode,
		Folder:     "▸",
		FolderOpen: "▾",
		File:       "·",
		Tick:       "✓",
		Fail:       "✗",
		Warn:       "!",
		Checked:    "[x]",
		Unchecked:  "[ ]",
		Safe:       "●",
		Review:     "◆",
		Careful:    "▲",
		BarFull:    "█",
		BarEmpty:   "░",
		Cursor:     "▸",
	}
}

// ASCII returns the lowest tier: nothing above U+007F except the three risk
// shapes, which the plan keeps in every tier so risk always reads the same.
func ASCII() Set {
	return Set{
		Tier:       TierASCII,
		Folder:     ">",
		FolderOpen: "v",
		File:       "-",
		// The plan writes this as "OK", but a two-cell tick breaks every
		// column it sits in, so the ascii tier uses a one-cell "+".
		Tick:      "+",
		Fail:      "X",
		Warn:      "!",
		Checked:   "[x]",
		Unchecked: "[ ]",
		Safe:      "●",
		Review:    "◆",
		Careful:   "▲",
		BarFull:   "#",
		BarEmpty:  "-",
		Cursor:    ">",
	}
}

// For returns the concrete set for a tier. TierAuto and any unknown value fall
// back to the unicode tier; use [Resolve] to apply the auto rule properly.
func For(t Tier) Set {
	switch t {
	case TierNerd:
		return Nerd()
	case TierASCII:
		return ASCII()
	case TierUnicode, TierAuto:
		return Unicode()
	default:
		return Unicode()
	}
}

// Tiers lists the three concrete tiers, in descending richness.
func Tiers() []Tier { return []Tier{TierNerd, TierUnicode, TierASCII} }

// FileIcon returns the glyph for a file or directory name. Full names are
// matched first ("package.json", "Dockerfile"), then the extension, then the
// generic file glyph. Matching is case-insensitive, like Windows.
func (s Set) FileIcon(name string) string {
	if s.extensions == nil {
		return s.File
	}
	lower := strings.ToLower(name)
	if g, ok := s.extensions[lower]; ok {
		return g
	}
	if i := strings.LastIndexByte(lower, '.'); i >= 0 {
		if g, ok := s.extensions[lower[i:]]; ok {
			return g
		}
	}
	return s.File
}

// Glyphs returns every non-empty glyph in the set, for tests and for the
// first-run probe. The order is stable.
func (s Set) Glyphs() []string {
	all := []string{
		s.Folder, s.FolderOpen, s.File,
		s.Tick, s.Fail, s.Warn,
		s.Checked, s.Unchecked,
		s.Safe, s.Review, s.Careful,
		s.Node, s.Docker, s.Git, s.Windows, s.Python, s.Rust, s.Go,
		s.Trash, s.Gear, s.Globe, s.Key, s.Update, s.Package, s.Clock,
		s.BarFull, s.BarEmpty, s.Cursor,
	}
	out := make([]string, 0, len(all))
	for _, g := range all {
		if g != "" {
			out = append(out, g)
		}
	}
	return out
}

// ExtensionGlyphs returns every glyph in the file-extension table.
func (s Set) ExtensionGlyphs() map[string]string { return s.extensions }
