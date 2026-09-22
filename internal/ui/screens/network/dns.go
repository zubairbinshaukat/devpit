package network

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/network"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// confirmFlushDNSID identifies the flush-DNS confirmation dialog.
const confirmFlushDNSID = "flush-dns"

// dnsState is where the flush-DNS screen is in its confirm → running → done
// flow.
type dnsState int

const (
	dnsStateConfirm dnsState = iota
	dnsStateRunning
	dnsStateDone
)

// dnsResultMsg carries the outcome of the flush.
type dnsResultMsg struct {
	output string
	err    error
}

// dnsModel is the "Flush DNS cache" screen. Safety rule 2 (default No):
// it opens straight into a [confirm.Model], which never defaults to yes, so
// nothing runs until the user explicitly confirms.
type dnsModel struct {
	flushFn func(context.Context) (string, error)

	state   dnsState
	confirm confirm.Model
	output  string
	err     error

	cont key.Binding
}

func newDNSScreen() dnsModel {
	return dnsModel{
		flushFn: func(ctx context.Context) (string, error) { return network.FlushDNS(ctx, network.Options{}) },
		state:   dnsStateConfirm,
		confirm: confirm.New(confirmFlushDNSID, "Flush the DNS resolver cache?", "Clears cached lookups; nothing else on the machine changes."),
		cont:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "back")),
	}
}

// Init implements uictx.Screen.
func (m dnsModel) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (m dnsModel) Title() string { return "Flush DNS cache" }

// ShortHelp implements uictx.Screen.
func (m dnsModel) ShortHelp() []key.Binding {
	switch m.state {
	case dnsStateConfirm:
		return m.confirm.Keys.ShortHelp()
	case dnsStateDone:
		return []key.Binding{m.cont}
	default:
		return nil
	}
}

// FullHelp implements uictx.Screen.
func (m dnsModel) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

// Update implements uictx.Screen.
func (m dnsModel) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case confirm.AnsweredMsg:
		if msg.ID != confirmFlushDNSID {
			return m, nil
		}
		if msg.Answer == confirm.AnswerYes {
			m.state = dnsStateRunning
			return m, m.flushCmd()
		}
		return m, uictx.Pop()

	case dnsResultMsg:
		m.state = dnsStateDone
		m.output = msg.output
		m.err = msg.err
		return m, nil

	case tea.KeyPressMsg:
		switch m.state {
		case dnsStateConfirm:
			var cmd tea.Cmd
			m.confirm, cmd = m.confirm.Update(msg)
			return m, cmd
		case dnsStateDone:
			if key.Matches(msg, m.cont) {
				return m, uictx.Pop()
			}
		}
	}
	return m, nil
}

func (m dnsModel) flushCmd() tea.Cmd {
	fn := m.flushFn
	return func() tea.Msg {
		out, err := fn(context.Background())
		return dnsResultMsg{output: out, err: err}
	}
}

// View implements uictx.Screen.
func (m dnsModel) View(ctx uictx.Context) string {
	th := ctx.Theme

	switch m.state {
	case dnsStateConfirm:
		return m.confirm.View(ctx)
	case dnsStateRunning:
		return th.Card.Render(th.Muted.Render("Flushing DNS cache…"))
	default:
		if m.err != nil {
			return th.Card.Render(th.Danger.Render(ctx.Icons.Fail + " " + m.err.Error()))
		}
		var b strings.Builder
		b.WriteString(th.Success.Render(ctx.Icons.Tick + " DNS cache flushed."))
		if out := strings.TrimSpace(m.output); out != "" {
			b.WriteString("\n\n")
			b.WriteString(th.Muted.Render(out))
		}
		return th.Card.Render(b.String())
	}
}
