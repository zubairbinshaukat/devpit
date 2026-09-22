package ports

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	engine "github.com/zubairbinshaukat/devpit/internal/ports"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// buildCtx returns the render context every test uses: dark theme, unicode
// icons, the given config, 100x30.
func buildCtx(cfg config.Config) uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      icons.Unicode(),
		Config:     cfg,
		Width:      100,
		Height:     30,
		BodyHeight: 26,
	}
}

// press builds the key message for a single printable character, the same
// way internal/ui/components/confirm's tests do.
func press(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

// special builds the key message for a named key.
func special(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

// mustModel type-asserts a screen back to Model, failing the test with a
// clear message if the assertion cannot hold.
func mustModel(t *testing.T, scr uictx.Screen) Model {
	t.Helper()
	m, ok := scr.(Model)
	if !ok {
		t.Fatalf("screen is %T, want ports.Model", scr)
	}
	return m
}

// runCmd executes cmd and fails the test if there was nothing to run.
func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command, got nil")
	}
	return cmd()
}

// TestKillHappyPathTwoKeystrokes drives the whole "kill a port" flow with
// exactly two tea.KeyPressMsg values sent to the screen's Update: Enter to
// pick "Kill a port" off the submenu (the port field is prefilled from the
// config's dev-port list) and "y" to confirm. Every other message in the
// sequence is a command the screen itself returned, resolved the way Bubble
// Tea would resolve it in a real run (plan.md milestone 3: "Kill port 3000 in
// two keystrokes").
func TestKillHappyPathTwoKeystrokes(t *testing.T) {
	var killCalls []uint32
	var waitFreeCalls []uint16

	m := New()
	m.byPortFn = func(port uint16) ([]engine.Conn, error) {
		return []engine.Conn{{
			Proto:     "tcp",
			LocalAddr: net.ParseIP("127.0.0.1"),
			LocalPort: port,
			State:     "LISTEN",
			PID:       111,
		}}, nil
	}
	m.lookupFn = func(pid uint32) (engine.Process, error) {
		switch pid {
		case 111:
			return engine.Process{PID: 111, ParentPID: 222, Name: "node.exe"}, nil
		case 222:
			return engine.Process{PID: 222, Name: "explorer.exe"}, nil
		}
		return engine.Process{}, errors.New("unexpected pid")
	}
	m.killFn = func(pid uint32) error {
		killCalls = append(killCalls, pid)
		return nil
	}
	m.killTreeFn = func(uint32) error {
		t.Fatal("killTreeFn should not be called on the plain happy path")
		return nil
	}
	m.waitFreeFn = func(_ context.Context, port uint16, _ time.Duration) bool {
		waitFreeCalls = append(waitFreeCalls, port)
		return true
	}

	cfg := config.Default() // DevPorts starts at 3000.
	ctx := buildCtx(cfg)

	// Keystroke 1: Enter selects "Kill a port" (the first submenu item).
	scr, cmd := m.Update(special(tea.KeyEnter), ctx)
	msg := runCmd(t, cmd)
	scr, cmd = scr.Update(msg, ctx) // menu.SelectedMsg -> stageKillInput + lookup
	got := mustModel(t, scr)
	if got.stage != stageKillInput {
		t.Fatalf("after selecting the item, stage = %v, want stageKillInput", got.stage)
	}
	msg = runCmd(t, cmd) // killLookupMsg
	scr, cmd = scr.Update(msg, ctx)
	got = mustModel(t, scr)
	if got.stage != stageKillConfirm {
		t.Fatalf("after the lookup, stage = %v, want stageKillConfirm", got.stage)
	}
	if cmd != nil {
		t.Fatal("reaching the confirm dialog should not itself issue a command")
	}

	// Keystroke 2: "y" confirms.
	scr, cmd = got.Update(press("y"), ctx)
	msg = runCmd(t, cmd)            // confirm.AnsweredMsg{Yes}
	scr, cmd = scr.Update(msg, ctx) // -> stageKilling + kill command
	got = mustModel(t, scr)
	if got.stage != stageKilling {
		t.Fatalf("after answering yes, stage = %v, want stageKilling", got.stage)
	}
	msg = runCmd(t, cmd) // killDoneMsg
	scr, _ = scr.Update(msg, ctx)
	got = mustModel(t, scr)

	if got.stage != stageSummary {
		t.Fatalf("final stage = %v, want stageSummary", got.stage)
	}
	if got.summaryModel.Result.Failed {
		t.Fatal("the summary reports failure on the happy path")
	}
	if want := "Port 3000 is free"; got.summaryModel.Result.Headline != want {
		t.Errorf("summary headline = %q, want %q", got.summaryModel.Result.Headline, want)
	}
	if len(killCalls) != 1 || killCalls[0] != 111 {
		t.Errorf("killFn calls = %v, want [111]", killCalls)
	}
	if len(waitFreeCalls) != 1 || waitFreeCalls[0] != 3000 {
		t.Errorf("waitFreeFn calls = %v, want [3000]", waitFreeCalls)
	}
}

// TestProtectedProcessCannotBeKilled pins docs/safety.md rule 16 at the
// screen level: a protected row can never be marked, its reason is rendered,
// and pressing the kill key with nothing else marked never calls killFn.
func TestProtectedProcessCannotBeKilled(t *testing.T) {
	const reason = "svchost.exe hosts one or more Windows services and is never killed directly"
	killCalls := 0

	m := New()
	m.listFn = func(context.Context) ([]engine.Conn, error) {
		return []engine.Conn{{
			Proto:     "tcp",
			LocalAddr: net.ParseIP("127.0.0.1"),
			LocalPort: 3000,
			State:     "LISTEN",
			PID:       77,
		}}, nil
	}
	m.lookupFn = func(pid uint32) (engine.Process, error) {
		if pid == 77 {
			return engine.Process{PID: 77, Name: "svchost.exe", Protected: true, ProtectedReason: reason}, nil
		}
		return engine.Process{}, errors.New("unexpected pid")
	}
	m.killFn = func(uint32) error {
		killCalls++
		return nil
	}

	ctx := buildCtx(config.Default())

	// Move to "Busy dev ports" (the second submenu item) and select it.
	scr, _ := m.Update(special(tea.KeyDown), ctx)
	scr, cmd := scr.Update(special(tea.KeyEnter), ctx)
	msg := runCmd(t, cmd)
	scr, cmd = scr.Update(msg, ctx) // menu.SelectedMsg -> fetch
	msg = runCmd(t, cmd)            // rowsMsg
	scr, _ = scr.Update(msg, ctx)
	got := mustModel(t, scr)

	if len(got.rows) != 1 || !got.rows[0].Protected {
		t.Fatalf("rows = %+v, want one protected row", got.rows)
	}
	if got.rows[0].ProtectedReason != reason {
		t.Errorf("ProtectedReason = %q, want %q", got.rows[0].ProtectedReason, reason)
	}

	// Space must not mark a protected row.
	scr, _ = got.Update(special(tea.KeySpace), ctx)
	got = mustModel(t, scr)
	if len(got.markedRows()) != 0 {
		t.Errorf("marked rows = %v, want none: a protected row must never be markable", got.markedRows())
	}

	// k with nothing marked must not open a confirm or call killFn.
	scr, cmd = got.Update(press("k"), ctx)
	got = mustModel(t, scr)
	if cmd != nil {
		t.Error("pressing k with nothing marked should not return a command")
	}
	if got.stage != stageList {
		t.Errorf("stage = %v, want stageList: a protected-only list must never reach confirm", got.stage)
	}
	if killCalls != 0 {
		t.Errorf("killFn was called %d times, want 0", killCalls)
	}

	if view := got.viewList(ctx); !strings.Contains(view, reason) {
		t.Errorf("the list view does not render the protected reason:\n%s", view)
	}
}

// TestKillTreeOfferedForNpmParent checks that when the process on a port was
// spawned by npm (a tree-offering parent, per ports.OffersTree), the confirm
// screen offers "kill process tree", and that turning it on kills the parent
// tree instead of just the target PID.
func TestKillTreeOfferedForNpmParent(t *testing.T) {
	var treeKillCalls []uint32
	var plainKillCalls []uint32

	m := New()
	m.byPortFn = func(port uint16) ([]engine.Conn, error) {
		return []engine.Conn{{LocalPort: port, State: "LISTEN", PID: 500}}, nil
	}
	m.lookupFn = func(pid uint32) (engine.Process, error) {
		switch pid {
		case 500:
			return engine.Process{PID: 500, ParentPID: 600, Name: "node.exe"}, nil
		case 600:
			return engine.Process{PID: 600, Name: "npm.exe"}, nil
		}
		return engine.Process{}, errors.New("unexpected pid")
	}
	m.treeFn = func(pid uint32) []engine.Process {
		if pid != 600 {
			t.Fatalf("treeFn called with %d, want the parent 600", pid)
		}
		return []engine.Process{{PID: 500, ParentPID: 600, Name: "node.exe"}}
	}
	m.killTreeFn = func(pid uint32) error {
		treeKillCalls = append(treeKillCalls, pid)
		return nil
	}
	m.killFn = func(pid uint32) error {
		plainKillCalls = append(plainKillCalls, pid)
		return nil
	}
	m.waitFreeFn = func(context.Context, uint16, time.Duration) bool { return true }

	ctx := buildCtx(config.Default())

	scr, cmd := m.Update(special(tea.KeyEnter), ctx) // select "Kill a port"
	msg := runCmd(t, cmd)
	scr, cmd = scr.Update(msg, ctx)
	msg = runCmd(t, cmd) // killLookupMsg
	scr, _ = scr.Update(msg, ctx)
	got := mustModel(t, scr)

	if got.stage != stageKillConfirm {
		t.Fatalf("stage = %v, want stageKillConfirm", got.stage)
	}
	if !got.killOfferTree {
		t.Fatal("killOfferTree is false, want true for an npm parent")
	}
	if got.killParent.Name != "npm.exe" {
		t.Errorf("killParent.Name = %q, want npm.exe", got.killParent.Name)
	}
	if !strings.Contains(got.viewKillConfirm(ctx), "Kill process tree") {
		t.Error(`view does not mention "Kill process tree" when it is offered`)
	}

	// Turn the toggle on, then confirm.
	scr, _ = got.Update(press("t"), ctx)
	got = mustModel(t, scr)
	if !got.killTreeOn {
		t.Fatal("t did not turn the kill-tree toggle on")
	}

	scr, cmd = got.Update(press("y"), ctx)
	msg = runCmd(t, cmd) // confirm.AnsweredMsg{Yes}
	_, cmd = scr.Update(msg, ctx)
	_ = runCmd(t, cmd) // killDoneMsg, not needed further

	if len(plainKillCalls) != 0 {
		t.Errorf("killFn was called with %v, want it untouched when kill-tree is on", plainKillCalls)
	}
	if len(treeKillCalls) != 1 || treeKillCalls[0] != 600 {
		t.Errorf("killTreeFn calls = %v, want [600] (the parent, not the target)", treeKillCalls)
	}
}

// TestBusyPortsUsesConfigList checks that the busy-dev-ports flow filters
// against ctx.Config.DevPorts, not some other list: a LISTEN row outside the
// configured ports must not appear.
func TestBusyPortsUsesConfigList(t *testing.T) {
	m := New()
	m.listFn = func(context.Context) ([]engine.Conn, error) {
		return []engine.Conn{
			{Proto: "tcp", LocalAddr: net.ParseIP("127.0.0.1"), LocalPort: 4321, State: "LISTEN", PID: 55},
			{Proto: "tcp", LocalAddr: net.ParseIP("127.0.0.1"), LocalPort: 9999, State: "LISTEN", PID: 66},
		}, nil
	}
	m.lookupFn = func(pid uint32) (engine.Process, error) {
		if pid == 55 {
			return engine.Process{PID: 55, Name: "node.exe"}, nil
		}
		t.Errorf("lookupFn called for PID %d, which is outside the configured dev-port list", pid)
		return engine.Process{}, errors.New("unexpected pid")
	}

	cfg := config.Default()
	cfg.DevPorts = []int{4321}
	ctx := buildCtx(cfg)

	scr, _ := m.Update(special(tea.KeyDown), ctx)      // cursor -> "Busy dev ports"
	scr, cmd := scr.Update(special(tea.KeyEnter), ctx) // select it
	msg := runCmd(t, cmd)
	scr, cmd = scr.Update(msg, ctx) // menu.SelectedMsg -> fetch
	msg = runCmd(t, cmd)            // rowsMsg
	scr, _ = scr.Update(msg, ctx)
	got := mustModel(t, scr)

	if len(got.rows) != 1 {
		t.Fatalf("rows = %+v, want exactly the one row on the configured port", got.rows)
	}
	if got.rows[0].PID != 55 {
		t.Errorf("row PID = %d, want 55", got.rows[0].PID)
	}
}

// TestPortInputRejectsGarbage checks that the kill-a-port field only ever
// accepts digits, and that backspace removes one at a time.
func TestPortInputRejectsGarbage(t *testing.T) {
	m := New()
	m.stage = stageKillInput
	m.portDigits = ""
	ctx := buildCtx(config.Default())

	scr, _ := m.Update(press("a"), ctx)
	got := mustModel(t, scr)
	if got.portDigits != "" {
		t.Errorf("a letter was accepted: portDigits = %q", got.portDigits)
	}

	scr, _ = got.Update(press("!"), ctx)
	got = mustModel(t, scr)
	if got.portDigits != "" {
		t.Errorf("punctuation was accepted: portDigits = %q", got.portDigits)
	}

	for _, d := range []string{"3", "0", "0", "0", "0"} {
		scr, _ = got.Update(press(d), ctx)
		got = mustModel(t, scr)
	}
	if got.portDigits != "30000" {
		t.Fatalf("portDigits = %q, want 30000", got.portDigits)
	}

	// A sixth digit must not grow the field past 65535's width.
	scr, _ = got.Update(press("1"), ctx)
	got = mustModel(t, scr)
	if got.portDigits != "30000" {
		t.Errorf("a 6th digit was accepted: portDigits = %q", got.portDigits)
	}

	scr, _ = got.Update(special(tea.KeyBackspace), ctx)
	got = mustModel(t, scr)
	if got.portDigits != "3000" {
		t.Errorf("backspace left portDigits = %q, want 3000", got.portDigits)
	}
}

// TestDefaultAnswerIsNo pins docs/safety.md rule 2 at the screen level:
// pressing Enter on a fresh confirm dialog answers No and never calls
// killFn.
func TestDefaultAnswerIsNo(t *testing.T) {
	killCalls := 0
	m := New()
	m.killFn = func(uint32) error {
		killCalls++
		return nil
	}
	m.stage = stageKillConfirm
	m.lastPort = 3000
	m.killTarget = engine.Process{PID: 1, Name: "x.exe"}
	m.killConfirm = confirm.New(killConfirmID, "Kill x.exe (PID 1) on port 3000?", "detail")

	ctx := buildCtx(config.Default())

	scr, cmd := m.Update(special(tea.KeyEnter), ctx)
	msg := runCmd(t, cmd)
	ans, ok := msg.(confirm.AnsweredMsg)
	if !ok {
		t.Fatalf("Enter produced %T, want confirm.AnsweredMsg", msg)
	}
	if ans.Answer != confirm.AnswerNo {
		t.Fatalf("a bare Enter answered %s, want no", ans.Answer)
	}

	scr, _ = scr.Update(ans, ctx)
	got := mustModel(t, scr)
	if got.stage != stageKillInput {
		t.Errorf("stage after a No answer = %v, want stageKillInput", got.stage)
	}
	if killCalls != 0 {
		t.Errorf("killFn was called %d times on a No answer, want 0", killCalls)
	}
}
