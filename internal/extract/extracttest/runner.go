package extracttest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/extract"
)

// Step is one moment of a scripted hatchzip run.
type Step struct {
	// At is when the step happens, on the fake clock, after the run started.
	At time.Duration
	// Line is printed when non-empty, to stderr when Stderr is set.
	Line   string
	Stderr bool
	// Do changes the fake file system (grow a part file, rename it …).
	Do func(fs *FakeFS)
}

// Script is one scripted hatchzip run.
type Script struct {
	// Stdout and Stderr are printed at the start, one line per line.
	Stdout string
	Stderr string
	// Steps play in order.
	Steps []Step
	// Exit is the exit code once the steps have played.
	Exit int
	// Hang keeps the run going after the steps until it is interrupted or
	// killed (a long entry).
	Hang bool
	// IgnoreBreaks is how many Ctrl+Breaks are ignored before the run exits
	// with BreakExit. A run ignoring 2 can only end by Kill.
	IgnoreBreaks int
	// BreakExit is the exit code after an obeyed Ctrl+Break (0 means 130).
	BreakExit int
}

// FakeRunner plays scripted runs: lines, exit code, and FS effects over time.
// Each Start takes the next script; a Start with none left fails.
type FakeRunner struct {
	mu         sync.Mutex
	scripts    []Script
	specs      []extract.Spec
	fs         *FakeFS
	clock      *Clock
	interrupts int
	kills      int
}

// ErrNoScript is a Start after every script has been used.
var ErrNoScript = errors.New("extracttest: no script left for this run")

// NewFakeRunner plays steps in order, one script per Start.
func NewFakeRunner(steps ...Script) *FakeRunner { return &FakeRunner{scripts: steps} }

// Bind gives the runner the file system its steps change and the clock their
// times are on. Without a clock every step plays at once.
func (f *FakeRunner) Bind(fsys *FakeFS, clock *Clock) *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fs, f.clock = fsys, clock
	return f
}

// Add queues more scripts.
func (f *FakeRunner) Add(steps ...Script) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts = append(f.scripts, steps...)
}

// Specs is every Spec it was started with.
func (f *FakeRunner) Specs() []extract.Spec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]extract.Spec(nil), f.specs...)
}

// Interrupts is how many Ctrl+Breaks were sent, over every run.
func (f *FakeRunner) Interrupts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.interrupts
}

// Kills is how many runs were killed.
func (f *FakeRunner) Kills() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.kills
}

// Start implements extract.Runner.
func (f *FakeRunner) Start(spec extract.Spec) (extract.Process, error) {
	f.mu.Lock()
	spec.Args = append([]string(nil), spec.Args...)
	f.specs = append(f.specs, spec)
	if len(f.scripts) == 0 {
		f.mu.Unlock()
		return nil, ErrNoScript
	}
	s := f.scripts[0]
	f.scripts = f.scripts[1:]
	fsys, clock := f.fs, f.clock
	f.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	p := &fakeProc{
		runner: f, s: s, fs: fsys, clock: clock,
		ctx: ctx, cancel: cancel,
		lines: make(chan extract.Line, 1024),
		done:  make(chan struct{}),
	}
	if clock != nil {
		p.start = clock.Now()
	}
	go p.play()
	return p, nil
}

type fakeProc struct {
	runner *FakeRunner
	s      Script
	fs     *FakeFS
	clock  *Clock
	start  time.Time
	ctx    context.Context
	cancel context.CancelFunc
	lines  chan extract.Line
	done   chan struct{}

	mu     sync.Mutex
	breaks int
	killed bool
	exit   int
}

func (p *fakeProc) say(text string, stderr bool) {
	for _, ln := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if ln != "" {
			p.lines <- extract.Line{Text: ln, Stderr: stderr}
		}
	}
}

func (p *fakeProc) play() {
	defer p.cancel()
	p.say(p.s.Stdout, false)
	p.say(p.s.Stderr, true)
	for _, st := range p.s.Steps {
		if !p.waitUntil(st.At) {
			break
		}
		if st.Line != "" {
			p.lines <- extract.Line{Text: st.Line, Stderr: st.Stderr}
		}
		if st.Do != nil && p.fs != nil {
			st.Do(p.fs)
		}
	}
	if p.s.Hang {
		<-p.ctx.Done()
	}
	p.mu.Lock()
	switch {
	case p.killed:
		p.exit = 1 // what Go reports for a TerminateJobObject kill
	case p.breaks > p.s.IgnoreBreaks:
		p.exit = p.s.BreakExit
		if p.exit == 0 {
			p.exit = 130
		}
	default:
		p.exit = p.s.Exit
	}
	p.mu.Unlock()
	close(p.lines)
	close(p.done)
}

// waitUntil sleeps until start+at on the fake clock; false when the run was
// interrupted (obeyed) or killed meanwhile.
func (p *fakeProc) waitUntil(at time.Duration) bool {
	if p.ctx.Err() != nil {
		return false
	}
	if p.clock != nil {
		p.clock.Sleep(p.ctx, p.start.Add(at).Sub(p.clock.Now()))
	}
	return p.ctx.Err() == nil
}

func (p *fakeProc) Lines() <-chan extract.Line { return p.lines }

func (p *fakeProc) Interrupt() error {
	p.runner.mu.Lock()
	p.runner.interrupts++
	p.runner.mu.Unlock()
	p.mu.Lock()
	p.breaks++
	obey := p.breaks > p.s.IgnoreBreaks
	p.mu.Unlock()
	if obey {
		p.cancel()
	}
	return nil
}

func (p *fakeProc) Kill() {
	p.runner.mu.Lock()
	p.runner.kills++
	p.runner.mu.Unlock()
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	p.cancel()
}

func (p *fakeProc) Wait() (int, error) {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exit, nil
}
