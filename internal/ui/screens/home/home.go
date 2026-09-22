// Package home is Devpit's main menu: the seven sections from the PRD, each
// with a fixed name and a one-line description.
//
// Descriptions are static today. They are meant to become dynamic
// ("~14 GB can be freed", "5 updates"), fed by detectors that run as commands
// after the first frame, never at startup.
//
// Every section now opens its real screen. Which screen a section opens goes
// through [Model.WithFactories], so a test can render a section without the
// screen behind it running real detection.
package home

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/logo"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/clean"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/gitssh"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/install"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/network"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/ports"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/settings"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/update"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Section identifiers, matching the PRD's main menu.
const (
	SectionClean    = "clean"
	SectionPorts    = "ports"
	SectionInstall  = "install"
	SectionUpdate   = "update"
	SectionNetwork  = "network"
	SectionGitSSH   = "gitssh"
	SectionSettings = "settings"
)

// Items returns the seven menu entries, in PRD order, with the icons of the
// given tier. Tool glyphs are empty outside the nerd tier and the menu draws
// nothing in their place.
func Items(ic icons.Set) []menu.Item {
	return []menu.Item{
		{
			ID:    SectionClean,
			Title: "Free Up Disk Space",
			Desc:  "Scan and clean dev junk, caches and temp files",
			Icon:  ic.Trash,
		},
		{
			ID:    SectionPorts,
			Title: "Fix Stuck Ports & Apps",
			Desc:  "Free busy ports and stop stuck processes",
			Hint:  "Port 3000 busy? Kill it",
			Icon:  ic.Node,
		},
		{
			ID:    SectionInstall,
			Title: "Install Developer Apps",
			Desc:  "Pick and install dev apps",
			Icon:  ic.Package,
		},
		{
			ID:    SectionUpdate,
			Title: "Update Everything",
			Desc:  "Update apps and tools through every detected package manager",
			Icon:  ic.Update,
		},
		{
			ID:    SectionNetwork,
			Title: "Network Tools",
			Desc:  "IP, connectivity and DNS helpers",
			Icon:  ic.Globe,
		},
		{
			ID:    SectionGitSSH,
			Title: "Git & SSH Setup",
			Desc:  "Identity and SSH key setup",
			Icon:  ic.Key,
		},
		{
			ID:    SectionSettings,
			Title: "Devpit Settings",
			Desc:  "Preferences, theme, privacy, tool rescan",
			Icon:  ic.Gear,
		},
	}
}

// Model is the home screen.
type Model struct {
	menu      menu.Model
	quit      key.Binding
	factories map[string]func() uictx.Screen
}

// New returns the home screen rendered with the given icon tier.
func New(ic icons.Set) Model {
	return Model{
		menu: menu.New(Items(ic)),
		quit: key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	}
}

// WithFactories overrides what a section opens, keyed by the Section
// identifiers. Anything the map does not name keeps its real constructor, and
// a nil map changes nothing.
//
// Only tests use it: it is how a golden frame of a screen whose Init runs
// real detection is rendered without ever execing a package manager.
func (m Model) WithFactories(f map[string]func() uictx.Screen) Model {
	m.factories = f
	return m
}

// Init implements uictx.Screen.
func (m Model) Init() tea.Cmd { return nil }

// Title implements uictx.Screen. Home needs no breadcrumb.
func (m Model) Title() string { return "" }

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	return []key.Binding{m.menu.Keys.Up, m.menu.Keys.Select, m.quit}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.menu.Keys.Up, m.menu.Keys.Down, m.menu.Keys.Select},
		{m.quit},
	}
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	if sel, ok := msg.(menu.SelectedMsg); ok {
		return m, m.open(sel.ID)
	}
	next, cmd := m.menu.Update(msg)
	m.menu = next
	return m, cmd
}

// Layout constants. The menu sits in a card of at most cardMax columns, which
// keeps a line of text a readable length on a wide terminal instead of
// stretching it across the whole screen, and the whole block is centred once
// there is enough room either side of it to look deliberate.
const (
	cardMax     = 72
	centreFrom  = 100
	minMenuRows = 10
	taglineText = "Pit crew ready. Pick a job."
)

// View implements uictx.Screen.
//
// The screen is a masthead over a card: the wordmark, a one-line tagline, and
// the menu inside a border. The wordmark is the first thing to go when the
// terminal is short — below roughly 25 rows it shrinks to a single spaced-out
// line, so the menu keeps every row it needs.
func (m Model) View(ctx uictx.Context) string {
	ascii := ctx.Icons.Tier == icons.TierASCII
	cardW := cardWidth(ctx.Width)

	head := m.head(ctx, cardW)
	rows := ctx.BodyHeight - lipgloss.Height(head) - 1 - 2 // blank line, card border
	if ctx.BodyHeight <= 0 {
		rows = 0
	}

	mm := m.menu.SetWidth(cardW - 4).SetHeight(max(0, rows))
	card := ctx.Theme.CardFor(ascii).Width(cardW).Render(mm.View(ctx))

	block := lipgloss.JoinVertical(lipgloss.Left, head, "", card)
	if ctx.Width >= centreFrom {
		return lipgloss.PlaceHorizontal(ctx.Width, lipgloss.Center, block)
	}
	return block
}

// head is the masthead above the card: the wordmark and the tagline, both
// centred over the card's width.
func (m Model) head(ctx uictx.Context, cardW int) string {
	th := ctx.Theme
	ascii := ctx.Icons.Tier == icons.TierASCII

	tagline := taglineText
	if e := ctx.Emoji(icons.EmojiBroom); e != "" {
		tagline = e + " " + tagline
	}

	if bigWordmark(ctx, cardW) {
		return lipgloss.JoinVertical(
			lipgloss.Left,
			centre(cardW, th.Logo.Render(logo.String(ascii))),
			"",
			centre(cardW, th.Tagline.Render(tagline)),
		)
	}
	return lipgloss.JoinVertical(
		lipgloss.Left,
		centre(cardW, th.Title.Render(compactMark)),
		centre(cardW, th.Tagline.Render(tagline)),
	)
}

// compactMark is the wordmark for a terminal too short for the block letters:
// the same six letters, spaced out so they still read as a mark.
const compactMark = "D E V P I T"

// bigWordmark reports whether there is room for the block-letter logo on top
// of a menu that is still worth looking at.
func bigWordmark(ctx uictx.Context, cardW int) bool {
	if ctx.BodyHeight <= 0 {
		return false
	}
	if cardW < logo.Width(ctx.Icons.Tier == icons.TierASCII)+2 {
		return false
	}
	// The wordmark, a blank line and the tagline, then the blank line and the
	// card border between the masthead and the first menu row.
	head := logo.Height(false) + 2
	return ctx.BodyHeight-head-1-2 >= minMenuRows
}

// cardWidth is how wide the menu card is drawn.
func cardWidth(width int) int {
	if width <= 0 {
		return cardMax
	}
	return max(24, min(width-2, cardMax))
}

// centre places one block in the middle of the card's width.
func centre(width int, s string) string {
	if width <= 0 {
		return s
	}
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, s)
}

// open maps a section to the screen it pushes. Each section owns one screen
// and pushes it once; the flows inside a section are states of that screen,
// not further router entries.
func (m Model) open(id string) tea.Cmd {
	if newScreen, ok := m.factories[id]; ok && newScreen != nil {
		return uictx.Push(newScreen())
	}
	switch id {
	case SectionClean:
		return uictx.Push(clean.New())
	case SectionPorts:
		return uictx.Push(ports.New())
	case SectionInstall:
		return uictx.Push(install.New())
	case SectionUpdate:
		return uictx.Push(update.New())
	case SectionNetwork:
		return uictx.Push(network.New())
	case SectionGitSSH:
		return uictx.Push(gitssh.New())
	case SectionSettings:
		return uictx.Push(settings.New())
	default:
		return nil
	}
}
