package whatsnew

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// DoneMsg says the card was dismissed at startup. The root model records the
// version and shows home, and with OpenSkill also opens Settings › AI agent
// skill. Nothing is ever installed from the card itself.
type DoneMsg struct {
	// OpenSkill asks for the AI agent skill screen.
	OpenSkill bool
}

// OfferMsg tells the card whether to offer the AI agent skill: Claude Code is
// on this PC and Devpit's skill is missing there (Older false) or older than
// this Devpit's (Older true). The root model sends it once its look at the
// skill, which runs off the first frame, comes back.
type OfferMsg struct {
	// Show is true when the offer belongs on the card.
	Show bool
	// Older is true when a skill is there but out of date.
	Older bool
}

// Model is the card.
type Model struct {
	entry   Entry
	version string
	// reopened is the card opened again from Settings › About: Enter goes
	// back there and nothing is recorded.
	reopened bool
	offer    OfferMsg

	cont, skill key.Binding
}

// New is the card shown at startup for the running version.
func New(running string, e Entry) Model {
	return Model{
		entry:   e,
		version: strings.TrimPrefix(running, "v"),
		cont:    key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "continue")),
		skill:   key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "AI agent skill")),
	}
}

// Reopened is the card opened from Settings › About. It never offers the
// skill (the person is already in Settings) and Enter simply goes back.
func Reopened(running string, e Entry) Model {
	m := New(running, e)
	m.reopened = true
	m.cont = key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "back"))
	return m
}

// Init implements uictx.Screen.
func (m Model) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (m Model) Title() string { return "What's new" }

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	if m.offer.Show && !m.reopened {
		return []key.Binding{m.cont, m.skill}
	}
	return []key.Binding{m.cont}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case OfferMsg:
		if !m.reopened {
			m.offer = msg
		}
		return m, nil
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.cont):
			return m, m.done(false)
		case msg.String() == "esc":
			// Esc reaches the card only when it is the root screen (the
			// router pops it otherwise): at startup it continues, the same
			// as Enter, so the card is never shown again for this version.
			return m, m.done(false)
		case key.Matches(msg, m.skill) && m.offer.Show && !m.reopened:
			return m, m.done(true)
		}
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		row := ctx.BodyRow(msg.Y) - m.topPad(ctx)
		_, at := m.layout(inner(ctx))
		switch {
		case row == at.button && at.button >= 0:
			return m, m.done(false)
		case at.offer >= 0 && row >= at.offer && row <= at.offerEnd:
			return m, m.done(true)
		}
	}
	return m, nil
}

// done ends the card: back to Settings when it was reopened, otherwise the
// message the root model records the version on.
func (m Model) done(openSkill bool) tea.Cmd {
	if m.reopened {
		return uictx.Pop()
	}
	return func() tea.Msg { return DoneMsg{OpenSkill: openSkill} }
}

// contentMax caps the card's width so its lines stay a readable length.
const contentMax = 84

// inner is ctx narrowed to the card's width.
func inner(ctx uictx.Context) uictx.Context {
	ctx.Width = min(ctx.Width, contentMax)
	return ctx
}

// spots are the rows a click can land on, counted from the card's top.
type spots struct {
	offer, offerEnd, button int
}

// layout builds the card's lines at ctx's width, giving up two blank lines
// when the terminal is too short for all of them.
func (m Model) layout(ctx uictx.Context) ([]string, spots) {
	lines, at := m.build(ctx, false)
	if ctx.BodyHeight > 0 && len(lines) > ctx.BodyHeight {
		return m.build(ctx, true)
	}
	return lines, at
}

// build lays the card out, compact or with all its air.
func (m Model) build(ctx uictx.Context, compact bool) ([]string, spots) {
	th := ctx.Theme
	ascii := ctx.Icons.Tier == icons.TierASCII
	at := spots{offer: -1, button: -1}
	var lines []string
	add := func(s ...string) { lines = append(lines, s...) }
	para := func(st lipgloss.Style, indent int, text string) {
		w := max(20, ctx.Width-indent-1)
		for _, l := range strings.Split(ansi.Wrap(plain(text, ascii), w, " "), "\n") {
			add(strings.Repeat(" ", indent) + st.Render(strings.TrimRight(l, " ")))
		}
	}

	add(" " + th.Title.Render("What's new in Devpit "+m.entry.Version))
	if !compact {
		add("")
	}
	add(" " + th.Subtitle.Render(plain(m.entry.Headline, ascii)))
	for _, l := range m.entry.Lines {
		para(th.Base, 1, l)
	}

	if len(m.entry.Moves) > 0 {
		add("", " "+th.Subtitle.Render("Where things moved"))
		fromW := 0
		for _, mv := range m.entry.Moves {
			fromW = max(fromW, ansi.StringWidth(plain(mv.From, ascii)))
		}
		arrow := "→"
		if ascii {
			arrow = "->"
		}
		for _, mv := range m.entry.Moves {
			from := plain(mv.From, ascii)
			to := plain(mv.To, ascii)
			room := ctx.Width - 3 - fromW - 4 - ansi.StringWidth(arrow) - 1
			add("   " + th.Muted.Render(from+strings.Repeat(" ", fromW-ansi.StringWidth(from))) +
				"  " + th.Accent.Render(arrow) + "  " + th.Base.Render(ansi.Truncate(to, max(8, room), ellipsis(ascii))))
		}
	}

	if m.offer.Show && !m.reopened {
		add("")
		at.offer = len(lines)
		text := "Claude Code is on this PC. Devpit has a skill that lets it check which account a folder uses and explain why, and it asks you before any change."
		if m.offer.Older {
			text = "The Devpit skill in Claude Code is older than this Devpit. The new one also knows where Devpit's help pages are."
		}
		add(" " + th.Accent.Render(dot(ascii)) + " " + th.Subtitle.Render("For AI agents"))
		para(th.Base, 3, text)
		add("   " + ctx.KeyHint("a", "open the AI agent skill screen") + th.Muted.Render("  nothing is installed until you say yes"))
		at.offerEnd = len(lines) - 1
	}

	if !compact {
		add("")
	}
	add(" "+th.Muted.Render("Everything that changed: ")+th.Info.Render(PageURL), "")
	at.button = len(lines)
	label := "Continue"
	if m.reopened {
		label = "Back to Settings"
	}
	arrow := "→"
	if ascii {
		arrow = "->"
	}
	add(" " + th.TabActive.Render(" "+label+" "+arrow+" "))
	return lines, at
}

// ellipsis is the tier's cut mark.
func ellipsis(ascii bool) string {
	if ascii {
		return "..."
	}
	return "…"
}

// dot is the small mark before the offer's heading.
func dot(ascii bool) string {
	if ascii {
		return "*"
	}
	return "◆"
}

// plain swaps the two typographic glyphs the table uses for their ASCII
// spelling on a terminal that only gets the ascii tier.
func plain(s string, ascii bool) string {
	if !ascii {
		return s
	}
	return strings.NewReplacer("›", ">", "–", "-", "→", "->").Replace(s)
}

// topPad is the rows View adds above the card so it sits a third of the way
// down rather than hanging from the header.
func (m Model) topPad(ctx uictx.Context) int {
	lines, _ := m.layout(inner(ctx))
	if ctx.BodyHeight <= 0 {
		return 0
	}
	return max(0, (ctx.BodyHeight-len(lines))/3)
}

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	lines, _ := m.layout(inner(ctx))
	out := strings.Join(lines, "\n")
	if ctx.Width > contentMax+4 {
		block := lipgloss.NewStyle().Width(contentMax).Render(out)
		out = lipgloss.PlaceHorizontal(ctx.Width, lipgloss.Center, block)
	}
	if pad := m.topPad(ctx); pad > 0 {
		out = strings.Repeat("\n", pad) + out
	}
	return out
}
