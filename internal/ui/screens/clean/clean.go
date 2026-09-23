// Package clean is the Free Up Disk Space screen: the whole path from
// "which folder?" to "freed 8.7 GB", in one state machine.
//
// The states are submenu → path picker → scanning → results → confirm →
// deleting → summary, and the arrow between results and deleting is the most
// important line of code in Devpit. It is guarded by [Model.beginDelete],
// which is the only function in the package that can put the screen into the
// deleting state, and it refuses unless two things are true at once: the
// selection is not empty, and the message that got it there is a
// [confirm.AnsweredMsg] carrying [confirm.AnswerYes] for this screen's
// dialog. That is safety rule 1 of docs/safety.md, and
// TestCannotReachDeletingWithoutConfirm holds it down.
//
// Everything that touches a disk is a field on the model: the scanner, the
// delete engine, the retry, the sweep and the scan cache. The real ones come
// from [DefaultEngines]; the tests pass fakes and never delete anything.
//
// Neither the scan nor the delete ever runs on the update loop. Both run on
// their own goroutine and report through a channel that a tea.Cmd drains in
// 150 ms slices, so a scan of a 100k-file tree cannot make a keystroke feel
// slow.
package clean

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	cleanengine "github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/scan/rules"
	"github.com/zubairbinshaukat/devpit/internal/telemetry"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/pathpicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/progress"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/restable"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/summary"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// state is where the screen is in the clean flow.
type state int

// The states, in the order the user walks through them.
const (
	stateMenu state = iota
	statePicker
	stateScanning
	stateResults
	stateConfirm
	stateDeleting
	stateSummary
)

// Submenu row identifiers.
const (
	rowProject = "project"
	rowFull    = "full"
	rowChoose  = "choose"
	rowResume  = "resume"
)

// dialogID identifies this screen's confirmation. An AnsweredMsg with any
// other ID is not this screen's to act on, which matters because the router
// broadcasts every message to the top screen.
const dialogID = "clean.delete"

// typedWord is what the Careful tier makes the user type.
const typedWord = "DELETE"

// keyDebounce is how close two identical key presses have to be before the
// second is treated as an echo. Some Windows consoles deliver a key twice.
const keyDebounce = 5 * time.Millisecond

// Model implements uictx.Screen, checked here so a signature change is a
// compile error in this package rather than a nil interface at the router.
var (
	_ uictx.Screen           = Model{}
	_ uictx.BusyReporter     = Model{}
	_ uictx.Stopper          = Model{}
	_ uictx.ProgressReporter = Model{}
)

// Model is the clean screen.
type Model struct {
	state state

	// full says the next scan uses the Full Scan rule set rather than the
	// project rules.
	full bool
	// root is the folder being scanned.
	root string

	menu   menu.Model
	picker pathpicker.Model
	table  restable.Model
	prog   progress.Model
	dialog confirm.Model
	card   summary.Model

	// Scan bookkeeping.
	cancel       context.CancelFunc
	batches      <-chan scan.Batch
	scanStats    scan.Stats
	scanErr      error
	streamClosed bool
	scanEnded    bool
	stale        bool
	fresh        bool

	// Delete bookkeeping. pending is the confirmed selection and is the only
	// thing beginDelete will act on.
	pending []scan.Item
	// ruleNames maps a confirmed item's lower-cased path to the rule that
	// matched it. It is captured when the delete starts, because the usage
	// stats report is built after pending has been cleared, and it holds
	// rule names rather than items so nothing else can be reported by
	// accident.
	ruleNames  map[string]string
	deleteFeed <-chan cleanengine.Progress
	report     cleanengine.Report
	locked     []cleanengine.Result
	retrying   bool
	freedSoFar uint64

	engines Engines
	now     func() time.Time

	lastKey   string
	lastKeyAt time.Time

	keys KeyMap
}

// KeyMap holds the bindings the screen owns on top of the components'.
type KeyMap struct {
	// Delete starts the confirmation from the results table.
	Delete key.Binding
	// Rescan throws away the results and scans again.
	Rescan key.Binding
	// Retry runs the locked items again, after the user closed whatever held
	// them.
	Retry key.Binding
	// Stop interrupts a scan or a delete. A delete finishes the item in
	// flight first; nothing is ever left half-removed.
	Stop key.Binding
	// Back leaves the screen.
	Back key.Binding
}

// DefaultKeyMap returns the screen's bindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Delete: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete ticked")),
		Rescan: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rescan")),
		Retry:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "retry locked")),
		Stop:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "stop")),
		Back:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// New returns the clean screen wired to the real scan and delete engines.
//
// It is the constructor the home menu uses: `uictx.Push(clean.New())`. The
// screen reads the configuration from the render context on every frame, so
// it needs nothing passed in, and it starts no work until the user picks a
// row.
func New() Model {
	return Model{
		menu:    menu.New(submenuRows()),
		table:   restable.New(),
		prog:    progress.New("Scanning"),
		engines: DefaultEngines(),
		now:     time.Now,
		keys:    DefaultKeyMap(),
	}
}

// WithEngines replaces the scan and delete entry points. Tests use it to
// drive the whole flow without a filesystem.
func (m Model) WithEngines(e Engines) Model {
	if e.Scan != nil {
		m.engines.Scan = e.Scan
	}
	if e.Clean != nil {
		m.engines.Clean = e.Clean
	}
	if e.Retry != nil {
		m.engines.Retry = e.Retry
	}
	if e.Sweep != nil {
		m.engines.Sweep = e.Sweep
	}
	if e.LoadCache != nil {
		m.engines.LoadCache = e.LoadCache
	}
	if e.SaveCache != nil {
		m.engines.SaveCache = e.SaveCache
	}
	if e.Verify != nil {
		m.engines.Verify = e.Verify
	}
	if e.Report != nil {
		m.engines.Report = e.Report
	}
	if e.Getenv != nil {
		m.engines.Getenv = e.Getenv
	}
	return m
}

// WithClock replaces the screen's clock, so tests can step past the key
// debounce without sleeping.
func (m Model) WithClock(now func() time.Time) Model {
	if now != nil {
		m.now = now
	}
	return m
}

// Init implements uictx.Screen. The screen starts idle: nothing is scanned
// until the user asks for it.
func (m Model) Init() tea.Cmd { return nil }

// Title implements uictx.Screen.
func (m Model) Title() string {
	switch m.state {
	case statePicker:
		return "Free Up Disk Space · Folder"
	case stateScanning:
		return "Free Up Disk Space · Scanning"
	case stateResults:
		return "Free Up Disk Space · Results"
	case stateConfirm:
		return "Free Up Disk Space · Confirm"
	case stateDeleting:
		return "Free Up Disk Space · Deleting"
	case stateSummary:
		return "Free Up Disk Space · Done"
	default:
		return "Free Up Disk Space"
	}
}

// StateName reports which step of the flow the screen is on, as one of
// "menu", "picker", "scanning", "results", "confirm", "deleting" or
// "summary". It exists for the tests that pin the flow; nothing in the UI
// reads it.
func (m Model) StateName() string {
	switch m.state {
	case statePicker:
		return "picker"
	case stateScanning:
		return "scanning"
	case stateResults:
		return "results"
	case stateConfirm:
		return "confirm"
	case stateDeleting:
		return "deleting"
	case stateSummary:
		return "summary"
	default:
		return "menu"
	}
}

// Root is the folder the screen is working on.
func (m Model) Root() string { return m.root }

// Stale reports that the table is showing a previous scan while a fresh one
// runs behind it.
func (m Model) Stale() bool { return m.stale && !m.fresh }

// Table exposes the results table, for tests.
func (m Model) Table() restable.Model { return m.table }

// Progress exposes the progress component, so the app can put its fraction on
// tea.View.ProgressBar.
func (m Model) Progress() progress.Model { return m.prog }

// Busy implements uictx.BusyReporter: a scan or a delete is running, which is
// when Esc means "stop" rather than "go back", Ctrl+C waits for the item in
// flight, and the app shows a terminal progress bar.
func (m Model) Busy() bool { return m.state == stateScanning || m.state == stateDeleting }

// Stop implements uictx.Stopper. It is the same cancellation Esc performs,
// reachable by the router when this screen is popped and by the app when the
// user presses Ctrl+C mid-delete. It never waits: the engine finishes the
// item it already started, and anything it does not reach stays a tombstone
// for the sweep.
func (m Model) Stop() { m.stop() }

// TerminalProgress implements uictx.ProgressReporter, putting the same
// fraction the on-screen bar shows onto the terminal's taskbar.
func (m Model) TerminalProgress() *tea.ProgressBar { return m.prog.TerminalBar() }

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	switch m.state {
	case stateResults:
		return append(m.table.Keys.ShortHelp(), m.keys.Delete, m.keys.Rescan)
	case stateConfirm:
		return m.dialog.Keys.ShortHelp()
	case stateScanning, stateDeleting:
		return []key.Binding{m.keys.Stop}
	case stateSummary:
		if len(m.locked) > 0 {
			return []key.Binding{m.card.Keys.Continue, m.keys.Retry}
		}
		return m.card.Keys.ShortHelp()
	case statePicker:
		return m.picker.Keys.ShortHelp()
	default:
		return []key.Binding{m.menu.Keys.Up, m.menu.Keys.Select, m.keys.Back}
	}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	switch m.state {
	case stateResults:
		return append(m.table.Keys.FullHelp(), []key.Binding{m.keys.Delete, m.keys.Rescan, m.keys.Back})
	case stateSummary:
		return [][]key.Binding{{m.card.Keys.Continue, m.keys.Retry}}
	default:
		return [][]key.Binding{{m.menu.Keys.Up, m.menu.Keys.Down, m.menu.Keys.Select}, {m.keys.Back}}
	}
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		if m.isEcho(km) {
			return m, nil
		}
		m.lastKey, m.lastKeyAt = km.String(), m.clock()
	}

	m = m.resize(ctx)

	switch msg := msg.(type) {
	case menu.SelectedMsg:
		return m.activate(msg.ID, ctx)

	case pathpicker.ChosenMsg:
		m.root = msg.Path
		return m.startScan(ctx)

	case pathpicker.CancelledMsg:
		m.state = stateMenu
		return m, nil

	case pathpicker.BrowsedMsg:
		// The folder browser's result is a Cmd output addressed to the picker;
		// Bubble Tea delivers it here, so hand it back down or Browse hangs.
		if m.state != statePicker {
			return m, nil
		}
		next, cmd := m.picker.Update(msg)
		m.picker = next
		return m, cmd

	case scanBatchMsg:
		return m.onBatch(msg, ctx)

	case scanStreamClosedMsg:
		m.streamClosed = true
		return m.maybeFinishScan(ctx)

	case scanDoneMsg:
		m.scanEnded = true
		m.scanStats = msg.stats
		m.scanErr = msg.err
		return m.maybeFinishScan(ctx)

	case cacheLoadedMsg:
		return m.onCacheLoaded(msg), nil

	case cacheSavedMsg:
		if msg.err != nil {
			return m, uictx.Status("warning", "The scan could not be cached: "+msg.err.Error())
		}
		return m, nil

	case restable.ProceedMsg:
		return m.askToDelete(ctx)

	case confirm.AnsweredMsg:
		return m.onAnswer(msg, ctx)

	case deleteProgressMsg:
		next, cmd := m.onDeleteProgress(msg)
		return next, cmd

	case deleteStreamClosedMsg:
		return m, nil

	case deleteDoneMsg:
		return m.onDeleteDone(msg.report, ctx)

	case retryDoneMsg:
		return m.onRetryDone(msg.results, ctx)

	case sweepDoneMsg:
		return m.onSweepDone(msg.report, ctx)

	case summary.DismissedMsg:
		return m, uictx.Pop()

	case tea.KeyPressMsg:
		return m.onKey(msg, ctx)
	}
	return m, nil
}

// onKey routes a key press to whatever owns the keyboard right now.
func (m Model) onKey(km tea.KeyPressMsg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch m.state {
	case stateMenu:
		next, cmd := m.menu.Update(km)
		m.menu = next
		return m, cmd

	case statePicker:
		next, cmd := m.picker.Update(km)
		m.picker = next
		return m, cmd

	case stateScanning:
		if key.Matches(km, m.keys.Stop) {
			m.stop()
			return m, uictx.Status("warning", "Stopping the scan…")
		}
		return m, nil

	case stateResults:
		// While the filter box has the keyboard every key is text, so the
		// screen's own bindings stay out of the way: typing "d" into a
		// filter must never open the delete confirmation.
		if m.table.Filtering() {
			next, cmd := m.table.Update(km)
			m.table = next
			return m, cmd
		}
		if key.Matches(km, m.keys.Delete) {
			return m.askToDelete(ctx)
		}
		if key.Matches(km, m.keys.Rescan) {
			return m.startScan(ctx)
		}
		if key.Matches(km, m.keys.Back) {
			m.state = stateMenu
			return m, nil
		}
		next, cmd := m.table.Update(km)
		m.table = next
		return m, cmd

	case stateConfirm:
		next, cmd := m.dialog.Update(km)
		m.dialog = next
		return m, cmd

	case stateDeleting:
		// Esc finishes the item in flight and stops there. Nothing is ever
		// left half-removed: the tombstone rename means an interrupted item
		// is a renamed folder the sweep finishes later.
		if key.Matches(km, m.keys.Stop) {
			m.stop()
			return m, uictx.Status("warning", "Finishing the current item, then stopping…")
		}
		return m, nil

	case stateSummary:
		if len(m.locked) > 0 && !m.retrying && key.Matches(km, m.keys.Retry) {
			return m.startRetry(ctx)
		}
		next, cmd := m.card.Update(km)
		m.card = next
		return m, cmd
	}
	return m, nil
}

// activate acts on a submenu row.
func (m Model) activate(id string, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch id {
	case rowProject, rowFull:
		m.full = id == rowFull
		if root := strings.TrimSpace(ctx.Config.DefaultProjectsFolder); root != "" {
			m.root = root
			return m.startScan(ctx)
		}
		return m.openPicker(ctx), nil

	case rowChoose:
		m.full = false
		return m.openPicker(ctx), nil

	case rowResume:
		return m.startSweep(ctx)
	}
	return m, nil
}

// openPicker shows the folder picker, listing the folders in the config.
func (m Model) openPicker(ctx uictx.Context) Model {
	m.picker = pathpicker.New(ctx.Config)
	m.state = statePicker
	return m
}

// startScan kicks off a scan of m.root and shows whatever the cache holds
// for it in the meantime.
func (m Model) startScan(ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if strings.TrimSpace(m.root) == "" {
		return m.openPicker(ctx), nil
	}
	m.stop()

	cctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.state = stateScanning
	m.scanStats = scan.Stats{}
	m.scanErr = nil
	m.streamClosed, m.scanEnded, m.stale, m.fresh = false, false, false, false
	m.pending = nil

	m.table = restable.New().
		SetNow(m.now).
		SetOlderDays(ctx.Config.OlderDays).
		SetStreaming(true)
	m.prog = progress.New("Scanning").SetWidth(ctx.Width)
	m = m.resize(ctx)

	root := m.root
	full := m.full
	cfg := ctx.Config
	scanFn := m.engines.Scan

	items := make(chan scan.Item, 256)
	done := make(chan scanDoneMsg, 1)
	box := &statsBox{}

	go func() {
		// rules.All probes the filesystem for the package-cache locations,
		// so it is built here, once per scan, and never on the update loop.
		table := rules.Project()
		if full {
			table = rules.All()
		}
		st, err := scanFn(cctx, scan.Options{
			Roots:      []string{root},
			NeverTouch: cfg.NeverTouch,
			ActiveDays: cfg.ActiveDays,
			OlderDays:  cfg.OlderDays,
			Rules:      table,
		}, items, box.set)
		// The caller owns out and closes it after Run returns.
		close(items)
		done <- scanDoneMsg{stats: st, err: err}
	}()

	m.batches = scan.Stream(cctx, items, batchEvery, box.get)

	// The cache load comes first so a previous scan is on screen before the
	// first fresh batch arrives, which is the whole point of the badge.
	return m, tea.Batch(
		m.loadCacheCmd(root),
		waitBatch(m.batches),
		waitScanDone(done),
	)
}

// loadCacheCmd reads the previous scan of a root off the update loop.
func (m Model) loadCacheCmd(root string) tea.Cmd {
	load := m.engines.LoadCache
	if load == nil {
		return nil
	}
	return func() tea.Msg {
		c, err := load(root)
		if err != nil || c == nil {
			return cacheLoadedMsg{root: root}
		}
		return cacheLoadedMsg{root: root, cache: c}
	}
}

// onCacheLoaded shows a previous scan under a "stale · rescanning" badge, but
// only while the fresh scan has not produced anything of its own.
func (m Model) onCacheLoaded(msg cacheLoadedMsg) Model {
	if msg.cache == nil || m.fresh || m.state != stateScanning {
		return m
	}
	if !strings.EqualFold(msg.root, m.root) || len(msg.cache.Items) == 0 {
		return m
	}
	m.table = m.table.SetItems(msg.cache.Items)
	m.stale = true
	return m
}

// onBatch folds a batch of fresh results into the table.
func (m Model) onBatch(msg scanBatchMsg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.stale && !m.fresh {
		// The first fresh result retires the cached ones: from here on the
		// table is showing this scan, not the last one.
		m.table = m.table.SetItems(nil)
	}
	m.fresh = true
	m.table = m.table.Append(msg.batch.Items)
	m.scanStats = msg.batch.Stats
	m.prog = m.prog.
		SetCounts(clampCount(m.scanStats.Found), 0).
		SetCurrent(lastPath(msg.batch.Items))
	m.prog.Bytes = m.scanStats.Bytes
	m = m.resize(ctx)
	return m, waitBatch(m.batches)
}

// maybeFinishScan moves to the results once both the walker has returned and
// the stream has delivered its last batch.
func (m Model) maybeFinishScan(ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if !m.streamClosed || !m.scanEnded || m.state != stateScanning {
		return m, nil
	}
	if m.stale && !m.fresh {
		// The fresh scan found nothing, so the cached rows are not results.
		m.table = m.table.SetItems(nil)
	}
	m.stale = false
	m.table = m.table.SetStreaming(false)
	m.state = stateResults
	m = m.resize(ctx)

	cmds := []tea.Cmd{m.saveCacheCmd()}
	switch {
	case m.scanErr != nil && errors.Is(m.scanErr, context.Canceled):
		cmds = append(cmds, uictx.Status("warning", "Scan stopped. These are the results so far."))
	case m.scanErr != nil:
		cmds = append(cmds, uictx.Status("danger", "Scan problem: "+m.scanErr.Error()))
	case m.table.Len() == 0:
		cmds = append(cmds, uictx.Status("success", "Nothing to clean in "+m.root))
	}
	return m, tea.Batch(cmds...)
}

// saveCacheCmd stores the finished scan so the next visit draws instantly.
func (m Model) saveCacheCmd() tea.Cmd {
	save := m.engines.SaveCache
	if save == nil {
		return nil
	}
	c := &scan.Cache{Root: m.root, Items: m.table.Items(), When: m.clock()}
	return func() tea.Msg { return cacheSavedMsg{err: save(c)} }
}

// askToDelete builds the confirmation for whatever is ticked. It is the only
// way into the confirm state, and it refuses an empty selection.
func (m Model) askToDelete(ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.state != stateResults {
		return m, nil
	}
	selected := m.table.Selected()
	if len(selected) == 0 {
		return m, uictx.Status("warning", "Nothing is ticked. Press space to tick a row.")
	}

	m.pending = selected
	m.dialog = confirm.New(dialogID, deleteQuestion(selected), restoreHints(selected))
	if hasCareful(selected) {
		m.dialog = m.dialog.WithTypedWord(typedWord)
	}
	m.state = stateConfirm
	return m, nil
}

// onAnswer handles the confirmation's outcome. A No goes back to the results
// with the selection untouched; a Yes is the only thing that ever reaches
// beginDelete.
func (m Model) onAnswer(msg confirm.AnsweredMsg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if msg.ID != dialogID || m.state != stateConfirm {
		return m, nil
	}
	if msg.Answer != confirm.AnswerYes {
		m.state = stateResults
		m.dialog = m.dialog.Reset()
		m.pending = nil
		return m, uictx.Status("", "Nothing was deleted.")
	}
	return m.beginDelete(msg, ctx)
}

// beginDelete is the only function that can put this screen into the
// deleting state, and it is safety rule 1 in code.
//
// It refuses unless the answer it was handed is a Yes for this screen's own
// dialog and the confirmed selection is not empty. Every other path into the
// screen — a stray Enter, a "d", a "y" pressed on the results table — ends
// somewhere else, because nothing else calls this function.
func (m Model) beginDelete(answer confirm.AnsweredMsg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if answer.ID != dialogID || answer.Answer != confirm.AnswerYes {
		m.state = stateResults
		return m, nil
	}
	if len(m.pending) == 0 {
		m.state = stateResults
		return m, uictx.Status("warning", "Nothing is ticked.")
	}
	if m.engines.Clean == nil {
		m.state = stateResults
		return m, uictx.Status("danger", "The delete engine is not available.")
	}

	items := m.toCleanItems(m.pending)
	m.ruleNames = ruleNames(m.pending)

	cctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.state = stateDeleting
	m.locked = nil
	m.freedSoFar = 0
	m.prog = progress.New("Deleting").SetWidth(ctx.Width).SetCounts(0, len(items))
	m = m.resize(ctx)

	opts := cleanengine.Options{NeverTouch: ctx.Config.NeverTouch}
	deleteFn := m.engines.Clean

	progressCh := make(chan cleanengine.Progress, 256)
	reportCh := make(chan cleanengine.Report, 1)

	go func() {
		rep := deleteFn(cctx, items, opts, func(p cleanengine.Progress) {
			// A blocked progress callback blocks the delete, so a full
			// channel drops the update rather than stalling the disk.
			select {
			case progressCh <- p:
			default:
			}
		})
		close(progressCh)
		reportCh <- rep
	}()

	m.deleteFeed = progressCh

	return m, tea.Batch(
		waitDeleteProgress(progressCh),
		waitReport(reportCh, func(r cleanengine.Report) tea.Msg { return deleteDoneMsg{report: r} }),
	)
}

// onDeleteProgress folds one progress update into the bar and asks for the
// next 150 ms slice.
func (m Model) onDeleteProgress(msg deleteProgressMsg) (Model, tea.Cmd) {
	if m.state != stateDeleting {
		return m, nil
	}
	p := msg.p
	m.prog = m.prog.SetCounts(p.Index, p.Total).SetCurrent(p.Path)
	m.prog.Bytes = p.Freed
	m.freedSoFar = p.Freed
	return m, waitDeleteProgress(m.deleteFeed)
}

// onDeleteDone turns a finished report into the summary card and folds the
// freed bytes into the lifetime total.
func (m Model) onDeleteDone(rep cleanengine.Report, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	m.stop()
	m.report = rep
	m.locked = lockedResults(rep)
	m.card = summary.New(reportCard(rep))
	m.state = stateSummary
	m.pending = nil
	m = m.resize(ctx)
	return m, tea.Batch(m.persist(ctx, rep.Freed), m.reportCmd(ctx, rep))
}

// startRetry runs the locked items again, which is the R key on the summary.
func (m Model) startRetry(ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if len(m.locked) == 0 || m.engines.Retry == nil {
		return m, nil
	}
	m.retrying = true

	items := make([]cleanengine.Item, 0, len(m.locked))
	for _, r := range m.locked {
		items = append(items, r.Item)
	}
	opts := cleanengine.Options{NeverTouch: ctx.Config.NeverTouch}
	retry := m.engines.Retry

	return m, func() tea.Msg {
		out := make([]cleanengine.Result, 0, len(items))
		for _, it := range items {
			out = append(out, retry(context.Background(), it, opts, nil))
		}
		return retryDoneMsg{results: out}
	}
}

// onRetryDone folds the retried items back into the report.
func (m Model) onRetryDone(results []cleanengine.Result, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	m.retrying = false

	freed := uint64(0)
	kept := make([]cleanengine.Result, 0, len(m.report.Skipped))
	done := map[string]cleanengine.Result{}
	for _, r := range results {
		done[strings.ToLower(r.Path)] = r
	}
	for _, r := range m.report.Skipped {
		if fresh, ok := done[strings.ToLower(r.Path)]; ok {
			if fresh.Err == nil {
				m.report.Deleted = append(m.report.Deleted, fresh)
				freed += fresh.Size
				continue
			}
			kept = append(kept, fresh)
			continue
		}
		kept = append(kept, r)
	}

	m.report.Skipped = kept
	m.report.Freed += freed
	m.locked = lockedResults(m.report)
	m.card = summary.New(reportCard(m.report))
	m = m.resize(ctx)

	if freed == 0 {
		return m, uictx.Status("warning", "Still locked. Close the program holding it and press R again.")
	}
	return m, tea.Batch(
		m.persist(ctx, freed),
		uictx.Status("success", "Retried and freed "+header.FormatBytes(freed)),
	)
}

// startSweep finishes the tombstones an interrupted delete left behind.
func (m Model) startSweep(ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.engines.Sweep == nil {
		return m, uictx.Status("danger", "The sweep engine is not available.")
	}
	roots := sweepRoots(ctx.Config)
	if len(roots) == 0 {
		return m, uictx.Status("warning", "No folders to resume. Scan one first.")
	}

	cctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.state = stateDeleting
	m.locked = nil
	m.prog = progress.New("Finishing interrupted deletes").SetWidth(ctx.Width)
	m = m.resize(ctx)

	opts := cleanengine.Options{NeverTouch: ctx.Config.NeverTouch}
	sweep := m.engines.Sweep

	progressCh := make(chan cleanengine.Progress, 256)
	reportCh := make(chan cleanengine.Report, 1)
	go func() {
		rep := sweep(cctx, roots, opts, func(p cleanengine.Progress) {
			select {
			case progressCh <- p:
			default:
			}
		})
		close(progressCh)
		reportCh <- rep
	}()

	m.deleteFeed = progressCh

	return m, tea.Batch(
		waitDeleteProgress(progressCh),
		waitReport(reportCh, func(r cleanengine.Report) tea.Msg { return sweepDoneMsg{report: r} }),
	)
}

// onSweepDone shows what the sweep finished.
func (m Model) onSweepDone(rep cleanengine.Report, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	m.stop()
	m.report = rep
	m.locked = lockedResults(rep)

	card := reportCard(rep)
	card.Headline = fmt.Sprintf("Finished %d interrupted delete%s", len(rep.Deleted), plural(len(rep.Deleted)))
	if len(rep.Deleted) == 0 && len(rep.Skipped) == 0 {
		card.Headline = "Nothing left over to finish"
	}
	m.card = summary.New(card)
	m.state = stateSummary
	m = m.resize(ctx)
	return m, nil
}

// persist folds freed bytes into the lifetime total and remembers the folder.
func (m Model) persist(ctx uictx.Context, freed uint64) tea.Cmd {
	cfg := ctx.Config
	cfg.LifetimeFreedBytes += freed
	if m.root != "" {
		cfg.AddRecentFolder(m.root)
		if cfg.DefaultProjectsFolder == "" {
			cfg.DefaultProjectsFolder = m.root
		}
	}
	return uictx.SaveConfig(cfg)
}

// reportCmd sends one usage-stats report for a finished cleanup, in the
// background, or returns nil when there is nothing to send or the user never
// asked for stats.
//
// Only the main delete reports; a retry does not, so an item cannot be
// counted twice. The command runs on its own goroutine like every tea.Cmd,
// bounds itself with the client's timeout, swallows whatever comes back and
// returns no message, so a dead server can neither delay the summary nor put
// an error on screen. That is PRIVACY.md's "never blocks, never shows an
// error", in code.
func (m Model) reportCmd(ctx uictx.Context, rep cleanengine.Report) tea.Cmd {
	send := m.engines.Report
	if send == nil {
		return nil
	}
	getenv := m.engines.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if !telemetry.Enabled(ctx.Config, getenv) {
		return nil
	}

	cfg := ctx.Config
	rules := m.ruleNames
	return func() tea.Msg {
		cctx, cancel := context.WithTimeout(context.Background(), telemetry.DefaultTimeout)
		defer cancel()
		_ = send(cctx, cfg, rep, rules)
		return nil
	}
}

// ruleNames maps each item's lower-cased path to the rule that matched it.
// Lower-cased because Windows paths are compared that way, and rule names
// only because that is all a report may ever carry.
func ruleNames(items []scan.Item) map[string]string {
	out := make(map[string]string, len(items))
	for _, it := range items {
		out[strings.ToLower(it.Path)] = it.Rule
	}
	return out
}

// stop cancels whatever is running. A delete finishes the item in flight
// first, which is the engine's contract, not this screen's.
func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

// resize hands the current size to the components that need it.
func (m Model) resize(ctx uictx.Context) Model {
	m.table = m.table.SetSize(ctx.Width, m.tableHeight(ctx))
	m.prog = m.prog.SetWidth(ctx.Width)
	return m
}

// tableHeight is the rows the table may draw into, once the screen's own
// heading and footer line have taken theirs.
func (m Model) tableHeight(ctx uictx.Context) int {
	return max(3, ctx.BodyHeight-3)
}

// isEcho reports that a key press is a duplicate of the last one, delivered
// within the debounce window. Some Windows consoles send every key twice.
func (m Model) isEcho(km tea.KeyPressMsg) bool {
	if m.lastKey == "" || m.lastKey != km.String() {
		return false
	}
	return m.clock().Sub(m.lastKeyAt) < keyDebounce
}

// clock returns the screen's time source.
func (m Model) clock() time.Time {
	if m.now == nil {
		return time.Now()
	}
	return m.now()
}

// toCleanItems turns the confirmed selection into delete-engine items, each
// carrying the Verify that re-checks its rule immediately before it is
// removed. That is safety rule 10, carried from here into the engine.
func (m Model) toCleanItems(items []scan.Item) []cleanengine.Item {
	verify := m.engines.Verify
	out := make([]cleanengine.Item, 0, len(items))
	for _, it := range items {
		item := it
		var check func() error
		if verify != nil {
			check = func() error { return verify(item) }
		}
		out = append(out, cleanengine.Item{
			Path:        it.Path,
			Size:        it.Size,
			Tier:        toCleanTier(it.Tier),
			Verify:      check,
			RestoreHint: it.RestoreHint,
		})
	}
	return out
}

// toCleanTier maps the scanner's tier onto the delete engine's. They are two
// separate types that happen to be numbered the same way, and a plain cast
// would keep working right up until one of them gained a tier or reordered —
// at which point a Careful item would be deleted permanently as if it were
// Safe, silently. The switch makes that a compile-time conversation instead,
// and anything it does not recognise becomes Careful: the tier that goes to
// the Recycle Bin, asks for a typed word, and is never pre-ticked.
func toCleanTier(t scan.Tier) cleanengine.Tier {
	switch t {
	case scan.TierSafe:
		return cleanengine.TierSafe
	case scan.TierReview:
		return cleanengine.TierReview
	case scan.TierCareful:
		return cleanengine.TierCareful
	default:
		return cleanengine.TierCareful
	}
}

// submenuRows is the four things this screen can do.
func submenuRows() []menu.Item {
	return []menu.Item{
		{
			ID:    rowProject,
			Title: "Project Junk",
			Desc:  "node_modules, dist, target, build output in your projects folder",
		},
		{
			ID:    rowFull,
			Title: "Full Scan",
			Desc:  "Project junk plus package caches, Windows temp and editor caches",
		},
		{
			ID:    rowChoose,
			Title: "Choose folder…",
			Desc:  "Scan a folder other than your default one",
		},
		{
			ID:    rowResume,
			Title: "Resume interrupted deletes",
			Desc:  "Finish anything a stopped clean left behind",
		},
	}
}

// sweepRoots is where a resume looks for leftovers: the default folder and
// everything scanned recently.
func sweepRoots(cfg config.Config) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(cfg.RecentFolders)+1)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[strings.ToLower(p)] {
			return
		}
		seen[strings.ToLower(p)] = true
		out = append(out, p)
	}
	add(cfg.DefaultProjectsFolder)
	for _, p := range cfg.RecentFolders {
		add(p)
	}
	return out
}

// hasCareful reports whether the selection holds a Careful item, which is
// what turns the confirmation into a typed-word one.
func hasCareful(items []scan.Item) bool {
	for _, it := range items {
		if it.Tier == scan.TierCareful {
			return true
		}
	}
	return false
}

// tierCounts counts the selection by risk tier.
func tierCounts(items []scan.Item) map[scan.Tier]int {
	out := map[scan.Tier]int{}
	for _, it := range items {
		out[it.Tier]++
	}
	return out
}

// totalBytes is the size of a selection.
func totalBytes(items []scan.Item) uint64 {
	var n uint64
	for _, it := range items {
		n += it.Size
	}
	return n
}

// deleteQuestion is the confirmation's single sentence: how many things, how
// big, and how risky.
func deleteQuestion(items []scan.Item) string {
	counts := tierCounts(items)
	parts := make([]string, 0, 3)
	for _, t := range []scan.Tier{scan.TierSafe, scan.TierReview, scan.TierCareful} {
		if n := counts[t]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, t))
		}
	}
	return fmt.Sprintf("Delete %d item%s (%s)?  %s",
		len(items), plural(len(items)), header.FormatBytes(totalBytes(items)),
		strings.Join(parts, ", "),
	)
}

// restoreHints is the "how to get this back" line under the question. Every
// rule carries one and the confirmation always shows them: safety rule 14.
func restoreHints(items []scan.Item) string {
	seen := map[string]bool{}
	hints := make([]string, 0, 4)
	for _, it := range items {
		h := strings.TrimSpace(it.RestoreHint)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		hints = append(hints, h)
		if len(hints) == 3 {
			break
		}
	}
	if len(hints) == 0 {
		return "Review and Careful items go to the Recycle Bin."
	}
	return strings.Join(hints, "  ·  ")
}

// lockedResults picks out the items something else had open.
func lockedResults(rep cleanengine.Report) []cleanengine.Result {
	var out []cleanengine.Result
	for _, r := range rep.Skipped {
		var locked *cleanengine.LockedError
		if errors.As(r.Err, &locked) {
			out = append(out, r)
		}
	}
	return out
}

// reportCard turns a delete report into the summary card's input.
func reportCard(rep cleanengine.Report) summary.Result {
	skipped := make([]summary.Skipped, 0, len(rep.Skipped))
	for _, r := range rep.Skipped {
		skipped = append(skipped, summary.Skipped{
			Name:   shortPath(r.Path),
			Reason: r.Reason,
		})
	}
	return summary.Result{
		Headline:   "Freed " + header.FormatBytes(rep.Freed),
		FreedBytes: rep.Freed,
		Items:      len(rep.Deleted),
		Duration:   rep.Elapsed,
		Skipped:    skipped,
	}
}

// shortPath names an item by its last two components, which is enough to
// tell api\node_modules from web\node_modules without overflowing the line.
func shortPath(p string) string {
	trimmed := strings.TrimRight(p, `\/`)
	if trimmed == "" {
		return "the item"
	}
	i := strings.LastIndexAny(trimmed, `\/`)
	if i < 0 {
		return trimmed
	}
	j := strings.LastIndexAny(trimmed[:i], `\/`)
	if j < 0 {
		return trimmed
	}
	return trimmed[j+1:]
}

// lastPath is the path of the last item in a batch, for the progress line.
func lastPath(items []scan.Item) string {
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1].Path
}

// clampCount narrows a scanner counter to the int the progress component
// takes, without wrapping on a machine that somehow found more than an int
// can hold.
func clampCount(n uint64) int {
	const maxInt = int64(^uint(0) >> 1)
	if n > uint64(maxInt) {
		return int(maxInt)
	}
	return int(n)
}

// plural is the "s" on a count.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
