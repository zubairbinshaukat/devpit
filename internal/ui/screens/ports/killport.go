package ports

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	engine "github.com/zubairbinshaukat/devpit/internal/ports"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/summary"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// killLookupMsg carries the result of checking one port: who (if anyone) is
// listening, and, when there is a listener, its process and parent so the
// confirm dialog can offer "kill process tree" (ports.OffersTree).
type killLookupMsg struct {
	port        uint16
	rows        []engine.Conn
	target      engine.Process
	hasParent   bool
	parent      engine.Process
	descendants int
	err         error
}

// lookupPortCmd checks port and resolves the owning process and its parent in
// one round trip, so the confirm dialog can be built the moment the result
// arrives.
func (m Model) lookupPortCmd(port uint16) tea.Cmd {
	byPortFn := m.byPortFn
	lookupFn := m.lookupFn
	treeFn := m.treeFn
	return func() tea.Msg {
		rows, err := byPortFn(port)
		if err != nil {
			return killLookupMsg{port: port, err: err}
		}
		if len(rows) == 0 {
			return killLookupMsg{port: port}
		}

		target, err := lookupFn(rows[0].PID)
		if err != nil {
			return killLookupMsg{port: port, rows: rows, err: err}
		}
		out := killLookupMsg{port: port, rows: rows, target: target}

		if target.ParentPID != 0 {
			if parent, perr := lookupFn(target.ParentPID); perr == nil {
				out.parent = parent
				out.hasParent = true
				if engine.OffersTree(parent.Name) {
					out.descendants = len(treeFn(parent.PID))
				}
			}
		}
		return out
	}
}

// killDoneMsg carries the outcome of a single kill (plain or tree), including
// whether the port went quiet afterwards.
type killDoneMsg struct {
	port  uint16
	freed bool
	err   error
}

// killCmd kills pid (as a tree when useTree) and then waits up to
// waitFreeTimeout for port to go quiet.
func (m Model) killCmd(port uint16, pid uint32, useTree bool) tea.Cmd {
	killFn := m.killFn
	killTreeFn := m.killTreeFn
	waitFreeFn := m.waitFreeFn
	return func() tea.Msg {
		var err error
		if useTree {
			err = killTreeFn(pid)
		} else {
			err = killFn(pid)
		}
		if err != nil {
			return killDoneMsg{port: port, err: err}
		}
		freed := waitFreeFn(context.Background(), port, waitFreeTimeout)
		return killDoneMsg{port: port, freed: freed}
	}
}

// handleKillLookup reacts to the result of lookupPortCmd: nothing listening,
// an error, a protected process (never offered a confirm, rule 16), or an
// ordinary process ready to confirm.
func (m Model) handleKillLookup(msg killLookupMsg) (uictx.Screen, tea.Cmd) {
	m.loading = false
	m.lastPort = msg.port

	if msg.err != nil {
		m.errMsg = "Could not check port " + strconv.Itoa(int(msg.port)) + ": " + msg.err.Error()
		return m, nil
	}
	if len(msg.rows) == 0 {
		m.killRows = nil
		m.errMsg = ""
		return m, uictx.Status("success", fmt.Sprintf("Port %d is already free", msg.port))
	}

	m.errMsg = ""
	m.killRows = msg.rows
	m.killTarget = msg.target
	m.killParent = engine.Process{}
	m.killOfferTree = false
	m.killDescendants = 0
	if msg.hasParent {
		m.killParent = msg.parent
		if engine.OffersTree(msg.parent.Name) {
			m.killOfferTree = true
			m.killDescendants = msg.descendants
		}
	}

	if m.killTarget.Protected {
		// Rule 16: a protected process is shown, never offered a kill.
		m.stage = stageKillInput
		return m, nil
	}

	question := fmt.Sprintf("Kill %s (PID %d) on port %d?", displayName(m.killTarget), m.killTarget.PID, msg.port)
	detail := "This stops the process immediately. Dev servers can usually be restarted."
	m.killConfirm = confirm.New(killConfirmID, question, detail)
	m.killTreeOn = false
	m.stage = stageKillConfirm
	return m, nil
}

// handleKillAnswer reacts to the confirm dialog for a single port.
func (m Model) handleKillAnswer(msg confirm.AnsweredMsg) (uictx.Screen, tea.Cmd) {
	if msg.Answer == confirm.AnswerNo {
		m.stage = stageKillInput
		m.killConfirm = m.killConfirm.Reset()
		return m, nil
	}

	pid := m.killTarget.PID
	useTree := m.killOfferTree && m.killTreeOn
	if useTree {
		pid = m.killParent.PID
	}
	m.stage = stageKilling
	m.loading = true
	return m, m.killCmd(m.lastPort, pid, useTree)
}

// handleKillDone builds the summary for a single-port kill.
func (m Model) handleKillDone(msg killDoneMsg) (uictx.Screen, tea.Cmd) {
	m.loading = false
	m.stage = stageSummary

	if msg.err != nil {
		reason, denied := describeKillErr(msg.err)
		m.summaryModel = summary.New(summary.Result{
			Headline: fmt.Sprintf("Could not kill the process on port %d", msg.port),
			Failed:   true,
			Skipped:  []summary.Skipped{{Name: displayName(m.killTarget), Reason: reason}},
		})
		if denied {
			return m, uictx.Status("warning", accessDeniedStatus)
		}
		return m, nil
	}

	if msg.freed {
		m.summaryModel = summary.New(summary.Result{
			Headline: fmt.Sprintf("Port %d is free", msg.port),
			Items:    1,
		})
		return m, nil
	}

	m.summaryModel = summary.New(summary.Result{
		Headline: fmt.Sprintf("Port %d is still busy", msg.port),
		Failed:   true,
		Skipped: []summary.Skipped{{
			Name:   displayName(m.killTarget),
			Reason: "the process was killed but something is still listening",
		}},
	})
	return m, nil
}

// updateKillInput handles the digit field: digits only, 1-65535, Enter to
// check the port.
func (m Model) updateKillInput(msg tea.Msg) (uictx.Screen, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok || m.loading {
		return m, nil
	}

	switch {
	case key.Matches(km, m.keys.Select):
		port, err := parsePort(m.portDigits)
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.loading = true
		m.lastPort = port
		return m, m.lookupPortCmd(port)

	case key.Matches(km, m.keys.Backspace):
		if n := len(m.portDigits); n > 0 {
			m.portDigits = m.portDigits[:n-1]
		}
		return m, nil

	default:
		t := km.Key().Text
		if len(t) == 1 && t[0] >= '0' && t[0] <= '9' && len(m.portDigits) < 5 {
			m.portDigits += t
		}
		return m, nil
	}
}

// updateKillConfirm forwards to the confirm dialog, first handling the
// kill-tree toggle when it is offered.
func (m Model) updateKillConfirm(msg tea.Msg) (uictx.Screen, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if m.killOfferTree && key.Matches(km, m.keys.ToggleTree) {
		m.killTreeOn = !m.killTreeOn
		return m, nil
	}
	next, cmd := m.killConfirm.Update(msg)
	m.killConfirm = next
	return m, cmd
}

// viewKillInput renders the port field, any listener rows found for it, and a
// protected reason when the listener can never be killed.
func (m Model) viewKillInput(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	b.WriteString(th.Subtitle.Render("Kill a port"))
	b.WriteString("\n\n")
	b.WriteString(th.Base.Render("Port: "))
	b.WriteString(th.Accent.Render(m.portDigits))
	if m.loading {
		b.WriteString(th.Muted.Render("  checking…"))
	}
	b.WriteString("\n")
	b.WriteString(th.Muted.Render("Digits only, 1-65535. Enter to check."))

	if m.errMsg != "" {
		b.WriteString("\n\n")
		b.WriteString(th.Danger.Render(ctx.Icons.Warn + " " + m.errMsg))
	}

	if len(m.killRows) > 0 {
		b.WriteString("\n\n")
		b.WriteString(m.renderKillRows(ctx))
		if m.killTarget.Protected {
			b.WriteString("\n\n")
			b.WriteString(th.Danger.Render(ctx.Icons.Warn + " " + displayName(m.killTarget) + " is protected"))
			b.WriteString("\n")
			b.WriteString(th.Muted.Render(m.killTarget.ProtectedReason))
		}
	}

	return b.String()
}

// viewKillConfirm renders the listener rows above the confirm dialog.
func (m Model) viewKillConfirm(ctx uictx.Context) string {
	var b strings.Builder
	b.WriteString(m.renderKillRows(ctx))
	b.WriteString("\n\n")
	b.WriteString(m.killConfirm.View(ctx))
	if m.killOfferTree {
		b.WriteString("\n\n")
		b.WriteString(m.renderTreeToggle(ctx, m.killTreeOn, m.killParent.Name, m.killDescendants))
	}
	if m.stage == stageKilling {
		b.WriteString("\n\n")
		b.WriteString(ctx.Theme.Muted.Render("Killing…"))
	}
	return b.String()
}

// renderKillRows draws the listener table for the kill-a-port flow: proto,
// local address, state, PID and process name.
func (m Model) renderKillRows(ctx uictx.Context) string {
	th := ctx.Theme
	header := []string{"PROTO", "LOCAL", "STATE", "PID", "PROCESS"}
	widths := []int{6, 22, 12, 8, 20}
	var b strings.Builder
	b.WriteString(th.Muted.Render(padColumns(header, widths)))
	for _, c := range m.killRows {
		name := displayName(m.killTarget)
		if c.PID != m.killTarget.PID {
			name = fmt.Sprintf("PID %d", c.PID)
		}
		cells := []string{
			c.Proto,
			formatAddr(c.LocalAddr.String(), c.LocalPort),
			c.State,
			strconv.FormatUint(uint64(c.PID), 10),
			name,
		}
		b.WriteString("\n")
		b.WriteString(th.Base.Render(padColumns(cells, widths)))
	}
	return b.String()
}
