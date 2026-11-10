package host

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// b64 is how the worker sends a hotspot name.
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// The PC of the hotspot tests: on Wi-Fi 6, 5 GHz, with a saved hotspot.
var (
	wlan = Adapter{
		Name: "Wi-Fi", Index: 12, IP: net.ParseIP("192.168.1.20").To4(), Best: true,
		GUID: "{11111111-2222-3333-4444-555555555555}", LinkBps: 1_201_000_000, Wireless: true, Standard: "Wi-Fi 6",
	}
	hsAdapter = Adapter{
		Name: "Local Area Connection* 10", Index: 23, IP: net.ParseIP("192.168.137.1").To4(),
		GUID: "{99999999-2222-3333-4444-555555555555}", LinkBps: 866_700_000, Wireless: true,
	}
	wlanCard = winapi.WifiInterface{
		GUID: wlan.GUID, Description: "Intel(R) Wi-Fi 6 AX201 160MHz", PHYs: []uint32{5, 6, 7, 4, 8, 10},
		Connected: true, PHY: 10, RxKbps: 1_201_000, TxKbps: 1_201_000, Channel: 36,
	}
	savedFacts = HotspotFacts{
		Capability: elevate.TetheringEnabled, State: tetherStateOff,
		SSID: "My Hotspot", Password: "hunter22secret", Band: "5", Band5: true, Session: true,
	}
)

const testRule = "devpit-hs-abcd2345"

// hsOK is what the worker reports for a hotspot that came up, in the order
// the real op sends it.
func hsOK(ssid, band string) []string {
	return []string{
		"Checking the Wi-Fi hotspot", "HSCAP=0", "HSTIMEOUT=1",
		"Keeping the hotspot on while nothing is connected",
		"Starting a direct Wi-Fi hotspot",
		"HSMODE=saved", "HSSSID=" + b64(ssid), "HSSTATUS=0", "HSBAND=" + band,
		"HSIP=192.168.137.1", "HSRULE=" + testRule, "Allowing file sharing for the hotspot only",
		"HSFW=" + testRule, "HS=on",
	}
}

// hsNet is a network view whose adapters change once the hotspot is up, and
// which records every category read.
type hsNet struct {
	mu        sync.Mutex
	before    []Adapter
	after     []Adapter
	up        bool
	cats      map[uint32]Category
	catCalls  []uint32
	catsEarly int // category reads before the hotspot op ran
	hsRan     bool
}

func (n *hsNet) Adapters(context.Context) ([]Adapter, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.up {
		return append([]Adapter(nil), n.after...), nil
	}
	return append([]Adapter(nil), n.before...), nil
}

func (n *hsNet) CategoryOf(_ context.Context, index uint32) (Category, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.catCalls = append(n.catCalls, index)
	if !n.hsRan {
		n.catsEarly++
	}
	c, ok := n.cats[index]
	if !ok {
		return "", errors.New("no such connection")
	}
	return c, nil
}

// hsRig is a manager over fakes for hotspot mode.
type hsRig struct {
	m        *Manager
	admin    *fakeAdmin
	net      *hsNet
	dir      string
	launches int
	counted  Adapter
	steps    []string
	probes   int
}

func newHSRig(t *testing.T, normal Category, lines []string) *hsRig {
	t.Helper()
	unregisterAll()
	r := &hsRig{dir: t.TempDir()}
	r.net = &hsNet{
		before: []Adapter{wlan},
		after:  []Adapter{wlan, hsAdapter},
		cats:   map[uint32]Category{wlan.Index: normal},
	}
	r.admin = &fakeAdmin{
		fwLines:  []string{"FW=FPS-SMB-In-TCP"},
		sidLines: []string{"SID=S-1-5-21-1-2-3-1004"},
		hsLines:  lines,
		onOp: func(req elevate.ShareRequest) {
			if req.Op == elevate.ShareOpHotspot {
				r.net.mu.Lock()
				r.net.hsRan = true
				r.net.mu.Unlock()
			}
		},
	}
	r.admin.onLine = func(op elevate.ShareOp, l string) {
		if op == elevate.ShareOpHotspot && strings.HasPrefix(l, "HSIP=") {
			r.net.mu.Lock()
			r.net.up = true
			r.net.mu.Unlock()
		}
	}
	r.m = NewManager(Deps{
		Launch:    func(context.Context) (Admin, error) { r.launches++; return r.admin, nil },
		Net:       r.net,
		Dir:       r.dir,
		Rand:      seeded(11),
		Now:       func() time.Time { return time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC) },
		KeepAwake: func() (func(), error) { return func() {}, nil },
		PID:       4321,
		Getenv:    env,
		Counter: func(_ string, a Adapter) (func() (uint64, error), ProgressSource, func()) {
			r.counted = a
			return func() (uint64, error) { return 0, nil }, SourceAdapter, func() {}
		},
		Measure: func(context.Context, string) (FolderSize, error) { return FolderSize{}, nil },
		Wifi:    func() ([]winapi.WifiInterface, error) { return []winapi.WifiInterface{wlanCard}, nil },
		HotspotProbe: func(context.Context) (HotspotFacts, error) {
			r.probes++
			return savedFacts, nil
		},
	})
	return r
}

func (r *hsRig) start(t *testing.T, opts Options) (Card, error) {
	t.Helper()
	if opts.Path == "" {
		opts.Path = `D:\Games`
	}
	if opts.Adapter.IP == nil {
		opts.Adapter = wlan
	}
	return r.m.Start(context.Background(), opts, func(s Step) { r.steps = append(r.steps, s.Key) })
}

func TestHotspotStartRunsTheStepsInOrderAndShowsTheCard(t *testing.T) {
	cases := []struct {
		name      string
		choice    HotspotChoice
		lines     []string
		wantSteps string
		wantSSID  string
		wantPass  func(user string) string
		own       bool
	}{
		{
			name: "saved", choice: HotspotSaved, lines: hsOK("My Hotspot", "5"),
			wantSteps: "hotspot,hsfirewall,firewall,login,folder,share", wantSSID: "My Hotspot",
			wantPass: func(string) string { return "hunter22secret" }, own: true,
		},
		{
			name: "saved switched to 5 GHz", choice: HotspotSavedSwitchTo5,
			lines:     append([]string{"Switching your hotspot to 5 GHz"}, append(hsOK("My Hotspot", "5"), "HSBANDSET=1")...),
			wantSteps: "band5,hotspot,hsfirewall,firewall,login,folder,share", wantSSID: "My Hotspot",
			wantPass: func(string) string { return "hunter22secret" }, own: true,
		},
		{
			name: "temporary", choice: HotspotTemp, lines: nil, // filled below, needs the account name
			wantSteps: "hotspot,hsfirewall,firewall,login,folder,share",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newHSRig(t, CategoryPrivate, c.lines)
			r.m.Suggest(context.Background())
			if c.choice == HotspotTemp {
				// The worker echoes the temporary name it was given.
				r.admin.onOp = func(req elevate.ShareRequest) {
					if req.Op == elevate.ShareOpHotspot {
						lines := hsOK(req.SSID, "5")
						lines[5] = "HSMODE=session"
						r.admin.mu.Lock()
						r.admin.hsLines = lines
						r.admin.mu.Unlock()
						r.net.mu.Lock()
						r.net.hsRan = true
						r.net.mu.Unlock()
					}
				}
			}
			card, err := r.start(t, Options{Mode: ModeHotspot, Hotspot: c.choice})
			if err != nil {
				t.Fatal(err)
			}
			if got := r.admin.opNames(); got != "hotspot,firewall,account,grant,share" {
				t.Errorf("operations = %s", got)
			}
			if got := strings.Join(r.steps, ","); got != c.wantSteps {
				t.Errorf("steps = %s, want %s", got, c.wantSteps)
			}
			want := Steps(Options{Mode: ModeHotspot, Hotspot: c.choice}, true)
			var keys []string
			for _, s := range want {
				keys = append(keys, s.Key)
			}
			if strings.Join(keys, ",") != c.wantSteps {
				t.Errorf("Steps() = %v, but Start reported %s", keys, c.wantSteps)
			}
			if r.launches != 1 {
				t.Errorf("launched %d times, want one prompt", r.launches)
			}
			hs := r.admin.ops[0]
			if hs.SetBand5 != (c.choice == HotspotSavedSwitchTo5) {
				t.Errorf("SetBand5 = %v for %s", hs.SetBand5, c.name)
			}
			if err := hs.Validate(); err != nil {
				t.Errorf("the worker would refuse the hotspot request: %v", err)
			}
			wantSSID, wantPass := c.wantSSID, ""
			if c.wantPass != nil {
				wantPass = c.wantPass(card.User)
			}
			if c.choice == HotspotTemp {
				wantSSID, wantPass = "Devpit-"+strings.TrimPrefix(card.User, "devpit-"), hs.Passphrase
				if hs.HotspotConfig != "temp" || hs.SSID != wantSSID || hs.Band != "5" || len(hs.Passphrase) != 8 {
					t.Errorf("temporary hotspot request = %v (passphrase length %d)", hs, len(hs.Passphrase))
				}
			}
			if card.Mode != ModeHotspot || card.IP != "192.168.137.1" || card.Adapter != hsAdapter.Name ||
				card.SSID != wantSSID || card.HotspotPassword != wantPass || card.OwnPassword != c.own ||
				card.Band != "5" || card.NormalIP != "192.168.1.20" || card.LinkBps != hsAdapter.LinkBps || !card.Wireless {
				t.Errorf("card = %+v", card)
			}
			if card.WifiString != WifiString(wantSSID, wantPass) {
				t.Errorf("WIFI: string = %q", card.WifiString)
			}
			if !strings.Contains(card.NetUse, `\\192.168.137.1\`+card.Share) || !strings.Contains(card.Robocopy, `\\192.168.137.1\`) {
				t.Errorf("the commands do not use the hotspot address: %q / %q", card.NetUse, card.Robocopy)
			}
			if card.HotspotFallback != "" || card.SwitchedNetwork {
				t.Errorf("card says fallback %q / switched %v", card.HotspotFallback, card.SwitchedNetwork)
			}
			if r.counted.Index != hsAdapter.Index {
				t.Errorf("the byte counter watches %+v, want the hotspot adapter", r.counted)
			}
			man, _, _ := LoadManifest(r.dir)
			if man.Mode != ModeHotspot || man.HotspotRule != testRule || !man.HotspotStarted ||
				!man.HotspotTimeoutWasOn || !man.HotspotTimeoutChanged || man.HotspotSSID != wantSSID ||
				man.HotspotConfig != hs.HotspotConfig {
				t.Errorf("manifest = %+v", man)
			}
			if err := man.request().Validate(); err != nil {
				t.Errorf("the worker would refuse this manifest's cleanup: %v", err)
			}
			if err := r.m.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(r.admin.opNames(), "share,stop") {
				t.Errorf("operations after stop = %s", r.admin.opNames())
			}
		})
	}
}

func TestHotspotIsOnlyStartedWhenChosen(t *testing.T) {
	r := newHSRig(t, CategoryPrivate, hsOK("My Hotspot", "5"))
	r.admin.onOp = func(req elevate.ShareRequest) {
		if req.Op == elevate.ShareOpHotspot {
			t.Errorf("a hotspot op was sent in normal mode: %v", req)
		}
	}
	// Asking for the suggestion launches nothing and sends nothing.
	if s := r.m.Suggest(context.Background()); !s.Suggested {
		t.Fatalf("suggestion = %+v", s)
	}
	if r.launches != 0 || len(r.admin.ops) != 0 {
		t.Fatal("Suggest started the worker")
	}
	// The suggestion holds, but the user picked the normal connection.
	if _, err := r.start(t, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := r.admin.opNames(); got != "firewall,account,grant,share" {
		t.Errorf("operations = %s", got)
	}
	if c, _ := r.m.Card(); c.Mode != ModeNormal || c.SSID != "" || c.HotspotPassword != "" {
		t.Errorf("normal card = %+v", c)
	}
}

func TestHotspotModeNeverAsksToSwitchTheNetwork(t *testing.T) {
	for _, cat := range []Category{CategoryPublic, CategoryPrivate, CategoryDomain} {
		t.Run(string(cat), func(t *testing.T) {
			r := newHSRig(t, cat, hsOK("My Hotspot", "5"))
			card, err := r.start(t, Options{Mode: ModeHotspot})
			if errors.Is(err, ErrNeedPrivate) {
				t.Fatal("hotspot mode asked to switch the network")
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(r.admin.opNames(), "profile") || card.SwitchedNetwork {
				t.Errorf("the network was switched: %s", r.admin.opNames())
			}
			if r.net.catsEarly != 0 {
				t.Errorf("the category was read %d times before the hotspot", r.net.catsEarly)
			}
		})
	}
}

func TestTheNormalPathIsPreparedOnlyOnAPrivateNetwork(t *testing.T) {
	for cat, prepared := range map[Category]bool{CategoryPrivate: true, CategoryPublic: false, CategoryDomain: false} {
		t.Run(string(cat), func(t *testing.T) {
			r := newHSRig(t, cat, hsOK("My Hotspot", "5"))
			card, err := r.start(t, Options{Mode: ModeHotspot})
			if err != nil {
				t.Fatal(err)
			}
			wantOps, wantIP := "hotspot,account,grant,share", ""
			if prepared {
				wantOps, wantIP = "hotspot,firewall,account,grant,share", "192.168.1.20"
			}
			if got := r.admin.opNames(); got != wantOps {
				t.Errorf("operations = %s, want %s", got, wantOps)
			}
			if card.NormalIP != wantIP {
				t.Errorf("NormalIP = %q, want %q", card.NormalIP, wantIP)
			}
			if r.launches != 1 {
				t.Errorf("%d prompts", r.launches)
			}
			man, _, _ := LoadManifest(r.dir)
			if prepared != (len(man.Rules) == 1) {
				t.Errorf("manifest rules = %v", man.Rules)
			}
		})
	}
}

func TestTheManifestNeverHoldsTheHotspotPassword(t *testing.T) {
	for _, choice := range []HotspotChoice{HotspotSaved, HotspotTemp} {
		r := newHSRig(t, CategoryPrivate, nil)
		r.m.Suggest(context.Background())
		var snapshots []string
		var hsPass string
		var atHotspot *Manifest
		r.admin.onOp = func(req elevate.ShareRequest) {
			if req.Op == elevate.ShareOpHotspot {
				hsPass = req.Passphrase
				if m, ok, _ := LoadManifest(r.dir); ok {
					atHotspot = &m
				}
				r.admin.mu.Lock()
				r.admin.hsLines = hsOK(map[bool]string{true: req.SSID, false: "My Hotspot"}[req.SSID != ""], "5")
				r.admin.mu.Unlock()
			}
		}
		r.admin.onLine = func(_ elevate.ShareOp, l string) {
			if strings.HasPrefix(l, "HSIP=") {
				r.net.mu.Lock()
				r.net.up = true
				r.net.mu.Unlock()
			}
			r.m.mu.Lock()
			user := r.m.man.User
			r.m.mu.Unlock()
			if raw, err := os.ReadFile(manifestPath(r.dir, user)); err == nil {
				snapshots = append(snapshots, string(raw))
			}
		}
		card, err := r.start(t, Options{Mode: ModeHotspot, Hotspot: choice})
		if err != nil {
			t.Fatal(err)
		}
		if atHotspot == nil || !atHotspot.HotspotStarted || atHotspot.HotspotConfig == "" {
			t.Errorf("choice %d: the manifest did not say a hotspot was about to start: %+v", choice, atHotspot)
		}
		raw, _ := os.ReadFile(manifestPath(r.dir, card.User))
		snapshots = append(snapshots, string(raw))
		secrets := []string{card.HotspotPassword, savedFacts.Password, card.Password}
		if choice == HotspotTemp {
			secrets = append(secrets, hsPass)
		}
		for _, s := range snapshots {
			for _, secret := range secrets {
				if secret != "" && (strings.Contains(s, secret) || strings.Contains(s, b64(secret))) {
					t.Errorf("choice %d: a manifest holds a password:\n%s", choice, s)
				}
			}
			if strings.Contains(strings.ToLower(s), "pass") {
				t.Errorf("choice %d: a manifest has a password field:\n%s", choice, s)
			}
		}
	}
}

func TestSavedModeSendsNoNameOrPassword(t *testing.T) {
	for _, choice := range []HotspotChoice{HotspotSaved, HotspotSavedSwitchTo5} {
		r := newHSRig(t, CategoryPrivate, hsOK("My Hotspot", "5"))
		r.m.Suggest(context.Background())
		if _, err := r.start(t, Options{Mode: ModeHotspot, Hotspot: choice}); err != nil {
			t.Fatal(err)
		}
		for _, op := range r.admin.ops {
			if op.Op == elevate.ShareOpHotspot && (op.HotspotConfig != "saved" || op.SSID != "" || op.Passphrase != "" || op.Band != "") {
				t.Errorf("saved-mode request carries a name, password or band: %v", op)
			}
			if op.Passphrase == savedFacts.Password || op.Password == savedFacts.Password ||
				op.SSID == savedFacts.SSID || op.HotspotSSID == savedFacts.SSID {
				t.Errorf("the saved hotspot name or password reached the worker in %s", op.Op)
			}
		}
	}
}

func TestAFailedHotspotStartFallsBackToNormalSharing(t *testing.T) {
	type tc struct {
		lines   []string
		err     error
		wantKey string
	}
	cases := map[string]tc{
		"no profile":          {lines: []string{"HSERR=noprofile"}, err: errors.New("no network connection to share"), wantKey: "unknown"},
		"capability disabled": {lines: []string{"HSCAP=1"}, err: errors.New("Windows does not allow it"), wantKey: "not-allowed"},
		"thrown":              {lines: []string{"HSCAP=0", "HSTIMEOUT=0"}, err: errors.New("powershell exited with 1"), wantKey: "unknown"},
		"no address": {
			lines: []string{"HSCAP=0", "HSTIMEOUT=0", "HSMODE=saved", "HSSSID=" + b64("My Hotspot"), "HSSTATUS=0", "HSBAND=5"},
			err:   errors.New("refused: the hotspot address \"\" is not a private IPv4 address"), wantKey: "unknown",
		},
		"rule failed": {
			lines: []string{"HSCAP=0", "HSTIMEOUT=1", "HSMODE=saved", "HSSTATUS=0", "HSIP=192.168.137.1", "HSRULE=" + testRule},
			err:   errors.New("making the hotspot firewall rule: access denied"), wantKey: "unknown",
		},
	}
	for _, s := range elevate.TetheringStatuses() {
		if s == elevate.TetheringSuccess {
			continue
		}
		cases["status "+s.Key()] = tc{
			lines:   []string{"HSCAP=0", "HSTIMEOUT=1", "HSMODE=saved", "HSSSID=" + b64("My Hotspot"), "HSBAND=5", "HSSTATUS=" + strconv.Itoa(int(s))},
			err:     errors.New("the hotspot did not start"),
			wantKey: s.Key(),
		}
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newHSRig(t, CategoryPrivate, c.lines)
			r.admin.failOp, r.admin.failErr = elevate.ShareOpHotspot, c.err
			card, err := r.start(t, Options{Mode: ModeHotspot})
			if err != nil {
				t.Fatalf("no fallback: %v", err)
			}
			if got := r.admin.opNames(); got != "hotspot,stop,firewall,account,grant,share" {
				t.Errorf("operations = %s, want the hotspot undone, then normal sharing", got)
			}
			if r.launches != 1 {
				t.Errorf("%d prompts, want one", r.launches)
			}
			if card.Mode != ModeNormal || card.IP != "192.168.1.20" || card.SSID != "" || card.HotspotFallbackKey != c.wantKey ||
				card.HotspotFallback == "" || !strings.Contains(card.HotspotFallback, "network") {
				t.Errorf("card = %+v, want normal sharing and key %s", card, c.wantKey)
			}
			if r.counted.Index != wlan.Index {
				t.Errorf("byte counter on %+v", r.counted)
			}
			man, _, _ := LoadManifest(r.dir)
			if man.Mode != ModeNormal || man.HotspotStarted || man.HotspotRule != "" || man.HotspotTimeoutChanged {
				t.Errorf("the manifest still holds the undone hotspot: %+v", man)
			}
		})
	}

	t.Run("public network", func(t *testing.T) {
		r := newHSRig(t, CategoryPublic, []string{"HSCAP=0", "HSTIMEOUT=0", "HSSTATUS=3"})
		r.admin.failOp, r.admin.failErr = elevate.ShareOpHotspot, errors.New("the hotspot did not start: wifi-off (3)")
		_, err := r.start(t, Options{Mode: ModeHotspot})
		var fb *HotspotFallbackError
		if !errors.As(err, &fb) || !errors.Is(err, ErrNeedPrivate) {
			t.Fatalf("err = %v, want a HotspotFallbackError wrapping ErrNeedPrivate", err)
		}
		if fb.Key != elevate.TetheringWiFiDeviceOff.Key() || fb.Message != elevate.TetheringWiFiDeviceOff.Message() {
			t.Errorf("fallback = %+v", fb)
		}
		if got := r.admin.opNames(); got != "hotspot,stop" {
			t.Errorf("operations = %s, want only the hotspot undone", got)
		}
		if _, ok, _ := LoadManifest(r.dir); ok {
			t.Error("a manifest is left while nothing exists")
		}
		if !r.m.Active() || r.admin.closed != 0 {
			t.Error("the worker was not kept for the user's answer")
		}
		// Yes: the same worker, no second prompt.
		card, err := r.start(t, Options{MakePrivate: true})
		if err != nil {
			t.Fatal(err)
		}
		if r.launches != 1 {
			t.Errorf("%d prompts after the yes, want one in all", r.launches)
		}
		if got := r.admin.opNames(); got != "hotspot,stop,profile,firewall,account,grant,share" {
			t.Errorf("operations = %s", got)
		}
		if !card.SwitchedNetwork || card.IP != "192.168.1.20" {
			t.Errorf("card = %+v", card)
		}
		if err := r.m.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("public network, the user says no", func(t *testing.T) {
		r := newHSRig(t, CategoryPublic, []string{"HSCAP=0", "HSTIMEOUT=0", "HSSTATUS=1"})
		r.admin.failOp, r.admin.failErr = elevate.ShareOpHotspot, errors.New("the hotspot did not start")
		if _, err := r.start(t, Options{Mode: ModeHotspot}); !errors.Is(err, ErrNeedPrivate) {
			t.Fatalf("err = %v", err)
		}
		if err := r.m.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if r.m.Active() || r.admin.closed != 1 {
			t.Error("the kept worker was not closed")
		}
	})

	t.Run("a cancelled start is not a fallback", func(t *testing.T) {
		r := newHSRig(t, CategoryPrivate, []string{"HSCAP=0"})
		ctx, cancel := context.WithCancel(context.Background())
		r.admin.failOp, r.admin.failErr = elevate.ShareOpHotspot, context.Canceled
		r.admin.onOp = func(req elevate.ShareRequest) {
			if req.Op == elevate.ShareOpHotspot {
				cancel()
			}
		}
		_, err := r.m.Start(ctx, Options{Path: `D:\Games`, Adapter: wlan, Mode: ModeHotspot}, nil)
		if err == nil {
			t.Fatal("a cancelled start went on")
		}
		if got := r.admin.opNames(); got != "hotspot,stop" || r.m.Active() {
			t.Errorf("operations = %s, active %v", got, r.m.Active())
		}
	})
}

func TestAHotspotAlreadyOnIsNotTakenOver(t *testing.T) {
	// The op succeeds having changed nothing: the user's own hotspot is on.
	r := newHSRig(t, CategoryPrivate, []string{"Checking the Wi-Fi hotspot", "HSCAP=0", "HSWASON=1"})
	var startedAtWasOn *bool
	r.admin.onLine = func(_ elevate.ShareOp, l string) {
		if l == "HSWASON=1" {
			m, _, _ := LoadManifest(r.dir)
			v := m.HotspotStarted
			startedAtWasOn = &v
		}
	}
	card, err := r.start(t, Options{Mode: ModeHotspot})
	if err != nil {
		t.Fatal(err)
	}
	if startedAtWasOn == nil || *startedAtWasOn {
		t.Error("the manifest still said Devpit started the hotspot after HSWASON")
	}
	hotspots := strings.Count(r.admin.opNames(), "hotspot")
	if hotspots != 1 || r.admin.opNames() != "hotspot,stop,firewall,account,grant,share" {
		t.Errorf("operations = %s", r.admin.opNames())
	}
	if card.Mode != ModeNormal || card.HotspotFallbackKey != "already-on" ||
		card.HotspotFallback != elevate.TetheringAlreadyOn.Message() {
		t.Errorf("card = %+v", card)
	}
	// The suggestion for a PC whose hotspot is on offers nothing.
	r2 := newHSRig(t, CategoryPrivate, nil)
	r2.m.deps.HotspotProbe = func(context.Context) (HotspotFacts, error) {
		f := savedFacts
		f.State = tetherStateOn
		return f, nil
	}
	if s := r2.m.Suggest(context.Background()); s.Offer || s.Reason != ReasonAlreadyOn {
		t.Errorf("suggestion with the hotspot on = %+v", s)
	}
}

func TestStartFindsTheHotspotAdapterAfterTheOp(t *testing.T) {
	r := newHSRig(t, CategoryPrivate, hsOK("My Hotspot", "5"))
	var before []Adapter
	r.admin.onOp = func(req elevate.ShareRequest) {
		if req.Op == elevate.ShareOpHotspot {
			before, _ = r.net.Adapters(context.Background())
			r.net.mu.Lock()
			r.net.hsRan = true
			r.net.mu.Unlock()
		}
	}
	card, err := r.start(t, Options{Mode: ModeHotspot})
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 {
		t.Fatalf("the hotspot adapter existed before the op: %v", before)
	}
	if card.Adapter != hsAdapter.Name || card.IP != "192.168.137.1" || r.counted.Index != hsAdapter.Index {
		t.Errorf("card %+v, counter on %+v", card, r.counted)
	}

	// Not found by its address: the address alone, and index 0 for the
	// counter, which then reports unknown.
	r = newHSRig(t, CategoryPrivate, hsOK("My Hotspot", "5"))
	r.net.after = []Adapter{wlan}
	card, err = r.start(t, Options{Mode: ModeHotspot})
	if err != nil {
		t.Fatal(err)
	}
	if card.Adapter != hotspotAdapterName || card.IP != "192.168.137.1" || r.counted.Index != 0 {
		t.Errorf("card %+v, counter on %+v", card, r.counted)
	}
}

func TestShutdownAllStopsAHotspotShareInTime(t *testing.T) {
	if shutdownTimeout > 30*time.Second {
		t.Errorf("shutdownTimeout = %v, want at most 30 s", shutdownTimeout)
	}
	r := newHSRig(t, CategoryPrivate, hsOK("My Hotspot", "5"))
	if _, err := r.start(t, Options{Mode: ModeHotspot}); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	if err := ShutdownAll(); err != nil {
		t.Fatal(err)
	}
	if time.Since(began) > 30*time.Second || r.m.Active() || !strings.HasSuffix(r.admin.opNames(), "share,stop") {
		t.Errorf("ShutdownAll took %v, ops %s", time.Since(began), r.admin.opNames())
	}
	if _, ok, _ := LoadManifest(r.dir); ok {
		t.Error("the manifest is left after the shutdown")
	}
}

// suggestRig builds a manager for one row of the suggestion table.
func suggestRig(adapters []Adapter, wifi []winapi.WifiInterface, wifiErr error, probe func(context.Context) (HotspotFacts, error)) (*Manager, *int) {
	calls := 0
	m := NewManager(Deps{
		Launch: func(context.Context) (Admin, error) { return nil, errors.New("Suggest must not start the worker") },
		Net:    fakeNet{adapters: adapters, cat: CategoryPrivate},
		Wifi:   func() ([]winapi.WifiInterface, error) { return wifi, wifiErr },
		HotspotProbe: func(ctx context.Context) (HotspotFacts, error) {
			calls++
			return probe(ctx)
		},
		probeLimit: 100 * time.Millisecond,
	})
	return m, &calls
}

func TestSuggestRule(t *testing.T) {
	facts := func(f func(*HotspotFacts)) func(context.Context) (HotspotFacts, error) {
		return func(context.Context) (HotspotFacts, error) {
			x := savedFacts
			if f != nil {
				f(&x)
			}
			return x, nil
		}
	}
	card := func(f func(*winapi.WifiInterface)) []winapi.WifiInterface {
		c := wlanCard
		c.PHYs = append([]uint32(nil), wlanCard.PHYs...)
		if f != nil {
			f(&c)
		}
		return []winapi.WifiInterface{c}
	}
	ethernet := Adapter{Name: "Ethernet", Index: 4, IP: net.ParseIP("192.168.1.5"), Best: true, LinkBps: 1e9}
	notBest := wlan
	notBest.Best = false
	noStandard := wlan
	noStandard.Standard = ""
	type row struct {
		adapters   []Adapter
		wifi       []winapi.WifiInterface
		wifiErr    error
		probe      func(context.Context) (HotspotFacts, error)
		offer, sug bool
		reason     SuggestReason
		noProbe    bool
	}
	rows := map[string]row{
		"rule holds":              {offer: true, sug: true, reason: ReasonSuggested},
		"Wi-Fi 7":                 {wifi: card(func(c *winapi.WifiInterface) { c.PHYs = []uint32{8, 10, 11} }), offer: true, sug: true, reason: ReasonSuggested},
		"Ethernet":                {adapters: []Adapter{ethernet, notBest}, offer: false, reason: ReasonWired, noProbe: true},
		"no internet connection":  {adapters: []Adapter{notBest}, reason: ReasonNoConnection, noProbe: true},
		"Wi-Fi card says offline": {wifi: card(func(c *winapi.WifiInterface) { c.Connected = false }), reason: ReasonNoConnection, noProbe: true},
		"old card (Wi-Fi 5)":      {wifi: card(func(c *winapi.WifiInterface) { c.PHYs = []uint32{7, 8} }), offer: true, reason: ReasonOldCard},
		"on 2.4 GHz":              {wifi: card(func(c *winapi.WifiInterface) { c.Channel = 6 }), offer: true, reason: ReasonOn24GHz},
		// 6 GHz channels also start at 1: on a card that can do 6 GHz a low
		// channel is an unknown band, never a demotion.
		"low channel, 6 GHz hotspot band": {wifi: card(func(c *winapi.WifiInterface) { c.Channel = 5 }), probe: facts(func(f *HotspotFacts) { f.Band6 = true }), offer: true, sug: true, reason: ReasonSuggested},
		"low channel, Wi-Fi 7 card":       {wifi: card(func(c *winapi.WifiInterface) { c.Channel = 5; c.PHYs = []uint32{8, 10, 11} }), offer: true, sug: true, reason: ReasonSuggested},
		"channel 14, no 6 GHz":            {wifi: card(func(c *winapi.WifiInterface) { c.Channel = 14 }), offer: true, reason: ReasonOn24GHz},
		"channel 36":                      {offer: true, sug: true, reason: ReasonSuggested},
		"no WLAN service, Wi-Fi 6":        {wifiErr: winapi.ErrNoWifi, offer: true, sug: true, reason: ReasonSuggested},
		"no WLAN service, unknown":        {adapters: []Adapter{noStandard}, wifiErr: winapi.ErrNoWifi, offer: true, reason: ReasonOldCard},
		"no connection profile":           {probe: facts(func(f *HotspotFacts) { *f = HotspotFacts{NoProfile: true} }), reason: ReasonNoConnection},
		"5 GHz unsupported":               {probe: facts(func(f *HotspotFacts) { f.Band5 = false }), reason: ReasonNo5GHz},
		"already on":                      {probe: facts(func(f *HotspotFacts) { f.State = tetherStateOn }), reason: ReasonAlreadyOn},
		"turning on":                      {probe: facts(func(f *HotspotFacts) { f.State = tetherStateInTransition }), reason: ReasonAlreadyOn},
		"state unknown":                   {probe: facts(func(f *HotspotFacts) { f.State = 0 }), reason: ReasonProbeFailed},
		"no band setting (old Win)":       {probe: facts(func(f *HotspotFacts) { f.Band = "" }), reason: ReasonOldWindows},
		"probe failed": {probe: func(context.Context) (HotspotFacts, error) {
			return HotspotFacts{}, errors.New("PowerShell did not run")
		}, reason: ReasonProbeFailed},
		"probe late": {probe: func(context.Context) (HotspotFacts, error) {
			time.Sleep(2 * time.Second) // ignores its context on purpose
			return savedFacts, nil
		}, reason: ReasonProbeFailed},
	}
	for c := elevate.TetheringDisabledByGroupPolicy; c <= elevate.TetheringDisabledBySystemCapability; c++ {
		rows["capability "+c.Key()] = row{probe: facts(func(f *HotspotFacts) { *f = HotspotFacts{Capability: c} }), reason: ReasonNotAllowed}
	}
	for name, r := range rows {
		t.Run(name, func(t *testing.T) {
			if r.adapters == nil {
				r.adapters = []Adapter{wlan}
			}
			if r.wifi == nil && r.wifiErr == nil {
				r.wifi = card(nil)
			}
			if r.probe == nil {
				r.probe = facts(nil)
			}
			m, calls := suggestRig(r.adapters, r.wifi, r.wifiErr, r.probe)
			began := time.Now()
			s := m.Suggest(context.Background())
			if took := time.Since(began); took > time.Second {
				t.Errorf("Suggest took %v; it must not wait past the probe limit", took)
			}
			if s.Offer != r.offer || s.Suggested != r.sug || s.Reason != r.reason {
				t.Errorf("Suggest = offer %v suggested %v reason %s; want %v %v %s", s.Offer, s.Suggested, s.Reason, r.offer, r.sug, r.reason)
			}
			if s.Suggested && !s.Offer {
				t.Error("suggested without being offered")
			}
			if r.noProbe && *calls != 0 {
				t.Error("the probe ran although the answer was already known")
			}
			if !s.Offer && (s.SavedPassword != "" || s.SavedSSID != "") {
				t.Errorf("a suggestion with no row holds the saved settings: %+v", s)
			}
			switch r.reason {
			case ReasonWired:
				if s.Hint == "" {
					t.Error("no hint that a cable is the fastest link")
				}
			case ReasonOldCard, ReasonOn24GHz:
				if s.Hint == "" {
					t.Error("an offered, not suggested row has no hint")
				}
			case ReasonSuggested:
				if s.Hint != "" || s.Adapter.Index != wlan.Index || s.SavedSSID != "My Hotspot" || s.SavedPassword != savedFacts.Password ||
					s.SavedBand != "5" || !s.Band5Supported || !s.TempSupported || s.NeedsBandChoice {
					t.Errorf("suggestion = %+v", s)
				}
			default:
				if s.Hint != "" {
					t.Errorf("a hidden row shows a hint: %q", s.Hint)
				}
			}
		})
	}
	t.Run("band not 5 GHz needs the band screen", func(t *testing.T) {
		for band, need := range map[string]bool{"auto": true, "2.4": true, "5": false, "6": false} {
			m, _ := suggestRig([]Adapter{wlan}, card(nil), nil, facts(func(f *HotspotFacts) { f.Band = band }))
			if s := m.Suggest(context.Background()); s.NeedsBandChoice != need || s.SavedBand != band {
				t.Errorf("band %s: %+v", band, s)
			}
		}
	})
	t.Run("temporary name needs the session type", func(t *testing.T) {
		m, _ := suggestRig([]Adapter{wlan}, card(nil), nil, facts(func(f *HotspotFacts) { f.Session = false }))
		if s := m.Suggest(context.Background()); !s.Suggested || s.TempSupported {
			t.Errorf("suggestion = %+v", s)
		}
	})
	for _, h := range []string{hintMaybeSlower, hintCable} {
		if regexp.MustCompile(`[0-9]|faster than|×`).MatchString(h) {
			t.Errorf("hint %q promises a speed", h)
		}
	}
}

func TestTheProbeTokensAreParsed(t *testing.T) {
	out := "noise\r\nHSCAP=0\r\nHSSTATE=2\r\nHSSSID=" + b64("Café; hotspot") + "\r\nHSPASS=" + b64("p@ss:word") +
		"\r\nHSBAND=2.4\r\nHSB5=1\r\nHSB6=0\r\nHSSESSION=1\r\n"
	f, err := parseHotspotProbe(out)
	if err != nil {
		t.Fatal(err)
	}
	if f.Capability != elevate.TetheringEnabled || f.State != 2 || f.SSID != "Café; hotspot" || f.Password != "p@ss:word" ||
		f.Band != "2.4" || !f.Band5 || f.Band6 || !f.Session {
		t.Errorf("facts = %+v", f)
	}
	if np, nerr := parseHotspotProbe("HSERR=noprofile\n"); nerr != nil || !np.NoProfile {
		t.Errorf("no profile = %+v %v", np, nerr)
	}
	if off, oerr := parseHotspotProbe("HSCAP=4\n"); oerr != nil || off.Capability != elevate.TetheringDisabledBySku {
		t.Errorf("disabled = %+v %v", off, oerr)
	}
	for _, bad := range []string{"", "HSERR=error", "HSCAP=x", "HSCAP=0\nHSSTATE=2\nHSSSID=!!", "HSCAP=0\nHSSSID=" + b64("a")} {
		if _, berr := parseHotspotProbe(bad); berr == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	// No band setting: Band stays empty, which offers nothing.
	f, err = parseHotspotProbe("HSCAP=0\nHSSTATE=2\nHSSSID=" + b64("a") + "\nHSPASS=" + b64("b") + "\nHSSESSION=0")
	if err != nil || f.Band != "" || f.Band5 {
		t.Errorf("old Windows = %+v %v", f, err)
	}
}

func TestTheHotspotProbeOnlyReads(t *testing.T) {
	for _, call := range []string{
		"StartTethering", "StopTethering", "ConfigureAccessPoint", "DisableNoConnectionsTimeout",
		"EnableNoConnectionsTimeout", "New-NetFirewallRule", "Set-", "Remove-", "netsh", "-EncodedCommand",
	} {
		if strings.Contains(hotspotProbeScript, call) {
			t.Errorf("the read-only probe calls %s", call)
		}
	}
	if strings.Contains(hotspotProbeScript, "%") {
		t.Error("the probe script has a placeholder; it must be a constant")
	}
	// The setters it must not use: assigning to the saved configuration.
	if regexp.MustCompile(`\$ap\.\w+\s*=`).MatchString(hotspotProbeScript) {
		t.Error("the probe assigns to the saved hotspot configuration")
	}
}

func TestWifiStringEscapes(t *testing.T) {
	cases := map[[2]string]string{
		{"Devpit-x7k2", "12345678"}:      `WIFI:T:WPA;S:Devpit-x7k2;P:12345678;;`,
		{`My;Net,"Home":\`, `p;a,s"s:\`}: `WIFI:T:WPA;S:My\;Net\,\"Home\"\:\\;P:p\;a\,s\"s\:\\;;`,
		{"Café", "pass word"}:            `WIFI:T:WPA;S:Café;P:pass word;;`,
	}
	for in, want := range cases {
		if got := WifiString(in[0], in[1]); got != want {
			t.Errorf("WifiString(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestHotspotPasswordIsEightDigits(t *testing.T) {
	eight := regexp.MustCompile(`^[0-9]{8}$`)
	r := seeded(3)
	seen := map[string]bool{}
	for range 1000 {
		p, err := HotspotPassword(r)
		if err != nil || !eight.MatchString(p) {
			t.Fatalf("%q, %v", p, err)
		}
		seen[p] = true
	}
	if len(seen) < 990 {
		t.Errorf("only %d different passwords in 1000", len(seen))
	}
	if p, _ := HotspotPassword(zeroReader{}); p != "00000000" {
		t.Errorf("leading zeros were not kept: %q", p)
	}
	if _, err := HotspotPassword(failingReader{}); err == nil {
		t.Error("no error from a failing random source")
	}
	if HotspotSSID("devpit-x7k2") != "Devpit-x7k2" {
		t.Error(HotspotSSID("devpit-x7k2"))
	}
	// The worker's own patterns accept what the host makes.
	req := hotspotRequest(HotspotTemp, "devpit-x7k2", "01234567")
	if err := req.Validate(); err != nil {
		t.Errorf("the worker refuses the temporary hotspot: %v", err)
	}
}

func TestStepsMatchWhatStartReports(t *testing.T) {
	keys := func(s []Step) string {
		var k []string
		for _, x := range s {
			k = append(k, x.Key)
		}
		return strings.Join(k, ",")
	}
	if got := keys(Steps(Options{}, true)); got != "firewall,login,folder,share" {
		t.Error(got)
	}
	if got := keys(Steps(Options{}, false)); got != "network,firewall,login,folder,share" {
		t.Error(got)
	}
	if got := keys(Steps(Options{Mode: ModeHotspot}, false)); got != "hotspot,hsfirewall,login,folder,share" {
		t.Error(got)
	}
	if got := keys(Steps(Options{Mode: ModeHotspot, Hotspot: HotspotSavedSwitchTo5}, true)); got != "band5,hotspot,hsfirewall,firewall,login,folder,share" {
		t.Error(got)
	}
	r := newRig(t, CategoryPublic)
	var seen []Step
	if _, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan, MakePrivate: true}, func(s Step) { seen = append(seen, s) }); err != nil {
		t.Fatal(err)
	}
	if keys(seen) != keys(Steps(Options{}, false)) {
		t.Errorf("Start reported %s", keys(seen))
	}
	for _, s := range seen {
		if s.Label == "" {
			t.Errorf("step %s has no label", s.Key)
		}
	}
}

func TestAdaptersCarryLinkSpeedAndStandard(t *testing.T) {
	list := []Adapter{
		{Name: "Wi-Fi", Index: 12, IP: net.ParseIP("192.168.1.20"), Best: true},
		{Name: "Ethernet", Index: 4, IP: net.ParseIP("10.0.0.5")},
	}
	rows := map[uint32]linkRow{
		12: {GUID: wlan.GUID, Bps: 1_201_000_000, Wireless: true},
		4:  {GUID: "{AAAAAAAA-2222-3333-4444-555555555555}", Bps: 1_000_000_000},
	}
	// The card's GUID may come in another case; it still matches.
	c := wlanCard
	c.GUID = strings.ToLower(c.GUID)
	got := annotate(list, rows, []winapi.WifiInterface{c})
	if got[0].LinkBps != 1_201_000_000 || !got[0].Wireless || got[0].Standard != "Wi-Fi 6" || got[0].GUID != wlan.GUID {
		t.Errorf("Wi-Fi adapter = %+v", got[0])
	}
	if got[1].LinkBps != 1_000_000_000 || got[1].Wireless || got[1].Standard != "" {
		t.Errorf("Ethernet adapter = %+v", got[1])
	}
	if linkSpeed(true, 300_000_000, 866_700_000) != 866_700_000 {
		t.Error("the faster of the two link speeds is not used")
	}
}

func TestUnknownLinkSpeedIsZeroNotAGuess(t *testing.T) {
	if linkSpeed(true, ^uint64(0), ^uint64(0)) != 0 {
		t.Error("Windows' unknown value became a speed")
	}
	if linkSpeed(false, 1e9, 1e9) != 0 {
		t.Error("a disconnected link has a speed")
	}
	list := []Adapter{{Name: "Wi-Fi", Index: 12}, {Name: "Mystery", Index: 30}}
	// No row for the second adapter, and a card with no PHY list.
	c := wlanCard
	c.PHYs = nil
	got := annotate(list, map[uint32]linkRow{12: {GUID: wlan.GUID, Wireless: true}}, []winapi.WifiInterface{c})
	if got[0].LinkBps != 0 || got[0].Standard != "" || !got[0].Wireless {
		t.Errorf("unknown Wi-Fi = %+v", got[0])
	}
	if g := got[1]; g.Name != "Mystery" || g.Index != 30 || g.GUID != "" || g.LinkBps != 0 || g.Wireless || g.Standard != "" {
		t.Errorf("an adapter Windows did not describe got values: %+v", got[1])
	}
	if got := annotate([]Adapter{{Index: 1}}, nil, nil); got[0].LinkBps != 0 {
		t.Error("no data gave a speed")
	}
}

// A leftover from before Windows last started never asks for a stop: the
// restart turned Devpit's hotspot off, so one that is on now is the
// person's. The rule and the timeout are still put back.
func TestACleanupFromBeforeTheRestartStopsNoHotspot(t *testing.T) {
	boot := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	m := Manifest{
		User: "devpit-abcd", Mode: ModeHotspot, HotspotConfig: "saved", HotspotSSID: "My Hotspot",
		HotspotRule: testRule, HotspotStarted: true, HotspotTimeoutWasOn: true, HotspotTimeoutChanged: true,
	}
	for name, tc := range map[string]struct {
		started, boot time.Time
		stop          bool
	}{
		"before the restart":  {boot.Add(-time.Hour), boot, false},
		"after the restart":   {boot.Add(time.Hour), boot, true},
		"boot time unknown":   {boot.Add(-time.Hour), time.Time{}, true},
		"start time unknown":  {time.Time{}, boot, true},
		"exactly at the boot": {boot, boot, true},
	} {
		m.StartedAt = tc.started
		r := m.cleanupRequest(tc.boot)
		if r.HotspotStarted != tc.stop {
			t.Errorf("%s: HotspotStarted = %v, want %v", name, r.HotspotStarted, tc.stop)
		}
		if r.HotspotRule != testRule || !r.HotspotTimeoutChanged || !r.HotspotTimeoutWasOn {
			t.Errorf("%s: the rule or the timeout is no longer put back: %+v", name, r)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("%s: the worker would refuse it: %v", name, err)
		}
	}
	if b := bootTime(); !b.IsZero() && b.After(time.Now()) {
		t.Errorf("the boot time %v is in the future", b)
	}
}

// After a crash on the legacy path the person is told their saved hotspot
// settings were overwritten (decision 14); no other leftover has a note.
func TestALegacyLeftoverCarriesTheSettingsNote(t *testing.T) {
	m := Manifest{User: "devpit-abcd", Mode: ModeHotspot, HotspotConfig: "temp", HotspotSSID: "Devpit-abcd", HotspotLegacy: true}
	if m.HotspotNote() != MsgHotspotSettingsLost || !strings.Contains(MsgHotspotSettingsLost, "Mobile hotspot") {
		t.Errorf("note = %q", m.HotspotNote())
	}
	m.HotspotLegacy = false
	if m.HotspotNote() != "" {
		t.Errorf("a leftover that changed no settings has the note %q", m.HotspotNote())
	}
	dir := t.TempDir()
	m.HotspotLegacy = true
	if err := saveManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Leftover(dir, nil, 1)
	if err != nil || !ok || got.HotspotNote() != MsgHotspotSettingsLost {
		t.Errorf("the note does not survive the manifest on disk: %v %v %q", ok, err, got.HotspotNote())
	}
}
