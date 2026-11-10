package recv_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// fakeClock is the session's clock: Sleep moves it on at once, and onSleep
// sees the new time.
type fakeClock struct {
	mu      sync.Mutex
	t       time.Time
	onSleep func(now time.Time)
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) sleep(_ context.Context, d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	now, hook := c.t, c.onSleep
	c.mu.Unlock()
	if hook != nil {
		hook(now)
	}
}

func countCalls(w *fakeWifi, call string) int {
	n := 0
	for _, c := range w.log() {
		if c == call {
			n++
		}
	}
	return n
}

// droppedRun joins the hotspot on a fake clock, then the PC falls back to
// its home Wi-Fi and the other PC stops answering. The first robocopy run
// fails; the other PC answers again once the clock is back after the join
// (never when back is 0), or as soon as the PC is on the hotspot again.
func droppedRun(t *testing.T, w *fakeWifi, back time.Duration) (*rig, *fakeClock, job.Job) {
	t.Helper()
	errLine := "2026/09/29 10:15:02 ERROR 64 (0x00000040) Copying File D:\\Games\\c\n"
	r := newWifiRig(t, w,
		step{text: files("a", "b") + errLine, code: 9},
		step{text: files("c") + summaryOK, code: 1},
	)
	clk := &fakeClock{t: ownerStart}
	d := r.deps
	d.Now, d.Sleep = clk.now, clk.sleep
	r.s = recv.New(d)
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	t0 := clk.now()
	clk.mu.Lock()
	clk.onSleep = func(now time.Time) {
		if back > 0 && now.Sub(t0) >= back {
			r.reachable.Store(true)
		}
	}
	clk.mu.Unlock()
	w.mu.Lock()
	w.onto(homeWifi)
	w.mu.Unlock()
	r.reachable.Store(false)
	w.onConnect = func() { r.reachable.Store(true) }
	j := r.s.Start(hotspotIP, "Games", "devpit-x7k2", `D:\Games`, robocopyPlan(3000, 3))
	if !j.Hotspot() {
		t.Fatalf("job = %+v, want a hotspot job", j)
	}
	return r, clk, j
}

// A rejoin is tried only while the hotspot is in the nearby list: a connect
// to a hotspot that is not there would knock the PC off its own Wi-Fi.
func TestRejoinWaitsUntilTheHotspotIsVisible(t *testing.T) {
	w := newFakeWifi()
	w.setOutOfRange(hotspot, true)
	r, clk, j := droppedRun(t, w, 0)
	t0 := clk.now()
	var visibleAtConnect []bool
	inner := w.onConnect
	w.onConnect = func() {
		w.mu.Lock()
		visibleAtConnect = append(visibleAtConnect, !w.outOfRange[hotspot])
		w.mu.Unlock()
		inner()
	}
	prev := clk.onSleep
	clk.mu.Lock()
	clk.onSleep = func(now time.Time) {
		prev(now)
		if now.Sub(t0) >= 100*time.Second {
			w.setOutOfRange(hotspot, false) // the other PC's hotspot is back
		}
	}
	clk.mu.Unlock()
	out, err := r.s.Run(context.Background(), j, nil)
	if err != nil || !out.Done {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if n := countCalls(w, "connect "+hotspot); n != 1 || len(visibleAtConnect) != 1 || !visibleAtConnect[0] {
		t.Errorf("connects = %d, visible at each = %v; want one, once the hotspot was in range (calls %v)", n, visibleAtConnect, w.log())
	}

	// The nearby list cannot be read (location access off) while the PC is
	// on its own Wi-Fi: Devpit cannot tell, so it never knocks the PC off.
	w2 := newFakeWifi()
	w2.visibleErr = fmt.Errorf("%w: WlanGetAvailableNetworkList", winapi.ErrWifiDenied)
	r2, _, j2 := droppedRun(t, w2, 5*time.Minute)
	if out, err := r2.s.Run(context.Background(), j2, nil); err != nil || !out.Done {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if n := countCalls(w2, "connect "+hotspot); n != 0 {
		t.Errorf("rejoined %d times without knowing the hotspot was there: %v", n, w2.log())
	}

	// The same, but the card is on no network: there is nothing to knock
	// off, so it tries.
	w3 := newFakeWifi()
	w3.visibleErr = w2.visibleErr
	r3, _, j3 := droppedRun(t, w3, 5*time.Minute)
	w3.mu.Lock()
	w3.onto("")
	w3.mu.Unlock()
	if out, err := r3.s.Run(context.Background(), j3, nil); err != nil || !out.Done {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if n := countCalls(w3, "connect "+hotspot); n != 1 {
		t.Errorf("connects with the card off-line = %d, want 1: %v", n, w3.log())
	}
}

// The backoff ticks every few seconds; a rejoin is tried at most once per
// RejoinInterval of the session's clock.
func TestRejoinIsRateLimited(t *testing.T) {
	if recv.RejoinInterval != 60*time.Second {
		t.Fatalf("RejoinInterval = %v", recv.RejoinInterval)
	}
	w := newFakeWifi()
	w.connectErr = errors.New("the hotspot did not answer")
	// The other PC answers again 170 s after the drop. The backoff waits
	// 2, 4, 8, 16, then 30 s, so the ticks fall at 0, 2, 6, 14, 30, 60,
	// 90, 120 and 150 s: the tries allowed are at 0, 60 and 120 s.
	r, _, j := droppedRun(t, w, 170*time.Second)
	out, err := r.s.Run(context.Background(), j, nil)
	if err != nil || !out.Done {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if n := countCalls(w, "connect "+hotspot); n != 3 {
		t.Errorf("connects = %d over 180 s, want 3 (once a minute): %v", n, w.log())
	}
}

// A rejoin whose connect never finishes must not hold the copy: stopping it
// returns at once, paused, and the hotspot is still left.
func TestStopIsPromptDuringARejoin(t *testing.T) {
	w := newFakeWifi()
	w.connectHang = true
	r, _, j := droppedRun(t, w, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for countCalls(w, "connect "+hotspot) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	done := make(chan recv.Outcome, 1)
	start := time.Now()
	go func() {
		out, _ := r.s.Run(ctx, j, nil)
		done <- out
	}()
	select {
	case out := <-done:
		if !out.Paused {
			t.Errorf("outcome = %+v, want paused", out)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("stopping took %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after stop while a rejoin was connecting")
	}
	if w.has(hotspot) {
		t.Error("the hotspot profile was not removed after the stop")
	}
}

// A leave that fails keeps the record for the retry, but this PC is no
// longer meant to be on the hotspot: the next copy in the same session is a
// normal one.
func TestAFailedLeaveForgetsTheHotspotForTheNextCopy(t *testing.T) {
	w := newFakeWifi()
	r := newWifiRig(t, w)
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	if j := r.s.Start(hotspotIP, "Games", "u", `D:\Games`, robocopyPlan(1, 1)); !j.Hotspot() {
		t.Fatal("a live join did not mark the copy as a hotspot copy")
	}
	w.leaveErr = errors.New("WlanDeleteProfile: boom")
	if _, err := r.s.Leave(); err == nil {
		t.Fatal("a failed leave reported success")
	}
	if _, ok, _ := r.s.LoadJoin(); !ok {
		t.Fatal("the record went although the leave failed")
	}
	if j := r.s.Start("10.0.0.9", "Games", "u", `D:\Games`, robocopyPlan(1, 1)); j.Hotspot() || j.SSID != "" {
		t.Errorf("job after a failed leave = %+v, want a normal copy", j)
	}
}

// The exit hook leaves only a join this very process made, bounded by its
// context, and deletes Devpit's entry even when the reconnect runs out of
// time.
func TestLeaveOnExitLeavesOnlyThisProcesssJoin(t *testing.T) {
	ctx := context.Background()

	// This process's own join: left.
	r, w, procs := joinedByWindow(t, 4242)
	self := sessionAs(r, w, 4242, procs)
	res, err := self.LeaveIfOwned(ctx)
	if err != nil || !res.Left || !res.Deleted || !res.Reconnected {
		t.Errorf("own join: %+v, %v", res, err)
	}
	if w.has(hotspot) || w.card.Profile != homeWifi {
		t.Errorf("after the exit hook: profiles %v, card on %q", w.profiles, w.card.Profile)
	}
	if _, ok, _ := self.LoadJoin(); ok {
		t.Error("the record is still there")
	}

	// Another window's join, and a record of an earlier process that had
	// this PID: left alone, no Wi-Fi call.
	for name, pid := range map[string]int{"another window": 1111, "an earlier process with this PID": 4242} {
		t.Run(name, func(t *testing.T) {
			r2, w2, procs2 := joinedByWindow(t, 4242)
			if pid == 4242 {
				procs2[4242] = ownerStart.Add(time.Hour) // this process started later
			} else {
				procs2[pid] = ownerStart.Add(time.Hour)
			}
			n := len(w2.log())
			got, gerr := sessionAs(r2, w2, pid, procs2).LeaveIfOwned(ctx)
			if gerr != nil || got.Left || len(w2.log()) != n {
				t.Errorf("hook = %+v, %v; calls %v", got, gerr, w2.log()[n:])
			}
			if _, ok, _ := r2.s.LoadJoin(); !ok {
				t.Error("the record was removed")
			}
		})
	}

	// A reconnect that never comes: the hook returns within its bound, with
	// Devpit's entry deleted and the record gone.
	r, w, procs = joinedByWindow(t, 4242)
	w.leaveHang = true
	bound, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err = sessionAs(r, w, 4242, procs).LeaveIfOwned(bound)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the hook took %v", d)
	}
	if err != nil || !res.Deleted || res.Reconnected || !strings.Contains(res.Note, "Pick it") {
		t.Errorf("hook with a slow reconnect = %+v, %v", res, err)
	}
	if w.has(hotspot) {
		t.Error("Devpit's profile was not deleted first")
	}
	if _, ok, _ := r.s.LoadJoin(); ok {
		t.Error("the record stayed after the profile was deleted")
	}

	// No record at all: nothing happens, with the real Wi-Fi seam too
	// (the record is read first, so no Windows call is made).
	if res, err := recv.LeaveOnExit(ctx, t.TempDir()); err != nil || res.Left {
		t.Errorf("LeaveOnExit with no record = %+v, %v", res, err)
	}
}

// sessionSeeing is sessionAs where PID 4242's start time cannot be read and
// its program, the boot time and this program are as given.
func sessionSeeing(r *rig, w recv.Wifi, image string, imageErr error, boot time.Time) *recv.Session {
	d := recvDepsFor(r, w)
	procs := owned{1111: ownerStart.Add(time.Hour), 4242: {}} // 4242 runs, start unknown
	d.PID, d.ProcessStart = 1111, procs.start
	d.ProcessImage = func(int) (string, error) { return image, imageErr }
	d.Executable = func() (string, error) { return `C:\Tools\devpit.exe`, nil }
	d.BootTime = func() (time.Time, error) { return boot, nil }
	return recv.New(d)
}

// When Windows will not give a PID's start time, the program it runs and the
// boot time tell a reused PID from the Devpit window that made the join.
func TestAReusedPIDIsToldApartWhenItsStartIsHidden(t *testing.T) {
	denied := errors.New("OpenProcess: Access is denied")
	longAgo := time.Now().Add(-24 * time.Hour) // booted before the join
	for _, c := range []struct {
		name     string
		image    string
		imageErr error
		boot     time.Time
		inUse    bool
	}{
		{"another program", `C:\Windows\System32\svchost.exe`, nil, longAgo, false},
		{"a Devpit window", `D:\scoop\apps\devpit\current\DEVPIT.EXE`, nil, longAgo, true},
		{"nothing readable, joined before the restart", "", denied, time.Now().Add(time.Hour), false},
		{"nothing readable, joined since the restart", "", denied, longAgo, true},
		{"a Devpit window, joined before the restart", `C:\Tools\devpit.exe`, nil, time.Now().Add(time.Hour), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, w, _ := joinedByWindow(t, 4242)
			second := sessionSeeing(r, w, c.image, c.imageErr, c.boot)
			res, err := second.UndoLeftoverJoin()
			if err != nil || res.InUse != c.inUse || res.Left == c.inUse {
				t.Errorf("undo = %+v, %v; want in use %v", res, err, c.inUse)
			}
			if gone := !w.has(hotspot); gone == c.inUse {
				t.Errorf("hotspot profile removed = %v, want %v", gone, !c.inUse)
			}
		})
	}
}

// With location access off Windows will not list the networks: the screen
// is told so with ErrScanDenied, and a typed name joins with no scan result.
func TestADeniedScanAsksForTheNameAndATypedNameJoins(t *testing.T) {
	w := newFakeWifi()
	w.scanErr = fmt.Errorf("%w: WlanGetAvailableNetworkList: Access is denied", winapi.ErrWifiDenied)
	r := newWifiRig(t, w)
	if _, err := r.s.FindHotspots(); !errors.Is(err, recv.ErrScanDenied) || !errors.Is(err, winapi.ErrWifiDenied) {
		t.Fatalf("FindHotspots err = %v, want ErrScanDenied", err)
	}
	// Any other scan failure is not a denial.
	w.scanErr = errors.New("WlanGetAvailableNetworkList: the device is not ready")
	if _, err := r.s.FindHotspots(); err == nil || errors.Is(err, recv.ErrScanDenied) {
		t.Errorf("other failure = %v", err)
	}

	// A typed name: no profile hint, no scan.
	res := mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	if !res.Record.Created || res.Record.Profile != hotspot || w.card.Profile != hotspot {
		t.Errorf("typed join = %+v, card on %q", res, w.card.Profile)
	}
	if _, err := r.s.Leave(); err != nil {
		t.Fatal(err)
	}
	// A typed name that cannot be a network name is refused before
	// anything is written or changed.
	n := len(w.log())
	if _, err := r.s.Join(context.Background(), recv.JoinRequest{SSID: strings.Repeat("x", 33), Password: hotspotPW}); !errors.Is(err, winapi.ErrBadNetworkName) {
		t.Errorf("long name err = %v", err)
	}
	if len(w.log()) != n {
		t.Errorf("a bad name changed Wi-Fi: %v", w.log()[n:])
	}
	if _, err := os.Stat(joinRecordPath(r)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a record was written for a bad name")
	}
}
