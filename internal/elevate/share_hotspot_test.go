package elevate

import (
	"context"
	"encoding/base64"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"
)

// marked returns the commands that are the hotspot script with marker m.
func marked(cmds []string, m string) []string {
	var out []string
	for _, c := range cmds {
		if strings.Contains(c, "# devpit "+m+"\n") {
			out = append(out, c)
		}
	}
	return out
}

// lineWith reports whether lines hold one that starts with prefix.
func lineWith(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func TestHotspotLifecycleAndTheOrderOfUndoing(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	s := newSession(f)
	var lines []string
	for _, r := range []ShareRequest{
		savedHotspot,
		{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword},
		{Op: ShareOpGrant, User: "devpit-abcd", Path: `D:\Games`},
		{Op: ShareOpShare, User: "devpit-abcd", Path: `D:\Games`, Name: "Games-x7k2"},
	} {
		got, err := run(t, s, r)
		if err != nil {
			t.Fatalf("%s: %v", r.Op, err)
		}
		lines = append(lines, got...)
	}
	for _, want := range []string{
		"HSCAP=0", "HSTIMEOUT=1", "HSMODE=saved",
		"HSSSID=" + base64.StdEncoding.EncodeToString([]byte("MyHotspot")),
		"HSSTATUS=0", "HSBAND=5", "HSIP=192.168.137.1", "HS=on",
	} {
		if !contains(lines, want) {
			t.Errorf("no line %q in %v", want, lines)
		}
	}
	if !lineWith(lines, "HSRULE=devpit-hs-") || !lineWith(lines, "HSFW=devpit-hs-") {
		t.Errorf("the rule was not reported: %v", lines)
	}
	cmds := f.commands()
	order := []string{"hs-probe", "hs-timeout-off", "hs-start", "hs-rule-add"}
	for i, m := range order {
		if !strings.Contains(cmds[i], "# devpit "+m+"\n") {
			t.Errorf("command %d is not %s:\n%s", i, m, cmds[i])
		}
	}

	before := len(f.commands())
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	undo := f.commands()[before:]
	wantOrder := []string{
		"Remove-SmbShare", "/remove:g *S-1-5-21-1-2-3-1004",
		"# devpit hs-rule-remove", "# devpit hs-stop", "# devpit hs-timeout-on",
	}
	if len(undo) != len(wantOrder) {
		t.Fatalf("stop ran %d commands, want %d:\n%s", len(undo), len(wantOrder), strings.Join(undo, "\n"))
	}
	for i, w := range wantOrder {
		if !strings.Contains(undo[i], w) {
			t.Errorf("undo step %d = %q, want it to contain %q", i, undo[i], w)
		}
	}
	if got := psValue(t, undo[3], "exp"); got != "MyHotspot" {
		t.Errorf("the stop checks for %q, want the recorded name", got)
	}
	if got := psValue(t, undo[3], "check"); got != "name" {
		t.Errorf("a saved-mode stop checks %q, want name", got)
	}
	if f.hasUser("devpit-abcd") || !s.state.empty() {
		t.Errorf("state after stop = %+v", s.state)
	}
	n := len(f.commands())
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil || len(f.commands()) != n {
		t.Errorf("a second stop ran commands or failed: %v", err)
	}
}

func TestAHotspotThatWasAlreadyOnIsLeftOn(t *testing.T) {
	shareEnv(t)
	// Seen by the probe: nothing at all is changed.
	f := newShareFake()
	f.hsProbeOut = []string{"HSCAP=0", "HSWASON=1"}
	s := newSession(f)
	lines, err := run(t, s, savedHotspot)
	if err != nil || !contains(lines, "HSWASON=1") {
		t.Fatalf("err = %v, lines = %v", err, lines)
	}
	if cmds := f.commands(); len(cmds) != 1 {
		t.Errorf("an already-on hotspot ran %d commands, want the probe only:\n%s", len(cmds), strings.Join(cmds, "\n"))
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil || len(f.commands()) != 1 {
		t.Errorf("stop touched a hotspot that was already on: %v\n%s", err, strings.Join(f.commands(), "\n"))
	}

	// Turned on between the probe and the start, or AlreadyOn from Windows:
	// never stopped, and the timeout Devpit turned off is still put back.
	for _, out := range [][]string{{"HSWASON=1"}, {"HSMODE=session", "HSSTATUS=9"}} {
		f = newShareFake()
		f.hsStartOut = out
		s = newSession(f)
		_, err := run(t, s, tempHotspot)
		if out[0] == "HSWASON=1" && err != nil {
			t.Fatalf("%v: %v", out, err)
		}
		if s.state.hotspot.started {
			t.Errorf("%v: recorded as started", out)
		}
		if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
			t.Fatal(err)
		}
		cmds := f.commands()
		if len(marked(cmds, "hs-stop")) != 0 || len(marked(cmds, "hs-rule-add")) != 0 {
			t.Errorf("%v: the hotspot was stopped or opened:\n%s", out, strings.Join(cmds, "\n"))
		}
		if len(marked(cmds, "hs-timeout-on")) != 1 {
			t.Errorf("%v: the timeout was not put back", out)
		}
	}
}

func TestTheNoConnectionsTimeoutIsPutBack(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	s := newSession(f)
	if _, err := run(t, s, savedHotspot); err != nil {
		t.Fatal(err)
	}
	if len(marked(f.commands(), "hs-timeout-off")) != 1 || !s.state.hotspot.timeoutChanged {
		t.Fatal("the timeout was not turned off and recorded")
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
		t.Fatal(err)
	}
	if len(marked(f.commands(), "hs-timeout-on")) != 1 {
		t.Error("stop did not turn the timeout back on")
	}

	// Off already: left alone both ways.
	f = newShareFake()
	f.hsProbeOut = []string{"HSCAP=0", "HSTIMEOUT=0"}
	s = newSession(f)
	for _, r := range []ShareRequest{savedHotspot, {Op: ShareOpStop}} {
		if _, err := run(t, s, r); err != nil {
			t.Fatal(err)
		}
	}
	if len(marked(f.commands(), "hs-timeout-off"))+len(marked(f.commands(), "hs-timeout-on")) != 0 {
		t.Error("a timeout that was off was changed")
	}

	// A start Windows refused still puts the timeout back, and stops nothing.
	f = newShareFake()
	f.hsStartOut = []string{"HSMODE=saved", "HSSTATUS=3"}
	s = newSession(f)
	if _, err := run(t, s, savedHotspot); err == nil || !strings.Contains(err.Error(), "wifi-off") {
		t.Fatalf("err = %v, want the Wi-Fi-off status", err)
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
		t.Fatal(err)
	}
	if len(marked(f.commands(), "hs-timeout-on")) != 1 || len(marked(f.commands(), "hs-stop")) != 0 {
		t.Errorf("after a refused start:\n%s", strings.Join(f.commands(), "\n"))
	}
}

func TestTheBandSwitchIsNotReverted(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.hsStartOut = append([]string{"HSBANDSET=1"}, savedStart("MyHotspot")...)
	s := newSession(f)
	r := savedHotspot
	r.SetBand5 = true
	lines, err := run(t, s, r)
	if err != nil || !contains(lines, "HSBANDSET=1") {
		t.Fatalf("err = %v, lines = %v", err, lines)
	}
	if stdinOf(t, f, "hs-start", 0)[4] != "1" {
		t.Error("the start was not asked to switch the band")
	}
	before := len(f.commands())
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.commands()[before:] {
		if strings.Contains(c, "ConfigureAccessPointAsync") {
			t.Errorf("stop changed the saved hotspot settings:\n%s", c)
		}
	}
}

func TestSavedModeSendsNoNameOrPassword(t *testing.T) {
	shareEnv(t)
	for _, r := range []ShareRequest{
		{Op: ShareOpHotspot, HotspotConfig: "saved", Passphrase: goodPassphrase},
		{Op: ShareOpHotspot, HotspotConfig: "saved", SSID: "MyHotspot"},
		{Op: ShareOpHotspot, HotspotConfig: "saved", Band: "5"},
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%+v was accepted", r)
		}
	}
	f := newShareFake()
	if _, err := run(t, newSession(f), savedHotspot); err != nil {
		t.Fatal(err)
	}
	in := stdinOf(t, f, "hs-start", 0)
	if in[1] != "" || in[2] != "" || in[0] != "saved" {
		t.Error("a saved-mode start carried a name or a password")
	}
}

func TestTheHotspotPassphraseIsNeverOnACommandLineOrInALine(t *testing.T) {
	shareEnv(t)
	const own = "MyOwnSecret1" // the user's saved password, legacy path
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	f := newShareFake()
	f.hsStartOut = []string{
		"HSMODE=legacy", "HSOLDSSID=" + b64("Home"), "HSOLDPASS=" + b64(own), "HSOLDBAND=1",
		"HSSSID=" + b64("Devpit-abcd"), "HSBAND=5", "HSSTATUS=0", "HSIP=192.168.137.1",
	}
	s := newSession(f)
	var lines []string
	for _, r := range []ShareRequest{tempHotspot, {Op: ShareOpStop}} {
		got, err := run(t, s, r)
		if err != nil {
			t.Fatalf("%s: %v", r.Op, err)
		}
		lines = append(lines, got...)
	}
	restore := marked(f.commands(), "hs-restore")
	if in := stdinOf(t, f, "hs-restore", 0); len(restore) != 1 || in[1] != own || in[0] != "Home" || in[2] != "1" {
		t.Fatalf("the saved settings were not put back: %v", restore)
	}
	if stdinOf(t, f, "hs-start", 0)[2] != goodPassphrase {
		t.Fatal("the start did not get the passphrase on its standard input")
	}
	secrets := []string{goodPassphrase, b64(goodPassphrase), own, b64(own)}
	for _, argv := range f.argvs {
		for _, a := range argv {
			for _, sec := range secrets {
				if strings.Contains(a, sec) {
					t.Errorf("a passphrase is on a command line: %q", a)
				}
			}
		}
	}
	// No script text holds a secret in any form: the text is what Windows
	// may write to the PowerShell event log (4104).
	for _, c := range f.commands() {
		for _, sec := range secrets {
			if strings.Contains(c, sec) {
				t.Errorf("a passphrase is inside a script's text:\n%s", c)
			}
		}
	}
	// The two scripts that carry one are constants, sent exactly as written.
	for _, m := range []struct{ marker, script string }{{"hs-start", scriptHotspotStart}, {"hs-restore", scriptHotspotRestore}} {
		for _, c := range marked(f.commands(), m.marker) {
			if c != "powershell: "+m.script {
				t.Errorf("%s is not the constant script", m.marker)
			}
		}
	}
	// Only standard input carries them, and only to the script that needs it.
	f.mu.Lock()
	for i, in := range f.stdins {
		holds := strings.Contains(in, b64(goodPassphrase)) || strings.Contains(in, b64(own))
		isStart, isRestore := strings.Contains(f.calls[i], "# devpit hs-start\n"), strings.Contains(f.calls[i], "# devpit hs-restore\n")
		if holds && !isStart && !isRestore {
			t.Errorf("a passphrase went to another command's input: %s", f.calls[i][:40])
		}
		if isStart && strings.Contains(in, b64(own)) {
			t.Error("the start was given the user's own password")
		}
	}
	f.mu.Unlock()
	for _, l := range lines {
		for _, sec := range secrets {
			if strings.Contains(l, sec) {
				t.Errorf("a passphrase is in a line: %q", l)
			}
		}
	}

	// A failing start whose error quotes the script line keeps both hidden.
	f = newShareFake()
	f.hsStartOut = []string{"HSMODE=legacy", "HSOLDPASS=" + b64(own)}
	f.hsStartStderr = []string{"At line:9 char:1 + $pass=([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" +
		b64(goodPassphrase) + "'))) " + own + " " + goodPassphrase}
	f.hsStartCode = 1
	_, err := run(t, newSession(f), tempHotspot)
	if err == nil {
		t.Fatal("the failing start succeeded")
	}
	for _, sec := range secrets {
		if strings.Contains(err.Error(), sec) {
			t.Errorf("the error holds a passphrase: %v", err)
		}
	}
}

func TestHotspotIsUndoneBeforeTheOtherSharesGate(t *testing.T) {
	shareEnv(t)
	for _, others := range []int{1, -1} {
		f := newShareFake()
		if others > 0 {
			f.others = others
		} else {
			f.failMatch = "OTHERS=" // the gate cannot tell
		}
		s := newSession(f)
		for _, r := range []ShareRequest{profileReq, {Op: ShareOpFirewall}, savedHotspot} {
			if _, err := run(t, s, r); err != nil {
				t.Fatalf("%s: %v", r.Op, err)
			}
		}
		before := len(f.commands())
		_, _ = run(t, s, ShareRequest{Op: ShareOpStop})
		undo := f.commands()[before:]
		gate := -1
		for i, c := range undo {
			if strings.Contains(c, "OTHERS=") {
				gate = i
			}
		}
		for _, m := range []string{"hs-rule-remove", "hs-stop", "hs-timeout-on"} {
			found := -1
			for i, c := range undo {
				if strings.Contains(c, "# devpit "+m+"\n") {
					found = i
				}
			}
			if found < 0 || found > gate {
				t.Errorf("others=%d: %s ran at %d, gate at %d:\n%s", others, m, found, gate, strings.Join(undo, "\n"))
			}
		}
		if !s.state.hotspot.empty() {
			t.Errorf("others=%d: the hotspot record stayed: %+v", others, s.state.hotspot)
		}
	}
}

// TestTheWorkerStopsTheHotspotWhenTheTUIDies is the crash test for the
// hotspot: the pipe is cut with no stop, and the worker undoes it by itself.
func TestTheWorkerStopsTheHotspotWhenTheTUIDies(t *testing.T) {
	shareEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := newShareFake()
	serverConn, clientConn := net.Pipe()
	served := make(chan error, 1)
	go func() { served <- serveConn(ctx, serverConn, f, true, 1) }()
	c, err := newClient(ctx, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Share(ctx, savedHotspot, nil); err != nil {
		t.Fatal(err)
	}
	_ = clientConn.Close()
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not notice the pipe was gone")
	}
	cmds := f.commands()
	for _, m := range []string{"hs-rule-remove", "hs-stop", "hs-timeout-on"} {
		if len(marked(cmds, m)) != 1 {
			t.Errorf("the worker did not run %s:\n%s", m, strings.Join(cmds, "\n"))
		}
	}
}

func TestCleanupStopsOnlyADevpitHotspot(t *testing.T) {
	shareEnv(t)
	saved := ShareRequest{
		Op: ShareOpCleanup, HotspotConfig: "saved", HotspotRule: "devpit-hs-abcd1234", HotspotStarted: true,
		HotspotTimeoutWasOn: true, HotspotTimeoutChanged: true, HotspotSSID: "MyHotspot",
	}
	f := newShareFake()
	if _, err := run(t, newSession(f), saved); err != nil {
		t.Fatal(err)
	}
	stop := marked(f.commands(), "hs-stop")
	if len(stop) != 1 || psValue(t, stop[0], "exp") != "MyHotspot" || psValue(t, stop[0], "check") != "name" {
		t.Fatalf("the cleanup stop is not checked against the recorded name: %v", stop)
	}
	if rm := marked(f.commands(), "hs-rule-remove"); len(rm) != 1 || psValue(t, rm[0], "n") != "devpit-hs-abcd1234" {
		t.Errorf("the rule was not removed by its name: %v", rm)
	}
	if len(marked(f.commands(), "hs-timeout-on")) != 1 {
		t.Error("the cleanup did not turn the timeout back on")
	}

	// The running hotspot carries another name: left on, and that is done.
	f = newShareFake()
	f.hsStopOut = []string{"HSSTOP=notours"}
	s := newSession(f)
	lines, err := run(t, s, saved)
	if err != nil || !s.state.empty() || !lineWith(lines, "Left the hotspot on") {
		t.Errorf("err = %v, state = %+v, lines = %v", err, s.state, lines)
	}

	// Not started by Devpit, or a per-session start whose name Windows does
	// not report: never stopped. A legacy start is checked by name.
	for _, tc := range []struct {
		r     ShareRequest
		stops bool
	}{
		{ShareRequest{Op: ShareOpCleanup, HotspotConfig: "saved", HotspotSSID: "MyHotspot"}, false},
		{ShareRequest{Op: ShareOpCleanup, HotspotConfig: "temp", HotspotStarted: true, HotspotSSID: "Devpit-abcd"}, false},
		{ShareRequest{Op: ShareOpCleanup, HotspotConfig: "temp", HotspotStarted: true, HotspotSSID: "Devpit-abcd", HotspotLegacy: true}, true},
	} {
		f := newShareFake()
		if _, err := run(t, newSession(f), tc.r); err != nil {
			t.Fatalf("%+v: %v", tc.r, err)
		}
		if got := len(marked(f.commands(), "hs-stop")) == 1; got != tc.stops {
			t.Errorf("%+v: stopped = %v, want %v", tc.r, got, tc.stops)
		}
	}
}

func TestEachTetheringStatusHasAMessage(t *testing.T) {
	keys := map[string]bool{}
	for _, s := range TetheringStatuses() {
		k := s.Key()
		if k == "" || keys[k] {
			t.Errorf("status %d has the key %q, empty or taken", s, k)
		}
		keys[k] = true
		if (s.Message() == "") != (s == TetheringSuccess) {
			t.Errorf("status %d (%s) has the message %q", s, k, s.Message())
		}
	}
	if len(keys) != 12 || TetheringAlreadyOn != 9 || TetheringRadioRestriction != 10 || TetheringBandInterference != 11 {
		t.Error("the table does not follow TetheringOperationStatus")
	}
	if TetheringStatus(42).Key() != "unknown" || TetheringStatus(42).Message() != TetheringUnknown.Message() {
		t.Error("a status Windows adds later is not read as unknown")
	}
	caps := map[string]bool{}
	for c := TetheringEnabled; c <= TetheringDisabledBySystemCapability; c++ {
		if caps[c.Key()] {
			t.Errorf("capability %d repeats the key %q", c, c.Key())
		}
		caps[c.Key()] = true
	}
	if len(caps) != 8 || TetheringDisabledBySystemCapability != 7 || TetheringCapability(9).Key() != "disabled-unknown" {
		t.Error("the capability table does not follow TetheringCapability")
	}
}

func TestADisabledCapabilityOrNoProfileChangesNothing(t *testing.T) {
	shareEnv(t)
	for _, out := range [][]string{{"HSCAP=1"}, {"HSERR=noprofile"}} {
		f := newShareFake()
		f.hsProbeOut = out
		s := newSession(f)
		lines, err := run(t, s, savedHotspot)
		if err == nil || !contains(lines, out[0]) {
			t.Errorf("%v: err = %v, lines = %v", out, err, lines)
		}
		if len(f.commands()) != 1 || !s.state.empty() {
			t.Errorf("%v: something was changed: %v", out, f.commands())
		}
	}
}

// The "turn off when nothing is connected" members are static members of
// NetworkOperatorTetheringManager, and PowerShell does not reach a static
// method through an instance: called on $m, the probe always read "na" and
// the hotspot ended five minutes after the last device left.
func TestTheTimeoutCallsAreMadeOnTheType(t *testing.T) {
	onInstance := regexp.MustCompile(`\$\w+\.(Is|Enable|Disable)NoConnectionsTimeout`)
	for name, tc := range map[string]struct{ script, want string }{
		"probe":       {scriptHotspotProbe, "$TM::IsNoConnectionsTimeoutEnabled()"},
		"timeout-off": {scriptHotspotTimeoutOff, "AwaitAction ($TM::DisableNoConnectionsTimeoutAsync())"},
		"timeout-on":  {scriptHotspotTimeoutOn, "AwaitAction ($TM::EnableNoConnectionsTimeoutAsync())"},
	} {
		if !strings.Contains(tc.script, tc.want) {
			t.Errorf("%s does not call %s", name, tc.want)
		}
	}
	for name, script := range map[string]string{
		"probe": scriptHotspotProbe, "timeout-off": scriptHotspotTimeoutOff, "timeout-on": scriptHotspotTimeoutOn,
		"start": scriptHotspotStart, "stop": scriptHotspotStop, "restore": scriptHotspotRestore,
	} {
		if m := onInstance.FindString(script); m != "" {
			t.Errorf("%s calls a static timeout member on an instance: %s", name, m)
		}
	}
}

// The hotspot's address is the ICS scope address on an adapter that is up,
// or the one address of an up, virtual 802.11 adapter; an address that is
// merely new (a VPN coming up) is never taken.
func TestTheHotspotAddressIsNeverJustANewAddress(t *testing.T) {
	for _, want := range []string{
		`$scope='192.168.137.1'`, `-Name ScopeAddress`,
		`-IPAddress $scope`, `[int]$_.InterfaceOperationalStatus -eq 1`,
		`[int]$_.InterfaceType -eq 71 -and $_.Virtual`,
	} {
		if !strings.Contains(scriptHotspotStart, want) {
			t.Errorf("the start script has no %q", want)
		}
	}
	for _, bad := range []string{"$before", "$new", "-notcontains $_.IPAddress", "Wi-Fi Direct", "InterfaceAlias"} {
		if strings.Contains(scriptHotspotStart, bad) {
			t.Errorf("the start script still picks an address by %q", bad)
		}
	}
}

// A per-session hotspot has no name to check, so the stop is narrow: only
// this worker's own start, only when Windows says plainly that it is on.
func TestASessionHotspotIsStoppedOnlyWhenPlainlyOn(t *testing.T) {
	shareEnv(t)
	if !strings.Contains(scriptHotspotStop, "if($st -ne 1){ Write-Output 'HSSTOP=unsure'; exit 0 }") {
		t.Error("the session stop does not require the hotspot to be on")
	}
	if i, j := strings.Index(scriptHotspotStop, "$st -eq 3"), strings.Index(scriptHotspotStop, "$st -eq 2"); i < 0 || i > j {
		t.Error("a state in transition is not waited out before the stop decides")
	}

	// Windows does not say it is on: left alone, and that is done.
	f := newShareFake()
	f.hsStartOut = sessionStart("Devpit-abcd")
	f.hsStopOut = []string{"HSSTOP=unsure"}
	s := newSession(f)
	if _, err := run(t, s, tempHotspot); err != nil {
		t.Fatal(err)
	}
	lines, err := run(t, s, ShareRequest{Op: ShareOpStop})
	if err != nil || !s.state.empty() || !lineWith(lines, "Left the hotspot as it is") {
		t.Errorf("err = %v, state = %+v, lines = %v", err, s.state, lines)
	}
	if stop := marked(f.commands(), "hs-stop"); len(stop) != 1 || psValue(t, stop[0], "check") != "session" {
		t.Errorf("the live session stop did not run once with the session check: %v", stop)
	}

	// A session record this worker did not start itself is never stopped.
	f = newShareFake()
	s = newSession(f)
	s.state.hotspot = hotspotState{started: true, check: hsCheckSession, ssid: "Devpit-abcd"}
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
		t.Fatal(err)
	}
	if len(marked(f.commands(), "hs-stop")) != 0 || !s.state.empty() {
		t.Errorf("a session hotspot this worker did not start was stopped:\n%s", strings.Join(f.commands(), "\n"))
	}
}

// In hotspot mode the SMB rule 24H2 turns on when a share is created (for
// every profile) is turned off again at once: the hotspot rule already lets
// the hotspot in, and V2 would open 445 on a Public normal network. It stays
// recorded, so stop turns it off again too.
func TestHotspotModeTurnsTheShareRuleOffAtOnce(t *testing.T) {
	shareEnv(t)
	steps := []ShareRequest{
		{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword},
		{Op: ShareOpShare, User: "devpit-abcd", Path: `D:\Games`, Name: "Games-x7k2"},
	}
	const off = "Set-NetFirewallRule -Name 'FPS-SMB-In-TCP-V2' -Enabled False"
	for _, hotspot := range []bool{true, false} {
		f := newShareFake()
		f.v2Out = []string{"FW=FPS-SMB-In-TCP-V2"}
		s := newSession(f)
		reqs := steps
		if hotspot {
			reqs = append([]ShareRequest{savedHotspot}, steps...)
		}
		for _, r := range reqs {
			if _, err := run(t, s, r); err != nil {
				t.Fatalf("hotspot=%v %s: %v", hotspot, r.Op, err)
			}
		}
		cmds := f.commands()
		last := cmds[len(cmds)-1]
		if got := strings.Contains(last, off); got != hotspot {
			t.Errorf("hotspot=%v: the rule was turned off right after the share = %v:\n%s", hotspot, got, strings.Join(cmds, "\n"))
		}
		if !contains(s.state.rules, "FPS-SMB-In-TCP-V2") {
			t.Errorf("hotspot=%v: the rule is no longer recorded for stop", hotspot)
		}
	}

	// A hotspot whose rule was never confirmed leaves the share's rule to stop.
	f := newShareFake()
	f.v2Out = []string{"FW=FPS-SMB-In-TCP-V2"}
	s := newSession(f)
	s.state.hotspot = hotspotState{rule: "devpit-hs-abcd1234"}
	for _, r := range steps {
		if _, err := run(t, s, r); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(strings.Join(f.commands(), "\n"), off) {
		t.Error("the rule was turned off for a hotspot whose rule was not made")
	}
}

// TestSwitchingTheBandKeepsTheNameAndPassword pins rule 69: the band switch
// configures the very object it read from GetCurrentAccessPointConfiguration,
// changing only Band to FiveGigahertz (TetheringWiFiBand 2), so the saved
// name and password go back exactly as they were; the saved password is
// never printed, and nothing the start prints about it reaches the TUI.
func TestSwitchingTheBandKeepsTheNameAndPassword(t *testing.T) {
	shareEnv(t)
	i := strings.Index(scriptHotspotStart, "if($kind -eq 'saved'){")
	j := strings.Index(scriptHotspotStart, "$ST=$null")
	if i < 0 || j < i {
		t.Fatal("the saved branch of the start script was not found")
	}
	saved := scriptHotspotStart[i:j]
	read := strings.Index(saved, "$ap=$m.GetCurrentAccessPointConfiguration()")
	set := strings.Index(saved, "if($set5 -eq '1'){")
	conf := strings.Index(saved, "AwaitAction ($m.ConfigureAccessPointAsync($ap)) 60000")
	if read < 0 || set < read || conf < set {
		t.Fatalf("the switch does not read the saved settings, then configure them:\n%s", saved)
	}
	between := saved[read+len("$ap=$m.GetCurrentAccessPointConfiguration()") : conf]
	assigns := regexp.MustCompile(`\$ap(\.\w+)?\s*=[^=]`).FindAllString(between, -1)
	if len(assigns) != 1 || !strings.Contains(between, "$ap.Band=[Enum]::ToObject($BandT, 2)") {
		t.Errorf("between the read and the configure the switch assigns %q; want only $ap.Band to 5 GHz", assigns)
	}
	if strings.Contains(saved, "Passphrase") || strings.Contains(saved, "$ssid") || strings.Contains(saved, "$pass") {
		t.Errorf("the saved branch touches a password or a Devpit name:\n%s", saved)
	}

	const own = "MyOwnSecret1"
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	f := newShareFake()
	f.hsStartOut = append([]string{"HSBANDSET=1", "HSPASS=" + b64(own), "HSOLDPASS=" + b64(own), "saved password " + own}, savedStart("MyHotspot")...)
	r := savedHotspot
	r.SetBand5 = true
	lines, err := run(t, newSession(f), r)
	if err != nil || !contains(lines, "HSBANDSET=1") {
		t.Fatalf("err = %v, lines = %v", err, lines)
	}
	for _, l := range lines {
		if strings.Contains(l, own) || strings.Contains(l, b64(own)) {
			t.Errorf("the saved password reached the TUI: %q", l)
		}
	}
	if in := stdinOf(t, f, "hs-start", 0); in[1] != "" || in[2] != "" {
		t.Error("the band switch was given a name or a password")
	}
}

func TestParseHotspotLine(t *testing.T) {
	for line, want := range map[string]bool{"HSIP=1.2.3.4": true, "HS=on": true, "Hs=on": false, "FW=x": false, "progress": false} {
		if _, _, ok := ParseHotspotLine(line); ok != want {
			t.Errorf("ParseHotspotLine(%q) ok = %v", line, ok)
		}
	}
}
