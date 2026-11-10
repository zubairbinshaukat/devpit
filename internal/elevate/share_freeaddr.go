package elevate

// The freeaddr operation ([ShareOpFreeAddr]) is for the receiving PC. A PC
// that has ever run its own Mobile Hotspot can keep the hotspot address
// (192.168.137.1 unless Internet Connection Sharing was set otherwise) as a
// manual address on one of its Wi-Fi Direct virtual adapters. When it then
// joins another PC's hotspot, whose address is the same, every connection to
// that address stays on this PC: it lists its own shares and the sign-in
// fails. freeaddr takes that one address off the Wi-Fi Direct virtual
// adapters, and off nothing else.
//
// # Request
//
// FreeIP is the address: plain IPv4 text in a private range, in its
// canonical form (no leading zeros), matched by [freeIPPattern] and checked
// again with net/netip. Every other field is empty.
//
// # Which adapters
//
// Only an adapter that is interface type 71 (IEEE 802.11) and is neither a
// hardware interface nor has a connector, as Get-NetAdapter -IncludeHidden
// reports in InterfaceType, HardwareInterface and ConnectorPresent. That is
// the Wi-Fi Direct virtual adapter; the physical Wi-Fi card has both flags
// set and is never touched, nor is any Ethernet, VPN or virtual switch. The
// same rule is winapi.IsWifiDirectVirtual on the TUI side. It reads numbers
// and flags, never a name, so it holds on every Windows language.
//
// The operation refuses while this PC's own Mobile Hotspot is on or
// changing (TetheringOperationalState 1 or 3): the address is then in use
// for real. When that state cannot be read (no connection profile, or WinRT
// refuses), the removal goes ahead, since it is limited to the virtual
// adapters anyway.
//
// # Lines
//
//	FREEERR=hotspoton  this PC's own hotspot is on; nothing was changed
//	FREED=<n>          how many addresses were removed
//	FREEKEPT=<n>       how many holders of the address were left alone,
//	                   because they are not Wi-Fi Direct virtual adapters
//
// # Undo
//
// None, and nothing is recorded for teardown or in a manifest. The address
// is Windows' own leftover: Internet Connection Sharing puts it back by
// itself the next time this PC runs its own Mobile Hotspot.

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// freeIPPattern is the only address shape freeaddr accepts: dotted IPv4 that
// starts like a private range. net/netip then checks the range exactly.
var freeIPPattern = regexp.MustCompile(`^(10|172|192)\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$`)

// The machine lines of the freeaddr operation.
const (
	FreeLineError = "FREEERR"
	FreeLineFreed = "FREED"
	FreeLineKept  = "FREEKEPT"
)

// validateFreeAddr checks a freeaddr request.
func (r ShareRequest) validateFreeAddr() error {
	if !freeIPPattern.MatchString(r.FreeIP) {
		return fmt.Errorf("refused: %q is not a private IPv4 address", r.FreeIP)
	}
	a, err := netip.ParseAddr(r.FreeIP)
	if err != nil || !a.Is4() || !a.IsPrivate() || a.String() != r.FreeIP {
		return fmt.Errorf("refused: %q is not a private IPv4 address", r.FreeIP)
	}
	rest := r
	rest.Op, rest.FreeIP = "", ""
	if !reflect.ValueOf(rest).IsZero() {
		return errors.New("refused: freeaddr carries the address only")
	}
	return nil
}

// scriptFreeAddr removes the address from the Wi-Fi Direct virtual adapters.
// The %s is the address, a [psString]. The hotspot check runs in its own
// try block, so a WinRT failure only means "cannot tell".
const scriptFreeAddr = "# devpit free-addr\n" + `$ErrorActionPreference='Stop'
$ip=%s
$hs='na'
try {
  Add-Type -AssemblyName System.Runtime.WindowsRuntime
  $TM=[Windows.Networking.NetworkOperators.NetworkOperatorTetheringManager,Windows.Networking.NetworkOperators,ContentType=WindowsRuntime]
  $NI=[Windows.Networking.Connectivity.NetworkInformation,Windows.Networking.Connectivity,ContentType=WindowsRuntime]
  $p=$NI::GetInternetConnectionProfile()
  if($null -eq $p){ $p=@($NI::GetConnectionProfiles())[0] }
  if($null -ne $p){ $hs=[string][int]$TM::CreateFromConnectionProfile($p).TetheringOperationalState }
} catch { $hs='na' }
if($hs -eq '1' -or $hs -eq '3'){ Write-Output 'FREEERR=hotspoton'; exit 0 }
$wd=@(Get-NetAdapter -IncludeHidden -ErrorAction SilentlyContinue | Where-Object { [int]$_.InterfaceType -eq 71 -and -not $_.HardwareInterface -and -not $_.ConnectorPresent } | ForEach-Object { [int]$_.ifIndex })
$n=0
$kept=0
foreach($a in @(Get-NetIPAddress -AddressFamily IPv4 -IPAddress $ip -ErrorAction SilentlyContinue)){
  if($a.IPAddress -ne $ip){ continue }
  if($wd -contains [int]$a.InterfaceIndex){
    $a | Remove-NetIPAddress -Confirm:$false
    $n++
  } else { $kept++ }
}
Write-Output ('FREED=' + $n)
Write-Output ('FREEKEPT=' + $kept)`

// freeTokens reads the machine lines of the freeaddr script.
func freeTokens(out []string) map[string]string {
	t := map[string]string{}
	for _, l := range out {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if ok && strings.HasPrefix(k, "FREE") {
			t[k] = v
		}
	}
	return t
}

// opFreeAddr runs the freeaddr script and passes on its checked counts. It
// records nothing: there is nothing to undo.
func (s *shareSession) opFreeAddr(ctx context.Context, req ShareRequest, emit onLine) error {
	if h := &s.state.hotspot; !h.empty() || h.wasOn {
		return errors.New("refused: this session runs a hotspot; an address it uses is not freed")
	}
	emit("info", "Taking the address off this PC's Wi-Fi Direct adapters")
	out, err := s.ps(ctx, fmt.Sprintf(scriptFreeAddr, psString(req.FreeIP)))
	t := freeTokens(out)
	if err != nil {
		return fmt.Errorf("freeing the address: %w", err)
	}
	if t[FreeLineError] == "hotspoton" {
		emit("info", FreeLineError+"=hotspoton")
		return errors.New("this PC's own Mobile Hotspot is on, so its address was left alone; turn it off and try again")
	}
	count := func(k string) (int, bool) {
		n, err := strconv.Atoi(t[k])
		return n, err == nil && n >= 0 && n <= 1000
	}
	freed, ok1 := count(FreeLineFreed)
	kept, ok2 := count(FreeLineKept)
	if !ok1 || !ok2 {
		return errors.New("freeing the address: the result was not reported")
	}
	emit("info", FreeLineFreed+"="+strconv.Itoa(freed))
	emit("info", FreeLineKept+"="+strconv.Itoa(kept))
	return nil
}
