package share

import (
	"context"
	"errors"
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/share/errmap"
	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/pathpicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// recvStage is where the receiving screen is.
type recvStage int

// The stages, in the order a copy passes through them.
const (
	recvResumeAsk recvStage = iota
	recvIP
	recvListing
	recvCreds
	recvShares
	recvDest
	recvChecking
	recvCheck
	recvCopy
	recvDone
	recvFailed
	recvError
)

// credsFor says which step the sign-in form belongs to.
type credsFor int

// The two places a sign-in can be asked for.
const (
	// credsForList is a PC that would not even list its shares without one.
	credsForList credsFor = iota
	// credsForShare is the sign-in for the chosen shared folder.
	credsForShare
	// credsForResume is a resumed copy whose sign-in Windows has forgotten.
	credsForResume
)

// Messages the receiving screen's commands return.
type (
	// listMsg is the result of listing a PC's shares.
	listMsg struct {
		host   string
		shares []netstat.Share
		err    error
	}
	// signedInMsg is the result of a sign-in.
	signedInMsg struct {
		shares []netstat.Share
		err    error
		what   credsFor
	}
	// checkMsg is the result of the dry run.
	checkMsg struct {
		check recv.Check
		err   error
	}
	// droppedMsg is the result of closing old connections.
	droppedMsg struct {
		n   int
		err error
	}
	// copyEventMsg is a progress update.
	copyEventMsg struct {
		stream *copyStream
		ev     recv.Event
	}
	// copyDoneMsg is the end of a run.
	copyDoneMsg struct {
		stream *copyStream
		out    recv.Outcome
		err    error
	}
)

// copyStream carries a run's events to the screen.
type copyStream struct {
	events chan recv.Event
	done   chan copyDoneMsg
}

// recvScreen is the "Copy from a shared folder" screen.
type recvScreen struct {
	deps Deps
	rcv  Receiver
	run  *runHandle
	ctx  context.Context

	stage recvStage
	frame int

	ip     textinput.Model
	host   string
	shares []netstat.Share
	shMenu menu.Model
	share  string

	user, pass textinput.Model
	focus      int
	credsFor   credsFor
	creds      netstat.Credentials
	haveCreds  bool

	picker pathpicker.Model
	dest   string
	check  recv.Check

	job    job.Job
	stream *copyStream
	ev     recv.Event
	out    recv.Outcome

	err      error
	errPhase errmap.Phase
	retry    func(recvScreen) (recvScreen, tea.Cmd)
	dropped  int
}

// newRecvScreen returns the receiving screen. With a saved copy it opens on
// the resume question instead of the address.
func newRecvScreen(deps Deps, cfg config.Config, saved *job.Job) recvScreen {
	h, ctx := newRunHandle()
	ip := newInput(30, "192.168.1.5")
	user := newInput(34, "your user name")
	pass := newInput(34, "password")
	pass.EchoMode = textinput.EchoPassword
	m := recvScreen{
		deps: deps, rcv: deps.NewRecv(), run: h, ctx: ctx,
		stage: recvIP, ip: ip, user: user, pass: pass,
		picker: pathpicker.New(pickerConfig(cfg)).WithPrompts(
			"Where should the files go?", "Type the folder to copy into, then press Enter."),
	}
	if saved != nil {
		m.stage, m.job = recvResumeAsk, *saved
		m.host, m.share, m.dest = saved.Host, saved.Share, saved.Dest
	}
	return m
}

// Init implements uictx.Screen.
func (m recvScreen) Init() tea.Cmd {
	if m.stage == recvIP {
		c := m.ip.Focus()
		return c
	}
	return nil
}

// Title implements uictx.Screen.
func (m recvScreen) Title() string { return "Copy from a shared folder" }

// Busy implements uictx.BusyReporter: a copy is in flight.
func (m recvScreen) Busy() bool { return m.stage == recvCopy }

// Stop implements uictx.Stopper. It cancels the copy, which saves it for
// Resume.
func (m recvScreen) Stop() { m.run.stop() }

// TerminalProgress implements uictx.ProgressReporter.
func (m recvScreen) TerminalProgress() *tea.ProgressBar {
	if m.stage != recvCopy || m.ev.TotalBytes <= 0 {
		return nil
	}
	return tea.NewProgressBar(tea.ProgressBarDefault, int(min(100, m.ev.DoneBytes*100/m.ev.TotalBytes)))
}

// bind builds a key hint.
func bind(k, h string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, h)) }

// ShortHelp implements uictx.Screen.
func (m recvScreen) ShortHelp() []key.Binding {
	switch m.stage {
	case recvResumeAsk:
		return []key.Binding{bind("enter", "resume"), bind("d", "discard")}
	case recvIP:
		return []key.Binding{bind("enter", "look for shares")}
	case recvCreds:
		return []key.Binding{bind("tab", "next field"), bind("enter", "sign in")}
	case recvShares:
		return m.shMenu.Keys.ShortHelp()
	case recvDest:
		return m.picker.Keys.ShortHelp()
	case recvCheck:
		if m.check.OK() {
			return []key.Binding{bind("enter", "start copying")}
		}
		return nil
	case recvCopy:
		return []key.Binding{bind("esc", "stop (resume later)")}
	case recvDone:
		return []key.Binding{bind("enter", "back")}
	case recvFailed:
		return []key.Binding{bind("r", "try again"), bind("enter", "back")}
	case recvError:
		hints := []key.Binding{bind("r", "try again")}
		if e, ok := explain(m.err, m.errPhase, m.user.Value()); ok && e.Action == errmap.ActionDropConnections {
			hints = append(hints, bind("d", "close old connections"))
		}
		return hints
	default:
		return nil
	}
}

// FullHelp implements uictx.Screen.
func (m recvScreen) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

// spin starts the spinner ticking.
func spin() tea.Cmd { return spinCmd() }

// Update implements uictx.Screen.
func (m recvScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case spinMsg:
		switch m.stage {
		case recvListing, recvChecking, recvCopy:
			m.frame++
			return m, spinCmd()
		}
		return m, nil

	case listMsg:
		return m.onList(msg)
	case signedInMsg:
		return m.onSignedIn(msg)
	case droppedMsg:
		return m.onDropped(msg)
	case pathpicker.ChosenMsg:
		m.dest = msg.Path
		return m.startCheck()
	case pathpicker.CancelledMsg:
		m.stage = recvShares
		return m, nil
	case pathpicker.BrowsedMsg:
		next, cmd := m.picker.Update(msg)
		m.picker = next
		return m, cmd
	case checkMsg:
		return m.onCheck(msg)

	case copyEventMsg:
		if msg.stream != m.stream {
			return m, nil
		}
		m.ev = msg.ev
		return m, waitCopy(m.stream)
	case copyDoneMsg:
		if msg.stream != m.stream {
			return m, nil
		}
		return m.onCopyDone(msg)

	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	if m.stage == recvDest {
		if next, cmd, ok := m.picker.Pointer(ctx, msg, 0); ok {
			m.picker = next
			return m, cmd
		}
	}
	return m, nil
}

// fail shows an error, remembering how to try again.
func (m recvScreen) fail(err error, phase errmap.Phase, retry func(recvScreen) (recvScreen, tea.Cmd)) (uictx.Screen, tea.Cmd) {
	m.stage, m.err, m.errPhase, m.retry = recvError, err, phase, retry
	return m, nil
}

// needsLogin reports whether an error means "the other PC wants a sign-in".
func needsLogin(err error) bool {
	code, ok := errmap.CodeOf(err)
	if !ok {
		return false
	}
	switch code {
	case 5, 86, 1326, 1327, 1385, 1909, 1272:
		return true
	}
	return false
}

// listCmd lists the shares of the typed address.
func (m recvScreen) listCmd() tea.Cmd {
	rcv, ctx, typed := m.rcv, m.ctx, m.ip.Value()
	return func() tea.Msg {
		h, s, err := rcv.List(ctx, typed)
		return listMsg{host: h, shares: s, err: err}
	}
}

// onList handles the first look at the other PC.
func (m recvScreen) onList(msg listMsg) (uictx.Screen, tea.Cmd) {
	if msg.host != "" {
		m.host = msg.host
	}
	if msg.err != nil {
		if needsLogin(msg.err) {
			return m.askCreds(credsForList)
		}
		return m.fail(msg.err, errmap.PhaseConnect, func(s recvScreen) (recvScreen, tea.Cmd) {
			s.stage = recvListing
			return s, tea.Batch(s.listCmd(), spin())
		})
	}
	return m.showShares(msg.shares)
}

// showShares moves to the list of shared folders.
func (m recvScreen) showShares(shares []netstat.Share) (uictx.Screen, tea.Cmd) {
	if len(shares) == 0 {
		return m.fail(errors.New("that PC has no shared folders you can see"), errmap.PhaseConnect,
			func(s recvScreen) (recvScreen, tea.Cmd) { s.stage = recvIP; return s, s.ip.Focus() })
	}
	m.shares = shares
	items := make([]menu.Item, len(shares))
	for i, s := range shares {
		items[i] = menu.Item{ID: s.Name, Title: s.Name, Desc: s.Comment}
	}
	m.shMenu = menu.New(items)
	m.stage = recvShares
	return m, nil
}

// askCreds opens the sign-in form for one of its three purposes.
func (m recvScreen) askCreds(what credsFor) (uictx.Screen, tea.Cmd) {
	m.stage, m.credsFor, m.focus = recvCreds, what, 0
	m.pass.SetValue("")
	if what == credsForResume {
		m.user.SetValue(m.job.User)
		m.focus = 1
		return m, m.pass.Focus()
	}
	m.pass.Blur()
	return m, m.user.Focus()
}

// submitCreds signs in with what was typed.
func (m recvScreen) submitCreds() (uictx.Screen, tea.Cmd) {
	creds := netstat.Credentials{User: netstat.NormalizeUser(m.user.Value()), Password: m.pass.Value()}
	m.creds, m.haveCreds = creds, true
	m.pass.SetValue("") // the field never holds it after this
	rcv, ctx, host, share, what := m.rcv, m.ctx, m.host, m.share, m.credsFor
	m.stage = recvListing
	return m, tea.Batch(func() tea.Msg {
		if what == credsForList {
			s, err := rcv.SignInHost(ctx, host, creds)
			return signedInMsg{shares: s, err: err, what: what}
		}
		if creds.User == "" && creds.Password == "" {
			return signedInMsg{what: what}
		}
		err := rcv.SignInShare(host, share, creds)
		return signedInMsg{err: err, what: what}
	}, spin())
}

// onSignedIn handles a finished sign-in.
func (m recvScreen) onSignedIn(msg signedInMsg) (uictx.Screen, tea.Cmd) {
	if msg.err != nil {
		user := m.creds.User
		m.haveCreds = false
		m.creds = netstat.Credentials{}
		return m.fail(msg.err, errmap.PhaseConnect, func(s recvScreen) (recvScreen, tea.Cmd) {
			s.user.SetValue(user)
			next, cmd := s.askCreds(msg.what)
			return next.(recvScreen), cmd
		})
	}
	switch msg.what {
	case credsForList:
		return m.showShares(msg.shares)
	case credsForShare:
		m.stage = recvDest
		return m, nil
	default: // resume
		return m.startCheck()
	}
}

// onDropped retries after old connections were closed.
func (m recvScreen) onDropped(msg droppedMsg) (uictx.Screen, tea.Cmd) {
	if msg.err != nil {
		return m.fail(msg.err, errmap.PhaseConnect, m.retry)
	}
	m.dropped = msg.n
	if m.retry != nil {
		next, cmd := m.retry(m)
		return next, cmd
	}
	return m, nil
}

// startCheck runs the dry run for the chosen share and destination.
func (m recvScreen) startCheck() (uictx.Screen, tea.Cmd) {
	m.stage = recvChecking
	rcv, ctx, host, share, dest := m.rcv, m.ctx, m.host, m.share, m.dest
	return m, tea.Batch(func() tea.Msg {
		c, err := rcv.Preflight(ctx, host, share, dest)
		return checkMsg{check: c, err: err}
	}, spin())
}

// onCheck handles the dry run's answer.
func (m recvScreen) onCheck(msg checkMsg) (uictx.Screen, tea.Cmd) {
	if msg.err != nil {
		resuming := m.job.Host != ""
		if needsLogin(msg.err) && !m.haveCreds {
			what := credsForShare
			if resuming {
				what = credsForResume
			}
			return m.askCreds(what)
		}
		return m.fail(msg.err, errmap.PhaseConnect, func(s recvScreen) (recvScreen, tea.Cmd) {
			s.stage = recvDest
			return s, nil
		})
	}
	m.check = msg.check
	if m.job.Host != "" && msg.check.OK() {
		return m.beginCopy(m.job)
	}
	m.stage = recvCheck
	return m, nil
}

// beginCopy starts the copy on its own goroutine.
func (m recvScreen) beginCopy(j job.Job) (uictx.Screen, tea.Cmd) {
	m.job, m.stage = j, recvCopy
	m.ev = recv.Event{TotalBytes: j.TotalBytes, TotalFiles: j.TotalFiles, DoneBytes: j.DoneBytes, DoneFiles: j.DoneFiles}
	st := &copyStream{events: make(chan recv.Event, 1), done: make(chan copyDoneMsg, 1)}
	m.stream = st
	rcv, ctx := m.rcv, m.ctx
	go func() {
		out, err := rcv.Run(ctx, j, func(e recv.Event) {
			select { // keep only the newest update
			case <-st.events:
			default:
			}
			select {
			case st.events <- e:
			default:
			}
		})
		st.done <- copyDoneMsg{stream: st, out: out, err: err}
	}()
	return m, tea.Batch(waitCopy(st), spin())
}

// waitCopy waits for the next event or the end.
func waitCopy(st *copyStream) tea.Cmd {
	return func() tea.Msg {
		select {
		case e := <-st.events:
			return copyEventMsg{stream: st, ev: e}
		case d := <-st.done:
			return d
		}
	}
}

// onCopyDone shows how the copy ended.
func (m recvScreen) onCopyDone(msg copyDoneMsg) (uictx.Screen, tea.Cmd) {
	m.stream = nil
	m.out = msg.out
	m.creds = netstat.Credentials{}
	// The connection is closed only when the copy is finished. A failed,
	// stopped or broken copy keeps it: "r" and Resume run robocopy again
	// without signing in, and Windows would then try this PC's own account
	// on the other PC and fail every file with an access error.
	if msg.err == nil && msg.out.Done {
		m.rcv.Disconnect(m.host, m.share)
	}
	switch {
	case msg.err != nil:
		return m.fail(msg.err, errmap.PhaseCopy, func(s recvScreen) (recvScreen, tea.Cmd) {
			next, cmd := s.beginCopy(s.job)
			return next.(recvScreen), cmd
		})
	case msg.out.Paused:
		return m, uictx.Pop()
	case msg.out.Done:
		m.stage = recvDone
		_ = m.rcv.ClearJob()
		return m, nil
	default:
		m.stage = recvFailed
		return m, nil
	}
}

// onKey routes a key to whatever has focus.
func (m recvScreen) onKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd) {
	switch m.stage {
	case recvResumeAsk:
		switch {
		case msg.Code == tea.KeyEnter:
			return m.startCheck()
		case msg.Text == "d":
			_ = m.rcv.ClearJob()
			return m, uictx.Pop()
		}
	case recvIP:
		if msg.Code == tea.KeyEnter {
			m.stage = recvListing
			return m, tea.Batch(m.listCmd(), spin())
		}
		next, cmd := m.ip.Update(msg)
		m.ip = next
		return m, cmd
	case recvCreds:
		return m.credsKey(msg)
	case recvShares:
		next, cmd := m.shMenu.Update(msg)
		m.shMenu = next
		if it, ok := m.shMenu.Selected(); ok && msg.Code == tea.KeyEnter {
			m.share = it.ID
			if m.haveCreds {
				m.stage = recvDest
				return m, nil
			}
			return m.askCreds(credsForShare)
		}
		return m, cmd
	case recvDest:
		next, cmd := m.picker.Update(msg)
		m.picker = next
		return m, cmd
	case recvCheck:
		if msg.Code == tea.KeyEnter && m.check.OK() {
			j := m.rcv.Start(m.host, m.share, m.creds.User, m.dest, m.check.Plan)
			return m.beginCopy(j)
		}
	case recvCopy:
		if msg.Code == tea.KeyEscape {
			m.run.stop()
		}
	case recvDone:
		if msg.Code == tea.KeyEnter || msg.Code == tea.KeyEscape {
			return m, uictx.Pop()
		}
	case recvFailed:
		switch {
		case msg.Text == "r":
			return m.beginCopy(m.job)
		case msg.Code == tea.KeyEnter:
			return m, uictx.Pop()
		}
	case recvError:
		return m.errorKey(msg)
	case recvListing, recvChecking:
	}
	return m, nil
}

// credsKey handles the sign-in form.
func (m recvScreen) credsKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd) {
	switch msg.Code {
	case tea.KeyTab, tea.KeyDown, tea.KeyUp:
		m.focus = 1 - m.focus
		if m.focus == 0 {
			m.pass.Blur()
			return m, m.user.Focus()
		}
		m.user.Blur()
		return m, m.pass.Focus()
	case tea.KeyEnter:
		if m.focus == 0 {
			m.focus = 1
			m.user.Blur()
			return m, m.pass.Focus()
		}
		return m.submitCreds()
	}
	var cmd tea.Cmd
	if m.focus == 0 {
		m.user, cmd = m.user.Update(msg)
	} else {
		m.pass, cmd = m.pass.Update(msg)
	}
	return m, cmd
}

// errorKey handles the error screen: retry, or close the old connections.
func (m recvScreen) errorKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd) {
	switch {
	case msg.Text == "r" && m.retry != nil:
		next, cmd := m.retry(m)
		return next, cmd
	case msg.Text == "d":
		if e, ok := explain(m.err, m.errPhase, m.user.Value()); ok && e.Action == errmap.ActionDropConnections {
			rcv, ctx, host := m.rcv, m.ctx, m.host
			return m, func() tea.Msg {
				n, err := rcv.DropConnections(ctx, host)
				return droppedMsg{n: n, err: err}
			}
		}
	case msg.Code == tea.KeyEnter:
		m.stage = recvIP
		return m, m.ip.Focus()
	}
	return m, nil
}

// speedText writes a speed like "118 MB/s".
func speedText(bps float64) string {
	if bps < 1 {
		return ""
	}
	return recv.FormatBytes(int64(bps)) + "/s"
}

// etaText writes the time left like "4m 12s left".
func etaText(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return fmt.Sprintf("%s left", roundDuration(d))
}
