package share

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// pollEvery is how often the sharing card asks how much has been sent.
const pollEvery = time.Second

// Messages of the sender-side progress poll. gen ties a message to the share
// that started the chain, so a late answer from an earlier share is dropped.
type (
	// trafficTickMsg says it is time to read the counters again.
	trafficTickMsg struct{ gen int }
	// trafficMsg carries one reading.
	trafficMsg struct {
		gen int
		p   host.Progress
	}
)

// polling reports whether the card is up, so progress should be read.
func (m hostScreen) polling() bool { return m.stage == hostCard || m.stage == hostConfirmStop }

// pollCmd schedules the next reading. It returns nil when the engine cannot
// report progress, so a fake or an older engine never polls.
func (m hostScreen) pollCmd() tea.Cmd {
	if _, ok := m.mgr.(Progresser); !ok {
		return nil
	}
	gen := m.pollGen
	return spinTick(pollEvery, func(time.Time) tea.Msg { return trafficTickMsg{gen: gen} })
}

// readTraffic reads the counters off the update loop.
func (m hostScreen) readTraffic() tea.Cmd {
	src, ok := m.mgr.(Progresser)
	if !ok {
		return nil
	}
	gen := m.pollGen
	return func() tea.Msg { return trafficMsg{gen: gen, p: src.Progress()} }
}

// endPoll kills the poll chain and forgets the last reading.
func (m *hostScreen) endPoll() {
	m.pollGen++
	m.prog = host.Progress{}
}

// onTrafficTick answers a tick with a reading, unless it is stale.
func (m hostScreen) onTrafficTick(msg trafficTickMsg) (uictx.Screen, tea.Cmd) {
	if msg.gen != m.pollGen || !m.polling() {
		return m, nil
	}
	return m, m.readTraffic()
}

// onTraffic keeps the newest reading and schedules the next one. Only one tick
// and one read are ever in flight, so nothing queues up behind a slow read.
func (m hostScreen) onTraffic(msg trafficMsg) (uictx.Screen, tea.Cmd) {
	if msg.gen != m.pollGen || !m.polling() {
		return m, nil
	}
	m.prog = msg.p
	return m, m.pollCmd()
}

// TerminalProgress implements uictx.ProgressReporter: the taskbar bar follows
// the send while it is flowing and the folder size is known. A share never
// finishes by itself, so the bar never reaches 100.
func (m hostScreen) TerminalProgress() *tea.ProgressBar {
	if m.stage != hostCard || !m.prog.Flowing || !m.prog.TotalKnown || m.prog.Total <= 0 {
		return nil
	}
	pct := int(min(99, m.prog.Sent*100/uint64(m.prog.Total))) //nolint:gosec // Total > 0 is checked above
	return tea.NewProgressBar(tea.ProgressBarDefault, pct)
}

// trafficBlock draws what this PC has sent, under the card. It is empty until
// bytes have moved, so a quiet share looks exactly as it did before. cardRows
// is the height of the card above it, used to fit the room that is left.
func (m hostScreen) trafficBlock(ctx uictx.Context, cardRows int) string {
	p := m.prog
	if !p.Visible() {
		return ""
	}
	avail := ctx.BodyHeight - cardRows - 1 // one blank row between card and block
	if avail < 1 {
		return ""
	}
	th := ctx.Theme

	about := ""
	if p.Source == host.SourceAdapter {
		about = "about "
	}
	sent := about + recv.FormatBytes(int64(p.Sent)) //nolint:gosec // a byte count fits int64
	of := ""
	frac := -1.0
	if p.TotalKnown && p.Total > 0 {
		of = " of "
		if p.TotalPartial {
			of += "at least "
		}
		of += recv.FormatBytes(p.Total)
		frac = min(1, float64(p.Sent)/float64(p.Total))
	}

	head := th.Info.Bold(true).Render(" Sending")
	var tail string
	switch {
	case !p.Flowing:
		head = th.Muted.Render(" Sent")
		tail = "idle for " + roundDuration(p.IdleFor)
	default:
		tail = speedText(p.Rate)
		if p.Active > 0 {
			tail += "  ·  " + roundDuration(p.Active)
		}
	}

	if avail < 3 {
		// Not enough room for the full block: one line.
		line := head + "  " + th.Base.Render(sent+of)
		if frac >= 0 {
			line += th.Info.Render(fmt.Sprintf("  %3.0f%%", frac*100))
		} else {
			line += th.Muted.Render(" so far")
		}
		line += th.Muted.Render("  ·  " + tail)
		return "\n" + line
	}

	var b strings.Builder
	b.WriteString("\n")
	if frac >= 0 {
		b.WriteString(head + "  " + ctx.Track(frac, min(40, max(10, ctx.Width-24))) +
			th.Info.Render(fmt.Sprintf("  %3.0f%%", frac*100)) + "\n")
		b.WriteString(" " + th.Base.Bold(true).Render(sent+of) + th.Muted.Render("  ·  "+tail) + "\n")
	} else {
		b.WriteString(head + "  " + th.Base.Bold(true).Render(sent+" so far") + th.Muted.Render("  ·  "+tail) +
			th.Muted.Render("  (measuring folder size…)") + "\n")
		b.WriteString("")
	}
	b.WriteString(" " + th.Muted.Render(m.trafficNote()))
	return b.String()
}

// trafficNote says plainly how rough the numbers are.
func (m hostScreen) trafficNote() string {
	if m.prog.Source == host.SourceAdapter {
		return "Counts everything this PC sends on " + m.card.Adapter + ", not only this copy."
	}
	return "Counted for every PC copying from this share. Not exact."
}
