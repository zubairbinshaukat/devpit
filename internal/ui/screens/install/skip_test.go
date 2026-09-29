package install

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/tools/catalog"
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

// twoAppRun starts a winget run of AppA and AppB.
func twoAppRun(t *testing.T, run RunStepFunc, clk *clock) Model {
	t.Helper()
	ctx := testCtx()
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "winget", Found: true}})),
		WithLookPathFunc(fakeLookPath(nil)),
		WithLoadCatalogFunc(func() ([]catalog.App, error) { return testCatalog(), nil }),
		WithRunStepFunc(run),
		WithClock(clk.now),
	)
	m = reachList(t, m, ctx)
	for _, name := range []string{"AppA", "AppB"} {
		m.cursor = rowIndex(m.rows, name)
		next, _ := m.Update(keyPress(' ', " "), ctx)
		m = next.(Model)
	}
	next, _ := m.Update(keyPress(13, ""), ctx)
	m = next.(Model)
	next, cmd := m.Update(keyPress('y', "y"), ctx)
	m = next.(Model)
	next, _ = m.Update(cmd(), ctx)
	m = next.(Model)
	if m.state != stateRunning {
		t.Fatalf("state = %v, want stateRunning", m.state)
	}
	return m
}

// pump feeds the run's events to the model until done says to stop.
func pump(t *testing.T, m Model, done func(Model) bool) Model {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done(m) {
		if time.Now().After(deadline) {
			t.Fatalf("condition never met; rows: %+v", m.jobs)
		}
		time.Sleep(2 * time.Millisecond)
		evs, closed := drainEvents(m.events)
		next, _ := m.Update(runBatchMsg{events: evs, closed: closed}, testCtx())
		m = next.(Model)
	}
	return m
}

func press(t *testing.T, m Model, code rune, text string) Model {
	t.Helper()
	next, _ := m.Update(keyPress(code, text), testCtx())
	return next.(Model)
}

// stuckOn makes the install of one package hang until its context ends, as
// a stuck installer would, telling the test which context it was given.
func stuckOn(id string, seen chan<- context.Context) RunStepFunc {
	return func(ctx context.Context, argv []string, _ time.Duration, onLine func(string)) tools.StepResult {
		if strings.Contains(strings.Join(argv, " "), id) {
			seen <- ctx
			<-ctx.Done()
			return tools.StepResult{ExitCode: -1}
		}
		onLine("installed " + id)
		return tools.StepResult{OK: true}
	}
}

// Two presses of s skip the app in flight and only that app; the next one
// installs, and the summary says how to install the skipped one later.
func TestSkipNeedsTwoPressesAndStopsOnlyThatApp(t *testing.T) {
	seen := make(chan context.Context, 1)
	m := twoAppRun(t, stuckOn("Pub.AppA", seen), newClock())
	jobCtx := <-seen

	m = press(t, m, 's', "s")
	if jobCtx.Err() != nil {
		t.Fatal("one press of s already stopped the app")
	}
	if help := m.ShortHelp(); help[0].Help().Desc != "press s again to skip" {
		t.Errorf("hint after the first press = %q", help[0].Help().Desc)
	}
	m = press(t, m, 's', "s")
	m = pump(t, m, func(m Model) bool { return m.state == stateSummary })

	a, b := m.jobs[0], m.jobs[1]
	if a.State != activity.Skipped || a.Detail != "skipped by you" || !strings.Contains(a.Next, "winget install --id Pub.AppA") {
		t.Errorf("AppA row = %+v", a)
	}
	if b.State != activity.Done {
		t.Errorf("AppB row = %+v, want it installed after the skip", b)
	}
	out := ansi.Strip(m.View(testCtx()))
	for _, want := range []string{"skipped by you", "What to do next", "Skipped 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
}

// Skip pressed as the install finished: it went through, so it counts as
// installed, and pressing s once the app is done stops nothing else.
func TestSkipRacingTheFinishKeepsTheResult(t *testing.T) {
	seen := make(chan context.Context, 1)
	run := func(ctx context.Context, argv []string, _ time.Duration, _ func(string)) tools.StepResult {
		if strings.Contains(strings.Join(argv, " "), "Pub.AppA") {
			seen <- ctx
			<-ctx.Done()
			return tools.StepResult{OK: true} // exited 0 on its own in the same instant
		}
		return tools.StepResult{OK: true}
	}
	m := twoAppRun(t, run, newClock())
	<-seen
	m = press(t, m, 's', "s")
	m = press(t, m, 's', "s")
	m = pump(t, m, func(m Model) bool { return m.state == stateSummary })
	if a := m.jobs[0]; a.State != activity.Done || a.Next != "" {
		t.Errorf("AppA row = %+v, want installed", a)
	}
	if b := m.jobs[1]; b.State != activity.Done {
		t.Errorf("AppB row = %+v, want installed", b)
	}
}

// Another key between the two presses, or waiting too long, starts over.
func TestSkipGateDisarmsOnOtherKeysAndExpires(t *testing.T) {
	seen := make(chan context.Context, 1)
	clk := newClock()
	m := twoAppRun(t, stuckOn("Pub.AppA", seen), clk)
	jobCtx := <-seen

	m = press(t, m, 's', "s")
	m = press(t, m, 'l', "l")
	m = press(t, m, 's', "s")
	clk.advance(activity.SkipWindow + time.Second)
	m = press(t, m, 's', "s")
	if jobCtx.Err() != nil {
		t.Fatal("the app was skipped without two presses in a row")
	}
	m.Stop()
	pump(t, m, func(m Model) bool { return m.state == stateSummary })
}

func TestSkipKeyIsInTheFooterAndHelp(t *testing.T) {
	m := New()
	m.state = stateRunning
	found := false
	for _, b := range m.ShortHelp() {
		found = found || b.Help().Key == "s"
	}
	if !found {
		t.Error("footer lacks the skip key")
	}
	found = false
	for _, g := range m.FullHelp() {
		for _, b := range g {
			found = found || b.Help().Key == "s"
		}
	}
	if !found {
		t.Error("? help lacks the skip key")
	}
}

// A silent installer gets the hint, and output clears it.
func TestStuckAppGetsAHintToSkip(t *testing.T) {
	seen := make(chan context.Context, 1)
	clk := newClock()
	m := twoAppRun(t, stuckOn("Pub.AppA", seen), clk)
	<-seen

	clk.advance(activity.StuckAfter + time.Second)
	next, _ := m.Update(runBatchMsg{}, testCtx())
	m = next.(Model)
	if out := ansi.Strip(m.View(testCtx())); !strings.Contains(out, "no output for 5m") || !strings.Contains(out, "press s twice to skip") {
		t.Errorf("no hint in the view:\n%s", out)
	}
	next, _ = m.Update(runBatchMsg{events: []runEvent{{line: "Downloading"}}}, testCtx())
	m = next.(Model)
	if m.jobs[0].Stuck != 0 {
		t.Error("the hint stayed after new output")
	}
	m.Stop()
	pump(t, m, func(m Model) bool { return m.state == stateSummary })
}

// A crash status and an admin-only app read in plain words, each with a next
// step; the install screen does not promise an automatic admin retry.
func TestInstallErrorsReadInPlainWords(t *testing.T) {
	cases := []struct {
		exit       int
		wantDetail string
		wantNext   string
	}{
		{3221226505, "crashed (0xC0000409)", "Try again"},
		{int(int32(-1073740791)), "crashed (0xC0000409)", "Try again"},
		{int(int32(-2147009240)), "needs admin rights", "as administrator and run: winget install --id Pub.AppA"},
	}
	for _, c := range cases {
		ctx := testCtx()
		var mu sync.Mutex
		var calls [][]string
		m := New(
			WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "winget", Found: true}})),
			WithLookPathFunc(fakeLookPath(nil)),
			WithLoadCatalogFunc(func() ([]catalog.App, error) { return testCatalog(), nil }),
			WithRunStepFunc(recordingRunStep(&mu, &calls, tools.StepResult{ExitCode: c.exit})),
		)
		m = reachList(t, m, ctx)
		m = runToSummary(t, m, ctx, rowIndex(m.rows, "AppA"))
		r := m.jobs[0]
		if r.State != activity.Failed || !strings.HasPrefix(r.Detail, c.wantDetail) || !strings.Contains(r.Next, c.wantNext) {
			t.Errorf("exit %d: row = %+v", c.exit, r)
		}
	}
}
