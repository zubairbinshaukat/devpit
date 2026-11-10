package share

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// hotspotFake is the scripted hotspot side of fakeReceiver. With on false the
// receiver says it cannot join, which keeps every older test on the address
// field.
type hotspotFake struct {
	on       bool
	nets     []winapi.WifiNetwork
	scanErr  error
	res      recv.JoinResult
	joinErr  error
	needPass bool // the first join without a password asks for one
	resume   recv.ResumeHotspot

	joins  []recv.JoinRequest
	leaves int
}

func (f *fakeReceiver) CanJoin() bool { return f.hs.on }

func (f *fakeReceiver) FindHotspots() ([]winapi.WifiNetwork, error) {
	f.note("scan")
	return f.hs.nets, f.hs.scanErr
}

func (f *fakeReceiver) Join(_ context.Context, req recv.JoinRequest) (recv.JoinResult, error) {
	f.note("join " + req.SSID)
	f.mu.Lock()
	f.hs.joins = append(f.hs.joins, req)
	f.mu.Unlock()
	if f.hs.needPass && req.Password == "" {
		return recv.JoinResult{}, recv.ErrNeedPassword
	}
	return f.hs.res, f.hs.joinErr
}

func (f *fakeReceiver) Leave() (recv.LeaveResult, error) {
	f.note("leave")
	f.mu.Lock()
	f.hs.leaves++
	f.mu.Unlock()
	return recv.LeaveResult{Left: true, Reconnected: true}, nil
}

func (f *fakeReceiver) ResumeNeeds(job.Job) (recv.ResumeHotspot, error) { return f.hs.resume, nil }

// hotspotRecv is a receiver on a PC that can join, with a saved-name hotspot
// and a Devpit one nearby.
func hotspotRecv() *fakeReceiver {
	r := newRecvFake()
	r.hs = hotspotFake{
		on: true,
		nets: []winapi.WifiNetwork{
			{SSID: "Devpit-x7k2", Signal: 92, Secured: true},
			{SSID: "OMEN-Hotspot", Signal: 80, Secured: true},
			{SSID: "Home Wi-Fi", Signal: 64, Secured: true, HasProfile: true, Profile: "Home Wi-Fi"},
		},
		res: recv.JoinResult{
			Host: "192.168.137.1",
			Verdict: recv.LinkVerdict{
				Before: recv.LinkInfo{OnWifi: true, RxKbps: 866000, Standard: "Wi-Fi 5"},
				After:  recv.LinkInfo{OnWifi: true, RxKbps: 1201000, Standard: "Wi-Fi 6"},
			},
		},
	}
	return r
}

// slowHotspot makes the joined link worse than the one before.
func slowHotspot(r *fakeReceiver) {
	r.hs.res.Verdict = recv.LinkVerdict{
		Worse:  true,
		Before: recv.LinkInfo{OnWifi: true, RxKbps: 1201000, Standard: "Wi-Fi 6"},
		After:  recv.LinkInfo{OnWifi: true, RxKbps: 286000, Standard: "Wi-Fi 6"},
		Reason: "The direct link is slower than your Wi-Fi.",
	}
}

func down() tea.Msg { return keyCode(tea.KeyDown) }

func recvHotspotStates() []goldenState {
	return []goldenState{
		{"recv_way", func(t *testing.T) uictx.Screen { return recvIn(t, hotspotRecv()).scr }},
		{"recv_hotspot_scanning", func(t *testing.T) uictx.Screen {
			s := asRecv(recvIn(t, hotspotRecv()))
			s.stage = recvScanning
			return s
		}},
		{"recv_hotspot_list", func(t *testing.T) uictx.Screen {
			return recvIn(t, hotspotRecv(), down(), enter()).scr
		}},
		{"recv_hotspot_password", func(t *testing.T) uictx.Screen {
			return recvIn(t, hotspotRecv(), down(), enter(), down(), enter()).typed("12345678").scr
		}},
		{"recv_hotspot_joining", func(t *testing.T) uictx.Screen {
			s := asRecv(recvIn(t, hotspotRecv()))
			s.stage, s.ssid = recvJoining, "OMEN-Hotspot"
			return s
		}},
		{"recv_hotspot_slow_link", func(t *testing.T) uictx.Screen {
			r := hotspotRecv()
			slowHotspot(r)
			return recvIn(t, r, down(), enter(), down(), down(), enter()).scr
		}},
		{"recv_hotspot_back_on_wifi", func(t *testing.T) uictx.Screen {
			r := hotspotRecv()
			slowHotspot(r)
			return recvIn(t, r, down(), enter(), down(), down(), enter(), text("b")).scr
		}},
		{"recv_hotspot_type_name", func(t *testing.T) uictx.Screen {
			r := hotspotRecv()
			r.hs.scanErr = recv.ErrScanDenied
			return recvIn(t, r, down(), enter()).scr
		}},
		{"recv_hotspot_shares", func(t *testing.T) uictx.Screen {
			return recvIn(t, hotspotRecv(), down(), enter(), enter()).typed("12345678").send(enter()).scr
		}},
	}
}

// TestAPCThatCannotJoinGoesStraightToTheAddress keeps the old flow for a PC
// with no Wi-Fi card: no question first.
func TestAPCThatCannotJoinGoesStraightToTheAddress(t *testing.T) {
	if s := asRecv(recvIn(t, newRecvFake())); s.stage != recvIP {
		t.Fatalf("stage = %v, want the address field", s.stage)
	}
}

// TestADevpitHotspotPrefillsTheUserAndListsAtTheHotspotAddress walks the
// shortest hotspot path: pick the Devpit name, type its password, and land on
// the sharing PC's folders with the user name already known.
func TestADevpitHotspotPrefillsTheUserAndListsAtTheHotspotAddress(t *testing.T) {
	r := hotspotRecv()
	h := recvIn(t, r, down(), enter(), enter()).typed("12345678").send(enter())
	s := asRecv(h)
	if s.stage != recvShares {
		t.Fatalf("stage = %v, want the shared folders", s.stage)
	}
	if got := s.user.Value(); got != "devpit-x7k2" {
		t.Errorf("user = %q, want devpit-x7k2", got)
	}
	if r.called("list 192.168.137.1") != 1 {
		t.Errorf("calls = %v, want a listing at the hotspot address", r.calls)
	}
	if n := len(r.hs.joins); n != 1 || r.hs.joins[0].Password != "12345678" {
		t.Errorf("joins = %+v, want one with the typed password", r.hs.joins)
	}
}

// TestASavedNetworkNeedsNoPassword joins a network this PC knows without
// asking for anything.
func TestASavedNetworkNeedsNoPassword(t *testing.T) {
	r := hotspotRecv()
	s := asRecv(recvIn(t, r, down(), enter(), down(), down(), enter()))
	if s.stage != recvShares {
		t.Fatalf("stage = %v, want the shared folders", s.stage)
	}
	if len(r.hs.joins) != 1 || r.hs.joins[0].Password != "" || r.hs.joins[0].Profile != "Home Wi-Fi" {
		t.Errorf("joins = %+v, want one with the saved profile and no password", r.hs.joins)
	}
}

// TestTheHotspotPasswordFieldIsEmptiedAfterJoining guards rule 28 for the new
// field: what was typed is not kept.
func TestTheHotspotPasswordFieldIsEmptiedAfterJoining(t *testing.T) {
	s := asRecv(recvIn(t, hotspotRecv(), down(), enter(), enter()).typed("12345678").send(enter()))
	if v := s.hsPass.Value(); v != "" {
		t.Errorf("hotspot password field still holds %q", v)
	}
}

// TestASlowerHotspotOffersToGoBackAndDoesSo goes back to the PC's own Wi-Fi
// on b, and asks for the normal address.
func TestASlowerHotspotOffersToGoBackAndDoesSo(t *testing.T) {
	r := hotspotRecv()
	slowHotspot(r)
	h := recvIn(t, r, down(), enter(), down(), down(), enter())
	if asRecv(h).stage != recvSlowLink {
		t.Fatalf("stage = %v, want the slower-link notice", asRecv(h).stage)
	}
	s := asRecv(h.send(text("b")))
	if s.stage != recvIP || r.hs.leaves != 1 {
		t.Errorf("stage = %v, leaves = %d; want the address field after one leave", s.stage, r.hs.leaves)
	}
	if s.joined {
		t.Error("the screen still thinks it is on the hotspot")
	}
}

// TestLeavingAnErrorAfterAJoinPutsTheWifiBack: Enter on an error after a join
// goes back to the PC's own network, never leaving it on the hotspot.
func TestLeavingAnErrorAfterAJoinPutsTheWifiBack(t *testing.T) {
	r := hotspotRecv()
	r.listErr = errors.New("that PC did not answer")
	h := recvIn(t, r, down(), enter(), down(), down(), enter())
	if asRecv(h).stage != recvError {
		t.Fatalf("stage = %v, want an error", asRecv(h).stage)
	}
	h.send(enter())
	if r.hs.leaves != 1 {
		t.Errorf("leaves = %d, want 1", r.hs.leaves)
	}
}

// TestResumeRejoinsTheHotspotFirst: a hotspot copy rejoins before it
// measures again, asking for the password only when it must.
func TestResumeRejoinsTheHotspotFirst(t *testing.T) {
	r := hotspotRecv()
	r.hs.resume = recv.ResumeHotspot{Rejoin: true, SSID: "OMEN-Hotspot", NeedPassword: true}
	saved := job.Job{Host: "192.168.137.1", Share: "Games", User: "bob", Dest: `D:\Games`, Mode: job.ModeHotspot, SSID: "OMEN-Hotspot", State: job.StatePaused}
	h := newRecvHarnessSaved(t, r, saved).send(enter())
	if asRecv(h).stage != recvHotspotPass {
		t.Fatalf("stage = %v, want the hotspot password", asRecv(h).stage)
	}
	h.typed("12345678").send(enter())
	if r.called("join OMEN-Hotspot") != 1 || r.called("preflight") != 1 {
		t.Errorf("calls = %v, want a join then a preflight", r.calls)
	}
}

// TestNoHotspotFramePromisesASpeed walks the hotspot frames for words that
// would promise a speed (rule 68).
func TestNoHotspotFramePromisesASpeed(t *testing.T) {
	for _, st := range recvHotspotStates() {
		frame := strings.ToLower(frameWithKeys(st.build(t), testCtx(80, 24, icons.TierUnicode)))
		for _, bad := range []string{"faster", "x faster", "up to", "×"} {
			if strings.Contains(frame, bad) {
				t.Errorf("%s says %q:\n%s", st.name, bad, frame)
			}
		}
	}
}

// newRecvHarnessSaved opens the receiving screen on a saved copy.
func newRecvHarnessSaved(t *testing.T, r *fakeReceiver, saved job.Job) *harness {
	t.Helper()
	scr := newRecvScreen(testDeps(newFakeHoster(), r, new([]string)), config.Default(), &saved)
	return newHarness(t, scr).init()
}

// TestTryingAFailedHotspotCopyAgainRejoinsFirst: r after a failed hotspot
// copy joins the hotspot again before copying, because the run put this PC
// back on its own Wi-Fi.
func TestTryingAFailedHotspotCopyAgainRejoinsFirst(t *testing.T) {
	r := hotspotRecv()
	r.hs.resume = recv.ResumeHotspot{Rejoin: true, SSID: "OMEN-Hotspot", Profile: "OMEN-Hotspot"}
	h := recvIn(t, r)
	s := asRecv(h)
	s.stage = recvFailed
	s.job = job.Job{Host: "192.168.137.1", Share: "Games", Dest: `D:\Games`, Mode: job.ModeHotspot, SSID: "OMEN-Hotspot"}
	h.scr = s
	h.send(text("r"))
	if r.called("join OMEN-Hotspot") != 1 || r.called("run ") != 1 {
		t.Fatalf("calls = %v, want a join before the run", r.calls)
	}
}

// TestGoingBackFromAResumedHotspotCopyForgetsItsHotspotRecord: b on a slow
// resumed hotspot copy starts afresh on the normal network instead of
// copying from the hotspot's address.
func TestGoingBackFromAResumedHotspotCopyForgetsItsHotspotRecord(t *testing.T) {
	r := hotspotRecv()
	slowHotspot(r)
	r.hs.resume = recv.ResumeHotspot{Rejoin: true, SSID: "OMEN-Hotspot", Profile: "OMEN-Hotspot"}
	saved := job.Job{Host: "192.168.137.1", Share: "Games", User: "bob", Dest: `D:\Games`, Mode: job.ModeHotspot, SSID: "OMEN-Hotspot", State: job.StatePaused}
	h := newRecvHarnessSaved(t, r, saved).send(enter())
	if asRecv(h).stage != recvSlowLink {
		t.Fatalf("stage = %v, want the slower-link notice", asRecv(h).stage)
	}
	s := asRecv(h.send(text("b")))
	if s.stage != recvIP || s.job.Hotspot() || s.resumeJoin {
		t.Fatalf("stage = %v, job = %+v, resumeJoin = %v; want a fresh start at the address", s.stage, s.job, s.resumeJoin)
	}
}

// TestARefusedScanAsksForTheNameAndJoinsIt: with location access off the
// name is typed, then joined like a picked one.
func TestARefusedScanAsksForTheNameAndJoinsIt(t *testing.T) {
	r := hotspotRecv()
	r.hs.scanErr = recv.ErrScanDenied
	h := recvIn(t, r, down(), enter())
	if asRecv(h).stage != recvHotspotName {
		t.Fatalf("stage = %v, want the name field", asRecv(h).stage)
	}
	h.typed("Devpit-x7k2").send(enter())
	if r.called("join Devpit-x7k2") != 1 {
		t.Fatalf("calls = %v, want a join of the typed name", r.calls)
	}
}
