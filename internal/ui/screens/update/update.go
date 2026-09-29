// Package update is the "Update Everything" screen.
//
// It works the way a careful person would at a terminal, one app at a time:
//
//  1. Detect which package managers are on the machine.
//  2. Ask each of them, at the same time, what is out of date. Each manager
//     gets a spinner row that turns into "✔ winget 15 updates".
//  3. List every outdated app, grouped by manager, all ticked. The user
//     unticks whatever they would rather keep as it is. Pinned and held apps
//     are shown but cannot be ticked; apps winget says need explicit
//     targeting start unticked.
//  4. Update the ticked apps one by one, each as a single live row: a
//     spinner, a bar when the manager reports progress, the last thing it
//     said, and at the end a tick, a warning ("restart needed") or a cross
//     with the reason in plain words.
//  5. Finish on a summary card, with the full raw log one key away.
//
// When a manager's answer cannot be read (a localized message Devpit does not
// know, a format change), that manager falls back to one "update everything"
// row that runs its old upgrade-all commands, so nothing is lost.
//
// Detection, checks and every update step run through injectable function
// fields, so tests never exec a real package manager or trigger a real UAC
// prompt. Chocolatey always needs elevation; its apps run through the
// elevated worker, launched once for the whole Chocolatey part of a run so
// ten apps are one UAC prompt, not ten. Long-running steps stream output
// back over a channel, drained by a [tea.Tick] into one batch message per
// tick — [Model.Update] itself never blocks.
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
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/activity"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/checklist"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// tickEvery is how often a busy state drains its events and advances the
// spinner: ten frames a second, the pace of the braille spinner.
const tickEvery = 100 * time.Millisecond

// checkTimeout bounds one manager's check. `winget upgrade` can take a while
// on a cold source cache; anything past two minutes is a hung manager.
const checkTimeout = 2 * time.Minute

// maxLogLines caps how many raw output lines the log view keeps.
const maxLogLines = 1000

// confirmID identifies this screen's single confirm dialog.
const confirmID = "update"

// managerOrder is the order checks are listed and apps are updated in. npm
// comes first: its global packages are updated against the Node.js that is
// installed now, before a winget or Scoop step can move Node.js underneath
// it. Chocolatey is last because it is the one that needs an admin prompt,
// which is best asked for once everything that does not need it is done.
var managerOrder = []string{"npm", "winget", "scoop", "choco"}

// DetectFunc detects the tools on the machine. The default wraps
// [tools.Detector.All] on a fresh [tools.New] detector.
type DetectFunc func(ctx context.Context) []tools.Tool

// RunStepFunc runs one command, streaming its output line by line with
// progress redraws marked transient. The default is [tools.RunStepLines].
type RunStepFunc func(ctx context.Context, argv []string, timeout time.Duration, onLine func(tools.Line)) tools.StepResult

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
	stateChecking
	stateUpToDate
	stateList
	stateConfirm
	stateRunning
	stateSummary
)

// check is one manager's answer to "what is out of date?".
type check struct {
	manager managers.Manager
	done    bool
	report  managers.OutdatedReport
	// failed is set when the check could not be read at all; the manager
	// then offers one "update everything" row instead of a list.
	failed  bool
	elapsed time.Duration
}

// pkg is one row of the pick list: an app, or a manager's fallback
// "update everything" entry.
type pkg struct {
	manager managers.Manager
	out     managers.Outdated
	// all marks the fallback entry that runs the manager's upgrade-all
	// commands, used when its check could not be read.
	all      bool
	selected bool
	// locked is why the row cannot be ticked, e.g. "pinned"; "" when it can.
	locked string
	// note is shown at the right of a row that can be ticked but starts
	// unticked, e.g. "needs explicit upgrade".
	note string
}

// label is the row's name.
func (p pkg) label() string {
	if p.all {
		return "Everything " + p.manager.Name() + " manages"
	}
	if p.out.Name != "" {
		return p.out.Name
	}
	return p.out.ID
}

// listRow is one line of the pick list: a manager heading or an app.
type listRow struct {
	heading bool
	group   int // index into Model.groups
	pkg     int // index into Model.pkgs; -1 for a heading
}

// group is one manager's part of the pick list.
type group struct {
	manager managers.Manager
	pkgs    []int
	// footnote is the muted line under the group, e.g. "1 app with an
	// unknown version isn't shown".
	footnote string
}

// job is one row of a run: what it runs and how it is doing.
type job struct {
	pkg     int // index into Model.pkgs; -1 for a cleanup job
	manager managers.Manager
	label   string
	cmds    [][]string
	row     activity.Row
}

// keyMap is the screen's own key bindings, on top of the global ones.
type keyMap struct {
	Up     key.Binding
	Down   key.Binding
	Toggle key.Binding
	All    key.Binding
	None   key.Binding
	Select key.Binding
	Stop   key.Binding
	Log    key.Binding
	Done   key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑↓", "move")),
		Down:   key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓", "down")),
		Toggle: key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "toggle")),
		All:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all")),
		None:   key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "none")),
		Select: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "update")),
		Stop:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "stop")),
		Log:    key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "log")),
		Done:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "done")),
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

// WithRunStepFunc overrides how a check or an update command is run.
func WithRunStepFunc(f RunStepFunc) Option { return func(m *Model) { m.runStepFn = f } }

// WithLaunchElevatedFunc overrides how the elevated worker is launched.
func WithLaunchElevatedFunc(f LaunchElevatedFunc) Option {
	return func(m *Model) { m.launchElevatedFn = f }
}

// WithClock overrides the clock, so tests can pin elapsed times.
func WithClock(now func() time.Time) Option { return func(m *Model) { m.now = now } }

// Model implements uictx.Screen, checked here so a signature change is a
// compile error in this package rather than a nil interface at the router.
var (
	_ uictx.Screen           = Model{}
	_ uictx.BusyReporter     = Model{}
	_ uictx.Stopper          = Model{}
	_ uictx.ProgressReporter = Model{}
)

// Model is the update screen.
type Model struct {
	detectFn         DetectFunc
	runStepFn        RunStepFunc
	launchElevatedFn LaunchElevatedFunc
	now              func() time.Time

	keys keyMap

	state state
	frame int

	checks     []check
	checkStart time.Time
	checkCh    chan checkResultMsg

	pkgs   []pkg
	groups []group
	rows   []listRow
	cursor int

	confirm confirm.Model

	run      *runHandle
	jobs     []job
	events   chan runEvent
	runStart time.Time
	elapsed  time.Duration
	log      []string
	showLog  bool
}

// Busy implements uictx.BusyReporter: an update run is in flight, which is
// when Esc means "stop" rather than "back" and Ctrl+C waits for the app in
// progress to finish before quitting. It covers the whole stateRunning span,
// including the time an elevated-worker step spends connecting, since that
// is also work that must not be interrupted mid-item.
func (m Model) Busy() bool { return m.state == stateRunning }

// Stop implements uictx.Stopper. It cancels the checks or the run in
// flight and, if an elevated worker is connected, closes it too, so nothing keeps running
// orphaned after the screen is popped or the user asks to quit. It never
// blocks: the goroutine finishes the app it already started (marked
// "cancelled") and every app after it is marked cancelled without running,
// landing on the summary rather than leaving the run stuck mid-flight.
func (m Model) Stop() { m.run.stop() }

// TerminalProgress implements uictx.ProgressReporter: Windows Terminal's
// taskbar button fills as apps finish, and pulses while checking.
func (m Model) TerminalProgress() *tea.ProgressBar {
	switch m.state {
	case stateChecking:
		return tea.NewProgressBar(tea.ProgressBarIndeterminate, 0)
	case stateRunning:
		done := 0
		for _, j := range m.jobs {
			if j.row.State.Final() {
				done++
			}
		}
		if len(m.jobs) == 0 {
			return nil
		}
		return tea.NewProgressBar(tea.ProgressBarDefault, done*100/len(m.jobs))
	}
	return nil
}

// New returns the update screen. Detection starts when [Model.Init] runs.
func New(opts ...Option) Model {
	m := Model{
		detectFn:         func(ctx context.Context) []tools.Tool { return tools.New().All(ctx) },
		runStepFn:        tools.RunStepLines,
		launchElevatedFn: launchWorker,
		now:              time.Now,
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

// Title implements uictx.Screen. While a run is going the breadcrumb names
// the app in flight, so the header alone says what is happening.
func (m Model) Title() string {
	if m.state == stateRunning {
		for _, j := range m.jobs {
			if j.row.State == activity.Running {
				return "Update Everything › " + j.manager.Name() + " › " + j.label
			}
		}
	}
	return "Update Everything"
}

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	switch m.state {
	case stateList:
		return []key.Binding{m.keys.Up, m.keys.Toggle, m.keys.All, m.keys.None, m.keys.Select}
	case stateConfirm:
		return m.confirm.Keys.ShortHelp()
	case stateRunning:
		return []key.Binding{m.keys.Stop, m.keys.Log}
	case stateSummary:
		return []key.Binding{m.keys.Done, m.keys.Log}
	default:
		return nil
	}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	switch m.state {
	case stateList:
		return [][]key.Binding{{m.keys.Up, m.keys.Down, m.keys.Toggle}, {m.keys.All, m.keys.None, m.keys.Select}}
	case stateConfirm:
		return m.confirm.Keys.FullHelp()
	case stateRunning:
		return [][]key.Binding{{m.keys.Stop, m.keys.Log}}
	case stateSummary:
		return [][]key.Binding{{m.keys.Done, m.keys.Log}}
	default:
		return nil
	}
}

// detectResultMsg carries the outcome of Init's background detection.
type detectResultMsg struct {
	tools []tools.Tool
}

// checkResultMsg is one manager's check, finished.
type checkResultMsg struct {
	index   int
	report  managers.OutdatedReport
	failed  bool
	elapsed time.Duration
}

// tickMsg advances the spinner while checking, and drains finished checks.
type tickMsg struct{}

// runEvent is one item streamed back from the goroutine driving updates.
type runEvent struct {
	job      int
	started  bool
	line     string
	progress *tools.Progress
	finished bool
	state    activity.State
	detail   string
	elapsed  time.Duration
	allDone  bool
}

// runBatchMsg is one tick's worth of drained [runEvent]s.
type runBatchMsg struct {
	events []runEvent
	closed bool
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case detectResultMsg:
		return m.onDetected(msg)
	case tickMsg:
		return m.onTick()
	case confirm.AnsweredMsg:
		if msg.ID == confirmID {
			return m.onAnswered(msg)
		}
	case runBatchMsg:
		return m.onBatch(msg)
	}

	switch m.state {
	case stateList:
		return m.updateList(msg, ctx)
	case stateConfirm:
		if cm, ok := msg.(tea.MouseClickMsg); ok && cm.Button == tea.MouseLeft {
			var cmd tea.Cmd
			m.confirm, cmd = m.confirm.Click(ctx, cm.X-confirmLeft, ctx.BodyRow(cm.Y))
			return m, cmd
		}
		var cmd tea.Cmd
		m.confirm, cmd = m.confirm.Update(msg)
		return m, cmd
	case stateRunning:
		return m.updateRunning(msg)
	case stateSummary:
		return m.updateSummary(msg)
	}
	return m, nil
}

// confirmLeft is the column the confirm card is drawn from.
const confirmLeft = 0

// updateRunning handles the keyboard while a run is in flight. It is the
// screen's own answer to Esc, reachable here because a Busy screen keeps the
// root model from popping on Esc and forwards the key instead (internal/app
// and internal/ui/uictx.BusyReporter). Stopping never pops: the run finishes
// the app in flight, marks everything after it "cancelled" and lands on the
// summary, same as Ctrl+C via [Model.Stop].
func (m Model) updateRunning(msg tea.Msg) (uictx.Screen, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch {
	case key.Matches(km, m.keys.Log):
		m.showLog = !m.showLog
	case key.Matches(km, m.keys.Stop):
		m.run.stop()
		return m, uictx.Status("warning", "Stopping: the app in flight is cut short, the rest are skipped…")
	}
	return m, nil
}

// updateSummary handles the finished run: Enter goes back, l shows the log.
func (m Model) updateSummary(msg tea.Msg) (uictx.Screen, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch {
	case key.Matches(km, m.keys.Log):
		m.showLog = !m.showLog
	case key.Matches(km, m.keys.Done):
		return m, uictx.Pop()
	}
	return m, nil
}

// onDetected starts one check per detected manager, all at once.
func (m Model) onDetected(msg detectResultMsg) (uictx.Screen, tea.Cmd) {
	mgrs := detectedManagers(msg.tools)
	if len(mgrs) == 0 {
		m.state = stateNoManagers
		return m, nil
	}
	m.checks = make([]check, len(mgrs))
	m.checkCh = make(chan checkResultMsg, len(mgrs))
	m.checkStart = m.now()
	// The checks share one context, held where Stop can reach it: leaving
	// the screen mid-check must not leave `winget upgrade` or
	// `scoop update` running behind the user's back.
	checkCtx, cancel := context.WithCancel(context.Background())
	m.run = newRunHandle(cancel)
	for i, mgr := range mgrs {
		m.checks[i] = check{manager: mgr}
		go runCheck(checkCtx, m.runStepFn, m.now, i, mgr, m.checkCh)
	}
	m.state = stateChecking
	return m, tick()
}

// runCheck asks one manager what is out of date and posts the answer. It
// runs on its own goroutine; the channel is buffered for every manager, so
// the send never blocks.
func runCheck(parent context.Context, run RunStepFunc, now func() time.Time, index int, mgr managers.Manager, out chan<- checkResultMsg) {
	ctx, cancel := context.WithTimeout(parent, checkTimeout)
	defer cancel()
	start := now()
	cmds := mgr.CheckCmds()
	var text strings.Builder
	for i, argv := range cmds {
		last := i == len(cmds)-1
		res := run(ctx, argv, checkTimeout, func(l tools.Line) {
			if last && !l.Transient {
				text.WriteString(l.Text)
				text.WriteByte('\n')
			}
		})
		if ctx.Err() != nil || (!last && !res.OK && res.ExitCode == -1) {
			out <- checkResultMsg{index: index, failed: true, elapsed: now().Sub(start)}
			return
		}
	}
	report := mgr.ParseOutdatedReport(text.String())
	out <- checkResultMsg{index: index, report: report, failed: !report.Parsed, elapsed: now().Sub(start)}
}

// tick schedules the next spinner frame.
func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

// onTick advances the spinner and folds in any check that has finished.
// When the last one is in, it builds the pick list.
func (m Model) onTick() (uictx.Screen, tea.Cmd) {
	if m.state != stateChecking {
		return m, nil
	}
	m.frame++
	for {
		select {
		case r := <-m.checkCh:
			c := &m.checks[r.index]
			c.done, c.report, c.failed, c.elapsed = true, r.report, r.failed, r.elapsed
			continue
		default:
		}
		break
	}
	for _, c := range m.checks {
		if !c.done {
			return m, tick()
		}
	}
	m.buildList()
	if len(m.pkgs) == 0 {
		m.state = stateUpToDate
		return m, nil
	}
	m.state = stateList
	m.cursor = m.firstPkgRow()
	return m, nil
}

// buildList turns the finished checks into the grouped pick list.
func (m *Model) buildList() {
	m.pkgs, m.groups, m.rows = nil, nil, nil
	for _, c := range m.checks {
		g := group{manager: c.manager}
		if c.failed {
			g.pkgs = append(g.pkgs, len(m.pkgs))
			m.pkgs = append(m.pkgs, pkg{manager: c.manager, all: true, selected: true, note: "couldn't list apps"})
		} else {
			for _, o := range managers.UpgradeOrder(c.manager.Name(), c.report.Packages) {
				p := pkg{manager: c.manager, out: o, selected: true}
				switch {
				case managers.IsDevpit(o.ID):
					p.locked, p.selected = "Devpit updates itself", false
				case o.Pinned:
					p.locked, p.selected = "pinned", false
				case o.Held:
					p.locked, p.selected = "held", false
				case o.Explicit:
					p.note, p.selected = "needs explicit upgrade", false
				}
				g.pkgs = append(g.pkgs, len(m.pkgs))
				m.pkgs = append(m.pkgs, p)
			}
			g.footnote = footnote(c.report)
		}
		if len(g.pkgs) == 0 {
			continue
		}
		gi := len(m.groups)
		m.groups = append(m.groups, g)
		m.rows = append(m.rows, listRow{heading: true, group: gi, pkg: -1})
		for _, pi := range g.pkgs {
			m.rows = append(m.rows, listRow{group: gi, pkg: pi})
		}
	}
}

// footnote is the muted line under a manager's group, for the apps it
// counted but did not list.
func footnote(r managers.OutdatedReport) string {
	var parts []string
	if r.Unknown > 0 {
		parts = append(parts, plural(r.Unknown, "app with an unknown version isn't shown", "apps with an unknown version aren't shown"))
	}
	if r.Pinned > 0 {
		parts = append(parts, plural(r.Pinned, "pinned app is left alone", "pinned apps are left alone"))
	}
	return strings.Join(parts, " · ")
}

// plural renders "1 thing" or "n things".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// firstPkgRow is the row the cursor starts on: the first app.
func (m Model) firstPkgRow() int {
	for i, r := range m.rows {
		if !r.heading {
			return i
		}
	}
	return 0
}

// updateList handles navigation, ticking and moving to the confirm dialog.
func (m Model) updateList(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch pm := msg.(type) {
	case tea.MouseClickMsg:
		if pm.Button != tea.MouseLeft {
			return m, nil
		}
		if i, ok := m.rowAt(ctx, ctx.BodyRow(pm.Y)); ok {
			m.cursor = i
			m.toggle(i)
		}
		return m, nil
	case tea.MouseMotionMsg:
		// Hover highlights without scrolling: a highlight that would slide
		// the list under a still pointer is not applied.
		if i, ok := m.rowAt(ctx, ctx.BodyRow(pm.Y)); ok {
			before, _ := m.window(ctx)
			moved := m
			moved.cursor = i
			if after, _ := moved.window(ctx); after == before {
				m = moved
			}
		}
		return m, nil
	case tea.MouseWheelMsg:
		switch pm.Button {
		case tea.MouseWheelUp:
			m.cursor = max(0, m.cursor-1)
		case tea.MouseWheelDown:
			m.cursor = min(len(m.rows)-1, m.cursor+1)
		}
		return m, nil
	}

	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch {
	case key.Matches(km, m.keys.Up):
		m.cursor = max(0, m.cursor-1)
	case key.Matches(km, m.keys.Down):
		m.cursor = min(len(m.rows)-1, m.cursor+1)
	case key.Matches(km, m.keys.Toggle):
		m.toggle(m.cursor)
	case key.Matches(km, m.keys.All):
		m.setAll(true)
	case key.Matches(km, m.keys.None):
		m.setAll(false)
	case key.Matches(km, m.keys.Select):
		picked := m.picked()
		if len(picked) == 0 {
			return m, uictx.Status("warning", "Tick at least one app first")
		}
		m.confirm = confirm.New(confirmID, fmt.Sprintf("Update %s?", plural(len(picked), "app", "apps")), m.confirmDetail(picked))
		m.state = stateConfirm
	}
	return m, nil
}

// toggle flips a row's tick: an app on its own, a heading for its whole
// group (ticking all when any is unticked). Locked rows never change.
func (m *Model) toggle(row int) {
	if row < 0 || row >= len(m.rows) {
		return
	}
	r := m.rows[row]
	if !r.heading {
		if p := &m.pkgs[r.pkg]; p.locked == "" {
			p.selected = !p.selected
		}
		return
	}
	ticked, total := m.groupTicks(r.group)
	on := ticked < total
	for _, pi := range m.groups[r.group].pkgs {
		if m.pkgs[pi].locked == "" {
			m.pkgs[pi].selected = on
		}
	}
}

// setAll ticks or unticks every row that can be.
func (m *Model) setAll(on bool) {
	for i := range m.pkgs {
		if m.pkgs[i].locked == "" {
			m.pkgs[i].selected = on
		}
	}
}

// groupTicks counts a group's ticked and tickable apps.
func (m Model) groupTicks(gi int) (ticked, total int) {
	for _, pi := range m.groups[gi].pkgs {
		if m.pkgs[pi].locked != "" {
			continue
		}
		total++
		if m.pkgs[pi].selected {
			ticked++
		}
	}
	return ticked, total
}

// picked returns the indices of the ticked apps, in list order.
func (m Model) picked() []int {
	var out []int
	for i, p := range m.pkgs {
		if p.selected && p.locked == "" {
			out = append(out, i)
		}
	}
	return out
}

// confirmDetail lists the commands the run will use, one per manager, so
// the user sees exactly what will be executed before saying yes.
func (m Model) confirmDetail(picked []int) string {
	var lines []string
	seen := map[string]bool{}
	for _, i := range picked {
		p := m.pkgs[i]
		name := p.manager.Name()
		if seen[name] {
			continue
		}
		seen[name] = true
		n := 0
		for _, j := range picked {
			if m.pkgs[j].manager.Name() == name {
				n++
			}
		}
		if p.all {
			for _, argv := range p.manager.UpgradeAllCmds() {
				lines = append(lines, name+": "+strings.Join(argv, " "))
			}
			continue
		}
		example := strings.Join(p.manager.UpgradeCmd(p.out.ID), " ")
		lines = append(lines, fmt.Sprintf("%s (%s): %s", name, plural(n, "app", "apps"), example))
	}
	return strings.Join(lines, "\n")
}

// onAnswered starts the run on Yes, and goes back to the list on No.
func (m Model) onAnswered(msg confirm.AnsweredMsg) (uictx.Screen, tea.Cmd) {
	if msg.Answer == confirm.AnswerNo {
		m.state = stateList
		return m, nil
	}
	m.run.stop() // the checks are all in; release their context
	m.jobs = m.buildJobs(m.picked())
	m.log = nil
	m.showLog = false
	m.runStart = m.now()
	m.events = make(chan runEvent, 256)

	runCtx, cancel := context.WithCancel(context.Background())
	m.run = newRunHandle(cancel)
	go runJobs(runCtx, m.runStepFn, m.launchElevatedFn, m.now, m.jobs, m.events, m.run)

	m.state = stateRunning
	return m, waitForEvents(m.events)
}

// buildJobs turns the ticked apps into the run, in manager order, with a
// cleanup job after a manager that leaves old versions behind.
func (m Model) buildJobs(picked []int) []job {
	var jobs []job
	for _, name := range managerOrder {
		var ids []string
		var mgr managers.Manager
		for _, i := range picked {
			p := m.pkgs[i]
			if p.manager.Name() != name {
				continue
			}
			mgr = p.manager
			j := job{pkg: i, manager: p.manager, label: p.label(), row: activity.Row{
				Label: p.label(), Old: p.out.Current, New: p.out.Latest, State: activity.Queued, Percent: -1,
			}}
			if p.all {
				j.cmds = p.manager.UpgradeAllCmds()
			} else {
				j.cmds = [][]string{p.manager.UpgradeCmd(p.out.ID)}
				ids = append(ids, p.out.ID)
			}
			jobs = append(jobs, j)
		}
		if mgr == nil || len(ids) == 0 {
			continue
		}
		if cmds := mgr.CleanupCmds(ids); len(cmds) > 0 {
			label := "Clear old " + name + " versions"
			jobs = append(jobs, job{pkg: -1, manager: mgr, label: label, cmds: cmds, row: activity.Row{
				Label: label, State: activity.Queued, Percent: -1,
			}})
		}
	}
	return jobs
}

// onBatch folds one tick's worth of streamed events into the model.
func (m Model) onBatch(msg runBatchMsg) (uictx.Screen, tea.Cmd) {
	m.frame++
	finished := msg.closed
	for _, ev := range msg.events {
		if ev.allDone {
			finished = true
			continue
		}
		if ev.job < 0 || ev.job >= len(m.jobs) {
			continue
		}
		r := &m.jobs[ev.job].row
		switch {
		case ev.started:
			r.State = activity.Running
		case ev.finished:
			r.State, r.Detail, r.Elapsed, r.Percent = ev.state, ev.detail, ev.elapsed, -1
		case ev.progress != nil:
			if ev.progress.Percent >= 0 {
				r.Percent = ev.progress.Percent
			}
		case ev.line != "":
			r.Detail = ev.line
			m.log = append(m.log, "["+m.jobs[ev.job].label+"] "+ev.line)
			if len(m.log) > maxLogLines {
				m.log = m.log[len(m.log)-maxLogLines:]
			}
		}
		if ev.elapsed > 0 && !ev.finished {
			r.Elapsed = ev.elapsed
		}
	}
	// A running row's clock ticks even while its manager is silent.
	for i := range m.jobs {
		if m.jobs[i].row.State == activity.Running {
			m.jobs[i].row.Elapsed += tickEvery
		}
	}
	if finished {
		// Anything the run never reported on was cut short by a Stop: a
		// full channel can drop a cancelled run's last events, but the
		// goroutine always closes it on the way out.
		for i := range m.jobs {
			if r := &m.jobs[i].row; !r.State.Final() {
				r.State, r.Detail, r.Percent = activity.Skipped, "cancelled", -1
			}
		}
		m.elapsed = m.now().Sub(m.runStart)
		m.state = stateSummary
		m.run = nil
		return m, nil
	}
	return m, waitForEvents(m.events)
}

// runJobs runs every job in order, streaming lines and outcomes over events,
// and closes events when done. It runs entirely on its own goroutine.
//
// ctx is cancelled by [Model.Stop] (Esc mid-run or Ctrl+C). Every send to
// events selects on ctx.Done() so a Stop against an abandoned consumer can
// never leave this goroutine blocked forever on a full, unread channel. A
// cancellation partway through marks the job that was running "cancelled",
// marks every job after it "cancelled" without starting it, and still
// reaches the summary rather than leaving the screen stuck on "running".
func runJobs(ctx context.Context, run RunStepFunc, launch LaunchElevatedFunc, now func() time.Time, jobs []job, events chan<- runEvent, handle *runHandle) {
	defer close(events)
	var worker elevatedClient
	var workerErr error
	defer func() {
		if worker != nil {
			handle.clearClient()
			_ = worker.Close()
		}
	}()

	for i, j := range jobs {
		if ctx.Err() != nil {
			cancelFrom(ctx, events, i, len(jobs))
			break
		}
		sendEvent(ctx, events, runEvent{job: i, started: true})
		start := now()

		var fin runEvent
		if j.manager.NeedsElevation() {
			if worker == nil && workerErr == nil {
				worker, workerErr = launch(ctx)
				if workerErr == nil {
					handle.setClient(worker)
				}
			}
			if workerErr != nil {
				fin = elevationFailure(workerErr)
			} else {
				fin = runElevated(ctx, worker, j, i, events)
			}
		} else {
			fin = runPlain(ctx, run, j, i, events)
		}
		fin.job, fin.finished, fin.elapsed = i, true, now().Sub(start)
		sendEvent(ctx, events, fin)
		if ctx.Err() != nil {
			cancelFrom(ctx, events, i+1, len(jobs))
			break
		}
	}
	sendEvent(ctx, events, runEvent{allDone: true})
}

// runPlain runs one job's commands directly, reporting output lines and
// progress, and returns its final event.
func runPlain(ctx context.Context, run RunStepFunc, j job, idx int, events chan<- runEvent) runEvent {
	var res tools.StepResult
	for _, argv := range j.cmds {
		res = run(ctx, argv, tools.DefaultStepTimeout, func(l tools.Line) {
			if p, ok := tools.ParseProgress(l.Text); ok && p.Percent >= 0 {
				pc := p
				sendEvent(ctx, events, runEvent{job: idx, progress: &pc})
			}
			if !l.Transient {
				sendEvent(ctx, events, runEvent{job: idx, line: l.Text})
			}
		})
		if ctx.Err() != nil {
			return runEvent{state: activity.Skipped, detail: "cancelled"}
		}
		if !res.OK {
			break
		}
	}
	return verdictEvent(j.manager.Name(), res.ExitCode, res.LastLines)
}

// runElevated runs one job's commands through the elevated worker.
func runElevated(ctx context.Context, worker elevatedClient, j job, idx int, events chan<- runEvent) runEvent {
	code := 0
	var last []string
	for _, argv := range j.cmds {
		var err error
		code, err = worker.Exec(ctx, argv, tools.DefaultStepTimeout, func(_, text string) {
			text = tools.CleanLine(text)
			if text == "" {
				return
			}
			last = append(last, text)
			if len(last) > 20 {
				last = last[len(last)-20:]
			}
			sendEvent(ctx, events, runEvent{job: idx, line: text})
		})
		if ctx.Err() != nil {
			return runEvent{state: activity.Skipped, detail: "cancelled"}
		}
		if err != nil {
			var died *elevate.WorkerDiedError
			if errors.As(err, &died) {
				return runEvent{state: activity.Failed, detail: "admin helper stopped: " + lastOr(died.LastLines, err.Error())}
			}
			return runEvent{state: activity.Failed, detail: err.Error()}
		}
		if code != 0 {
			break
		}
	}
	return verdictEvent(j.manager.Name(), code, last)
}

// elevationFailure is the final event for a job whose admin helper could
// not start: skipped when the user said no to the prompt, failed otherwise.
func elevationFailure(err error) runEvent {
	var declined *elevate.DeclinedError
	if errors.As(err, &declined) {
		return runEvent{state: activity.Skipped, detail: "skipped (needs admin)"}
	}
	var died *elevate.WorkerDiedError
	if errors.As(err, &died) {
		return runEvent{state: activity.Failed, detail: "admin helper stopped: " + lastOr(died.LastLines, err.Error())}
	}
	return runEvent{state: activity.Failed, detail: err.Error()}
}

// verdictEvent turns a finished command into the row's final state, in the
// plain words [managers.Explain] chooses.
func verdictEvent(manager string, code int, last []string) runEvent {
	v := managers.Explain(manager, code, last)
	switch v.Kind {
	case managers.VerdictOK:
		return runEvent{state: activity.Done}
	case managers.VerdictUpToDate:
		return runEvent{state: activity.Done, detail: v.Text}
	case managers.VerdictRestart:
		return runEvent{state: activity.Warn, detail: v.Text}
	case managers.VerdictPinned, managers.VerdictCancelled:
		return runEvent{state: activity.Skipped, detail: v.Text}
	default:
		detail := v.Text
		if l := lastOr(last, ""); l != "" && v.Kind == managers.VerdictFailed {
			detail += " · " + l
		}
		return runEvent{state: activity.Failed, detail: detail}
	}
}

// lastOr is the last non-empty line, or fallback.
func lastOr(lines []string, fallback string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return fallback
}

// cancelFrom marks jobs [from, n) cancelled without running them.
func cancelFrom(ctx context.Context, events chan<- runEvent, from, n int) {
	for i := from; i < n; i++ {
		sendEvent(ctx, events, runEvent{job: i, finished: true, state: activity.Skipped, detail: "cancelled"})
	}
}

// sendEvent delivers ev without ever blocking forever. It sends at once when
// there is room, and otherwise waits for either room or ctx to be cancelled,
// so a Stop against a full, unread channel still lets the goroutine exit
// instead of leaking. The final events of a cancelled run are sent after
// ctx is done; they go through when there is room, which the buffer leaves
// for them in every realistic run.
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
		evs, closed := drainEvents(ch)
		return runBatchMsg{events: evs, closed: closed}
	})
}

// drainEvents reads everything currently buffered on ch without blocking,
// and reports whether ch has been closed.
func drainEvents(ch chan runEvent) ([]runEvent, bool) {
	var batch []runEvent
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return batch, true
			}
			batch = append(batch, ev)
		default:
			return batch, false
		}
	}
}

// detectedManagers returns the managers found on the machine, in run order.
func detectedManagers(detected []tools.Tool) []managers.Manager {
	found := make(map[string]bool, len(detected))
	for _, t := range detected {
		if t.Found {
			found[t.Name] = true
		}
	}
	var out []managers.Manager
	for _, name := range managerOrder {
		if !found[name] {
			continue
		}
		for _, mgr := range managers.All() {
			if mgr.Name() == name {
				out = append(out, mgr)
				break
			}
		}
	}
	return out
}

// ---- View -----------------------------------------------------------------

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	th := ctx.Theme
	switch m.state {
	case stateDetecting:
		return " " + th.Accent.Render(ctx.SpinnerFrame(m.frame)) + " " + th.Muted.Render("Looking for package managers…")
	case stateNoManagers:
		return th.Warning.Render(" " + ctx.Icons.Warn +
			" No package manager (winget, Scoop, npm or Chocolatey) was found, so there is nothing to update.")
	case stateChecking:
		return m.viewChecking(ctx)
	case stateUpToDate:
		return m.viewUpToDate(ctx)
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

// viewChecking is one spinner row per manager, each turning into a tick and
// a count as its answer comes in.
func (m Model) viewChecking(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	b.WriteString(" " + th.Title.Render("Checking for updates"))
	b.WriteString("\n\n")
	for i, c := range m.checks {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(m.checkRow(ctx, c))
	}
	return b.String()
}

// checkRow draws one manager's check line.
func (m Model) checkRow(ctx uictx.Context, c check) string {
	th := ctx.Theme
	name := fmt.Sprintf("%-8s", c.manager.Name())
	switch {
	case !c.done:
		return " " + th.Accent.Render(ctx.SpinnerFrame(m.frame)) + " " + th.Base.Render(name) +
			th.Muted.Render("asking "+c.manager.Name()+" what's out of date…")
	case c.failed:
		return " " + th.Warning.Render(ctx.Icons.Warn) + " " + th.Base.Render(name) +
			th.Warning.Render("couldn't list apps; will offer to update everything")
	default:
		n := len(c.report.Packages)
		text := "up to date"
		style := th.Muted
		if n > 0 {
			text, style = plural(n, "update", "updates"), th.Info
		}
		return " " + th.Success.Render(ctx.Icons.Tick) + " " + th.Base.Render(name) + style.Render(text) +
			th.Muted.Render("  "+activity.Duration(c.elapsed))
	}
}

// viewUpToDate is the happy ending: nothing to do.
func (m Model) viewUpToDate(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder
	for _, c := range m.checks {
		b.WriteString(m.checkRow(ctx, c))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(th.CardFor(ctx.Icons.Tier == icons.TierASCII).Render(
		th.Success.Bold(true).Render(ctx.Icons.Tick+" Everything's up to date.") + th.Muted.Render("  Nice.")))
	return b.String()
}

// listHead is the rows the pick list draws above its first row.
const listHead = 3

// viewList draws the grouped pick list, windowed around the cursor, with the
// selection count under it.
func (m Model) viewList(ctx uictx.Context) string {
	th := ctx.Theme
	var b strings.Builder

	total := 0
	var per []string
	for _, g := range m.groups {
		total += len(g.pkgs)
		per = append(per, fmt.Sprintf("%s %d", g.manager.Name(), len(g.pkgs)))
	}
	dot := " · "
	if ctx.Icons.Tier == icons.TierASCII {
		dot = " - "
	}
	b.WriteString(" " + th.Title.Render(plural(total, "update available", "updates available")) +
		th.Muted.Render(" "+dot+" "+strings.Join(per, dot)))
	b.WriteString("\n")
	b.WriteString(" " + th.Muted.Render("Everything is ticked. Untick anything you'd rather keep as it is."))
	b.WriteString("\n\n")

	start, end := m.window(ctx)
	nameW := m.nameWidth()
	for i := start; i < end; i++ {
		b.WriteString(m.renderRow(ctx, i, nameW))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(m.selectionLine(ctx))
	return b.String()
}

// listRows is how many pick-list rows fit on screen.
func (m Model) listRows(ctx uictx.Context) int {
	if ctx.BodyHeight <= 0 {
		return len(m.rows)
	}
	return max(3, ctx.BodyHeight-listHead-2)
}

// window is the slice of rows on screen, keeping the cursor in view.
func (m Model) window(ctx uictx.Context) (start, end int) {
	h := m.listRows(ctx)
	n := len(m.rows)
	if n <= h {
		return 0, n
	}
	start = min(max(0, m.cursor-h/2), n-h)
	return start, start + h
}

// rowAt maps a body row to a pick-list row.
func (m Model) rowAt(ctx uictx.Context, bodyRow int) (int, bool) {
	start, end := m.window(ctx)
	i := start + bodyRow - listHead
	if bodyRow < listHead || i >= end {
		return 0, false
	}
	return i, true
}

// nameWidth is the name column, so every version lines up.
func (m Model) nameWidth() int {
	w := 0
	for _, p := range m.pkgs {
		w = max(w, ansi.StringWidth(p.label()))
	}
	return min(w, 40)
}

// renderRow draws one pick-list row: a manager heading with its group tick,
// or an app with its versions.
func (m Model) renderRow(ctx uictx.Context, i, nameW int) string {
	r := m.rows[i]
	sel := i == m.cursor
	if r.heading {
		g := m.groups[r.group]
		ticked, total := m.groupTicks(r.group)
		box := checklist.Off
		switch {
		case total > 0 && ticked == total:
			box = checklist.On
		case ticked > 0:
			box = checklist.Some
		}
		note := fmt.Sprintf("%d of %d", ticked, total)
		if g.footnote != "" {
			sep := "  ·  "
			if ctx.Icons.Tier == icons.TierASCII {
				sep = "  -  "
			}
			note = g.footnote + sep + note
		}
		style := ctx.Theme.Subtitle
		return checklist.Render(ctx, checklist.Line{Selected: sel, Box: box, Text: g.manager.Name(), Style: &style, Note: note}, ctx.Width)
	}
	p := m.pkgs[r.pkg]
	line := checklist.Line{Selected: sel, Box: checklist.BoxOf(p.selected), Indent: 2, Text: p.label(), TextWidth: nameW}
	switch {
	case p.all:
		line.Aside = strings.Join(p.manager.UpgradeAllCmds()[0], " ")
		line.Note = p.note
	default:
		line.Aside = p.out.Current + " " + ctx.Icons.Arrow + " " + p.out.Latest
		line.Note = p.note
	}
	if p.locked != "" {
		line.Box, line.Dim, line.Note = checklist.Blank, true, p.locked
	}
	return checklist.Render(ctx, line, ctx.Width)
}

// selectionLine is the count under the list.
func (m Model) selectionLine(ctx uictx.Context) string {
	th := ctx.Theme
	n, total := 0, 0
	for _, p := range m.pkgs {
		if p.locked != "" {
			continue
		}
		total++
		if p.selected {
			n++
		}
	}
	return " " + th.Muted.Render("Selected ") + th.Success.Bold(true).Render(fmt.Sprint(n)) +
		th.Muted.Render(fmt.Sprintf(" of %d", total))
}

// viewRunning draws the run: the header with the overall bar, then one live
// row per app, or the raw log when asked for.
func (m Model) viewRunning(ctx uictx.Context) string {
	done, current := 0, ""
	for _, j := range m.jobs {
		if j.row.State.Final() {
			done++
		} else if j.row.State == activity.Running && current == "" {
			current = j.manager.Name()
		}
	}
	elapsed := m.now().Sub(m.runStart)
	var b strings.Builder
	b.WriteString(activity.Header(ctx, "Updating", done, len(m.jobs), current, elapsed, ctx.Width))
	b.WriteString("\n\n")
	b.WriteString(m.body(ctx, ctx.BodyHeight-3))
	return b.String()
}

// body is the job rows, or the log when it is showing.
func (m Model) body(ctx uictx.Context, height int) string {
	if m.showLog {
		th := ctx.Theme
		if len(m.log) == 0 {
			return th.Muted.Render("  Nothing logged yet.")
		}
		return th.Subtitle.Render(" Full log") + th.Muted.Render("  (l hides it)") + "\n" +
			activity.LogView(ctx, m.log, ctx.Width, max(1, height-1))
	}
	rows := make([]activity.Row, len(m.jobs))
	for i, j := range m.jobs {
		rows[i] = j.row
	}
	return activity.View(ctx, rows, m.frame, ctx.Width, height)
}

// viewSummary is the done box over the finished rows.
func (m Model) viewSummary(ctx uictx.Context) string {
	rows := make([]activity.Row, len(m.jobs))
	for i, j := range m.jobs {
		rows[i] = j.row
	}
	// The cleanup jobs are housekeeping, not apps: they show as rows but do
	// not count towards "Updated n".
	var apps []activity.Row
	for _, j := range m.jobs {
		if j.pkg >= 0 {
			apps = append(apps, j.row)
		}
	}
	var b strings.Builder
	b.WriteString(activity.DoneBox(ctx, "Updated", activity.Count(apps), m.elapsed, ctx.Width))
	b.WriteString("\n\n")
	b.WriteString(m.body(ctx, ctx.BodyHeight-5))
	return b.String()
}
