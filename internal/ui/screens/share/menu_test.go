package share

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

func TestMenuShowsTheTwoActionsAndOpensTheirScreens(t *testing.T) {
	deps := testDeps(newFakeHoster(), newRecvFake(), new([]string))
	h := newHarness(t, NewWith(deps)).init()
	v := h.view()
	for _, want := range []string{"Share a folder", "Copy from a shared folder", "nothing goes to the internet"} {
		if !strings.Contains(strings.ToLower(v), strings.ToLower(want)) {
			t.Errorf("menu lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Resume") || strings.Contains(v, "Clean up") {
		t.Error("the menu offers a resume or a cleanup that does not exist")
	}
	h.send(keyCode(tea.KeyEnter))
	if len(h.pushed) != 1 {
		t.Fatalf("pushed %d screens", len(h.pushed))
	}
	if _, ok := h.pushed[0].(hostScreen); !ok {
		t.Errorf("the first entry opened %T, want the sharing screen", h.pushed[0])
	}
	h.send(keyCode(tea.KeyDown), keyCode(tea.KeyEnter))
	if _, ok := h.pushed[1].(recvScreen); !ok {
		t.Errorf("the second entry opened %T, want the receiving screen", h.pushed[1])
	}
}

func TestMenuOffersResumeAndCleanupWhenThereIsSomething(t *testing.T) {
	saved := job.Job{Host: "192.168.1.5", Share: "Games", Dest: `D:\Games`, TotalBytes: 80 << 30, DoneBytes: 47 << 30, State: job.StatePaused}
	left := host.Manifest{PID: 1, User: "devpit-ab12", Path: `D:\Games`}
	deps := testDeps(newFakeHoster(), newRecvFake(), new([]string))
	deps.SavedJob = func() (job.Job, bool) { return saved, true }
	deps.Leftover = func() (host.Manifest, bool) { return left, true }
	h := newHarness(t, NewWith(deps)).init()

	v := h.view()
	if !strings.Contains(v, "Resume the copy from 192.168.1.5") || !strings.Contains(v, "47.0 GB of 80.0 GB copied") || !strings.Contains(v, "Clean up an old share") {
		t.Fatalf("menu:\n%s", v)
	}
	// Resume is first, because it is what a person coming back wants.
	h.send(keyCode(tea.KeyEnter))
	rs, ok := h.pushed[0].(recvScreen)
	if !ok || rs.stage != recvResumeAsk || rs.job.Host != "192.168.1.5" {
		t.Fatalf("resume opened %T %+v", h.pushed[0], h.pushed[0])
	}
	// Cleanup is last.
	h.send(keyCode(tea.KeyDown), keyCode(tea.KeyDown), keyCode(tea.KeyDown), keyCode(tea.KeyEnter))
	if _, ok := h.pushed[1].(cleanupScreen); !ok {
		t.Errorf("cleanup opened %T", h.pushed[1])
	}
}

func TestNothingIsReadFromDiskWhenTheMenuIsBuilt(t *testing.T) {
	called := false
	deps := Deps{Leftover: func() (host.Manifest, bool) { called = true; return host.Manifest{}, false }}
	m := NewWith(deps)
	_ = m.View(testCtx(80, 24, icons.TierUnicode))
	if called {
		t.Error("the leftover check ran before Init: the first frame must cost nothing")
	}
}

func TestCleanupAsksThenRemovesWithOneCall(t *testing.T) {
	var got host.Manifest
	calls := 0
	deps := Deps{CleanUp: func(_ context.Context, m host.Manifest) error { calls++; got = m; return nil }}
	man := host.Manifest{User: "devpit-ab12", Path: `D:\Games`}
	h := newHarness(t, newCleanupScreen(deps, man))
	if v := strings.Join(strings.Fields(h.view()), " "); !strings.Contains(v, "Remove the old share?") || !strings.Contains(v, `D:\Games`) || !strings.Contains(v, "Windows will") {
		t.Errorf("ask:\n%s", v)
	}
	if calls != 0 {
		t.Fatal("cleaned up before the user answered")
	}
	h.send(text("y"))
	if calls != 1 || got.User != "devpit-ab12" {
		t.Errorf("calls %d, manifest %+v", calls, got)
	}
	if v := h.view(); !strings.Contains(v, "The old share is gone") {
		t.Errorf("done:\n%s", v)
	}
	h.send(keyCode(tea.KeyEnter))
	if h.popped != 1 {
		t.Error("Enter did not go back")
	}
}

func TestCleanupNoDoesNothing(t *testing.T) {
	calls := 0
	deps := Deps{CleanUp: func(context.Context, host.Manifest) error { calls++; return nil }}
	h := newHarness(t, newCleanupScreen(deps, host.Manifest{}))
	h.send(text("n"))
	if calls != 0 || h.popped != 1 {
		t.Errorf("calls %d, popped %d", calls, h.popped)
	}
}

func TestCleanupDeclinedAndFailedAreExplained(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"declined": {&elevate.DeclinedError{}, "You said no to the admin prompt"},
		"failed":   {errors.New("removing the account: Access is denied"), "Could not remove everything"},
	} {
		t.Run(name, func(t *testing.T) {
			deps := Deps{CleanUp: func(context.Context, host.Manifest) error { return tc.err }}
			h := newHarness(t, newCleanupScreen(deps, host.Manifest{}))
			h.send(text("y"))
			v := h.view()
			if !strings.Contains(v, tc.want) || !strings.Contains(v, "offer again") {
				t.Errorf("view:\n%s", v)
			}
			if uictx.Busy(h.scr) {
				t.Error("busy after failing")
			}
		})
	}
}

func TestCleanupIsBusyWhileRunningAndStopCancelsIt(t *testing.T) {
	started := make(chan struct{})
	result := make(chan error, 1)
	deps := Deps{CleanUp: func(ctx context.Context, _ host.Manifest) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	s := newCleanupScreen(deps, host.Manifest{})
	next, cmd := s.Update(confirm.AnsweredMsg{ID: cleanupID, Answer: confirm.AnswerYes}, testCtx(80, 24, icons.TierUnicode))
	go func() { result <- cmd().(cleanupDoneMsg).err }()
	<-started
	if !uictx.Busy(next) {
		t.Error("a running cleanup is not busy")
	}
	uictx.Stop(next)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want the cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not cancel the cleanup")
	}
}
