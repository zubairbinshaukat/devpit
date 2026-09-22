package update

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

func testCtx() uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      icons.Unicode(),
		Config:     config.Default(),
		Width:      100,
		Height:     30,
		BodyHeight: 26,
	}
}

func fakeDetectFn(list []tools.Tool) DetectFunc {
	return func(context.Context) []tools.Tool { return list }
}

func keyPress(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Text: text}
}

// reachList drives Init and the resulting detectResultMsg through Update so
// tests start from a populated stateList.
func reachList(t *testing.T, m Model, ctx uictx.Context) Model {
	t.Helper()
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned a nil command")
	}
	msg := cmd()
	next, _ := m.Update(msg, ctx)
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	if mm.state != stateList {
		t.Fatalf("state = %v, want stateList (errText=%q)", mm.state, mm.errText)
	}
	return mm
}

func stepIndex(steps []step, name string) int {
	for i, s := range steps {
		if s.manager.Name() == name {
			return i
		}
	}
	return -1
}

func TestStepsRunInManagerOrder(t *testing.T) {
	ctx := testCtx()
	// Detected out of order; buildSteps must still produce the fixed order.
	m := New(WithDetectFunc(fakeDetectFn([]tools.Tool{
		{Name: "choco", Found: true},
		{Name: "npm", Found: true},
		{Name: "winget", Found: true},
		{Name: "scoop", Found: true},
	})))
	m = reachList(t, m, ctx)

	want := []string{"winget", "scoop", "npm", "choco"}
	if len(m.steps) != len(want) {
		t.Fatalf("len(steps) = %d, want %d", len(m.steps), len(want))
	}
	for i, name := range want {
		if m.steps[i].manager.Name() != name {
			t.Errorf("steps[%d] = %q, want %q", i, m.steps[i].manager.Name(), name)
		}
	}
}

func TestUntickedStepIsSkipped(t *testing.T) {
	ctx := testCtx()
	var mu sync.Mutex
	var calls [][]string
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{
			{Name: "winget", Found: true},
			{Name: "scoop", Found: true},
		})),
		WithRunStepFunc(recordingRunStep(&mu, &calls, tools.StepResult{OK: true})),
	)
	m = reachList(t, m, ctx)

	// Untick scoop; only winget should run.
	idx := stepIndex(m.steps, "scoop")
	m.cursor = idx
	next, _ := m.Update(keyPress(' ', " "), ctx)
	m = next.(Model)
	if m.steps[idx].selected {
		t.Fatal("setup: scoop should now be unticked")
	}

	m = runToSummary(t, m, ctx)

	mu.Lock()
	defer mu.Unlock()
	for _, call := range calls {
		if len(call) > 0 && call[0] == "scoop" {
			t.Errorf("scoop ran even though it was unticked: %v", call)
		}
	}
	if len(m.results) != 1 || m.results[0].Name != "winget" {
		t.Fatalf("results = %+v, want exactly one winget result", m.results)
	}
}

// recordingRunStep returns a RunStepFunc that records every argv it is
// called with and always reports result.
func recordingRunStep(mu *sync.Mutex, calls *[][]string, result tools.StepResult) RunStepFunc {
	return func(_ context.Context, argv []string, _ time.Duration, onLine func(string)) tools.StepResult {
		mu.Lock()
		*calls = append(*calls, append([]string(nil), argv...))
		mu.Unlock()
		for _, l := range result.LastLines {
			if onLine != nil {
				onLine(l)
			}
		}
		return result
	}
}

// runToSummary answers Yes on the confirm dialog and drives the run through
// to stateSummary, collecting every streamed event synchronously.
func runToSummary(t *testing.T, m Model, ctx uictx.Context) Model {
	t.Helper()
	next, _ := m.Update(keyPress(13, ""), ctx)
	m = next.(Model)
	if m.state != stateConfirm {
		t.Fatalf("state = %v, want stateConfirm", m.state)
	}

	next, cmd := m.Update(keyPress('y', "y"), ctx)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("confirming yes produced no command")
	}
	ansMsg := cmd()

	next, _ = m.Update(ansMsg, ctx)
	m = next.(Model)
	if m.state != stateRunning {
		t.Fatalf("state = %v, want stateRunning", m.state)
	}

	var events []runEvent
	for ev := range m.events {
		events = append(events, ev)
	}

	next, _ = m.Update(runBatchMsg{events: events}, ctx)
	m = next.(Model)
	if m.state != stateSummary {
		t.Fatalf("state = %v, want stateSummary", m.state)
	}
	return m
}

type fakeElevatedClient struct {
	mu       sync.Mutex
	execArgv [][]string
	execFn   func(argv []string) (code int, lines []string, err error)
	closed   bool
}

func (f *fakeElevatedClient) Exec(_ context.Context, argv []string, _ time.Duration, onLine func(stream, text string)) (int, error) {
	f.mu.Lock()
	f.execArgv = append(f.execArgv, append([]string(nil), argv...))
	f.mu.Unlock()
	code, lines, err := f.execFn(argv)
	for _, l := range lines {
		if onLine != nil {
			onLine("stdout", l)
		}
	}
	return code, err
}

func (f *fakeElevatedClient) Close() error {
	f.closed = true
	return nil
}

func TestChocoGoesThroughElevatedWorker(t *testing.T) {
	ctx := testCtx()
	client := &fakeElevatedClient{execFn: func([]string) (int, []string, error) { return 0, nil, nil }}
	launched := false
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "choco", Found: true}})),
		WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) {
			launched = true
			return client, nil
		}),
	)
	m = reachList(t, m, ctx)

	m = runToSummary(t, m, ctx)

	if !launched {
		t.Fatal("the elevated worker was never launched for a choco step")
	}
	if len(client.execArgv) == 0 {
		t.Fatal("Exec was never called on the elevated client")
	}
	want := m.steps[0].manager.UpgradeAllCmds()
	if len(client.execArgv) != len(want) {
		t.Fatalf("Exec called %d times, want %d", len(client.execArgv), len(want))
	}
	if !client.closed {
		t.Error("the elevated client was never closed")
	}
	if len(m.results) != 1 || !m.results[0].OK {
		t.Fatalf("results = %+v, want one OK choco result", m.results)
	}
}

func TestDeclinedUACMarksStepSkipped(t *testing.T) {
	ctx := testCtx()
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "choco", Found: true}})),
		WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) {
			return nil, &elevate.DeclinedError{}
		}),
	)
	m = reachList(t, m, ctx)

	m = runToSummary(t, m, ctx)

	if len(m.results) != 1 {
		t.Fatalf("results = %+v, want one result", m.results)
	}
	r := m.results[0]
	if !r.Skipped {
		t.Error("a declined UAC prompt must mark the step skipped, not failed")
	}
	if r.SkipReason == "" {
		t.Error("a skipped step must explain why")
	}
}

func TestWorkerDiedMarksStepFailed(t *testing.T) {
	ctx := testCtx()
	client := &fakeElevatedClient{execFn: func([]string) (int, []string, error) {
		return 0, nil, &elevate.WorkerDiedError{LastLines: []string{"choco: installing", "pipe broke"}}
	}}
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "choco", Found: true}})),
		WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) { return client, nil }),
	)
	m = reachList(t, m, ctx)

	m = runToSummary(t, m, ctx)

	if len(m.results) != 1 {
		t.Fatalf("results = %+v, want one result", m.results)
	}
	r := m.results[0]
	if r.OK || r.Skipped {
		t.Fatalf("a worker death must mark the step failed, got %+v", r)
	}
	if len(r.LastLines) != 2 || r.LastLines[1] != "pipe broke" {
		t.Errorf("LastLines = %v, want the worker's last lines", r.LastLines)
	}
}

// sanity check that errors.As unwrapping in production code is exercised
// with a plain (non-typed) error too, so a launch failure that is neither
// DeclinedError nor WorkerDiedError still surfaces as a normal failure
// rather than panicking.
func TestElevatedLaunchOtherErrorMarksStepFailed(t *testing.T) {
	ctx := testCtx()
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "choco", Found: true}})),
		WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) {
			return nil, errors.New("boom")
		}),
	)
	m = reachList(t, m, ctx)

	m = runToSummary(t, m, ctx)

	if len(m.results) != 1 || m.results[0].OK || m.results[0].Skipped {
		t.Fatalf("results = %+v, want one plain failure", m.results)
	}
}

// startRun answers Yes on the confirm dialog and drives the run to
// stateRunning without waiting for it to finish.
func startRun(t *testing.T, m Model, ctx uictx.Context) Model {
	t.Helper()
	next, _ := m.Update(keyPress(13, ""), ctx)
	m = next.(Model)
	if m.state != stateConfirm {
		t.Fatalf("state = %v, want stateConfirm", m.state)
	}

	next, cmd := m.Update(keyPress('y', "y"), ctx)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("confirming yes produced no command")
	}
	ansMsg := cmd()

	next, _ = m.Update(ansMsg, ctx)
	m = next.(Model)
	if m.state != stateRunning {
		t.Fatalf("state = %v, want stateRunning", m.state)
	}
	return m
}

// TestBusyIsTrueOnlyWhileRunning holds down uictx.BusyReporter's contract:
// only the running state may claim Esc away from "back" or make Ctrl+C wait.
func TestBusyIsTrueOnlyWhileRunning(t *testing.T) {
	ctx := testCtx()
	var mu sync.Mutex
	var calls [][]string
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "winget", Found: true}})),
		WithRunStepFunc(recordingRunStep(&mu, &calls, tools.StepResult{OK: true})),
	)
	if m.Busy() {
		t.Error("a freshly built screen must not be busy")
	}

	m = reachList(t, m, ctx)
	if m.Busy() {
		t.Error("stateList must not be busy")
	}

	m = startRun(t, m, ctx)
	if !m.Busy() {
		t.Error("stateRunning must be busy")
	}

	var events []runEvent
	for ev := range m.events {
		events = append(events, ev)
	}
	next, _ := m.Update(runBatchMsg{events: events}, ctx)
	m = next.(Model)
	if m.state != stateSummary {
		t.Fatalf("state = %v, want stateSummary", m.state)
	}
	if m.Busy() {
		t.Error("stateSummary must not be busy")
	}
}

// TestStopCancelsTheRunAndUnblocksTheGoroutine is the regression test for
// finding 2: update ran against context.Background(), so Esc or Ctrl+C
// during a run left the goroutine (and its child process) running orphaned
// and could block it forever on an unread channel. runStepFn here blocks
// until ctx is cancelled, standing in for a stuck package-manager child; the
// goroutine must exit within a second of Stop() and the model must still
// reach the summary card rather than being stuck on "running".
func TestStopCancelsTheRunAndUnblocksTheGoroutine(t *testing.T) {
	ctx := testCtx()
	started := make(chan struct{})
	unblocked := make(chan struct{})
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "winget", Found: true}})),
		WithRunStepFunc(func(ctx context.Context, _ []string, _ time.Duration, _ func(string)) tools.StepResult {
			close(started)
			<-ctx.Done()
			close(unblocked)
			return tools.StepResult{LastLines: []string{"still fetching"}}
		}),
	)
	m = reachList(t, m, ctx)
	m = startRun(t, m, ctx)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("the fake runStepFn never started")
	}
	if !m.Busy() {
		t.Fatal("Busy() should be true once the run has started")
	}

	m.Stop()

	select {
	case <-unblocked:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not unblock the goroutine within 1s")
	}

	var events []runEvent
	drained := make(chan struct{})
	go func() {
		for ev := range m.events {
			events = append(events, ev)
		}
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("the events channel was never closed within 1s of Stop()")
	}

	next, _ := m.Update(runBatchMsg{events: events}, ctx)
	m = next.(Model)
	if m.state != stateSummary {
		t.Fatalf("state = %v, want stateSummary", m.state)
	}
	if m.Busy() {
		t.Error("Busy() should be false once the run has reached summary")
	}

	if len(m.results) != 1 {
		t.Fatalf("results = %+v, want exactly one result for the in-flight step", m.results)
	}
	r := m.results[0]
	if r.OK || r.Skipped {
		t.Errorf("the in-flight step must be recorded as failed, not OK or skipped: %+v", r)
	}
	if r.SkipReason != "cancelled" {
		t.Errorf("SkipReason = %q, want %q", r.SkipReason, "cancelled")
	}
	if len(r.LastLines) == 0 || r.LastLines[0] != "still fetching" {
		t.Errorf("LastLines = %v, want the output the fake produced before cancellation", r.LastLines)
	}
}
