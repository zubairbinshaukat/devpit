package host

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	mrand "math/rand"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
)

// seeded returns a reproducible random source, so a test that checks the
// shape of a name or a password is not flaky. Production always uses
// crypto/rand; a test below checks that is the default.
func seeded(seed int64) *mrand.Rand { return mrand.New(mrand.NewSource(seed)) } //nolint:gosec // a test source on purpose

func TestPasswordIsSixDigitsAndSafeToTypeInACommand(t *testing.T) {
	if PasswordLength != 6 {
		t.Fatalf("PasswordLength = %d, want 6", PasswordLength)
	}
	sixDigits := regexp.MustCompile(`^[0-9]{6}$`)
	r := seeded(1)
	seen := map[string]bool{}
	leadingZero := false
	for range 2000 {
		pw, err := Password(r, "devpit-abcd")
		if err != nil {
			t.Fatal(err)
		}
		if !sixDigits.MatchString(pw) {
			t.Fatalf("%q is not 6 digits", pw)
		}
		if pw[0] == '0' {
			leadingZero = true
		}
		seen[pw] = true
	}
	if !leadingZero {
		t.Error("no code with a leading zero in 2000; digits are not uniform")
	}
	if len(seen) < 1900 {
		t.Errorf("only %d different passwords in 2000", len(seen))
	}
}

func TestPasswordDigitsAreRoughlyUniform(t *testing.T) {
	r := seeded(7)
	var count [10]int
	for range 5000 {
		pw, err := Password(r, "u")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range pw {
			count[c-'0']++
		}
	}
	// 30000 digits, 3000 expected each; a bias bug would be far outside this.
	for d, n := range count {
		if n < 2700 || n > 3300 {
			t.Errorf("digit %d appeared %d times, want about 3000", d, n)
		}
	}
}

func TestPasswordUsesOnlyWhatTheReaderGives(t *testing.T) {
	// A zero-filled reader gives the lowest value every time: leading zeros
	// are kept, not trimmed as a number would be.
	pw, err := Password(zeroReader{}, "u")
	if err != nil || pw != "000000" {
		t.Errorf("Password(zeros) = %q, %v; want 000000", pw, err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestPasswordUsesCryptoRandWhenNothingIsInjected(t *testing.T) {
	d := Deps{}.withDefaults()
	if d.Rand != rand.Reader {
		t.Error("the default random source is not crypto/rand")
	}
	a, _ := Password(d.Rand, "u")
	b, _ := Password(d.Rand, "u")
	if a == b {
		t.Error("two passwords from crypto/rand were the same")
	}
}

func TestPasswordFailsCleanlyWhenTheRandomSourceFails(t *testing.T) {
	if _, err := Password(failingReader{}, "u"); err == nil {
		t.Error("no error from a failing random source")
	}
	if _, err := UserName(failingReader{}); err == nil {
		t.Error("no error from a failing random source")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestNames(t *testing.T) {
	user := regexp.MustCompile(`^devpit-[a-z0-9]{4}$`)
	for i := range 50 {
		u, err := UserName(seeded(int64(i)))
		if err != nil || !user.MatchString(u) {
			t.Fatalf("user %q, %v", u, err)
		}
	}
	shareOK := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	tests := map[string]string{
		"Games":                   "Games-",
		"My Games":                "My-Games-",
		"Été 写真":                  "t-",
		"...":                     "Share-",
		"":                        "Share-",
		strings.Repeat("a", 60):   strings.Repeat("a", 24) + "-",
		"a'; Remove-Item *; '":    "a-Remove-Item-",
		`weird\name/with:chars*?`: "weird-name-with-chars-",
	}
	for folder, prefix := range tests {
		got, err := ShareName(seeded(1), folder)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(got, prefix) || !shareOK.MatchString(got) {
			t.Errorf("ShareName(%q) = %q, want prefix %q and a valid name", folder, got, prefix)
		}
	}
}

func TestPickAdapters(t *testing.T) {
	ip := func(s string) []net.IP { return []net.IP{net.ParseIP(s)} }
	ifs := []Iface{
		{Name: "Loopback Pseudo-Interface 1", Index: 1, IPv4: ip("127.0.0.1"), Up: true, Loopback: true},
		{Name: "vEthernet (WSL)", Index: 9, IPv4: ip("172.20.0.1"), Up: true},
		{Name: "VirtualBox Host-Only Network", Index: 10, IPv4: ip("192.168.56.1"), Up: true},
		{Name: "Wi-Fi", Index: 12, IPv4: ip("192.168.1.20"), Up: true},
		{Name: "Ethernet", Index: 4, IPv4: ip("10.0.0.5"), Up: true},
		{Name: "Ethernet 2", Index: 5, IPv4: ip("192.168.0.9"), Up: false},
		{Name: "Tailscale", Index: 14, IPv4: ip("100.64.0.3"), Up: true},
		{Name: "Cable", Index: 6, IPv4: ip("169.254.3.4"), Up: true},
		{Name: "Public Wi-Fi", Index: 7, IPv4: ip("8.8.4.4"), Up: true},
	}
	got := PickAdapters(ifs, 12)
	var names []string
	for _, a := range got {
		names = append(names, a.Name)
	}
	if strings.Join(names, ",") != "Wi-Fi,Ethernet" {
		t.Fatalf("adapters = %v, want the best route first, then the other real LAN adapter", names)
	}
	if !got[0].Best || got[1].Best {
		t.Errorf("best flags = %v %v", got[0].Best, got[1].Best)
	}
	if got := PickAdapters(ifs, 0); got[0].Name != "Ethernet" {
		t.Errorf("with no best route, order = %v, want by index", got)
	}
	if len(PickAdapters(nil, 0)) != 0 {
		t.Error("adapters from nothing")
	}
}

func TestCommandsCarryEverythingAndAreSafeToPaste(t *testing.T) {
	netUse, robo, cleanup := Commands("192.168.1.5", "Games-x7k2", "devpit-abcd", "Ab3dEf7hJk9mNp2q")
	if netUse != `net use \\192.168.1.5\Games-x7k2 /user:devpit-abcd Ab3dEf7hJk9mNp2q` {
		t.Error(netUse)
	}
	if strings.Contains(robo, `:\`) || strings.Contains(robo, "%") {
		t.Errorf("the copy line must not assume a drive or a cmd-only variable: %q", robo)
	}
	for _, want := range []string{`robocopy \\192.168.1.5\Games-x7k2 ".\Games-x7k2"`, "/E", "/MT:16", "/Z", "/R:3", "/W:5", "/XO"} {
		if !strings.Contains(robo, want) {
			t.Errorf("robocopy line %q lacks %q", robo, want)
		}
	}
	if cleanup != `net use \\192.168.1.5\Games-x7k2 /delete` {
		t.Error(cleanup)
	}
}

func TestParseCategory(t *testing.T) {
	for in, want := range map[string]Category{"Private\r\n": CategoryPrivate, " Public ": CategoryPublic, "DomainAuthenticated": CategoryDomain} {
		if got, err := parseCategory(in); err != nil || got != want {
			t.Errorf("parseCategory(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := parseCategory("Privé"); err == nil {
		t.Error("a translated word was accepted")
	}
}

// fakeAdmin records every operation and can be told to fail one.
type fakeAdmin struct {
	mu       sync.Mutex
	ops      []elevate.ShareRequest
	failOp   elevate.ShareOp
	failErr  error
	closed   int
	onOp     func(elevate.ShareRequest)
	fwLines  []string
	sidLines []string
	// shareLines is what the share step reports, such as a 24H2 rule that
	// creating the share turned on.
	shareLines []string
	// hsLines is what the hotspot op reports, played back line by line (the
	// worker's machine lines and progress text).
	hsLines []string
	// onLine sees each line played back, after the manager has handled it.
	onLine func(op elevate.ShareOp, line string)
}

func (f *fakeAdmin) Share(_ context.Context, req elevate.ShareRequest, onLine func(string)) error {
	f.mu.Lock()
	f.ops = append(f.ops, req)
	hook := f.onOp
	fail := f.failOp == req.Op
	seen := f.onLine
	f.mu.Unlock()
	if hook != nil {
		hook(req)
	}
	if onLine != nil {
		var lines []string
		switch req.Op {
		case elevate.ShareOpFirewall:
			lines = f.fwLines
		case elevate.ShareOpAccount:
			lines = f.sidLines
		case elevate.ShareOpShare:
			lines = f.shareLines
		case elevate.ShareOpHotspot:
			lines = f.hsLines
		}
		for _, l := range lines {
			onLine(l)
			if seen != nil {
				seen(req.Op, l)
			}
		}
	}
	if fail {
		return f.failErr
	}
	return nil
}

func (f *fakeAdmin) Close() error {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
	return nil
}

func (f *fakeAdmin) opNames() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var s []string
	for _, o := range f.ops {
		s = append(s, string(o.Op))
	}
	return strings.Join(s, ",")
}

// fakeNet answers adapter and category questions.
type fakeNet struct {
	adapters []Adapter
	cat      Category
	err      error
}

func (f fakeNet) Adapters(context.Context) ([]Adapter, error) { return f.adapters, f.err }
func (f fakeNet) CategoryOf(context.Context, uint32) (Category, error) {
	return f.cat, f.err
}

var lan = Adapter{Name: "Ethernet", Index: 4, IP: net.ParseIP("192.168.1.5"), Best: true}

// rig builds a manager over fakes, with the manifest in a temp directory.
type rig struct {
	m        *Manager
	admin    *fakeAdmin
	dir      string
	launches int
	awake    int
	released int
}

func newRig(t *testing.T, cat Category) *rig {
	t.Helper()
	unregisterAll()
	r := &rig{
		dir: t.TempDir(),
		admin: &fakeAdmin{
			fwLines:  []string{"FW=FPS-SMB-In-TCP", "not a rule line"},
			sidLines: []string{"SID=S-1-5-21-1-2-3-1004"},
		},
	}
	r.m = NewManager(Deps{
		Launch: func(context.Context) (Admin, error) { r.launches++; return r.admin, nil },
		Net:    fakeNet{adapters: []Adapter{lan}, cat: cat},
		Dir:    r.dir,
		Rand:   seeded(7),
		Now:    func() time.Time { return time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC) },
		KeepAwake: func() (func(), error) {
			r.awake++
			return func() { r.released++ }, nil
		},
		PID:    4321,
		Getenv: env,
	})
	return r
}

func TestStartRunsTheStepsInOrderAndShowsTheCard(t *testing.T) {
	r := newRig(t, CategoryPrivate)
	var steps []string
	card, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, func(s Step) { steps = append(steps, s.Key) })
	if err != nil {
		t.Fatal(err)
	}
	if got := r.admin.opNames(); got != "firewall,account,grant,share" {
		t.Errorf("operations = %s (a Private network needs no profile switch)", got)
	}
	if strings.Join(steps, ",") != "firewall,login,folder,share" {
		t.Errorf("steps = %v", steps)
	}
	if r.launches != 1 {
		t.Errorf("the worker was launched %d times, want one (one UAC prompt)", r.launches)
	}
	if card.IP != "192.168.1.5" || !strings.HasPrefix(card.User, "devpit-") || len(card.Password) != PasswordLength ||
		!strings.HasPrefix(card.Share, "Games-") || card.Path != `D:\Games` {
		t.Errorf("card = %+v", card)
	}
	if !strings.Contains(card.NetUse, card.User) || !strings.Contains(card.NetUse, card.Password) ||
		!strings.Contains(card.Robocopy, card.Share) {
		t.Errorf("commands do not match the card: %q / %q", card.NetUse, card.Robocopy)
	}
	got, ok := r.m.Card()
	if !ok || got != card || !r.m.Active() {
		t.Error("the manager does not report the running share")
	}
	if r.awake != 1 || r.released != 0 {
		t.Errorf("keep-awake: %d starts, %d releases while sharing", r.awake, r.released)
	}
	// The account request carried the password; nothing else did.
	for _, op := range r.admin.ops {
		if op.Op != elevate.ShareOpAccount && op.Password != "" {
			t.Errorf("%s carried the password", op.Op)
		}
		if op.Op == elevate.ShareOpAccount && op.Password != card.Password {
			t.Error("the account was not given the card's password")
		}
	}
}

func TestTheManifestIsOnDiskBeforeAnythingIsCreatedAndNeverHoldsThePassword(t *testing.T) {
	r := newRig(t, CategoryPrivate)
	var atFirstOp Manifest
	var seen bool
	r.admin.onOp = func(req elevate.ShareRequest) {
		if !seen {
			seen = true
			m, ok, err := LoadManifest(r.dir)
			if err != nil || !ok {
				t.Errorf("no manifest when the first operation ran: %v %v", ok, err)
			}
			atFirstOp = m
		}
	}
	card, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if atFirstOp.User != card.User || atFirstOp.Share != card.Share || atFirstOp.Path != `D:\Games` || atFirstOp.PID != 4321 {
		t.Errorf("the manifest at the first operation = %+v", atFirstOp)
	}
	final, _, _ := LoadManifest(r.dir)
	if final.SID != "S-1-5-21-1-2-3-1004" || len(final.Rules) != 1 || final.Rules[0] != "FPS-SMB-In-TCP" {
		t.Errorf("final manifest = %+v, want the SID and the rule that was turned on", final)
	}
	raw, err := os.ReadFile(manifestPath(r.dir, card.User))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), card.Password) || strings.Contains(strings.ToLower(string(raw)), "password") {
		t.Errorf("the manifest mentions the password:\n%s", raw)
	}
	info, _ := os.Stat(manifestPath(r.dir, card.User))
	if runtimeIsUnix() && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("manifest mode %v is readable by others", info.Mode())
	}
}

func runtimeIsUnix() bool { return os.PathSeparator == '/' }

func TestAPublicNetworkAsksFirstAndIsSwitchedAndRecorded(t *testing.T) {
	r := newRig(t, CategoryPublic)
	_, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil)
	if !errors.Is(err, ErrNeedPrivate) {
		t.Fatalf("err = %v, want ErrNeedPrivate", err)
	}
	if r.launches != 0 || r.m.Active() {
		t.Error("something started before the user agreed")
	}
	if _, ok, _ := LoadManifest(r.dir); ok {
		t.Error("a manifest was written before the user agreed")
	}

	if _, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan, MakePrivate: true}, nil); err != nil {
		t.Fatal(err)
	}
	if got := r.admin.opNames(); got != "profile,firewall,account,grant,share" {
		t.Errorf("operations = %s", got)
	}
	p := r.admin.ops[0]
	if p.Category != "Private" || p.Original != "Public" || p.Index != 4 {
		t.Errorf("profile request = %+v, want Public restored later", p)
	}
	man, _, _ := LoadManifest(r.dir)
	if man.ProfileIndex != 4 || man.ProfileOriginal != "Public" {
		t.Errorf("manifest = %+v, want the original category recorded", man)
	}
	if c, _ := r.m.Card(); !c.SwitchedNetwork {
		t.Error("the card does not say the network was switched")
	}
}

func TestADomainNetworkIsLeftAlone(t *testing.T) {
	r := newRig(t, CategoryDomain)
	if _, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.admin.opNames(), "profile") {
		t.Error("a domain network was switched")
	}
}

func TestStopUndoesEverythingAndIsSafeToRepeat(t *testing.T) {
	r := newRig(t, CategoryPrivate)
	if _, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(r.admin.opNames(), "share,stop") {
		t.Errorf("operations = %s", r.admin.opNames())
	}
	if _, ok, _ := LoadManifest(r.dir); ok {
		t.Error("the manifest is still there after a clean stop")
	}
	if r.admin.closed != 1 || r.released != 1 || r.m.Active() {
		t.Errorf("closed %d, released %d, active %v", r.admin.closed, r.released, r.m.Active())
	}
	if _, ok := r.m.Card(); ok {
		t.Error("the card is still shown")
	}
	if err := r.m.Stop(context.Background()); err != nil || r.admin.closed != 1 {
		t.Errorf("a second stop did something: %v", err)
	}
}

func TestAFailedStopKeepsTheManifestSoTheNextLaunchCanFinish(t *testing.T) {
	r := newRig(t, CategoryPrivate)
	if _, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil); err != nil {
		t.Fatal(err)
	}
	r.admin.failOp, r.admin.failErr = elevate.ShareOpStop, errors.New("removing the folder permission: access denied")
	if err := r.m.Stop(context.Background()); err == nil {
		t.Fatal("a failed stop reported success")
	}
	if _, ok, _ := LoadManifest(r.dir); !ok {
		t.Error("the manifest was deleted although the cleanup did not finish")
	}
	if !r.m.Active() || r.released != 0 {
		t.Error("the share was forgotten although it is not gone")
	}
	r.admin.failOp = ""
	if err := r.m.Stop(context.Background()); err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
	if _, ok, _ := LoadManifest(r.dir); ok {
		t.Error("manifest left after the retry worked")
	}
}

func TestAFailureDuringSetupRollsEverythingBack(t *testing.T) {
	for _, failing := range []elevate.ShareOp{elevate.ShareOpFirewall, elevate.ShareOpAccount, elevate.ShareOpGrant, elevate.ShareOpShare} {
		t.Run(string(failing), func(t *testing.T) {
			r := newRig(t, CategoryPrivate)
			r.admin.failOp, r.admin.failErr = failing, errors.New("boom")
			_, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil)
			if err == nil || !strings.Contains(err.Error(), "boom") {
				t.Fatalf("err = %v", err)
			}
			ops := r.admin.opNames()
			if !strings.HasSuffix(ops, ",stop") && !strings.HasSuffix(ops, string(failing)+",stop") {
				t.Errorf("operations = %s, want a stop after the failure", ops)
			}
			if _, ok, _ := LoadManifest(r.dir); ok {
				t.Error("manifest left after a rolled back setup")
			}
			if r.admin.closed != 1 || r.m.Active() {
				t.Error("the worker was not closed")
			}
			if r.awake != 0 {
				t.Error("the PC was kept awake for a share that never started")
			}
		})
	}
}

func TestADeclinedPromptChangesNothing(t *testing.T) {
	unregisterAll()
	dir := t.TempDir()
	m := NewManager(Deps{
		Launch: func(context.Context) (Admin, error) { return nil, &elevate.DeclinedError{} },
		Net:    fakeNet{adapters: []Adapter{lan}, cat: CategoryPrivate}, Dir: dir, Rand: seeded(1),
	})
	_, err := m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil)
	var declined *elevate.DeclinedError
	if !errors.As(err, &declined) {
		t.Fatalf("err = %v", err)
	}
	if _, ok, _ := LoadManifest(dir); ok || m.Active() {
		t.Error("a declined prompt left state behind")
	}
}

func TestStartTwiceIsRefused(t *testing.T) {
	r := newRig(t, CategoryPrivate)
	if _, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil); err == nil {
		t.Error("a second share was started on top of the first")
	}
}

func TestShutdownAllStopsWhatIsRunning(t *testing.T) {
	r := newRig(t, CategoryPrivate)
	if _, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil); err != nil {
		t.Fatal(err)
	}
	if err := ShutdownAll(); err != nil {
		t.Fatal(err)
	}
	if r.m.Active() || !strings.HasSuffix(r.admin.opNames(), "stop") {
		t.Errorf("ShutdownAll left the share up: %s", r.admin.opNames())
	}
	if err := ShutdownAll(); err != nil {
		t.Errorf("a second ShutdownAll failed: %v", err)
	}
}

func TestStopFromAnotherGoroutineWhileTheCardIsRead(t *testing.T) {
	r := newRig(t, CategoryPrivate)
	if _, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(3)
		go func() { defer wg.Done(); _ = r.m.Stop(context.Background()) }()
		go func() { defer wg.Done(); r.m.Card() }()
		go func() { defer wg.Done(); r.m.Active() }()
	}
	wg.Wait()
	if r.m.Active() {
		t.Error("still active after the stops")
	}
	if r.admin.closed != 1 {
		t.Errorf("worker closed %d times, want once", r.admin.closed)
	}
	if r.released != 1 {
		t.Errorf("keep-awake released %d times, want once", r.released)
	}
}

func TestLeftover(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := Leftover(dir, nil, 1); ok || err != nil {
		t.Fatalf("with no manifest: %v %v", ok, err)
	}
	if err := saveManifest(dir, Manifest{PID: 500, User: "devpit-abcd", Path: `D:\Games`, Share: "Games-x7k2"}); err != nil {
		t.Fatal(err)
	}
	alive := map[int]bool{500: true}
	isAlive := func(pid int, _ time.Time) bool { return alive[pid] }

	if _, ok, _ := Leftover(dir, isAlive, 1); ok {
		t.Error("a share that a live Devpit owns was offered for cleanup")
	}
	if _, ok, _ := Leftover(dir, isAlive, 500); ok {
		t.Error("this process's own share was offered for cleanup")
	}
	alive[500] = false
	m, ok, err := Leftover(dir, isAlive, 1)
	if !ok || err != nil || m.User != "devpit-abcd" {
		t.Errorf("a dead owner's share = %+v %v %v", m, ok, err)
	}

	// A hotspot share's record comes back whole, so its cleanup can stop the
	// hotspot and remove its rule.
	hs := Manifest{
		PID: 501, User: "devpit-wxyz", Path: `D:\Movies`, Share: "Movies-x7k2", Mode: ModeHotspot,
		HotspotConfig: "temp", HotspotSSID: "Devpit-wxyz", HotspotRule: "devpit-hs-wxyz2345",
		HotspotStarted: true, HotspotTimeoutWasOn: true, HotspotTimeoutChanged: true, HotspotLegacy: true,
		StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if serr := saveManifest(dir, hs); serr != nil {
		t.Fatal(serr)
	}
	alive[500] = true // only the hotspot share's owner is gone now
	got, ok, err := Leftover(dir, isAlive, 1)
	if !ok || err != nil || got.User != "devpit-wxyz" {
		t.Fatalf("the hotspot share's leftover = %+v %v %v", got, ok, err)
	}
	got.file = ""
	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", func() Manifest { h := hs; h.Version = manifestVersion; return h }()) {
		t.Errorf("the hotspot record did not round-trip:\n got %+v\nwant %+v", got, hs)
	}
}

func TestAVersion1ManifestIsStillRead(t *testing.T) {
	dir := t.TempDir()
	// Exactly what a 0.5.0 build wrote: no mode, no hotspot fields.
	raw := `{"version":1,"pid":7,"started_at":"2026-09-29T10:00:00Z","user":"devpit-abcd","sid":"S-1-5-21-1-2-3-1004",` +
		`"path":"D:\\Games","granted":true,"share":"Games-x7k2","profile_index":4,"profile_original":"Public","rules":["FPS-SMB-In-TCP"]}`
	if err := os.WriteFile(manifestPath(dir, "devpit-abcd"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	m, ok, err := LoadManifest(dir)
	if !ok || err != nil {
		t.Fatalf("a version 1 manifest was not read: %v %v", ok, err)
	}
	if m.Version != 1 || m.User != "devpit-abcd" || m.Mode != ModeNormal || m.HotspotConfig != "" || m.HotspotStarted {
		t.Errorf("version 1 manifest = %+v", m)
	}
	req := m.request()
	if err := req.Validate(); err != nil {
		t.Errorf("the worker would refuse a version 1 cleanup: %v", err)
	}
	if req.HotspotConfig != "" || req.HotspotRule != "" || req.HotspotStarted {
		t.Errorf("a version 1 cleanup asks for hotspot work: %+v", req)
	}
	if manifestVersion != 2 {
		t.Errorf("manifestVersion = %d, want 2", manifestVersion)
	}
}

// TestTheCardCommandsMatchTheCopyArgs ties the card's robocopy line for a PC
// without Devpit to the copy Devpit itself runs: every copy-behaviour flag of
// robocopy.CopyArgs is on the card, the card has no flag the real copy does
// not, and a new flag in CopyArgs fails here until it is placed on one side.
func TestTheCardCommandsMatchTheCopyArgs(t *testing.T) {
	_, robo, _ := Commands("192.168.137.1", "Games-x7k2", "devpit-abcd", "123456")
	args := robocopy.CopyArgs(`\\192.168.137.1\Games-x7k2`, `.\Games-x7k2`, `C:\log.txt`)
	// Flags of Devpit's own copy that a person typing the line does not need:
	// output shaping for Devpit's log parser, the log file itself, and /XJ.
	// /XJ (skip junctions) is a behaviour flag the card does not carry yet;
	// it is listed here on purpose so adding it to the card is a choice.
	devpitOnly := map[string]bool{"/XJ": true, "/NP": true, "/BYTES": true, "/NJH": true, "/NDL": true, "/FP": true, "/NC": true}
	cardFlags := map[string]bool{}
	for _, f := range strings.Fields(robo) {
		if strings.HasPrefix(f, "/") {
			cardFlags[f] = true
		}
	}
	argFlags := map[string]bool{}
	for _, a := range args[2:] {
		if strings.HasPrefix(a, "/UNILOG:") {
			continue
		}
		argFlags[a] = true
		if devpitOnly[a] {
			continue
		}
		if !cardFlags[a] {
			t.Errorf("CopyArgs has %s but the card's robocopy line does not: %q", a, robo)
		}
	}
	for f := range cardFlags {
		if !argFlags[f] {
			t.Errorf("the card's robocopy line has %s, which Devpit's own copy does not use", f)
		}
	}
	for f := range devpitOnly {
		if !argFlags[f] {
			t.Errorf("%s is listed as Devpit-only but CopyArgs no longer has it; update the list", f)
		}
	}
	if !strings.HasPrefix(robo, `robocopy \\192.168.137.1\Games-x7k2 ".\Games-x7k2" `) {
		t.Errorf("the card's copy does not start with the share and the destination: %q", robo)
	}
}

func TestLeftoverReportsADamagedManifestInsteadOfIgnoringIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(manifestPath(dir, "devpit-abcd"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Leftover(dir, nil, 1); ok || err == nil {
		t.Errorf("damaged manifest: ok=%v err=%v", ok, err)
	}
}

func TestCleanUpSendsTheManifestToTheWorkerAndRemovesIt(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{
		PID: 9, User: "devpit-abcd", SID: "S-1-5-21-1-2-3-1004", Path: `D:\Games`, Granted: true, Share: "Games-x7k2",
		ProfileIndex: 4, ProfileOriginal: "Public", Rules: []string{"FPS-SMB-In-TCP"},
		Mode: ModeHotspot, HotspotConfig: "saved", HotspotSSID: "My Hotspot", HotspotRule: "devpit-hs-abcd2345",
		HotspotStarted: true, HotspotTimeoutWasOn: true, HotspotTimeoutChanged: true,
	}
	if err := saveManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	admin := &fakeAdmin{}
	err := CleanUp(context.Background(), dir, m, func(context.Context) (Admin, error) { return admin, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(admin.ops) != 1 || admin.ops[0].Op != elevate.ShareOpCleanup {
		t.Fatalf("ops = %+v", admin.ops)
	}
	op := admin.ops[0]
	if op.User != m.User || op.SID != m.SID || op.Path != m.Path || op.Name != m.Share || op.Index != 4 ||
		op.Original != "Public" || len(op.Rules) != 1 {
		t.Errorf("cleanup request = %+v", op)
	}
	// The hotspot record reaches the worker, so cleanup also stops a
	// leftover Devpit hotspot, removes its rule and puts the timeout back.
	if op.HotspotConfig != "saved" || op.HotspotSSID != "My Hotspot" || op.HotspotRule != "devpit-hs-abcd2345" ||
		!op.HotspotStarted || !op.HotspotTimeoutWasOn || !op.HotspotTimeoutChanged || op.HotspotLegacy {
		t.Errorf("cleanup request lost the hotspot record: %+v", op)
	}
	if op.SSID != "" || op.Passphrase != "" || op.Band != "" || op.SetBand5 {
		t.Errorf("a cleanup carries a hotspot name, password or band to set: %+v", op)
	}
	// A temporary hotspot on older Windows is recorded as legacy, and the
	// worker accepts that record too.
	legacy := m
	legacy.HotspotConfig, legacy.HotspotSSID, legacy.HotspotLegacy = "temp", "Devpit-abcd", true
	if err := legacy.request().Validate(); err != nil {
		t.Errorf("the worker would refuse a legacy hotspot cleanup: %v", err)
	}
	if err := op.Validate(); err != nil {
		t.Errorf("the worker would refuse the cleanup request: %v", err)
	}
	if _, ok, _ := LoadManifest(dir); ok || admin.closed != 1 {
		t.Error("manifest kept or worker left open")
	}
}

func TestCleanUpKeepsTheManifestWhenTheWorkerCannotFinish(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{User: "devpit-abcd"}
	if err := saveManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	admin := &fakeAdmin{failOp: elevate.ShareOpCleanup, failErr: errors.New("still there")}
	if err := CleanUp(context.Background(), dir, m, func(context.Context) (Admin, error) { return admin, nil }); err == nil {
		t.Error("no error")
	}
	if _, ok, _ := LoadManifest(dir); !ok {
		t.Error("manifest removed although the cleanup failed")
	}
	declined := func(context.Context) (Admin, error) { return nil, &elevate.DeclinedError{} }
	if err := CleanUp(context.Background(), dir, m, declined); err == nil {
		t.Error("declined prompt reported success")
	}
	if _, ok, _ := LoadManifest(dir); !ok {
		t.Error("manifest removed after a declined prompt")
	}
}

func TestManifestPathRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := Manifest{PID: 3, User: "devpit-abcd", Rules: []string{"a", "b"}, StartedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	if err := saveManifest(dir, in); err != nil {
		t.Fatal(err)
	}
	out, ok, err := LoadManifest(dir)
	if !ok || err != nil || fmt.Sprint(out.Rules) != "[a b]" || out.Version != manifestVersion || !out.StartedAt.Equal(in.StartedAt) {
		t.Errorf("round trip = %+v %v %v", out, ok, err)
	}
}

// On 24H2 creating the share can turn on FPS-SMB-In-TCP-V2 by itself. The
// worker reports it from the share step, and it must reach the manifest, also
// when the share step then fails, so the next launch after a crash turns it
// off again. A rule reported twice is recorded once.
func TestARuleTheShareStepTurnedOnIsRecordedForCleanup(t *testing.T) {
	for _, failShare := range []bool{false, true} {
		r := newRig(t, CategoryPrivate)
		r.admin.shareLines = []string{"FW=FPS-SMB-In-TCP-V2", "FW=FPS-SMB-In-TCP"}
		if failShare {
			r.admin.failOp, r.admin.failErr = elevate.ShareOpShare, errors.New("New-SmbShare failed")
		}
		_, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, func(Step) {})
		if (err != nil) != failShare {
			t.Fatalf("failShare=%v: err = %v", failShare, err)
		}
		m, ok, lerr := LoadManifest(r.dir)
		if failShare {
			// A failed start is taken down at once by the worker's own stop,
			// which undoes every rule its session turned on, the V2 rule
			// included; the manifest is gone because nothing is left.
			if got := r.admin.opNames(); !strings.HasSuffix(got, "share,stop") {
				t.Errorf("failed share: operations = %s, want a stop right after the share step", got)
			}
			continue
		}
		if !ok || lerr != nil || fmt.Sprint(m.Rules) != "[FPS-SMB-In-TCP FPS-SMB-In-TCP-V2]" {
			t.Errorf("manifest rules = %v (%v, %v), want the firewall step's rule and the share step's, once each", m.Rules, ok, lerr)
		}
	}
}
