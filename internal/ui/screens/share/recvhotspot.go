package share

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/share/errmap"
	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// Joiner is optional on a Receiver: an engine that can join the sharing PC's
// Wi-Fi hotspot and put this PC back on its own network afterwards. The
// screen checks for it with a type assertion, so a Receiver without it (an
// older fake, or a PC with no Wi-Fi card) simply never offers the hotspot.
// *recv.Session is the real one.
type Joiner interface {
	// CanJoin reports whether this PC has a Wi-Fi card that can join.
	CanJoin() bool
	// FindHotspots scans and lists the networks nearby, Devpit names first.
	FindHotspots() ([]winapi.WifiNetwork, error)
	// Join joins one, measures the link before and after, and says whether
	// the hotspot is worse than the link this PC had.
	Join(ctx context.Context, req recv.JoinRequest) (recv.JoinResult, error)
	// Leave puts this PC back on the network it was on.
	Leave() (recv.LeaveResult, error)
	// ResumeNeeds says whether resuming a copy has to join a hotspot first.
	ResumeNeeds(j job.Job) (recv.ResumeHotspot, error)
}

// The ways the receiving screen can reach the sharing PC (the IDs of the
// first menu, shown only on a PC that can join a hotspot).
const (
	wayAddress = "address"
	wayHotspot = "hotspot"
)

// Messages of the hotspot steps.
type (
	// hotspotsMsg is the result of a scan.
	hotspotsMsg struct {
		nets []winapi.WifiNetwork
		err  error
	}
	// joinedMsg is the result of a join.
	joinedMsg struct {
		res recv.JoinResult
		err error
	}
	// leftMsg is the result of going back to this PC's own network.
	leftMsg struct {
		res recv.LeaveResult
		err error
	}
)

// joiner returns the engine's hotspot side when it has one and this PC can
// join.
func (m recvScreen) joiner() (Joiner, bool) {
	j, ok := m.rcv.(Joiner)
	if !ok || !j.CanJoin() {
		return nil, false
	}
	return j, true
}

// wayMenu is the first question on a PC that can join a hotspot.
func wayMenu() menu.Model {
	return menu.New([]menu.Item{
		{ID: wayAddress, Title: "Type the other PC's address", Desc: "The usual way, over your Wi-Fi or office network."},
		{ID: wayHotspot, Title: "Join the other PC's hotspot", Desc: "When the sharing PC's card shows a hotspot name and password."},
	})
}

// newHotspotInput is the masked hotspot password field.
func newHotspotInput() textinput.Model {
	ti := newInput(34, "hotspot password")
	ti.EchoMode = textinput.EchoPassword
	return ti
}

// scanCmd looks for networks nearby.
func (m recvScreen) scanCmd() tea.Cmd {
	j, ok := m.joiner()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		nets, err := j.FindHotspots()
		return hotspotsMsg{nets: nets, err: err}
	}
}

// startScan shows the spinner and scans.
func (m recvScreen) startScan() (recvScreen, tea.Cmd) {
	m.stage = recvScanning
	return m, tea.Batch(m.scanCmd(), spin())
}

// onHotspots shows the networks found, or says none were.
func (m recvScreen) onHotspots(msg hotspotsMsg) (uictx.Screen, tea.Cmd) {
	retry := func(s recvScreen) (recvScreen, tea.Cmd) { return s.startScan() }
	if errors.Is(msg.err, recv.ErrScanDenied) {
		// Windows would not list the networks (location access is off):
		// the name is on the sharing PC's card, so it can be typed.
		m.stage = recvHotspotName
		m.hsName.SetValue("")
		return m, m.hsName.Focus()
	}
	if msg.err != nil {
		return m.fail(msg.err, errmap.PhaseConnect, retry)
	}
	if len(msg.nets) == 0 {
		return m.fail(errors.New("no Wi-Fi networks were found nearby. Check that the sharing PC shows its hotspot, then try again"),
			errmap.PhaseConnect, retry)
	}
	m.nets = msg.nets
	items := make([]menu.Item, len(msg.nets))
	for i, n := range msg.nets {
		it := menu.Item{ID: fmt.Sprint(i), Title: n.SSID}
		switch {
		case strings.HasPrefix(n.SSID, recv.HotspotPrefix):
			it.Desc = "A Devpit hotspot."
		case n.HasProfile:
			it.Desc = "Saved on this PC, so no password is needed."
		}
		if n.Signal > 0 {
			it.Hint = fmt.Sprintf("signal %d%%", n.Signal)
		}
		items[i] = it
	}
	m.netMenu = menu.New(items)
	m.stage = recvHotspots
	return m, nil
}

// pickNetwork joins the chosen network, asking for its password first when
// this PC does not know it already.
func (m recvScreen) pickNetwork(n winapi.WifiNetwork) (uictx.Screen, tea.Cmd) {
	m.ssid, m.netProfile = n.SSID, n.Profile
	if n.HasProfile {
		return m.startJoin("")
	}
	return m.askHotspotPass()
}

// askHotspotPass opens the masked password field.
func (m recvScreen) askHotspotPass() (uictx.Screen, tea.Cmd) {
	m.stage = recvHotspotPass
	m.hsPass.SetValue("")
	return m, m.hsPass.Focus()
}

// startJoin joins m.ssid. The password is handed over once and never kept.
func (m recvScreen) startJoin(pass string) (uictx.Screen, tea.Cmd) {
	j, ok := m.joiner()
	if !ok {
		return m, nil
	}
	m.hsPass.SetValue("")
	m.hsPass.Blur()
	m.stage = recvJoining
	ctx, req := m.ctx, recv.JoinRequest{SSID: m.ssid, Password: pass, Profile: m.netProfile}
	return m, tea.Batch(func() tea.Msg {
		res, err := j.Join(ctx, req)
		return joinedMsg{res: res, err: err}
	}, spin())
}

// onJoined handles a finished join: ask for the password, fail, warn about a
// slower link, or carry on to the sharing PC's folders.
func (m recvScreen) onJoined(msg joinedMsg) (uictx.Screen, tea.Cmd) {
	switch {
	case errors.Is(msg.err, recv.ErrNeedPassword):
		return m.askHotspotPass()
	case msg.err != nil:
		m.joinNote = msg.res.Undo.Note
		return m.fail(msg.err, errmap.PhaseConnect, func(s recvScreen) (recvScreen, tea.Cmd) {
			if len(s.nets) == 0 {
				return s.startScan()
			}
			s.stage = recvHotspots
			return s, nil
		})
	}
	m.joined, m.join, m.joinNote = true, msg.res, ""
	if msg.res.Verdict.Worse {
		m.stage = recvSlowLink
		return m, nil
	}
	return m.afterJoin()
}

// afterJoin goes on from a good join: a resumed copy measures again, a new
// one lists the sharing PC's folders at the hotspot's address.
func (m recvScreen) afterJoin() (uictx.Screen, tea.Cmd) {
	if m.resumeJoin {
		m.resumeJoin = false
		return m.startCheck()
	}
	h := m.join.Host
	if h == "" {
		h = recv.DefaultHotspotHost
	}
	m.ip.SetValue(h)
	// A Devpit hotspot is named after the share's temporary login, so the
	// user name is known: Devpit-x7k2 signs in as devpit-x7k2.
	if suffix, ok := strings.CutPrefix(m.ssid, recv.HotspotPrefix); ok && suffix != "" {
		m.user.SetValue("devpit-" + strings.ToLower(suffix))
	}
	m.stage = recvListing
	return m, tea.Batch(m.listCmd(), spin())
}

// leaveCmd puts this PC back on its own network.
func (m recvScreen) leaveCmd() tea.Cmd {
	j, ok := m.rcv.(Joiner)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		res, err := j.Leave()
		return leftMsg{res: res, err: err}
	}
}

// backToWifi leaves the hotspot and asks for the sharing PC's normal address.
func (m recvScreen) backToWifi() (uictx.Screen, tea.Cmd) {
	m.joined = false
	// A resumed hotspot copy cannot carry on over the normal network: its
	// record names the hotspot's address. It starts again from the address
	// the user types; finished files are still skipped.
	if m.resumeJoin || m.job.Hotspot() {
		m.resumeJoin, m.job = false, job.Job{}
	}
	m.stage = recvLeaving
	return m, tea.Batch(m.leaveCmd(), spin())
}

// onLeft shows the address field again, with what happened.
func (m recvScreen) onLeft(msg leftMsg) (uictx.Screen, tea.Cmd) {
	m.ipNote = "Back on your own network. Type the sharing PC's normal address from its card."
	switch {
	case msg.err != nil:
		m.ipNote = "Devpit could not put this PC back on its Wi-Fi. Pick your network in Windows, then type the sharing PC's normal address."
	case msg.res.Note != "":
		m.ipNote = msg.res.Note + " Type the sharing PC's normal address from its card."
	}
	m.ip.SetValue("")
	m.stage = recvIP
	return m, m.ip.Focus()
}

// retryCopy runs a stopped or failed copy again. A hotspot copy rejoins the
// hotspot first: every end of a run puts this PC back on its own Wi-Fi, so
// copying straight away would only wait for an address it cannot reach.
func (m recvScreen) retryCopy() (uictx.Screen, tea.Cmd) {
	if m.job.Hotspot() {
		return m.resumeStart()
	}
	return m.beginCopy(m.job)
}

// resumeStart resumes a copy, joining the hotspot it came from first.
func (m recvScreen) resumeStart() (uictx.Screen, tea.Cmd) {
	j, ok := m.joiner()
	if !ok || !m.job.Hotspot() {
		return m.startCheck()
	}
	need, err := j.ResumeNeeds(m.job)
	if err != nil || !need.Rejoin {
		return m.startCheck()
	}
	m.resumeJoin = true
	m.ssid, m.netProfile = need.SSID, need.Profile
	if need.NeedPassword {
		return m.askHotspotPass()
	}
	return m.startJoin("")
}

// hotspotKey handles keys on the hotspot stages. It reports false for a
// stage that is not one of them.
func (m recvScreen) hotspotKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd, bool) {
	switch m.stage {
	case recvWay:
		next, cmd := m.wayMenu.Update(msg)
		m.wayMenu = next
		if it, ok := m.wayMenu.Selected(); ok && msg.Code == tea.KeyEnter {
			if it.ID == wayHotspot {
				s, c := m.startScan()
				return s, c, true
			}
			m.stage = recvIP
			return m, m.ip.Focus(), true
		}
		return m, cmd, true
	case recvHotspots:
		if msg.Text == "r" {
			s, c := m.startScan()
			return s, c, true
		}
		next, cmd := m.netMenu.Update(msg)
		m.netMenu = next
		if it, ok := m.netMenu.Selected(); ok && msg.Code == tea.KeyEnter {
			var i int
			if _, err := fmt.Sscan(it.ID, &i); err == nil && i >= 0 && i < len(m.nets) {
				s, c := m.pickNetwork(m.nets[i])
				return s, c, true
			}
		}
		return m, cmd, true
	case recvHotspotName:
		if msg.Code == tea.KeyEnter {
			name := strings.TrimSpace(m.hsName.Value())
			if name == "" {
				return m, nil, true
			}
			m.hsName.Blur()
			m.ssid, m.netProfile = name, ""
			s, c := m.startJoin("")
			return s, c, true
		}
		var cmd tea.Cmd
		m.hsName, cmd = m.hsName.Update(msg)
		return m, cmd, true
	case recvHotspotPass:
		if msg.Code == tea.KeyEnter {
			if m.hsPass.Value() == "" {
				return m, nil, true
			}
			s, c := m.startJoin(m.hsPass.Value())
			return s, c, true
		}
		var cmd tea.Cmd
		m.hsPass, cmd = m.hsPass.Update(msg)
		return m, cmd, true
	case recvSlowLink:
		switch {
		case msg.Code == tea.KeyEnter:
			s, c := m.afterJoin()
			return s, c, true
		case msg.Text == "b":
			s, c := m.backToWifi()
			return s, c, true
		}
		return m, nil, true
	case recvScanning, recvJoining, recvLeaving:
		return m, nil, true
	}
	return m, nil, false
}

// hotspotHelp is the key line of the hotspot stages, and false for any other
// stage.
func (m recvScreen) hotspotHelp() ([]keyHint, bool) {
	switch m.stage {
	case recvWay:
		return []keyHint{{"enter", "choose"}}, true
	case recvHotspots:
		return []keyHint{{"enter", "join"}, {"r", "scan again"}}, true
	case recvHotspotPass, recvHotspotName:
		return []keyHint{{"enter", "join"}}, true
	case recvSlowLink:
		return []keyHint{{"b", "back to my Wi-Fi"}, {"enter", "use the hotspot anyway"}}, true
	case recvScanning, recvJoining, recvLeaving:
		return nil, true
	}
	return nil, false
}

// keyHint is one key and what it does.
type keyHint struct{ key, help string }

// linkText writes a link's negotiated rate as Windows reports it, or "not
// known". It is the card's link rate, not a promise of copy speed.
func linkText(l recv.LinkInfo) string {
	if !l.OnWifi {
		return "not on Wi-Fi"
	}
	rate := l.RxKbps
	if rate == 0 {
		rate = l.TxKbps
	}
	s := "link rate not known"
	if rate > 0 {
		s = fmt.Sprintf("%d Mbps link", rate/1000)
	}
	if l.Standard != "" {
		s = l.Standard + ", " + s
	}
	return s
}

// hotspotView draws the hotspot stages, and false for any other stage.
func (m recvScreen) hotspotView(ctx uictx.Context) (string, bool) {
	th := ctx.Theme
	switch m.stage {
	case recvWay:
		return th.Muted.Render("How do you want to reach the sharing PC?") + "\n\n" + m.wayMenu.View(ctx), true
	case recvScanning:
		return th.Muted.Render(ctx.SpinnerFrame(m.frame) + " Looking for Wi-Fi networks nearby…"), true
	case recvHotspots:
		return ctx.Wrap(th.Muted.Render("Pick the hotspot name shown on the sharing PC's card.")) + "\n\n" + m.netMenu.View(ctx), true
	case recvHotspotName:
		return ctx.Wrap(th.Muted.Render("Windows would not list the Wi-Fi networks nearby. Type the hotspot name from the sharing PC's card.")) +
			"\n\n" + th.Muted.Render("Hotspot  ") + styleInput(m.hsName, th).View(), true
	case recvHotspotPass:
		var b strings.Builder
		b.WriteString(th.Base.Render("Join "+m.ssid+".") + "\n")
		b.WriteString(ctx.Wrap(th.Muted.Render("Type the hotspot password from the sharing PC's card.")) + "\n\n")
		b.WriteString(th.Muted.Render("Password  ") + styleInput(m.hsPass, th).View() + "\n\n")
		b.WriteString(ctx.Wrap(th.Muted.Render("Devpit uses it once to join and does not save it. When the copy ends, this PC goes back to its own Wi-Fi.")))
		return b.String(), true
	case recvJoining:
		return th.Muted.Render(ctx.SpinnerFrame(m.frame)+" Joining "+m.ssid+"…") + "\n" +
			th.Muted.Render("This can take half a minute."), true
	case recvLeaving:
		return th.Muted.Render(ctx.SpinnerFrame(m.frame) + " Going back to your own Wi-Fi…"), true
	case recvSlowLink:
		v := m.join.Verdict
		label := func(s string) string { return th.Muted.Render(fmt.Sprintf("%-12s", s)) }
		var b strings.Builder
		b.WriteString(th.Warning.Render(ctx.Icons.Warn+" "+v.Reason) + "\n\n")
		b.WriteString(label("Your Wi-Fi") + th.Base.Render(linkText(v.Before)) + "\n")
		b.WriteString(label("Hotspot") + th.Base.Render(linkText(v.After)) + "\n\n")
		b.WriteString(ctx.Wrap(th.Muted.Render("These are the link rates Windows reports, not copy speeds.")) + "\n")
		b.WriteString(ctx.Wrap(th.Muted.Render("Press b to go back to your Wi-Fi and copy over your network, or Enter to use the hotspot anyway.")))
		return b.String(), true
	}
	return "", false
}
