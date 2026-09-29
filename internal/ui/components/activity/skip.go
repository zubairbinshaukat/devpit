package activity

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// StuckAfter is how long a running job may say nothing before its row hints
// that skipping it is an option. Three minutes: package managers print a line
// every few seconds while they download, so a silence this long is a
// silent installer or a hung one, and skipping is a fair thing to offer. It
// is only a hint. The job is never stopped for it.
const StuckAfter = 3 * time.Minute

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
// line, e.g. "no output for 3m 05s · press s to skip".
func StuckText(silent time.Duration) string {
	return "no output for " + Duration(silent) + " · press s to skip"
}

// NextSteps draws the "what to do next" block under a finished run: one line
// per row that has a Next, "Name: what to do". It is empty when no row has
// one. Each line is cut to width. lines is how many rows it takes, so the
// screen can leave room for it.
func NextSteps(ctx uictx.Context, rows []Row, width int) (block string, lines int) {
	th := ctx.Theme
	var out []string
	for _, r := range rows {
		if r.Next == "" {
			continue
		}
		line := "  " + r.Label + ": " + r.Next
		if width > 2 && ansi.StringWidth(line)+1 > width {
			line = ansi.Truncate(line, width-1, ellipsis(ctx))
		}
		out = append(out, th.Muted.Render(line))
	}
	if len(out) == 0 {
		return "", 0
	}
	head := " " + th.Subtitle.Render("What to do next")
	return head + "\n" + strings.Join(out, "\n"), len(out) + 1
}
