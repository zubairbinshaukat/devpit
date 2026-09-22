package ports

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	engine "github.com/zubairbinshaukat/devpit/internal/ports"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/summary"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// rowsMsg carries a freshly fetched table for the busy-dev-ports or
// node-processes flow.
type rowsMsg struct {
	kind listKind
	rows []row
	err  error
}

// rowFromConn builds a busy-dev-ports row from a LISTEN connection, resolving
// its process and, when it has one, the process's parent (for the kill-tree
// offer).
func rowFromConn(c engine.Conn, lookupFn func(uint32) (engine.Process, error)) row {
	proc, _ := lookupFn(c.PID)
	r := row{
		PID:             c.PID,
		Name:            proc.Name,
		ParentPID:       proc.ParentPID,
		Proto:           c.Proto,
		Local:           formatAddr(c.LocalAddr.String(), c.LocalPort),
		Protected:       proc.Protected,
		ProtectedReason: proc.ProtectedReason,
	}
	if r.Name == "" {
		r.Name = fmt.Sprintf("PID %d", c.PID)
	}
	if proc.ParentPID != 0 {
		if parent, err := lookupFn(proc.ParentPID); err == nil {
			r.ParentName = parent.Name
		}
	}
	return r
}

// rowFromProcess builds a node-processes row, resolving the parent name the
// same way rowFromConn does.
func rowFromProcess(proc engine.Process, lookupFn func(uint32) (engine.Process, error)) row {
	r := row{
		PID:             proc.PID,
		Name:            proc.Name,
		ParentPID:       proc.ParentPID,
		Protected:       proc.Protected,
		ProtectedReason: proc.ProtectedReason,
	}
	if r.Name == "" {
		r.Name = fmt.Sprintf("PID %d", proc.PID)
	}
	if proc.ParentPID != 0 {
		if parent, err := lookupFn(proc.ParentPID); err == nil {
			r.ParentName = parent.Name
		}
	}
	return r
}

// fetchBusyCmd lists every connection, keeps the LISTEN rows on devPorts
// (ctx.Config.DevPorts, falling back to ports.DefaultDevPorts) and resolves
// each one's process.
func (m Model) fetchBusyCmd(devPorts []uint16) tea.Cmd {
	listFn := m.listFn
	lookupFn := m.lookupFn
	return func() tea.Msg {
		conns, err := listFn(context.Background())
		if err != nil {
			return rowsMsg{kind: listKindBusy, err: err}
		}
		busy := engine.Busy(conns, devPorts)
		sort.Slice(busy, func(i, j int) bool { return busy[i].LocalPort < busy[j].LocalPort })

		out := make([]row, 0, len(busy))
		for _, c := range busy {
			out = append(out, rowFromConn(c, lookupFn))
		}
		return rowsMsg{kind: listKindBusy, rows: out}
	}
}

// fetchNodeCmd lists every connection's owning PID, then keeps the processes
// named node, npm, pnpm or yarn (plan.md section 8: "via List PIDs +
// Lookup").
func (m Model) fetchNodeCmd() tea.Cmd {
	listFn := m.listFn
	lookupFn := m.lookupFn
	return func() tea.Msg {
		conns, err := listFn(context.Background())
		if err != nil {
			return rowsMsg{kind: listKindNode, err: err}
		}

		seen := make(map[uint32]bool, len(conns))
		pids := make([]uint32, 0, len(conns))
		for _, c := range conns {
			if !seen[c.PID] {
				seen[c.PID] = true
				pids = append(pids, c.PID)
			}
		}
		sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })

		out := make([]row, 0, len(pids))
		for _, pid := range pids {
			proc, err := lookupFn(pid)
			if err != nil || !isNodeProcess(proc.Name) {
				continue
			}
			out = append(out, rowFromProcess(proc, lookupFn))
		}
		return rowsMsg{kind: listKindNode, rows: out}
	}
}

// batchKillDoneMsg carries the outcome of killing every marked row.
type batchKillDoneMsg struct {
	kind         listKind
	killed       int
	total        int
	skipped      []summary.Skipped
	accessDenied bool
}

// batchKillCmd kills every row in marked, one at a time. When useTree is set
// and a row's parent is a tree-offering process (npm, node, ...), the whole
// tree rooted at the parent is killed instead of just the row's own PID, so
// npm does not simply respawn the child.
func (m Model) batchKillCmd(marked []row, useTree bool) tea.Cmd {
	killFn := m.killFn
	killTreeFn := m.killTreeFn
	kind := m.kind
	return func() tea.Msg {
		var skipped []summary.Skipped
		killed := 0
		accessDenied := false

		for _, r := range marked {
			var err error
			if useTree && r.ParentName != "" && engine.OffersTree(r.ParentName) {
				err = killTreeFn(r.ParentPID)
			} else {
				err = killFn(r.PID)
			}
			if err != nil {
				reason, denied := describeKillErr(err)
				if denied {
					accessDenied = true
				}
				skipped = append(skipped, summary.Skipped{Name: rowLabel(r), Reason: reason})
				continue
			}
			killed++
		}
		return batchKillDoneMsg{kind: kind, killed: killed, total: len(marked), skipped: skipped, accessDenied: accessDenied}
	}
}

// handleRows stores a freshly fetched table, ignoring a stale response from a
// flow the user has since navigated away from.
func (m Model) handleRows(msg rowsMsg) (uictx.Screen, tea.Cmd) {
	if msg.kind != m.kind || m.stage != stageList {
		return m, nil
	}
	m.loading = false
	if msg.err != nil {
		m.errMsg = msg.err.Error()
		m.rows = nil
		return m, nil
	}
	m.errMsg = ""
	m.rows = msg.rows
	m.cursor = 0
	m.marked = map[uint32]bool{}
	return m, nil
}

// handleListAnswer reacts to the confirm dialog for the marked rows.
func (m Model) handleListAnswer(msg confirm.AnsweredMsg) (uictx.Screen, tea.Cmd) {
	if msg.Answer == confirm.AnswerNo {
		m.stage = stageList
		m.listConfirm = m.listConfirm.Reset()
		return m, nil
	}
	marked := m.markedRows()
	m.stage = stageKillingList
	m.loading = true
	return m, m.batchKillCmd(marked, m.listTreeOn)
}

// handleBatchDone builds the summary for a marked-rows kill.
func (m Model) handleBatchDone(msg batchKillDoneMsg) (uictx.Screen, tea.Cmd) {
	if msg.kind != m.kind {
		return m, nil
	}
	m.loading = false
	m.stage = stageSummary
	m.summaryModel = summary.New(summary.Result{
		Headline: fmt.Sprintf("Killed %d of %d", msg.killed, msg.total),
		Items:    msg.killed,
		Skipped:  msg.skipped,
		Failed:   msg.total > 0 && msg.killed == 0,
	})
	if msg.accessDenied {
		return m, uictx.Status("warning", accessDeniedStatus)
	}
	return m, nil
}

// markedRows returns the rows currently ticked, in table order.
func (m Model) markedRows() []row {
	var out []row
	for _, r := range m.rows {
		if m.marked[r.PID] {
			out = append(out, r)
		}
	}
	return out
}

// updateList handles navigation, marking and the actions on the table.
func (m Model) updateList(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok || m.loading {
		return m, nil
	}

	switch {
	case key.Matches(km, m.keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case key.Matches(km, m.keys.Down):
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
		return m, nil

	case key.Matches(km, m.keys.Space):
		if m.cursor >= 0 && m.cursor < len(m.rows) {
			r := m.rows[m.cursor]
			if !r.Protected {
				if m.marked == nil {
					m.marked = map[uint32]bool{}
				}
				m.marked[r.PID] = !m.marked[r.PID]
			}
		}
		return m, nil

	case key.Matches(km, m.keys.Refresh):
		m.loading = true
		m.rows = nil
		if m.kind == listKindBusy {
			return m, m.fetchBusyCmd(configPorts(ctx.Config.DevPorts))
		}
		return m, m.fetchNodeCmd()

	case key.Matches(km, m.keys.Kill):
		marked := m.markedRows()
		if len(marked) == 0 {
			return m, nil
		}
		return m.confirmKillMarked(marked), nil
	}
	return m, nil
}

// confirmKillMarked builds the confirm dialog for the marked rows, offering
// kill-tree when at least one marked row's parent qualifies.
func (m Model) confirmKillMarked(marked []row) Model {
	m.listOfferTree = false
	for _, r := range marked {
		if r.ParentName != "" && engine.OffersTree(r.ParentName) {
			m.listOfferTree = true
			break
		}
	}
	m.listTreeOn = false
	question := fmt.Sprintf("Kill %d marked process(es)?", len(marked))
	detail := "This stops them immediately. Dev servers can usually be restarted."
	m.listConfirm = confirm.New(listConfirmID, question, detail)
	m.stage = stageListConfirm
	return m
}

// updateListConfirm forwards to the confirm dialog, first handling the
// kill-tree toggle when it is offered.
func (m Model) updateListConfirm(msg tea.Msg) (uictx.Screen, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	if m.listOfferTree && key.Matches(km, m.keys.ToggleTree) {
		m.listTreeOn = !m.listTreeOn
		return m, nil
	}
	next, cmd := m.listConfirm.Update(msg)
	m.listConfirm = next
	return m, cmd
}

// viewList renders the table for the busy-dev-ports or node-processes flow.
func (m Model) viewList(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	title := "Busy dev ports"
	if m.kind == listKindNode {
		title = "Node processes"
	}
	b.WriteString(th.Subtitle.Render(title))
	b.WriteString("\n\n")

	if m.loading {
		b.WriteString(th.Muted.Render("Checking…"))
		return b.String()
	}
	if m.errMsg != "" {
		b.WriteString(th.Danger.Render(ctx.Icons.Warn + " " + m.errMsg))
		return b.String()
	}
	if len(m.rows) == 0 {
		b.WriteString(th.Muted.Render("Nothing found. Press r to refresh."))
		return b.String()
	}

	for i, r := range m.rows {
		b.WriteString(m.renderListRow(ctx, i, r))
		b.WriteString("\n")
	}

	marked := len(m.markedRows())
	b.WriteString("\n")
	b.WriteString(th.Muted.Render(fmt.Sprintf("Marked: %d of %d", marked, len(m.rows))))
	return b.String()
}

// renderListRow draws one row: a cursor, a checkbox (or the protected
// reason, greyed, in its place), PID, name and parent.
func (m Model) renderListRow(ctx uictx.Context, i int, r row) string {
	th := ctx.Theme
	selected := i == m.cursor

	cursor := " "
	if selected {
		cursor = ctx.Icons.Cursor
	}

	box := ctx.Icons.Unchecked
	if m.marked[r.PID] {
		box = ctx.Icons.Checked
	}

	loc := r.Local
	if loc == "" {
		loc = "-"
	}
	parent := r.ParentName
	if parent == "" {
		parent = "-"
	}

	line := fmt.Sprintf("%s %s  %-21s  PID %-8d %-16s parent %s",
		cursor, box, loc, r.PID, r.Name, parent)

	style := th.Base
	if selected {
		style = th.Selected
	}
	out := style.Render(ctx.Truncate(line))

	if r.Protected {
		out += "\n  " + th.Muted.Render(ctx.Icons.Warn+" protected — "+r.ProtectedReason)
	}
	return out
}

// viewListConfirm renders the marked rows above the confirm dialog.
func (m Model) viewListConfirm(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	for _, r := range m.markedRows() {
		b.WriteString(th.Base.Render("  " + rowLabel(r)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(m.listConfirm.View(ctx))
	if m.listOfferTree {
		b.WriteString("\n\n")
		b.WriteString(m.renderTreeToggle(ctx, m.listTreeOn, "", 0))
	}
	if m.stage == stageKillingList {
		b.WriteString("\n\n")
		b.WriteString(th.Muted.Render("Killing…"))
	}
	return b.String()
}
