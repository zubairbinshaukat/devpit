package recv_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// The adapters of the live test: the receiver's own old hotspot address on a
// Wi-Fi Direct adapter, and its real link on the Wi-Fi card.
var (
	wfdHolder  = winapi.AddressHolder{Alias: "Local Area Connection* 11", IfIndex: 43, IfType: 71, Virtual: true, Routes: true}
	wfdHolder2 = winapi.AddressHolder{Alias: "Local Area Connection* 10", IfIndex: 16, IfType: 71, Virtual: true}
	ethHolder  = winapi.AddressHolder{Alias: "Ethernet", IfIndex: 7, IfType: 6}
	cardHolder = winapi.AddressHolder{Alias: "Wi-Fi", IfIndex: 11, IfType: 71}
)

// fakeAddrs is this PC's address table: who holds which address. Share of
// the fake worker clears the Wi-Fi Direct holders, as the real script does.
type fakeAddrs struct {
	mu      sync.Mutex
	holders map[string][]winapi.AddressHolder
	reads   int
	err     error
}

func (f *fakeAddrs) read(ip string) ([]winapi.AddressHolder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	return append([]winapi.AddressHolder(nil), f.holders[ip]...), nil
}

func (f *fakeAddrs) set(ip string, hs ...winapi.AddressHolder) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holders == nil {
		f.holders = map[string][]winapi.AddressHolder{}
	}
	f.holders[ip] = hs
}

func route(alias string) func(string) (string, string, error) {
	return func(string) (string, string, error) { return alias, "192.168.137.9", nil }
}

// fakeAdmin is the elevated worker.
type fakeAdmin struct {
	addrs    *fakeAddrs
	moveTo   *winapi.AddressHolder // after the free, Windows puts the address here
	reqs     []elevate.ShareRequest
	closed   int
	shareErr error
}

func (a *fakeAdmin) Share(_ context.Context, req elevate.ShareRequest, _ func(string)) error {
	a.reqs = append(a.reqs, req)
	if err := req.Validate(); err != nil {
		return err
	}
	if a.shareErr != nil {
		return a.shareErr
	}
	a.addrs.mu.Lock()
	var kept []winapi.AddressHolder
	for _, h := range a.addrs.holders[req.FreeIP] {
		if !h.Virtual {
			kept = append(kept, h)
		}
	}
	if a.moveTo != nil {
		kept = append(kept, *a.moveTo)
	}
	a.addrs.holders[req.FreeIP] = kept
	a.addrs.mu.Unlock()
	return nil
}

func (a *fakeAdmin) Close() error { a.closed++; return nil }

// clashRig is a rig whose session reads addrs and launches admin.
func clashRig(t *testing.T, w recv.Wifi, addrs *fakeAddrs, admin *fakeAdmin, launches *int, steps ...step) *rig {
	t.Helper()
	r := newWifiRig(t, w, steps...)
	r.deps.Addresses = addrs.read
	r.deps.Route = route("Wi-Fi")
	r.deps.Admin = func(context.Context) (recv.Admin, error) {
		*launches++
		return admin, nil
	}
	r.s = recv.New(r.deps)
	return r
}

// speedWords are claims no check sentence may make.
var speedWords = regexp.MustCompile(`(?i)\b(fast|faster|slow|slower|speed|mbps|gbps|mb/s|seconds?)\b|\d+ ?(mb|gb)`)

func TestCheckHostSeesThatThisPCHoldsTheAddress(t *testing.T) {
	addrs := &fakeAddrs{}
	addrs.set(hotspotIP, wfdHolder)
	launches := 0
	r := clashRig(t, nil, addrs, &fakeAdmin{addrs: addrs}, &launches)
	c, err := r.s.CheckHost(context.Background(), hotspotIP)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Local || !c.Fixable || c.Reachable || c.Via != wfdHolder.Alias || c.Host != hotspotIP {
		t.Errorf("check = %+v", c)
	}
	if len(c.Holders) != 1 || c.Holders[0] != wfdHolder.Alias {
		t.Errorf("holders = %v", c.Holders)
	}
	if r.dials.Load() != 0 {
		t.Error("the 445 probe ran against this PC's own address")
	}
	reason := c.Reason()
	if !strings.Contains(reason, hotspotIP) || !strings.Contains(reason, "Wi-Fi Direct") || speedWords.MatchString(reason) {
		t.Errorf("reason = %q", reason)
	}
	if launches != 0 {
		t.Error("a check launched the worker")
	}
}

func TestCheckHostOnAHealthyLink(t *testing.T) {
	addrs := &fakeAddrs{}
	launches := 0
	r := clashRig(t, nil, addrs, &fakeAdmin{addrs: addrs}, &launches)
	c, err := r.s.CheckHost(context.Background(), hotspotIP)
	if err != nil {
		t.Fatal(err)
	}
	if c.Local || c.Fixable || !c.Reachable || c.Via != "Wi-Fi" || len(c.Holders) != 0 || c.Reason() != "" {
		t.Errorf("check = %+v, reason %q", c, c.Reason())
	}
	r.reachable.Store(false)
	c, err = r.s.CheckHost(context.Background(), hotspotIP)
	if err != nil || c.Reachable || !strings.Contains(c.Reason(), "does not answer") || speedWords.MatchString(c.Reason()) {
		t.Errorf("unreachable check = %+v, %v, reason %q", c, err, c.Reason())
	}
	for _, bad := range []string{"8.8.8.8", "PC-NAME", "", "::1"} {
		if _, err := r.s.CheckHost(context.Background(), bad); err == nil {
			t.Errorf("CheckHost(%q) was accepted", bad)
		}
	}
	addrs.err = errors.New("no table")
	if _, err := r.s.CheckHost(context.Background(), hotspotIP); err == nil {
		t.Error("a failed read was not an error")
	}
	plain := recv.New(recv.Deps{})
	if _, err := plain.CheckHost(context.Background(), hotspotIP); !errors.Is(err, recv.ErrNoAddressCheck) {
		t.Errorf("no seam: err = %v", err)
	}
}

func TestFreeAddressLaunchesTheWorkerOnceAndRechecks(t *testing.T) {
	addrs := &fakeAddrs{}
	addrs.set(hotspotIP, wfdHolder, wfdHolder2)
	admin := &fakeAdmin{addrs: addrs}
	launches := 0
	r := clashRig(t, nil, addrs, admin, &launches)
	if err := r.s.FreeAddress(context.Background(), hotspotIP); err != nil {
		t.Fatal(err)
	}
	if launches != 1 || admin.closed != 1 || len(admin.reqs) != 1 {
		t.Fatalf("launches %d, closed %d, requests %v", launches, admin.closed, admin.reqs)
	}
	if got := admin.reqs[0]; got.Op != elevate.ShareOpFreeAddr || got.FreeIP != hotspotIP {
		t.Errorf("request = %+v", got)
	}
	if addrs.reads != 2 {
		t.Errorf("addresses read %d times, want a check before and after", addrs.reads)
	}

	// Nothing to free: no prompt at all.
	if err := r.s.FreeAddress(context.Background(), hotspotIP); err != nil || launches != 1 {
		t.Errorf("a free address: err %v, launches %d", err, launches)
	}

	// Windows moves the address to another Wi-Fi Direct adapter.
	addrs.set(hotspotIP, wfdHolder)
	moved := wfdHolder2
	admin.moveTo = &moved
	if err := r.s.FreeAddress(context.Background(), hotspotIP); !errors.Is(err, recv.ErrStillHeld) {
		t.Errorf("moved address: err = %v", err)
	}
	if admin.closed != launches {
		t.Errorf("the worker was not closed every time: %d of %d", admin.closed, launches)
	}

	// A declined prompt or a worker refusal is an error, and the worker is
	// still closed.
	admin.moveTo = nil
	addrs.set(hotspotIP, wfdHolder)
	admin.shareErr = errors.New("refused")
	if err := r.s.FreeAddress(context.Background(), hotspotIP); err == nil || admin.closed != launches {
		t.Errorf("worker failure: err %v, closed %d of %d", err, admin.closed, launches)
	}
	r.deps.Admin = func(context.Context) (recv.Admin, error) { return nil, &elevate.DeclinedError{} }
	r.s = recv.New(r.deps)
	var declined *elevate.DeclinedError
	if err := r.s.FreeAddress(context.Background(), hotspotIP); !errors.As(err, &declined) {
		t.Errorf("declined prompt: err = %v", err)
	}
	if err := r.s.FreeAddress(context.Background(), "8.8.8.8"); err == nil {
		t.Error("a public address was sent to the worker")
	}
}

func TestOnlyVirtualAdaptersAreFixable(t *testing.T) {
	for name, hs := range map[string][]winapi.AddressHolder{
		"Ethernet":             {ethHolder},
		"the Wi-Fi card":       {cardHolder},
		"virtual and Ethernet": {wfdHolder, ethHolder},
	} {
		t.Run(name, func(t *testing.T) {
			addrs := &fakeAddrs{}
			addrs.set(hotspotIP, hs...)
			admin := &fakeAdmin{addrs: addrs}
			launches := 0
			r := clashRig(t, nil, addrs, admin, &launches)
			c, err := r.s.CheckHost(context.Background(), hotspotIP)
			if err != nil || !c.Local || c.Fixable {
				t.Fatalf("check = %+v, %v", c, err)
			}
			if !strings.Contains(c.Reason(), "network settings") || speedWords.MatchString(c.Reason()) {
				t.Errorf("reason = %q", c.Reason())
			}
			if err := r.s.FreeAddress(context.Background(), hotspotIP); !errors.Is(err, recv.ErrNotFixable) {
				t.Errorf("err = %v", err)
			}
			if launches != 0 || len(admin.reqs) != 0 {
				t.Error("the worker was launched for an adapter Devpit never changes")
			}
		})
	}
	addrs := &fakeAddrs{}
	addrs.set(hotspotIP, wfdHolder, wfdHolder2)
	launches := 0
	r := clashRig(t, nil, addrs, &fakeAdmin{addrs: addrs}, &launches)
	if c, _ := r.s.CheckHost(context.Background(), hotspotIP); !c.Fixable || len(c.Holders) != 2 {
		t.Errorf("two Wi-Fi Direct holders: %+v", c)
	}
}

func TestJoinReportsAnAddressClash(t *testing.T) {
	addrs := &fakeAddrs{}
	addrs.set(hotspotIP, wfdHolder)
	launches := 0
	w := newFakeWifi()
	r := clashRig(t, w, addrs, &fakeAdmin{addrs: addrs}, &launches)
	res := mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	if res.Host != hotspotIP || !res.Check.Local || !res.Check.Fixable || res.Check.Host != hotspotIP {
		t.Errorf("result = %+v", res)
	}
	if launches != 0 {
		t.Error("Join launched the worker")
	}

	// A healthy join reports a healthy check.
	addrs2 := &fakeAddrs{}
	r2 := clashRig(t, newFakeWifi(), addrs2, &fakeAdmin{addrs: addrs2}, &launches)
	res = mustJoin(t, r2, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	if res.Check.Local || !res.Check.Reachable || res.Check.Host != hotspotIP {
		t.Errorf("healthy check = %+v", res.Check)
	}

	// A failed read is not a failed join.
	addrs3 := &fakeAddrs{err: errors.New("no table")}
	r3 := clashRig(t, newFakeWifi(), addrs3, &fakeAdmin{addrs: addrs3}, &launches)
	res = mustJoin(t, r3, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	if res.Check.Host != "" {
		t.Errorf("a failed check filled the result: %+v", res.Check)
	}
}

func TestAHotspotCopyStopsWhenTheAddressComesBack(t *testing.T) {
	errLine := "2026/09/29 10:15:02 ERROR 86 (0x00000056) Copying File D:\\Games\\c\n"
	addrs := &fakeAddrs{}
	launches := 0
	w := newFakeWifi()
	r := clashRig(t, w, addrs, &fakeAdmin{addrs: addrs}, &launches, step{text: files("a") + errLine, code: 9})
	mustJoin(t, r, recv.JoinRequest{SSID: hotspot, Password: hotspotPW})
	j := r.s.Start(hotspotIP, "Games", "devpit-x7k2", `D:\Games`, robocopyPlan(3000, 3))
	// Windows puts the old hotspot address on another Wi-Fi Direct adapter.
	addrs.set(hotspotIP, wfdHolder2)
	out, err := r.s.Run(context.Background(), j, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Paused || !out.Check.Local || !out.Check.Fixable || out.Message != out.Check.Reason() {
		t.Errorf("outcome = %+v", out)
	}
	if len(r.runner.runs) != 1 {
		t.Errorf("robocopy ran %d times against this PC itself, want 1", len(r.runner.runs))
	}
	saved, ok, _ := r.s.LoadJob()
	if !ok || saved.LastError != out.Message {
		t.Errorf("saved job = %+v", saved)
	}
}
