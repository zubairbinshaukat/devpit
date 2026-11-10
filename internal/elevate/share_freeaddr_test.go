package elevate

import (
	"strings"
	"testing"
)

func TestFreeAddrValidate(t *testing.T) {
	good := []string{"192.168.137.1", "10.0.0.1", "172.16.5.4", "172.31.255.254", "192.168.0.1"}
	for _, ip := range good {
		if err := (ShareRequest{Op: ShareOpFreeAddr, FreeIP: ip}).Validate(); err != nil {
			t.Errorf("%q was refused: %v", ip, err)
		}
	}
	bad := []string{
		"", "8.8.8.8", "127.0.0.1", "169.254.1.1", "172.32.0.1", "192.169.1.1", "11.0.0.1",
		"192.168.137.01", "192.168.137.256", "192.168.137.1/24", " 192.168.137.1", "192.168.137.1\n",
		"192.168.137.1;calc", "::ffff:192.168.137.1", "fd00::1", "192.168.137.*", "192.168.137",
	}
	for _, ip := range bad {
		if err := (ShareRequest{Op: ShareOpFreeAddr, FreeIP: ip}).Validate(); err == nil {
			t.Errorf("%q was accepted", ip)
		}
	}
	extra := []ShareRequest{
		{Op: ShareOpFreeAddr, FreeIP: "192.168.137.1", Index: 5},
		{Op: ShareOpFreeAddr, FreeIP: "192.168.137.1", User: "devpit-abcd"},
		{Op: ShareOpFreeAddr, FreeIP: "192.168.137.1", Rules: []string{"FPS-SMB-In-TCP"}},
		{Op: ShareOpFreeAddr, FreeIP: "192.168.137.1", HotspotConfig: "saved"},
		{Op: ShareOpCleanup, FreeIP: "192.168.137.1"},
		{Op: ShareOpStop, FreeIP: "192.168.137.1"},
		{Op: ShareOpHotspot, HotspotConfig: "saved", FreeIP: "192.168.137.1"},
	}
	for _, r := range extra {
		if err := r.Validate(); err == nil {
			t.Errorf("%+v was accepted", r)
		}
	}
	if s := (ShareRequest{Op: ShareOpFreeAddr, FreeIP: "192.168.137.1"}).String(); !strings.Contains(s, "192.168.137.1") {
		t.Errorf("String() = %q", s)
	}
}

func TestFreeAddrTouchesOnlyWifiDirectVirtualAdaptersAndRecordsNothing(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.freeOut = []string{"FREED=1", "FREEKEPT=1"}
	s := newSession(f)
	lines, err := run(t, s, ShareRequest{Op: ShareOpFreeAddr, FreeIP: "192.168.137.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(lines, "FREED=1") || !contains(lines, "FREEKEPT=1") {
		t.Errorf("lines = %v", lines)
	}
	cmds := f.commands()
	if len(cmds) != 1 || len(marked(cmds, "free-addr")) != 1 {
		t.Fatalf("ran %d commands, want the free-addr script once:\n%s", len(cmds), strings.Join(cmds, "\n"))
	}
	script := cmds[0]
	if got := psValue(t, script, "ip"); got != "192.168.137.1" {
		t.Errorf("the script frees %q", got)
	}
	if strings.Contains(script, "'192.168.137.1'") {
		t.Error("the address is quoted into the script; it must travel as a psString")
	}
	rule := "[int]$_.InterfaceType -eq 71 -and -not $_.HardwareInterface -and -not $_.ConnectorPresent"
	if !strings.Contains(script, rule) {
		t.Errorf("the script does not pick the adapters by the Wi-Fi Direct rule:\n%s", script)
	}
	if strings.Count(script, "Remove-NetIPAddress") != 1 || !strings.Contains(script, "if($wd -contains [int]$a.InterfaceIndex){\n    $a | Remove-NetIPAddress") {
		t.Errorf("the removal is not limited to the Wi-Fi Direct adapters:\n%s", script)
	}
	for _, never := range []string{"Set-NetIPAddress", "New-NetIPAddress", "Disable-NetAdapter", "SharedAccess", "-InterfaceAlias"} {
		if strings.Contains(script, never) {
			t.Errorf("the script holds %q", never)
		}
	}
	if !s.state.empty() {
		t.Errorf("freeaddr recorded something to undo: %+v", s.state)
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpStop}); err != nil || len(f.commands()) != 1 {
		t.Errorf("stop after freeaddr ran commands or failed: %v\n%s", err, strings.Join(f.commands(), "\n"))
	}
}

func TestFreeAddrLeavesARunningHotspotOfThisPCAlone(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.freeOut = []string{"FREEERR=hotspoton"}
	s := newSession(f)
	lines, err := run(t, s, ShareRequest{Op: ShareOpFreeAddr, FreeIP: "192.168.137.1"})
	if err == nil || !contains(lines, "FREEERR=hotspoton") {
		t.Fatalf("err = %v, lines = %v", err, lines)
	}
	if !strings.Contains(err.Error(), "Mobile Hotspot is on") {
		t.Errorf("err = %v", err)
	}
}

func TestFreeAddrNeedsItsCounts(t *testing.T) {
	shareEnv(t)
	for _, out := range [][]string{nil, {"FREED=1"}, {"FREED=x", "FREEKEPT=0"}, {"FREED=-1", "FREEKEPT=0"}} {
		f := newShareFake()
		f.freeOut = out
		s := newSession(f)
		lines, err := run(t, s, ShareRequest{Op: ShareOpFreeAddr, FreeIP: "192.168.137.1"})
		if err == nil {
			t.Errorf("%v: no error", out)
		}
		if lineWith(lines, "FREED=") {
			t.Errorf("%v: an unchecked count was passed on: %v", out, lines)
		}
	}
	f := newShareFake()
	f.failMatch = "# devpit free-addr"
	if _, err := run(t, newSession(f), ShareRequest{Op: ShareOpFreeAddr, FreeIP: "192.168.137.1"}); err == nil {
		t.Error("a failed script was not an error")
	}
}

func TestFreeAddrIsRefusedWhileThisSessionRunsAHotspot(t *testing.T) {
	shareEnv(t)
	f := newShareFake()
	f.freeOut = []string{"FREED=1", "FREEKEPT=0"}
	s := newSession(f)
	if _, err := run(t, s, savedHotspot); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, s, ShareRequest{Op: ShareOpFreeAddr, FreeIP: "192.168.137.1"}); err == nil {
		t.Error("freeaddr ran beside this session's own hotspot")
	}
	if len(marked(f.commands(), "free-addr")) != 0 {
		t.Error("the free-addr script ran")
	}
}
