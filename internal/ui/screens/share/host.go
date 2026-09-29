package share

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/activity"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/pathpicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// hostStage is where the sharing screen is.
type hostStage int

// The stages, in the order a share passes through them.
const (
	hostPick hostStage = iota
	hostLooking
	hostAdapters
	hostPrivate
	hostSetup
	hostCard
	hostConfirmStop
	hostStopping
	hostStopped
	hostFailed
)

// Dialog identifiers.
const (
	privateID   = "share-private"
	stopShareID = "share-stop"
)

// Messages the sharing screen's commands return.
type (
	// hostAdaptersMsg is the list of adapters.
	hostAdaptersMsg struct {
		list []host.Adapter
		err  error
	}
	// hostCategoryMsg is the network category of the chosen adapter.
	hostCategoryMsg struct {
		cat host.Category
		err error
	}
	// hostStepMsg is the setup reaching a step.
	hostStepMsg struct {
		stream *hostStream
		step   host.Step
	}
	// hostStartedMsg is the setup ending.
	hostStartedMsg struct {
		card host.Card
		err  error
	}
	// hostStoppedMsg is the teardown ending.
	hostStoppedMsg struct{ err error }
	// spinMsg advances the spinner.
	spinMsg struct{}
)

// hostStream carries setup steps from the goroutine doing the work to the
// screen. The channel is closed by the goroutine after it sent the result.
type hostStream struct {
	steps  chan host.Step
	result chan hostStartedMsg
}

// hostScreen is the "Share a folder" screen.
type hostScreen struct {
	deps Deps
	mgr  Hoster
	run  *runHandle
	ctx  context.Context

	stage    hostStage
	picker   pathpicker.Model
	adapters []host.Adapter
	adMenu   menu.Model
	path     string
	adapter  host.Adapter
	category host.Category
	confirm  confirm.Model

	steps   []host.Step
	stepAt  int
	stream  *hostStream
	card    host.Card
	err     error
	stopErr error
	frame   int
	private bool
}

// newHostScreen returns the sharing screen.
func newHostScreen(deps Deps, cfg config.Config) hostScreen {
	h, ctx := newRunHandle()
	return hostScreen{
		deps: deps, mgr: deps.NewHost(), run: h, ctx: ctx,
		stage:  hostPick,
		picker: pathpicker.New(pickerConfig(cfg)).WithPrompts("Which folder do you want to share?", "Type the folder to share, then press Enter."),
	}
}

// Init implements uictx.Screen.
func (m hostScreen) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (m hostScreen) Title() string { return "Share a folder" }

// Busy implements uictx.BusyReporter: from the moment setup starts until
// everything is taken down again, leaving must go through Stop.
func (m hostScreen) Busy() bool {
	switch m.stage {
	case hostSetup, hostStopping:
		return true
	case hostCard, hostConfirmStop:
		// Follows the engine, so a Ctrl+C that has taken the share down
		// lets the program leave at once.
		return m.mgr != nil && m.mgr.Active()
	default:
		return m.mgr != nil && m.mgr.Active() && m.stage != hostFailed
	}
}

// Stop implements uictx.Stopper: Ctrl+C. It must not block, so it takes the
// share down on its own goroutine; Busy stays true until that is done, and
// the program's own exit path stops whatever is left.
func (m hostScreen) Stop() {
	m.run.stop()
	mgr := m.mgr
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = mgr.Stop(ctx)
	}()
}

// ShortHelp implements uictx.Screen.
func (m hostScreen) ShortHelp() []key.Binding {
	bind := func(k, h string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, h)) }
	switch m.stage {
	case hostPick:
		return m.picker.Keys.ShortHelp()
	case hostAdapters:
		return m.adMenu.Keys.ShortHelp()
	case hostPrivate, hostConfirmStop:
		return m.confirm.Keys.ShortHelp()
	case hostCard:
		return []key.Binding{bind("c", "copy net use"), bind("r", "copy robocopy"), bind("p", "copy password"), bind("s", "stop sharing")}
	case hostStopped, hostFailed:
		return []key.Binding{bind("enter", "back")}
	default:
		return nil
	}
}

// FullHelp implements uictx.Screen.
func (m hostScreen) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

// Update implements uictx.Screen.
func (m hostScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case spinMsg:
		if m.stage == hostSetup || m.stage == hostStopping || m.stage == hostLooking {
			m.frame++
			return m, spinCmd()
		}
		return m, nil

	case pathpicker.ChosenMsg:
		m.path = msg.Path
		m.stage = hostLooking
		return m, tea.Batch(m.adaptersCmd(), spinCmd())
	case pathpicker.CancelledMsg:
		return m, uictx.Pop()
	case pathpicker.BrowsedMsg:
		next, cmd := m.picker.Update(msg)
		m.picker = next
		return m, cmd

	case hostAdaptersMsg:
		return m.onAdapters(msg)
	case hostCategoryMsg:
		return m.onCategory(msg)

	case confirm.AnsweredMsg:
		return m.onAnswer(msg)

	case hostStepMsg:
		if msg.stream != m.stream {
			return m, nil
		}
		for i, s := range m.steps {
			if s.Key == msg.step.Key {
				m.stepAt = i
			}
		}
		if len(m.steps) == 0 || m.steps[m.stepAt].Key != msg.step.Key {
			m.steps = append(m.steps, msg.step)
			m.stepAt = len(m.steps) - 1
		}
		return m, waitHost(m.stream)
	case hostStartedMsg:
		if m.stream == nil {
			return m, nil
		}
		m.stream = nil
		if msg.err != nil {
			m.stage, m.err = hostFailed, msg.err
			return m, nil
		}
		m.stage, m.card = hostCard, msg.card
		return m, nil
	case hostStoppedMsg:
		if msg.err != nil {
			m.stage, m.stopErr = hostFailed, msg.err
			return m, nil
		}
		m.stage = hostStopped
		return m, nil

	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	if m.stage == hostPick {
		if next, cmd, ok := m.picker.Pointer(ctx, msg, 0); ok {
			m.picker = next
			return m, cmd
		}
	}
	return m, nil
}

// spinCmd schedules the next spinner frame.
func spinCmd() tea.Cmd {
	return spinTick(spinnerEvery*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{} })
}

// adaptersCmd lists the adapters off the update loop.
func (m hostScreen) adaptersCmd() tea.Cmd {
	mgr, ctx := m.mgr, m.ctx
	return func() tea.Msg {
		l, err := mgr.Adapters(ctx)
		return hostAdaptersMsg{list: l, err: err}
	}
}

// onAdapters picks the adapter, or asks which one when there are several.
func (m hostScreen) onAdapters(msg hostAdaptersMsg) (uictx.Screen, tea.Cmd) {
	if msg.err != nil {
		m.stage, m.err = hostFailed, msg.err
		return m, nil
	}
	if len(msg.list) == 0 {
		m.stage = hostFailed
		m.err = errors.New("no network connection found. Connect this PC to Wi-Fi or a cable and try again")
		return m, nil
	}
	m.adapters = msg.list
	if len(msg.list) == 1 {
		return m.chooseAdapter(msg.list[0])
	}
	items := make([]menu.Item, len(msg.list))
	for i, a := range msg.list {
		desc := "Other PCs reach this one at " + a.IP.String()
		if a.Best {
			desc += ". Windows uses it for the internet."
		}
		items[i] = menu.Item{ID: fmt.Sprint(i), Title: a.Name + "  " + a.IP.String(), Desc: desc}
	}
	m.adMenu = menu.New(items)
	m.stage = hostAdapters
	return m, nil
}

// chooseAdapter moves on to the network category check.
func (m hostScreen) chooseAdapter(a host.Adapter) (uictx.Screen, tea.Cmd) {
	m.adapter = a
	m.stage = hostLooking
	mgr, ctx := m.mgr, m.ctx
	return m, tea.Batch(func() tea.Msg {
		c, err := mgr.CategoryOf(ctx, a)
		return hostCategoryMsg{cat: c, err: err}
	}, spinCmd())
}

// onCategory asks before switching a Public network, and starts otherwise.
func (m hostScreen) onCategory(msg hostCategoryMsg) (uictx.Screen, tea.Cmd) {
	if msg.err != nil {
		m.stage, m.err = hostFailed, msg.err
		return m, nil
	}
	m.category = msg.cat
	if msg.cat == host.CategoryPublic {
		m.stage = hostPrivate
		m.confirm = confirm.New(privateID,
			"Switch this network to Private while you share?",
			"Windows blocks file sharing on a Public network. "+
				"Devpit puts it back to Public when you stop sharing.")
		return m, nil
	}
	return m.begin(false)
}

// begin starts the setup on its own goroutine, reporting each step.
func (m hostScreen) begin(makePrivate bool) (uictx.Screen, tea.Cmd) {
	m.private = makePrivate
	m.stage = hostSetup
	m.steps, m.stepAt, m.err = nil, 0, nil
	st := &hostStream{steps: make(chan host.Step, 16), result: make(chan hostStartedMsg, 1)}
	m.stream = st
	mgr, ctx := m.mgr, m.ctx
	opts := host.Options{Path: m.path, Adapter: m.adapter, MakePrivate: makePrivate}
	go func() {
		card, err := mgr.Start(ctx, opts, func(s host.Step) { st.steps <- s })
		st.result <- hostStartedMsg{card: card, err: err}
		close(st.steps)
	}()
	return m, tea.Batch(waitHost(st), spinCmd())
}

// waitHost waits for the next step or the result.
func waitHost(st *hostStream) tea.Cmd {
	return func() tea.Msg {
		if s, ok := <-st.steps; ok {
			return hostStepMsg{stream: st, step: s}
		}
		return <-st.result
	}
}

// onAnswer handles the two dialogs.
func (m hostScreen) onAnswer(msg confirm.AnsweredMsg) (uictx.Screen, tea.Cmd) {
	switch msg.ID {
	case privateID:
		if msg.Answer != confirm.AnswerYes {
			return m, uictx.Pop()
		}
		return m.begin(true)
	case stopShareID:
		if msg.Answer != confirm.AnswerYes {
			m.stage = hostCard
			return m, nil
		}
		return m.stopNow()
	}
	return m, nil
}

// stopNow takes the share down.
func (m hostScreen) stopNow() (uictx.Screen, tea.Cmd) {
	m.stage = hostStopping
	mgr := m.mgr
	return m, tea.Batch(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return hostStoppedMsg{err: mgr.Stop(ctx)}
	}, spinCmd())
}

// onKey routes a key to whatever has focus.
func (m hostScreen) onKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd) {
	switch m.stage {
	case hostPick:
		next, cmd := m.picker.Update(msg)
		m.picker = next
		return m, cmd
	case hostAdapters:
		if msg.Code == tea.KeyEscape {
			return m, uictx.Pop()
		}
		next, cmd := m.adMenu.Update(msg)
		m.adMenu = next
		if it, ok := m.adMenu.Selected(); ok && msg.Code == tea.KeyEnter {
			var i int
			_, _ = fmt.Sscan(it.ID, &i)
			return m.chooseAdapter(m.adapters[i])
		}
		return m, cmd
	case hostPrivate, hostConfirmStop:
		next, cmd := m.confirm.Update(msg)
		m.confirm = next
		return m, cmd
	case hostCard:
		return m.onCardKey(msg)
	case hostStopped:
		if msg.Code == tea.KeyEnter || msg.Code == tea.KeyEscape {
			return m, uictx.Pop()
		}
	case hostFailed:
		switch {
		case msg.Code == tea.KeyEnter && m.stopErr != nil:
			m.stopErr = nil
			return m.stopNow()
		case msg.Code == tea.KeyEnter:
			return m, uictx.Pop()
		}
	case hostLooking, hostSetup, hostStopping:
		// Nothing to press. Esc is handled by Busy and Stop.
	}
	return m, nil
}

// onCardKey handles the keys of the card.
func (m hostScreen) onCardKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd) {
	copyFn := m.deps.Copy
	if copyFn == nil {
		copyFn = copyToClipboard
	}
	switch {
	case msg.Text == "c":
		return m, copyFn(m.card.NetUse)
	case msg.Text == "r":
		return m, copyFn(m.card.Robocopy)
	case msg.Text == "p":
		return m, copyFn(m.card.Password)
	case msg.Text == "s", msg.Code == tea.KeyEscape:
		m.stage = hostConfirmStop
		m.confirm = confirm.New(stopShareID, "Stop sharing?",
			"The other PC can no longer copy. Devpit removes the share, the temporary login and the settings it changed.")
		return m, nil
	}
	return m, nil
}

// View implements uictx.Screen.
func (m hostScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	switch m.stage {
	case hostPick:
		return m.picker.View(ctx)
	case hostLooking:
		return th.Muted.Render(ctx.SpinnerFrame(m.frame) + " Looking at your network…")
	case hostAdapters:
		return th.Muted.Render("This PC has more than one network connection. Which one is the other PC on?") +
			"\n\n" + m.adMenu.View(ctx)
	case hostPrivate, hostConfirmStop:
		return m.confirm.View(ctx)
	case hostSetup:
		return m.setupView(ctx)
	case hostCard:
		return m.cardView(ctx)
	case hostStopping:
		return th.Muted.Render(ctx.SpinnerFrame(m.frame) + " Stopping. Removing everything Devpit set up…")
	case hostStopped:
		return th.Success.Render(ctx.Icons.Tick+" Sharing stopped.") + "\n\n" +
			ctx.Wrap(th.Muted.Render("The share, the temporary login and the firewall and network changes are removed.")) +
			"\n\n" + th.Muted.Render("Press Enter to go back.")
	default:
		return m.failedView(ctx)
	}
}

// setupView draws the setup steps as activity rows.
func (m hostScreen) setupView(ctx uictx.Context) string {
	th := ctx.Theme
	order := []host.Step{
		{Key: "network", Label: "Switch the network to Private"},
		{Key: "firewall", Label: "Allow sharing in the firewall"},
		{Key: "login", Label: "Create a temporary login"},
		{Key: "folder", Label: "Let that login read the folder"},
		{Key: "share", Label: "Share the folder read-only"},
	}
	if !m.private {
		order = order[1:]
	}
	rows := make([]activity.Row, len(order))
	reached := -1
	for i, s := range order {
		for _, seen := range m.steps {
			if seen.Key == s.Key {
				reached = i
			}
		}
	}
	for i, s := range order {
		r := activity.Row{Label: s.Label, Percent: -1}
		switch {
		case i < reached:
			r.State = activity.Done
		case i == reached:
			r.State = activity.Running
		default:
			r.State = activity.Queued
		}
		rows[i] = r
	}
	if reached < 0 && len(rows) > 0 {
		rows[0].State = activity.Running
		rows[0].Detail = "Waiting for the admin prompt"
	}
	head := th.Title.Render("Setting up sharing") + "\n" +
		th.Muted.Render("Windows asks for permission once. Allow it to continue.") + "\n\n"
	return head + activity.View(ctx, rows, m.frame, ctx.Width, max(len(rows), 1))
}

// cardView is the card the other PC's user reads.
func (m hostScreen) cardView(ctx uictx.Context) string {
	th := ctx.Theme
	c := m.card
	label := func(s string) string { return th.Muted.Render(fmt.Sprintf("%-10s", s)) }
	var body strings.Builder
	body.WriteString(th.CardTitle.Render(ctx.Icons.Tick+" Sharing "+fit(c.Path, max(ctx.Width-24, 20))) + "  " +
		th.Muted.Render("(read-only)") + "\n\n")
	body.WriteString(label("Address") + th.Info.Render(c.IP) + th.Muted.Render("  on "+c.Adapter) + "\n")
	for _, a := range m.adapters {
		if a.IP.String() != c.IP {
			body.WriteString(label("") + th.Muted.Render(a.IP.String()+"  on "+a.Name) + "\n")
		}
	}
	body.WriteString(label("Share") + th.Base.Render(c.Share) + "\n")
	body.WriteString(label("User") + th.Base.Render(c.User) + "\n")
	body.WriteString(label("Password") + th.Base.Bold(true).Render(c.Password) + "\n\n")
	body.WriteString(th.Muted.Render("On a PC without Devpit, type these two commands:") + "\n")
	// The commands wrap rather than being cut: they are read off this screen
	// and typed on the other PC, so every character has to be there.
	room := max(ctx.Width-8, 20)
	body.WriteString(th.Base.Render(activity.WrapCommand(c.NetUse, "  ", room)) + "\n")
	body.WriteString(th.Base.Render(activity.WrapCommand(c.Robocopy, "  ", room)) + "\n")
	body.WriteString(th.Muted.Render("The copy lands in a new folder where that terminal is open.") + "\n\n")
	body.WriteString(th.Muted.Render("The login is temporary and can only read this folder."))
	if c.SwitchedNetwork {
		body.WriteString("\n" + th.Muted.Render("The network was switched to Private. It goes back when you stop."))
	}
	card := th.CardFor(ctx.Icons.Tier == "ascii")
	if ctx.Width > 6 {
		card = card.Width(ctx.Width - 2)
	}
	return card.Render(body.String())
}

// failedView explains a failure, or an unfinished teardown.
func (m hostScreen) failedView(ctx uictx.Context) string {
	th := ctx.Theme
	var declined *elevate.DeclinedError
	switch {
	case m.stopErr != nil:
		return th.Danger.Render(ctx.Icons.Fail+" Not everything could be removed.") + "\n\n" +
			ctx.Wrap(th.Muted.Render(oneLine(m.stopErr.Error()))) + "\n\n" +
			ctx.Wrap(th.Base.Render("Press Enter to try again. If it keeps failing, Devpit offers to finish the cleanup the next time it starts."))
	case errors.As(m.err, &declined):
		return th.Warning.Render(ctx.Icons.Warn+" You said no to the admin prompt.") + "\n\n" +
			ctx.Wrap(th.Base.Render("Sharing needs it to create the share. Nothing was changed.")) +
			"\n\n" + th.Muted.Render("Press Enter to go back.")
	default:
		return th.Danger.Render(ctx.Icons.Fail+" Could not start sharing.") + "\n\n" +
			ctx.Wrap(th.Muted.Render(oneLine(m.err.Error()))) + "\n\n" +
			ctx.Wrap(th.Base.Render("Anything that was set up is removed again. Press Enter to go back."))
	}
}
