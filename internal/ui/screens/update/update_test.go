package update

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/activity"
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
		BodyTop:    3,
	}
}

func fakeDetectFn(names ...string) DetectFunc {
	return func(context.Context) []tools.Tool {
		var out []tools.Tool
		for _, n := range names {
			out = append(out, tools.Tool{Name: n, Found: true})
		}
		return out
	}
}

func keyPress(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Text: text}
}

// wingetTable is what `winget upgrade` prints on an English machine with
// two outdated apps and one it cannot version.
const wingetTable = `Name           Id                    Version  Available Source
-----------------------------------------------------------------
Docker Desktop Docker.DockerDesktop  4.87.0   4.91.0    winget
FFmpeg         Gyan.FFmpeg           8.1.2    9.0.2     winget
2 upgrades available.
1 package(s) have version numbers that cannot be determined. Use --include-unknown to see all results.
`

// scoopStatus is `scoop status` with one app to update and one held.
const scoopStatus = `Name Installed Version Latest Version Missing Dependencies Info
---- ----------------- -------------- -------------------- ----
git  2.46.0            2.47.1
nvm  1.1.12            1.2.2                               Held package
`

// fakeRunner answers checks with canned output and records every command,
// in the order they ran. upgrade decides each upgrade's result.
type fakeRunner struct {
	mu      sync.Mutex
	calls   [][]string
	outputs map[string]string
	upgrade func(argv []string, onLine func(tools.Line)) tools.StepResult
}

func (f *fakeRunner) run(_ context.Context, argv []string, _ time.Duration, onLine func(tools.Line)) tools.StepResult {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), argv...))
	f.mu.Unlock()
	if out, ok := f.outputs[strings.Join(argv, " ")]; ok {
		for _, l := range strings.Split(out, "\n") {
			onLine(tools.Line{Text: l})
		}
		return tools.StepResult{OK: true}
	}
	if f.upgrade != nil {
		return f.upgrade(argv, onLine)
	}
	return tools.StepResult{OK: true}
}

// commands returns every recorded call whose argv starts with prefix.
func (f *fakeRunner) commands(prefix ...string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if len(c) >= len(prefix) && strings.Join(c[:len(prefix)], " ") == strings.Join(prefix, " ") {
			out = append(out, strings.Join(c, " "))
		}
	}
	return out
}

func newRunner() *fakeRunner {
	return &fakeRunner{outputs: map[string]string{
		"winget upgrade --accept-source-agreements": wingetTable,
		"scoop status":                  scoopStatus,
		"npm outdated -g --json":        `{"pnpm":{"current":"9.11.0","wanted":"9.14.2","latest":"9.14.2"}}`,
		"scoop update":                  "Scoop was updated successfully!",
		"choco outdated":                "Outdated Packages\n\ngit|2.40.0|2.47.1|false\n\nChocolatey has determined 1 package(s) are outdated.",
		"choco outdated --limit-output": "git|2.40.0|2.47.1|false",
	}}
}

func update(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg, testCtx())
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return mm, cmd
}

// reachList runs detection and every check through to the pick list.
func reachList(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = update(t, m, m.Init()())
	deadline := time.Now().Add(5 * time.Second)
	for m.state == stateChecking {
		if time.Now().After(deadline) {
			t.Fatal("checks never finished")
		}
		time.Sleep(5 * time.Millisecond)
		m, _ = update(t, m, tickMsg{})
	}
	return m
}

// runToSummary confirms the pick list and drives the run to its summary.
func runToSummary(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = update(t, m, keyPress(tea.KeyEnter, ""))
	if m.state != stateConfirm {
		t.Fatalf("state = %v, want stateConfirm", m.state)
	}
	m, cmd := update(t, m, keyPress('y', "y"))
	m, _ = update(t, m, cmd())
	if m.state != stateRunning {
		t.Fatalf("state = %v, want stateRunning", m.state)
	}
	var events []runEvent
	for ev := range m.events {
		events = append(events, ev)
	}
	m, _ = update(t, m, runBatchMsg{events: events, closed: true})
	if m.state != stateSummary {
		t.Fatalf("state = %v, want stateSummary", m.state)
	}
	return m
}

// view renders the screen without colour.
func view(m Model) string { return ansi.Strip(m.View(testCtx())) }

func pkgNamed(m Model, name string) *pkg {
	for i := range m.pkgs {
		if m.pkgs[i].label() == name {
			return &m.pkgs[i]
		}
	}
	return nil
}

func rowFor(m Model, label string) activity.Row {
	for _, j := range m.jobs {
		if j.label == label {
			return j.row
		}
	}
	return activity.Row{}
}

// Every app is listed under its manager, everything that can be ticked is,
// and the ones that cannot are there with the reason.
func TestPickListIsGroupedAndTicked(t *testing.T) {
	r := newRunner()
	m := reachList(t, New(WithDetectFunc(fakeDetectFn("winget", "scoop", "npm")), WithRunStepFunc(r.run)))
	if m.state != stateList {
		t.Fatalf("state = %v, want stateList", m.state)
	}

	var heads []string
	for _, row := range m.rows {
		if row.heading {
			heads = append(heads, m.groups[row.group].manager.Name())
		}
	}
	if strings.Join(heads, ",") != "npm,winget,scoop" {
		t.Errorf("groups = %v, want npm,winget,scoop", heads)
	}
	for _, name := range []string{"pnpm", "Docker Desktop", "FFmpeg", "git"} {
		p := pkgNamed(m, name)
		if p == nil || !p.selected || p.locked != "" {
			t.Errorf("%s should be listed and ticked: %+v", name, p)
		}
	}
	if p := pkgNamed(m, "nvm"); p == nil || p.selected || p.locked != "held" {
		t.Errorf("held nvm should be listed, unticked and locked: %+v", p)
	}

	out := view(m)
	for _, want := range []string{"5 updates available", "4.87.0 → 4.91.0", "1 app with an unknown version isn't shown", "Selected 4 of 4", "held"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	// The scoop bucket refresh ran before scoop was asked for its status.
	if got := r.commands("scoop"); len(got) < 2 || got[0] != "scoop update" || got[1] != "scoop status" {
		t.Errorf("scoop checks = %v, want update before status", got)
	}
}

// Unticking an app keeps it out of the run; everything else is upgraded one
// app at a time with its manager's own per-app command, npm first, and Scoop
// clears the old versions of just the apps it updated.
func TestRunUpgradesTickedAppsOneByOne(t *testing.T) {
	r := newRunner()
	m := reachList(t, New(WithDetectFunc(fakeDetectFn("winget", "scoop", "npm")), WithRunStepFunc(r.run)))

	// Untick FFmpeg.
	for i, row := range m.rows {
		if !row.heading && m.pkgs[row.pkg].label() == "FFmpeg" {
			m.cursor = i
		}
	}
	m, _ = update(t, m, keyPress(tea.KeySpace, " "))
	m = runToSummary(t, m)

	upgrades := append(append(r.commands("npm", "install"), r.commands("winget", "upgrade", "--id")...), r.commands("scoop", "update", "git")...)
	want := []string{
		"npm install -g pnpm@latest",
		"winget upgrade --id Docker.DockerDesktop -e --silent --accept-package-agreements --accept-source-agreements --disable-interactivity",
		"scoop update git",
	}
	if strings.Join(upgrades, "\n") != strings.Join(want, "\n") {
		t.Errorf("upgrades =\n%s\nwant\n%s", strings.Join(upgrades, "\n"), strings.Join(want, "\n"))
	}
	if got := r.commands("winget", "upgrade", "--id", "Gyan.FFmpeg"); len(got) != 0 {
		t.Errorf("unticked FFmpeg was upgraded: %v", got)
	}
	if got := r.commands("scoop", "cleanup"); len(got) != 1 || got[0] != "scoop cleanup git" {
		t.Errorf("cleanup = %v, want only the updated app", got)
	}
	if rowFor(m, "Docker Desktop").State != activity.Done {
		t.Errorf("Docker Desktop row = %+v", rowFor(m, "Docker Desktop"))
	}
	if out := view(m); !strings.Contains(out, "Updated 3") { // the Scoop cleanup is a row, not an app
		t.Errorf("summary lacks the count:\n%s", out)
	}
}

// Exit codes come back as plain words on the row, and a restart turns the
// row into a warning rather than a failure.
func TestVerdictsReadAsPlainWords(t *testing.T) {
	r := newRunner()
	r.upgrade = func(argv []string, onLine func(tools.Line)) tools.StepResult {
		switch {
		case strings.Contains(strings.Join(argv, " "), "Docker.DockerDesktop"):
			onLine(tools.Line{Text: "Installer failed"})
			return tools.StepResult{ExitCode: hresult(0x8A150101), LastLines: []string{"Installer failed"}} // in use
		case strings.Contains(strings.Join(argv, " "), "Gyan.FFmpeg"):
			return tools.StepResult{ExitCode: hresult(0x8A150109)} // restart needed
		}
		return tools.StepResult{OK: true}
	}
	m := reachList(t, New(WithDetectFunc(fakeDetectFn("winget")), WithRunStepFunc(r.run)))
	m = runToSummary(t, m)

	if row := rowFor(m, "Docker Desktop"); row.State != activity.Failed || !strings.Contains(row.Detail, "in use") {
		t.Errorf("in-use row = %+v", row)
	}
	if row := rowFor(m, "FFmpeg"); row.State != activity.Warn || !strings.Contains(row.Detail, "restart") {
		t.Errorf("restart row = %+v", row)
	}
	out := view(m)
	for _, want := range []string{"1 needs a restart", "Failed 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
}

// hresult is a winget error code the way a process exit status carries it
// on Windows: the HRESULT's bits read as a signed 32-bit number.
func hresult(code uint32) int { return int(int32(code)) }

// A progress redraw moves the row's bar and never reaches the log; a real
// line becomes the row's detail and does.
func TestProgressAndLog(t *testing.T) {
	r := newRunner()
	gate := make(chan struct{})
	r.upgrade = func(argv []string, onLine func(tools.Line)) tools.StepResult {
		onLine(tools.Line{Text: "Downloading https://example.test/docker.exe"})
		onLine(tools.Line{Text: "38.1 MB / 90.2 MB", Transient: true})
		<-gate
		return tools.StepResult{OK: true}
	}
	m := reachList(t, New(WithDetectFunc(fakeDetectFn("winget")), WithRunStepFunc(r.run)))
	m, _ = update(t, m, keyPress(tea.KeyEnter, ""))
	m, cmd := update(t, m, keyPress('y', "y"))
	m, _ = update(t, m, cmd())

	deadline := time.Now().Add(5 * time.Second)
	for rowFor(m, "Docker Desktop").Percent <= 0 {
		if time.Now().After(deadline) {
			t.Fatal("progress never arrived")
		}
		time.Sleep(5 * time.Millisecond)
		evs, _ := drainEvents(m.events)
		m, _ = update(t, m, runBatchMsg{events: evs})
	}
	row := rowFor(m, "Docker Desktop")
	if row.State != activity.Running || row.Percent < 42 || row.Percent > 43 || !strings.Contains(row.Detail, "Downloading") {
		t.Errorf("running row = %+v", row)
	}
	if len(m.log) != 1 || strings.Contains(strings.Join(m.log, ""), "MB /") {
		t.Errorf("log = %q, want the one real line only", m.log)
	}
	if !m.Busy() || !strings.Contains(m.Title(), "Docker Desktop") {
		t.Errorf("busy=%v title=%q", m.Busy(), m.Title())
	}
	if out := view(m); !strings.Contains(out, "Updating 1 of 2") || !strings.Contains(out, "42%") {
		t.Errorf("running view:\n%s", out)
	}
	m.Stop()
	close(gate)
	for ev := range m.events {
		m, _ = update(t, m, runBatchMsg{events: []runEvent{ev}})
	}
	m, _ = update(t, m, runBatchMsg{closed: true})
	if m.state != stateSummary || rowFor(m, "FFmpeg").Detail != "cancelled" {
		t.Errorf("after stop: state=%v ffmpeg=%+v", m.state, rowFor(m, "FFmpeg"))
	}
}

// A manager whose answer cannot be read offers to update everything, the
// old way, rather than dropping out of the list.
func TestUnreadableCheckFallsBackToUpdateAll(t *testing.T) {
	r := newRunner()
	r.outputs["winget upgrade --accept-source-agreements"] = "Quelque chose d'inattendu s'est produit."
	m := reachList(t, New(WithDetectFunc(fakeDetectFn("winget")), WithRunStepFunc(r.run)))
	if len(m.pkgs) != 1 || !m.pkgs[0].all || !m.pkgs[0].selected {
		t.Fatalf("pkgs = %+v, want one ticked update-everything row", m.pkgs)
	}
	runToSummary(t, m)
	if got := r.commands("winget", "upgrade", "--all"); len(got) != 1 {
		t.Errorf("fallback ran %v, want the upgrade-all command", got)
	}
}

// Nothing out of date is a happy ending, not an empty list.
func TestEverythingUpToDate(t *testing.T) {
	r := newRunner()
	r.outputs["npm outdated -g --json"] = "{}"
	m := reachList(t, New(WithDetectFunc(fakeDetectFn("npm")), WithRunStepFunc(r.run)))
	if m.state != stateUpToDate || !strings.Contains(view(m), "Everything's up to date") {
		t.Fatalf("state = %v:\n%s", m.state, view(m))
	}
}

// A heading ticks its whole group, a and n tick and untick everything, and
// none of them touch a locked app.
func TestGroupAndBulkToggles(t *testing.T) {
	r := newRunner()
	m := reachList(t, New(WithDetectFunc(fakeDetectFn("scoop")), WithRunStepFunc(r.run)))
	m, _ = update(t, m, keyPress('n', "n"))
	if len(m.picked()) != 0 {
		t.Fatalf("n left %d ticked", len(m.picked()))
	}
	m.cursor = 0 // the scoop heading
	m, _ = update(t, m, keyPress(tea.KeySpace, " "))
	if p := pkgNamed(m, "git"); !p.selected {
		t.Error("the heading did not tick its group")
	}
	if p := pkgNamed(m, "nvm"); p.selected {
		t.Error("a held app was ticked")
	}
	m, _ = update(t, m, keyPress(tea.KeyEnter, ""))
	if m.state != stateConfirm {
		t.Errorf("state = %v, want the confirm dialog", m.state)
	}
}

// A click on a row ticks or unticks it; hovering moves the highlight.
func TestMouseTogglesRows(t *testing.T) {
	r := newRunner()
	m := reachList(t, New(WithDetectFunc(fakeDetectFn("winget")), WithRunStepFunc(r.run)))
	c := testCtx()
	// Row 0 is the heading, row 1 Docker Desktop, drawn under the list head.
	y := c.BodyTop + listHead + 1
	m, _ = update(t, m, tea.MouseMotionMsg{X: 10, Y: y})
	if m.cursor != 1 {
		t.Fatalf("hover moved the cursor to %d, want 1", m.cursor)
	}
	m, _ = update(t, m, tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseLeft})
	if p := pkgNamed(m, "Docker Desktop"); p.selected {
		t.Error("a click did not untick the row")
	}
	m, _ = update(t, m, tea.MouseClickMsg{X: 10, Y: y, Button: tea.MouseRight})
	if p := pkgNamed(m, "Docker Desktop"); p.selected {
		t.Error("a right click toggled the row")
	}
}

type fakeElevatedClient struct {
	mu       sync.Mutex
	execArgv [][]string
	code     int
	closed   bool
}

func (f *fakeElevatedClient) Exec(_ context.Context, argv []string, _ time.Duration, onLine func(stream, text string)) (int, error) {
	f.mu.Lock()
	f.execArgv = append(f.execArgv, append([]string(nil), argv...))
	f.mu.Unlock()
	onLine("stdout", "Upgraded "+argv[len(argv)-2])
	return f.code, nil
}

func (f *fakeElevatedClient) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

// Chocolatey apps run through the elevated worker, launched once for all of
// them, and the worker is closed when the run ends.
func TestChocoRunsThroughOneElevatedWorker(t *testing.T) {
	r := newRunner()
	client := &fakeElevatedClient{}
	launches := 0
	m := reachList(t, New(
		WithDetectFunc(fakeDetectFn("choco")),
		WithRunStepFunc(r.run),
		WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) {
			launches++
			return client, nil
		}),
	))
	if len(m.pkgs) == 0 {
		t.Fatalf("choco listed nothing:\n%s", view(m))
	}
	m = runToSummary(t, m)
	if launches != 1 || len(client.execArgv) != len(m.pkgs) || !client.closed {
		t.Errorf("launches=%d execs=%v closed=%v", launches, client.execArgv, client.closed)
	}
	if got := strings.Join(client.execArgv[0], " "); got != "choco upgrade git -y" {
		t.Errorf("elevated argv = %q", got)
	}
}

// Saying no to the admin prompt skips the Chocolatey apps; it does not fail
// them.
func TestDeclinedAdminPromptSkips(t *testing.T) {
	r := newRunner()
	m := reachList(t, New(
		WithDetectFunc(fakeDetectFn("choco")),
		WithRunStepFunc(r.run),
		WithLaunchElevatedFunc(func(context.Context) (elevatedClient, error) {
			return nil, &elevate.DeclinedError{}
		}),
	))
	m = runToSummary(t, m)
	for _, j := range m.jobs {
		if j.row.State != activity.Skipped || !strings.Contains(j.row.Detail, "needs admin") {
			t.Errorf("%s = %+v, want skipped (needs admin)", j.label, j.row)
		}
	}
}

// No package manager at all says so plainly.
func TestNoManagers(t *testing.T) {
	m := New(WithDetectFunc(fakeDetectFn()))
	m, _ = update(t, m, m.Init()())
	if m.state != stateNoManagers || !strings.Contains(view(m), "No package manager") {
		t.Errorf("state = %v:\n%s", m.state, view(m))
	}
}

// Leaving the screen mid-check cancels the checks: the manager commands see
// their context end rather than running on behind the user's back.
func TestStopCancelsTheChecks(t *testing.T) {
	cancelled := make(chan struct{}, 4)
	run := func(ctx context.Context, _ []string, _ time.Duration, _ func(tools.Line)) tools.StepResult {
		<-ctx.Done()
		cancelled <- struct{}{}
		return tools.StepResult{ExitCode: -1}
	}
	m := New(WithDetectFunc(fakeDetectFn("winget", "npm")), WithRunStepFunc(run))
	m, _ = update(t, m, m.Init()())
	if m.state != stateChecking {
		t.Fatalf("state = %v, want checking", m.state)
	}
	m.Stop()
	for range 2 {
		select {
		case <-cancelled:
		case <-time.After(5 * time.Second):
			t.Fatal("a check kept running after Stop")
		}
	}
}
