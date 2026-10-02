// Package header draws the bar at the top of every screen: the app-name badge,
// version and breadcrumb on the first row with the machine's vital signs as
// small labelled pills at its right edge, the section tabs two rows below it
// (one blank row between, so the header breathes), and a rule under both.
//
// The tab row only exists inside a section. The home screen is itself the
// list of sections, so a tab bar above it would say everything twice; there
// the header is the badge row and the rule, and the menu gets the row back.
// Under the open tab the rule turns into a short accent underline, which is
// what makes the bar read as tabs rather than as a row of words.
//
// Nothing here runs at startup. The version is a link-time constant; the
// toolchain versions, the disk figure and the update notice arrive later as
// messages produced by commands, so the first frame is drawn without a single
// syscall.
package header

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/version"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// Pending is what an undetected value reads as until its detector reports in.
// The ascii tier shows [PendingASCII] instead, because a terminal that cannot
// draw a box-drawing rule cannot draw an ellipsis either.
const (
	Pending      = "…"
	PendingASCII = "..."
)

// Rows is how many lines the header occupies with its tab bar: the badge
// row, one blank row of air, the tab row and the rule under them.
// RowsCompact is the header without tabs, on the home and first-run
// screens.
const (
	Rows        = 4
	RowsCompact = 2
)

// TabRow is the terminal row the tabs are drawn on, for hit-testing clicks.
// It sits under the blank row that separates the tabs from the badge row, so
// a click on that gap opens nothing.
const TabRow = 2

// tabPad is the blank column before the first tab; tabGap is the air between
// two tabs. Two columns keep neighbouring labels from reading as one phrase.
const (
	tabPad = 1
	tabGap = 2
)

// DiskMsg carries the result of the free-space probe.
type DiskMsg struct {
	// Space is the free and total bytes on the system drive.
	Space winapi.DiskSpace
	// Err is set when the probe failed; the header then hides the figure.
	Err error
}

// ToolVersionsMsg carries lazily detected toolchain versions. Milestone 1
// fills it in; until then the header shows Pending.
type ToolVersionsMsg struct {
	// Node is the Node.js version, e.g. "22.3.0".
	Node string
	// Git is the git version, e.g. "2.45.1".
	Git string
}

// UpdateMsg says a newer Devpit is published. The header answers with a pill
// that stays for the rest of the session.
type UpdateMsg struct {
	// Version is the newer version, without a leading "v".
	Version string
}

// Tab is one entry of the section bar.
type Tab struct {
	// ID identifies the section, matching the home screen's identifiers.
	ID string
	// Label is the short name drawn on the bar.
	Label string
}

// Model is the header component.
type Model struct {
	// Title is the current screen name, shown after the app name.
	Title string
	// Tabs are the sections drawn on the second row, in order.
	Tabs []Tab
	// Active is the ID of the open section, or "" on the home screen.
	Active string
	// ShowTabs draws the tab row. The root model turns it on inside a
	// section and off on home, where the menu already is the list of tabs.
	ShowTabs bool

	node   string
	git    string
	disk   winapi.DiskSpace
	have   bool
	update string
}

// New returns a header with every detected value still pending.
func New() Model {
	return Model{node: Pending, git: Pending}
}

// DetectDiskCmd probes free space on the system drive. Bubble Tea runs it on
// its own goroutine, so the syscall never blocks the first frame.
func DetectDiskCmd() tea.Cmd {
	return func() tea.Msg {
		space, err := winapi.FreeSpace(winapi.SystemDrive())
		return DiskMsg{Space: space, Err: err}
	}
}

// Update folds the detector results into the header.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case DiskMsg:
		if msg.Err == nil {
			m.disk = msg.Space
			m.have = true
		}
	case ToolVersionsMsg:
		if msg.Node != "" {
			m.node = msg.Node
		}
		if msg.Git != "" {
			m.git = msg.Git
		}
	case UpdateMsg:
		m.update = strings.TrimPrefix(strings.TrimSpace(msg.Version), "v")
	}
	return m, nil
}

// Height is the number of rows the header occupies, including its rule.
func (m Model) Height() int {
	if m.tabsShown() {
		return Rows
	}
	return RowsCompact
}

// tabsShown reports whether the tab row is drawn this frame.
func (m Model) tabsShown() bool { return m.ShowTabs && len(m.Tabs) > 0 }

// UpdateAvailable is the newer version the header was told about, or "".
func (m Model) UpdateAvailable() string { return m.update }

// View renders the header to exactly ctx.Width columns.
func (m Model) View(ctx uictx.Context) string {
	th := ctx.Theme
	ascii := ctx.Icons.Tier == icons.TierASCII

	var name strings.Builder
	if e := ctx.Emoji(icons.EmojiFlag); e != "" {
		name.WriteString(e)
		name.WriteByte(' ')
	}
	name.WriteString(th.Badge.Render(" DEVPIT "))
	name.WriteByte(' ')
	name.WriteString(th.Muted.Render(VersionLabel(version.Short())))
	if m.Title != "" {
		name.WriteString(th.Muted.Render(separator(ascii)))
		name.WriteString(th.Subtitle.Render(m.Title))
	}

	badgeRow := fit(ctx.Width, name.String(), m.pills(th, ascii), ascii)
	if !m.tabsShown() {
		return badgeRow + "\n" + m.rule(th, ctx.Width, ascii)
	}
	tabRow := fit(ctx.Width, m.tabs(ctx), th.Muted.Render(tabHint(len(m.Tabs), ascii)), ascii)
	return badgeRow + "\n\n" + tabRow + "\n" + m.rule(th, ctx.Width, ascii)
}

// tabs draws the section bar. The open section sits on the selection band in
// the accent, with the section's own icon beside it when the tier has one;
// the rest are muted so the bar never competes with the screen under it.
func (m Model) tabs(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", tabPad))
	for i, t := range m.Tabs {
		if i > 0 {
			b.WriteString(strings.Repeat(" ", tabGap))
		}
		label := " " + t.Label + " "
		if t.ID == m.Active {
			b.WriteString(th.TabActive.Render(label))
		} else {
			b.WriteString(th.TabIdle.Render(label))
		}
	}
	return b.String()
}

// rule is the line under the header. Under the open tab it turns into a
// heavy accent stroke as wide as the tab, the underline that makes the bar
// read as tabs.
func (m Model) rule(th *theme.Theme, width int, ascii bool) string {
	width = max(0, width)
	from, w, ok := m.activeSpan()
	if !m.tabsShown() || !ok || from+w > width {
		return th.Rule.Render(strings.Repeat(ruleRune(ascii), width))
	}
	return th.Rule.Render(strings.Repeat(ruleRune(ascii), from)) +
		th.Accent.Render(strings.Repeat(underlineRune(ascii), w)) +
		th.Rule.Render(strings.Repeat(ruleRune(ascii), width-from-w))
}

// activeSpan is the first column and the width of the open tab, walking the
// same widths tabs draws.
func (m Model) activeSpan() (from, width int, ok bool) {
	col := tabPad
	for i, t := range m.Tabs {
		if i > 0 {
			col += tabGap
		}
		w := ansi.StringWidth(t.Label) + 2
		if t.ID == m.Active {
			return col, w, true
		}
		col += w
	}
	return 0, 0, false
}

// TabAt returns the tab drawn under column x of the tab row, and whether
// there is one. It walks the same widths tabs draws, so a click lands on the
// label the user saw.
func (m Model) TabAt(x int) (Tab, bool) {
	if !m.tabsShown() {
		return Tab{}, false
	}
	col := tabPad
	for i, t := range m.Tabs {
		if i > 0 {
			col += tabGap
		}
		w := ansi.StringWidth(t.Label) + 2
		if x >= col && x < col+w {
			return t, true
		}
		col += w
	}
	return Tab{}, false
}

// Next returns the tab after (step=1) or before (step=-1) the active one,
// wrapping around, and false when there are no tabs. With no active section
// it starts from the first or last tab.
func (m Model) Next(step int) (Tab, bool) {
	n := len(m.Tabs)
	if n == 0 {
		return Tab{}, false
	}
	cur := -1
	for i, t := range m.Tabs {
		if t.ID == m.Active {
			cur = i
			break
		}
	}
	if cur < 0 {
		if step < 0 {
			return m.Tabs[n-1], true
		}
		return m.Tabs[0], true
	}
	return m.Tabs[((cur+step)%n+n)%n], true
}

// tabHint is the muted reminder at the right of the tab row. The digit range
// follows the tabs, so adding or dropping a section cannot leave it stale.
func tabHint(n int, ascii bool) string {
	if ascii {
		if n < 2 {
			return "tab <-> "
		}
		return "tab <-> - 1-" + strconv.Itoa(n) + " "
	}
	if n < 2 {
		return "tab ⇄ "
	}
	return "tab ⇄ · 1–" + strconv.Itoa(n) + " "
}

// pills draws the machine's vital signs: the toolchain versions Devpit found,
// the free space it is there to win back, and the update notice once one
// has arrived.
func (m Model) pills(th *theme.Theme, ascii bool) string {
	var b strings.Builder
	if m.update != "" {
		b.WriteString(pill(th, "update", "v"+m.update, th.PillGood))
		b.WriteByte(' ')
	}
	b.WriteString(pill(th, "node", value(m.node, ascii), th.PillValue))
	b.WriteByte(' ')
	b.WriteString(pill(th, "git", value(m.git, ascii), th.PillValue))
	if m.have {
		b.WriteByte(' ')
		b.WriteString(pill(th, "free", FormatBytes(m.disk.FreeBytes), th.PillGood))
	}
	return b.String()
}

// pill renders one labelled chip. The label and the value carry the same
// background, so the two read as one object, and both survive NO_COLOR as
// plain " label value " text.
func pill(th *theme.Theme, label, val string, valStyle lipgloss.Style) string {
	return th.PillLabel.Render(" "+label+" ") + valStyle.Render(val+" ")
}

// value swaps the pending ellipsis for its ascii spelling.
func value(v string, ascii bool) string {
	if ascii && v == Pending {
		return PendingASCII
	}
	return v
}

// ruleRune is the line under the header: box drawing where it is available, a
// hyphen where it is not.
func ruleRune(ascii bool) string {
	if ascii {
		return "-"
	}
	return "─"
}

// underlineRune is the stroke under the open tab.
func underlineRune(ascii bool) string {
	if ascii {
		return "="
	}
	return "━"
}

// separator divides the version from the breadcrumb after it.
func separator(ascii bool) string {
	if ascii {
		return "  -  "
	}
	return "  ·  "
}

// ellipsis is the mark a cut line ends with.
func ellipsis(ascii bool) string {
	if ascii {
		return PendingASCII
	}
	return Pending
}

// fit places left and right on one row of the given width, padding between
// them and dropping the right side first when there is not enough room.
func fit(width int, left, right string, ascii bool) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	if width <= 0 {
		return left
	}
	if lw+rw+1 > width {
		if lw >= width {
			return ansiTruncate(left, width, ellipsis(ascii))
		}
		return left
	}
	return left + strings.Repeat(" ", width-lw-rw) + right
}

// ansiTruncate cuts a styled string to at most width cells, keeping the escape
// sequences intact. It is a function rather than a MaxWidth style so nothing
// builds a lipgloss.Style during a render.
func ansiTruncate(s string, width int, tail string) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, tail)
}

// FormatBytes renders a byte count the way the UI shows sizes: three
// significant figures and a binary unit, e.g. "1.2 GB", "980 MB".
func FormatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	val := float64(b) / float64(div)
	suffix := [...]string{"KB", "MB", "GB", "TB", "PB"}[exp]
	if val >= 100 {
		return fmt.Sprintf("%.0f %s", val, suffix)
	}
	return fmt.Sprintf("%.1f %s", val, suffix)
}

// VersionLabel is how a version is shown: "v0.4.0" for a release, and "dev"
// for a development build, including one linked with an empty version, which
// would otherwise read as a lone "v".
func VersionLabel(v string) string {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" || v == "dev" {
		return "dev"
	}
	return "v" + v
}
