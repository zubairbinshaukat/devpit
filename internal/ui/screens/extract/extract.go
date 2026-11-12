// Package extract is the "Extract big archive in place" screen, pushed from
// the Clean menu and from Share's done card.
//
// The screen plans, asks, watches and explains; it never touches an archive.
// Everything that does is hatchzip, driven through internal/extract behind
// the small [Extractor] interface. The walk is pick → check (the space plan)
// → confirm → running → done, with side doors for a missing hatchzip (install
// it here and carry on), a password, and an unfinished extraction (Resume or
// Restore).
//
// The arrow between the confirmation and the run is the line that matters.
// It is guarded by [Model.beginExtract], the only function in the package
// that calls [Extractor.Start], and it refuses unless the message that got it
// there is a [confirm.AnsweredMsg] carrying Yes for this screen's own run
// dialog and the check that just passed left a ticket. That is safety rule 76
// of docs/safety.md, and TestCannotReachRunningWithoutConfirm holds it down.
//
// Nothing here runs on the update loop. Checks, the extraction, the install
// steps and the restore each run in a command or on their own goroutine and
// report through messages stamped with a generation number, so a result from
// something the user has already walked away from is dropped.
package extract

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/filepicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/pathpicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// state is where the screen is in the extract flow.
type state int

// The states.
const (
	// statePick is the archive picker.
	statePick state = iota
	// stateChecking is every short wait before a card: looking at the file,
	// looking for hatchzip, and `hatchzip check` itself.
	stateChecking
	// stateTool says hatchzip is missing or too old, and offers to install it.
	stateTool
	stateToolConfirm
	stateInstalling
	stateInstallFailed
	// stateCheck is the space plan card, or why the check did not pass.
	stateCheck
	// stateFolder is the folder picker, opened from the plan card to choose
	// where the new folder goes.
	stateFolder
	// stateUnfinished offers Resume or Restore.
	stateUnfinished
	stateConfirm
	statePassword
	stateRunning
	stateDone
	statePaused
	stateFailed
	stateRestoreConfirm
	stateRestoring
	stateRestored
)

// The dialogs. An AnsweredMsg with any other ID is not this screen's.
const (
	runDialogID     = "extract.run"
	installDialogID = "extract.install"
	restoreDialogID = "extract.restore"
)

// action is what the screen was about to do when it found it needed hatchzip.
type action int

const (
	actCheck action = iota
	actRestore
)

// keyDebounce is how close two identical key presses have to be before the
// second is treated as an echo. Some Windows consoles deliver a key twice,
// and a doubled Enter on the plan card would answer the confirmation No.
const keyDebounce = 5 * time.Millisecond

// spinEvery is how often a waiting state advances its spinner.
const spinEvery = 120 * time.Millisecond

// Model implements uictx.Screen, checked here so a signature change is a
// compile error in this package rather than a nil interface at the router.
var (
	_ uictx.Screen           = Model{}
	_ uictx.BusyReporter     = Model{}
	_ uictx.Stopper          = Model{}
	_ uictx.ProgressReporter = Model{}
)

// Model is the extract screen.
type Model struct {
	// Everything that touches the outside world. The real ones are set by
	// New; tests replace them through the options.
	eng      Extractor
	injected bool
	detect   func(ctx context.Context) tools.Tool
	scoop    func(ctx context.Context) tools.Tool
	runStep  RunStepFunc
	now      func() time.Time
	identify filepicker.IdentifyFunc
	find     func(ctx context.Context, dirs []string) []extractengine.Found
	fsys     extractengine.StatFS
	outDir   func(set archive.Set) string
	free     func(path string) (uint64, bool)
	browse   func(start string) (string, error)
	tick     func(time.Duration, func(time.Time) tea.Msg) tea.Cmd
	// The folder picker's outside world: where a new folder would go inside
	// a picked one, and, for tests, its disk and its Windows dialog.
	outDirIn     func(parent string, set archive.Set) (string, error)
	folderStat   func(string) (os.FileInfo, error)
	folderBrowse func(start string) (string, error)

	// How the screen was opened.
	openPath string
	onlySets []archive.Set
	limited  bool

	// life is cancelled when the screen is discarded; op is the work in
	// flight that Esc and Stop wind down. Both are pointers so every copy of
	// this value screen shares them.
	life *lifetime
	op   *operation

	state    state
	gen      uint64
	frame    int
	spinning bool
	details  bool
	// waitText replaces the wait line's default wording.
	waitText string

	picker filepicker.Model
	// folder is the folder picker of the plan card; folderErr is why the
	// folder it last gave was refused; projects is the one folder it lists.
	folder    pathpicker.Model
	folderErr string
	projects  string
	menu      menu.Model
	dialog    confirm.Model
	pass      textinput.Model

	// hatchzip.
	toolOK bool
	tool   toolInfo
	after  action
	inst   installRun

	// The archive and its check.
	name       string
	req        extractengine.Request
	resuming   bool
	started    time.Time // of the unfinished extraction, when known
	modeChosen bool
	autoKeep   bool
	check      extractengine.CheckResult
	ticket     *extractengine.Ticket
	secret     extractengine.Secret
	wrongPass  bool

	// The run.
	prog        extractengine.Progress
	pausing     bool
	autoResumes int // how many times Devpit resumed this run by itself (rule 85)
	out         extractengine.Outcome
	freeAfter   uint64
	freeKnown   bool

	// The restore.
	restoreOK    bool
	restoreStart time.Time
	restoreTook  time.Duration
	restoreLine  string
	restoreErr   string

	lastKey   string
	lastKeyAt time.Time
}

// lifetime is the screen's own context.
type lifetime struct {
	ctx    context.Context
	cancel context.CancelFunc
}

// operation is the one piece of work in flight: an extraction, an install
// step or a restore. Stop and Esc reach it through here, and neither blocks.
type operation struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	job    *extractengine.Job
	// feed carries the newest progress; finished closes when the run ends.
	feed     chan extractengine.Progress
	finished chan struct{}
	// lines carries the output of an install step or a restore.
	lines chan string
}

// begin starts a new piece of work and returns its context.
func (o *operation) begin(parent context.Context) context.Context {
	ctx, cancel := context.WithCancel(parent)
	o.mu.Lock()
	o.cancel = cancel
	o.job = nil
	o.feed = make(chan extractengine.Progress, 1)
	o.finished = make(chan struct{})
	o.lines = make(chan string, 64)
	o.mu.Unlock()
	return ctx
}

// setJob records the running extraction.
func (o *operation) setJob(j *extractengine.Job) {
	o.mu.Lock()
	o.job = j
	o.mu.Unlock()
}

// stop winds the work down: an extraction is paused (the engine sends
// Ctrl+Break and only later escalates), anything else is cancelled. It is
// safe to call twice, and before the work has really started.
func (o *operation) stop() {
	o.mu.Lock()
	cancel, job := o.cancel, o.job
	o.mu.Unlock()
	if job != nil {
		job.Pause()
	}
	if cancel != nil {
		cancel()
	}
}

// push hands the newest progress to the screen without ever blocking the
// engine: an update nobody has read yet is replaced.
func (o *operation) push(feed chan extractengine.Progress, p extractengine.Progress) {
	for {
		select {
		case feed <- p:
			return
		default:
		}
		select {
		case <-feed:
		default:
		}
	}
}

// say hands one output line over, dropping it when nobody is reading.
func say(lines chan string, line string) {
	select {
	case lines <- line:
	default:
	}
}

// New returns the extract screen. With no options it opens on the picker,
// wired to the real hatchzip, disk and Scoop; nothing runs until the user
// picks an archive.
func New(opts ...Option) Model {
	m := Model{
		detect:   detectTool("hatchzip"),
		scoop:    detectTool("scoop"),
		runStep:  tools.RunStep,
		now:      time.Now,
		identify: realIdentify,
		find:     realFind,
		fsys:     realFS(),
		free:     realFree,
		tick:     tea.Tick,
		op:       &operation{},
	}
	for _, opt := range opts {
		opt(&m)
	}
	if m.outDir == nil {
		fsys := m.fsys
		m.outDir = func(set archive.Set) string { return extractengine.DefaultOutDir(set, fsys) }
	}
	if m.outDirIn == nil {
		fsys := m.fsys
		m.outDirIn = func(parent string, set archive.Set) (string, error) {
			return extractengine.OutDirIn(parent, set, fsys)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.life = &lifetime{ctx: ctx, cancel: cancel}

	m.picker = filepicker.New(m.identify).WithClock(m.now).WithStartDir(downloadsFolder())
	if m.browse != nil {
		m.picker = m.picker.WithBrowse(m.browse).WithBrowseAvailable(true)
	}
	switch {
	case m.openPath != "":
		m.state = stateChecking
		m.name = filepicker.Base(m.openPath)
	case m.limited:
		m.picker = m.picker.Only(rowsOf(m.onlySets))
	default:
		m.picker = m.picker.SetLoading(true)
	}
	return m
}

// Init implements uictx.Screen. The first message brings the render context,
// and with it the configuration the archive search needs.
func (m Model) Init() tea.Cmd { return func() tea.Msg { return initMsg{} } }

// Title implements uictx.Screen. The breadcrumb stays under Clean wherever
// the screen was opened from.
func (m Model) Title() string { return "Free Up Disk Space · Extract" }

// StateName reports which step the screen is on. It exists for the tests
// that pin the flow; nothing in the UI reads it.
func (m Model) StateName() string {
	return [...]string{
		"pick", "checking", "tool", "toolConfirm", "installing", "installFailed", "check",
		"folder", "unfinished", "confirm", "password", "running", "done", "paused", "failed",
		"restoreConfirm", "restoring", "restored",
	}[m.state]
}

// Busy implements uictx.BusyReporter: hatchzip, an install step or a restore
// is running, which is when Esc means "pause" or "stop" rather than "back"
// and Ctrl+C waits for it to wind down.
//
// It is also how a step inside the screen keeps Esc: the app pops a screen
// that is not busy before the screen ever sees the key, which from the folder
// picker would throw away the archive and its passed check. Those steps stop
// counting the moment the screen is told to stop, so Ctrl+C never waits on
// one.
func (m Model) Busy() bool {
	if m.state == stateRunning || m.state == stateInstalling || m.state == stateRestoring {
		return true
	}
	return m.ownsEsc() && m.life.ctx.Err() == nil
}

// ownsEsc reports a step where Esc goes back one step inside the screen: the
// details view closes, the folder picker returns to the plan card, a typed
// archive path returns to the list, and the password prompt returns to the
// picker. The archive list itself is not one: Esc there leaves the screen.
func (m Model) ownsEsc() bool {
	if m.details {
		return true
	}
	switch m.state {
	case stateFolder:
		return true
	case statePick:
		return m.picker.Typing()
	case statePassword:
		// Opened on one archive there is no picker to go back to, so Esc
		// leaves, as it does everywhere else on that walk.
		return m.openPath == ""
	}
	return false
}

// Stop implements uictx.Stopper. Over a running extraction it pauses: the
// engine sends Ctrl+Break, then a second one, and only then ends hatchzip's
// job, all inside the app's stop grace period. It never waits. It also ends
// the screen's own context, so a check or a search still in flight stops.
func (m Model) Stop() {
	m.op.stop()
	if m.state != stateRunning {
		// A running extraction is paused, not cancelled outright: its
		// commands still have the outcome to deliver.
		m.life.cancel()
	}
}

// TerminalProgress implements uictx.ProgressReporter, putting the fraction
// the on-screen bar shows onto the terminal's taskbar.
func (m Model) TerminalProgress() *tea.ProgressBar {
	if m.state != stateRunning || m.prog.TotalBytes <= 0 {
		return nil
	}
	return tea.NewProgressBar(tea.ProgressBarDefault, m.percent())
}

// percent is how far the run has got, never 100 before hatchzip has exited 0.
func (m Model) percent() int {
	if m.prog.TotalBytes <= 0 {
		return 0
	}
	pct := float64(m.prog.DoneBytes) * 100 / float64(m.prog.TotalBytes)
	return int(min(99, max(0, pct+0.5)))
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	next, cmd := m.update(msg, ctx)
	return next, cmd
}

// update is Update with the concrete type.
func (m Model) update(msg tea.Msg, ctx uictx.Context) (Model, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		if m.isEcho(km) {
			return m, nil
		}
		m.lastKey, m.lastKeyAt = km.String(), m.now()
	}

	switch msg := msg.(type) {
	case initMsg:
		return m.onInit(ctx)
	case spinMsg:
		return m.onSpin()
	case rowsMsg:
		if !m.limited {
			m.picker = m.picker.SetRows(msg.rows)
		}
		return m, nil

	case filepicker.ChosenMsg:
		if m.state != statePick {
			return m, nil
		}
		return m.prepare(msg.Set)
	case filepicker.CancelledMsg:
		return m, uictx.Pop()
	case filepicker.BrowsedMsg, filepicker.CheckedMsg:
		// Results of the picker's own commands: Bubble Tea delivers them
		// here, so hand them back down or the picker waits forever.
		if m.state != statePick {
			return m, nil
		}
		next, cmd := m.picker.Update(msg)
		m.picker = next
		return m, cmd

	case pathpicker.ChosenMsg:
		if m.state != stateFolder {
			return m, nil
		}
		return m.pickFolder(msg.Path)
	case pathpicker.CancelledMsg:
		if m.state == stateFolder {
			// Back to the card as it was: same folder, same ticket.
			m.state = stateCheck
		}
		return m, nil
	case pathpicker.BrowsedMsg:
		// The folder dialog's result, handed back down like the file one's.
		if m.state != stateFolder {
			return m, nil
		}
		next, cmd := m.folder.Update(msg)
		m.folder = next
		return m, cmd
	case folderMsg:
		return m.onFolder(msg)

	case preparedMsg:
		return m.onPrepared(msg, ctx)
	case toolMsg:
		return m.onTool(msg)
	case checkedMsg:
		return m.onChecked(msg)
	case menu.SelectedMsg:
		return m.onMenu(msg.ID)
	case confirm.AnsweredMsg:
		return m.onAnswer(msg)

	case startedMsg:
		return m.onStarted(msg)
	case progressMsg:
		return m.onProgress(msg)
	case outcomeMsg:
		return m.onOutcome(msg)
	case stepDoneMsg:
		return m.onStepDone(msg)
	case restoredMsg:
		return m.onRestored(msg)

	case tea.KeyPressMsg:
		return m.onKey(msg)
	case tea.PasteMsg:
		return m.onPaste(msg)
	case tea.MouseClickMsg, tea.MouseWheelMsg, tea.MouseMotionMsg:
		return m.onMouse(msg, ctx)
	}
	return m, nil
}

// onMouse routes the mouse to whichever list is on screen. Every other step
// is a card or a run that a stray click must not disturb.
func (m Model) onMouse(msg tea.Msg, ctx uictx.Context) (Model, tea.Cmd) {
	if m.details {
		return m, nil
	}
	switch m.state {
	case statePick:
		next, cmd, _ := m.picker.Pointer(ctx, msg, 0)
		m.picker = next
		return m, cmd
	case stateFolder:
		// The list sits under the question, however many lines that folds to.
		pick, _ := m.folderPrompts(ctx)
		if next, cmd, handled := m.folderPicker(ctx).Pointer(ctx, msg, strings.Count(pick, "\n")); handled {
			m.folder, m.folderErr = next, ""
			return m, cmd
		}
		return m, nil
	case stateTool, stateUnfinished:
		top := m.menuTop(ctx)
		if top < 0 {
			return m, nil
		}
		if next, cmd, handled := m.menu.Pointer(ctx, msg, top); handled {
			m.menu = next
			return m, cmd
		}
		next, cmd := m.menu.Update(msg)
		m.menu = next
		return m, cmd
	}
	return m, nil
}

// onPaste puts pasted text into the field that has the keyboard: the typed
// path or the password. Anywhere else a paste does nothing, so a clipboard
// full of text can never be read as keys.
func (m Model) onPaste(msg tea.PasteMsg) (Model, tea.Cmd) {
	if m.details {
		return m, nil
	}
	var cmd tea.Cmd
	switch m.state {
	case statePick:
		m.picker, cmd = m.picker.Update(msg)
	case statePassword:
		m.pass, cmd = m.pass.Update(msg)
		m.wrongPass = false
	}
	return m, cmd
}

// isEcho reports that a key press is a duplicate of the last one, delivered
// within the debounce window. A character going into a text field is never
// one: a pasted path arrives as fast as an echo does, and dropping the second
// p of "AppData" turns it into a path that is not there.
func (m Model) isEcho(km tea.KeyPressMsg) bool {
	if m.lastKey == "" || m.lastKey != km.String() {
		return false
	}
	if km.Text != "" && m.typing() {
		return false
	}
	return m.now().Sub(m.lastKeyAt) < keyDebounce
}

// typing reports that a text field has the keyboard: the typed archive path,
// the typed folder or the password.
func (m Model) typing() bool {
	if m.details {
		return false
	}
	switch m.state {
	case statePick:
		return m.picker.Typing()
	case stateFolder:
		return m.folder.Typing()
	case statePassword:
		return true
	}
	return false
}

// records is the engine for what needs no hatchzip: Devpit's own record of
// unfinished extractions.
func (m Model) records() Extractor {
	if m.eng != nil {
		return m.eng
	}
	return extractengine.New(extractengine.Tool{}, extractengine.Real())
}

// spin starts the spinner ticking, unless it already is.
func (m *Model) spin() tea.Cmd {
	if m.spinning || m.tick == nil {
		return nil
	}
	m.spinning = true
	return m.spinCmd()
}

// spinCmd schedules the next spinner frame.
func (m Model) spinCmd() tea.Cmd {
	return m.tick(spinEvery, func(time.Time) tea.Msg { return spinMsg{} })
}

// waits reports a state that shows a spinner.
func (m Model) waits() bool {
	switch m.state {
	case stateChecking, stateInstalling, stateRunning, stateRestoring:
		return true
	}
	return false
}

// onSpin advances the spinner and folds in whatever a running install step
// or restore has said since the last frame.
func (m Model) onSpin() (Model, tea.Cmd) {
	if !m.waits() {
		m.spinning = false
		return m, nil
	}
	m.frame++
	switch m.state {
	case stateInstalling:
		m = m.drainInstall()
	case stateRestoring:
		m = m.drainRestore()
	}
	return m, m.spinCmd()
}

// bind builds a key hint.
func bind(k, h string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, h)) }
