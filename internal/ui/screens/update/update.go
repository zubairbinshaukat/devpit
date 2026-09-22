// Package update is the "Update Everything" screen: detect which package
// managers are on the machine, let the user untick any of them, then run one
// manager at a time with streamed output and a summary card.
//
// Detection and every update step run through injectable function fields, so
// tests never exec a real package manager or trigger a real UAC prompt.
// Chocolatey always needs elevation ([managers.Manager.NeedsElevation]); its
// step runs through the elevated worker client instead of [tools.RunStep].
// Long-running steps stream output back over a channel, drained by a 150ms
// [tea.Tick] into one batch message per tick — [Model.Update] itself never
// blocks.
package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/summary"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// tickEvery is how often the running state drains the event channel.
const tickEvery = 150 * time.Millisecond

// maxOutLines caps how many streamed lines the output pane keeps.
const maxOutLines = 500

// confirmID identifies this screen's single confirm dialog.
const confirmID = "update"

// managerOrder is the fixed order steps run in, from plan.md section 9.
var managerOrder = []string{"winget", "scoop", "npm", "choco"}

// DetectFunc detects the tools on the machine. The default wraps
// [tools.Detector.All] on a fresh [tools.New] detector.
type DetectFunc func(ctx context.Context) []tools.Tool

// RunStepFunc runs one update command, streaming its output. The default is
// [tools.RunStep].
type RunStepFunc func(ctx context.Context, argv []string, timeout time.Duration, onLine func(string)) tools.StepResult

// elevatedClient is the subset of *[elevate.Client] a step needs. It exists
// so tests can fake the elevated worker without a real UAC prompt.
type elevatedClient interface {
	Exec(ctx context.Context, argv []string, timeout time.Duration, onLine func(stream, text string)) (int, error)
	Close() error
}

// LaunchElevatedFunc starts (or connects to) the elevated worker. The
// default launches the current executable via [elevate.Launch].
type LaunchElevatedFunc func(ctx context.Context) (elevatedClient, error)

// state is the screen's internal step.
type state int

const (
	stateDetecting state = iota
	stateNoManagers
	stateError
	stateList
	stateConfirm
	stateRunning
	stateSummary
)

// outcome is the result of running one manager's update step.
type outcome struct {
	Name       string
	OK         bool
	Skipped    bool
	SkipReason string
	LastLines  []string
}

// step is one manager's update row in the untick list.
type step struct {
	manager  managers.Manager
	selected bool
}

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
		Select: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "update")),
		Stop:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "stop")),
	}
}

// runHandle is the mutable state a running update keeps outside the Model
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
// never blocks: it only signals; whether the work has actually wound down is
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

// WithRunStepFunc overrides how an update command is run.
func WithRunStepFunc(f RunStepFunc) Option { return func(m *Model) { m.runStepFn = f } }

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

// Model is the update screen.
type Model struct {
	detectFn         DetectFunc
	runStepFn        RunStepFunc
	launchElevatedFn LaunchElevatedFunc

	keys keyMap

	state   state
	errText string

	detected []tools.Tool
	steps    []step
	cursor   int

	pendingSteps []step
	confirm      confirm.Model

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

// Busy implements uictx.BusyReporter: an update run is in flight, which is
// when Esc means "stop" rather than "back" and Ctrl+C waits for the manager
// step in progress to finish before quitting. It covers the whole
// stateRunning span, including the time an elevated-worker step spends
// connecting, since that is also work that must not be interrupted mid-item.
func (m Model) Busy() bool { return m.state == stateRunning }

// Stop implements uictx.Stopper. It cancels the run's context and, if an
// elevated worker is connected, closes it too, so nothing keeps running
// orphaned after the screen is popped or the user asks to quit. It never
// blocks: the goroutine finishes the step it already started (marked
// "cancelled") and every step after it is marked cancelled without running,
// landing on the summary card rather than leaving the run stuck mid-flight.
func (m Model) Stop() { m.run.stop() }

// New returns the update screen. Detection starts when [Model.Init] runs.
func New(opts ...Option) Model {
	m := Model{
		detectFn:         func(ctx context.Context) []tools.Tool { return tools.New().All(ctx) },
		runStepFn:        tools.RunStep,
		launchElevatedFn: launchWorker,
		keys:             defaultKeys(),
		state:            stateDetecting,
	}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

// Init implements uictx.Screen: it kicks off detection off the render path.
func (m Model) Init() tea.Cmd {
	detectFn := m.detectFn
	return func() tea.Msg {
		return detectResultMsg{tools: detectFn(context.Background())}
	}
}

// Title implements uictx.Screen.
func (m Model) Title() string { return "Update Everything" }

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
}

// runEvent is one item streamed back from the goroutine driving updates.
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
func (m Model) Update(msg tea.Msg, _ uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case detectResultMsg:
		return m.onDetected(msg)
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
// the step in flight, marks everything after it "cancelled" and lands on the
// summary card, same as Ctrl+C via [Model.Stop].
func (m Model) updateRunning(msg tea.Msg) (uictx.Screen, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok || !key.Matches(km, m.keys.Stop) {
		return m, nil
	}
	m.run.stop()
	return m, uictx.Status("warning", "Finishing the step in flight, then stopping…")
}

// onDetected builds the step list in manager order and moves past the
// detecting state.
func (m Model) onDetected(msg detectResultMsg) (uictx.Screen, tea.Cmd) {
	m.detected = msg.tools
	m.steps = buildSteps(msg.tools)
	if len(m.steps) == 0 {
		m.state = stateNoManagers
		return m, nil
	}
	m.cursor = 0
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
		if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(km, m.keys.Down):
		if m.cursor < len(m.steps)-1 {
			m.cursor++
		}
	case key.Matches(km, m.keys.Toggle):
		if m.cursor >= 0 && m.cursor < len(m.steps) {
			m.steps[m.cursor].selected = !m.steps[m.cursor].selected
		}
	case key.Matches(km, m.keys.Select):
		picked := selectedSteps(m.steps)
		if len(picked) == 0 {
			return m, uictx.Status("warning", "Pick at least one manager first")
		}
		m.pendingSteps = picked
		question := fmt.Sprintf("Update %d package manager(s)?", len(picked))
		m.confirm = confirm.New(confirmID, question, confirmDetail(picked))
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
	m.runTotal = len(m.pendingSteps)
	m.runStart = time.Now()
	m.events = make(chan runEvent, 64)

	runCtx, cancel := context.WithCancel(context.Background())
	m.run = newRunHandle(cancel)

	runStepFn := m.runStepFn
	launchElevatedFn := m.launchElevatedFn
	steps := m.pendingSteps
	events := m.events
	handle := m.run
	go runUpdateSteps(runCtx, runStepFn, launchElevatedFn, steps, events, handle)

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
			reason := "update failed, see output below"
			if r.SkipReason != "" {
				reason = r.SkipReason
			}
			skipped = append(skipped, summary.Skipped{Name: r.Name, Reason: reason})
		}
	}
	return summary.Result{
		Headline: fmt.Sprintf("Updated %d of %d", passed, len(m.results)),
		Items:    len(m.results),
		Duration: m.runElapse,
		Skipped:  skipped,
		Failed:   failed,
	}
}

// buildSteps orders the detected managers winget, scoop, npm, choco, each
// pre-ticked.
func buildSteps(detected []tools.Tool) []step {
	found := make(map[string]bool, len(detected))
	for _, t := range detected {
		if t.Found {
			found[t.Name] = true
		}
	}
	all := managers.All()
	var steps []step
	for _, name := range managerOrder {
		if !found[name] {
			continue
		}
		for _, mgr := range all {
			if mgr.Name() == name {
				steps = append(steps, step{manager: mgr, selected: true})
				break
			}
		}
	}
	return steps
}

// selectedSteps returns the ticked steps, preserving order.
func selectedSteps(steps []step) []step {
	var picked []step
	for _, st := range steps {
		if st.selected {
			picked = append(picked, st)
		}
	}
	return picked
}

// confirmDetail lists every selected manager and its exact commands.
func confirmDetail(steps []step) string {
	var b strings.Builder
	for i, st := range steps {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(st.manager.Name() + ":")
		for _, argv := range st.manager.UpgradeAllCmds() {
			b.WriteString("\n  " + strings.Join(argv, " "))
		}
	}
	return b.String()
}

// runUpdateSteps runs every step in order, streaming lines and per-step
// outcomes over events, and closes events when done. It runs entirely on its
// own goroutine.
//
// ctx is cancelled by [Model.Stop] (Esc mid-run or Ctrl+C). Every send to
// events selects on ctx.Done() so a Stop against an abandoned consumer can
// never leave this goroutine blocked forever on a full, unread channel. A
// cancellation partway through marks the step that was running "cancelled"
// along with whatever output it produced, marks every step after it
// "cancelled" without starting it, and still reaches the summary card rather
// than leaving the screen stuck on "running".
func runUpdateSteps(ctx context.Context, runStepFn RunStepFunc, launchElevatedFn LaunchElevatedFunc, steps []step, events chan<- runEvent, handle *runHandle) {
	defer close(events)
	for i, st := range steps {
		if ctx.Err() != nil {
			cancelRemainingSteps(ctx, events, steps[i:])
			break
		}

		var out outcome
		var cancelled bool
		if st.manager.NeedsElevation() {
			out, cancelled = runElevatedStep(ctx, launchElevatedFn, st, events, handle)
		} else {
			out, cancelled = runManagerStep(ctx, runStepFn, st.manager, events)
		}
		sendEvent(ctx, events, runEvent{stepDone: true, outcome: out})
		if cancelled {
			cancelRemainingSteps(ctx, events, steps[i+1:])
			break
		}
	}
	sendEvent(ctx, events, runEvent{allDone: true})
}

// runManagerStep runs every command of one non-elevated manager step,
// stopping early if ctx is cancelled mid-command. The second return reports
// whether the step was cut short that way.
func runManagerStep(ctx context.Context, runStepFn RunStepFunc, mgr managers.Manager, events chan<- runEvent) (outcome, bool) {
	ok := true
	var lastLines []string
	for _, argv := range mgr.UpgradeAllCmds() {
		res := runStepFn(ctx, argv, tools.DefaultStepTimeout, func(line string) {
			sendEvent(ctx, events, runEvent{line: line})
		})
		lastLines = res.LastLines
		if ctx.Err() != nil {
			return outcome{Name: mgr.Name(), SkipReason: "cancelled", LastLines: lastLines}, true
		}
		if !res.OK {
			ok = false
			break
		}
	}
	return outcome{Name: mgr.Name(), OK: ok, LastLines: lastLines}, false
}

// runElevatedStep runs one manager's commands through the elevated worker.
// A declined UAC prompt marks the step skipped; a worker death marks it
// failed with whatever output the worker managed to stream first. Once
// connected, the client is registered on handle so a Stop mid-run closes it
// instead of leaving it running detached. The second return reports whether
// the step was cut short by a cancellation.
func runElevatedStep(ctx context.Context, launchElevatedFn LaunchElevatedFunc, st step, events chan<- runEvent, handle *runHandle) (outcome, bool) {
	client, err := launchElevatedFn(ctx)
	if err != nil {
		var declined *elevate.DeclinedError
		if errors.As(err, &declined) {
			return outcome{Name: st.manager.Name(), Skipped: true, SkipReason: "skipped (needs admin)"}, false
		}
		var died *elevate.WorkerDiedError
		if errors.As(err, &died) {
			return outcome{Name: st.manager.Name(), LastLines: died.LastLines}, false
		}
		return outcome{Name: st.manager.Name(), LastLines: []string{err.Error()}}, false
	}
	handle.setClient(client)
	defer func() {
		handle.clearClient()
		_ = client.Close()
	}()

	ok := true
	var lastLines []string
	for _, argv := range st.manager.UpgradeAllCmds() {
		_, execErr := client.Exec(ctx, argv, tools.DefaultStepTimeout, func(_, text string) {
			sendEvent(ctx, events, runEvent{line: text})
		})
		if ctx.Err() != nil {
			lines := lastLines
			var died *elevate.WorkerDiedError
			if errors.As(execErr, &died) {
				lines = died.LastLines
			}
			return outcome{Name: st.manager.Name(), SkipReason: "cancelled", LastLines: lines}, true
		}
		if execErr != nil {
			ok = false
			var died *elevate.WorkerDiedError
			if errors.As(execErr, &died) {
				lastLines = died.LastLines
			} else {
				lastLines = []string{execErr.Error()}
			}
			break
		}
	}
	return outcome{Name: st.manager.Name(), OK: ok, LastLines: lastLines}, false
}

// cancelRemainingSteps marks every step in steps "cancelled" without running
// it. It is what a Stop mid-run leaves for every step that never got to
// start.
func cancelRemainingSteps(ctx context.Context, events chan<- runEvent, steps []step) {
	for _, st := range steps {
		sendEvent(ctx, events, runEvent{stepDone: true, outcome: outcome{
			Name: st.manager.Name(), Skipped: true, SkipReason: "cancelled",
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
		return ctx.Theme.Muted.Render("Detecting package managers…")
	case stateError:
		return ctx.Theme.Danger.Render(ctx.Icons.Fail + " " + m.errText)
	case stateNoManagers:
		return ctx.Theme.Warning.Render(ctx.Icons.Warn +
			" No package manager (winget, Scoop, npm or Chocolatey) was found, so there is nothing to update.")
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

// viewList renders the untick list of detected managers.
func (m Model) viewList(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(th.Muted.Render("Space unticks a manager, Enter runs the rest."))
	b.WriteString("\n\n")

	for i, st := range m.steps {
		b.WriteString(m.renderStep(ctx, st, i == m.cursor))
		if i < len(m.steps)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderStep draws one manager row.
func (m Model) renderStep(ctx uictx.Context, st step, atCursor bool) string {
	th := ctx.Theme
	box := ctx.Icons.Unchecked
	if st.selected {
		box = ctx.Icons.Checked
	}
	cursor := "  "
	if atCursor {
		cursor = ctx.Icons.Cursor + " "
	}
	text := ctx.Truncate(cursor + box + " " + st.manager.Name())
	if atCursor {
		return th.Selected.Render(text)
	}
	return th.Base.Render(text)
}

// viewRunning renders the progress bar and the streamed output pane.
func (m Model) viewRunning(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(th.Subtitle.Render(fmt.Sprintf("Updating %d/%d", m.runIdx, m.runTotal)))
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
// failed (not merely skipped) step.
func (m Model) viewSummary(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(m.summary.View(ctx))
	for _, r := range m.results {
		if r.OK || r.Skipped {
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
