package install

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/tools/catalog"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// testCatalog is a small, deterministic catalog fixture: AppA (every
// manager), AppB (winget only) and AppC (scoop only), across two
// categories.
func testCatalog() []catalog.App {
	return []catalog.App{
		{Name: "AppA", Category: "Cat1", Scoop: "appa", Winget: "Pub.AppA", Detect: []string{"appa.exe"}},
		{Name: "AppB", Category: "Cat1", Winget: "Pub.AppB", Detect: []string{"appb.exe"}},
		{Name: "AppC", Category: "Cat2", Scoop: "appc", Detect: []string{"appc.exe"}},
	}
}

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

func fakeLookPath(found map[string]bool) LookPathFunc {
	return func(exe string) (string, error) {
		if found[exe] {
			return `C:\fake\` + exe, nil
		}
		return "", errors.New("not found")
	}
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

func rowIndex(rows []row, name string) int {
	for i, r := range rows {
		if !r.isHeader && r.app.Name == name {
			return i
		}
	}
	return -1
}

func TestInstalledAppsAreNotSelectable(t *testing.T) {
	ctx := testCtx()
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "winget", Found: true}})),
		WithLookPathFunc(fakeLookPath(map[string]bool{"appa.exe": true})),
		WithLoadCatalogFunc(func() ([]catalog.App, error) { return testCatalog(), nil }),
	)
	m = reachList(t, m, ctx)

	idx := rowIndex(m.rows, "AppA")
	if idx < 0 {
		t.Fatal("AppA row not found")
	}
	if !m.rows[idx].installed {
		t.Fatal("AppA should be detected as installed")
	}
	if !m.rows[idx].disabled() {
		t.Fatal("an installed app must be disabled (not selectable)")
	}

	m.cursor = idx
	next, _ := m.Update(keyPress(' ', " "), ctx)
	mm := next.(Model)
	if mm.rows[idx].selected {
		t.Error("space toggled an already-installed app")
	}
}

func TestUnavailableForManagerIsNotSelectable(t *testing.T) {
	ctx := testCtx()
	// Only scoop is detected, so AppB (winget-only) has no id for it.
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "scoop", Found: true}})),
		WithLookPathFunc(fakeLookPath(nil)),
		WithLoadCatalogFunc(func() ([]catalog.App, error) { return testCatalog(), nil }),
	)
	m = reachList(t, m, ctx)

	if m.manager == nil || m.manager.Name() != "scoop" {
		t.Fatalf("manager = %v, want scoop", m.manager)
	}

	idx := rowIndex(m.rows, "AppB")
	if idx < 0 {
		t.Fatal("AppB row not found")
	}
	if !m.rows[idx].unavailable {
		t.Fatal("AppB has no scoop id and should be unavailable")
	}
	if !m.rows[idx].disabled() {
		t.Fatal("an unavailable app must be disabled (not selectable)")
	}

	m.cursor = idx
	next, _ := m.Update(keyPress(' ', " "), ctx)
	mm := next.(Model)
	if mm.rows[idx].selected {
		t.Error("space toggled an app unavailable for the chosen manager")
	}
}

func TestConfirmDefaultsToNo(t *testing.T) {
	ctx := testCtx()
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "winget", Found: true}})),
		WithLookPathFunc(fakeLookPath(nil)),
		WithLoadCatalogFunc(func() ([]catalog.App, error) { return testCatalog(), nil }),
	)
	m = reachList(t, m, ctx)

	idx := rowIndex(m.rows, "AppA")
	m.cursor = idx
	next, _ := m.Update(keyPress(' ', " "), ctx)
	m = next.(Model)
	if !m.rows[idx].selected {
		t.Fatal("setup: AppA should now be selected")
	}

	next, _ = m.Update(keyPress(13, ""), ctx)
	m = next.(Model)
	if m.state != stateConfirm {
		t.Fatalf("state = %v, want stateConfirm", m.state)
	}
	if m.confirm.Answer() != confirm.AnswerNo {
		t.Error("a freshly opened confirm dialog must default to No")
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

// runToSummary drives a stateList model, with the row at idx selected,
// through confirm and the full run to stateSummary.
func runToSummary(t *testing.T, m Model, ctx uictx.Context, idx int) Model {
	t.Helper()
	m.cursor = idx
	next, _ := m.Update(keyPress(' ', " "), ctx)
	m = next.(Model)
	if !m.rows[idx].selected {
		t.Fatalf("setup: row %d should be selected", idx)
	}

	next, _ = m.Update(keyPress(13, ""), ctx)
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

func TestInstallRunsOnlySelected(t *testing.T) {
	ctx := testCtx()
	var mu sync.Mutex
	var calls [][]string
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "winget", Found: true}})),
		WithLookPathFunc(fakeLookPath(nil)),
		WithLoadCatalogFunc(func() ([]catalog.App, error) { return testCatalog(), nil }),
		WithRunStepFunc(recordingRunStep(&mu, &calls, tools.StepResult{OK: true})),
	)
	m = reachList(t, m, ctx)
	idx := rowIndex(m.rows, "AppA")

	m = runToSummary(t, m, ctx, idx)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("runStepFn called %d times, want 1", len(calls))
	}
	want := m.manager.InstallCmd("Pub.AppA")
	if len(calls[0]) != len(want) {
		t.Fatalf("argv = %v, want %v", calls[0], want)
	}
	for i := range want {
		if calls[0][i] != want[i] {
			t.Fatalf("argv = %v, want %v", calls[0], want)
		}
	}
	if len(m.results) != 1 || !m.results[0].OK {
		t.Fatalf("results = %+v, want one OK result", m.results)
	}
}

// fakeElevatedClient stands in for the elevated worker so no test triggers a
// real UAC prompt.
type fakeElevatedClient struct {
	mu       sync.Mutex
	execArgv [][]string
	closed   bool
}

func (f *fakeElevatedClient) Exec(_ context.Context, argv []string, _ time.Duration, _ func(stream, text string)) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execArgv = append(f.execArgv, append([]string(nil), argv...))
	return 0, nil
}

func (f *fakeElevatedClient) Close() error {
	f.closed = true
	return nil
}

// chocoModel is an install screen whose only detected manager is Chocolatey,
// which is the one manager that needs elevation.
func chocoModel(t *testing.T, ctx uictx.Context, opts ...Option) Model {
	t.Helper()
	base := []Option{
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "choco", Found: true}})),
		WithLookPathFunc(fakeLookPath(nil)),
		WithLoadCatalogFunc(func() ([]catalog.App, error) {
			return []catalog.App{{Name: "AppA", Category: "Cat1", Choco: "appa", Detect: []string{"appa.exe"}}}, nil
		}),
		WithRunStepFunc(func(context.Context, []string, time.Duration, func(string)) tools.StepResult {
			t.Error("a manager that needs elevation must not run through tools.RunStep")
			return tools.StepResult{}
		}),
	}
	m := New(append(base, opts...)...)
	m = reachList(t, m, ctx)
	if m.manager == nil || m.manager.Name() != "choco" {
		t.Fatalf("manager = %v, want choco", m.manager)
	}
	return m
}

// TestChocoInstallGoesThroughElevatedWorker is safety rule 20's install half:
// Chocolatey is never run in-process, only through the elevated worker.
func TestChocoInstallGoesThroughElevatedWorker(t *testing.T) {
	ctx := testCtx()
	client := &fakeElevatedClient{}
	launched := 0

	m := chocoModel(t, ctx, WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) {
		launched++
		return client, nil
	}))
	m = runToSummary(t, m, ctx, rowIndex(m.rows, "AppA"))

	if launched != 1 {
		t.Fatalf("the elevated worker was launched %d times, want exactly 1", launched)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.execArgv) != 1 {
		t.Fatalf("Exec was called %d times, want 1", len(client.execArgv))
	}
	want := m.manager.InstallCmd("appa")
	if strings.Join(client.execArgv[0], " ") != strings.Join(want, " ") {
		t.Errorf("argv = %v, want %v", client.execArgv[0], want)
	}
	if !client.closed {
		t.Error("the elevated client was never closed")
	}
	if len(m.results) != 1 || !m.results[0].OK {
		t.Fatalf("results = %+v, want one OK result", m.results)
	}
}

// TestDeclinedUACMarksInstallSkipped keeps a refused prompt from looking like
// a failure (plan.md section 10, "Elevated worker").
func TestDeclinedUACMarksInstallSkipped(t *testing.T) {
	ctx := testCtx()
	m := chocoModel(t, ctx, WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) {
		return nil, &elevate.DeclinedError{}
	}))
	m = runToSummary(t, m, ctx, rowIndex(m.rows, "AppA"))

	if len(m.results) != 1 {
		t.Fatalf("results = %+v, want one result", m.results)
	}
	r := m.results[0]
	if !r.Skipped {
		t.Error("a declined UAC prompt must mark the install skipped, not failed")
	}
	if r.SkipReason != "skipped (needs admin)" {
		t.Errorf("SkipReason = %q, want the admin wording", r.SkipReason)
	}
	if !strings.Contains(m.View(ctx), "needs admin") {
		t.Error("the summary should say the install needs admin")
	}
}

func TestFailureShowsLastLines(t *testing.T) {
	ctx := testCtx()
	var mu sync.Mutex
	var calls [][]string
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "winget", Found: true}})),
		WithLookPathFunc(fakeLookPath(nil)),
		WithLoadCatalogFunc(func() ([]catalog.App, error) { return testCatalog(), nil }),
		WithRunStepFunc(recordingRunStep(&mu, &calls, tools.StepResult{
			OK:        false,
			ExitCode:  1,
			LastLines: []string{"downloading AppA", "error: checksum mismatch"},
		})),
	)
	m = reachList(t, m, ctx)
	idx := rowIndex(m.rows, "AppA")

	m = runToSummary(t, m, ctx, idx)

	if len(m.results) != 1 || m.results[0].OK {
		t.Fatalf("results = %+v, want one failed result", m.results)
	}
	got := m.results[0].LastLines
	if len(got) != 2 || got[0] != "downloading AppA" || got[1] != "error: checksum mismatch" {
		t.Fatalf("LastLines = %v, want the recorded failure lines", got)
	}

	view := m.View(ctx)
	if !strings.Contains(view, "checksum mismatch") {
		t.Error("summary view does not show the failure's last lines")
	}
}

// startRun drives a stateList model, with the row at idx selected and
// confirmed, to stateRunning and returns it along with the command it
// resolved to.
func startRun(t *testing.T, m Model, ctx uictx.Context, idx int) Model {
	t.Helper()
	m.cursor = idx
	next, _ := m.Update(keyPress(' ', " "), ctx)
	m = next.(Model)
	if !m.rows[idx].selected {
		t.Fatalf("setup: row %d should be selected", idx)
	}

	next, _ = m.Update(keyPress(13, ""), ctx)
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
		WithLookPathFunc(fakeLookPath(nil)),
		WithLoadCatalogFunc(func() ([]catalog.App, error) { return testCatalog(), nil }),
		WithRunStepFunc(recordingRunStep(&mu, &calls, tools.StepResult{OK: true})),
	)
	if m.Busy() {
		t.Error("a freshly built screen must not be busy")
	}

	m = reachList(t, m, ctx)
	if m.Busy() {
		t.Error("stateList must not be busy")
	}

	idx := rowIndex(m.rows, "AppA")
	m = startRun(t, m, ctx, idx)
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
// finding 2: install ran against context.Background(), so Esc or Ctrl+C
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
		WithLookPathFunc(fakeLookPath(nil)),
		WithLoadCatalogFunc(func() ([]catalog.App, error) { return testCatalog(), nil }),
		WithRunStepFunc(func(ctx context.Context, _ []string, _ time.Duration, _ func(string)) tools.StepResult {
			close(started)
			<-ctx.Done()
			close(unblocked)
			return tools.StepResult{LastLines: []string{"still downloading"}}
		}),
	)
	m = reachList(t, m, ctx)
	idx := rowIndex(m.rows, "AppA")
	m = startRun(t, m, ctx, idx)

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
		t.Fatalf("results = %+v, want exactly one result for the in-flight app", m.results)
	}
	r := m.results[0]
	if r.OK || r.Skipped {
		t.Errorf("the in-flight app must be recorded as failed, not OK or skipped: %+v", r)
	}
	if r.SkipReason != "cancelled" {
		t.Errorf("SkipReason = %q, want %q", r.SkipReason, "cancelled")
	}
	if len(r.LastLines) == 0 || r.LastLines[0] != "still downloading" {
		t.Errorf("LastLines = %v, want the output the fake produced before cancellation", r.LastLines)
	}
}
