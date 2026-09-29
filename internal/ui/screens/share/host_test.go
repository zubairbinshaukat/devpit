package share

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/pathpicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// newHost returns a harness on the sharing screen over a fake engine, with
// the folder already chosen.
func newHostHarness(t *testing.T, f *fakeHoster, copied *[]string) *harness {
	t.Helper()
	scr := newHostScreen(testDeps(f, &fakeReceiver{}, copied), config.Default())
	return newHarness(t, scr)
}

func chosen(path string) tea.Msg { return pathpicker.ChosenMsg{Path: path} }

func TestSharingHappyPathShowsTheCardAndStopsCleanly(t *testing.T) {
	f := newFakeHoster()
	var copied []string
	h := newHostHarness(t, f, &copied)

	if !strings.Contains(h.view(), "Which folder do you want to share?") {
		t.Fatalf("the picker is not asking about sharing:\n%s", h.view())
	}
	h.send(chosen(`D:\Games`))
	hs := h.scr.(hostScreen)
	if hs.stage != hostCard {
		t.Fatalf("stage = %d, want the card\n%s", hs.stage, h.view())
	}
	if len(f.started) != 1 || f.started[0].Path != `D:\Games` || f.started[0].MakePrivate {
		t.Errorf("started = %+v", f.started)
	}
	view := h.view()
	for _, want := range []string{"192.168.1.5", "Games-x7k2", "devpit-ab12", "Xk3mPq9RtVw2NbLc", "net use", "robocopy", `D:\Games`, "read-only"} {
		if !strings.Contains(view, want) {
			t.Errorf("the card lacks %q:\n%s", want, view)
		}
	}
	if !uictx.Busy(h.scr) {
		t.Error("a screen that is sharing is not busy: Esc could leave it running")
	}

	h.send(text("c"), text("r"), text("p"))
	want := []string{f.card.NetUse, f.card.Robocopy, f.card.Password}
	if strings.Join(copied, "|") != strings.Join(want, "|") {
		t.Errorf("copied = %q", copied)
	}

	h.send(text("s"))
	if h.scr.(hostScreen).stage != hostConfirmStop {
		t.Fatal("s did not ask before stopping")
	}
	h.send(text("y"))
	if h.scr.(hostScreen).stage != hostStopped || f.stops != 1 {
		t.Fatalf("stage %d after stopping, stops %d\n%s", h.scr.(hostScreen).stage, f.stops, h.view())
	}
	if uictx.Busy(h.scr) {
		t.Error("still busy after stopping")
	}
	h.send(keyCode(tea.KeyEnter))
	if h.popped != 1 {
		t.Error("Enter did not go back")
	}
}

func TestEscOnTheCardAsksBeforeStoppingAndNoKeepsSharing(t *testing.T) {
	f := newFakeHoster()
	h := newHostHarness(t, f, new([]string))
	h.send(chosen(`D:\Games`), keyCode(tea.KeyEscape))
	if h.scr.(hostScreen).stage != hostConfirmStop {
		t.Fatal("Esc did not ask")
	}
	h.send(text("n"))
	if h.scr.(hostScreen).stage != hostCard || f.stops != 0 {
		t.Errorf("stage %d, stops %d; No must keep sharing", h.scr.(hostScreen).stage, f.stops)
	}
}

func TestAPublicNetworkAsksAndNoLeaves(t *testing.T) {
	f := newFakeHoster()
	f.cat = host.CategoryPublic
	h := newHostHarness(t, f, new([]string))
	h.send(chosen(`D:\Games`))
	if h.scr.(hostScreen).stage != hostPrivate || len(f.started) != 0 {
		t.Fatalf("stage %d, started %d; want the question and nothing started", h.scr.(hostScreen).stage, len(f.started))
	}
	if v := h.view(); !strings.Contains(v, "Switch this network to Private") || !strings.Contains(v, "puts it back") {
		t.Errorf("question:\n%s", v)
	}
	h.send(text("n"))
	if h.popped != 1 || len(f.started) != 0 {
		t.Errorf("popped %d, started %d", h.popped, len(f.started))
	}
}

func TestAPublicNetworkYesSwitchesAndSaysSo(t *testing.T) {
	f := newFakeHoster()
	f.cat = host.CategoryPublic
	h := newHostHarness(t, f, new([]string))
	h.send(chosen(`D:\Games`), text("y"))
	if len(f.started) != 1 || !f.started[0].MakePrivate {
		t.Fatalf("started = %+v", f.started)
	}
	if v := h.view(); !strings.Contains(v, "switched to Private") {
		t.Errorf("the card does not mention the switch:\n%s", v)
	}
}

func TestSeveralAdaptersAskWhichAndUseTheChosenOne(t *testing.T) {
	f := newFakeHoster()
	f.adapters = []host.Adapter{
		{Name: "Wi-Fi", Index: 12, IP: net.ParseIP("192.168.1.20"), Best: true},
		{Name: "Ethernet", Index: 4, IP: net.ParseIP("10.0.0.5")},
	}
	h := newHostHarness(t, f, new([]string))
	h.send(chosen(`D:\Games`))
	if h.scr.(hostScreen).stage != hostAdapters {
		t.Fatalf("stage = %d, want the adapter list\n%s", h.scr.(hostScreen).stage, h.view())
	}
	v := h.view()
	if !strings.Contains(v, "192.168.1.20") || !strings.Contains(v, "10.0.0.5") || !strings.Contains(v, "internet") {
		t.Errorf("list:\n%s", v)
	}
	h.send(keyCode(tea.KeyDown), keyCode(tea.KeyEnter))
	if len(f.started) != 1 || f.started[0].Adapter.Name != "Ethernet" {
		t.Errorf("started = %+v, want the second adapter", f.started)
	}
	// The card shows the other addresses too.
	if v := h.view(); !strings.Contains(v, "192.168.1.20") {
		t.Errorf("the card does not list the other address:\n%s", v)
	}
}

func TestNoNetworkConnectionIsExplained(t *testing.T) {
	f := newFakeHoster()
	f.adapters = nil
	h := newHostHarness(t, f, new([]string))
	h.send(chosen(`D:\Games`))
	if v := h.view(); !strings.Contains(v, "no network connection") {
		t.Errorf("view:\n%s", v)
	}
}

func TestSetupFailuresAreExplained(t *testing.T) {
	f := newFakeHoster()
	f.startErr = &elevate.DeclinedError{}
	h := newHostHarness(t, f, new([]string))
	h.send(chosen(`D:\Games`))
	if v := h.view(); !strings.Contains(v, "You said no to the admin prompt") || !strings.Contains(v, "Nothing was changed") {
		t.Errorf("declined:\n%s", v)
	}
	if uictx.Busy(h.scr) {
		t.Error("busy after a failed start")
	}

	f2 := newFakeHoster()
	f2.startErr = errors.New("sharing the folder: access denied")
	h2 := newHostHarness(t, f2, new([]string))
	h2.send(chosen(`D:\Games`))
	if v := h2.view(); !strings.Contains(v, "Could not start sharing") || !strings.Contains(v, "access denied") {
		t.Errorf("failed:\n%s", v)
	}
	h2.send(keyCode(tea.KeyEnter))
	if h2.popped != 1 {
		t.Error("Enter did not go back")
	}
}

func TestAStopThatCannotFinishOffersARetry(t *testing.T) {
	f := newFakeHoster()
	var copied []string
	h := newHostHarness(t, f, &copied)
	h.send(chosen(`D:\Games`), text("s"))
	f.stopErr = errors.New("removing the account: access denied")
	h.send(text("y"))
	hs := h.scr.(hostScreen)
	if hs.stage != hostFailed || hs.stopErr == nil {
		t.Fatalf("stage %d\n%s", hs.stage, h.view())
	}
	if v := h.view(); !strings.Contains(v, "Not everything could be removed") || !strings.Contains(strings.Join(strings.Fields(v), " "), "finish the cleanup") {
		t.Errorf("view:\n%s", v)
	}
	f.stopErr = nil
	h.send(keyCode(tea.KeyEnter))
	if h.scr.(hostScreen).stage != hostStopped || f.stops != 2 {
		t.Errorf("stage %d, stops %d after the retry", h.scr.(hostScreen).stage, f.stops)
	}
}

func TestCtrlCStopsTheShareWithoutBlocking(t *testing.T) {
	f := newFakeHoster()
	h := newHostHarness(t, f, new([]string))
	h.send(chosen(`D:\Games`))
	done := make(chan struct{})
	go func() {
		uictx.Stop(h.scr)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop blocked the caller")
	}
	select {
	case <-f.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not take the share down")
	}
	// Busy follows the engine: it goes false once the share is gone.
	deadline := time.Now().Add(time.Second)
	for uictx.Busy(h.scr) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if uictx.Busy(h.scr) {
		t.Error("still busy after the share was stopped")
	}
}

func TestTheSharingScreenNeverShowsItsPasswordBeforeItExists(t *testing.T) {
	f := newFakeHoster()
	h := newHostHarness(t, f, new([]string))
	if strings.Contains(h.view(), f.card.Password) {
		t.Error("the password is on the picker")
	}
}

func TestSetupProgressListsTheStepsAsTheyHappen(t *testing.T) {
	f := newFakeHoster()
	h := newHostHarness(t, f, new([]string))
	scr := h.scr.(hostScreen)
	scr.stage = hostSetup
	scr.steps = []host.Step{{Key: "firewall"}, {Key: "login"}}
	scr.stepAt = 1
	v := strip(scr.View(h.ctx))
	for _, want := range []string{"Allow sharing in the firewall", "Create a temporary login", "Share the folder read-only"} {
		if !strings.Contains(v, want) {
			t.Errorf("setup view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Switch the network to Private") {
		t.Error("the network step is listed although the network was not switched")
	}
}
