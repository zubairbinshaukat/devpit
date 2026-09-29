// Package activity draws a run of long jobs as one live row per job: a
// spinner, the job's name, a bar or the latest thing it said, and how long it
// has taken, turning into a tick, a warning or a cross when it finishes.
//
// It replaces the scrolling transcript the Update and Install screens used to
// show. A package manager prints dozens of lines per package, and a wall of
// them tells the user less than one line per package does: which one is
// running, how far it has got, and how each of the others went. The raw
// output is still kept by the screens, behind a key, for the day something
// fails and the detail matters.
//
// Everything here is a pure render function over plain values. The screens
// own the state and the tick that advances the spinner, so the component has
// nothing to update and nothing to get out of step.
package activity

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// State is where one job is in its run.
type State int

// The job states, in the order a job moves through them. Done, Warn, Failed
// and Skipped are all final.
const (
	// Queued is waiting its turn.
	Queued State = iota
	// Running is the job in flight.
	Running
	// Done finished cleanly.
	Done
	// Warn finished but wants something from the user, e.g. a restart.
	Warn
	// Failed did not finish.
	Failed
	// Skipped never ran: unticked, cancelled, or not applicable.
	Skipped
)

// Final reports whether a job in this state is finished, one way or another.
func (s State) Final() bool { return s >= Done }

// Row is one job.
type Row struct {
	// Label is the job's name, e.g. "Docker Desktop".
	Label string
	// Old and New are the versions an update moves between. Either may be
	// empty; a row with neither shows only its Detail.
	Old, New string
	// State is where the job is.
	State State
	// Percent is how far a running job has got, 0 to 100, or negative when
	// the job has not said. Most package managers do not say when their
	// output is piped, so an unknown percentage is normal, not an error.
	Percent float64
	// Detail is the one line the row shows beside the name: the latest
	// output of a running job, or the reason a job failed or was skipped.
	Detail string
	// Elapsed is how long the job ran, or has been running.
	Elapsed time.Duration
}

// labelMax caps the name column, so one long package name cannot push every
// other row's detail off the edge.
const labelMax = 30

// barWidth is the width of a running job's bar.
const barWidth = 16

// View draws the rows into width columns and at most height lines. When the
// run is taller than that, the window follows the first job that is not
// finished, so the row that is moving is always on screen, and says how many
// rows are hidden above and below it. frame advances the spinner.
func View(ctx uictx.Context, rows []Row, frame, width, height int) string {
	if len(rows) == 0 {
		return ""
	}
	labelW := 0
	for _, r := range rows {
		labelW = max(labelW, ansi.StringWidth(r.Label))
	}
	labelW = min(labelW, labelMax)

	start, end := window(rows, height)
	var lines []string
	if start > 0 {
		lines = append(lines, more(ctx, start, true))
	}
	for i := start; i < end; i++ {
		lines = append(lines, RowView(ctx, rows[i], frame, width, labelW))
	}
	if end < len(rows) {
		lines = append(lines, more(ctx, len(rows)-end, false))
	}
	return strings.Join(lines, "\n")
}

// window picks the slice of rows to draw in height lines, keeping the first
// unfinished row a couple of rows from the top so the user sees what just
// finished as well as what is next.
func window(rows []Row, height int) (start, end int) {
	n := len(rows)
	if height <= 0 || n <= height {
		return 0, n
	}
	avail := max(1, height-2) // room for both "n more" markers
	focus := n - 1
	for i, r := range rows {
		if !r.State.Final() {
			focus = i
			break
		}
	}
	start = max(0, focus-2)
	if start+avail > n {
		start = n - avail
	}
	return start, start + avail
}

// more is one "n more" marker.
func more(ctx uictx.Context, n int, above bool) string {
	arrow := "▼"
	if above {
		arrow = "▲"
	}
	if ctx.Icons.Tier == icons.TierASCII {
		arrow = "v"
		if above {
			arrow = "^"
		}
	}
	return ctx.Theme.Muted.Render("   " + arrow + " " + strconv.Itoa(n) + " more")
}

// RowView draws one job: its mark, its name padded to labelW, what it is
// doing or how it went, and its time at the right edge.
func RowView(ctx uictx.Context, r Row, frame, width, labelW int) string {
	th := ctx.Theme
	mark, markStyle := stateMark(ctx, r.State, frame)

	label := r.Label
	if ansi.StringWidth(label) > labelW {
		label = ansi.Truncate(label, labelW, ellipsis(ctx))
	}
	label += strings.Repeat(" ", labelW-ansi.StringWidth(label))

	right := ""
	if r.State != Queued && r.State != Skipped && r.Elapsed > 0 {
		right = Duration(r.Elapsed)
	} else if r.State == Queued {
		right = "queued"
	}

	// " ⠹ " + label + "  " + middle + gap + right
	fixed := 1 + 1 + 1 + labelW + 2
	room := width - fixed - ansi.StringWidth(right) - 2
	if width <= 0 {
		room = 1 << 20
	}
	middle := middleOf(ctx, r, max(0, room))

	labelStyle := th.Base
	switch r.State {
	case Queued, Skipped:
		labelStyle = th.Muted
	case Running:
		labelStyle = th.Subtitle
	}

	line := " " + markStyle.Render(mark) + " " + labelStyle.Render(label) + "  " + middle
	if right != "" && width > 0 {
		pad := width - ansi.StringWidth(line) - ansi.StringWidth(right) - 1
		if pad >= 1 {
			line += strings.Repeat(" ", pad) + th.Muted.Render(right)
		}
	}
	return line
}

// stateMark is the one-cell glyph at the left of a row, and its colour.
func stateMark(ctx uictx.Context, s State, frame int) (string, lipgloss.Style) {
	th := ctx.Theme
	ic := ctx.Icons
	switch s {
	case Running:
		return ctx.SpinnerFrame(frame), th.Accent
	case Done:
		return ic.Tick, th.Success
	case Warn:
		return ic.Warn, th.Warning
	case Failed:
		return ic.Fail, th.Danger
	case Skipped:
		return ic.Queued, th.Muted
	default:
		return ic.Queued, th.Muted
	}
}

// middleOf is the part of a row between the name and the time: a bar and
// the latest line for a running job, the version move for a finished one,
// the reason for one that failed or was skipped.
func middleOf(ctx uictx.Context, r Row, room int) string {
	th := ctx.Theme
	cut := func(s string, w int) string {
		if w <= 0 {
			return ""
		}
		if ansi.StringWidth(s) > w {
			return ansi.Truncate(s, w, ellipsis(ctx))
		}
		return s
	}
	versions := func(dim bool) (string, int) {
		if r.Old == "" && r.New == "" {
			return "", 0
		}
		plain := r.Old + " " + ctx.Icons.Arrow + " " + r.New
		if r.Old == "" {
			plain = r.New
		}
		if ansi.StringWidth(plain) > room {
			return th.Muted.Render(cut(plain, room)), room
		}
		newStyle := th.Base
		if dim {
			newStyle = th.Muted
		}
		if r.Old == "" {
			return newStyle.Render(r.New), ansi.StringWidth(plain)
		}
		return th.Muted.Render(r.Old+" "+ctx.Icons.Arrow+" ") + newStyle.Render(r.New), ansi.StringWidth(plain)
	}

	switch r.State {
	case Running:
		out := ""
		used := 0
		if r.Percent >= 0 && room >= barWidth+6 {
			pct := fmt.Sprintf(" %3.0f%%", min(r.Percent, 100))
			out = ctx.Track(r.Percent/100, barWidth) + th.Info.Render(pct)
			used = barWidth + len(pct)
		}
		detail := r.Detail
		if detail == "" {
			detail = "working" + ellipsis(ctx)
		}
		if used > 0 {
			out += "  "
			used += 2
		}
		return out + th.Muted.Render(cut(detail, room-used))
	case Done:
		v, w := versions(false)
		if r.Detail != "" && w+2 < room {
			sep := ""
			if v != "" {
				sep = "  "
			}
			return v + th.Muted.Render(sep+cut(r.Detail, room-w-len(sep)))
		}
		return v
	case Warn:
		v, w := versions(false)
		sep := ""
		if v != "" {
			sep = "  "
		}
		return v + th.Warning.Render(sep+cut(r.Detail, room-w-len(sep)))
	case Failed:
		return th.Danger.Render(cut(r.Detail, room))
	case Skipped:
		return th.Muted.Render(cut(r.Detail, room))
	default: // Queued
		v, _ := versions(true)
		return v
	}
}

// Header is the two lines over a run: what is happening and how far along
// the whole run is, then a bar for it.
//
//	Updating 3 of 17  ·  winget  ·  2m 04s
//	━━━━━━━━━━━━━━──────────────────────────  18%
func Header(ctx uictx.Context, verb string, done, total int, context string, elapsed time.Duration, width int) string {
	th := ctx.Theme
	sep := th.Muted.Render("  ·  ")
	top := th.Title.Render(fmt.Sprintf("%s %d of %d", verb, min(done+1, max(total, 1)), total))
	if done >= total && total > 0 {
		top = th.Title.Render(fmt.Sprintf("%s %d of %d", verb, total, total))
	}
	if context != "" {
		top += sep + th.Subtitle.Render(context)
	}
	if elapsed > 0 {
		top += sep + th.Muted.Render(Duration(elapsed))
	}
	frac := 0.0
	if total > 0 {
		frac = float64(done) / float64(total)
	}
	w := min(48, max(10, width-8))
	bar := " " + ctx.Track(frac, w) + th.Info.Render(fmt.Sprintf("  %3.0f%%", frac*100))
	return " " + top + "\n" + bar
}

// Tally counts a run's outcomes for the summary line.
type Tally struct {
	Done, Warn, Failed, Skipped int
}

// Count tallies rows by state.
func Count(rows []Row) Tally {
	var t Tally
	for _, r := range rows {
		switch r.State {
		case Done:
			t.Done++
		case Warn:
			t.Warn++
		case Failed:
			t.Failed++
		case Skipped:
			t.Skipped++
		}
	}
	return t
}

// DoneBox is the summary card at the end of a run, in the shape of the
// website demo's: "✔ Updated 14 · ! 1 needs a restart · Skipped 2 · ✖ Failed 1
// · 6m 12s". verb is the past tense of what the run did, e.g. "Updated".
func DoneBox(ctx uictx.Context, verb string, t Tally, elapsed time.Duration, width int) string {
	th := ctx.Theme
	ic := ctx.Icons
	sep := th.Muted.Render("  ·  ")

	lead := th.Success.Bold(true).Render(ic.Tick + " " + verb + " " + strconv.Itoa(t.Done+t.Warn))
	if t.Failed > 0 && t.Done+t.Warn == 0 {
		lead = th.Danger.Bold(true).Render(ic.Fail + " Nothing " + strings.ToLower(verb))
	}
	parts := []string{lead}
	if t.Warn > 0 {
		noun := "needs a restart"
		if t.Warn > 1 {
			noun = "need a restart"
		}
		parts = append(parts, th.Warning.Render(fmt.Sprintf("%s %d %s", ic.Warn, t.Warn, noun)))
	}
	if t.Skipped > 0 {
		parts = append(parts, th.Muted.Render(fmt.Sprintf("Skipped %d", t.Skipped)))
	}
	if t.Failed > 0 {
		parts = append(parts, th.Danger.Render(fmt.Sprintf("%s Failed %d", ic.Fail, t.Failed)))
	}
	if elapsed > 0 {
		parts = append(parts, th.Muted.Render(Duration(elapsed)))
	}
	body := strings.Join(parts, sep)
	card := th.CardFor(ctx.Icons.Tier == icons.TierASCII)
	if w := ansi.StringWidth(body) + 4; width > 0 && w < width {
		card = card.Width(w)
	}
	return card.Render(body)
}

// LogView draws the last lines of a run's raw output, each cut to width,
// for the screens' "show the full log" key.
func LogView(ctx uictx.Context, lines []string, width, height int) string {
	th := ctx.Theme
	if height <= 0 {
		height = len(lines)
	}
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if width > 2 && ansi.StringWidth(l)+2 > width {
			l = ansi.Truncate(l, width-2, ellipsis(ctx))
		}
		out = append(out, th.Muted.Render("  "+l))
	}
	return strings.Join(out, "\n")
}

// Duration renders an elapsed time the way the rows show it: "0.8s", "12s",
// "1m 02s", "1h 05m".
func Duration(d time.Duration) string {
	switch {
	case d <= 0:
		return ""
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// ellipsis is the mark a cut line ends with, in the tier's spelling.
func ellipsis(ctx uictx.Context) string {
	if ctx.Icons.Tier == icons.TierASCII {
		return "..."
	}
	return "…"
}
