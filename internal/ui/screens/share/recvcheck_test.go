package share

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// checkRecv is a receiver that can also check the other PC's address and
// free a clashing one, as *recv.Session does.
type checkRecv struct {
	*fakeReceiver
	check  recv.HostCheck // the answer until the address is freed
	freed  bool
	frees  int
	checks int
}

func (c *checkRecv) CheckHost(_ context.Context, host string) (recv.HostCheck, error) {
	c.checks++
	if c.freed {
		return recv.HostCheck{Host: host, Reachable: true, Via: "Wi-Fi"}, nil
	}
	hc := c.check
	hc.Host = host
	return hc, nil
}

func (c *checkRecv) FreeAddress(context.Context, string) error {
	c.frees++
	c.freed = true
	return nil
}

// clashRecv is a hotspot receiver that still holds 192.168.137.1 on its own
// Wi-Fi Direct adapter, as in the first real test.
func clashRecv() *checkRecv {
	return &checkRecv{
		fakeReceiver: hotspotRecv(),
		check: recv.HostCheck{
			Local: true, Fixable: true, Holders: []string{"Local Area Connection* 10"}, Via: "Local Area Connection* 10",
		},
	}
}

func newCheckHarness(t *testing.T, c *checkRecv) *harness {
	t.Helper()
	deps := testDeps(newFakeHoster(), c.fakeReceiver, new([]string))
	deps.NewRecv = func() Receiver { return c }
	return newHarness(t, newRecvScreen(deps, config.Default(), nil)).init()
}

// joinSaved joins the saved "Home Wi-Fi" network from the hotspot list.
func joinSaved(h *harness) *harness { return h.send(down(), enter(), down(), down(), enter()) }

func recvCheckStates() []goldenState {
	return []goldenState{
		{"recv_check_ask", func(t *testing.T) uictx.Screen { return joinSaved(newCheckHarness(t, clashRecv())).scr }},
		{"recv_check_clash", func(t *testing.T) uictx.Screen {
			return joinSaved(newCheckHarness(t, clashRecv())).send(enter()).scr
		}},
		{"recv_check_ready", func(t *testing.T) uictx.Screen {
			c := clashRecv()
			c.check = recv.HostCheck{Reachable: true, Via: "Wi-Fi"}
			return joinSaved(newCheckHarness(t, c)).send(enter()).scr
		}},
		{"recv_copy_estimated", func(t *testing.T) uictx.Screen {
			s := asRecv(recvIn(t, newRecvFake()))
			s.stage, s.host = recvCopy, "192.168.1.178"
			s.ev = recv.Event{Phase: recv.PhaseCopying, TotalBytes: 56 << 30, DoneBytes: 26 << 30, TotalFiles: 8, Estimated: true, Elapsed: 62 * time.Minute}
			return s
		}},
		{"recv_check_no_answer", func(t *testing.T) uictx.Screen {
			c := clashRecv()
			c.check = recv.HostCheck{Via: "Wi-Fi"}
			return joinSaved(newCheckHarness(t, c)).send(enter()).scr
		}},
	}
}

// TestAClashIsFoundAndFixedWithOneKey is the first real test's bug: the
// receiver holds the hotspot's address itself. f frees it, Devpit checks
// again, and Enter goes on to the folders.
func TestAClashIsFoundAndFixedWithOneKey(t *testing.T) {
	c := clashRecv()
	h := joinSaved(newCheckHarness(t, c))
	if asRecv(h).stage != recvConnCheck {
		t.Fatalf("stage = %v, want the check question after joining", asRecv(h).stage)
	}
	h.send(enter())
	if s := asRecv(h); !s.hc.Local || !strings.Contains(h.view(), "talking to itself") {
		t.Fatalf("the clash is not shown:\n%s", h.view())
	}
	h.send(text("f"))
	if c.frees != 1 || !asRecv(h).checkReady() {
		t.Fatalf("frees = %d, ready = %v; want one fix and a passing check", c.frees, asRecv(h).checkReady())
	}
	h.send(enter())
	if asRecv(h).stage != recvShares {
		t.Fatalf("stage = %v, want the shared folders", asRecv(h).stage)
	}
}

// TestATypedAddressThatIsThisPCIsCaught: a PC joined by hand from the
// Windows Wi-Fi list types the address and is told, instead of seeing its
// own empty share list.
func TestATypedAddressThatIsThisPCIsCaught(t *testing.T) {
	c := clashRecv()
	h := newCheckHarness(t, c).send(enter()).typed("192.168.137.1").send(enter())
	if s := asRecv(h); s.stage != recvConnCheck || !s.hc.Local {
		t.Fatalf("stage = %v, local = %v; want the clash", s.stage, s.hc.Local)
	}
	if c.called("list") != 0 {
		t.Fatalf("calls = %v, want no listing of this PC itself", c.calls)
	}
}

// TestAHealthyTypedAddressListsAtOnce: the check adds no step when all is
// well.
func TestAHealthyTypedAddressListsAtOnce(t *testing.T) {
	c := clashRecv()
	c.check = recv.HostCheck{Reachable: true}
	h := newCheckHarness(t, c).send(enter()).typed("192.168.1.5").send(enter())
	if asRecv(h).stage != recvShares {
		t.Fatalf("stage = %v, want the shared folders", asRecv(h).stage)
	}
}

// TestAClashOnAnAdapterDevpitMustNotChangeGoesBack: no fix key, and Enter
// puts the PC back on its own Wi-Fi.
func TestAClashOnAnAdapterDevpitMustNotChangeGoesBack(t *testing.T) {
	c := clashRecv()
	c.check.Fixable = false
	h := joinSaved(newCheckHarness(t, c)).send(enter(), text("f"))
	if c.frees != 0 {
		t.Fatal("f freed an address Devpit must not touch")
	}
	h.send(enter())
	if c.hs.leaves != 1 {
		t.Fatalf("leaves = %d, want the PC back on its Wi-Fi", c.hs.leaves)
	}
}

// TestAClashThatStopsACopyOffersTheFixAndCarriesOn: the run stops when this
// PC takes the hotspot's address again; f frees it and Enter carries on.
func TestAClashThatStopsACopyOffersTheFixAndCarriesOn(t *testing.T) {
	c := clashRecv()
	c.outcome = recv.Outcome{Paused: true, Check: recv.HostCheck{Host: "192.168.137.1", Local: true, Fixable: true}}
	h := newCheckHarness(t, c)
	s := asRecv(h)
	s.job = job.Job{Host: "192.168.137.1", Share: "Games", Dest: `D:\Games`, Mode: job.ModeHotspot, SSID: "OMEN-Hotspot"}
	h.scr = s
	next, cmd := asRecv(h).beginCopy(s.job)
	h.scr = next
	h.settle(cmd)
	if st := asRecv(h); st.stage != recvConnCheck || !st.hc.Local {
		t.Fatalf("stage = %v, want the clash after the copy stopped", st.stage)
	}
	c.outcome = recv.Outcome{Done: true}
	h.send(text("f"), enter())
	if c.frees != 1 || c.called("run ") != 2 {
		t.Fatalf("frees = %d, calls = %v; want one fix and the copy run again", c.frees, c.calls)
	}
}

// TestAResumedHotspotCopyIsCheckedToo: the clash can come back between two
// runs, so Resume checks after rejoining.
func TestAResumedHotspotCopyIsCheckedToo(t *testing.T) {
	c := clashRecv()
	c.hs.resume = recv.ResumeHotspot{Rejoin: true, SSID: "OMEN-Hotspot", Profile: "OMEN-Hotspot"}
	saved := job.Job{Host: "192.168.137.1", Share: "Games", User: "bob", Dest: `D:\Games`, Mode: job.ModeHotspot, SSID: "OMEN-Hotspot", State: job.StatePaused}
	deps := testDeps(newFakeHoster(), c.fakeReceiver, new([]string))
	deps.NewRecv = func() Receiver { return c }
	h := newHarness(t, newRecvScreen(deps, config.Default(), &saved)).init().send(enter(), enter())
	if s := asRecv(h); s.stage != recvConnCheck || !s.hc.Local {
		t.Fatalf("stage = %v, want the clash before resuming", s.stage)
	}
	if c.called("preflight") != 0 {
		t.Fatal("measured again before the check passed")
	}
}
