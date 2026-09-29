// Package home is Devpit's main menu: the eight sections from the PRD, each
// with a fixed name, an icon in the section's own colour and a one-line
// description.
//
// Only the highlighted section shows its description, in a line every other
// section keeps blank: the list stays calm and keeps its shape, and the
// sentence under the cursor (or the pointer, where the terminal reports
// hover) is the one worth reading.
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
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/zubairbinshaukat/devpit/internal/about"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/logo"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/clean"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/gitssh"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/install"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/network"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/ports"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/settings"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/share"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/update"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Section identifiers, matching the PRD's main menu. They are the theme's
// own section names, so each section's icon finds its hue by id.
const (
	SectionClean    = theme.SectionClean
	SectionPorts    = theme.SectionPorts
	SectionInstall  = theme.SectionInstall
	SectionUpdate   = theme.SectionUpdate
	SectionNetwork  = theme.SectionNetwork
	SectionGitSSH   = theme.SectionGitSSH
	SectionSettings = theme.SectionSettings
	SectionShare    = theme.SectionShare
)

// Tabs returns the eight sections as the header draws them: the same order
// as the menu, with a short label that fits a tab.
func Tabs() []header.Tab {
	return []header.Tab{
		{ID: SectionClean, Label: "Clean"},
		{ID: SectionPorts, Label: "Ports"},
		{ID: SectionInstall, Label: "Apps"},
		{ID: SectionUpdate, Label: "Update"},
		{ID: SectionNetwork, Label: "Network"},
		{ID: SectionGitSSH, Label: "Git"},
		{ID: SectionSettings, Label: "Settings"},
		{ID: SectionShare, Label: "Share"},
	}
}

// TabByDigit maps a typed "1".."7" to its tab, and reports false for any
// other key text.
func TabByDigit(text string) (header.Tab, bool) {
	tabs := Tabs()
	if len(text) != 1 || text[0] < '1' || int(text[0]-'0') > len(tabs) {
		return header.Tab{}, false
	}
	return tabs[text[0]-'1'], true
}

// SectionFor reports which section a screen belongs to, or "" for a screen
// that is not one of the eight (home itself, first run, a sub-screen). The
// header uses it to light the right tab, so it looks at the screen's type
// rather than trusting a title that a screen may change as it works.
func SectionFor(s uictx.Screen) string {
	switch s.(type) {
	case clean.Model:
		return SectionClean
	case ports.Model:
		return SectionPorts
	case install.Model:
		return SectionInstall
	case update.Model:
		return SectionUpdate
	case network.Model:
		return SectionNetwork
	case gitssh.Model:
		return SectionGitSSH
	case settings.Model:
		return SectionSettings
	case share.Model:
		return SectionShare
	default:
		return ""
	}
}

// Items returns the eight menu entries, in PRD order, with the icons of the
// given tier. The ascii tier has no section glyphs and the menu draws nothing
// in their place.
func Items(ic icons.Set) []menu.Item {
	item := func(id, title, desc string) menu.Item {
		return menu.Item{ID: id, Title: title, Desc: desc, Icon: ic.Section(id), Hue: id}
	}
	ports := item(SectionPorts, "Fix Stuck Ports & Apps", "Free busy ports and stop stuck processes")
	ports.Hint = "Port 3000 busy? Kill it"
	return []menu.Item{
		item(SectionClean, "Free Up Disk Space", "Scan and clean dev junk, caches and temp files"),
		ports,
		item(SectionInstall, "Install Developer Apps", "Pick and install dev apps"),
		item(SectionUpdate, "Update Everything", "Update apps and tools through every detected package manager"),
		item(SectionNetwork, "Network Tools", "IP, connectivity and DNS helpers"),
		item(SectionGitSSH, "Git & SSH Setup", "Identity and SSH key setup"),
		item(SectionSettings, "Devpit Settings", "Preferences, theme, privacy, tool rescan"),
		item(SectionShare, "Share Files", "Move big folders between two PCs on your network"),
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
		menu: menu.New(Items(ic)).DescOnSelectedOnly(true).ReserveDescRows(true),
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
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case menu.SelectedMsg:
		return m, m.Open(msg.ID)
	case tea.KeyPressMsg:
		// 1-8 opens a section directly. Home has no text input, so a digit
		// can never be something the user meant to type.
		if t, ok := TabByDigit(msg.Text); ok {
			return m, m.Open(t.ID)
		}
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		row := ctx.BodyRow(msg.Y) - m.menuTop(ctx)
		mm := m.sizedMenu(ctx)
		next, cmd := mm.Click(ctx, row)
		m.menu = next
		return m, cmd
	case tea.MouseMotionMsg:
		// The pointer passing over a row highlights it, so its description
		// shows up in place, the way it would under the keyboard cursor.
		row := ctx.BodyRow(msg.Y) - m.menuTop(ctx)
		if next, changed := m.sizedMenu(ctx).Hover(ctx, row); changed {
			m.menu = next
		}
		return m, nil
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
)

// View implements uictx.Screen.
//
// The screen is a masthead over a card: the wordmark with its byline, and
// the menu inside a border. The wordmark is the first thing to go when the
// terminal is short — below roughly 25 rows it shrinks to a single spaced-out
// line, so the menu keeps every row it needs.
func (m Model) View(ctx uictx.Context) string {
	ascii := ctx.Icons.Tier == icons.TierASCII
	cardW := cardWidth(ctx.Width)

	head := m.head(ctx, cardW)
	mm := m.sizedMenu(ctx)
	card := ctx.Theme.CardFor(ascii).Width(cardW).Render(mm.View(ctx))

	parts := []string{head, "", card}
	if m.hasTagline(ctx) {
		parts = append(parts, "", centre(cardW, tagline(ctx)))
	}
	block := lipgloss.JoinVertical(lipgloss.Left, parts...)
	if top := m.topPad(ctx); top > 0 {
		block = strings.Repeat("\n", top) + block
	}
	if ctx.Width >= centreFrom {
		return lipgloss.PlaceHorizontal(ctx.Width, lipgloss.Center, block)
	}
	return block
}

// Rows the tagline under the card takes: a blank line and the line itself.
const taglineRows = 2

// spare is how many body rows the masthead and the card leave unused.
func (m Model) spare(ctx uictx.Context) int {
	if ctx.BodyHeight <= 0 {
		return 0
	}
	cardW := cardWidth(ctx.Width)
	used := m.headHeight(ctx, cardW) + 1 + 2 + m.sizedMenu(ctx).Height(ctx)
	return max(0, ctx.BodyHeight-used)
}

// hasTagline reports whether there is room for the line under the card.
func (m Model) hasTagline(ctx uictx.Context) bool { return m.spare(ctx) >= taglineRows }

// topPad is the blank rows above the masthead that centre the whole block in
// the body, so a tall terminal does not leave the menu hanging from the top
// with a gap under it.
func (m Model) topPad(ctx uictx.Context) int {
	left := m.spare(ctx)
	if m.hasTagline(ctx) {
		left -= taglineRows
	}
	return max(0, left/2)
}

// tagline is the muted line under the card: the space Devpit has won back
// on this machine once there is any, and a greeting before that. It is the
// one place the home screen talks about the user rather than about itself.
func tagline(ctx uictx.Context) string {
	th := ctx.Theme
	if freed := ctx.Config.LifetimeFreedBytes; freed > 0 {
		return th.Muted.Render("You've freed ") + th.Success.Render(header.FormatBytes(freed)) +
			th.Muted.Render(" with Devpit. See you next lap.")
	}
	keys := "1–8"
	if ctx.Icons.Tier == icons.TierASCII {
		keys = "1-8"
	}
	return th.Muted.Render("Pit crew ready. Pick a section, or press " + keys + ".")
}

// sizedMenu is the menu with the width and height View draws it at, so the
// click path and the render path measure the same rows.
func (m Model) sizedMenu(ctx uictx.Context) menu.Model {
	cardW := cardWidth(ctx.Width)
	rows := ctx.BodyHeight - m.headHeight(ctx, cardW) - 1 - 2 // blank line, card border
	if ctx.BodyHeight <= 0 {
		rows = 0
	}
	return m.menu.SetWidth(cardW - 4).SetHeight(max(0, rows))
}

// menuTop is the body row the menu's first line is drawn on: after the
// centring pad, the masthead, the blank line and the card's top border.
func (m Model) menuTop(ctx uictx.Context) int {
	return m.topPad(ctx) + m.headHeight(ctx, cardWidth(ctx.Width)) + 2
}

// headHeight is how many rows the masthead takes, without rendering it.
func (m Model) headHeight(ctx uictx.Context, cardW int) int {
	if m.bigWordmark(ctx, cardW) {
		// wordmark, byline
		return logo.Height(ctx.Icons.Tier == icons.TierASCII) + 1
	}
	return 1
}

// head is the masthead above the card: the wordmark with its byline, centred
// over the card's width. The byline is the author's credit; it is drawn
// tight under the letters so it reads as part of the mark.
func (m Model) head(ctx uictx.Context, cardW int) string {
	th := ctx.Theme
	ascii := ctx.Icons.Tier == icons.TierASCII

	if m.bigWordmark(ctx, cardW) {
		return lipgloss.JoinVertical(
			lipgloss.Left,
			centre(cardW, th.Logo.Render(logo.String(ascii))),
			centre(cardW, th.Muted.Render(about.Byline)),
		)
	}
	return centre(cardW, th.Title.Render(compactMark)+" "+th.Muted.Render(about.Byline))
}

// compactMark is the wordmark for a terminal too short for the block letters:
// the same six letters, spaced out so they still read as a mark.
const compactMark = "D E V P I T"

// bigWordmark reports whether there is room for the block-letter logo on top
// of the whole menu. The menu wins: when both do not fit, the wordmark
// shrinks to one line rather than the menu starting to scroll.
func (m Model) bigWordmark(ctx uictx.Context, cardW int) bool {
	if ctx.BodyHeight <= 0 {
		return false
	}
	ascii := ctx.Icons.Tier == icons.TierASCII
	if cardW < logo.Width(ascii)+2 {
		return false
	}
	// The wordmark and its byline, then the blank line and the card border
	// between the masthead and the first menu row.
	head := logo.Height(ascii) + 1
	full := ctx
	full.BodyHeight = 0 // unbounded: the menu's natural height
	need := max(minMenuRows, m.menu.SetWidth(cardW-4).SetHeight(0).Height(full))
	return ctx.BodyHeight-head-1-2 >= need
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

// Open maps a section to the screen it pushes. Each section owns one screen
// and pushes it once; the flows inside a section are states of that screen,
// not further router entries. It is exported so the tab bar can open a
// section through exactly the path the menu uses.
func (m Model) Open(id string) tea.Cmd {
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
	case SectionShare:
		return uictx.Push(share.New())
	default:
		return nil
	}
}
