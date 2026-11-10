package recv_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

const (
	cardGUID   = "{79ACB1E1-233C-4CA3-94F7-F4FEF1EBA2A8}"
	hotspot    = "Devpit-x7k2"
	hotspotPW  = "48151623"
	homeWifi   = "Home"
	hotspotIP  = "192.168.137.1"
	homeRxKbps = 1_201_000
)

// fakeWifi is a Wi-Fi card: its link, its saved profiles, and every call
// that would change something, in order.
type fakeWifi struct {
	mu       sync.Mutex
	card     winapi.WifiInterface
	none     bool  // no card at all
	ifErr    error // Interfaces fails
	profiles map[string]bool
	nets     []winapi.WifiNetwork
	calls    []string
	// hotspotLink is the link the card gets on the hotspot.
	hotspotLink winapi.WifiInterface
	joinErr     error
	leaveErr    error
	onJoin      func() // runs as Join starts, before anything changes
	onConnect   func()
	// gateway is what Gateway reports once gatewayAfter calls have returned "".
	gateway      string
	gatewayAfter int
	gatewayErr   error
	gatewayCalls int
	// outOfRange lists networks the nearby list does not show; visibleErr
	// makes that list unreadable (location access off).
	outOfRange map[string]bool
	visibleErr error
	// connectErr fails ConnectProfile; connectHang makes it wait for ctx.
	connectErr  error
	connectHang bool
	// wired is an up Ethernet adapter with a gateway.
	wired bool
	// scanErr fails Scan; leaveHang makes Leave's reconnect wait for ctx
	// after the delete.
	scanErr   error
	leaveHang bool
}

// Gateway is a read: it is not logged with the calls that change something.
func (f *fakeWifi) Gateway(string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gatewayCalls++
	if f.gatewayErr != nil {
		return "", f.gatewayErr
	}
	if f.gatewayCalls <= f.gatewayAfter {
		return "", nil
	}
	return f.gateway, nil
}

func newFakeWifi() *fakeWifi {
	return &fakeWifi{
		card: winapi.WifiInterface{
			GUID: cardGUID, Description: "Intel(R) Wi-Fi 6 AX201 160MHz", PHYs: []uint32{5, 6, 7, 4, 8, 10},
			Connected: true, PHY: winapi.PHYHE, RxKbps: homeRxKbps, TxKbps: 907_000, Channel: 48, Profile: homeWifi,
		},
		profiles:    map[string]bool{homeWifi: true},
		outOfRange:  map[string]bool{},
		hotspotLink: winapi.WifiInterface{PHY: winapi.PHYHE, RxKbps: 1_201_000, TxKbps: 1_201_000, Channel: 36},
	}
}

func (f *fakeWifi) Interfaces() ([]winapi.WifiInterface, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ifErr != nil {
		return nil, f.ifErr
	}
	if f.none {
		return nil, nil
	}
	return []winapi.WifiInterface{f.card}, nil
}

func (f *fakeWifi) Scan(string) ([]winapi.WifiNetwork, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "scan")
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	return f.nets, nil
}

func (f *fakeWifi) HasProfile(_, name string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.profiles[name], nil
}

// onto moves the card onto a profile; the hotspot gets the hotspot link.
func (f *fakeWifi) onto(profile string) {
	f.card.Connected, f.card.Profile = profile != "", profile
	if profile == homeWifi {
		f.card.PHY, f.card.RxKbps, f.card.TxKbps, f.card.Channel = winapi.PHYHE, homeRxKbps, 907_000, 48
	} else if profile != "" {
		l := f.hotspotLink
		f.card.PHY, f.card.RxKbps, f.card.TxKbps, f.card.Channel = l.PHY, l.RxKbps, l.TxKbps, l.Channel
	}
}

func (f *fakeWifi) Join(_ context.Context, _, ssid, pass string) error {
	if f.onJoin != nil {
		f.onJoin()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "join "+ssid+" with a password of "+string(rune('0'+len(pass)))+" characters")
	if f.joinErr != nil {
		if !errors.Is(f.joinErr, winapi.ErrProfileExists) {
			f.profiles[ssid] = true // a failed join can leave the profile behind
		}
		return f.joinErr
	}
	f.profiles[ssid] = true
	f.onto(ssid)
	return nil
}

func (f *fakeWifi) ConnectProfile(ctx context.Context, _, profile string) error {
	f.mu.Lock()
	f.calls = append(f.calls, "connect "+profile)
	if f.connectHang {
		// The hotspot never answers: the connect waits until ctx ends.
		f.mu.Unlock()
		<-ctx.Done()
		return ctx.Err()
	}
	if f.connectErr != nil {
		err := f.connectErr
		f.mu.Unlock()
		return err
	}
	if !f.profiles[profile] {
		f.mu.Unlock()
		return errors.New("no such profile")
	}
	f.onto(profile)
	hook := f.onConnect
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

// Visible is a read of the nearby list: not logged with the calls that
// change something.
func (f *fakeWifi) Visible(_, ssid string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.visibleErr != nil {
		return false, f.visibleErr
	}
	return !f.outOfRange[ssid], nil
}

// Wired is a read of the adapter table.
func (f *fakeWifi) Wired() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wired, nil
}

func (f *fakeWifi) setOutOfRange(ssid string, out bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outOfRange[ssid] = out
}

func (f *fakeWifi) Leave(ctx context.Context, _, temp, prev string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "leave temp="+temp+" previous="+prev)
	if f.leaveErr != nil {
		return f.leaveErr
	}
	if temp != "" {
		delete(f.profiles, temp)
		if f.card.Profile == temp {
			f.onto("")
		}
	}
	if f.leaveHang && prev != "" {
		// The delete is done; the old network never comes back in time.
		f.mu.Unlock()
		<-ctx.Done()
		f.mu.Lock()
		return errors.Join(winapi.ErrReconnect, ctx.Err())
	}
	if prev != "" {
		if !f.profiles[prev] {
			return errors.Join(winapi.ErrReconnect, winapi.ErrPreviousGone)
		}
		f.onto(prev)
	}
	return nil
}

func (f *fakeWifi) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeWifi) has(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.profiles[name]
}

func robocopyPlan(bytes, files int64) robocopy.Plan { return robocopy.Plan{Files: files, Bytes: bytes} }

func joinRecordPath(r *rig) string { return filepath.Join(r.dir, "share-join.json") }

func mustJoin(t *testing.T, r *rig, req recv.JoinRequest) recv.JoinResult {
	t.Helper()
	res, err := r.s.Join(context.Background(), req)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	return res
}

// secretLooking fails on any JSON key that looks like it holds a secret.
func secretLooking(t *testing.T, raw []byte) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for k := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "pass") || strings.Contains(lk, "key") || strings.Contains(lk, "secret") {
			t.Errorf("field %q looks like a secret", k)
		}
	}
}

func TestTheJoinRecordIsOnDiskFirstAndHoldsNoPassphrase(t *testing.T) {
	w := newFakeWifi()
	r := newWifiRig(t, w)
	var seen recv.JoinRecord
	var onDisk bool
	w.onJoin = func() {
		raw, err := os.ReadFile(joinRecordPath(r))
		if err != nil {
			return
		}
		onDisk = true
		_ = json.Unmarshal(raw, &seen)
	}
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	if !onDisk {
		t.Fatal("the join record was not on disk when the profile was added")
	}
	if seen.Interface != cardGUID || seen.SSID != hotspot || seen.TempProfile != hotspot || !seen.Created ||
		seen.Previous != homeWifi || seen.Profile != hotspot || seen.Time.IsZero() {
		t.Errorf("record before the join = %+v", seen)
	}
	raw, err := os.ReadFile(joinRecordPath(r))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), hotspotPW) {
		t.Errorf("the join record holds the hotspot password:\n%s", raw)
	}
	secretLooking(t, raw)
	for _, c := range w.log() {
		if strings.Contains(c, hotspotPW) {
			t.Errorf("fake log leaked the password: %q", c)
		}
	}
}

func TestLeaveDeletesOnlyTheDevpitProfileAndReconnects(t *testing.T) {
	w := newFakeWifi()
	r := newWifiRig(t, w)
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	if !w.has(hotspot) {
		t.Fatal("the join added no profile")
	}
	res, err := r.s.Leave()
	if err != nil {
		t.Fatal(err)
	}
	if !res.Left || !res.Deleted || !res.Reconnected || res.Previous != homeWifi || !strings.Contains(res.Note, homeWifi) {
		t.Errorf("leave = %+v", res)
	}
	if w.has(hotspot) || !w.has(homeWifi) {
		t.Errorf("profiles after leaving = %v", w.profiles)
	}
	if got := w.log(); got[len(got)-1] != "leave temp="+hotspot+" previous="+homeWifi {
		t.Errorf("calls = %v", got)
	}
	if _, serr := os.Stat(joinRecordPath(r)); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("the join record is still there: %v", serr)
	}
	// Leaving again does nothing.
	n := len(w.log())
	if again, aerr := r.s.Leave(); aerr != nil || again.Left || len(w.log()) != n {
		t.Errorf("second leave = %+v %v, calls %v", again, aerr, w.log())
	}

	// A delete that fails keeps the record for the next try.
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	w.leaveErr = errors.New("WlanDeleteProfile: boom")
	if _, ferr := r.s.Leave(); ferr == nil {
		t.Fatal("a failed delete reported success")
	}
	if _, ok, _ := r.s.LoadJoin(); !ok {
		t.Error("the record went although the profile was not deleted")
	}
	// A previous network that is gone: the profile is deleted, the record
	// goes, and the screen gets the plain line.
	w.leaveErr = nil
	delete(w.profiles, homeWifi)
	res, err = r.s.Leave()
	if err != nil || !strings.Contains(res.Note, "Pick your Wi-Fi network") {
		t.Errorf("leave with the old network gone = %+v %v", res, err)
	}
	if _, ok, _ := r.s.LoadJoin(); ok {
		t.Error("the record stayed after its profile was deleted")
	}
}

func TestAnExistingProfileIsUsedAndNeverDeleted(t *testing.T) {
	w := newFakeWifi()
	w.profiles["My hotspot"] = true
	r := newWifiRig(t, w)
	res := mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Profile: "My hotspot"})
	if !res.UsedSaved || res.Record.Created || res.Record.TempProfile != "" || res.Record.Profile != "My hotspot" {
		t.Errorf("join = %+v", res)
	}
	for _, c := range w.log() {
		if strings.HasPrefix(c, "join ") {
			t.Errorf("a new profile was added although one was saved: %v", w.log())
		}
	}
	if _, err := r.s.Leave(); err != nil {
		t.Fatal(err)
	}
	if !w.has("My hotspot") {
		t.Error("the user's own profile was deleted")
	}
	if got := w.log(); got[len(got)-1] != "leave temp= previous="+homeWifi {
		t.Errorf("calls = %v", got)
	}

	// A profile named like the hotspot that appears between the check and
	// the add is the user's too.
	w2 := newFakeWifi()
	w2.joinErr = winapi.ErrProfileExists
	r2 := newWifiRig(t, w2)
	w2.onJoin = func() { w2.mu.Lock(); w2.profiles[hotspot] = true; w2.mu.Unlock() }
	res = mustJoin(t, r2, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	if res.Record.Created || !res.UsedSaved {
		t.Errorf("raced join = %+v", res)
	}
	rec, _, _ := r2.s.LoadJoin()
	if rec.Created || rec.TempProfile != "" {
		t.Errorf("record = %+v, must not say Devpit made it", rec)
	}
	_, _ = r2.s.Leave()
	if !w2.has(hotspot) {
		t.Error("a profile Devpit did not create was deleted")
	}
}

func TestALeftoverJoinIsUndoneOnTheNextOpen(t *testing.T) {
	w := newFakeWifi()
	r := newWifiRig(t, w)
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})

	// "Crash": a new session over the same folder opens Share Files.
	r2 := newWifiRig(t, w)
	r2.s = recv.New(recvDepsFor(r, w))
	res, err := r2.s.UndoLeftoverJoin()
	if err != nil {
		t.Fatal(err)
	}
	if !res.Left || !res.Deleted || !strings.Contains(res.Note, "last time") || !strings.Contains(res.Note, homeWifi) {
		t.Errorf("undo = %+v", res)
	}
	if w.has(hotspot) || w.card.Profile != homeWifi {
		t.Errorf("after the undo: profiles %v, card on %q", w.profiles, w.card.Profile)
	}
	// Nothing left: the next open says nothing.
	if res, err := r2.s.UndoLeftoverJoin(); err != nil || res.Left || res.Note != "" {
		t.Errorf("second open = %+v %v", res, err)
	}
}

// recvDepsFor is a minimal set of deps over r's state folder and w.
func recvDepsFor(r *rig, w recv.Wifi) recv.Deps {
	return recv.Deps{
		Wifi: w, StateDir: r.dir, LogDir: filepath.Join(r.dir, "logs"),
		Sleep: func(context.Context, time.Duration) {},
	}
}

func TestLeaveRunsOnStopFailureAndDone(t *testing.T) {
	bad := "2026/09/29 10:15:02 ERROR 5 (0x00000005) Copying File D:\\Games\\secret.bin\n"
	for _, tc := range []struct {
		name  string
		steps []step
		stop  bool
		check func(recv.Outcome) bool
	}{
		{"done", []step{{text: files("a", "b", "c") + summaryOK, code: 1}}, false, func(o recv.Outcome) bool { return o.Done }},
		{"stop", []step{{text: files("a"), code: 1, hold: make(chan struct{})}}, true, func(o recv.Outcome) bool { return o.Paused }},
		{"failure", []step{{text: bad, code: 9}, {text: bad, code: 9}, {text: bad, code: 9}}, false, func(o recv.Outcome) bool {
			return !o.Done && !o.Paused && len(o.Failed) == 1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newFakeWifi()
			r := newWifiRig(t, w, tc.steps...)
			mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
			j := r.s.Start(hotspotIP, "Games", "devpit-x7k2", `D:\Games`, robocopyPlan(3000, 3))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.stop {
				go func() { time.Sleep(60 * time.Millisecond); cancel() }()
			}
			out, err := r.s.Run(ctx, j, nil)
			if err != nil || !tc.check(out) {
				t.Fatalf("outcome = %+v, %v", out, err)
			}
			if w.has(hotspot) || w.card.Profile != homeWifi {
				t.Errorf("not left: profiles %v, card on %q", w.profiles, w.card.Profile)
			}
			if _, ok, _ := r.s.LoadJoin(); ok {
				t.Error("the join record is still there")
			}
			if !strings.Contains(out.WifiNote, homeWifi) {
				t.Errorf("wifi note = %q", out.WifiNote)
			}
		})
	}
	// A normal copy never touches Wi-Fi.
	w := newFakeWifi()
	r := newWifiRig(t, w, step{text: files("a", "b", "c") + summaryOK, code: 1})
	if _, err := r.s.Run(context.Background(), startJob(r, 3000, 3), nil); err != nil {
		t.Fatal(err)
	}
	if len(w.log()) != 0 {
		t.Errorf("a normal copy made Wi-Fi calls: %v", w.log())
	}
}

func TestAPCWithNoWifiCardIsNeverOfferedAJoin(t *testing.T) {
	noCard := newFakeWifi()
	noCard.none = true
	noService := newFakeWifi()
	noService.ifErr = winapi.ErrNoWifi
	for name, wifi := range map[string]recv.Wifi{"no seam": nil, "no card": noCard, "no service": noService} {
		t.Run(name, func(t *testing.T) {
			r := newWifiRig(t, wifi)
			if r.s.CanJoin() {
				t.Error("CanJoin = true")
			}
			if _, err := r.s.FindHotspots(); !errors.Is(err, recv.ErrNoWifiCard) {
				t.Errorf("FindHotspots err = %v", err)
			}
			if _, err := r.s.Join(context.Background(), recv.JoinRequest{SSID: hotspot, Password: hotspotPW}); !errors.Is(err, recv.ErrNoWifiCard) {
				t.Errorf("Join err = %v", err)
			}
			if _, err := os.Stat(joinRecordPath(r)); !errors.Is(err, os.ErrNotExist) {
				t.Error("a join record was written on a PC with no Wi-Fi")
			}
			if fw, ok := wifi.(*fakeWifi); ok && len(fw.log()) != 0 {
				t.Errorf("calls = %v", fw.log())
			}
		})
	}
}

func TestTheReconnectLoopRejoinsTheHotspot(t *testing.T) {
	errLine := "2026/09/29 10:15:02 ERROR 64 (0x00000040) Copying File D:\\Games\\c\n"
	w := newFakeWifi()
	r := newWifiRig(t,
		w,
		step{text: files("a", "b") + errLine, code: 9},
		step{text: files("c") + summaryOK, code: 1},
	)
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	r.mu.Lock()
	r.sleeps = nil // the join's settle wait
	r.mu.Unlock()
	j := r.s.Start(hotspotIP, "Games", "devpit-x7k2", `D:\Games`, robocopyPlan(3000, 3))
	if !j.Hotspot() || j.SSID != hotspot {
		t.Fatalf("job = %+v, want it to remember the hotspot", j)
	}
	// The PC drops off the hotspot, so the other PC stops answering until
	// Devpit rejoins.
	w.mu.Lock()
	w.onto(homeWifi)
	w.mu.Unlock()
	r.reachable.Store(false)
	w.onConnect = func() { r.reachable.Store(true) }
	var notes []string
	out, err := r.s.Run(context.Background(), j, func(e recv.Event) {
		if e.Phase == recv.PhaseWaiting {
			notes = append(notes, e.Note)
		}
	})
	if err != nil || !out.Done {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if n := countCalls(w, "connect "+hotspot); n != 1 {
		t.Errorf("calls = %v, want one rejoin with the hotspot profile", w.log())
	}
	if len(r.sleeps) != 0 {
		t.Errorf("sleeps = %v; a rejoin that works needs no backoff", r.sleeps)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "Rejoined") {
		t.Errorf("notes = %v", notes)
	}
	if len(r.runner.runs) != 2 {
		t.Errorf("robocopy ran %d times, want 2", len(r.runner.runs))
	}

	// Still on the hotspot: the other PC itself is gone, so no rejoin.
	w2 := newFakeWifi()
	r2 := newWifiRig(t, w2, step{text: files("a", "b") + errLine, code: 9}, step{text: files("c") + summaryOK, code: 1})
	mustJoin(t, r2, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	r2.reachable.Store(false)
	if _, err := r2.s.Run(context.Background(), r2.s.Start(hotspotIP, "Games", "u", `D:\Games`, robocopyPlan(3000, 3)), nil); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(w2.log(), "connect "+hotspot) {
		t.Errorf("rejoined while still on the hotspot: %v", w2.log())
	}
}

func TestResumeRemembersTheHotspotAndAsksForItsPasswordAgain(t *testing.T) {
	w := newFakeWifi()
	hold := make(chan struct{})
	r := newWifiRig(t, w, step{text: files("a"), code: 1, hold: hold})
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(60 * time.Millisecond); cancel() }()
	if _, err := r.s.Run(ctx, r.s.Start(hotspotIP, "Games", "devpit-x7k2", `D:\Games`, robocopyPlan(3000, 3)), nil); err != nil {
		t.Fatal(err)
	}
	saved, ok, err := r.s.LoadJob()
	if err != nil || !ok || !saved.Hotspot() || saved.SSID != hotspot || saved.Mode != job.ModeHotspot || !saved.Resumable() {
		t.Fatalf("saved = %+v %v %v", saved, ok, err)
	}
	raw, _ := os.ReadFile(filepath.Join(r.dir, "share-recv.json"))
	if strings.Contains(string(raw), hotspotPW) {
		t.Errorf("the resume record holds the hotspot password:\n%s", raw)
	}
	need, err := r.s.ResumeNeeds(saved)
	if err != nil || !need.Rejoin || !need.NeedPassword || need.SSID != hotspot || need.Profile != "" {
		t.Errorf("resume needs = %+v %v; Devpit deleted its profile, so the password is asked again", need, err)
	}
	// With the user's own saved profile, no password.
	w.profiles[hotspot] = true
	need, _ = r.s.ResumeNeeds(saved)
	if !need.Rejoin || need.NeedPassword || need.Profile != hotspot {
		t.Errorf("with a saved profile = %+v", need)
	}
	// Join with no password then works and never adds a profile.
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot})
	need, _ = r.s.ResumeNeeds(saved)
	if need.Rejoin {
		t.Errorf("already on the hotspot = %+v", need)
	}
	// No saved profile and no password: asked, nothing written.
	w2 := newFakeWifi()
	r2 := newWifiRig(t, w2)
	if _, err := r2.s.Join(context.Background(), recv.JoinRequest{SSID: hotspot}); !errors.Is(err, recv.ErrNeedPassword) {
		t.Errorf("err = %v", err)
	}
	if _, ok, _ := r2.s.LoadJoin(); ok {
		t.Error("a record was written for a join that never started")
	}
	// A normal job needs nothing.
	if need, err := r.s.ResumeNeeds(startJob(r, 1, 1)); err != nil || need.Rejoin {
		t.Errorf("normal job = %+v %v", need, err)
	}
}

func TestAFailedJoinIsUndoneAndSaysWhereThePCIs(t *testing.T) {
	w := newFakeWifi()
	w.joinErr = winapi.ErrJoinTimeout
	r := newWifiRig(t, w)
	res, err := r.s.Join(context.Background(), recv.JoinRequest{SSID: hotspot, Password: "wrongpass"})
	if !errors.Is(err, winapi.ErrJoinTimeout) {
		t.Fatalf("err = %v", err)
	}
	if !res.Undo.Deleted || !strings.Contains(res.Undo.Note, homeWifi) || w.has(hotspot) {
		t.Errorf("undo = %+v, profiles %v", res.Undo, w.profiles)
	}
	if _, ok, _ := r.s.LoadJoin(); ok {
		t.Error("the record stayed after the undo")
	}
	if j := r.s.Start(hotspotIP, "Games", "u", `D:\Games`, robocopyPlan(1, 1)); j.Hotspot() {
		t.Error("a failed join still marks the copy as a hotspot copy")
	}
}

func TestFindHotspotsPutsDevpitNamesFirst(t *testing.T) {
	w := newFakeWifi()
	w.nets = []winapi.WifiNetwork{ // strongest first, as the scan gives them
		{SSID: "Neighbour", Signal: 99},
		{SSID: homeWifi, Signal: 90, HasProfile: true, Profile: homeWifi},
		{SSID: "Devpit-ab12", Signal: 60},
		{SSID: "devpit-lower", Signal: 55},
		{SSID: hotspot, Signal: 40},
	}
	r := newWifiRig(t, w)
	got, err := r.s.FindHotspots()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, n := range got {
		names = append(names, n.SSID)
	}
	want := []string{"Devpit-ab12", hotspot, "Neighbour", homeWifi, "devpit-lower"}
	if !slices.Equal(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
	if w.nets[0].SSID != "Neighbour" {
		t.Error("FindHotspots reordered the caller's slice")
	}
}

func link(rx uint32) recv.LinkInfo {
	return recv.LinkInfo{OnWifi: true, PHY: winapi.PHYHE, RxKbps: rx, TxKbps: rx, Channel: 36}
}

func TestAWorseHotspotLinkIsReportedWithBothLinks(t *testing.T) {
	w := newFakeWifi()
	w.hotspotLink = winapi.WifiInterface{PHY: winapi.PHYHE, RxKbps: 286_800, TxKbps: 286_800, Channel: 36} // a 20 MHz soft AP
	r := newWifiRig(t, w)
	res := mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	v := res.Verdict
	if !v.Worse || v.Reason == "" {
		t.Fatalf("verdict = %+v, want worse", v)
	}
	if !v.Before.OnWifi || v.Before.RxKbps != homeRxKbps || v.Before.Channel != 48 || v.Before.Profile != homeWifi ||
		v.Before.Standard != "Wi-Fi 6" {
		t.Errorf("before = %+v", v.Before)
	}
	if !v.After.OnWifi || v.After.RxKbps != 286_800 || v.After.Channel != 36 || v.After.Profile != hotspot {
		t.Errorf("after = %+v", v.After)
	}
	// Equal or better changes nothing.
	for _, after := range []uint32{homeRxKbps, 2_401_000} {
		if v := recv.CompareLinks(link(homeRxKbps), link(after)); v.Worse || v.Reason != "" {
			t.Errorf("after %d: %+v", after, v)
		}
	}
	// An unknown rate is never a reason to switch.
	if v := recv.CompareLinks(link(0), link(100)); v.Worse {
		t.Errorf("unknown before: %+v", v)
	}
	if v := recv.CompareLinks(link(1000), recv.LinkInfo{OnWifi: true}); v.Worse {
		t.Errorf("unknown after: %+v", v)
	}
	// The hotspot link never came up.
	if v := recv.CompareLinks(link(1000), recv.LinkInfo{}); !v.Worse {
		t.Errorf("no link after: %+v", v)
	}
	// Only the receive rate counts when known; the send rate stands in.
	if v := recv.CompareLinks(recv.LinkInfo{OnWifi: true, TxKbps: 1000}, recv.LinkInfo{OnWifi: true, TxKbps: 500}); !v.Worse {
		t.Errorf("send rates only: %+v", v)
	}
}

func TestTheFallbackMarginAtItsBoundary(t *testing.T) {
	if recv.WorseLinkMarginPercent <= 0 || recv.WorseLinkMarginPercent >= 100 {
		t.Fatalf("margin %d%% is not a margin", recv.WorseLinkMarginPercent)
	}
	const before = 1_000_000
	edge := uint32(before * (100 - recv.WorseLinkMarginPercent) / 100) // 750 000 at 25 %
	if v := recv.CompareLinks(link(before), link(edge)); v.Worse {
		t.Errorf("exactly at the margin (%d of %d) counts as worse", edge, before)
	}
	if v := recv.CompareLinks(link(before), link(edge-1)); !v.Worse {
		t.Errorf("just below the margin (%d of %d) is not worse", edge-1, before)
	}
}

func TestAWiredReceiverIsToldToStayWired(t *testing.T) {
	w := newFakeWifi()
	w.card.Connected, w.card.Profile, w.card.RxKbps, w.card.TxKbps, w.card.PHY, w.card.Channel = false, "", 0, 0, 0, 0
	w.wired = true // an Ethernet adapter that is up, with a gateway
	r := newWifiRig(t, w)
	before, err := r.s.CurrentLink()
	if err != nil || before.OnWifi || !before.Wired {
		t.Fatalf("current link = %+v %v, want on a cable", before, err)
	}
	v := recv.CompareLinks(before, link(2_401_000))
	if !v.Worse || !strings.Contains(v.Reason, "cable") || !strings.Contains(v.Reason, "not on Wi-Fi") {
		t.Errorf("verdict = %+v", v)
	}
	// The join itself sees the cable too.
	res := mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	if !res.Verdict.Worse || !res.Verdict.Before.Wired {
		t.Errorf("join verdict = %+v", res.Verdict)
	}
}

// A PC that was on no network at all has nothing better to go back to: it is
// not told it is on a cable, and the direct link is kept.
func TestAReceiverOnNoNetworkKeepsTheDirectLink(t *testing.T) {
	w := newFakeWifi()
	w.card.Connected, w.card.Profile, w.card.RxKbps, w.card.TxKbps, w.card.PHY, w.card.Channel = false, "", 0, 0, 0, 0
	r := newWifiRig(t, w)
	before, err := r.s.CurrentLink()
	if err != nil || before.OnWifi || before.Wired {
		t.Fatalf("current link = %+v %v, want on nothing", before, err)
	}
	res := mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	v := res.Verdict
	if v.Worse || strings.Contains(v.Reason, "cable") || !strings.Contains(v.Reason, "not on Wi-Fi before") {
		t.Errorf("verdict = %+v, want the direct link kept with a neutral line", v)
	}
	// The direct link that never came up is still worse.
	if v := recv.CompareLinks(recv.LinkInfo{}, recv.LinkInfo{}); !v.Worse {
		t.Errorf("no link before or after: %+v", v)
	}
}

// With location access off Windows names no rates; the links cannot be
// compared, which is said and is never a reason to switch.
func TestUnknownRatesAreSaidAndNeverWorse(t *testing.T) {
	for _, c := range [][2]recv.LinkInfo{
		{link(0), link(100)}, {link(1000), {OnWifi: true}}, {{OnWifi: true}, {OnWifi: true}},
	} {
		if v := recv.CompareLinks(c[0], c[1]); v.Worse || !v.Unknown {
			t.Errorf("%+v -> %+v, want unknown and not worse", c, v)
		}
	}
	if v := recv.CompareLinks(link(1000), link(1000)); v.Unknown {
		t.Errorf("known rates marked unknown: %+v", v)
	}
	if recv.UnknownLinkNote == "" {
		t.Error("no sentence for an unknown verdict")
	}
}

// No verdict or leave line promises a speed: no "faster", no multiplier, no
// "up to N Mbps".
func TestNoJoinMessagePromisesASpeed(t *testing.T) {
	bad := regexp.MustCompile(`(?i)faster|×|\d\s*x\b|\bx\s*\d|up to|mbps|gbps`)
	var lines []string
	for _, c := range [][2]recv.LinkInfo{
		{{}, link(1)}, {{Wired: true}, link(1)}, {link(1000), {}}, {link(1000), link(10)}, {link(1000), link(1000)},
	} {
		lines = append(lines, recv.CompareLinks(c[0], c[1]).Reason)
	}
	lines = append(lines, recv.UnknownLinkNote)
	w := newFakeWifi()
	r := newWifiRig(t, w)
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	res, _ := r.s.Leave()
	lines = append(lines, res.Note)
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	delete(w.profiles, homeWifi)
	res, _ = r.s.UndoLeftoverJoin()
	lines = append(lines, res.Note)
	for _, l := range lines {
		if bad.MatchString(l) {
			t.Errorf("message promises a speed: %q", l)
		}
	}
}

// owned is a pretend set of running processes: PID to creation time.
type owned map[int]time.Time

func (o owned) start(pid int) (time.Time, bool) {
	t, ok := o[pid]
	return t, ok
}

// sessionAs is a session over r's state folder that runs as PID pid, in a
// world where the processes in procs are the ones running.
func sessionAs(r *rig, w recv.Wifi, pid int, procs owned) *recv.Session {
	d := recvDepsFor(r, w)
	d.PID, d.ProcessStart = pid, procs.start
	return recv.New(d)
}

var ownerStart = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// joinedByWindow joins as window pid (running since ownerStart) and returns
// the rig and the Wi-Fi card for another window to look at.
func joinedByWindow(t *testing.T, pid int) (*rig, *fakeWifi, owned) {
	t.Helper()
	w := newFakeWifi()
	r := newWifiRig(t, w)
	procs := owned{pid: ownerStart}
	r.s = sessionAs(r, w, pid, procs)
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	rec, ok, err := r.s.LoadJoin()
	if err != nil || !ok || rec.PID != pid || !rec.ProcessStart.Equal(ownerStart) {
		t.Fatalf("record = %+v, %v, %v; want it owned by PID %d started %v", rec, ok, err, pid, ownerStart)
	}
	return r, w, procs
}

func TestALiveOtherWindowsJoinIsLeftAlone(t *testing.T) {
	r, w, procs := joinedByWindow(t, 4242)
	procs[1111] = ownerStart.Add(time.Hour) // the second window itself
	second := sessionAs(r, w, 1111, procs)
	before := len(w.log())

	res, err := second.UndoLeftoverJoin()
	if err != nil || !res.InUse || res.Left || res.Note != recv.HotspotInUseNote {
		t.Errorf("undo = %+v, %v; want InUse and the note", res, err)
	}
	if recv.HotspotInUseNote != "Another Devpit window is using the Devpit hotspot right now." {
		t.Errorf("note = %q", recv.HotspotInUseNote)
	}
	// Opening a join in the second window must not replace or undo it either,
	// and leaving from it must not touch the first window's join.
	if _, jerr := second.Join(context.Background(), recv.JoinRequest{SSID: "Devpit-other", Password: hotspotPW}); !errors.Is(jerr, recv.ErrHotspotInUse) {
		t.Errorf("Join err = %v, want ErrHotspotInUse", jerr)
	}
	if lres, lerr := second.Leave(); lerr != nil || lres.Left || !lres.InUse {
		t.Errorf("Leave = %+v, %v", lres, lerr)
	}
	if got := w.log(); len(got) != before {
		t.Errorf("the second window changed Wi-Fi: %v", got[before:])
	}
	if !w.has(hotspot) || w.card.Profile != hotspot {
		t.Errorf("the first window's join was disturbed: profiles %v, card on %q", w.profiles, w.card.Profile)
	}
	rec, ok, _ := second.LoadJoin()
	if !ok || rec.PID != 4242 || rec.SSID != hotspot {
		t.Errorf("the first window's record changed: %+v %v", rec, ok)
	}
	// The first window still leaves its own join.
	first := sessionAs(r, w, 4242, procs)
	if lres, lerr := first.Leave(); lerr != nil || !lres.Left || !lres.Deleted {
		t.Errorf("owner Leave = %+v, %v", lres, lerr)
	}
}

func TestADeadOwnersJoinIsUndone(t *testing.T) {
	r, w, procs := joinedByWindow(t, 4242)
	delete(procs, 4242) // the first window is gone
	procs[1111] = ownerStart.Add(time.Hour)
	second := sessionAs(r, w, 1111, procs)

	res, err := second.UndoLeftoverJoin()
	if err != nil || res.InUse || !res.Left || !res.Deleted || !strings.Contains(res.Note, "last time") {
		t.Errorf("undo = %+v, %v", res, err)
	}
	if w.has(hotspot) || w.card.Profile != homeWifi {
		t.Errorf("after the undo: profiles %v, card on %q", w.profiles, w.card.Profile)
	}
	if _, ok, _ := second.LoadJoin(); ok {
		t.Error("the record is still there")
	}
}

func TestAReusedPIDIsNotTheOwner(t *testing.T) {
	r, w, procs := joinedByWindow(t, 4242)
	// PID 4242 is running, but it is a different program that started later.
	procs[4242] = ownerStart.Add(3 * time.Hour)
	procs[1111] = ownerStart.Add(4 * time.Hour)
	second := sessionAs(r, w, 1111, procs)

	res, err := second.UndoLeftoverJoin()
	if err != nil || res.InUse || !res.Left || !res.Deleted {
		t.Errorf("undo = %+v, %v", res, err)
	}
	if w.has(hotspot) || w.card.Profile != homeWifi {
		t.Errorf("after the undo: profiles %v, card on %q", w.profiles, w.card.Profile)
	}
}

func TestAnOldJoinRecordWithoutOwnerIsUndone(t *testing.T) {
	w := newFakeWifi()
	r := newWifiRig(t, w)
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	// Rewrite the record as an older Devpit wrote it: no owner fields.
	raw, err := os.ReadFile(joinRecordPath(r))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "pid")
	delete(m, "process_start")
	old, _ := json.Marshal(m)
	if err := os.WriteFile(joinRecordPath(r), old, 0o600); err != nil {
		t.Fatal(err)
	}
	// Even with every PID "running", a record with no owner is a leftover.
	procs := owned{1: ownerStart, 4242: ownerStart, 1111: ownerStart.Add(time.Hour)}
	second := sessionAs(r, w, 1111, procs)

	res, uerr := second.UndoLeftoverJoin()
	if uerr != nil || res.InUse || !res.Left || !res.Deleted {
		t.Errorf("undo = %+v, %v", res, uerr)
	}
	if w.has(hotspot) || w.card.Profile != homeWifi {
		t.Errorf("after the undo: profiles %v, card on %q", w.profiles, w.card.Profile)
	}
}

func TestJoinReportsTheHotspotGatewayAsTheHost(t *testing.T) {
	w := newFakeWifi()
	w.gateway, w.gatewayAfter = "192.168.50.1", 3 // the ICS range was changed; the address arrives late
	r := newWifiRig(t, w)
	res := mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	if res.Host != "192.168.50.1" {
		t.Fatalf("Host = %q, want the gateway", res.Host)
	}
	if w.gatewayCalls != 4 {
		t.Errorf("Gateway was read %d times, want 4 (3 empty answers, then the address)", w.gatewayCalls)
	}
}

func TestJoinFallsBackToTheUsualHotspotAddress(t *testing.T) {
	for name, set := range map[string]func(*fakeWifi){
		"no gateway ever":  func(*fakeWifi) {},
		"the read fails":   func(f *fakeWifi) { f.gatewayErr = errors.New("no adapter table") },
		"never arrives":    func(f *fakeWifi) { f.gateway, f.gatewayAfter = "192.168.50.1", 1000 },
		"a malformed text": func(f *fakeWifi) { f.gateway = "not an address" },
	} {
		t.Run(name, func(t *testing.T) {
			w := newFakeWifi()
			set(w)
			r := newWifiRig(t, w)
			res := mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
			if res.Host != hotspotIP {
				t.Fatalf("Host = %q, want %q", res.Host, recv.DefaultHotspotHost)
			}
			if w.gatewayCalls > 10 {
				t.Errorf("Gateway was read %d times: the wait is not bounded", w.gatewayCalls)
			}
		})
	}
}

func TestAPublicGatewayIsNotUsed(t *testing.T) {
	for _, gw := range []string{"8.8.8.8", "203.0.113.9", "169.254.1.1", "::1", "fd00::1"} {
		w := newFakeWifi()
		w.gateway = gw
		r := newWifiRig(t, w)
		res := mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
		if res.Host != recv.DefaultHotspotHost {
			t.Errorf("gateway %q: Host = %q, want the usual hotspot address", gw, res.Host)
		}
	}
}
