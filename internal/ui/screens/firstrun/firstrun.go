// Package firstrun is what a new user sees before the main menu: a short
// wizard, one question per step, instead of a page of settings. A stepper at
// the top ("● ━━ ○ ━━ ○ ━━ ○  Step 1 of 4 · Welcome") says where they are.
//
//  1. Welcome: what Devpit is and the three promises it keeps.
//  2. Icons: a sample of the icon glyphs and "do these look like icons?".
//     When the installer already put the icon font in (config
//     font_installed) the answer starts on Yes; the user still sees the
//     sample and can say No. The step is skipped outright on a terminal
//     that only gets the ascii tier, where the question has no good answer.
//  3. Theme: every theme with a swatch of its colours. Moving through the
//     list repaints the screen in that theme, so the choice is made by
//     looking rather than by guessing what "rose" means.
//  4. Privacy: usage stats, off unless the user turns them on.
//
// Enter goes forward and Esc goes back; nothing is saved until the last
// step, so quitting halfway leaves the wizard to run again next time.
//
// It is hand-rolled rather than built with huh: the glyph probe has to
// render raw glyphs from every tier at a fixed width, and the whole screen
// has to work while Devpit's own icon resolution is still undecided, which
// is exactly the case a form library abstracts away.
package firstrun

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/checklist"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/logo"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// TelemetryQuestion is the exact wording from the PRD. It is a constant so
// the PRIVACY.md text and this screen can never drift apart.
const TelemetryQuestion = "Help show how much space Devpit saves? Only totals are sent — no file names or paths."

// ProbePrompt is the glyph probe question.
const ProbePrompt = "Do all of these render?"

// SafetyBullets are the three promises the screen makes, in order.
var SafetyBullets = []string{
	"Nothing is deleted without a preview and an explicit confirmation.",
	"The default answer on every confirmation is No.",
	"Nothing leaves this machine unless you turn usage stats on.",
}

// step is one page of the wizard.
type step int

const (
	stepWelcome step = iota
	stepIcons
	stepTheme
	stepPrivacy
)

// stepNames label the stepper, in order.
var stepNames = []string{"Welcome", "Icons", "Theme", "Privacy"}

// themeBlurbs say what each theme is, beside its swatch.
var themeBlurbs = map[string]string{
	config.ThemeAuto:  "follows your terminal's background",
	config.ThemeDark:  "Devpit aqua on a dark background",
	config.ThemeLight: "Devpit aqua on a light background",
	config.ThemeAqua:  "the house colour",
	config.ThemeBlue:  "a calm blue accent",
	config.ThemeRose:  "a soft rose accent",
	config.ThemeMono:  "no colour at all, shapes and words only",
}

// DoneMsg is emitted when the user finishes the wizard. The app saves the
// config, rebuilds the theme and icon set from it, and swaps in the home
// screen.
type DoneMsg struct {
	// Config is the configuration to persist. FirstRunDone is already set.
	Config config.Config
}

// Model is the first-run wizard.
type Model struct {
	step step

	glyphsOK  bool
	theme     string
	telemetry bool

	// cursor is the highlighted option on the current step.
	cursor int

	keys keyMap
}

// keyMap is the wizard's own key bindings.
type keyMap struct {
	Next key.Binding
	Back key.Binding
	Up   key.Binding
	Down key.Binding
	Yes  key.Binding
	No   key.Binding
}

// New returns the wizard seeded from the current config. Usage stats start
// off, as the PRD requires. The glyph answer starts on Yes only when Devpit
// installed the icon font itself; otherwise it starts on No, so a terminal
// that cannot draw the probe never claims it can.
func New(cfg config.Config) Model {
	th := cfg.Theme
	if th == "" {
		th = config.ThemeAuto
	}
	return Model{
		glyphsOK:  cfg.FontInstalled,
		theme:     th,
		telemetry: false,
		keys: keyMap{
			Next: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next")),
			Back: key.NewBinding(key.WithKeys("esc", "backspace"), key.WithHelp("esc", "back")),
			Up:   key.NewBinding(key.WithKeys("up", "k", "left", "h"), key.WithHelp("↑↓", "choose")),
			Down: key.NewBinding(key.WithKeys("down", "j", "right", "l", "tab")),
			Yes:  key.NewBinding(key.WithKeys("y"), key.WithHelp("y/n", "yes/no")),
			No:   key.NewBinding(key.WithKeys("n")),
		},
	}
}

// Init implements uictx.Screen.
func (m Model) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (m Model) Title() string { return "Welcome" }

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	if m.step == stepWelcome {
		return []key.Binding{m.nextHelp()}
	}
	return []key.Binding{m.keys.Up, m.nextHelp(), m.keys.Back}
}

// nextHelp is the Enter hint, which says "finish" on the last step.
func (m Model) nextHelp() key.Binding {
	if m.step == stepPrivacy {
		return key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "start using Devpit"))
	}
	return m.keys.Next
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.keys.Up, m.keys.Next, m.keys.Back}, {m.keys.Yes}}
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.onKey(msg, ctx)
	case tea.MouseClickMsg:
		if msg.Button != tea.MouseLeft {
			return m, nil
		}
		return m.onClick(ctx, ctx.BodyRow(msg.Y))
	case tea.MouseMotionMsg:
		if i, ok := m.optionAt(ctx, ctx.BodyRow(msg.Y)); ok && i != m.cursor {
			m.cursor = i
			m = m.pick(ctx)
			return m, m.preview(ctx)
		}
	}
	return m, nil
}

// onKey moves through the options and the steps.
func (m Model) onKey(km tea.KeyPressMsg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	n := len(m.options(ctx))
	switch {
	case key.Matches(km, m.keys.Next):
		return m.next(ctx)
	case key.Matches(km, m.keys.Back):
		return m.back(ctx), nil
	case key.Matches(km, m.keys.Up) && n > 0:
		m.cursor = (m.cursor - 1 + n) % n
	case key.Matches(km, m.keys.Down) && n > 0:
		m.cursor = (m.cursor + 1) % n
	case key.Matches(km, m.keys.Yes) && m.step == stepIcons:
		m.cursor = 0
	case key.Matches(km, m.keys.Yes) && m.step == stepPrivacy:
		m.cursor = 1
	case key.Matches(km, m.keys.No) && m.step == stepIcons:
		m.cursor = 1
	case key.Matches(km, m.keys.No) && m.step == stepPrivacy:
		m.cursor = 0
	default:
		return m, nil
	}
	m = m.pick(ctx)
	return m, m.preview(ctx)
}

// preview repaints the whole app in the theme being considered, without
// saving it, so the user sees their choice before they make it. It is nil
// on every other step.
func (m Model) preview(ctx uictx.Context) tea.Cmd {
	if m.step != stepTheme {
		return nil
	}
	cfg := ctx.Config
	cfg.Theme = m.theme
	return func() tea.Msg { return uictx.ConfigChangedMsg{Config: cfg, Persist: false} }
}

// pick copies the highlighted option into the answer for the current step.
func (m Model) pick(ctx uictx.Context) Model {
	switch m.step {
	case stepIcons:
		m.glyphsOK = m.cursor == 0
	case stepTheme:
		if m.cursor >= 0 && m.cursor < len(config.Themes) {
			m.theme = config.Themes[m.cursor]
		}
	case stepPrivacy:
		m.telemetry = m.cursor == 1
	}
	return m
}

// next moves one step on, or finishes on the last one.
func (m Model) next(ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	m = m.pick(ctx)
	if m.step == stepPrivacy {
		return m, m.finish(ctx.Config)
	}
	m.step++
	if m.step == stepIcons && skipIcons(ctx) {
		m.step++
	}
	m.cursor = m.cursorFor(m.step)
	return m, nil
}

// back moves one step back; on the first step there is nowhere to go.
func (m Model) back(ctx uictx.Context) Model {
	if m.step == stepWelcome {
		return m
	}
	m = m.pick(ctx)
	m.step--
	if m.step == stepIcons && skipIcons(ctx) {
		m.step--
	}
	m.cursor = m.cursorFor(m.step)
	return m
}

// cursorFor puts the cursor on the current answer of a step.
func (m Model) cursorFor(s step) int {
	switch s {
	case stepIcons:
		if m.glyphsOK {
			return 0
		}
		return 1
	case stepTheme:
		for i, t := range config.Themes {
			if t == m.theme {
				return i
			}
		}
	case stepPrivacy:
		if m.telemetry {
			return 1
		}
	}
	return 0
}

// skipIcons reports whether the icons step has nothing to ask: a terminal
// that only gets the ascii tier cannot show the probe at all.
func skipIcons(ctx uictx.Context) bool { return ctx.Icons.Tier == icons.TierASCII }

// finish builds the command that persists the answers.
func (m Model) finish(cfg config.Config) tea.Cmd {
	cfg.Theme = m.theme
	cfg.TelemetryOptIn = m.telemetry
	cfg.FirstRunDone = true
	// The answer is recorded as a fact about this terminal, not as a forced
	// tier, so the auto rule (and a later font install) can still reason
	// about it.
	cfg.GlyphsConfirmed = m.glyphsOK
	cfg.Icons = config.IconsAuto
	cfg.Normalize()
	return func() tea.Msg { return DoneMsg{Config: cfg} }
}

// option is one choice on a step.
type option struct {
	label string
	aside string
	// swatch is drawn before the label on the theme step.
	swatch string
}

// options lists the choices of the current step.
func (m Model) options(ctx uictx.Context) []option {
	switch m.step {
	case stepIcons:
		return []option{
			{label: "Yes, they look like icons", aside: "turns on the richer icon set"},
			{label: "No, I see boxes or question marks", aside: "keeps the plain shapes"},
		}
	case stepTheme:
		out := make([]option, len(config.Themes))
		for i, t := range config.Themes {
			out[i] = option{label: t, aside: themeBlurbs[t], swatch: swatch(t, ctx)}
		}
		return out
	case stepPrivacy:
		return []option{
			{label: "No, keep everything on this machine"},
			{label: "Yes, send anonymous totals", aside: "freed space and counts, nothing else"},
		}
	}
	return nil
}

// swatch is three squares in a theme's accent, success and info colours.
func swatch(pref string, ctx uictx.Context) string {
	th := theme.Resolve(pref, ctx.Theme.IsDark)
	sq := "■"
	if ctx.Icons.Tier == icons.TierASCII {
		sq = "#"
	}
	return th.Accent.Render(sq) + th.Success.Render(sq) + th.Info.Render(sq)
}

// ---- layout ---------------------------------------------------------------

// layout builds the screen's lines and says which line the first option is
// on, so rendering and clicks read the same geometry.
func (m Model) layout(ctx uictx.Context) (lines []string, optTop int) {
	th := ctx.Theme
	add := func(s ...string) { lines = append(lines, s...) }
	// para adds prose wrapped to the wizard's width here, not later, so the
	// line count the click path reads is the line count that is drawn.
	para := func(style lipgloss.Style, text string) {
		wrapped := text
		if ctx.Width > 2 {
			wrapped = lipgloss.Wrap(text, ctx.Width-2, "")
		}
		for _, l := range strings.Split(wrapped, "\n") {
			add(" " + style.Render(l))
		}
	}

	add(m.stepper(ctx), "")

	switch m.step {
	case stepWelcome:
		if big := logo.Height(ctx.Icons.Tier == icons.TierASCII); ctx.BodyHeight >= big+14 && ctx.Width >= logo.Width(ctx.Icons.Tier == icons.TierASCII)+4 {
			for _, l := range strings.Split(th.Logo.Render(logo.String(ctx.Icons.Tier == icons.TierASCII)), "\n") {
				add(" " + l)
			}
			add("")
		}
		title := "Welcome to Devpit"
		if e := ctx.Emoji(icons.EmojiFlag); e != "" {
			title = e + " " + title
		}
		add(" "+th.Title.Render(title), "")
		para(th.Base, "A pit stop for your dev machine: free disk space, fix stuck ports, and keep your tools up to date, from one menu.")
		add("")
		for _, s := range SafetyBullets {
			add("  " + th.Success.Render(ctx.Icons.Tick) + " " + th.Base.Render(ansi.Truncate(s, max(10, ctx.Width-5), "…")))
		}
		add("", " "+button(ctx, "Let's go", true))
		return lines, -1

	case stepIcons:
		add(" "+th.Title.Render("Do these look like icons?"), "")
		add("      " + probe(ctx))
		add("")
		para(th.Muted, "If the last three are boxes or question marks, the icon font is not in this terminal yet.")
		if ctx.Config.FontInstalled {
			para(th.Muted, "The installer added it: a terminal opened before that still needs a restart.")
		} else {
			para(th.Muted, "You can install it any time from Settings › Icon font.")
		}
		add("")

	case stepTheme:
		add(" " + th.Title.Render("Pick a look"))
		para(th.Muted, "The screen changes as you move. Settings can change it later.")
		add("")

	case stepPrivacy:
		add(" " + th.Title.Render("One last thing"))
		add("")
		para(th.Base, TelemetryQuestion)
		para(th.Muted, "Off by default. Details are in PRIVACY.md.")
		add("")
	}

	optTop = len(lines)
	opts := m.options(ctx)
	for i, o := range opts {
		add(m.optionLine(ctx, o, i))
	}
	add("")
	if m.step == stepPrivacy {
		add(" " + button(ctx, "Start using Devpit", true))
	} else {
		add(" " + button(ctx, "Next", true))
	}
	return lines, optTop
}

// optionLine draws one choice as a radio row on the selection band.
func (m Model) optionLine(ctx uictx.Context, o option, i int) string {
	radio := "○"
	if ctx.Icons.Tier == icons.TierASCII {
		radio = "( )"
	}
	on := i == m.cursor
	if on {
		radio = "●"
		if ctx.Icons.Tier == icons.TierASCII {
			radio = "(*)"
		}
	}
	text := radio + " " + o.label
	labelW := 0
	for _, other := range m.options(ctx) {
		labelW = max(labelW, ansi.StringWidth(radio+" "+other.label))
	}
	if o.swatch != "" {
		// The swatch carries its own colours, so it follows the row rather
		// than sitting inside its band; the row is drawn at its natural
		// width so the swatch stays next to the words it illustrates.
		aside := o.aside + strings.Repeat(" ", max(0, asideWidth(m.options(ctx))-ansi.StringWidth(o.aside)))
		line := checklist.Render(ctx, checklist.Line{Selected: on, Box: checklist.None, Text: text, TextWidth: labelW, Aside: aside}, 0)
		return line + "  " + o.swatch
	}
	return checklist.Render(ctx, checklist.Line{Selected: on, Box: checklist.None, Text: text, TextWidth: labelW, Aside: o.aside}, min(ctx.Width, contentMax))
}

// asideWidth is the widest aside among opts, so swatches line up.
func asideWidth(opts []option) int {
	w := 0
	for _, o := range opts {
		w = max(w, ansi.StringWidth(o.aside))
	}
	return w
}

// contentMax caps the wizard's width, so its lines stay a readable length
// and the block can be centred on a wide terminal.
const contentMax = 84

// stepper is the progress line at the top: a dot per step, joined by
// strokes, the finished part in the accent.
func (m Model) stepper(ctx uictx.Context) string {
	th := ctx.Theme
	ascii := ctx.Icons.Tier == icons.TierASCII
	dotOn, dotOff, stroke := "●", "○", "━━"
	if ascii {
		dotOn, dotOff, stroke = "*", "o", "--"
	}
	var b strings.Builder
	b.WriteString(" ")
	for i := range stepNames {
		if i > 0 {
			if step(i) <= m.step {
				b.WriteString(th.Accent.Render(" " + stroke + " "))
			} else {
				b.WriteString(th.Rule.Render(" " + stroke + " "))
			}
		}
		if step(i) <= m.step {
			b.WriteString(th.Accent.Render(dotOn))
		} else {
			b.WriteString(th.Muted.Render(dotOff))
		}
	}
	shown, total := int(m.step)+1, len(stepNames)
	if skipIcons(ctx) {
		total--
		if m.step > stepIcons {
			shown--
		}
	}
	b.WriteString(th.Muted.Render("     Step ") + th.Base.Render(strconv.Itoa(shown)) + th.Muted.Render(" of "+strconv.Itoa(total)+" · ") +
		th.Subtitle.Render(stepNames[m.step]))
	return b.String()
}

// probe is the glyph sample, spaced out so each one can be judged: three
// shapes every terminal has, then three from the icon font.
func probe(ctx uictx.Context) string {
	th := ctx.Theme
	u := icons.Unicode()
	n := icons.Nerd()
	shapes := th.Base.Render(strings.Join([]string{u.Tick, u.Safe, u.Folder}, "   "))
	nerd := th.SectionIcon(theme.SectionClean).Render(n.Section(theme.SectionClean)) + "   " +
		th.SectionIcon(theme.SectionGitSSH).Render(n.Git) + "   " +
		th.SectionIcon(theme.SectionUpdate).Render(n.Node)
	return shapes + "   " + nerd
}

// ProbeLine is the glyph sample the user is asked to judge: the three unicode
// shapes plus two Nerd Font glyphs. If the last two show as boxes, the answer
// is No and Devpit stays out of the nerd tier.
func ProbeLine() string {
	u := icons.Unicode()
	n := icons.Nerd()
	return strings.Join([]string{u.Tick, u.Safe, u.Folder, n.Folder, n.Node}, " ")
}

// button draws a call to action: the label on the selection band, with an
// arrow, so Enter has something visible to press.
func button(ctx uictx.Context, label string, primary bool) string {
	th := ctx.Theme
	arrow := "→"
	if ctx.Icons.Tier == icons.TierASCII {
		arrow = "->"
	}
	s := th.TabIdle
	if primary {
		s = th.TabActive
	}
	return s.Render(" " + label + " " + arrow + " ")
}

// View implements uictx.Screen. The block sits a third of the way down and,
// on a terminal wider than it, in the middle, so the wizard reads as a
// dialog rather than as a log.
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

// inner is ctx narrowed to the wizard's own width, which is what the layout
// is built at; View, the centring pad and the click path all use it.
func inner(ctx uictx.Context) uictx.Context {
	ctx.Width = min(ctx.Width, contentMax)
	return ctx
}

// topPad is the rows View adds above the layout.
func (m Model) topPad(ctx uictx.Context) int {
	lines, _ := m.layout(inner(ctx))
	if ctx.BodyHeight <= 0 {
		return 0
	}
	return max(0, (ctx.BodyHeight-len(lines))/3)
}

// optionAt maps a body row to the option drawn there.
func (m Model) optionAt(ctx uictx.Context, row int) (int, bool) {
	lines, top := m.layout(inner(ctx))
	if top < 0 {
		return 0, false
	}
	i := row - m.topPad(ctx) - top
	n := len(m.options(ctx))
	if i < 0 || i >= n || top+i >= len(lines) {
		return 0, false
	}
	return i, true
}

// onClick picks an option, or presses the button under the pointer.
func (m Model) onClick(ctx uictx.Context, row int) (uictx.Screen, tea.Cmd) {
	if i, ok := m.optionAt(ctx, row); ok {
		m.cursor = i
		m = m.pick(ctx)
		return m, m.preview(ctx)
	}
	lines, _ := m.layout(inner(ctx))
	if row-m.topPad(ctx) == len(lines)-1 {
		return m.next(ctx)
	}
	return m, nil
}
