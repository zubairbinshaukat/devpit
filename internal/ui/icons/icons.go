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

	// Selection. Three cells wide in the unicode and ascii tiers. Partial is
	// the box of a group where only some of the items under it are ticked.
	Checked   string
	Unchecked string
	Partial   string

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
	// SelectBar is the bar at the left edge of the selected row. It is a
	// glyph rather than a colour so the selection survives NO_COLOR.
	SelectBar string

	// Spinner is the frame sequence of the busy indicator. Every frame is one
	// cell, so a spinning row never jitters.
	Spinner []string
	// Track and TrackEmpty are the progress-bar pieces: a heavy line over a
	// light one, the look of the website's terminal demo.
	Track      string
	TrackEmpty string
	// Queued marks a row that is waiting its turn in a run.
	Queued string
	// Arrow separates an old version from a new one, e.g. "4.87 → 4.91".
	Arrow string

	// sections maps a home section id to its glyph. Unlike the tool glyphs,
	// sections have a unicode shape too, so the menu has icons on a stock
	// Windows Terminal; ascii has none. The unicode shapes come from the
	// geometric-shape, arrow and operator blocks Cascadia Mono and Consolas
	// both carry, so no font substitution can widen one to two cells.
	sections map[string]string

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
		Partial:    "", // nf-fa-minus_square_o
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
		SelectBar:  "▌",
		Spinner:    brailleSpinner,
		Track:      "━",
		TrackEmpty: "─",
		Queued:     "·",
		Arrow:      "→",
		sections: map[string]string{
			"clean":    "", // nf-fa-trash
			"ports":    "", // nf-fa-plug
			"install":  "", // nf-oct-package
			"update":   "", // nf-fa-refresh
			"network":  "", // nf-fa-globe
			"gitssh":   "", // nf-dev-git_branch
			"settings": "", // nf-fa-cog
			"share":    "", // nf-fa-share_alt
		},
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
		Partial:    "[-]",
		Safe:       "●",
		Review:     "◆",
		Careful:    "▲",
		BarFull:    "█",
		BarEmpty:   "░",
		Cursor:     "▸",
		SelectBar:  "▌",
		Spinner:    brailleSpinner,
		Track:      "━",
		TrackEmpty: "─",
		Queued:     "·",
		Arrow:      "→",
		sections: map[string]string{
			"clean":    "◧",
			"ports":    "◉",
			"install":  "▣",
			"update":   "↻",
			"network":  "◎",
			"gitssh":   "◈",
			"settings": "≡",
			"share":    "⇄",
		},
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
		Tick:       "+",
		Fail:       "X",
		Warn:       "!",
		Checked:    "[x]",
		Unchecked:  "[ ]",
		Partial:    "[-]",
		Safe:       "●",
		Review:     "◆",
		Careful:    "▲",
		BarFull:    "#",
		BarEmpty:   "-",
		Cursor:     ">",
		SelectBar:  "|",
		Spinner:    []string{"|", "/", "-", "\\"},
		Track:      "=",
		TrackEmpty: "-",
		Queued:     ".",
		Arrow:      ">",
	}
}

// brailleSpinner is the ten-frame dot spinner most modern CLIs use. Every
// frame is a single braille cell.
var brailleSpinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// sectionOrder is the home menu order, so Glyphs lists section glyphs in a
// stable order.
var sectionOrder = []string{"clean", "ports", "install", "update", "network", "gitssh", "settings", "share"}

// Section returns the glyph for a home section id, or "" when the tier has
// none for it.
func (s Set) Section(id string) string { return s.sections[id] }

// SpinnerFrame returns frame n of the spinner, wrapping around. A set with no
// frames returns "".
func (s Set) SpinnerFrame(n int) string {
	if len(s.Spinner) == 0 {
		return ""
	}
	if n < 0 {
		n = -n
	}
	return s.Spinner[n%len(s.Spinner)]
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
		s.Checked, s.Unchecked, s.Partial,
		s.Safe, s.Review, s.Careful,
		s.Node, s.Docker, s.Git, s.Windows, s.Python, s.Rust, s.Go,
		s.Trash, s.Gear, s.Globe, s.Key, s.Update, s.Package, s.Clock,
		s.BarFull, s.BarEmpty, s.Cursor, s.SelectBar,
		s.Track, s.TrackEmpty, s.Queued, s.Arrow,
	}
	all = append(all, s.Spinner...)
	for _, id := range sectionOrder {
		all = append(all, s.sections[id])
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
