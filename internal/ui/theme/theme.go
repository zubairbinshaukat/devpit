// Package theme holds Devpit's colour palette and the Lip Gloss styles built
// from it.
//
// Every theme is built once, at package initialisation, and handed out by
// [For] or [Resolve]. Nothing here is mutated after that, and View functions
// must never build a [lipgloss.Style] from scratch: styles are values, copying
// one and chaining a width onto it is free, rebuilding one per frame is not.
//
// Rules the palette follows, from the research summary:
//
//   - One accent per theme. Colour always carries meaning, never decoration.
//   - No faint/dim SGR: Windows Terminal renders it unreadably on light
//     backgrounds. An explicit muted grey is used instead.
//   - No bold-only cues: Windows Terminal renders bold as a brighter colour,
//     not a thicker glyph. The selected row uses accent plus reverse video,
//     which still reads under NO_COLOR.
package theme

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Palette is the set of semantic colours. Roles come from the PRD; each role
// has a light-background and a dark-background variant.
type Palette struct {
	// Accent is the single brand colour: cursor, selection, headings.
	Accent color.Color
	// Success marks ticks, freed space and the Safe risk tier.
	Success color.Color
	// Warning marks the Review risk tier and skipped items.
	Warning color.Color
	// Danger marks the Careful risk tier and errors.
	Danger color.Color
	// Info marks paths, sizes and key hints.
	Info color.Color
	// Muted is secondary text. It replaces the faint SGR attribute.
	Muted color.Color
	// Fg is ordinary body text.
	Fg color.Color
	// Border is the colour of rules and card borders: quieter than Muted, so
	// chrome never competes with the text inside it.
	Border color.Color
	// Surface is the background of a pill or badge. It is a small step away
	// from the terminal background, never a second colour scheme.
	Surface color.Color
}

// Dark is the palette for dark terminal backgrounds.
//
// The hues are pastel and low-saturation on purpose: Devpit lives in a
// terminal next to code, so it should feel calm, not loud. The neutrals are
// borrowed from the Catppuccin Mocha scale (a soothing, widely used terminal
// palette) so the chrome sits naturally beside the themes most people already
// run. The brand accent is a soft aqua; lavender carries secondary
// information such as sizes, paths and key hints.
var Dark = Palette{
	Accent:  lipgloss.Color("#7FDBCA"), // aqua: cursor, selection, headings
	Success: lipgloss.Color("#A6E3A1"), // green
	Warning: lipgloss.Color("#FAB387"), // peach
	Danger:  lipgloss.Color("#F38BA8"), // rose-red
	Info:    lipgloss.Color("#B4BEFE"), // lavender
	Muted:   lipgloss.Color("#8A90A8"), // explicit grey, never faint
	Fg:      lipgloss.Color("#CDD6F4"),
	Border:  lipgloss.Color("#45475A"),
	Surface: lipgloss.Color("#313244"),
}

// Light is the palette for light terminal backgrounds: the same roles, taken
// from the Catppuccin Latte scale so every hue keeps its contrast on white.
var Light = Palette{
	Accent:  lipgloss.Color("#179299"), // teal
	Success: lipgloss.Color("#40A02B"),
	Warning: lipgloss.Color("#DF8E1D"),
	Danger:  lipgloss.Color("#D20F39"),
	Info:    lipgloss.Color("#7287FD"), // lavender
	Muted:   lipgloss.Color("#6C6F85"),
	Fg:      lipgloss.Color("#4C4F69"),
	Border:  lipgloss.Color("#BCC0CC"),
	Surface: lipgloss.Color("#E6E9EF"),
}

// Variant is a colour scheme: which accent the theme paints with, or none.
type Variant string

// The variants. Aqua is Devpit's own. Blue and Rose are for people who want a
// different accent; Mono drops colour entirely and leans on reverse video, so
// it also describes what NO_COLOR looks like.
const (
	VariantAqua Variant = "aqua"
	VariantBlue Variant = "blue"
	VariantRose Variant = "rose"
	VariantMono Variant = "mono"
)

// Preference values [Resolve] accepts for config.Theme. Auto, Dark and Light
// pick a background and keep the aqua accent; the rest pick an accent and
// follow the terminal's own background.
const (
	PrefAuto  = "auto"
	PrefDark  = "dark"
	PrefLight = "light"
	PrefAqua  = "aqua"
	PrefBlue  = "blue"
	PrefRose  = "rose"
	PrefMono  = "mono"
)

// Presets is every value [Resolve] understands, in the order a settings
// screen should cycle them.
var Presets = []string{PrefAuto, PrefDark, PrefLight, PrefAqua, PrefBlue, PrefRose, PrefMono}

// accents holds each variant's accent colour for a light and a dark
// background. Mono has none: its "accent" is ordinary body text, and the
// selection is carried by reverse video alone.
var accents = map[Variant]struct{ light, dark color.Color }{
	VariantAqua: {Light.Accent, Dark.Accent},
	VariantBlue: {lipgloss.Color("#1E66F5"), lipgloss.Color("#89B4FA")},
	VariantRose: {lipgloss.Color("#EA76CB"), lipgloss.Color("#F5C2E7")},
	VariantMono: {Light.Fg, Dark.Fg},
}

// Theme is a palette plus the styles built from it. Every field is an
// immutable value; take a copy and chain on it if a screen needs a variant.
type Theme struct {
	// IsDark records which background this theme was built for.
	IsDark bool
	// Variant records which accent this theme was built with.
	Variant Variant
	// Palette is the raw colour set behind the styles.
	Palette Palette

	// Base is plain body text.
	Base lipgloss.Style
	// Title is a screen heading, in the accent colour.
	Title lipgloss.Style
	// Subtitle is a secondary heading: body colour, one weight up.
	Subtitle lipgloss.Style
	// Muted is secondary text such as menu descriptions. It is an explicit
	// grey, never the faint attribute.
	Muted lipgloss.Style
	// Accent is accent-coloured text.
	Accent lipgloss.Style
	// Success, Warning, Danger and Info carry meaning, never decoration.
	Success lipgloss.Style
	Warning lipgloss.Style
	Danger  lipgloss.Style
	Info    lipgloss.Style

	// Selected is the style for the row under the cursor: accent plus reverse
	// video, so it survives NO_COLOR.
	Selected lipgloss.Style
	// SelectedDesc is the description line of the selected row.
	SelectedDesc lipgloss.Style
	// Cursor is the caret drawn to the left of the selected row.
	Cursor lipgloss.Style
	// Hint is the right-hand note on a menu row, e.g. "~14 GB can be freed".
	Hint lipgloss.Style

	// HeaderBar is the top bar of every screen.
	HeaderBar lipgloss.Style
	// HeaderName is the app name inside the header.
	HeaderName lipgloss.Style
	// Badge is the reverse-video app-name chip in the header.
	Badge lipgloss.Style
	// Pill, PillLabel and PillValue draw a small labelled chip such as
	// "node 22.3.0". The three share one background so they read as one
	// object; PillGood is the value style for a figure that is good news.
	Pill      lipgloss.Style
	PillLabel lipgloss.Style
	PillValue lipgloss.Style
	PillGood  lipgloss.Style
	// FooterBar is the key-hint bar at the bottom of every screen.
	FooterBar lipgloss.Style
	// Rule is the horizontal line under the header and over the footer.
	Rule lipgloss.Style

	// Card is a bordered box used for menus, summaries and dialogs.
	// CardASCII is the same box for terminals without box drawing.
	Card      lipgloss.Style
	CardASCII lipgloss.Style
	// CardTitle is the heading inside a card.
	CardTitle lipgloss.Style

	// Logo is the big wordmark on the home screen, Tagline the line under it.
	Logo    lipgloss.Style
	Tagline lipgloss.Style

	// Key is a keystroke in a hint, Desc is its description.
	Key  lipgloss.Style
	Desc lipgloss.Style

	// Notice is a full-width attention line, e.g. the resize warning.
	Notice lipgloss.Style
}

// CardFor returns the card border that the given icon tier can draw: the
// rounded one everywhere, the +-| one on a terminal limited to ASCII.
func (t *Theme) CardFor(ascii bool) lipgloss.Style {
	if ascii {
		return t.CardASCII
	}
	return t.Card
}

// build assembles a complete theme for one background and one accent. It is
// called once per combination, from the package-level table below.
func build(isDark bool, v Variant) Theme {
	ld := lipgloss.LightDark(isDark)
	acc := accents[v]

	p := Palette{
		Accent:  ld(acc.light, acc.dark),
		Success: ld(Light.Success, Dark.Success),
		Warning: ld(Light.Warning, Dark.Warning),
		Danger:  ld(Light.Danger, Dark.Danger),
		Info:    ld(Light.Info, Dark.Info),
		Muted:   ld(Light.Muted, Dark.Muted),
		Fg:      ld(Light.Fg, Dark.Fg),
		Border:  ld(Light.Border, Dark.Border),
		Surface: ld(Light.Surface, Dark.Surface),
	}
	if v == VariantMono {
		// Monochrome keeps two greys and nothing else: meaning is carried by
		// the risk shapes and the words beside them, which is what the rule
		// "shape + colour + word" is insurance for in the first place.
		p.Success, p.Warning, p.Danger, p.Info = p.Fg, p.Fg, p.Fg, p.Fg
	}

	base := lipgloss.NewStyle().Foreground(p.Fg)
	muted := lipgloss.NewStyle().Foreground(p.Muted)
	accent := lipgloss.NewStyle().Foreground(p.Accent)
	rule := lipgloss.NewStyle().Foreground(p.Border)
	pill := lipgloss.NewStyle().Foreground(p.Fg).Background(p.Surface)

	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.Border).
		Padding(0, 1)

	return Theme{
		IsDark:  isDark,
		Variant: v,
		Palette: p,

		Base:     base,
		Title:    accent.Bold(true),
		Subtitle: base.Bold(true),
		Muted:    muted,
		Accent:   accent,
		Success:  lipgloss.NewStyle().Foreground(p.Success),
		Warning:  lipgloss.NewStyle().Foreground(p.Warning),
		Danger:   lipgloss.NewStyle().Foreground(p.Danger),
		Info:     lipgloss.NewStyle().Foreground(p.Info),

		// Reverse video is the part that carries the selection when colours
		// are stripped; the accent foreground is the part that carries it when
		// they are not.
		Selected:     lipgloss.NewStyle().Foreground(p.Accent).Reverse(true),
		SelectedDesc: accent,
		Cursor:       accent,
		Hint:         lipgloss.NewStyle().Foreground(p.Info),

		HeaderBar:  base,
		HeaderName: accent.Bold(true),
		Badge:      lipgloss.NewStyle().Foreground(p.Accent).Reverse(true).Bold(true),
		Pill:       pill,
		PillLabel:  lipgloss.NewStyle().Foreground(p.Muted).Background(p.Surface),
		PillValue:  lipgloss.NewStyle().Foreground(p.Info).Background(p.Surface),
		PillGood:   lipgloss.NewStyle().Foreground(p.Success).Background(p.Surface),
		FooterBar:  muted,
		Rule:       rule,

		Card:      card,
		CardASCII: card.Border(lipgloss.ASCIIBorder()),
		CardTitle: accent.Bold(true),

		Logo:    accent,
		Tagline: muted,

		Key:  lipgloss.NewStyle().Foreground(p.Info),
		Desc: muted,

		Notice: lipgloss.NewStyle().Foreground(p.Warning).Padding(1, 2),
	}
}

// themes holds every built theme, keyed by variant and background. They are
// built once, at program load, and never change: they are the only
// package-level state here and they are read-only.
var themes = map[Variant][2]*Theme{
	VariantAqua: newPair(VariantAqua),
	VariantBlue: newPair(VariantBlue),
	VariantRose: newPair(VariantRose),
	VariantMono: newPair(VariantMono),
}

// newPair builds the light and dark themes of one variant. Index 0 is light,
// index 1 is dark, matching the bool that selects between them.
func newPair(v Variant) [2]*Theme {
	light := build(false, v)
	dark := build(true, v)
	return [2]*Theme{&light, &dark}
}

// Of returns the theme for one variant and background. The returned pointer is
// shared and must be treated as read-only.
func Of(v Variant, isDark bool) *Theme {
	pair, ok := themes[v]
	if !ok {
		pair = themes[VariantAqua]
	}
	if isDark {
		return pair[1]
	}
	return pair[0]
}

// For returns the default (aqua) theme for a dark or light terminal
// background.
func For(isDark bool) *Theme { return Of(VariantAqua, isDark) }

// Resolve maps a config theme preference and the background colour detected by
// Bubble Tea onto a concrete theme.
//
// "auto", "dark" and "light" behave as they always have: they choose a
// background and keep Devpit's aqua. "aqua", "blue", "rose" and "mono" choose an
// accent and follow the terminal's own background. Anything else is auto.
func Resolve(pref string, detectedDark bool) *Theme {
	switch strings.ToLower(strings.TrimSpace(pref)) {
	case PrefLight:
		return Of(VariantAqua, false)
	case PrefDark:
		return Of(VariantAqua, true)
	case PrefBlue:
		return Of(VariantBlue, detectedDark)
	case PrefRose:
		return Of(VariantRose, detectedDark)
	case PrefMono:
		return Of(VariantMono, detectedDark)
	default:
		return Of(VariantAqua, detectedDark)
	}
}
