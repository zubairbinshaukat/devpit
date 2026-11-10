package elevate

import (
	"strings"
	"testing"
)

// The SMB rule is picked by its exact name: a wildcard also caught
// FPS-SMB-In-TCP-NoScope, the Domain rule that takes SMB from any address.
func TestTheFirewallScriptPicksOnlyTheExactSMBRule(t *testing.T) {
	if !strings.Contains(scriptFirewallOn, "$_.Name -eq 'FPS-SMB-In-TCP'") {
		t.Error("the SMB rule is not matched by its exact name")
	}
	if strings.Contains(scriptFirewallOn, "FPS-SMB-In-TCP*") {
		t.Error("the script still matches the SMB rule with a wildcard")
	}
	if !strings.Contains(scriptFirewallOn, "-notlike '*NoScope*'") {
		t.Error("the whole-group fallback can turn on a NoScope rule")
	}
}

// On 24H2, creating a share can turn on FPS-SMB-In-TCP-V2 (every profile)
// by itself. The share script reports it, and stop turns it off again.
func TestA24H2RuleTheShareTurnedOnIsTurnedOffAgain(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.v2Out = []string{"FW=FPS-SMB-In-TCP-V2"}
	s := newSession(f)
	var lines []string
	for _, r := range []ShareRequest{
		{Op: ShareOpFirewall},
		{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword},
		{Op: ShareOpShare, User: "devpit-abcd", Path: `D:\Games`, Name: "Games-x7k2"},
	} {
		got, err := run(t, s, r)
		if err != nil {
			t.Fatalf("%s: %v", r.Op, err)
		}
		lines = append(lines, got...)
	}
	if !contains(lines, "FW=FPS-SMB-In-TCP-V2") {
		t.Errorf("the TUI was not told about the second rule: %v", lines)
	}
	if !strings.Contains(scriptShare, "-28672") ||
		strings.Index(scriptShare, "$off=") > strings.Index(scriptShare, "New-SmbShare") {
		t.Error("the share script does not read the 24H2 rules before creating the share")
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.commands(), "\n"),
		"Set-NetFirewallRule -Name 'FPS-SMB-In-TCP','FPS-SMB-In-TCP-V2' -Enabled False") {
		t.Errorf("stop did not turn both rules off:\n%s", strings.Join(f.commands(), "\n"))
	}
}

// With a second Devpit window still sharing, stopping this one must not cut
// the other off by closing the firewall or making the network Public.
func TestStopLeavesTheFirewallAloneWhileAnotherDevpitShareIsUp(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.others = 1
	s := newSession(f)
	for _, r := range []ShareRequest{
		profileReq,
		{Op: ShareOpFirewall},
		{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword},
		{Op: ShareOpShare, User: "devpit-abcd", Path: `D:\Games`, Name: "Games-x7k2"},
	} {
		if _, err := run(t, s, r); err != nil {
			t.Fatalf("%s: %v", r.Op, err)
		}
	}
	before := len(f.commands())
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
		t.Fatal(err)
	}
	undo := strings.Join(f.commands()[before:], "\n")
	if strings.Contains(undo, "Enabled False") || strings.Contains(undo, "Set-NetConnectionProfile") {
		t.Errorf("stop reverted what the other share needs:\n%s", undo)
	}
	if !strings.Contains(undo, "Remove-SmbShare") || f.hasUser("devpit-abcd") {
		t.Error("stop did not remove this window's own share and account")
	}
	if !s.state.empty() {
		t.Errorf("state after stop = %+v", s.state)
	}
}

// If the worker cannot tell whether another share is up, it keeps the
// firewall and network record, so a later stop can finish.
func TestAFailedCheckForOtherSharesKeepsTheRecord(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.failMatch = "OTHERS="
	s := newSession(f)
	for _, r := range []ShareRequest{profileReq, {Op: ShareOpFirewall}} {
		if _, err := run(t, s, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err == nil {
		t.Fatal("stop succeeded without knowing whether another share is up")
	}
	if len(s.state.rules) == 0 || s.state.profileIndex == 0 {
		t.Fatalf("the record was dropped: %+v", s.state)
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil || !s.state.empty() {
		t.Fatalf("second stop: %v, state %+v", err, s.state)
	}
}

// The hotspot's rule lets in TCP 445 from the hotspot's own /24 and nothing
// else; an address that is not private stops the operation before any rule
// is made, and the hotspot it started is still stopped.
func TestTheHotspotRuleIsScopedToTheHotspotSubnet(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	if _, err := run(t, newSession(f), savedHotspot); err != nil {
		t.Fatal(err)
	}
	add := marked(f.commands(), "hs-rule-add")
	if len(add) != 1 {
		t.Fatalf("rule scripts = %v", add)
	}
	if got := psValue(t, add[0], "ra"); got != "192.168.137.0/24" {
		t.Errorf("the rule lets in %q, want the hotspot's /24", got)
	}
	if got := psValue(t, add[0], "d"); got != hsRuleDescription {
		t.Errorf("the rule's description is %q", got)
	}
	if got := psValue(t, add[0], "n"); !hsRulePattern.MatchString(got) {
		t.Errorf("the rule is named %q", got)
	}
	if !strings.Contains(add[0], "-Direction Inbound -Protocol TCP -LocalPort 445 -RemoteAddress $ra -Profile Any -Action Allow") {
		t.Errorf("the rule is not inbound TCP 445 from $ra only:\n%s", add[0])
	}

	for _, ip := range []string{"8.8.8.8", "", "garbage", "fe80::1", "192.168.137.1; calc", "127.0.0.1", "169.254.1.1"} {
		f := newShareFake()
		f.hsStartOut = append(savedStart("MyHotspot"), "HSIP="+ip)
		s := newSession(f)
		if _, err := run(t, s, savedHotspot); err == nil {
			t.Errorf("HSIP=%q was accepted", ip)
		}
		if len(marked(f.commands(), "hs-rule-add")) != 0 || s.state.hotspot.rule != "" {
			t.Errorf("HSIP=%q: a rule was made", ip)
		}
		if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil || len(marked(f.commands(), "hs-stop")) != 1 {
			t.Errorf("HSIP=%q: the started hotspot was not stopped (%v)", ip, err)
		}
	}
	if got, err := hotspotSubnet("10.20.30.40"); err != nil || got != "10.20.30.0/24" {
		t.Errorf("hotspotSubnet(10.20.30.40) = %q, %v", got, err)
	}
}

// The rule is removed by its exact name and Devpit's description: never by
// a wildcard, never a rule of the user's that happens to share the name.
func TestTheHotspotRuleIsRemovedByExactNameOnly(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	s := newSession(f)
	if _, err := run(t, s, savedHotspot); err != nil {
		t.Fatal(err)
	}
	name := s.state.hotspot.rule
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
		t.Fatal(err)
	}
	rm := marked(f.commands(), "hs-rule-remove")
	if len(rm) != 1 || psValue(t, rm[0], "n") != name || psValue(t, rm[0], "d") != hsRuleDescription {
		t.Fatalf("remove scripts = %v, want one for %s", rm, name)
	}
	if !strings.Contains(scriptHotspotRuleRemove, "$_.Name -ceq $n -and $_.Description -ceq $d") ||
		!strings.Contains(scriptHotspotRuleRemove, "if($r.Count -eq 1)") {
		t.Error("the removal does not insist on the exact name and the description")
	}
	if strings.Contains(scriptHotspotRuleRemove, "*") || strings.Contains(scriptHotspotRuleRemove, "-like") {
		t.Error("the removal matches by wildcard")
	}
	for _, bad := range []string{"devpit-hs-*", "devpit-hs-ab?d", "devpit-hs-[ab]", "FPS-SMB-In-TCP", "devpit-hs-abcd'"} {
		if err := (ShareRequest{Op: ShareOpCleanup, HotspotRule: bad}).Validate(); err == nil {
			t.Errorf("a cleanup of rule %q was accepted", bad)
		}
	}
}

// icacls fails with 1332 when the account the permission names is gone;
// then there is nothing left to remove.
func TestAPermissionForAnAccountThatIsGoneCountsAsRemoved(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.failMatch, f.failCode = "/remove:g", errnoNoneMapped
	s := newSession(f)
	for _, r := range []ShareRequest{
		{Op: ShareOpAccount, User: "devpit-abcd", Password: goodPassword},
		{Op: ShareOpGrant, User: "devpit-abcd", Path: `D:\Games`},
	} {
		if _, err := run(t, s, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if s.state.grantPath != "" {
		t.Error("the permission is still recorded")
	}
}

// The worker refuses what host.CheckFolder refuses (a second line of
// defence; the two are kept in step by this table, which follows
// internal/share/host/safepath.go).
func TestValidateSharePathMirrorsHostCheckFolder(t *testing.T) {
	shareEnv(t)
	t.Setenv("SystemRoot", `C:\Windows`)
	t.Setenv("ProgramFiles(x86)", `C:\Program Files (x86)`)
	t.Setenv("ProgramW6432", `C:\Program Files`)
	t.Setenv("USERPROFILE", `C:\Users\Dev`)
	cases := map[string]bool{
		`C:\`:                               false, // a whole drive
		`D:\`:                               false,
		`C:\Windows\Temp`:                   false,
		`C:\Program Files (x86)\Steam`:      false,
		`C:\ProgramData\x`:                  false,
		`C:\Users`:                          false, // above every profile
		`C:\Users\Dev`:                      false, // the profile itself
		`C:\Users\Other`:                    false, // another whole profile
		`C:\Users\Dev\AppData\Local\x`:      false,
		`C:\Users\Other\.ssh`:               false,
		`C:\Users\Dev\Documents\Games`:      true,
		`C:\Users\Other\Pictures`:           true,
		`D:\Games`:                          true,
		`D:\Bob's Games`:                    true,
		`c:/users/dev/appdata`:              false,
		`C:\Users\Dev\Documents\AppData`:    true, // only the profile's own AppData
		`E:\Backups\Users\Dev\AppData\Copy`: true,
	}
	for p, ok := range cases {
		if err := validateSharePath(p); (err == nil) != ok {
			t.Errorf("validateSharePath(%q) = %v, want ok=%v", p, err, ok)
		}
	}
}
