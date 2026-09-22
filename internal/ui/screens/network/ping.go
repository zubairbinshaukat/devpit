package network

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/network"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// pingTickInterval is how often the running screen drains streamed ping
// lines into the viewport, matching the plan's 150 ms scan-stream cadence.
const pingTickInterval = 150 * time.Millisecond

// defaultPingHost is what an empty host field falls back to.
const defaultPingHost = "1.1.1.1"

// pingCount is how many echo requests each run sends.
const pingCount = 4

// pingState is where the ping screen is in its input → running → done flow.
type pingState int

const (
	pingStateInput pingState = iota
	pingStateRunning
	pingStateDone
)

// pingLineBuffer is a thread-safe buffer for lines streamed from a running
// ping. [network.Ping]'s onLine callback runs on the goroutine backing the
// Cmd that calls it; drain runs on Bubble Tea's update loop from a 150 ms
// tick, so the two sides need a lock between them.
type pingLineBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (b *pingLineBuffer) add(s string) {
	b.mu.Lock()
	b.lines = append(b.lines, s)
	b.mu.Unlock()
}

func (b *pingLineBuffer) drain() []string {
	b.mu.Lock()
	out := b.lines
	b.lines = nil
	b.mu.Unlock()
	return out
}

// pingTickMsg asks the screen to drain whatever lines have arrived since the
// last tick. buf identifies which run it belongs to, so a stray tick from a
// run the user has already left behind is ignored.
type pingTickMsg struct{ buf *pingLineBuffer }

// pingDoneMsg carries the final result once network.Ping returns.
type pingDoneMsg struct {
	result network.PingResult
	err    error
}

// pingRunFunc matches network.Ping's signature and is what tests replace to
// avoid ever executing the real ping binary.
type pingRunFunc func(ctx context.Context, host string, count int, onLine func(string), opts network.Options) (network.PingResult, error)

// pingModel is the "Ping a host" screen.
type pingModel struct {
	runFn pingRunFunc

	state  pingState
	input  textinput.Model
	view   viewport.Model
	lines  []string
	buf    *pingLineBuffer
	result network.PingResult
	err    error

	initCmd tea.Cmd
	submit  key.Binding
	cont    key.Binding
}

func newPingScreen() pingModel {
	ti := textinput.New()
	ti.Placeholder = defaultPingHost
	ti.SetValue(defaultPingHost)
	ti.CursorEnd()
	focusCmd := ti.Focus()

	vp := viewport.New(viewport.WithWidth(70), viewport.WithHeight(8))

	return pingModel{
		runFn:   network.Ping,
		state:   pingStateInput,
		input:   ti,
		view:    vp,
		initCmd: focusCmd,
		submit:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "ping")),
		cont:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "back")),
	}
}

// Init implements uictx.Screen.
func (m pingModel) Init() tea.Cmd { return m.initCmd }

// Title implements uictx.Screen.
func (m pingModel) Title() string { return "Ping a host" }

// ShortHelp implements uictx.Screen.
func (m pingModel) ShortHelp() []key.Binding {
	switch m.state {
	case pingStateInput:
		return []key.Binding{m.submit}
	case pingStateDone:
		return []key.Binding{m.cont}
	default:
		return nil
	}
}

// FullHelp implements uictx.Screen.
func (m pingModel) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

// Update implements uictx.Screen.
func (m pingModel) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case pingTickMsg:
		if m.state != pingStateRunning || msg.buf != m.buf {
			return m, nil
		}
		m.drainInto()
		return m, tickCmd(m.buf)

	case pingDoneMsg:
		if m.state != pingStateRunning {
			return m, nil
		}
		m.drainInto()
		m.result = msg.result
		m.err = msg.err
		m.state = pingStateDone
		return m, nil

	case tea.KeyPressMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *pingModel) drainInto() {
	if lines := m.buf.drain(); len(lines) > 0 {
		m.lines = append(m.lines, lines...)
		m.view.SetContent(strings.Join(m.lines, "\n"))
		m.view.GotoBottom()
	}
}

func (m pingModel) updateKey(msg tea.KeyPressMsg) (uictx.Screen, tea.Cmd) {
	switch m.state {
	case pingStateInput:
		if key.Matches(msg, m.submit) {
			host := strings.TrimSpace(m.input.Value())
			if host == "" {
				host = defaultPingHost
			}
			m.state = pingStateRunning
			m.buf = &pingLineBuffer{}
			m.lines = nil
			m.view.SetContent("")
			return m, tea.Batch(startPingCmd(m.runFn, host, m.buf), tickCmd(m.buf))
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case pingStateRunning:
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd

	case pingStateDone:
		if key.Matches(msg, m.cont) {
			return m, uictx.Pop()
		}
	}
	return m, nil
}

// startPingCmd runs the (possibly fake) ping function on its own goroutine
// and reports the final result. Every line it streams via onLine lands in
// buf, drained by the periodic tick rather than by this command.
func startPingCmd(runFn pingRunFunc, host string, buf *pingLineBuffer) tea.Cmd {
	return func() tea.Msg {
		result, err := runFn(context.Background(), host, pingCount, buf.add, network.Options{})
		return pingDoneMsg{result: result, err: err}
	}
}

// tickCmd schedules the next drain. It is rescheduled by the tick handler
// itself for as long as the run is still in progress.
func tickCmd(buf *pingLineBuffer) tea.Cmd {
	return tea.Tick(pingTickInterval, func(time.Time) tea.Msg { return pingTickMsg{buf: buf} })
}

// View implements uictx.Screen.
func (m pingModel) View(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	switch m.state {
	case pingStateInput:
		b.WriteString(th.CardTitle.Render("Host to ping"))
		b.WriteString("\n")
		b.WriteString(m.input.View())

	case pingStateRunning:
		b.WriteString(th.CardTitle.Render("Pinging…"))
		b.WriteString("\n")
		b.WriteString(m.view.View())

	case pingStateDone:
		m.renderDone(ctx, &b)
	}

	return th.Card.Render(b.String())
}

func (m pingModel) renderDone(ctx uictx.Context, b *strings.Builder) {
	th := ctx.Theme
	if m.err != nil {
		b.WriteString(th.Danger.Render(ctx.Icons.Fail + " " + m.err.Error()))
		return
	}

	r := m.result
	mark := th.Success.Render(ctx.Icons.Tick)
	if r.Received == 0 {
		mark = th.Danger.Render(ctx.Icons.Fail)
	}
	b.WriteString(mark)
	b.WriteString(" ")
	b.WriteString(th.CardTitle.Render(fmt.Sprintf("Sent %d, received %d", r.Sent, r.Received)))
	b.WriteString("\n")
	b.WriteString(th.Base.Render(fmt.Sprintf("min %.0fms  avg %.0fms  max %.0fms", r.MinMs, r.AvgMs, r.MaxMs)))
}
