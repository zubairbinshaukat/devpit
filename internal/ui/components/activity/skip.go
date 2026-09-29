package activity

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// StuckAfter is how long a running job may say nothing before its row hints
// that skipping it is an option. Five minutes: package managers print a line
// every few seconds while they download, but the silent installers of big
// apps (Docker Desktop, Visual Studio) can work quietly for several minutes,
// so only a silence longer than that suggests a hung one. It is only a hint.
// The job is never stopped for it.
const StuckAfter = 5 * time.Minute

// SkipWindow is how long the first press of the skip key stays armed. A
// second press inside it skips; after it the key needs pressing twice again.
// Skipping stops an installer partway, so one stray key must never do it.
const SkipWindow = 5 * time.Second

// SkipGate is the press-twice-to-confirm rule for the skip key. It is a plain
// value with no clock of its own: the screen passes the time in, so a test
// can step through the window exactly. The zero value is disarmed.
type SkipGate struct {
	armed bool
	job   int
	at    time.Time
}

// Press records one press of the skip key for job at now. fire is true when
// this press is the confirming second one: same job, inside [SkipWindow].
// The returned gate is what the screen keeps; after a fire it is disarmed.
func (g SkipGate) Press(job int, now time.Time) (SkipGate, bool) {
	if g.Armed(job, now) {
		return SkipGate{}, true
	}
	return SkipGate{armed: true, job: job, at: now}, false
}

// Armed reports whether a first press for job is waiting for its second at
// now. A press for another job, or one older than [SkipWindow], is not.
func (g SkipGate) Armed(job int, now time.Time) bool {
	return g.armed && g.job == job && now.Sub(g.at) <= SkipWindow
}

// Disarm is the gate after any other key was pressed: the confirmation was
// for the skip key alone, and a different key in between means the user
// changed their mind.
func (SkipGate) Disarm() SkipGate { return SkipGate{} }

// StuckText is the hint a silent running row shows in place of its latest
// line, e.g. "no output for 3m 05s · press s twice to skip".
func StuckText(silent time.Duration) string {
	return "no output for " + Duration(silent) + " · press s twice to skip"
}

// NextSteps draws the "what to do next" block under a finished run: one step
// per row that has a Next, "Name: what to do". It is empty when no row has
// one. A step that is wider than the screen wraps at its spaces under a
// hanging indent instead of being cut, because a step is often a command to
// type by hand and every character of it matters. lines is how many rows the
// block takes, so the screen can leave room for it; FitNextSteps shortens it.
func NextSteps(ctx uictx.Context, rows []Row, width int) (block string, lines int) {
	th := ctx.Theme
	var out []string
	for _, r := range rows {
		if r.Next == "" {
			continue
		}
		step := WrapCommand(r.Label+": "+r.Next, stepIndent, width-1)
		for _, l := range strings.Split(step, "\n") {
			out = append(out, th.Muted.Render(l))
		}
	}
	if len(out) == 0 {
		return "", 0
	}
	head := " " + th.Subtitle.Render("What to do next")
	return head + "\n" + strings.Join(out, "\n"), len(out) + 1
}

// stepIndent starts each next step; its wrapped lines are indented two more.
const stepIndent = "  "

// FitNextSteps shortens a NextSteps block of lines rows to at most maxLines,
// dropping whole steps from the end and saying how many were left out, so a
// wrapped command is never shown half. With room for fewer than two lines
// (the heading and one line) it is dropped. It returns the block and its
// height.
func FitNextSteps(ctx uictx.Context, block string, lines, maxLines int) (string, int) {
	if lines <= maxLines {
		return block, lines
	}
	if maxLines < 2 {
		return "", 0
	}
	all := strings.Split(block, "\n")
	head, body := all[0], all[1:]
	// Group the lines into steps: a wrapped line starts deeper than a step.
	var steps [][]string
	for _, l := range body {
		if len(steps) > 0 && strings.HasPrefix(ansi.Strip(l), stepIndent+"  ") {
			steps[len(steps)-1] = append(steps[len(steps)-1], l)
			continue
		}
		steps = append(steps, []string{l})
	}
	// Keep whole steps while they fit with the heading and the "more" line.
	kept, used := []string{head}, 1
	n := 0
	for ; n < len(steps); n++ {
		if used+len(steps[n])+1 > maxLines {
			break
		}
		kept = append(kept, steps[n]...)
		used += len(steps[n])
	}
	if n == 0 && maxLines >= 3 {
		// Not even the first step fits whole: show its first line, since a
		// hint that part of it is hidden beats no hint at all.
		kept = append(kept, steps[0][0])
		n = 1
	}
	if left := len(steps) - n; left > 0 {
		kept = append(kept, ctx.Theme.Muted.Render(fmt.Sprintf("%s%sand %d more", stepIndent, ellipsis(ctx), left)))
	}
	return strings.Join(kept, "\n"), len(kept)
}

// WrapCommand breaks text that may be typed by hand, such as a command, across
// lines no wider than width at its spaces, never cutting it: the first line
// starts with indent, the rest with two more spaces. It breaks only at spaces,
// never after a dash the way prose wrapping does, because "Games-x7k2" split
// in two would be typed with a space in it. A word longer than a line (a long
// path) is split where it must be.
func WrapCommand(text, indent string, width int) string {
	room := width - ansi.StringWidth(indent) - 2
	if room < 10 || ansi.StringWidth(text) <= width-ansi.StringWidth(indent) {
		return indent + text
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= room:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
		for ansi.StringWidth(line) > room {
			lines = append(lines, ansi.Truncate(line, room, ""))
			line = ansi.TruncateLeft(line, room, "")
		}
	}
	lines = append(lines, line)
	for i, l := range lines {
		if i == 0 {
			lines[i] = indent + l
		} else {
			lines[i] = indent + "  " + l
		}
	}
	return strings.Join(lines, "\n")
}
