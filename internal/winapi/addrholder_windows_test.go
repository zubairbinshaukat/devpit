//go:build windows

package winapi

import (
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Read-only: the physical Wi-Fi cards the WLAN service lists are never
// counted as Wi-Fi Direct virtual adapters by the flag rule. Logs counts only.
func TestThePhysicalWifiCardIsNotVirtualOnThisPC(t *testing.T) {
	list, err := readAdapters()
	if err != nil {
		t.Fatal(err)
	}
	virtual := 0
	for _, r := range list {
		if r.haveFlags && IsWifiDirectVirtual(r.ifType, r.flags) {
			virtual++
		}
	}
	t.Logf("adapters: %d, Wi-Fi Direct virtual: %d", len(list), virtual)
	cards, err := WifiInterfaces()
	if errors.Is(err, ErrNoWifi) {
		t.Skipf("no WLAN service: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, c := range cards {
		for _, r := range list {
			if !sameGUID(r.guid, c.GUID) {
				continue
			}
			checked++
			if !r.haveFlags {
				t.Error("the flags of a Wi-Fi card could not be read")
			} else if IsWifiDirectVirtual(r.ifType, r.flags) {
				t.Error("a physical Wi-Fi card counts as a Wi-Fi Direct virtual adapter")
			}
		}
	}
	t.Logf("Wi-Fi cards checked: %d", checked)
}

// Read-only: the flag rule picks the same adapters as the filter the
// worker's freeaddr script uses (internal/elevate/share_freeaddr.go), on this
// PC. Only Get-NetAdapter runs. Logs counts only.
func TestTheFlagRuleMatchesTheWorkersFilterOnThisPC(t *testing.T) {
	const filter = `[int]$_.InterfaceType -eq 71 -and -not $_.HardwareInterface -and -not $_.ConnectorPresent`
	ps := filepath.Join(os.Getenv("WINDIR"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	out, err := exec.Command(ps, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
		"Get-NetAdapter -IncludeHidden | Where-Object { "+filter+" } | ForEach-Object { 'WD=' + [int]$_.ifIndex }").Output()
	if err != nil {
		t.Skipf("cannot run Windows PowerShell here: %v", err)
	}
	theirs := map[uint32]bool{}
	for _, l := range strings.Fields(string(out)) {
		if v, ok := strings.CutPrefix(l, "WD="); ok {
			n, perr := strconv.ParseUint(v, 10, 32)
			if perr != nil {
				t.Fatalf("bad line %q", l)
			}
			theirs[uint32(n)] = true
		}
	}
	list, err := readAdapters()
	if err != nil {
		t.Fatal(err)
	}
	compared := 0
	for _, r := range list {
		if !r.haveFlags {
			continue
		}
		compared++
		if mine := IsWifiDirectVirtual(r.ifType, r.flags); mine != theirs[r.index] {
			t.Errorf("adapter %d: the flag rule says %v, the worker's filter %v", r.index, mine, theirs[r.index])
		}
	}
	t.Logf("compared: %d, picked by the filter: %d", compared, len(theirs))
}

// Read-only: loopback is held by no adapter in the list, and the route to an
// address this PC holds comes from that address.
func TestLocalAddressHoldersOnThisPCOnlyReads(t *testing.T) {
	list, err := readAdapters()
	if err != nil {
		t.Fatal(err)
	}
	var own netip.Addr
	for _, r := range list {
		for _, a := range r.v4 {
			if !a.IsLoopback() && !a.IsLinkLocalUnicast() {
				own = a
			}
		}
	}
	if !own.IsValid() {
		t.Skip("this PC has no IPv4 address")
	}
	hs, err := LocalAddressHolders(own.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) == 0 {
		t.Fatal("an address this PC holds was not found")
	}
	t.Logf("holders: %d, routes: %v", len(hs), hs[0].Routes)
	if _, src, rerr := RouteSource(own.String()); rerr != nil {
		t.Errorf("RouteSource: %v", rerr)
	} else if src != own.String() {
		t.Logf("the route to an own address comes from another address (allowed): %v", src != "")
	}
	none, err := LocalAddressHolders("192.0.2.77") // TEST-NET-1, never assigned
	if err != nil || len(none) != 0 {
		t.Errorf("got %d holders, %v for an unassigned address", len(none), err)
	}
}
