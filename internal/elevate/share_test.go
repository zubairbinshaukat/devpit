package elevate

import (
	"context"
	"encoding/base64"
	"io/fs"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
)

// shareFake is an [Executor] and a [UserManager] that touches nothing: it
// records every command line, plays back scripted output for the PowerShell
// scripts, and keeps a set of accounts in memory.
type shareFake struct {
	mu     sync.Mutex
	calls  []string // decoded command lines, in order
	argvs  [][]string
	stdins []string // standard input of each command, in order
	users  map[string]string
	sids   map[string]string

	fwOut     []string // stdout of the firewall-on script
	aceOut    []string // stdout of the share script
	failMatch string   // a command line containing this fails once
	fwFail    bool     // the firewall script prints its rules, then fails
	failCode  int      // the exit code of a failMatch failure; 0 means 1
	others    int      // Devpit shares up besides this session's
	v2Out     []string // the share script's FW= lines (24H2's second SMB rule)
	failed    bool
	deleted   []string
	createErr error // returned by CreateUser, to play a NetUserAdd failure

	// The hotspot scripts, found by their marker comment.
	hsProbeOut    []string // stdout of the probe
	hsStartOut    []string // stdout of the start
	hsStartStderr []string // stderr of the start; with hsStartCode, a failure
	hsStartCode   int
	hsStopOut     []string // stdout of the stop
}

func newShareFake() *shareFake {
	return &shareFake{
		users: map[string]string{}, sids: map[string]string{},
		fwOut:      []string{"FW=FPS-SMB-In-TCP"},
		aceOut:     []string{`ACE=PC\devpit-abcd|Read|Allow`},
		hsProbeOut: []string{"HSCAP=0", "HSTIMEOUT=1"},
		hsStartOut: savedStart("MyHotspot"),
		hsStopOut:  []string{"HSSTOPSTATUS=0"},
	}
}

// savedStart is what the start script prints for a saved-mode start of the
// hotspot called ssid.
func savedStart(ssid string) []string {
	return []string{
		"HSMODE=saved", "HSSSID=" + base64.StdEncoding.EncodeToString([]byte(ssid)),
		"HSBAND=5", "HSSTATUS=0", "HSIP=192.168.137.1",
	}
}

// sessionStart is the same for a temp-mode, per-session start.
func sessionStart(ssid string) []string {
	return []string{
		"HSMODE=session", "HSSSID=" + base64.StdEncoding.EncodeToString([]byte(ssid)),
		"HSBAND=5", "HSSTATUS=0", "HSIP=192.168.137.1",
	}
}

// psValue decodes the [psString] a script assigns to $name.
func psValue(t *testing.T, script, name string) string {
	t.Helper()
	prefix := "$" + name + "=([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('"
	i := strings.Index(script, prefix)
	if i < 0 {
		t.Fatalf("the script sets no $%s:\n%s", name, script)
	}
	rest := script[i+len(prefix):]
	b, err := base64.StdEncoding.DecodeString(rest[:strings.Index(rest, "'")])
	if err != nil {
		t.Fatalf("$%s: %v", name, err)
	}
	return string(b)
}

// stdinOf returns the values the n-th command with hotspot marker m was
// given on standard input, decoded the way ReadIn decodes them.
func stdinOf(t *testing.T, f *shareFake, m string, n int) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.calls {
		if !strings.Contains(c, "# devpit "+m+"\n") {
			continue
		}
		if n > 0 {
			n--
			continue
		}
		var out []string
		for _, l := range strings.Split(strings.TrimSuffix(f.stdins[i], "\n"), "\n") {
			b, err := base64.StdEncoding.DecodeString(l)
			if err != nil {
				t.Fatalf("%s: stdin line %q: %v", m, l, err)
			}
			out = append(out, string(b))
		}
		return out
	}
	t.Fatalf("no %s command", m)
	return nil
}

// playHotspot plays back the hotspot scripts.
func (f *shareFake) playHotspot(text string, onLine func(stream, text string)) int {
	out := func(lines []string) {
		for _, l := range lines {
			onLine("stdout", l)
		}
	}
	switch {
	case strings.Contains(text, "# devpit hs-probe"):
		out(f.hsProbeOut)
	case strings.Contains(text, "# devpit hs-start"):
		out(f.hsStartOut)
		for _, l := range f.hsStartStderr {
			onLine("stderr", l)
		}
		return f.hsStartCode
	case strings.Contains(text, "# devpit hs-stop"):
		out(f.hsStopOut)
	case strings.Contains(text, "# devpit hs-rule-add"):
		i := strings.Index(text, "$n=") + 3
		j := strings.Index(text[i:], "\n")
		b, _ := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(text[i:i+j],
			"([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('"), "')))"))
		onLine("stdout", "HSFW="+string(b))
	}
	return 0
}

// decode turns a command line into readable text: PowerShell scripts are
// decoded from their base64 form so a test can look inside them.
func decode(argv []string) string {
	for i, a := range argv {
		if a == "-EncodedCommand" && i+1 < len(argv) {
			b, _ := base64.StdEncoding.DecodeString(argv[i+1])
			u := make([]uint16, len(b)/2)
			for j := range u {
				u[j] = uint16(b[2*j]) | uint16(b[2*j+1])<<8
			}
			return "powershell: " + string(utf16.Decode(u))
		}
	}
	return strings.Join(argv, " ")
}

func (f *shareFake) Exec(ctx context.Context, argv []string, onLine func(stream, text string)) (int, error) {
	return f.ExecInput(ctx, argv, "", onLine)
}

// ExecInput records what the command was given on its standard input.
func (f *shareFake) ExecInput(_ context.Context, argv []string, stdin string, onLine func(stream, text string)) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	text := decode(argv)
	f.calls = append(f.calls, text)
	f.argvs = append(f.argvs, argv)
	f.stdins = append(f.stdins, stdin)
	if f.failMatch != "" && !f.failed && strings.Contains(text, f.failMatch) {
		f.failed = true
		if f.failCode != 0 {
			return f.failCode, nil
		}
		return 1, nil
	}
	switch {
	case strings.Contains(text, "# devpit hs-"):
		return f.playHotspot(text, onLine), nil
	case strings.Contains(text, "OTHERS="):
		onLine("stdout", "OTHERS="+strconv.Itoa(f.others))
	case strings.Contains(text, "New-SmbShare"):
		for _, l := range f.v2Out {
			onLine("stdout", l)
		}
		for _, l := range f.aceOut {
			onLine("stdout", l)
		}
	case strings.Contains(text, "Get-NetFirewallRule"):
		for _, l := range f.fwOut {
			onLine("stdout", l)
		}
		if f.fwFail {
			return 1, nil
		}
	}
	return 0, nil
}

func (f *shareFake) Remove(context.Context, string) error { return nil }

func (f *shareFake) CreateUser(name, password string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.users[name] = password
	f.sids[name] = "S-1-5-21-1-2-3-1004"
	return nil
}

func (f *shareFake) DeleteUser(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.users, name)
	delete(f.sids, name) // a deleted account's SID maps to nobody
	f.deleted = append(f.deleted, name)
	return nil
}

func (f *shareFake) UserSID(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sids[name], nil
}

// SIDAccount says which fake account holds sid, if any.
func (f *shareFake) SIDAccount(sid string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, s := range f.sids {
		if s == sid {
			return name, true, nil
		}
	}
	return "", false, nil
}

func (f *shareFake) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *shareFake) hasUser(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.users[name]
	return ok
}

// dirInfo is a stat result that says "a folder".
type dirInfo struct{ fs.FileInfo }

func (dirInfo) IsDir() bool { return true }

// shareEnv points the worker's environment at a fake Windows and makes every
// folder "exist".
func shareEnv(t *testing.T) {
	t.Helper()
	t.Setenv("WINDIR", `C:\Windows`)
	t.Setenv("SystemDrive", `C:`)
	t.Setenv("ProgramFiles", `C:\Program Files`)
	t.Setenv("ProgramData", `C:\ProgramData`)
}

// newSession returns a session over f whose stat says every path is a folder.
func newSession(f *shareFake) *shareSession {
	s := newShareSession(f)
	s.stat = func(string) (fs.FileInfo, error) { return dirInfo{}, nil }
	s.resolve = func(p string) (string, error) { return p, nil }
	return s
}

func run(t *testing.T, s *shareSession, r ShareRequest) ([]string, error) {
	t.Helper()
	var lines []string
	err := s.run(context.Background(), r, func(_, text string) { lines = append(lines, text) })
	return lines, err
}

const goodPassword = "407193"

func TestShareValidate(t *testing.T) {
	shareEnv(t)
	good := ShareRequest{Op: ShareOpShare, User: "devpit-abcd", Name: "Games-x7k2", Path: `D:\Games`}
	tests := []struct {
		name   string
		mutate func(*ShareRequest)
		ok     bool
	}{
		{"good share", func(*ShareRequest) {}, true},
		{"user is Administrator", func(r *ShareRequest) { r.User = "Administrator" }, false},
		{"user is not devpit-shaped", func(r *ShareRequest) { r.User = "devpit-ABCD" }, false},
		{"user carries a command", func(r *ShareRequest) { r.User = "devpit-abcd; calc" }, false},
		{"share name with a quote", func(r *ShareRequest) { r.Name = "x'; calc; '" }, false},
		{"share name with a space", func(r *ShareRequest) { r.Name = "my share" }, false},
		{"empty share name", func(r *ShareRequest) { r.Name = "" }, false},
		{"path with an apostrophe", func(r *ShareRequest) { r.Path = `D:\Bob's Games` }, true},
		{"path with a double quote", func(r *ShareRequest) { r.Path = `D:\say "hi"` }, false},
		{"path is UNC", func(r *ShareRequest) { r.Path = `\\host\share` }, false},
		{"path is relative", func(r *ShareRequest) { r.Path = `Games` }, false},
		{"path climbs out", func(r *ShareRequest) { r.Path = `D:\Games\..\..\Windows` }, false},
		{"path is a wildcard", func(r *ShareRequest) { r.Path = `D:\Ga*` }, false},
		{"path is a stream", func(r *ShareRequest) { r.Path = `D:\Games:evil` }, false},
		{"path is under Windows", func(r *ShareRequest) { r.Path = `C:\Windows\System32` }, false},
		{"path is Program Files", func(r *ShareRequest) { r.Path = `c:\program files\App` }, false},
		{"path is the system drive", func(r *ShareRequest) { r.Path = `C:\` }, false},
		{"another drive root", func(r *ShareRequest) { r.Path = `D:\` }, false},
		{"path has spaces and unicode", func(r *ShareRequest) { r.Path = `D:\Mes Photos\été 写真` }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := good
			tt.mutate(&r)
			err := r.Validate()
			if (err == nil) != tt.ok {
				t.Errorf("Validate = %v, want ok=%v", err, tt.ok)
			}
		})
	}

	hotspot := []struct {
		name   string
		mutate func(*ShareRequest)
		ok     bool
	}{
		{"good temp hotspot", func(*ShareRequest) {}, true},
		{"6 GHz", func(r *ShareRequest) { r.Band = "6" }, true},
		{"auto band", func(r *ShareRequest) { r.Band = "auto" }, true},
		{"2.4 GHz is not asked for", func(r *ShareRequest) { r.Band = "2.4" }, false},
		{"no band", func(r *ShareRequest) { r.Band = "" }, false},
		{"band carries a command", func(r *ShareRequest) { r.Band = "5; calc" }, false},
		{"ssid not Devpit-shaped", func(r *ShareRequest) { r.SSID = "HomeWiFi" }, false},
		{"ssid upper case", func(r *ShareRequest) { r.SSID = "Devpit-ABCD" }, false},
		{"ssid with a quote", func(r *ShareRequest) { r.SSID = "Devpit-ab'cd" }, false},
		{"ssid too long", func(r *ShareRequest) { r.SSID = "Devpit-abcdefghijklm" }, false},
		{"passphrase 7 digits", func(r *ShareRequest) { r.Passphrase = "4071935" }, false},
		{"passphrase 9 digits", func(r *ShareRequest) { r.Passphrase = "407193581" }, false},
		{"passphrase with a letter", func(r *ShareRequest) { r.Passphrase = "4071935a" }, false},
		{"passphrase with a quote", func(r *ShareRequest) { r.Passphrase = "4071'358" }, false},
		{"passphrase non-ASCII digits", func(r *ShareRequest) { r.Passphrase = "٤٠٧١٩٣٥٨" }, false},
		{"temp with the band switch", func(r *ShareRequest) { r.SetBand5 = true }, false},
		{"unknown configuration", func(r *ShareRequest) { r.HotspotConfig = "mine" }, false},
		{"start with a cleanup rule", func(r *ShareRequest) { r.HotspotRule = "devpit-hs-abcd" }, false},
		{"start with a recorded start", func(r *ShareRequest) { r.HotspotStarted = true }, false},
		{"saved", func(r *ShareRequest) { *r = ShareRequest{Op: ShareOpHotspot, HotspotConfig: "saved"} }, true},
		{"saved with the band switch", func(r *ShareRequest) {
			*r = ShareRequest{Op: ShareOpHotspot, HotspotConfig: "saved", SetBand5: true}
		}, true},
		{"saved with a name", func(r *ShareRequest) { r.HotspotConfig, r.Passphrase, r.Band = "saved", "", "" }, false},
		{"saved with a password", func(r *ShareRequest) { r.HotspotConfig, r.SSID, r.Band = "saved", "", "" }, false},
		{"saved with a band", func(r *ShareRequest) { r.HotspotConfig, r.SSID, r.Passphrase = "saved", "", "" }, false},
	}
	for _, tt := range hotspot {
		t.Run("hotspot/"+tt.name, func(t *testing.T) {
			r := tempHotspot
			tt.mutate(&r)
			err := r.Validate()
			if (err == nil) != tt.ok {
				t.Errorf("Validate = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && r.Passphrase != "" && strings.Contains(err.Error(), r.Passphrase) {
				t.Errorf("the refusal holds the passphrase: %v", err)
			}
		})
	}
}

// The two hotspot starts the tests use.
var (
	savedHotspot = ShareRequest{Op: ShareOpHotspot, HotspotConfig: "saved"}
	tempHotspot  = ShareRequest{Op: ShareOpHotspot, HotspotConfig: "temp", SSID: "Devpit-abcd", Passphrase: goodPassphrase, Band: "5"}
)

const goodPassphrase = "40719358"

func TestShareValidateOtherOps(t *testing.T) {
	shareEnv(t)
	bad := []ShareRequest{
		{Op: "format-disk"},
		{Op: ShareOpProfile, Index: 0, Category: "Private", Original: "Public"},
		{Op: ShareOpProfile, Index: 5, Category: "DomainAuthenticated", Original: "Public"},
		{Op: ShareOpProfile, Index: 5, Category: "Private", Original: "Private; calc"},
		{Op: ShareOpAccount, User: "devpit-abcd", Password: "short"},
		{Op: ShareOpAccount, User: "devpit-abcd", Password: ""},
		{Op: ShareOpAccount, User: "devpit-abcd", Password: "40719"},    // 5 digits
		{Op: ShareOpAccount, User: "devpit-abcd", Password: "4071930"},  // 7 digits
		{Op: ShareOpAccount, User: "devpit-abcd", Password: "40719a"},   // a letter
		{Op: ShareOpAccount, User: "devpit-abcd", Password: "abcdef"},   // letters only
		{Op: ShareOpAccount, User: "devpit-abcd", Password: "407 93"},   // a space
		{Op: ShareOpAccount, User: "devpit-abcd", Password: "4071'3"},   // a quote
		{Op: ShareOpAccount, User: "devpit-abcd", Password: `4071"3`},   // a double quote
		{Op: ShareOpAccount, User: "devpit-abcd", Password: "407193\n"}, // a trailing newline
		{Op: ShareOpAccount, User: "devpit-abcd", Password: "٤٠٧١٩٣"},   // non-ASCII digits
		{Op: ShareOpCleanup, User: "root"},
		{Op: ShareOpCleanup, Rules: []string{"Allow-Everything"}},
		{Op: ShareOpCleanup, Rules: []string{"FPS-x; calc"}},
		{Op: ShareOpCleanup, SID: "S-1-5-18"},
		{Op: ShareOpCleanup, Index: 4},
		{Op: ShareOpHotspot},
		{Op: ShareOpCleanup, HotspotRule: "FPS-SMB-In-TCP"},
		{Op: ShareOpCleanup, HotspotRule: "devpit-hs-*"},
		{Op: ShareOpCleanup, HotspotRule: "devpit-hs-ab cd"},
		{Op: ShareOpCleanup, HotspotConfig: "temp", HotspotSSID: "MyHotspot"},
		{Op: ShareOpCleanup, HotspotConfig: "saved", HotspotSSID: "bad\nname"},
		{Op: ShareOpCleanup, HotspotConfig: "saved", HotspotSSID: strings.Repeat("x", 33)},
		{Op: ShareOpCleanup, HotspotConfig: "saved", HotspotSSID: "\xff\xfe"},
		{Op: ShareOpCleanup, HotspotStarted: true, HotspotSSID: "MyHotspot"},
		{Op: ShareOpCleanup, HotspotTimeoutChanged: true},
		{Op: ShareOpCleanup, HotspotConfig: "saved", HotspotLegacy: true},
		{Op: ShareOpCleanup, HotspotConfig: "mine"},
		{Op: ShareOpCleanup, Passphrase: goodPassphrase},
		{Op: ShareOpCleanup, SSID: "Devpit-abcd"},
		{Op: ShareOpCleanup, Band: "5"},
		{Op: ShareOpCleanup, SetBand5: true},
	}
	for _, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%+v was accepted", r)
		}
	}
	good := []ShareRequest{
		{Op: ShareOpProfile, Index: 5, Category: "Private", Original: "Public"},
		{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword},
		{Op: ShareOpAccount, User: "devpit-abcd", Password: "000000"},
		{Op: ShareOpStop},
		{Op: ShareOpFirewall},
		{Op: ShareOpCleanup},
		{
			Op: ShareOpCleanup, User: "devpit-abcd", SID: "S-1-5-21-1-2-3-1004", Name: "Games-x7k2", Path: `D:\Games`,
			Rules: []string{"FPS-SMB-In-TCP"}, Index: 5, Original: "Public",
		},
		savedHotspot,
		tempHotspot,
		{
			Op: ShareOpCleanup, HotspotConfig: "saved", HotspotRule: "devpit-hs-abcd1234", HotspotStarted: true,
			HotspotTimeoutWasOn: true, HotspotTimeoutChanged: true, HotspotSSID: "Bob’s hotspot é",
		},
		{Op: ShareOpCleanup, HotspotConfig: "temp", HotspotStarted: true, HotspotSSID: "Devpit-abcd", HotspotLegacy: true},
		{Op: ShareOpCleanup, HotspotConfig: "saved", HotspotStarted: true}, // cut off before the name arrived
	}
	for _, r := range good {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v refused: %v", r, err)
		}
	}
}

func TestShareRequestStringHidesThePassword(t *testing.T) {
	r := ShareRequest{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword}
	if strings.Contains(r.String(), goodPassword) {
		t.Error("String leaks the password")
	}
}

func TestPowerShellIsPassedEncodedFromItsFullPath(t *testing.T) {
	t.Setenv("WINDIR", `C:\Windows`)
	argv := psArgv("Write-Output 'héllo'")
	if !strings.Contains(argv[0], "powershell.exe") || !strings.HasPrefix(argv[0], `C:\Windows`) {
		t.Errorf("powershell is started as %q, want the full System32 path", argv[0])
	}
	if got := decode(argv); got != "powershell: Write-Output 'héllo'" {
		t.Errorf("round trip = %q", got)
	}

	// Every hotspot script goes the same way.
	shareEnv(t)
	f := newShareFake()
	f.hsStartOut = sessionStart("Devpit-abcd")
	s := newSession(f)
	for _, r := range []ShareRequest{tempHotspot, {Op: ShareOpStop}} {
		if _, err := run(t, s, r); err != nil {
			t.Fatalf("%s: %v", r.Op, err)
		}
	}
	seen := map[string]bool{}
	for i, a := range f.argvs {
		if len(a) != 6 || a[0] != `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe` || a[4] != "-EncodedCommand" {
			t.Errorf("hotspot command %d is %q", i, a)
		}
		for _, m := range []string{"hs-probe", "hs-timeout-off", "hs-start", "hs-rule-add", "hs-rule-remove", "hs-stop", "hs-timeout-on"} {
			if strings.Contains(f.calls[i], "# devpit "+m+"\n") {
				seen[m] = true
			}
		}
	}
	if len(seen) != 7 {
		t.Errorf("hotspot scripts seen: %v, want all seven", seen)
	}
}

func TestShareRequestStringHidesThePassphrase(t *testing.T) {
	r := tempHotspot
	got := r.String()
	if strings.Contains(got, goodPassphrase) || strings.Contains(got, base64.StdEncoding.EncodeToString([]byte(goodPassphrase))) {
		t.Errorf("String leaks the passphrase: %s", got)
	}
	if !strings.Contains(got, "Devpit-abcd") || !strings.Contains(got, "hotspot") {
		t.Errorf("String = %q, want the op and the name", got)
	}
}

func TestFullLifecycleAndTheOrderOfUndoing(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	s := newSession(f)

	steps := []ShareRequest{
		{Op: ShareOpProfile, Index: 7, Category: "Private", Original: "Public"},
		{Op: ShareOpFirewall},
		{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword},
		{Op: ShareOpGrant, User: "devpit-abcd", Path: `D:\Games`},
		{Op: ShareOpShare, User: "devpit-abcd", Path: `D:\Games`, Name: "Games-x7k2"},
	}
	var infoLines []string
	for _, r := range steps {
		lines, err := run(t, s, r)
		if err != nil {
			t.Fatalf("%s: %v", r.Op, err)
		}
		infoLines = append(infoLines, lines...)
	}
	if !contains(infoLines, "FW=FPS-SMB-In-TCP") || !contains(infoLines, "SID=S-1-5-21-1-2-3-1004") {
		t.Errorf("the worker did not report the rule and the SID: %v", infoLines)
	}
	if !f.hasUser("devpit-abcd") {
		t.Fatal("the account was not created")
	}

	create := strings.Join(f.commands(), "\n")
	for _, want := range []string{
		"Set-NetConnectionProfile -InterfaceIndex 7 -NetworkCategory Private",
		"@FirewallAPI.dll,-28502",
		"FPS-SMB-In-TCP",
		"New-SmbShare -Name $n -Path $p -ReadAccess $u -Description $d",
		"$n=" + psString("Games-x7k2"),
		"$p=" + psString(`D:\Games`),
		`icacls.exe D:\Games /grant *S-1-5-21-1-2-3-1004:(OI)(CI)RX`,
	} {
		if !strings.Contains(create, want) {
			t.Errorf("no command contained %q:\n%s", want, create)
		}
	}
	if strings.Contains(create, "Everyone") || strings.Contains(create, "-FullAccess") || strings.Contains(create, "-ChangeAccess") {
		t.Error("the share command grants more than read access to the one account")
	}

	before := len(f.commands())
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	undo := f.commands()[before:]
	wantOrder := []string{
		"Remove-SmbShare", "/remove:g *S-1-5-21-1-2-3-1004", "OTHERS=",
		"Set-NetFirewallRule -Name 'FPS-SMB-In-TCP' -Enabled False",
		"Set-NetConnectionProfile -InterfaceIndex 7 -NetworkCategory Public",
	}
	if len(undo) != len(wantOrder) {
		t.Fatalf("stop ran %d commands, want %d:\n%s", len(undo), len(wantOrder), strings.Join(undo, "\n"))
	}
	for i, w := range wantOrder {
		if !strings.Contains(undo[i], w) {
			t.Errorf("undo step %d = %q, want it to contain %q", i, undo[i], w)
		}
	}
	if f.hasUser("devpit-abcd") {
		t.Error("stop left the account behind")
	}
	if !s.state.empty() {
		t.Errorf("state after stop = %+v", s.state)
	}
	// Stopping twice does nothing more.
	n := len(f.commands())
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil || len(f.commands()) != n {
		t.Errorf("a second stop ran commands or failed: %v", err)
	}
}

func TestThePasswordIsNeverOnACommandLine(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	s := newSession(f)
	if _, err := run(t, s, ShareRequest{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword}); err != nil {
		t.Fatal(err)
	}
	if f.users["devpit-abcd"] != goodPassword {
		t.Fatal("the account did not get the password")
	}
	for _, argv := range f.argvs {
		for _, a := range argv {
			if strings.Contains(a, goodPassword) {
				t.Errorf("the password is on a command line: %q", a)
			}
		}
	}
	for _, c := range f.commands() {
		if strings.Contains(c, goodPassword) {
			t.Errorf("the password is inside a script: %q", c)
		}
	}
}

func TestAShareWithAnUnexpectedAccessEntryIsRefusedAndStaysTrackedForRemoval(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.aceOut = []string{`ACE=PC\devpit-abcd|Read|Allow`, `ACE=Everyone|Read|Allow`}
	s := newSession(f)
	if _, err := run(t, s, ShareRequest{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword}); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, s, ShareRequest{Op: ShareOpShare, User: "devpit-abcd", Path: `D:\Games`, Name: "Games-x7k2"})
	if err == nil || !strings.Contains(err.Error(), "unexpected access entry") {
		t.Fatalf("err = %v", err)
	}
	if s.state.shareName == "" {
		t.Error("the bad share is not tracked, so stop would leave it open")
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.commands(), "\n"), "Remove-SmbShare") {
		t.Error("stop did not remove the bad share")
	}
}

func TestAShareWithFullControlIsRefused(t *testing.T) {
	if err := checkACL([]string{`ACE=PC\devpit-abcd|Full|Allow`}, "devpit-abcd"); err == nil {
		t.Error("full control was accepted")
	}
	if err := checkACL(nil, "devpit-abcd"); err == nil {
		t.Error("no access list was accepted")
	}
	if err := checkACL([]string{`ACE=OTHERPC\devpit-abcd|Read|Deny`}, "devpit-abcd"); err == nil {
		t.Error("a deny entry was accepted")
	}
}

func TestTeardownKeepsGoingPastAFailureAndFinishesOnTheNextStop(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	s := newSession(f)
	for _, r := range []ShareRequest{
		{Op: ShareOpProfile, Index: 7, Category: "Private", Original: "Public"},
		{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword},
		{Op: ShareOpGrant, User: "devpit-abcd", Path: `D:\Games`},
	} {
		if _, err := run(t, s, r); err != nil {
			t.Fatal(err)
		}
	}
	f.failMatch = "/remove:g"
	_, err := run(t, s, ShareRequest{Op: ShareOpStop})
	if err == nil || !strings.Contains(err.Error(), "folder permission") {
		t.Fatalf("first stop err = %v, want the permission failure", err)
	}
	if f.hasUser("devpit-abcd") {
		t.Error("the account was not removed although only the permission failed")
	}
	if s.state.profileIndex != 0 {
		t.Error("the network category was not restored after an earlier failure")
	}
	if s.state.grantPath == "" {
		t.Error("the failed permission removal was forgotten")
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	if !s.state.empty() {
		t.Errorf("state after the second stop = %+v", s.state)
	}
}

func TestCleanupUndoesAManifestWithoutAnyState(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.users["devpit-abcd"] = "x"
	s := newSession(f)
	_, err := run(t, s, ShareRequest{
		Op: ShareOpCleanup, User: "devpit-abcd", SID: "S-1-5-21-1-2-3-1004", Path: `D:\Games`, Name: "Games-x7k2",
		Rules: []string{"FPS-SMB-In-TCP"}, Index: 7, Original: "Public",
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.hasUser("devpit-abcd") {
		t.Error("cleanup left the account")
	}
	all := strings.Join(f.commands(), "\n")
	for _, want := range []string{"Remove-SmbShare", "/remove:g", "Enabled False", "-NetworkCategory Public"} {
		if !strings.Contains(all, want) {
			t.Errorf("cleanup did not run %q:\n%s", want, all)
		}
	}
	// A cleanup for an account that no longer exists is not an error.
	if _, err := run(t, newSession(newShareFake()), ShareRequest{Op: ShareOpCleanup, User: "devpit-zzzz"}); err != nil {
		t.Errorf("cleanup of a missing account failed: %v", err)
	}
}

func TestAccountOperationsAreRefusedWithoutAUserManager(t *testing.T) {
	shareEnv(t)
	s := newShareSession(plainExecutor{})
	_, err := run(t, s, ShareRequest{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword})
	if err == nil {
		t.Error("an account was created with no user manager")
	}
}

func TestAPasswordPolicyRefusalSaysWhatToChangeAndNeverHoldsThePassword(t *testing.T) {
	for _, code := range []syscall.Errno{2244, 2245, 2704, 2705} {
		shareEnv(t)
		f := newShareFake()
		f.createErr = code
		_, err := run(t, newSession(f), ShareRequest{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword})
		if err == nil {
			t.Fatalf("error %d: the account was created", code)
		}
		msg := err.Error()
		for _, want := range []string{"6-digit", "Password Policy", "Minimum password length", "complexity", "Windows error " + strconv.Itoa(int(code))} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %d: message lacks %q:\n%s", code, want, msg)
			}
		}
		if strings.Contains(msg, goodPassword) {
			t.Errorf("error %d: the message holds the password:\n%s", code, msg)
		}
	}
}

func TestAnotherAccountFailureKeepsItsOwnText(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.createErr = syscall.Errno(2224) // NERR_UserExists
	_, err := run(t, newSession(f), ShareRequest{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword})
	if err == nil || !strings.Contains(err.Error(), "creating the account") || strings.Contains(err.Error(), "Password Policy") {
		t.Errorf("err = %v", err)
	}
	if err != nil && strings.Contains(err.Error(), goodPassword) {
		t.Error("the message holds the password")
	}
}

// plainExecutor is an Executor that is not a UserManager.
type plainExecutor struct{}

func (plainExecutor) Exec(context.Context, []string, func(string, string)) (int, error) {
	return 0, nil
}
func (plainExecutor) Remove(context.Context, string) error { return nil }

func TestARefusedRequestRunsNothing(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	s := newSession(f)
	_, err := run(t, s, ShareRequest{Op: ShareOpShare, User: "devpit-abcd", Path: `C:\Windows`, Name: "x"})
	if err == nil || len(f.commands()) != 0 {
		t.Errorf("err = %v, commands = %v", err, f.commands())
	}
}

func TestGrantOfAFolderThatIsNotThereFails(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	s := newSession(f)
	s.stat = func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	if _, err := run(t, s, ShareRequest{Op: ShareOpGrant, User: "devpit-abcd", Path: `D:\Gone`}); err == nil {
		t.Error("granted access to a folder that does not exist")
	}
}

// TestTheWorkerCleansUpByItselfWhenTheTUIDies is the crash test: the pipe is
// cut with no stop and no shutdown, exactly what killing Devpit does, and the
// worker must still undo everything it did.
func TestTheWorkerCleansUpByItselfWhenTheTUIDies(t *testing.T) {
	shareEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	f := newShareFake()
	serverConn, clientConn := net.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- serveConn(ctx, serverConn, f, true, 1)
	}()
	c, err := newClient(ctx, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	// The worker in this test uses os.Stat on D:\Games, which does not
	// exist, so run the steps that do not need a real folder.
	for _, r := range []ShareRequest{
		{Op: ShareOpProfile, Index: 7, Category: "Private", Original: "Public"},
		{Op: ShareOpFirewall},
		{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword},
	} {
		if err := c.Share(ctx, r, nil); err != nil {
			t.Fatalf("%s: %v", r.Op, err)
		}
	}
	if !f.hasUser("devpit-abcd") {
		t.Fatal("setup did not create the account")
	}

	// Kill the TUI's end of the pipe. No shutdown request is sent.
	_ = clientConn.Close()

	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not notice the pipe was gone")
	}
	if f.hasUser("devpit-abcd") {
		t.Error("the worker left the temporary account behind")
	}
	all := strings.Join(f.commands(), "\n")
	if !strings.Contains(all, "Enabled False") || !strings.Contains(all, "-NetworkCategory Public") {
		t.Errorf("the worker did not restore the firewall and the network:\n%s", all)
	}
}

func TestTheWorkerCleansUpOnAShutdownWithoutAStop(t *testing.T) {
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
	if err := c.Share(ctx, ShareRequest{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	<-served
	if f.hasUser("devpit-abcd") {
		t.Error("a shutdown left the account behind")
	}
}

func TestShareOverTheWireRefusesABadRequest(t *testing.T) {
	shareEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f := newShareFake()
	c, _ := dial(ctx, t, f)
	defer c.Close() //nolint:errcheck // test cleanup
	err := c.Share(ctx, ShareRequest{Op: ShareOpAccount, User: "Administrator", Password: goodPassword}, nil)
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("err = %v, want a refusal", err)
	}
	if f.hasUser("Administrator") || len(f.commands()) != 0 {
		t.Error("a refused request had an effect")
	}
}
