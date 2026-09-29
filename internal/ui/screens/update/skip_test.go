package update

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/activity"
)

// clock is a settable time source for WithClock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// startRun confirms the pick list and returns the model with the run going.
func startRun(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = update(t, m, keyPress(tea.KeyEnter, ""))
	m, cmd := update(t, m, keyPress('y', "y"))
	m, _ = update(t, m, cmd())
	if m.state != stateRunning {
		t.Fatalf("state = %v, want stateRunning", m.state)
	}
	return m
}

// pump feeds the run's events to the model, as the tick does, until done
// says to stop.
func pump(t *testing.T, m Model, done func(Model) bool) Model {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done(m) {
		if time.Now().After(deadline) {
			t.Fatalf("condition never met; rows: %+v", m.jobs)
		}
		time.Sleep(2 * time.Millisecond)
		evs, closed := drainEvents(m.events)
		m, _ = update(t, m, runBatchMsg{events: evs, closed: closed})
	}
	return m
}

func rowState(want activity.State, label string) func(Model) bool {
	return func(m Model) bool { return rowFor(m, label).State == want }
}

// stuckRun wraps a fakeRunner so an upgrade of app blocks until its context
// is cancelled, then reports -1 the way tools.RunStepLines does for a killed
// process.
func stuckRun(base *fakeRunner, app string, seen chan<- context.Context) RunStepFunc {
	return func(ctx context.Context, argv []string, timeout time.Duration, onLine func(tools.Line)) tools.StepResult {
		if strings.Contains(strings.Join(argv, " "), app) {
			base.mu.Lock()
			base.calls = append(base.calls, append([]string(nil), argv...))
			base.mu.Unlock()
			if seen != nil {
				seen <- ctx
			}
			<-ctx.Done()
			return tools.StepResult{ExitCode: -1}
		}
		return base.run(ctx, argv, timeout, onLine)
	}
}

// The first press of s only arms the gate. The second stops that app, and
// only that app: the next one still runs and finishes, and the summary says
// how to retry the skipped one.
func TestSkipNeedsTwoPressesAndStopsOnlyThatApp(t *testing.T) {
	r := newRunner()
	seen := make(chan context.Context, 1)
	clk := newClock()
	m := reachList(t, New(
		WithDetectFunc(fakeDetectFn("winget")),
		WithRunStepFunc(stuckRun(r, "Docker.DockerDesktop", seen)),
		WithClock(clk.now),
	))
	m = startRun(t, m)
	m = pump(t, m, rowState(activity.Running, "Docker Desktop"))
	jobCtx := <-seen

	m, cmd := update(t, m, keyPress('s', "s"))
	if cmd == nil {
		t.Error("the first press gave no hint")
	}
	if jobCtx.Err() != nil {
		t.Fatal("one press of s already stopped the app")
	}
	if help := m.ShortHelp(); help[0].Help().Desc != "press s again to skip" {
		t.Errorf("hint after the first press = %q", help[0].Help().Desc)
	}

	m, _ = update(t, m, keyPress('s', "s"))
	m = pump(t, m, func(m Model) bool { return m.state == stateSummary })

	row := rowFor(m, "Docker Desktop")
	if row.State != activity.Skipped || row.Detail != "skipped by you" {
		t.Fatalf("Docker Desktop row = %+v", row)
	}
	if !strings.Contains(row.Next, "winget upgrade --id Docker.DockerDesktop") {
		t.Errorf("no retry command on the skipped row: %q", row.Next)
	}
	if got := rowFor(m, "FFmpeg"); got.State != activity.Done {
		t.Errorf("FFmpeg row = %+v, want it to have run after the skip", got)
	}
	out := view(m)
	for _, want := range []string{"skipped by you", "What to do next", "Skipped 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
}

// The gate closes after its window, and any other key disarms it.
func TestSkipGateExpiresAndOtherKeysDisarm(t *testing.T) {
	r := newRunner()
	seen := make(chan context.Context, 1)
	clk := newClock()
	m := reachList(t, New(
		WithDetectFunc(fakeDetectFn("winget")),
		WithRunStepFunc(stuckRun(r, "Docker.DockerDesktop", seen)),
		WithClock(clk.now),
	))
	m = startRun(t, m)
	m = pump(t, m, rowState(activity.Running, "Docker Desktop"))
	jobCtx := <-seen

	m, _ = update(t, m, keyPress('s', "s"))
	clk.advance(activity.SkipWindow + time.Second)
	m, _ = update(t, m, keyPress('s', "s")) // arms again, does not skip
	m, _ = update(t, m, keyPress('l', "l"))
	m, _ = update(t, m, keyPress('s', "s")) // arms again after l, does not skip
	if jobCtx.Err() != nil {
		t.Fatal("the app was skipped without two presses in a row")
	}
	m.Stop()
	_ = pump(t, m, func(m Model) bool { return m.state == stateSummary })
}

// Skip is on the footer and in the ? help.
func TestSkipKeyIsInTheFooterAndHelp(t *testing.T) {
	m := New()
	m.state = stateRunning
	found := false
	for _, b := range m.ShortHelp() {
		if b.Help().Key == "s" {
			found = true
		}
	}
	if !found {
		t.Error("footer lacks the skip key")
	}
	found = false
	for _, g := range m.FullHelp() {
		for _, b := range g {
			if b.Help().Key == "s" {
				found = true
			}
		}
	}
	if !found {
		t.Error("? help lacks the skip key")
	}
}

// The elevated worker's own command is what a skip cancels: the job's
// context, not the run's, reaches Exec, and the worker stays up.
type blockingElevated struct {
	mu     sync.Mutex
	closed bool
	seen   chan context.Context
}

func (b *blockingElevated) Exec(ctx context.Context, argv []string, _ time.Duration, _ func(stream, text string)) (int, error) {
	b.seen <- ctx
	<-ctx.Done()
	return 0, ctx.Err()
}

func (b *blockingElevated) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return nil
}

func TestSkipCancelsOnlyTheElevatedCommand(t *testing.T) {
	r := newRunner()
	client := &blockingElevated{seen: make(chan context.Context, 1)}
	m := reachList(t, New(
		WithDetectFunc(fakeDetectFn("choco")),
		WithRunStepFunc(r.run),
		WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) { return client, nil }),
		WithClock(newClock().now),
	))
	m = startRun(t, m)
	m = pump(t, m, rowState(activity.Running, "git"))
	jobCtx := <-client.seen

	m, _ = update(t, m, keyPress('s', "s"))
	m, _ = update(t, m, keyPress('s', "s"))
	select {
	case <-jobCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the elevated command's context was not cancelled")
	}
	m = pump(t, m, func(m Model) bool { return m.state == stateSummary })
	row := rowFor(m, "git")
	if row.Detail != "skipped by you" || !strings.Contains(row.Next, "choco upgrade git -y") || !strings.Contains(row.Next, "administrator") {
		t.Errorf("row = %+v", row)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if !client.closed {
		t.Error("the worker was left open after the run")
	}
}

// A crash status is plain words with a next step, in both spellings.
func TestCrashExitCodeReadsAsCrashed(t *testing.T) {
	for _, exit := range []int{3221226505, hresult(0xC0000409)} {
		r := newRunner()
		r.upgrade = func(argv []string, _ func(tools.Line)) tools.StepResult {
			if strings.Contains(strings.Join(argv, " "), "Docker.DockerDesktop") {
				return tools.StepResult{ExitCode: exit}
			}
			return tools.StepResult{OK: true}
		}
		m := reachList(t, New(WithDetectFunc(fakeDetectFn("winget")), WithRunStepFunc(r.run)))
		m = runToSummary(t, m)
		row := rowFor(m, "Docker Desktop")
		if row.State != activity.Failed || row.Detail != "crashed (0xC0000409)" || row.Next == "" {
			t.Errorf("exit %d: row = %+v", exit, row)
		}
		if out := view(m); !strings.Contains(out, "crashed (0xC0000409)") || !strings.Contains(out, "What to do next") {
			t.Errorf("exit %d: summary:\n%s", exit, out)
		}
	}
}

// An app that says it is in use names itself in the message.
func TestInUseNamesTheApp(t *testing.T) {
	r := newRunner()
	r.upgrade = func(argv []string, _ func(tools.Line)) tools.StepResult {
		if strings.Contains(strings.Join(argv, " "), "Docker.DockerDesktop") {
			return tools.StepResult{ExitCode: hresult(0x80073D02)}
		}
		return tools.StepResult{OK: true}
	}
	m := reachList(t, New(WithDetectFunc(fakeDetectFn("winget")), WithRunStepFunc(r.run)))
	m = runToSummary(t, m)
	if row := rowFor(m, "Docker Desktop"); row.Detail != "in use, close Docker Desktop and retry" {
		t.Errorf("row = %+v", row)
	}
}

// adminRunner fails Docker Desktop with the packaged-service error until it
// is run elevated.
func adminRunner() *fakeRunner {
	r := newRunner()
	r.upgrade = func(argv []string, _ func(tools.Line)) tools.StepResult {
		if strings.Contains(strings.Join(argv, " "), "Docker.DockerDesktop") {
			return tools.StepResult{ExitCode: hresult(0x80073D28)}
		}
		return tools.StepResult{OK: true}
	}
	return r
}

// collect drains a finished run's events.
func collect(m Model) []runEvent {
	var evs []runEvent
	for ev := range m.events {
		evs = append(evs, ev)
	}
	return evs
}

// An app that needs admin rights waits, and is retried once at the end
// through one elevated worker, together with any others like it.
func TestNeedsAdminIsRetriedOnceAtTheEnd(t *testing.T) {
	r := adminRunner()
	client := &fakeElevatedClient{}
	launches := 0
	m := reachList(t, New(
		WithDetectFunc(fakeDetectFn("winget")),
		WithRunStepFunc(r.run),
		WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) {
			launches++
			return client, nil
		}),
	))
	m, _ = update(t, m, keyPress(tea.KeyEnter, ""))
	if !strings.Contains(m.confirm.View(testCtx()), "needs admin rights is tried again at the end") {
		t.Error("the confirmation does not say what happens to apps that need admin rights")
	}
	m, cmd := update(t, m, keyPress('y', "y"))
	m, _ = update(t, m, cmd())
	evs := collect(m)

	var deferred, retrying bool
	for _, ev := range evs {
		if ev.deferred {
			deferred = true
			if !strings.Contains(ev.detail, "needs admin rights") {
				t.Errorf("deferred event = %+v", ev)
			}
		}
		if ev.started && ev.detail == "retrying as administrator" {
			retrying = true
		}
	}
	if !deferred || !retrying {
		t.Errorf("deferred=%v retrying=%v; the row should wait, then show retrying", deferred, retrying)
	}
	m, _ = update(t, m, runBatchMsg{events: evs, closed: true})

	if launches != 1 || len(client.execArgv) != 1 || !client.closed {
		t.Fatalf("launches=%d execs=%v closed=%v, want one worker and one retried command", launches, client.execArgv, client.closed)
	}
	if got := strings.Join(client.execArgv[0], " "); !strings.Contains(got, "winget upgrade --id Docker.DockerDesktop") {
		t.Errorf("retried %q", got)
	}
	if row := rowFor(m, "Docker Desktop"); row.State != activity.Done {
		t.Errorf("row = %+v, want updated after the admin retry", row)
	}
	if got := len(r.commands("winget", "upgrade", "--id", "Docker.DockerDesktop")); got != 1 {
		t.Errorf("the unelevated command ran %d times, want once", got)
	}
}

// When the admin retry fails again, or the prompt is declined, the row says
// so and how to do it by hand.
func TestAdminRetryFailureAndDecline(t *testing.T) {
	t.Run("fails again", func(t *testing.T) {
		client := &fakeElevatedClient{code: hresult(0x80073D28)}
		m := reachList(t, New(
			WithDetectFunc(fakeDetectFn("winget")), WithRunStepFunc(adminRunner().run),
			WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) { return client, nil }),
		))
		m = runToSummary(t, m)
		row := rowFor(m, "Docker Desktop")
		if row.State != activity.Failed || row.Detail != "needs admin rights" || !strings.Contains(row.Next, "as administrator") {
			t.Errorf("row = %+v", row)
		}
	})
	t.Run("declined", func(t *testing.T) {
		launches := 0
		m := reachList(t, New(
			WithDetectFunc(fakeDetectFn("winget")), WithRunStepFunc(adminRunner().run),
			WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) {
				launches++
				return nil, &elevate.DeclinedError{}
			}),
		))
		m = runToSummary(t, m)
		row := rowFor(m, "Docker Desktop")
		if row.State != activity.Skipped || !strings.Contains(row.Detail, "needs admin") || !strings.Contains(row.Next, "winget upgrade --id Docker.DockerDesktop") {
			t.Errorf("row = %+v", row)
		}
		if launches != 1 {
			t.Errorf("the prompt was shown %d times, want once", launches)
		}
	})
}

// Stopping the run before the retry cancels the waiting apps; no admin
// prompt appears.
func TestStopBeforeTheAdminRetryAsksForNoPrompt(t *testing.T) {
	r := adminRunner()
	gate := make(chan struct{})
	inner := r.upgrade
	r.upgrade = func(argv []string, onLine func(tools.Line)) tools.StepResult {
		if strings.Contains(strings.Join(argv, " "), "Gyan.FFmpeg") {
			<-gate
		}
		return inner(argv, onLine)
	}
	launches := 0
	m := reachList(t, New(
		WithDetectFunc(fakeDetectFn("winget")), WithRunStepFunc(r.run),
		WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) {
			launches++
			return &fakeElevatedClient{}, nil
		}),
	))
	m = startRun(t, m)
	m = pump(t, m, rowState(activity.Running, "FFmpeg"))
	m.Stop()
	close(gate)
	m = pump(t, m, func(m Model) bool { return m.state == stateSummary })
	if launches != 0 {
		t.Errorf("the admin prompt was shown %d times after Stop", launches)
	}
	if row := rowFor(m, "Docker Desktop"); row.State != activity.Skipped {
		t.Errorf("Docker Desktop = %+v, want cancelled", row)
	}
}

// A silent app gets a hint to skip it, and the hint goes when it speaks.
func TestStuckAppGetsAHintToSkip(t *testing.T) {
	r := newRunner()
	seen := make(chan context.Context, 1)
	clk := newClock()
	m := reachList(t, New(
		WithDetectFunc(fakeDetectFn("winget")),
		WithRunStepFunc(stuckRun(r, "Docker.DockerDesktop", seen)),
		WithClock(clk.now),
	))
	m = startRun(t, m)
	m = pump(t, m, rowState(activity.Running, "Docker Desktop"))
	<-seen

	clk.advance(activity.StuckAfter - time.Second)
	m, _ = update(t, m, runBatchMsg{})
	if rowFor(m, "Docker Desktop").Stuck != 0 || strings.Contains(view(m), "press s to skip") {
		t.Fatal("hinted before the silence was long enough")
	}
	clk.advance(2 * time.Second)
	m, _ = update(t, m, runBatchMsg{})
	if rowFor(m, "Docker Desktop").Stuck < activity.StuckAfter {
		t.Fatalf("row = %+v, want it marked stuck", rowFor(m, "Docker Desktop"))
	}
	if out := view(m); !strings.Contains(out, "no output for 3m") || !strings.Contains(out, "press s to skip") {
		t.Errorf("no hint in the view:\n%s", out)
	}

	// Output clears it.
	m, _ = update(t, m, runBatchMsg{events: []runEvent{{job: 0, line: "Downloading"}}})
	if rowFor(m, "Docker Desktop").Stuck != 0 {
		t.Error("the hint stayed after new output")
	}
	m.Stop()
	pump(t, m, func(m Model) bool { return m.state == stateSummary })
}
