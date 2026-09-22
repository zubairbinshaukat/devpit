// Package ports is the "Fix Stuck Ports & Apps" screen: kill whatever holds a
// port you type, list busy dev ports, or list and stop Node processes.
//
// Every call into the ports engine goes through a function field on [Model]
// (listFn, byPortFn, lookupFn, treeFn, killFn, killTreeFn, waitFreeFn),
// defaulted in [New] to the real internal/ports functions and run inside a
// [tea.Cmd] so Update never blocks. Tests replace the fields with fakes and
// never kill a real process.
//
// The screen is one [uictx.Screen] pushed once from the home menu; its three
// flows (kill a port, busy dev ports, node processes) are internal states
// rather than separate pushes, because Esc is intercepted globally by the
// router and always pops the whole screen (docs/handoff.md, "Esc pops one
// level" means one router entry, not one internal step). That is safe here:
// nothing destructive happens without an explicit "y" on a confirm dialog
// whose default answer is always No (docs/safety.md rule 2).
//
// Protected processes (docs/safety.md rule 16 — PID 0/4, System32 binaries,
// Windows services) are shown with their reason in place of a checkbox and
// can never be marked or confirmed for a kill.
package ports

import (
	"context"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	engine "github.com/zubairbinshaukat/devpit/internal/ports"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/summary"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// waitFreeTimeout is how long the single-port kill flow waits for the port to
// go quiet before reporting it still busy (plan.md section 8: "re-query the
// port for 2s").
const waitFreeTimeout = 2 * time.Second

// Submenu item identifiers.
const (
	itemKillPort  = "kill-port"
	itemBusyPorts = "busy-ports"
	itemNodeProcs = "node-procs"
)

// Confirm dialog identifiers.
const (
	killConfirmID = "ports-kill-one"
	listConfirmID = "ports-kill-many"
)

// stage is the screen's internal state machine.
type stage int

// The stages, in the order a user normally passes through them.
const (
	stageSubmenu stage = iota
	stageKillInput
	stageKillConfirm
	stageKilling
	stageList
	stageListConfirm
	stageKillingList
	stageSummary
)

// listKind distinguishes the two table-based flows, which share almost all of
// their update and view logic.
type listKind int

// The two list kinds.
const (
	listKindBusy listKind = iota
	listKindNode
)

// row is one line of a busy-dev-ports or node-processes table.
type row struct {
	PID             uint32
	Name            string
	ParentPID       uint32
	ParentName      string
	Proto           string
	Local           string
	Protected       bool
	ProtectedReason string
}

// keyMap holds the bindings this screen answers to, beyond the ones the
// embedded menu and confirm components already carry.
type keyMap struct {
	Up         key.Binding
	Down       key.Binding
	Select     key.Binding
	Space      key.Binding
	Kill       key.Binding
	Refresh    key.Binding
	ToggleTree key.Binding
	Backspace  key.Binding
}

// defaultKeyMap returns the screen's own bindings.
func defaultKeyMap() keyMap {
	return keyMap{
		Up:         key.NewBinding(key.WithKeys("up"), key.WithHelp("↑↓", "move")),
		Down:       key.NewBinding(key.WithKeys("down")),
		Select:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select")),
		Space:      key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "mark")),
		Kill:       key.NewBinding(key.WithKeys("k"), key.WithHelp("k", "kill marked")),
		Refresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		ToggleTree: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "kill tree")),
		Backspace:  key.NewBinding(key.WithKeys("backspace"), key.WithHelp("backspace", "delete digit")),
	}
}

// Model is the ports screen.
type Model struct {
	// Engine hooks. Every one of these is called from inside a tea.Cmd, never
	// from Update directly.
	listFn     func(context.Context) ([]engine.Conn, error)
	byPortFn   func(uint16) ([]engine.Conn, error)
	lookupFn   func(uint32) (engine.Process, error)
	treeFn     func(uint32) []engine.Process
	killFn     func(uint32) error
	killTreeFn func(uint32) error
	waitFreeFn func(context.Context, uint16, time.Duration) bool

	keys    keyMap
	submenu menu.Model
	stage   stage
	loading bool
	errMsg  string

	// Kill-a-port flow.
	portDigits      string
	lastPort        uint16
	killRows        []engine.Conn
	killTarget      engine.Process
	killParent      engine.Process
	killOfferTree   bool
	killTreeOn      bool
	killDescendants int
	killConfirm     confirm.Model

	// Busy-dev-ports / node-processes flow.
	kind          listKind
	rows          []row
	cursor        int
	marked        map[uint32]bool
	listOfferTree bool
	listTreeOn    bool
	listConfirm   confirm.Model

	summaryModel summary.Model
}

// New returns the ports screen wired to the real internal/ports engine. Push
// it from the home menu with uictx.Push(ports.New()).
func New() Model {
	return Model{
		listFn:     engine.List,
		byPortFn:   engine.ByPort,
		lookupFn:   engine.Lookup,
		treeFn:     engine.Tree,
		killFn:     engine.Kill,
		killTreeFn: engine.KillTree,
		waitFreeFn: engine.WaitFree,
		keys:       defaultKeyMap(),
		submenu:    menu.New(submenuItems()),
		marked:     map[uint32]bool{},
	}
}

// submenuItems returns the three entry points, in the order the plan lists
// them.
func submenuItems() []menu.Item {
	return []menu.Item{
		{
			ID:    itemKillPort,
			Title: "Kill a port",
			Desc:  "Type a port number and free whatever is listening on it",
		},
		{
			ID:    itemBusyPorts,
			Title: "Busy dev ports",
			Desc:  "Check every configured dev port at once",
		},
		{
			ID:    itemNodeProcs,
			Title: "Node processes",
			Desc:  "List and stop node, npm, pnpm or yarn",
		},
	}
}

// Init implements uictx.Screen. There is nothing to start until the user
// picks a submenu item.
func (m Model) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (m Model) Title() string { return "Ports" }

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch tm := msg.(type) {
	case menu.SelectedMsg:
		if m.stage == stageSubmenu {
			return m.selectSubmenu(tm.ID, ctx)
		}
		return m, nil
	case killLookupMsg:
		return m.handleKillLookup(tm)
	case killDoneMsg:
		return m.handleKillDone(tm)
	case rowsMsg:
		return m.handleRows(tm)
	case batchKillDoneMsg:
		return m.handleBatchDone(tm)
	case confirm.AnsweredMsg:
		switch tm.ID {
		case killConfirmID:
			return m.handleKillAnswer(tm)
		case listConfirmID:
			return m.handleListAnswer(tm)
		}
		return m, nil
	case summary.DismissedMsg:
		return m, uictx.Pop()
	}

	switch m.stage {
	case stageSubmenu:
		return m.updateSubmenu(msg)
	case stageKillInput:
		return m.updateKillInput(msg)
	case stageKillConfirm:
		return m.updateKillConfirm(msg)
	case stageList:
		return m.updateList(msg, ctx)
	case stageListConfirm:
		return m.updateListConfirm(msg)
	case stageSummary:
		return m.updateSummary(msg)
	default:
		// stageKilling and stageKillingList wait for their cmd's result and
		// take no keys meanwhile.
		return m, nil
	}
}

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	switch m.stage {
	case stageKillInput:
		return m.viewKillInput(ctx)
	case stageKillConfirm, stageKilling:
		return m.viewKillConfirm(ctx)
	case stageList:
		return m.viewList(ctx)
	case stageListConfirm, stageKillingList:
		return m.viewListConfirm(ctx)
	case stageSummary:
		return m.summaryModel.View(ctx)
	default:
		return m.viewSubmenu(ctx)
	}
}

// updateSubmenu forwards to the menu component.
func (m Model) updateSubmenu(msg tea.Msg) (uictx.Screen, tea.Cmd) {
	next, cmd := m.submenu.Update(msg)
	m.submenu = next
	return m, cmd
}

// updateSummary forwards to the summary card.
func (m Model) updateSummary(msg tea.Msg) (uictx.Screen, tea.Cmd) {
	next, cmd := m.summaryModel.Update(msg)
	m.summaryModel = next
	return m, cmd
}

// selectSubmenu starts one of the three flows.
func (m Model) selectSubmenu(id string, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch id {
	case itemKillPort:
		port := m.prefillPort(ctx)
		m.stage = stageKillInput
		m.portDigits = strconv.Itoa(int(port))
		m.errMsg = ""
		m.loading = true
		m.killRows = nil
		return m, m.lookupPortCmd(port)

	case itemBusyPorts:
		m.stage = stageList
		m.kind = listKindBusy
		m.rows = nil
		m.marked = map[uint32]bool{}
		m.cursor = 0
		m.errMsg = ""
		m.loading = true
		return m, m.fetchBusyCmd(configPorts(ctx.Config.DevPorts))

	case itemNodeProcs:
		m.stage = stageList
		m.kind = listKindNode
		m.rows = nil
		m.marked = map[uint32]bool{}
		m.cursor = 0
		m.errMsg = ""
		m.loading = true
		return m, m.fetchNodeCmd()
	}
	return m, nil
}

// prefillPort picks the port shown when the kill-a-port field first opens:
// the last port this screen killed, otherwise the first configured dev port
// (plan.md milestone 3: "the port field prefilled ... is acceptable").
func (m Model) prefillPort(ctx uictx.Context) uint16 {
	if m.lastPort != 0 {
		return m.lastPort
	}
	if ports := configPorts(ctx.Config.DevPorts); len(ports) > 0 {
		return ports[0]
	}
	return 3000
}

// viewSubmenu renders the three entry points.
func (m Model) viewSubmenu(ctx uictx.Context) string {
	var b strings.Builder
	b.WriteString(ctx.Theme.Muted.Render("Free a busy port or stop a stuck process."))
	b.WriteString("\n\n")
	b.WriteString(m.submenu.View(ctx))
	return b.String()
}
