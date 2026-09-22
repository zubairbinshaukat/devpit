// Package install is the "Install Developer Apps" screen: pick apps from the
// catalog with a multiselect list, confirm the exact commands that will run,
// then install them one at a time with streamed output and a summary card.
//
// Detection (which tools are on PATH, which catalog apps already appear
// installed) and every install step run through injectable function fields,
// so tests never exec a real package manager. A manager that needs elevation
// ([managers.Manager.NeedsElevation], which today means Chocolatey) has its
// installs run through the elevated worker instead of [tools.RunStep]; the
// worker is launched once for the whole run rather than once per app, so a
// ten-app Chocolatey install is one UAC prompt and not ten. A declined prompt
// marks every app in that run "skipped (needs admin)" rather than failed.
// Long-running steps stream output back over a channel, drained by a 150ms
// [tea.Tick] into one batch message per tick — [Model.Update] itself never
// blocks.
package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/tools/catalog"
	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/summary"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// tickEvery is how often the running state drains the event channel.
const tickEvery = 150 * time.Millisecond

// maxOutLines caps how many streamed lines the output pane keeps, so a
// chatty installer never grows the view without bound.
const maxOutLines = 500

// confirmID identifies this screen's single confirm dialog.
const confirmID = "install"

// DetectFunc detects the tools on the machine. The default wraps
// [tools.Detector.All] on a fresh [tools.New] detector.
type DetectFunc func(ctx context.Context) []tools.Tool

// RunStepFunc runs one install command, streaming its output. The default is
// [tools.RunStep].
type RunStepFunc func(ctx context.Context, argv []string, timeout time.Duration, onLine func(string)) tools.StepResult

// LookPathFunc resolves an executable name on PATH, matching [exec.LookPath].
// It is used to decide whether a catalog app already appears installed.
type LookPathFunc func(exe string) (string, error)

// IsElevatedFunc reports whether the current process token is elevated. The
// default is [elevate.IsElevated].
type IsElevatedFunc func() (bool, error)

// LoadCatalogFunc loads the install catalog. The default is [catalog.Load].
type LoadCatalogFunc func() ([]catalog.App, error)

// elevatedClient is the subset of *[elevate.Client] an install needs. It
// exists so tests can fake the elevated worker without a real UAC prompt.
type elevatedClient interface {
	Exec(ctx context.Context, argv []string, timeout time.Duration, onLine func(stream, text string)) (int, error)
	Close() error
}

// LaunchElevatedFunc starts (or connects to) the elevated worker. The default
// launches the current executable via [elevate.Launch].
type LaunchElevatedFunc func(ctx context.Context) (elevatedClient, error)

// state is the screen's internal step.
type state int

const (
	stateDetecting state = iota
	stateNoManager
	stateError
	stateList
	stateConfirm
	stateRunning
	stateSummary
)

// outcome is the result of installing one app.
type outcome struct {
	Name       string
	OK         bool
	Skipped    bool
	SkipReason string
	ExitCode   int
	LastLines  []string
}

// row is one line of the multiselect list: either a category header or one
// app.
type row struct {
	isHeader    bool
	category    string
	app         catalog.App
	installed   bool
	unavailable bool
	selected    bool
}

// disabled reports whether the row can receive the cursor or be toggled.
func (r row) disabled() bool { return r.isHeader || r.installed || r.unavailable }

// keyMap is the screen's own key bindings, on top of the global ones.
type keyMap struct {
	Up     key.Binding
	Down   key.Binding
	Toggle key.Binding
	Select key.Binding
	Stop   key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑↓", "move")),
		Down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "down")),
		Toggle: key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "toggle")),
		Select: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "install")),
		Stop:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "stop")),
	}
}

// runHandle is the mutable state a running install keeps outside the Model
// value, so that Stop — which only ever acts on a copy of Model handed to it
// through the uictx.Stopper interface — can still reach the goroutine's
// cancel func and, once one is connected, its elevated worker client.
//
// A nil *runHandle is valid and its methods are no-ops, which is what a
// screen that has never started a run holds.
type runHandle struct {
	cancel context.CancelFunc

	mu     sync.Mutex
	client elevatedClient
}

// newRunHandle returns a handle wired to cancel.
func newRunHandle(cancel context.CancelFunc) *runHandle {
	return &runHandle{cancel: cancel}
}

// setClient records the elevated worker client once a run connects to one,
// so a Stop that arrives afterward can close it.
func (h *runHandle) setClient(c elevatedClient) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.client = c
	h.mu.Unlock()
}

// clearClient forgets the client once the goroutine is done with it, so a
// later Stop on a finished run never double-acts on a stale reference.
func (h *runHandle) clearClient() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.client = nil
	h.mu.Unlock()
}

// stop cancels the run and, if an elevated worker is connected, closes it
// too. Close is idempotent (elevate.Client.Close uses sync.Once), so it is
// safe even if the goroutine's own defer also closes the same client. stop
// never blocks: it only signals: whether the work has actually wound down is
// what Model.Busy answers.
func (h *runHandle) stop() {
	if h == nil {
		return
	}
	if h.cancel != nil {
		h.cancel()
	}
	h.mu.Lock()
	c := h.client
	h.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// Option configures a [Model]. Production code uses none of these; tests use
// them to replace every engine call with a fake.
type Option func(*Model)

// WithDetectFunc overrides tool detection.
func WithDetectFunc(f DetectFunc) Option { return func(m *Model) { m.detectFn = f } }

// WithRunStepFunc overrides how an install command is run.
func WithRunStepFunc(f RunStepFunc) Option { return func(m *Model) { m.runStepFn = f } }

// WithLookPathFunc overrides how an executable is resolved on PATH.
func WithLookPathFunc(f LookPathFunc) Option { return func(m *Model) { m.lookPathFn = f } }

// WithIsElevatedFunc overrides the elevation check used for the winget UAC
// notice.
func WithIsElevatedFunc(f IsElevatedFunc) Option { return func(m *Model) { m.isElevatedFn = f } }

// WithLoadCatalogFunc overrides how the catalog is loaded.
func WithLoadCatalogFunc(f LoadCatalogFunc) Option { return func(m *Model) { m.loadCatalogFn = f } }

// WithLaunchElevatedFunc overrides how the elevated worker is launched.
func WithLaunchElevatedFunc(f LaunchElevatedFunc) Option {
	return func(m *Model) { m.launchElevatedFn = f }
}

// Model implements uictx.Screen, checked here so a signature change is a
// compile error in this package rather than a nil interface at the router.
var (
	_ uictx.Screen       = Model{}
	_ uictx.BusyReporter = Model{}
	_ uictx.Stopper      = Model{}
)

// Model is the install screen.
type Model struct {
	detectFn         DetectFunc
	runStepFn        RunStepFunc
	lookPathFn       LookPathFunc
	isElevatedFn     IsElevatedFunc
	loadCatalogFn    LoadCatalogFunc
	launchElevatedFn LaunchElevatedFunc

	keys keyMap

	state   state
	errText string

	detected []tools.Tool
	manager  managers.Manager
	rows     []row
	cursor   int

	pendingApps []catalog.App
	confirm     confirm.Model

	run       *runHandle
	runIdx    int
	runTotal  int
	runStart  time.Time
	runElapse time.Duration
	results   []outcome
	outLines  []string
	events    chan runEvent

	summary summary.Model
}

// Busy implements uictx.BusyReporter: an install run is in flight, which is
// when Esc means "stop" rather than "back" and Ctrl+C waits for the app in
// progress to finish before quitting. It covers the whole stateRunning span,
// including the time an elevated-worker install spends connecting, since
// that is also work that must not be interrupted mid-item.
func (m Model) Busy() bool { return m.state == stateRunning }

// Stop implements uictx.Stopper. It cancels the run's context and, if an
// elevated worker is connected, closes it too, so nothing keeps running
// orphaned after the screen is popped or the user asks to quit. It never
// blocks: the goroutine finishes the app it already started (marked
// "cancelled") and every app after it is marked cancelled without running,
// landing on the summary card rather than leaving the run stuck mid-flight.
func (m Model) Stop() { m.run.stop() }

// New returns the install screen. Detection starts when [Model.Init] runs.
func New(opts ...Option) Model {
	m := Model{
		detectFn:      func(ctx context.Context) []tools.Tool { return tools.New().All(ctx) },
		runStepFn:     tools.RunStep,
		lookPathFn:    exec.LookPath,
		isElevatedFn:  elevate.IsElevated,
		loadCatalogFn: catalog.Load,
		// launchElevatedFn is only reached for a manager that needs
		// elevation; every other install never goes near the worker.
		launchElevatedFn: launchWorker,
		keys:             defaultKeys(),
		state:            stateDetecting,
	}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

// Init implements uictx.Screen: it kicks off detection and catalog loading
// off the render path.
func (m Model) Init() tea.Cmd {
	detectFn := m.detectFn
	loadCatalogFn := m.loadCatalogFn
	return func() tea.Msg {
		detected := detectFn(context.Background())
		apps, err := loadCatalogFn()
		return detectResultMsg{tools: detected, apps: apps, err: err}
	}
}

// Title implements uictx.Screen.
func (m Model) Title() string { return "Install Developer Apps" }

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	switch m.state {
	case stateList:
		return []key.Binding{m.keys.Up, m.keys.Toggle, m.keys.Select}
	case stateConfirm:
		return m.confirm.Keys.ShortHelp()
	case stateRunning:
		return []key.Binding{m.keys.Stop}
	case stateSummary:
		return m.summary.Keys.ShortHelp()
	default:
		return nil
	}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	switch m.state {
	case stateList:
		return [][]key.Binding{{m.keys.Up, m.keys.Down, m.keys.Toggle, m.keys.Select}}
	case stateConfirm:
		return m.confirm.Keys.FullHelp()
	case stateRunning:
		return [][]key.Binding{{m.keys.Stop}}
	case stateSummary:
		return m.summary.Keys.FullHelp()
	default:
		return nil
	}
}

// detectResultMsg carries the outcome of Init's background detection.
type detectResultMsg struct {
	tools []tools.Tool
	apps  []catalog.App
	err   error
}

// runEvent is one item streamed back from the goroutine driving installs.
type runEvent struct {
	line     string
	stepDone bool
	outcome  outcome
	allDone  bool
}

// runBatchMsg is one tick's worth of drained [runEvent]s.
type runBatchMsg struct {
	events []runEvent
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case detectResultMsg:
		return m.onDetected(msg, ctx)
	case confirm.AnsweredMsg:
		if msg.ID == confirmID {
			return m.onAnswered(msg)
		}
	case runBatchMsg:
		return m.onBatch(msg)
	}

	switch m.state {
	case stateList:
		return m.updateList(msg)
	case stateConfirm:
		var cmd tea.Cmd
		m.confirm, cmd = m.confirm.Update(msg)
		return m, cmd
	case stateRunning:
		return m.updateRunning(msg)
	case stateSummary:
		if _, ok := msg.(summary.DismissedMsg); ok {
			return m, uictx.Pop()
		}
		var cmd tea.Cmd
		m.summary, cmd = m.summary.Update(msg)
		return m, cmd
	}
	return m, nil
}

// updateRunning handles the keyboard while a run is in flight. It is the
// screen's own answer to Esc, reachable here because a Busy screen keeps the
// root model from popping on Esc and forwards the key instead (internal/app
// and internal/ui/uictx.BusyReporter). Stopping never pops: the run finishes
// the app in flight, marks everything after it "cancelled" and lands on the
// summary card, same as Ctrl+C via [Model.Stop].
func (m Model) updateRunning(msg tea.Msg) (uictx.Screen, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok || !key.Matches(km, m.keys.Stop) {
		return m, nil
	}
	m.run.stop()
	return m, uictx.Status("warning", "Finishing the app in flight, then stopping…")
}

// onDetected resolves the preferred manager, builds the app rows and moves
// past the detecting state.
func (m Model) onDetected(msg detectResultMsg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if msg.err != nil {
		m.state = stateError
		m.errText = msg.err.Error()
		return m, nil
	}
	m.detected = msg.tools
	m.manager = managers.Preferred(msg.tools, ctx.Config.PreferredManager)
	if m.manager == nil {
		m.state = stateNoManager
		return m, nil
	}
	installed := computeInstalled(msg.apps, m.lookPathFn)
	m.rows = buildRows(msg.apps, installed, m.manager)
	m.cursor = nextSelectable(m.rows, -1, 1)
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.state = stateList
	return m, nil
}

// updateList handles navigation, toggling and moving to the confirm dialog.
func (m Model) updateList(msg tea.Msg) (uictx.Screen, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch {
	case key.Matches(km, m.keys.Up):
		if i := nextSelectable(m.rows, m.cursor, -1); i >= 0 {
			m.cursor = i
		}
	case key.Matches(km, m.keys.Down):
		if i := nextSelectable(m.rows, m.cursor, 1); i >= 0 {
			m.cursor = i
		}
	case key.Matches(km, m.keys.Toggle):
		if m.cursor >= 0 && m.cursor < len(m.rows) && !m.rows[m.cursor].disabled() {
			m.rows[m.cursor].selected = !m.rows[m.cursor].selected
		}
	case key.Matches(km, m.keys.Select):
		apps := m.selectedApps()
		if len(apps) == 0 {
			return m, uictx.Status("warning", "Pick at least one app first")
		}
		m.pendingApps = apps
		question := fmt.Sprintf("Install %d app(s)?", len(apps))
		m.confirm = confirm.New(confirmID, question, m.confirmDetail(apps))
		m.state = stateConfirm
	}
	return m, nil
}

// onAnswered handles the confirm dialog's outcome.
func (m Model) onAnswered(msg confirm.AnsweredMsg) (uictx.Screen, tea.Cmd) {
	if msg.Answer == confirm.AnswerNo {
		m.state = stateList
		return m, nil
	}

	m.results = nil
	m.outLines = nil
	m.runIdx = 0
	m.runTotal = len(m.pendingApps)
	m.runStart = time.Now()
	m.events = make(chan runEvent, 64)

	runCtx, cancel := context.WithCancel(context.Background())
	m.run = newRunHandle(cancel)

	runStepFn := m.runStepFn
	launchElevatedFn := m.launchElevatedFn
	manager := m.manager
	apps := m.pendingApps
	events := m.events
	handle := m.run
	go runInstallSteps(runCtx, runStepFn, launchElevatedFn, manager, apps, events, handle)

	m.state = stateRunning
	return m, waitForEvents(m.events)
}

// onBatch folds one tick's worth of streamed events into the model.
func (m Model) onBatch(msg runBatchMsg) (uictx.Screen, tea.Cmd) {
	finished := false
	for _, ev := range msg.events {
		switch {
		case ev.allDone:
			finished = true
		case ev.stepDone:
			m.results = append(m.results, ev.outcome)
			m.runIdx++
		case ev.line != "":
			m.outLines = append(m.outLines, ev.line)
			if len(m.outLines) > maxOutLines {
				m.outLines = m.outLines[len(m.outLines)-maxOutLines:]
			}
		}
	}
	if finished {
		m.runElapse = time.Since(m.runStart)
		m.state = stateSummary
		m.summary = summary.New(m.buildResult())
		m.run = nil
		return m, nil
	}
	return m, waitForEvents(m.events)
}

// buildResult turns the collected outcomes into a summary card result.
func (m Model) buildResult() summary.Result {
	passed := 0
	failed := false
	var skipped []summary.Skipped
	for _, r := range m.results {
		switch {
		case r.Skipped:
			skipped = append(skipped, summary.Skipped{Name: r.Name, Reason: r.SkipReason})
		case r.OK:
			passed++
		default:
			failed = true
			reason := "install failed, see output below"
			if r.SkipReason != "" {
				reason = r.SkipReason
			}
			skipped = append(skipped, summary.Skipped{Name: r.Name, Reason: reason})
		}
	}
	return summary.Result{
		Headline: fmt.Sprintf("Installed %d of %d", passed, len(m.results)),
		Items:    len(m.results),
		Duration: m.runElapse,
		Skipped:  skipped,
		Failed:   failed,
	}
}

// selectedApps returns the apps ticked in the list, in catalog order.
func (m Model) selectedApps() []catalog.App {
	var apps []catalog.App
	for _, r := range m.rows {
		if !r.isHeader && r.selected && !r.disabled() {
			apps = append(apps, r.app)
		}
	}
	return apps
}

// confirmDetail lists the manager and the exact command for every app,
// plus the winget UAC notice from plan.md section 9 when it applies.
func (m Model) confirmDetail(apps []catalog.App) string {
	var b strings.Builder
	b.WriteString("Manager: " + m.manager.Name())
	if m.manager.Name() == "winget" {
		elevated, _ := m.isElevatedFn()
		if !elevated {
			b.WriteString("\nwinget may show one UAC prompt per package.")
		}
	}
	for _, a := range apps {
		id := managerAppID(a, m.manager.Name())
		b.WriteString("\n" + strings.Join(m.manager.InstallCmd(id), " "))
	}
	return b.String()
}

// computeInstalled reports, for every app, whether any of its Detect
// executables were found on PATH.
func computeInstalled(apps []catalog.App, lookPath LookPathFunc) map[string]bool {
	out := make(map[string]bool, len(apps))
	for _, a := range apps {
		found := false
		for _, exe := range a.Detect {
			if _, err := lookPath(exe); err == nil {
				found = true
				break
			}
		}
		out[a.Name] = found
	}
	return out
}

// buildRows groups apps by category, in first-appearance order, and marks
// each app already installed or unavailable for the chosen manager.
func buildRows(apps []catalog.App, installed map[string]bool, manager managers.Manager) []row {
	var order []string
	byCat := make(map[string][]catalog.App)
	for _, a := range apps {
		if _, ok := byCat[a.Category]; !ok {
			order = append(order, a.Category)
		}
		byCat[a.Category] = append(byCat[a.Category], a)
	}

	managerName := ""
	if manager != nil {
		managerName = manager.Name()
	}

	var rows []row
	for _, cat := range order {
		rows = append(rows, row{isHeader: true, category: cat})
		for _, a := range byCat[cat] {
			r := row{app: a, installed: installed[a.Name]}
			if !r.installed && !a.HasManager(managerName) {
				r.unavailable = true
			}
			rows = append(rows, r)
		}
	}
	return rows
}

// managerAppID returns the catalog id for the given manager name.
func managerAppID(a catalog.App, managerName string) string {
	switch managerName {
	case "scoop":
		return a.Scoop
	case "winget":
		return a.Winget
	case "choco":
		return a.Choco
	default:
		return ""
	}
}

// nextSelectable walks from i in the given direction and returns the next
// row that can take the cursor, or -1 if there is none.
func nextSelectable(rows []row, i, step int) int {
	for n := i + step; n >= 0 && n < len(rows); n += step {
		if !rows[n].disabled() {
			return n
		}
	}
	return -1
}

// runInstallSteps installs every app in order, streaming lines and per-app
// outcomes over events, and closes events when done. It runs entirely on its
// own goroutine.
//
// ctx is cancelled by [Model.Stop] (Esc mid-run or Ctrl+C). Every send to
// events selects on ctx.Done() so a Stop against an abandoned consumer can
// never leave this goroutine blocked forever on a full, unread channel. A
// cancellation partway through marks the app that was running "cancelled"
// along with whatever output it produced, marks every app after it
// "cancelled" without starting it, and still reaches the summary card rather
// than leaving the screen stuck on "running".
func runInstallSteps(ctx context.Context, runStepFn RunStepFunc, launchElevatedFn LaunchElevatedFunc, manager managers.Manager, apps []catalog.App, events chan<- runEvent, handle *runHandle) {
	defer close(events)
	if manager.NeedsElevation() {
		runElevatedInstalls(ctx, launchElevatedFn, manager, apps, events, handle)
		sendEvent(ctx, events, runEvent{allDone: true})
		return
	}
	for i, app := range apps {
		if ctx.Err() != nil {
			cancelRemainingApps(ctx, events, apps[i:])
			break
		}
		id := managerAppID(app, manager.Name())
		argv := manager.InstallCmd(id)
		res := runStepFn(ctx, argv, tools.DefaultStepTimeout, func(line string) {
			sendEvent(ctx, events, runEvent{line: line})
		})
		if ctx.Err() != nil {
			sendEvent(ctx, events, runEvent{stepDone: true, outcome: outcome{
				Name: app.Name, SkipReason: "cancelled", LastLines: res.LastLines,
			}})
			cancelRemainingApps(ctx, events, apps[i+1:])
			break
		}
		sendEvent(ctx, events, runEvent{stepDone: true, outcome: outcome{
			Name:      app.Name,
			OK:        res.OK,
			ExitCode:  res.ExitCode,
			LastLines: res.LastLines,
		}})
	}
	sendEvent(ctx, events, runEvent{allDone: true})
}

// runElevatedInstalls installs every app through one elevated worker, so the
// user answers a single UAC prompt for the whole run. A declined prompt marks
// every app skipped; a worker that never started, or died, marks them failed
// with whatever output it managed to stream first. Once connected, the
// client is registered on handle so a Stop mid-run closes it instead of
// leaving it running detached.
func runElevatedInstalls(ctx context.Context, launchElevatedFn LaunchElevatedFunc, manager managers.Manager, apps []catalog.App, events chan<- runEvent, handle *runHandle) {
	client, err := launchElevatedFn(ctx)
	if err != nil {
		var declined *elevate.DeclinedError
		if errors.As(err, &declined) {
			for _, app := range apps {
				sendEvent(ctx, events, runEvent{stepDone: true, outcome: outcome{
					Name: app.Name, Skipped: true, SkipReason: "skipped (needs admin)",
				}})
			}
			return
		}
		lines := []string{err.Error()}
		var died *elevate.WorkerDiedError
		if errors.As(err, &died) {
			lines = died.LastLines
		}
		for _, app := range apps {
			sendEvent(ctx, events, runEvent{stepDone: true, outcome: outcome{Name: app.Name, LastLines: lines}})
		}
		return
	}
	handle.setClient(client)
	defer func() {
		handle.clearClient()
		_ = client.Close()
	}()

	for i, app := range apps {
		if ctx.Err() != nil {
			cancelRemainingApps(ctx, events, apps[i:])
			return
		}
		id := managerAppID(app, manager.Name())
		argv := manager.InstallCmd(id)
		var lastLines []string
		code, execErr := client.Exec(ctx, argv, tools.DefaultStepTimeout, func(_, text string) {
			lastLines = append(lastLines, text)
			sendEvent(ctx, events, runEvent{line: text})
		})
		if ctx.Err() != nil {
			lines := lastLines
			var died *elevate.WorkerDiedError
			if errors.As(execErr, &died) {
				lines = died.LastLines
			}
			sendEvent(ctx, events, runEvent{stepDone: true, outcome: outcome{
				Name: app.Name, SkipReason: "cancelled", LastLines: lines,
			}})
			cancelRemainingApps(ctx, events, apps[i+1:])
			return
		}
		if execErr != nil {
			lines := []string{execErr.Error()}
			var died *elevate.WorkerDiedError
			if errors.As(execErr, &died) {
				lines = died.LastLines
			}
			sendEvent(ctx, events, runEvent{stepDone: true, outcome: outcome{
				Name: app.Name, ExitCode: code, LastLines: lines,
			}})
			continue
		}
		sendEvent(ctx, events, runEvent{stepDone: true, outcome: outcome{Name: app.Name, OK: true, ExitCode: code}})
	}
}

// cancelRemainingApps marks every app in apps "cancelled" without running it.
// It is what a Stop mid-run leaves for every app that never got to start.
func cancelRemainingApps(ctx context.Context, events chan<- runEvent, apps []catalog.App) {
	for _, app := range apps {
		sendEvent(ctx, events, runEvent{stepDone: true, outcome: outcome{
			Name: app.Name, Skipped: true, SkipReason: "cancelled",
		}})
	}
}

// sendEvent delivers ev to events without ever blocking forever. It sends at
// once when there is room, and otherwise waits for either room or ctx to be
// cancelled, so a Stop against a full, unread channel still lets the
// goroutine exit instead of leaking.
func sendEvent(ctx context.Context, events chan<- runEvent, ev runEvent) {
	select {
	case events <- ev:
		return
	default:
	}
	select {
	case events <- ev:
	case <-ctx.Done():
	}
}

// launchWorker is the production [LaunchElevatedFunc]: it launches the
// current executable as the elevated worker.
func launchWorker(ctx context.Context) (elevatedClient, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return elevate.Launch(ctx, exe, elevate.Options{})
}

// waitForEvents drains whatever has arrived on ch every tick, without
// blocking Update: the drain itself never waits on ch, and the tick is what
// paces it.
func waitForEvents(ch chan runEvent) tea.Cmd {
	return tea.Tick(tickEvery, func(time.Time) tea.Msg {
		return runBatchMsg{events: drainEvents(ch)}
	})
}

// drainEvents reads everything currently buffered on ch without blocking.
func drainEvents(ch chan runEvent) []runEvent {
	var batch []runEvent
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return batch
			}
			batch = append(batch, ev)
		default:
			return batch
		}
	}
}

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	switch m.state {
	case stateDetecting:
		return ctx.Theme.Muted.Render("Detecting installed tools…")
	case stateError:
		return ctx.Theme.Danger.Render(ctx.Icons.Fail + " " + m.errText)
	case stateNoManager:
		return ctx.Theme.Warning.Render(ctx.Icons.Warn +
			" No package manager (Scoop, winget or Chocolatey) was found, so Devpit has nothing to install with.")
	case stateList:
		return m.viewList(ctx)
	case stateConfirm:
		return m.confirm.View(ctx)
	case stateRunning:
		return m.viewRunning(ctx)
	case stateSummary:
		return m.viewSummary(ctx)
	default:
		return ""
	}
}

// viewList renders the manager line and the visible window of rows.
func (m Model) viewList(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(th.Muted.Render("Manager: " + m.manager.Name()))
	b.WriteString("\n\n")

	height := ctx.BodyHeight - 2
	start, end := visibleWindow(len(m.rows), m.cursor, height)
	for i := start; i < end; i++ {
		b.WriteString(m.renderRow(ctx, m.rows[i], i == m.cursor))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderRow draws one row of the multiselect list.
func (m Model) renderRow(ctx uictx.Context, r row, atCursor bool) string {
	th := ctx.Theme
	if r.isHeader {
		return th.Subtitle.Render(r.category)
	}

	box := ctx.Icons.Unchecked
	switch {
	case r.installed:
		box = ctx.Icons.Tick
	case r.selected:
		box = ctx.Icons.Checked
	}

	cursor := "  "
	if atCursor {
		cursor = ctx.Icons.Cursor + " "
	}

	line := "  " + cursor + box + " " + r.app.Name
	switch {
	case r.installed:
		line += " (already installed)"
	case r.unavailable:
		line += fmt.Sprintf(" (not available via %s)", m.manager.Name())
	}
	text := ctx.Truncate(line)

	switch {
	case r.disabled():
		return th.Muted.Render(text)
	case atCursor:
		return th.Selected.Render(text)
	default:
		return th.Base.Render(text)
	}
}

// viewRunning renders the progress bar and the streamed output pane.
func (m Model) viewRunning(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(th.Subtitle.Render(fmt.Sprintf("Installing %d/%d", m.runIdx, m.runTotal)))
	b.WriteString("\n")
	b.WriteString(renderBar(ctx, m.runIdx, m.runTotal, 30))
	b.WriteString("\n\n")

	width := ctx.Width - 2
	if width < 10 {
		width = 10
	}
	height := ctx.BodyHeight - 4
	if height < 3 {
		height = 3
	}
	out := viewport.New(viewport.WithWidth(width), viewport.WithHeight(height))
	out.SetContent(strings.Join(m.outLines, "\n"))
	out.GotoBottom()
	b.WriteString(out.View())
	return b.String()
}

// viewSummary renders the summary card plus the last output lines of every
// failed app.
func (m Model) viewSummary(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(m.summary.View(ctx))
	for _, r := range m.results {
		if r.OK {
			continue
		}
		b.WriteString("\n\n")
		b.WriteString(th.Danger.Render(ctx.Icons.Fail + " " + r.Name))
		for _, line := range r.LastLines {
			b.WriteString("\n  ")
			b.WriteString(th.Muted.Render(ctx.Truncate(line)))
		}
	}
	return b.String()
}

// renderBar draws a fixed-width relative bar for a "done/total" progress.
func renderBar(ctx uictx.Context, done, total, width int) string {
	if width <= 0 {
		width = 20
	}
	filled := 0
	if total > 0 {
		filled = width * done / total
	}
	if filled > width {
		filled = width
	}
	th := ctx.Theme
	return th.Accent.Render(strings.Repeat(ctx.Icons.BarFull, filled)) +
		th.Muted.Render(strings.Repeat(ctx.Icons.BarEmpty, width-filled))
}

// visibleWindow returns the [start,end) slice of rows to draw so the cursor
// always stays visible within height rows.
func visibleWindow(n, cursor, height int) (start, end int) {
	if height <= 0 || n <= height {
		return 0, n
	}
	start = cursor - height/2
	if start < 0 {
		start = 0
	}
	if start+height > n {
		start = n - height
	}
	if start < 0 {
		start = 0
	}
	return start, start + height
}
