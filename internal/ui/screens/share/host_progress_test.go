package share

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
)

// fakeProgressHoster is a fakeHoster that can also say how much was sent.
type fakeProgressHoster struct {
	*fakeHoster
	pmu sync.Mutex
	p   host.Progress
}

func (f *fakeProgressHoster) Progress() host.Progress {
	f.pmu.Lock()
	defer f.pmu.Unlock()
	return f.p
}

// newProgressHarness opens the sharing screen over an engine that reports
// progress and runs it to the card.
func newProgressHarness(t *testing.T, f *fakeProgressHoster) *harness {
	t.Helper()
	deps := Deps{
		NewHost: func() Hoster { return f },
		NewRecv: func() Receiver { return &fakeReceiver{} },
		Copy:    func(string) tea.Cmd { return nil },
	}
	h := newHarness(t, newHostScreen(deps, config.Default()))
	h.send(chosen(`D:\Games`))
	if h.scr.(hostScreen).stage == hostAdapters {
		h.send(keyCode(tea.KeyEnter)) // the first connection
	}
	if h.scr.(hostScreen).stage != hostCard {
		t.Fatalf("not on the card:\n%s", h.view())
	}
	return h
}

// roomy gives the screen a taller terminal, where the whole block fits.
func roomy(h *harness) *harness {
	h.ctx = testCtx(100, 30, icons.TierUnicode)
	return h
}

func sending(sent uint64, total int64) host.Progress {
	return host.Progress{
		Source: host.SourceAdapter, Sent: sent, Rate: 95 << 20, Flowing: true,
		Active: 320 * time.Second, Total: total, TotalKnown: total > 0,
	}
}

// reading feeds the screen one reading the way the poll would.
func reading(h *harness, p host.Progress) {
	h.t.Helper()
	gen := h.scr.(hostScreen).pollGen
	h.send(trafficMsg{gen: gen, p: p})
}

func TestNoProgressBlockUntilBytesHaveMoved(t *testing.T) {
	plain := newHostHarness(t, newFakeHoster(), new([]string))
	plain.send(chosen(`D:\Games`))
	want := plain.view()

	h := newProgressHarness(t, &fakeProgressHoster{fakeHoster: newFakeHoster()})
	reading(h, host.Progress{Source: host.SourceAdapter})
	if got := h.view(); got != want {
		t.Errorf("a quiet share must look exactly as before.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

func TestAHosterWithoutProgressNeverPolls(t *testing.T) {
	h := newHostHarness(t, newFakeHoster(), new([]string))
	h.send(chosen(`D:\Games`))
	hs := h.scr.(hostScreen)
	if hs.pollCmd() != nil {
		t.Error("an engine that cannot report progress must not be polled")
	}
	if hs.TerminalProgress() != nil {
		t.Error("no progress, so no taskbar bar")
	}
}

func TestTheBlockShowsSentTotalSpeedAndTimeWithTheBar(t *testing.T) {
	h := roomy(newProgressHarness(t, &fakeProgressHoster{fakeHoster: newFakeHoster()}))
	reading(h, sending(12<<30, 32<<30))
	v := h.view()
	for _, want := range []string{"Sending", "about 12.0 GB of 32.0 GB", "38%", "5m 20s", "MB/s", "Counts everything this PC sends on Ethernet"} {
		if !strings.Contains(v, want) {
			t.Errorf("the block lacks %q:\n%s", want, v)
		}
	}
	if !strings.Contains(v, "Xk3mPq9RtVw2NbLc") {
		t.Error("the card must still show the password above the block")
	}
}

func TestWhileTheFolderIsStillBeingMeasuredThereIsNoBarOrPercent(t *testing.T) {
	h := roomy(newProgressHarness(t, &fakeProgressHoster{fakeHoster: newFakeHoster()}))
	reading(h, sending(3<<30, 0))
	v := h.view()
	if !strings.Contains(v, "so far") || !strings.Contains(v, "measuring folder size") {
		t.Errorf("measuring state is not explained:\n%s", v)
	}
	if strings.Contains(v, "%") {
		t.Errorf("no percentage before the total is known:\n%s", v)
	}
}

func TestAnIdleShareKeepsWhatWasSentAndNeverSaysDone(t *testing.T) {
	h := newProgressHarness(t, &fakeProgressHoster{fakeHoster: newFakeHoster()})
	p := sending(32<<30, 32<<30)
	p.Flowing, p.Rate, p.IdleFor = false, 0, 130*time.Second
	reading(h, p)
	v := h.view()
	if !strings.Contains(v, "Sent") || !strings.Contains(v, "idle for 2m 10s") {
		t.Errorf("idle state is wrong:\n%s", v)
	}
	for _, bad := range []string{"Done", "Complete", "Finished"} {
		if strings.Contains(v, bad) {
			t.Errorf("a share cannot know a copy is finished, but the frame says %q:\n%s", bad, v)
		}
	}
}

func TestAPartialFolderMeasureSaysAtLeast(t *testing.T) {
	h := newProgressHarness(t, &fakeProgressHoster{fakeHoster: newFakeHoster()})
	p := sending(1<<30, 8<<30)
	p.TotalPartial = true
	reading(h, p)
	if v := h.view(); !strings.Contains(v, "of at least") {
		t.Errorf("a partial total must say so:\n%s", v)
	}
}

func TestMoreSentThanTheTotalClampsTheBar(t *testing.T) {
	h := newProgressHarness(t, &fakeProgressHoster{fakeHoster: newFakeHoster()})
	reading(h, sending(20<<30, 10<<30)) // two receivers, or a re-copy
	if v := h.view(); !strings.Contains(v, "100%") || strings.Contains(v, "200%") {
		t.Errorf("the bar must stop at 100%%:\n%s", v)
	}
}

func TestTheBlockDegradesToOneLineWhenTheTerminalIsShort(t *testing.T) {
	f := &fakeProgressHoster{fakeHoster: newFakeHoster()}
	f.adapters = append(f.adapters, host.Adapter{Name: "Wi-Fi", Index: 7, IP: net.ParseIP("10.0.0.9")})
	h := newProgressHarness(t, f)
	reading(h, sending(12<<30, 32<<30))
	v := h.view()
	if !strings.Contains(v, "Sending") || !strings.Contains(v, "38%") {
		t.Errorf("even a tight screen shows the progress:\n%s", v)
	}
	if rows := strings.Count(v, "\n") + 1; rows > h.ctx.BodyHeight {
		t.Errorf("frame is %d rows, the body has %d", rows, h.ctx.BodyHeight)
	}
}

func TestTheBlockFitsTheAsciiTier(t *testing.T) {
	h := newProgressHarness(t, &fakeProgressHoster{fakeHoster: newFakeHoster()})
	h.ctx = testCtx(100, 30, icons.TierASCII)
	reading(h, sending(12<<30, 32<<30))
	for _, r := range h.view() {
		if r > 127 && r != '·' && r != '›' && r != '…' {
			t.Errorf("non-ascii %q in the ascii tier:\n%s", r, h.view())
			break
		}
	}
}

func TestStaleAndLateReadingsAreIgnored(t *testing.T) {
	f := &fakeProgressHoster{fakeHoster: newFakeHoster()}
	h := newProgressHarness(t, f)
	gen := h.scr.(hostScreen).pollGen

	h.send(trafficMsg{gen: gen - 1, p: sending(1<<30, 2<<30)})
	if h.scr.(hostScreen).prog.Visible() {
		t.Error("a reading from an older share was taken")
	}

	reading(h, sending(1<<30, 2<<30))
	if !h.scr.(hostScreen).prog.Visible() {
		t.Fatal("a current reading was dropped")
	}

	// Stop sharing: the chain must end and the block must go.
	h.send(text("s"), text("y"))
	if h.scr.(hostScreen).prog.Visible() {
		t.Error("the old reading survived the stop")
	}
	h.send(trafficMsg{gen: gen, p: sending(9<<30, 9<<30)})
	if h.scr.(hostScreen).prog.Visible() {
		t.Error("a late reading revived the block after stopping")
	}
	h.send(trafficTickMsg{gen: gen})
	if h.scr.(hostScreen).prog.Visible() {
		t.Error("a late tick revived the block after stopping")
	}
}

func TestOnlyTheNewestReadingIsKept(t *testing.T) {
	h := newProgressHarness(t, &fakeProgressHoster{fakeHoster: newFakeHoster()})
	reading(h, sending(1<<30, 8<<30))
	reading(h, sending(2<<30, 8<<30))
	if got := h.scr.(hostScreen).prog.Sent; got != 2<<30 {
		t.Errorf("Sent = %d, want the newest", got)
	}
}

func TestProgressKeepsPollingWhileTheStopQuestionIsOpen(t *testing.T) {
	h := newProgressHarness(t, &fakeProgressHoster{fakeHoster: newFakeHoster()})
	h.send(text("s"))
	if h.scr.(hostScreen).stage != hostConfirmStop {
		t.Fatal("s did not ask")
	}
	reading(h, sending(1<<30, 8<<30))
	if !h.scr.(hostScreen).prog.Visible() {
		t.Error("readings stopped while the stop question was open")
	}
	h.send(text("n"))
	if h.scr.(hostScreen).stage != hostCard || !h.scr.(hostScreen).prog.Visible() {
		t.Error("answering No must keep sharing and keep the block")
	}
}

func TestProgressDoesNotChangeBusy(t *testing.T) {
	f := &fakeProgressHoster{fakeHoster: newFakeHoster()}
	h := newProgressHarness(t, f)
	before := h.scr.(hostScreen).Busy()
	reading(h, sending(1<<30, 8<<30))
	if h.scr.(hostScreen).Busy() != before || !before {
		t.Error("progress must not change whether leaving the screen is allowed")
	}
}

func TestTheTaskbarBarFollowsOnlyAFlowingSendWithAKnownTotalAndNeverHits100(t *testing.T) {
	h := newProgressHarness(t, &fakeProgressHoster{fakeHoster: newFakeHoster()})
	tp := func() *tea.ProgressBar { return h.scr.(hostScreen).TerminalProgress() }

	if tp() != nil {
		t.Error("a bar before anything was sent")
	}
	reading(h, sending(1<<30, 0))
	if tp() != nil {
		t.Error("a bar with an unknown total")
	}
	reading(h, sending(4<<30, 8<<30))
	if b := tp(); b == nil || b.Value != 50 {
		t.Errorf("flowing at half: %+v", b)
	}
	reading(h, sending(30<<30, 8<<30))
	if b := tp(); b == nil || b.Value != 99 {
		t.Errorf("over the total must stay at 99, got %+v", b)
	}
	idle := sending(4<<30, 8<<30)
	idle.Flowing = false
	reading(h, idle)
	if tp() != nil {
		t.Error("an idle share must not show a taskbar bar")
	}
}

func TestNoProgressFrameContainsAnythingButTheCardsOwnPassword(t *testing.T) {
	h := newProgressHarness(t, &fakeProgressHoster{fakeHoster: newFakeHoster()})
	reading(h, sending(12<<30, 32<<30))
	v := h.view()
	if strings.Count(v, "Xk3mPq9RtVw2NbLc") != 2 { // the Password row and the net use line
		t.Errorf("the password should appear exactly where the card puts it:\n%s", v)
	}
}
