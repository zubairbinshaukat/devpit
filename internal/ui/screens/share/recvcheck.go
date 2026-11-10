package share

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// HostChecker is optional on a Receiver: an engine that can tell whether the
// other PC can be reached at an address, or whether this PC holds that
// address itself (a receiver that once ran its own Mobile Hotspot keeps
// 192.168.137.1 on a Wi-Fi Direct adapter and then talks to itself), and can
// free such an address with one admin prompt. *recv.Session is the real one.
type HostChecker interface {
	CheckHost(ctx context.Context, host string) (recv.HostCheck, error)
	FreeAddress(ctx context.Context, ip string) error
}

// Messages of the connection check.
type (
	// hostCheckedMsg is the result of a check. fromIP says the check ran on
	// a typed address, which goes straight on to the listing when all is
	// well.
	hostCheckedMsg struct {
		check  recv.HostCheck
		err    error
		fromIP bool
	}
	// freedMsg is the result of freeing the address.
	freedMsg struct{ err error }
)

// checker returns the engine's check side, if it has one.
func (m recvScreen) checker() (HostChecker, bool) {
	c, ok := m.rcv.(HostChecker)
	return c, ok
}

// checkCmd checks host off the update loop.
func (m recvScreen) checkCmd(host string, fromIP bool) tea.Cmd {
	c, ok := m.checker()
	if !ok {
		return nil
	}
	ctx := m.ctx
	return func() tea.Msg {
		hc, err := c.CheckHost(ctx, host)
		return hostCheckedMsg{check: hc, err: err, fromIP: fromIP}
	}
}

// askToCheck is the stage after a good join: the PC is on the hotspot, and
// Enter checks that the other PC can be reached before signing in.
func (m recvScreen) askToCheck() (uictx.Screen, tea.Cmd) {
	m.stage, m.hcDone, m.hcErr = recvConnCheck, false, nil
	return m, nil
}

// runCheck starts the check of m.host.
func (m recvScreen) runCheck(fromIP bool) (uictx.Screen, tea.Cmd) {
	cmd := m.checkCmd(m.host, fromIP)
	if cmd == nil {
		if fromIP {
			return m.listHost()
		}
		return m.afterCheck()
	}
	m.stage = recvConnChecking
	return m, tea.Batch(cmd, spin())
}

// listHost lists the other PC's folders at m.host.
func (m recvScreen) listHost() (uictx.Screen, tea.Cmd) {
	m.ip.SetValue(m.host)
	m.stage = recvListing
	return m, tea.Batch(m.listCmd(), spin())
}

// onHostChecked shows the answer, or goes on when a typed address is fine.
func (m recvScreen) onHostChecked(msg hostCheckedMsg) (uictx.Screen, tea.Cmd) {
	m.hc, m.hcErr, m.hcDone = msg.check, msg.err, true
	switch {
	case msg.err != nil && msg.fromIP:
		// The check could not run: carry on as before it existed.
		return m.listHost()
	case msg.fromIP && !msg.check.Local:
		// A typed address only stops here for the clash; anything else is
		// what the listing reports, as it always did.
		return m.listHost()
	}
	m.stage = recvConnCheck
	return m, nil
}

// fixCmd frees the address (one admin prompt) and checks again.
func (m recvScreen) fixCmd() (uictx.Screen, tea.Cmd) {
	c, ok := m.checker()
	if !ok {
		return m, nil
	}
	m.stage = recvFixing
	ctx, host := m.ctx, m.host
	return m, tea.Batch(func() tea.Msg { return freedMsg{err: c.FreeAddress(ctx, host)} }, spin())
}

// onFreed checks again after the fix, or says why it did not work.
func (m recvScreen) onFreed(msg freedMsg) (uictx.Screen, tea.Cmd) {
	if msg.err != nil {
		m.stage, m.hcErr = recvConnCheck, msg.err
		return m, nil
	}
	return m.runCheck(false)
}

// checkReady says the check passed: the other PC answers and is not this PC.
func (m recvScreen) checkReady() bool {
	return m.hcDone && m.hcErr == nil && !m.hc.Local && m.hc.Reachable
}

// connCheckKey handles keys on the check stages. It reports false for a
// stage that is not one of them.
func (m recvScreen) connCheckKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd, bool) {
	switch m.stage {
	case recvConnCheck:
		switch {
		case msg.Code == tea.KeyEnter && m.checkReady():
			s, c := m.afterCheck()
			return s, c, true
		case msg.Code == tea.KeyEnter && m.hcDone && m.hc.Local && !m.hc.Fixable:
			// Nothing Devpit may change: back to the user's own Wi-Fi.
			s, c := m.backToWifi()
			return s, c, true
		case msg.Code == tea.KeyEnter:
			s, c := m.runCheck(false)
			return s, c, true
		case msg.Text == "f" && m.hcDone && m.hc.Local && m.hc.Fixable:
			s, c := m.fixCmd()
			return s, c, true
		case msg.Text == "b" && m.joined:
			s, c := m.backToWifi()
			return s, c, true
		}
		return m, nil, true
	case recvConnChecking, recvFixing:
		return m, nil, true
	}
	return m, nil, false
}

// connCheckHelp is the key line of the check stages.
func (m recvScreen) connCheckHelp() ([]keyHint, bool) {
	switch m.stage {
	case recvConnCheck:
		var h []keyHint
		switch {
		case !m.hcDone:
			h = append(h, keyHint{"enter", "check the connection"})
		case m.checkReady():
			h = append(h, keyHint{"enter", "sign in"})
		case m.hc.Local && m.hc.Fixable:
			h = append(h, keyHint{"f", "fix it"}, keyHint{"enter", "check again"})
		case m.hc.Local:
			h = append(h, keyHint{"enter", "back to my Wi-Fi"})
		default:
			h = append(h, keyHint{"enter", "check again"})
		}
		if m.joined {
			h = append(h, keyHint{"b", "back to my Wi-Fi"})
		}
		return h, true
	case recvConnChecking, recvFixing:
		return nil, true
	}
	return nil, false
}

// connCheckView draws the check stages.
func (m recvScreen) connCheckView(ctx uictx.Context) (string, bool) {
	th := ctx.Theme
	switch m.stage {
	case recvConnChecking:
		return th.Muted.Render(ctx.SpinnerFrame(m.frame) + " Checking that the other PC answers at " + m.host + "…"), true
	case recvFixing:
		return th.Muted.Render(ctx.SpinnerFrame(m.frame)+" Taking the address off this PC's own hotspot adapter…") + "\n" +
			th.Muted.Render("Windows asks for permission once. Allow it to continue."), true
	case recvConnCheck:
	default:
		return "", false
	}
	var b strings.Builder
	if m.ssid != "" && m.joined {
		b.WriteString(th.Success.Render(ctx.Icons.Tick+" Joined "+m.ssid+".") + "\n\n")
	}
	if !m.hcDone {
		b.WriteString(ctx.Wrap(th.Base.Render("Press Enter to check that the other PC can be reached at " + m.host + " before you sign in.")))
		return b.String(), true
	}
	var declined *elevate.DeclinedError
	switch {
	case errors.As(m.hcErr, &declined):
		b.WriteString(th.Warning.Render(ctx.Icons.Warn+" You said no to the admin prompt. Nothing was changed.") + "\n\n")
		b.WriteString(ctx.Wrap(th.Muted.Render(m.hc.Reason())))
	case m.hcErr != nil:
		b.WriteString(th.Danger.Render(ctx.Icons.Fail+" The check did not work.") + "\n\n")
		b.WriteString(ctx.Wrap(th.Muted.Render(errorDetail(m.hcErr))))
	case m.checkReady():
		b.WriteString(th.Success.Render(ctx.Icons.Tick+" The other PC answers at "+m.host+".") + "\n\n")
		b.WriteString(th.Muted.Render("Press Enter to sign in with the user name and code from its card."))
	case m.hc.Local:
		b.WriteString(th.Danger.Render(ctx.Icons.Fail+" This PC is talking to itself.") + "\n\n")
		b.WriteString(ctx.Wrap(th.Base.Render(m.hc.Reason())))
		if m.hc.Fixable {
			b.WriteString("\n\n" + th.Muted.Render("Press f to fix it, then Devpit checks again."))
		}
	default:
		b.WriteString(th.Warning.Render(ctx.Icons.Warn+" "+m.hc.Reason()) + "\n\n")
		b.WriteString(ctx.Wrap(th.Muted.Render("Check that the other PC is still sharing, then press Enter to check again.")))
	}
	return b.String(), true
}

// afterCheck goes on from a passed check: a resumed copy measures again, a
// copy stopped by the clash carries on, a new one lists the folders.
func (m recvScreen) afterCheck() (uictx.Screen, tea.Cmd) {
	switch {
	case m.checkFor == checkForCopy:
		m.checkFor = checkForList
		return m.beginCopy(m.job)
	case m.resumeJoin:
		m.resumeJoin = false
		return m.startCheck()
	}
	return m.listHost()
}

// checkFor says where a passed check leads.
type checkFor int

// Where a check leads.
const (
	checkForList checkFor = iota // list the other PC's folders
	checkForCopy                 // carry on a copy the clash stopped
)

// clashStoppedCopy shows a copy that stopped because this PC took the
// hotspot's address again, with the same fix as after a join.
func (m recvScreen) clashStoppedCopy(hc recv.HostCheck) (uictx.Screen, tea.Cmd) {
	m.hc, m.hcErr, m.hcDone = hc, nil, true
	m.host, m.checkFor, m.joined = m.job.Host, checkForCopy, true
	m.stage = recvConnCheck
	return m, nil
}
