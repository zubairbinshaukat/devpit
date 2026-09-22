// Package header draws the bar at the top of every screen: the app-name badge
// and version on the first row, the machine's vital signs as small labelled
// pills on the second, and a rule under both.
//
// Nothing here runs at startup. The version is a link-time constant; the
// toolchain versions and the disk figure arrive later as messages produced by
// commands, so the first frame is drawn without a single syscall.
package header

import (
	"fmt"
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

// Rows is how many lines the header occupies: the badge row, the pill row and
// the rule under them.
const Rows = 3

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

// Model is the header component.
type Model struct {
	// Title is the current screen name, shown after the app name.
	Title string

	node string
	git  string
	disk winapi.DiskSpace
	have bool
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
	}
	return m, nil
}

// Height is the number of rows the header occupies, including its rule.
func (m Model) Height() int { return Rows }

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
	name.WriteString(th.Muted.Render("v" + version.Short()))
	if m.Title != "" {
		name.WriteString(th.Muted.Render(separator(ascii)))
		name.WriteString(th.Subtitle.Render(m.Title))
	}

	badgeRow := fit(ctx.Width, name.String(), "", ascii)
	pillRow := fit(ctx.Width, "", m.pills(th, ascii), ascii)
	rule := th.Rule.Render(strings.Repeat(ruleRune(ascii), max(0, ctx.Width)))

	return badgeRow + "\n" + pillRow + "\n" + rule
}

// pills draws the machine's vital signs: the toolchain versions Devpit found
// and the free space it is there to win back.
func (m Model) pills(th *theme.Theme, ascii bool) string {
	var b strings.Builder
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
// them and truncating the right side first when there is not enough room.
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
