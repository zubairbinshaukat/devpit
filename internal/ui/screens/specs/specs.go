// Package specs is Settings › PC specs: everything about this PC on one
// scrolling page, with a Buyer's check at the top and a report that can be
// copied or saved.
//
// The screen only reads. Each section is read by its own command on its
// own goroutine, under the screen's context and a time limit
// ([sysinfo.Bounded]), so a stuck call never holds the page; Stop (Esc)
// cancels them all. Every message carries the read's generation, and a
// message from an older read is dropped. Nothing is cached or saved without
// a key press, and a save never overwrites a file (docs/safety.md, This PC).
package specs

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/gitssh"
	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/version"
)

// Options are everything the screen reaches outside itself. A nil field
// keeps the real one.
type Options struct {
	// Source makes a reader of this PC; each read makes a new one.
	Source func() sysinfo.Source
	// Elevated reads the drives' counters as admin (one UAC prompt).
	Elevated func(ctx context.Context) ([]sysinfo.Reliability, error)
	// Copy puts text on the clipboard.
	Copy func(text string) tea.Cmd
	// Save writes a report and returns the full path written.
	Save func(name string, b []byte) (string, error)
	// Now is the clock.
	Now func() time.Time
	// Tick schedules the spinner.
	Tick func(time.Duration, func(time.Time) tea.Msg) tea.Cmd
	// Version is Devpit's version, for the report.
	Version string
}

// withDefaults fills the nil fields with the real ones.
func (o Options) withDefaults() Options {
	if o.Source == nil {
		o.Source = sysinfo.NewLive
	}
	if o.Elevated == nil {
		o.Elevated = elevatedRead
	}
	if o.Copy == nil {
		o.Copy = copyToClipboard
	}
	if o.Save == nil {
		o.Save = saveToDesktop
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Tick == nil {
		o.Tick = tea.Tick
	}
	if o.Version == "" {
		o.Version = version.Short()
	}
	return o
}

// relPhase is where the admin read of the drives' counters is.
type relPhase int

const (
	relIdle relPhase = iota
	relRunning
	relDone
	relDeclined
	relFailed
)

// exportKind is what a pending export does with the report.
type exportKind int

const (
	exportNone exportKind = iota
	exportCopy
	exportSave
)

// run is the current read's cancel handle, shared by every copy of the
// screen value so Stop reaches it.
type run struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	ctx    context.Context
}

func newRun() *run {
	ctx, cancel := context.WithCancel(context.Background())
	return &run{ctx: ctx, cancel: cancel}
}

func (r *run) stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	c := r.cancel
	r.mu.Unlock()
	if c != nil {
		c()
	}
}

// Model is the PC specs screen.
type Model struct {
	opts Options
	run  *run
	gen  int
	rep  sysinfo.Report
	read time.Time

	rel    relPhase
	relErr string

	scroll int
	spin   int

	asking  bool
	export  exportKind
	confirm confirm.Model

	keys keyMap
}

// keyMap is the screen's keys.
type keyMap struct {
	Up, Down, PgUp, PgDown, Home, End, Prev, Next, Refresh, Copy, Save, Elevate key.Binding
}

func newKeys() keyMap {
	return keyMap{
		Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑↓", "scroll")),
		Down:    key.NewBinding(key.WithKeys("down", "j")),
		PgUp:    key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup/pgdn", "page")),
		PgDown:  key.NewBinding(key.WithKeys("pgdown", " ")),
		Home:    key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("home/end", "top/bottom")),
		End:     key.NewBinding(key.WithKeys("end", "G")),
		Prev:    key.NewBinding(key.WithKeys("left", "h", "["), key.WithHelp("←→", "section")),
		Next:    key.NewBinding(key.WithKeys("right", "l", "]")),
		Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "read again")),
		Copy:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy")),
		Save:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save")),
		Elevate: key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "read wear as admin")),
	}
}

// New returns the screen. It reads nothing until it is pushed (Init).
func New(o Options) Model {
	m := Model{opts: o.withDefaults(), keys: newKeys()}
	m.run = newRun()
	m.rep = emptyReport()
	return m
}

// emptyReport is a report with nothing arrived yet.
func emptyReport() sysinfo.Report {
	return sysinfo.Report{Have: map[sysinfo.Section]bool{}, Errs: map[sysinfo.Section]string{}}
}

// Init implements uictx.Screen: one command per section.
func (m Model) Init() tea.Cmd { return m.startRead() }

// Title implements uictx.Screen.
func (m Model) Title() string { return "PC specs" }

// Busy implements uictx.BusyReporter. Reading never holds Esc: Esc leaves
// and Stop cancels.
func (m Model) Busy() bool { return false }

// Stop implements uictx.Stopper: cancel every read in flight. It does not
// wait.
func (m Model) Stop() { m.run.stop() }

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	if m.asking {
		return m.confirm.Keys.ShortHelp()
	}
	return []key.Binding{m.keys.Up, m.keys.Prev, m.keys.Copy, m.keys.Save}
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	k := m.keys
	return [][]key.Binding{{k.Up, k.PgUp, k.Home, k.Prev}, {k.Refresh, k.Copy, k.Save, k.Elevate}}
}

// sectionMsg is one section's answer.
type sectionMsg struct {
	gen   int
	sec   sysinfo.Section
	apply func(*sysinfo.Report)
	err   error
}

// relMsg is the admin read's answer.
type relMsg struct {
	gen int
	rs  []sysinfo.Reliability
	err error
}

// spinMsg advances the spinner.
type spinMsg struct{ gen int }

// savedMsg is a finished save.
type savedMsg struct {
	path string
	err  error
}

// spinEvery is how often the spinner moves.
const spinEvery = 120 * time.Millisecond

// startRead starts a read of every section under the current run.
func (m Model) startRead() tea.Cmd {
	src := m.opts.Source()
	ctx, gen := m.run.ctx, m.gen
	cmds := []tea.Cmd{
		read(ctx, gen, sysinfo.SecSystem, sysinfo.NativeLimit, src.System, func(r *sysinfo.Report, v sysinfo.System) { r.System = v }),
		read(ctx, gen, sysinfo.SecBattery, sysinfo.NativeLimit, src.Battery, func(r *sysinfo.Report, v sysinfo.Battery) { r.Battery = v }),
		read(ctx, gen, sysinfo.SecMemory, sysinfo.NativeLimit, src.Memory, func(r *sysinfo.Report, v sysinfo.Memory) { r.Memory = v }),
		read(ctx, gen, sysinfo.SecVolumes, sysinfo.NativeLimit, src.Volumes, func(r *sysinfo.Report, v []sysinfo.Volume) { r.Volumes = v }),
		read(ctx, gen, sysinfo.SecDisks, sysinfo.BatchSectionLimit, src.Disks, func(r *sysinfo.Report, v sysinfo.DiskInfo) { r.Disks = v }),
		read(ctx, gen, sysinfo.SecCPU, sysinfo.NativeLimit, src.CPU, func(r *sysinfo.Report, v sysinfo.CPU) { r.CPU = v }),
		read(ctx, gen, sysinfo.SecGraphics, sysinfo.NativeLimit, src.Graphics, func(r *sysinfo.Report, v []sysinfo.GPU) { r.GPUs = v }),
		read(ctx, gen, sysinfo.SecDisplays, sysinfo.NativeLimit, src.Displays, func(r *sysinfo.Report, v []sysinfo.Display) { r.Displays = v }),
		read(ctx, gen, sysinfo.SecWindows, sysinfo.BatchSectionLimit, src.Windows, func(r *sysinfo.Report, v sysinfo.Windows) { r.Windows = v }),
		read(ctx, gen, sysinfo.SecNetwork, sysinfo.NativeLimit, src.Network, func(r *sysinfo.Report, v []sysinfo.Adapter) { r.Network = v }),
		read(ctx, gen, sysinfo.SecDevices, sysinfo.NativeLimit, src.Devices, func(r *sysinfo.Report, v sysinfo.Devices) { r.Devices = v }),
		m.opts.Tick(spinEvery, func(time.Time) tea.Msg { return spinMsg{gen: gen} }),
	}
	return tea.Batch(cmds...)
}

// read is one section's command.
func read[T any](ctx context.Context, gen int, sec sysinfo.Section, limit time.Duration,
	f func(context.Context) (T, error), set func(*sysinfo.Report, T),
) tea.Cmd {
	return func() tea.Msg {
		v, err := sysinfo.Bounded(ctx, limit, f)
		return sectionMsg{gen: gen, sec: sec, err: err, apply: func(r *sysinfo.Report) { set(r, v) }}
	}
}

// reading reports whether any section is still out.
func (m Model) reading() bool {
	for _, s := range sysinfo.Sections() {
		if !m.rep.Have[s] {
			return true
		}
	}
	return false
}

// merge folds one answer into a copy of the report: the maps are copied so
// an older screen value never sees the change.
func (m Model) merge(msg sectionMsg) Model {
	r := m.rep
	have := make(map[sysinfo.Section]bool, len(r.Have)+1)
	for k, v := range r.Have {
		have[k] = v
	}
	errs := make(map[sysinfo.Section]string, len(r.Errs))
	for k, v := range r.Errs {
		errs[k] = v
	}
	have[msg.sec] = true
	if msg.err != nil {
		errs[msg.sec] = plainError(msg.err)
		if msg.sec == sysinfo.SecDisks || msg.sec == sysinfo.SecWindows {
			msg.apply(&r) // a note may come with the failure
		}
	} else {
		delete(errs, msg.sec)
		msg.apply(&r)
	}
	r.Have, r.Errs = have, errs
	m.rep = r
	if !m.reading() && m.read.IsZero() {
		m.read = m.opts.Now()
	}
	return m
}

// plainError is a failure in words.
func plainError(err error) string {
	switch {
	case errors.Is(err, sysinfo.ErrTimeout):
		return "Windows took too long to answer"
	case errors.Is(err, sysinfo.ErrUnsupported):
		return "this only works on Windows"
	case errors.Is(err, context.Canceled):
		return "stopped"
	}
	return err.Error()
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case sectionMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		return m.merge(msg), nil
	case spinMsg:
		if msg.gen != m.gen || !m.reading() && m.rel != relRunning {
			return m, nil
		}
		m.spin++
		if ctx.ReducedMotion {
			return m, nil
		}
		gen := m.gen
		return m, m.opts.Tick(spinEvery, func(time.Time) tea.Msg { return spinMsg{gen: gen} })
	case relMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		return m.applyRel(msg)
	case savedMsg:
		if msg.err != nil {
			return m, uictx.Status("danger", "Could not save the report: "+msg.err.Error())
		}
		return m, uictx.Status("success", "Saved to "+msg.path)
	case confirm.AnsweredMsg:
		if !m.asking {
			return m, nil
		}
		m.asking = false
		return m, m.doExport(msg.Answer == confirm.AnswerYes)
	case tea.KeyPressMsg:
		if m.asking {
			next, cmd := m.confirm.Update(msg)
			m.confirm = next
			return m, cmd
		}
		return m.onKey(msg, ctx)
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scroll -= 3
		case tea.MouseWheelDown:
			m.scroll += 3
		}
		return m.clamp(ctx), nil
	case tea.MouseClickMsg:
		return m.onClick(msg, ctx)
	}
	return m, nil
}

// onKey handles the keys while no question is up.
func (m Model) onKey(msg tea.KeyPressMsg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	k := m.keys
	page := max(1, m.viewHeight(ctx)-1)
	switch {
	case key.Matches(msg, k.Up):
		m.scroll--
	case key.Matches(msg, k.Down):
		m.scroll++
	case key.Matches(msg, k.PgUp):
		m.scroll -= page
	case key.Matches(msg, k.PgDown):
		m.scroll += page
	case key.Matches(msg, k.Home):
		m.scroll = 0
	case key.Matches(msg, k.End):
		m.scroll = 1 << 20
	case key.Matches(msg, k.Prev), key.Matches(msg, k.Next):
		dir := 1
		if key.Matches(msg, k.Prev) {
			dir = -1
		}
		m.scroll = m.jump(ctx, dir)
	case key.Matches(msg, k.Refresh):
		return m.refresh()
	case key.Matches(msg, k.Copy):
		return m.ask(exportCopy), nil
	case key.Matches(msg, k.Save):
		return m.ask(exportSave), nil
	case key.Matches(msg, k.Elevate):
		if m.canElevate() {
			return m.startRel()
		}
	}
	return m.clamp(ctx), nil
}

// refresh reads everything again under a new run; the old one is cancelled
// and its late answers are dropped by generation.
func (m Model) refresh() (uictx.Screen, tea.Cmd) {
	m.run.stop()
	m.run = newRun()
	m.gen++
	m.rep = emptyReport()
	m.read = time.Time{}
	m.rel, m.relErr = relIdle, ""
	return m, m.startRead()
}

// canElevate is whether [e] is on offer: drives are listed and at least one
// has no counters, and no admin read is running or done.
func (m Model) canElevate() bool {
	if !m.rep.Got(sysinfo.SecDisks) || m.rel == relRunning || m.rel == relDone {
		return false
	}
	for _, d := range m.rep.Disks.Disks {
		if d.Rel == nil {
			return true
		}
	}
	return false
}

// startRel runs the admin read.
func (m Model) startRel() (uictx.Screen, tea.Cmd) {
	m.rel, m.relErr = relRunning, ""
	f, ctx, gen := m.opts.Elevated, m.run.ctx, m.gen
	read := func() tea.Msg {
		rs, err := f(ctx)
		return relMsg{gen: gen, rs: rs, err: err}
	}
	return m, tea.Batch(read, m.opts.Tick(spinEvery, func(time.Time) tea.Msg { return spinMsg{gen: gen} }))
}

// applyRel merges the counters, or records why there are none. Everything
// else on the page stays as it is.
func (m Model) applyRel(msg relMsg) (uictx.Screen, tea.Cmd) {
	var declined *elevate.DeclinedError
	switch {
	case errors.As(msg.err, &declined):
		m.rel = relDeclined
		return m, uictx.Status("warning", "Skipped: needs admin. Everything else is shown.")
	case msg.err != nil:
		m.rel, m.relErr = relFailed, plainError(msg.err)
		return m, nil
	}
	m.rel = relDone
	m.rep.Disks = sysinfo.MergeReliability(m.rep.Disks, msg.rs)
	return m, nil
}

// ask puts up the serials question before an export. It starts on No.
func (m Model) ask(k exportKind) Model {
	m.export = k
	m.asking = true
	m.confirm = confirm.New("specs-export", "Include serial numbers and MAC addresses?",
		"Leave them out if you will post this publicly.")
	return m
}

// report is the Markdown of what is on the page.
func (m Model) report(serials bool) string {
	now := m.opts.Now()
	return sysinfo.Markdown(m.rep, sysinfo.MarkdownOptions{
		Serials: serials, Now: now, Version: m.opts.Version, Findings: sysinfo.Assess(m.rep, now),
	})
}

// doExport copies or saves the report.
func (m Model) doExport(serials bool) tea.Cmd {
	text := m.report(serials)
	switch m.export {
	case exportCopy:
		return m.opts.Copy(text)
	case exportSave:
		name := sysinfo.ReportFileName(m.rep.System.Title(), m.opts.Now())
		save := m.opts.Save
		return func() tea.Msg {
			path, err := save(name, []byte(text))
			return savedMsg{path: path, err: err}
		}
	}
	return nil
}

// copyToClipboard tries the Windows clipboard, then OSC 52.
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		if gitssh.CopyWin32(text) == nil {
			return uictx.StatusMsg{Text: "Copied the report to the clipboard", Level: "success"}
		}
		return tea.BatchMsg{tea.SetClipboard(text), uictx.Status("success", "Copied the report to the clipboard")}
	}
}

// saveToDesktop saves on the Desktop and never overwrites.
func saveToDesktop(name string, b []byte) (string, error) {
	dir, err := sysinfo.DesktopDir()
	if err != nil {
		return "", err
	}
	return sysinfo.SaveNew(dir, name, b)
}

// elevatedRead starts the admin worker (one UAC prompt), asks it for the
// drives' counters and shuts it down.
func elevatedRead(ctx context.Context) ([]sysinfo.Reliability, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	c, err := elevate.Launch(ctx, exe, elevate.Options{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	return c.Specs(ctx)
}
