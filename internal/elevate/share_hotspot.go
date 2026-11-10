package elevate

// The hotspot operation ([ShareOpHotspot]) starts the Windows Mobile Hotspot
// for one share and opens TCP 445 to the hotspot's own subnet only. It is the
// contract the host engine (internal/share/host) builds on.
//
// # Request
//
// A hotspot request carries:
//
//   - HotspotConfig: "saved" starts the hotspot with the name and password
//     already saved in Windows (StartTetheringAsync with no configuration);
//     "temp" starts it with a temporary name and password.
//   - SetBand5 (saved only): switch the saved hotspot to 5 GHz first, keeping
//     its name and password. Not recorded and never put back (decision 18).
//   - SSID, Passphrase, Band (temp only): SSID matches ^Devpit-[a-z0-9]{4,12}$,
//     Passphrase ^[0-9]{8}$, Band is "5", "6" or "auto". Saved mode carries
//     none of them: the user's own password never reaches the worker.
//
// A cleanup request (from the manifest) may carry HotspotConfig, HotspotRule
// (^devpit-hs-[a-z0-9]{4,12}$), HotspotStarted, HotspotTimeoutWasOn,
// HotspotTimeoutChanged, HotspotSSID (1-32 bytes, no control characters; in
// temp mode it must also match the SSID pattern) and HotspotLegacy. It never
// carries SSID, Passphrase, Band or SetBand5.
//
// # Lines
//
// The worker sends progress text plus these machine lines, each checked
// before it is passed on (the scripts' own output is never forwarded as is,
// and no line ever holds a password):
//
//	HSERR=noprofile   Windows has no connection profile to share; nothing changed
//	HSCAP=<n>         TetheringCapability; anything but 0 stops here (see [TetheringCapability])
//	HSWASON=1         the hotspot was already on: nothing was changed, it is left on
//	HSTIMEOUT=1|0|na  the "turn off when nothing is connected" setting before the
//	                  start; 1 means the worker turned it off and will put it back
//	HSMODE=saved|session|legacy  how the hotspot was started; legacy (Windows
//	                  before 26100, temp only) means the saved settings were
//	                  overwritten and are held in worker memory to restore
//	HSBANDSET=1|0     saved mode with SetBand5: the band was switched to 5 GHz, or
//	                  the card cannot do 5 GHz and nothing was switched
//	HSBAND=auto|2.4|5|6  the band the hotspot was started on
//	HSSSID=<base64>   UTF-8 name of the hotspot, sent before the start
//	HSSTATUS=<n>      TetheringOperationStatus of the start (see [TetheringStatus])
//	HSIP=<ipv4>       the address the hotspot holds on this PC
//	HSRULE=<name>     the firewall rule about to be made (record it before it exists)
//	HSFW=<name>       the rule was made
//	HS=on             the hotspot is up and the rule is in place
//
// The operation fails (with an error) on HSERR, a non-zero HSCAP, a non-zero
// HSSTATUS, an address that is not a private IPv4, or a failed rule. It
// succeeds with nothing changed after HSWASON=1. In every case whatever it
// did change stays recorded and stop undoes it.
//
// # Undo
//
// Teardown runs these newest first, before the "other Devpit shares" gate,
// each kept recorded until it succeeds: remove the rule (exact name and
// Devpit's description) → stop the hotspot, only when Devpit started it, it
// was not already on and the running name is still the recorded one → in
// legacy mode, put the saved settings back → turn the timeout back on when
// Devpit turned it off. The band switch is not reverted.
//
// Windows offers no way to read the name of a hotspot started with a
// per-session configuration (session mode): the saved settings stay the
// user's. A live worker stops a session-mode hotspot only when it ran the
// start itself and the stop finds the hotspot plainly on (a state in
// transition is waited out; Off or unknown leaves it alone). The stop's own
// read is the only state read after the start, so a person who turns
// Devpit's hotspot off and their own on while the share is up cannot be
// told apart; that window is the life of one share. A cleanup from a
// manifest cannot prove the running hotspot is Devpit's, so it leaves it on
// and turns the timeout back on, which ends the hotspot five minutes after
// the last device leaves. A manifest written before the PC last started is
// cleaned up with no stop at all (host.Manifest's request): a restart turns
// the hotspot off, so whatever runs now is not Devpit's.
//
// # Secrets
//
// The scripts that handle a password (hs-start, hs-restore) are constants
// and read their values from standard input ([InputExecutor]), because
// Windows writes the text of any script block that uses Add-Type to the
// PowerShell event log (event 4104) even with no logging policy. No password
// is ever in script text, on a command line or in a line sent on.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The two hotspot configurations a request may name.
const (
	hotspotSaved = "saved"
	hotspotTemp  = "temp"
)

// hsRuleDescription marks the hotspot firewall rule as Devpit's. A rule is
// removed only when its name and this description both match.
const hsRuleDescription = "devpit-temp-hotspot-rule"

// The machine lines of a hotspot operation; see the comment at the top of
// this file.
const (
	HotspotLineError      = "HSERR"
	HotspotLineCapability = "HSCAP"
	HotspotLineWasOn      = "HSWASON"
	HotspotLineTimeout    = "HSTIMEOUT"
	HotspotLineMode       = "HSMODE"
	HotspotLineBandSet    = "HSBANDSET"
	HotspotLineBand       = "HSBAND"
	HotspotLineSSID       = "HSSSID"
	HotspotLineStatus     = "HSSTATUS"
	HotspotLineIP         = "HSIP"
	HotspotLineRule       = "HSRULE"
	HotspotLineFirewall   = "HSFW"
	HotspotLineOn         = "HS"
)

// The script-only tokens, read by the worker and never passed on.
const (
	hsState      = "HSSTATE"
	hsOldSSID    = "HSOLDSSID"
	hsOldPass    = "HSOLDPASS"
	hsOldBand    = "HSOLDBAND"
	hsStop       = "HSSTOP"
	hsStopStatus = "HSSTOPSTATUS"
)

// ParseHotspotLine splits a machine line of the hotspot operation into its
// name and value. ok is false for a progress line.
func ParseHotspotLine(line string) (name, value string, ok bool) {
	name, value, ok = strings.Cut(strings.TrimSpace(line), "=")
	if !ok || !strings.HasPrefix(name, "HS") || strings.ToUpper(name) != name {
		return "", "", false
	}
	return name, value, true
}

// TetheringStatus is Windows' TetheringOperationStatus, the result of a
// start or a stop.
type TetheringStatus int

// The values of TetheringOperationStatus.
const (
	TetheringSuccess TetheringStatus = iota
	TetheringUnknown
	TetheringMobileBroadbandDeviceOff
	TetheringWiFiDeviceOff
	TetheringEntitlementCheckTimeout
	TetheringEntitlementCheckFailure
	TetheringOperationInProgress
	TetheringBluetoothDeviceOff
	TetheringNetworkLimitedConnectivity
	TetheringAlreadyOn
	TetheringRadioRestriction
	TetheringBandInterference
)

// TetheringStatuses lists every known status, in order.
func TetheringStatuses() []TetheringStatus {
	out := make([]TetheringStatus, 0, int(TetheringBandInterference)+1)
	for s := TetheringSuccess; s <= TetheringBandInterference; s++ {
		out = append(out, s)
	}
	return out
}

// Key is a stable name for the status, for the host to map to its own words.
// A value Windows may add later is "unknown".
func (s TetheringStatus) Key() string {
	k, _ := s.describe()
	return k
}

// Message is what Devpit tells the user for the status (plan 8.2), and ""
// for success.
func (s TetheringStatus) Message() string {
	_, m := s.describe()
	return m
}

// describe holds the 8.2 table.
func (s TetheringStatus) describe() (key, message string) {
	const instead = " Sharing over your network instead."
	switch s {
	case TetheringSuccess:
		return "success", ""
	case TetheringMobileBroadbandDeviceOff:
		return "mobile-broadband-off", "The mobile data device is off." + instead
	case TetheringWiFiDeviceOff:
		return "wifi-off", "Wi-Fi is turned off, so a hotspot cannot start." + instead
	case TetheringEntitlementCheckTimeout:
		return "entitlement-timeout", "Your mobile plan did not answer about hotspot use." + instead
	case TetheringEntitlementCheckFailure:
		return "entitlement-failure", "Your mobile plan does not allow a hotspot." + instead
	case TetheringOperationInProgress:
		return "in-progress", "Windows is already changing the hotspot." + instead
	case TetheringBluetoothDeviceOff:
		return "bluetooth-off", "Bluetooth is off and this hotspot needs it." + instead
	case TetheringNetworkLimitedConnectivity:
		return "limited-connectivity", "This PC's connection is limited, so a hotspot cannot start." + instead
	case TetheringAlreadyOn:
		return "already-on", "Your hotspot is already on. Devpit left it alone and is sharing over your network."
	case TetheringRadioRestriction:
		return "radio-restriction", "This Wi-Fi card cannot run a hotspot on that band here." + instead
	case TetheringBandInterference:
		return "band-interference", "That band is busy or not allowed right now." + instead
	default: // TetheringUnknown and anything newer
		return "unknown", "Windows could not start the hotspot." + instead
	}
}

// TetheringCapability is Windows' TetheringCapability for the internet
// connection. Only [TetheringEnabled] allows hotspot mode; the others are
// never offered and show no message (plan 8.3).
type TetheringCapability int

// The values of TetheringCapability.
const (
	TetheringEnabled TetheringCapability = iota
	TetheringDisabledByGroupPolicy
	TetheringDisabledByHardwareLimitation
	TetheringDisabledByOperator
	TetheringDisabledBySku
	TetheringDisabledByRequiredAppNotInstalled
	TetheringDisabledDueToUnknownCause
	TetheringDisabledBySystemCapability
)

// Key is a stable name for the capability. A value Windows may add later is
// "disabled-unknown".
func (c TetheringCapability) Key() string {
	switch c {
	case TetheringEnabled:
		return "enabled"
	case TetheringDisabledByGroupPolicy:
		return "disabled-group-policy"
	case TetheringDisabledByHardwareLimitation:
		return "disabled-hardware"
	case TetheringDisabledByOperator:
		return "disabled-operator"
	case TetheringDisabledBySku:
		return "disabled-sku"
	case TetheringDisabledByRequiredAppNotInstalled:
		return "disabled-app-missing"
	case TetheringDisabledBySystemCapability:
		return "disabled-system-capability"
	default:
		return "disabled-unknown"
	}
}

// hsStopCheck is how teardown decides the running hotspot is Devpit's.
type hsStopCheck int

const (
	// hsCheckName stops it only when the saved hotspot name is the
	// recorded one (saved mode, legacy mode). The zero value, so a record
	// with no mode never stops anything it cannot match.
	hsCheckName hsStopCheck = iota
	// hsCheckSession stops it when it is on: this worker started it with a
	// per-session configuration, whose name Windows does not report.
	hsCheckSession
	// hsCheckSkip leaves it on: a cleanup of a per-session start cannot
	// tell whose hotspot is running.
	hsCheckSkip
)

// savedAccessPoint is the user's own hotspot settings, saved before the
// legacy path overwrote them. It lives in worker memory only.
type savedAccessPoint struct {
	ssid, passphrase, band string
}

// hotspotState is the hotspot part of [shareState].
type hotspotState struct {
	started        bool // Devpit started it (recorded before the start runs)
	wasOn          bool // it was on before Devpit: never touched
	timeoutWasOn   bool
	timeoutChanged bool // Devpit turned the timeout off (recorded before)
	rule           string
	ruleOn         bool // the rule was confirmed made (never seeded)
	ssid           string
	check          hsStopCheck
	startedHere    bool // this worker ran the start itself (never seeded)
	legacy         *savedAccessPoint
}

// empty reports whether there is nothing of the hotspot's to undo.
func (h *hotspotState) empty() bool {
	return !h.started && h.rule == "" && !h.timeoutChanged && h.legacy == nil
}

// seedHotspot loads a manifest's hotspot record.
func seedHotspot(r ShareRequest) hotspotState {
	h := hotspotState{
		started: r.HotspotStarted, rule: r.HotspotRule, ssid: r.HotspotSSID,
		timeoutWasOn: r.HotspotTimeoutWasOn, timeoutChanged: r.HotspotTimeoutChanged,
	}
	if r.HotspotConfig == hotspotTemp && !r.HotspotLegacy {
		h.check = hsCheckSkip
	}
	return h
}

// validateHotspot checks a hotspot start request.
func (r ShareRequest) validateHotspot() error {
	if r.HotspotRule != "" || r.HotspotStarted || r.HotspotTimeoutWasOn || r.HotspotTimeoutChanged ||
		r.HotspotSSID != "" || r.HotspotLegacy {
		return errors.New("refused: a hotspot start carries no cleanup fields")
	}
	switch r.HotspotConfig {
	case hotspotSaved:
		if r.SSID != "" || r.Passphrase != "" || r.Band != "" {
			return errors.New("refused: the saved hotspot is started without a name, password or band")
		}
	case hotspotTemp:
		if r.SetBand5 {
			return errors.New("refused: the band switch is for the saved hotspot only")
		}
		if !ssidPattern.MatchString(r.SSID) {
			return fmt.Errorf("refused: %q is not a Devpit hotspot name", r.SSID)
		}
		if !passphrasePattern.MatchString(r.Passphrase) {
			return errors.New("refused: the hotspot password is not in the allowed form (8 digits)")
		}
		if !hotspotBands[r.Band] {
			return fmt.Errorf("refused: %q is not a hotspot band (5, 6 or auto)", r.Band)
		}
	default:
		return fmt.Errorf("refused: %q is not a hotspot configuration (saved or temp)", r.HotspotConfig)
	}
	return nil
}

// validateHotspotCleanup checks the hotspot fields of a cleanup request,
// which came from a manifest on disk.
func (r ShareRequest) validateHotspotCleanup() error {
	if r.SSID != "" || r.Passphrase != "" || r.Band != "" || r.SetBand5 {
		return errors.New("refused: a cleanup never carries a hotspot name, password or band to set")
	}
	switch r.HotspotConfig {
	case "", hotspotSaved, hotspotTemp:
	default:
		return fmt.Errorf("refused: %q is not a hotspot configuration (saved or temp)", r.HotspotConfig)
	}
	if r.HotspotRule != "" && !hsRulePattern.MatchString(r.HotspotRule) {
		return fmt.Errorf("refused: %q is not Devpit's hotspot firewall rule", r.HotspotRule)
	}
	if r.HotspotSSID != "" {
		if err := checkHotspotName(r.HotspotSSID); err != nil {
			return err
		}
		if r.HotspotConfig == hotspotTemp && !ssidPattern.MatchString(r.HotspotSSID) {
			return fmt.Errorf("refused: %q is not a Devpit hotspot name", r.HotspotSSID)
		}
	}
	if r.HotspotStarted && r.HotspotConfig == "" {
		return errors.New("refused: a started hotspot needs its configuration")
	}
	if r.HotspotTimeoutChanged && !r.HotspotTimeoutWasOn {
		return errors.New("refused: the hotspot timeout record is not consistent")
	}
	if r.HotspotLegacy && r.HotspotConfig != hotspotTemp {
		return errors.New("refused: only a temporary hotspot overwrites the saved settings")
	}
	return nil
}

// checkHotspotName refuses a hotspot name that is empty, longer than the 32
// bytes Wi-Fi allows, not UTF-8, or holds a control character. The name only
// ever reaches a script through [psString].
func checkHotspotName(s string) error {
	if s == "" || len(s) > 32 || !utf8.ValidString(s) {
		return errors.New("refused: the hotspot name is not 1 to 32 bytes of text")
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return errors.New("refused: the hotspot name holds a control character")
		}
	}
	return nil
}

// The scripts. Each starts with a marker comment (tests find them by it) and
// runs in Windows PowerShell 5.1, the only PowerShell with the WinRT
// projection, through [psArgv]. Every value is a [psString] or, in the
// scripts that handle a password, read from standard input ([hsReadInput]);
// no value is quoted into a script. The WinRT async calls are awaited through the AsTask
// extension methods, with a bound.

// hsHelpers loads WinRT and defines the await helpers.
const hsHelpers = `$ErrorActionPreference='Stop'
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$ext=[System.WindowsRuntimeSystemExtensions].GetMethods()
$asTaskOp=@($ext | Where-Object { $_.Name -eq 'AsTask' -and $_.IsGenericMethod -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -like 'IAsyncOperation?1' })[0]
$asTaskAct=@($ext | Where-Object { $_.Name -eq 'AsTask' -and -not $_.IsGenericMethod -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncAction' })[0]
function Await($op, [Type]$type, [int]$ms) {
  $task=$asTaskOp.MakeGenericMethod($type).Invoke($null, @($op))
  if(-not $task.Wait($ms)){ throw 'Windows did not answer in time' }
  $task.Result
}
function AwaitAction($action, [int]$ms) {
  $task=$asTaskAct.Invoke($null, @($action))
  if(-not $task.Wait($ms)){ throw 'Windows did not answer in time' }
}
function B64($s) { [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([string]$s)) }
$TM=[Windows.Networking.NetworkOperators.NetworkOperatorTetheringManager,Windows.Networking.NetworkOperators,ContentType=WindowsRuntime]
$Res=[Windows.Networking.NetworkOperators.NetworkOperatorTetheringOperationResult,Windows.Networking.NetworkOperators,ContentType=WindowsRuntime]
$NI=[Windows.Networking.Connectivity.NetworkInformation,Windows.Networking.Connectivity,ContentType=WindowsRuntime]
`

// hsProfileInternet takes the internet connection profile, which a start
// needs.
const hsProfileInternet = `$p=$NI::GetInternetConnectionProfile()
if($null -eq $p){ Write-Output 'HSERR=noprofile'; exit 0 }
`

// hsProfileAny is the same for an undo, which takes any profile when the
// internet one has gone.
const hsProfileAny = `$p=$NI::GetInternetConnectionProfile()
if($null -eq $p){ $p=@($NI::GetConnectionProfiles())[0] }
if($null -eq $p){ Write-Output 'HSERR=noprofile'; exit 0 }
`

// scriptHotspotProbe reads the capability, the state and the timeout. It
// changes nothing.
const scriptHotspotProbe = "# devpit hs-probe\n" + hsHelpers + hsProfileInternet + `$cap=[int]$TM::GetTetheringCapabilityFromConnectionProfile($p)
Write-Output ('HSCAP=' + $cap)
if($cap -ne 0){ exit 0 }
$m=$TM::CreateFromConnectionProfile($p)
$st=[int]$m.TetheringOperationalState
if($st -eq 1 -or $st -eq 3){ Write-Output 'HSWASON=1'; exit 0 }
if($st -ne 2){ Write-Output ('HSSTATE=' + $st); exit 0 }
$t='na'
try { if($TM::IsNoConnectionsTimeoutEnabled()){ $t='1' } else { $t='0' } } catch { $t='na' }
Write-Output ('HSTIMEOUT=' + $t)`

// scriptHotspotTimeoutOff and scriptHotspotTimeoutOn switch the "turn off
// when nothing is connected" setting. IsNoConnectionsTimeoutEnabled and the
// Enable/Disable…Async pair are static members of
// NetworkOperatorTetheringManager (the setting is the PC's, not a
// connection's), so they are called on the type: PowerShell does not reach a
// static method through an instance. The Async forms are awaited with a
// bound, like every other WinRT call here.
const (
	scriptHotspotTimeoutOff = "# devpit hs-timeout-off\n" + hsHelpers + `AwaitAction ($TM::DisableNoConnectionsTimeoutAsync()) 30000
Write-Output 'HSTIMEOUT=0'`
	scriptHotspotTimeoutOn = "# devpit hs-timeout-on\n" + hsHelpers + `AwaitAction ($TM::EnableNoConnectionsTimeoutAsync()) 30000
Write-Output 'HSTIMEOUT=1'`
)

// hsReadInput defines ReadIn, which reads the next value from standard
// input: one line of base64 of UTF-8 per value, written by [hsInput]. The
// scripts that handle a password take every value this way, so their text is
// a constant that holds no secret (see [InputExecutor]).
const hsReadInput = `function ReadIn {
  $l=[Console]::In.ReadLine()
  if($null -eq $l){ throw 'a value is missing' }
  [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($l))
}
`

// hsInput is the standard input of a script that reads its values with
// ReadIn, in the order it reads them.
func hsInput(values ...string) string {
	var b strings.Builder
	for _, v := range values {
		b.WriteString(base64.StdEncoding.EncodeToString([]byte(v)))
		b.WriteString("\n")
	}
	return b.String()
}

// scriptHotspotStart starts the hotspot and finds its address. It is a
// constant: the configuration, the name, the password, the band and "1" or
// "0" for the band switch arrive on standard input, in that order
// ([hsInput]). HSSSID is printed before the start so the name is recorded
// before the hotspot can exist; in legacy mode the saved settings are printed
// before they are overwritten, for the worker's memory only.
//
// The address is the ICS scope address (from the registry, 192.168.137.1
// unless changed) as soon as an adapter that is up holds it; it may already
// be there before the start, which is fine once Windows has said the hotspot
// is on. Failing that, the one address on an up, virtual 802.11 adapter
// (interface type 71: the Wi-Fi Direct adapter the hotspot runs on). An
// address that is merely new, such as a VPN's, is never taken. Nothing is
// read from translated text.
const scriptHotspotStart = "# devpit hs-start\n" + hsHelpers + hsReadInput + hsProfileInternet + `$kind=ReadIn
$ssid=ReadIn
$pass=ReadIn
$want=ReadIn
$set5=ReadIn
$m=$TM::CreateFromConnectionProfile($p)
$st=[int]$m.TetheringOperationalState
if($st -eq 1 -or $st -eq 3){ Write-Output 'HSWASON=1'; exit 0 }
if($st -ne 2){ Write-Output ('HSSTATE=' + $st); exit 0 }
$BandT=$null
try { $BandT=[Windows.Networking.NetworkOperators.TetheringWiFiBand,Windows.Networking.NetworkOperators,ContentType=WindowsRuntime] } catch { $BandT=$null }
$names=@('auto','2.4','5','6')
function Start-Hotspot($cfg) {
  $r=$null
  for($i=0; $i -lt 2; $i++){
    if($null -eq $cfg){ $r=Await ($m.StartTetheringAsync()) $Res 90000 } else { $r=Await ($m.StartTetheringAsync($cfg)) $Res 90000 }
    if([int]$r.Status -ne 6){ break }
    Start-Sleep -Seconds 3
  }
  [int]$r.Status
}
$band='auto'
if($kind -eq 'saved'){
  Write-Output 'HSMODE=saved'
  $ap=$m.GetCurrentAccessPointConfiguration()
  if($set5 -eq '1'){
    if($null -ne $BandT -and $ap.IsBandSupported([Enum]::ToObject($BandT, 2))){
      $ap.Band=[Enum]::ToObject($BandT, 2)
      AwaitAction ($m.ConfigureAccessPointAsync($ap)) 60000
      Write-Output 'HSBANDSET=1'
      $ap=$m.GetCurrentAccessPointConfiguration()
    } else { Write-Output 'HSBANDSET=0' }
  }
  if($null -ne $BandT){ $band=$names[[int]$ap.Band] }
  Write-Output ('HSSSID=' + (B64 $ap.Ssid))
  $status=Start-Hotspot $null
} else {
  $ST=$null
  try { $ST=[Windows.Networking.NetworkOperators.NetworkOperatorTetheringSessionAccessPointConfiguration,Windows.Networking.NetworkOperators,ContentType=WindowsRuntime] } catch { $ST=$null }
  if($null -ne $ST){
    Write-Output 'HSMODE=session'
    $AK=[Windows.Networking.NetworkOperators.TetheringWiFiAuthenticationKind,Windows.Networking.NetworkOperators,ContentType=WindowsRuntime]
    $cfg=$ST::new()
    $cfg.Ssid=$ssid
    $cfg.Passphrase=$pass
    $b=2
    $k=0
    if($want -eq 'auto'){ $b=0 }
    if($want -eq '6' -and $cfg.IsBandSupported([Enum]::ToObject($BandT, 3)) -and $cfg.IsAuthenticationKindSupported([Enum]::ToObject($AK, 2))){ $b=3; $k=2 }
    $cfg.Band=[Enum]::ToObject($BandT, $b)
    $cfg.AuthenticationKind=[Enum]::ToObject($AK, $k)
    Write-Output ('HSSSID=' + (B64 $cfg.Ssid))
    $status=Start-Hotspot $cfg
    if($b -eq 3 -and ($status -eq 10 -or $status -eq 11)){
      $b=2
      $cfg.Band=[Enum]::ToObject($BandT, 2)
      $cfg.AuthenticationKind=[Enum]::ToObject($AK, 0)
      $status=Start-Hotspot $cfg
    }
    $band=$names[$b]
  } else {
    Write-Output 'HSMODE=legacy'
    $ap=$m.GetCurrentAccessPointConfiguration()
    $ob='na'
    if($null -ne $BandT){ $ob=[string][int]$ap.Band }
    Write-Output ('HSOLDSSID=' + (B64 $ap.Ssid))
    Write-Output ('HSOLDPASS=' + (B64 $ap.Passphrase))
    Write-Output ('HSOLDBAND=' + $ob)
    $ap.Ssid=$ssid
    $ap.Passphrase=$pass
    if($null -ne $BandT){
      $b=2
      if($want -eq 'auto'){ $b=0 }
      $ap.Band=[Enum]::ToObject($BandT, $b)
      $band=$names[$b]
    }
    Write-Output ('HSSSID=' + (B64 $ssid))
    AwaitAction ($m.ConfigureAccessPointAsync($ap)) 60000
    $status=Start-Hotspot $null
  }
}
Write-Output ('HSBAND=' + $band)
Write-Output ('HSSTATUS=' + $status)
if($status -ne 0){ exit 0 }
$scope='192.168.137.1'
try {
  $v=[string](Get-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Services\SharedAccess\Parameters' -Name ScopeAddress -ErrorAction Stop).ScopeAddress
  if($v -ne ''){ $scope=$v }
} catch { }
function Test-Up([int]$ix) { @(Get-NetAdapter -IncludeHidden -InterfaceIndex $ix -ErrorAction SilentlyContinue | Where-Object { [int]$_.InterfaceOperationalStatus -eq 1 }).Count -gt 0 }
$ip=''
for($i=0; $i -lt 40 -and $ip -eq ''; $i++){
  foreach($a in @(Get-NetIPAddress -AddressFamily IPv4 -IPAddress $scope -ErrorAction SilentlyContinue)){
    if($ip -eq '' -and (Test-Up $a.InterfaceIndex)){ $ip=$scope }
  }
  if($ip -eq ''){
    $wd=@(Get-NetAdapter -IncludeHidden -ErrorAction SilentlyContinue | Where-Object { [int]$_.InterfaceType -eq 71 -and $_.Virtual -and [int]$_.InterfaceOperationalStatus -eq 1 } | ForEach-Object { [int]$_.ifIndex })
    $c=@(Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $wd -contains [int]$_.InterfaceIndex -and $_.IPAddress -notlike '169.254.*' } | ForEach-Object { $_.IPAddress })
    if($c.Count -eq 1){ $ip=$c[0] } else { Start-Sleep -Milliseconds 500 }
  }
}
Write-Output ('HSIP=' + $ip)`

// scriptHotspotStop stops the hotspot. The two %s are the recorded name and
// the check, "name" or "session", both [psString]s. A state in transition is
// waited out briefly. With "name" a hotspot whose saved name is not the
// recorded one is left on (HSSTOP=notours). With "session" it is stopped only
// when Windows says plainly that it is on; any other state is left alone
// (HSSTOP=unsure).
const scriptHotspotStop = "# devpit hs-stop\n" + hsHelpers + hsProfileAny + `$exp=%s
$check=%s
$m=$TM::CreateFromConnectionProfile($p)
$st=[int]$m.TetheringOperationalState
for($i=0; $i -lt 20 -and $st -eq 3; $i++){ Start-Sleep -Milliseconds 500; $st=[int]$m.TetheringOperationalState }
if($st -eq 2){ Write-Output 'HSSTOP=off'; exit 0 }
if($check -eq 'session'){
  if($st -ne 1){ Write-Output 'HSSTOP=unsure'; exit 0 }
} else {
  if([string]$m.GetCurrentAccessPointConfiguration().Ssid -cne $exp){ Write-Output 'HSSTOP=notours'; exit 0 }
}
$r=Await ($m.StopTetheringAsync()) $Res 60000
Write-Output ('HSSTOPSTATUS=' + [int]$r.Status)`

// scriptHotspotRestore puts the user's saved hotspot settings back after the
// legacy path. It is a constant: the saved name, password and band ("na"
// when this Windows has no band setting) arrive on standard input, in that
// order ([hsInput]), so the user's password is never in script text.
const scriptHotspotRestore = "# devpit hs-restore\n" + hsHelpers + hsReadInput + hsProfileAny + `$os=ReadIn
$op=ReadIn
$ob=ReadIn
$m=$TM::CreateFromConnectionProfile($p)
$ap=$m.GetCurrentAccessPointConfiguration()
$ap.Ssid=$os
$ap.Passphrase=$op
if($ob -ne 'na'){ $ap.Band=[Enum]::ToObject([Windows.Networking.NetworkOperators.TetheringWiFiBand,Windows.Networking.NetworkOperators,ContentType=WindowsRuntime], [int]$ob) }
AwaitAction ($m.ConfigureAccessPointAsync($ap)) 60000
Write-Output 'HSRESTORED=1'`

// scriptHotspotRuleAdd makes the hotspot's firewall rule: inbound TCP 445,
// from the hotspot's own /24 only. The three %s are the rule name, Devpit's
// description and the subnet, all [psString]s.
const scriptHotspotRuleAdd = "# devpit hs-rule-add\n" + `$ErrorActionPreference='Stop'
$n=%s
$d=%s
$ra=%s
New-NetFirewallRule -Name $n -DisplayName $n -Description $d -Direction Inbound -Protocol TCP -LocalPort 445 -RemoteAddress $ra -Profile Any -Action Allow -PolicyStore PersistentStore | Out-Null
Write-Output ('HSFW=' + $n)`

// scriptHotspotRuleRemove removes the rule, only when its exact name and
// Devpit's description both match. A rule that is not there is not an
// error. The two %s are [psString]s.
const scriptHotspotRuleRemove = "# devpit hs-rule-remove\n" + `$ErrorActionPreference='Stop'
$n=%s
$d=%s
$r=@(Get-NetFirewallRule -PolicyStore PersistentStore -Name $n -ErrorAction SilentlyContinue | Where-Object { $_.Name -ceq $n -and $_.Description -ceq $d })
if($r.Count -eq 1){ $r[0] | Remove-NetFirewallRule }
Write-Output ('HSFWGONE=' + $n)`

// hsTokens is the machine lines of one script run, last value winning.
type hsTokens map[string]string

// parseHotspotTokens reads the machine lines out of a script's output.
func parseHotspotTokens(out []string) hsTokens {
	t := hsTokens{}
	for _, l := range out {
		if k, v, ok := ParseHotspotLine(l); ok {
			t[k] = v
		}
	}
	return t
}

// int reads a token as a small non-negative number.
func (t hsTokens) int(k string) (int, bool) {
	v, ok := t[k]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 1000 {
		return 0, false
	}
	return n, true
}

// decodeB64 reads a base64 token.
func (t hsTokens) decodeB64(k string) (string, bool) {
	v, ok := t[k]
	if !ok {
		return "", false
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// redact hides each secret, and its base64 form (how [psString] carries it,
// and how PowerShell may quote a script line in an error), in text.
func redact(text string, secrets []string) string {
	for _, s := range secrets {
		if s == "" {
			continue
		}
		text = strings.ReplaceAll(text, s, "[hidden]")
		text = strings.ReplaceAll(text, base64.StdEncoding.EncodeToString([]byte(s)), "[hidden]")
	}
	return text
}

// execInput runs argv with stdin on its standard input. A non-empty stdin
// needs an [InputExecutor]; without one the run is refused, never moved into
// the script text.
func (s *shareSession) execInput(ctx context.Context, argv []string, stdin string, onLine func(stream, text string)) (int, error) {
	if stdin == "" {
		return s.exec.Exec(ctx, argv, onLine)
	}
	ie, ok := s.exec.(InputExecutor)
	if !ok {
		return -1, errors.New("refused: this worker cannot hand a password to a script safely")
	}
	return ie.ExecInput(ctx, argv, stdin, onLine)
}

// psHotspot runs a hotspot script, with stdin on its standard input (""
// for none), and returns its machine lines. Unlike [shareSession.ps], an
// error never quotes the script's standard output (which may hold the saved
// settings in legacy mode), and every secret is hidden from the error text.
func (s *shareSession) psHotspot(ctx context.Context, script, stdin string, secrets ...string) (hsTokens, error) {
	var out, errLines []string
	code, err := s.execInput(ctx, psArgv(script), stdin, func(stream, text string) {
		if stream == "stderr" {
			errLines = append(errLines, text)
			return
		}
		out = append(out, text)
	})
	t := parseHotspotTokens(out)
	if old, ok := t.decodeB64(hsOldPass); ok {
		secrets = append(secrets, old, t[hsOldPass])
	}
	if err != nil {
		if msg := redact(err.Error(), secrets); msg != err.Error() {
			return t, errors.New(msg)
		}
		return t, err
	}
	if code != 0 {
		return t, fmt.Errorf("powershell exited with %d: %s", code, redact(lastNonEmpty(errLines), secrets))
	}
	if v, ok := t[HotspotLineError]; ok {
		if v != "noprofile" {
			v = "error"
		}
		return t, fmt.Errorf("the hotspot cannot start: Windows has no network connection to share (%s=%s)", HotspotLineError, v)
	}
	return t, nil
}

// hotspotSubnet checks the hotspot's address and returns its /24, the only
// remote address the firewall rule lets in. A public or malformed address is
// refused, so the rule can never open SMB to the internet.
func hotspotSubnet(ip string) (string, error) {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() || !a.IsPrivate() {
		return "", fmt.Errorf("refused: the hotspot address %q is not a private IPv4 address", ip)
	}
	b := a.As4()
	return fmt.Sprintf("%d.%d.%d.0/24", b[0], b[1], b[2]), nil
}

// newHotspotRuleName returns a fresh devpit-hs- rule name.
func newHotspotRuleName() string {
	return "devpit-hs-" + strings.ToLower(rand.Text()[:8])
}

// opHotspot runs the hotspot steps, recording each before it runs.
func (s *shareSession) opHotspot(ctx context.Context, req ShareRequest, emit onLine) error {
	h := &s.state.hotspot
	if !h.empty() || h.wasOn {
		return errors.New("refused: this session already has a hotspot; stop sharing first")
	}
	line := func(k, v string) { emit("info", k+"="+v) }

	emit("info", "Checking the Wi-Fi hotspot")
	t, err := s.psHotspot(ctx, scriptHotspotProbe, "")
	if _, ok := t[HotspotLineError]; ok {
		line(HotspotLineError, "noprofile")
	}
	if err != nil {
		return err
	}
	c, ok := t.int(HotspotLineCapability)
	if !ok {
		return errors.New("the hotspot check reported no capability")
	}
	line(HotspotLineCapability, strconv.Itoa(c))
	if c != int(TetheringEnabled) {
		return fmt.Errorf("the hotspot cannot start: Windows does not allow it here (%s)", TetheringCapability(c).Key())
	}
	if t[HotspotLineWasOn] == "1" {
		h.wasOn = true
		line(HotspotLineWasOn, "1")
		return nil
	}
	if _, ok := t[hsState]; ok {
		return errors.New("the hotspot is in a state Windows does not name; nothing was changed")
	}
	timeout := t[HotspotLineTimeout]
	switch timeout {
	case "1", "0", "na":
		line(HotspotLineTimeout, timeout)
	default:
		return errors.New("the hotspot check reported no timeout setting")
	}
	if timeout == "1" {
		// Recorded first: turning the timeout back on is harmless.
		h.timeoutWasOn, h.timeoutChanged = true, true
		emit("info", "Keeping the hotspot on while nothing is connected")
		if _, err = s.psHotspot(ctx, scriptHotspotTimeoutOff, ""); err != nil {
			// A plain failure changed nothing; a cut-off one may have.
			if ctx.Err() == nil {
				h.timeoutWasOn, h.timeoutChanged = false, false
			}
			return err
		}
	}
	ip, err := s.startHotspot(ctx, req, emit)
	if err != nil || !h.started {
		return err
	}
	return s.hotspotRule(ctx, ip, emit)
}

// startHotspot runs the start script and records what it reports, also
// when the script fails or is cut off. It returns the address the hotspot
// holds, unchecked.
func (s *shareSession) startHotspot(ctx context.Context, req ShareRequest, emit onLine) (string, error) {
	h := &s.state.hotspot
	line := func(k, v string) { emit("info", k+"="+v) }
	// Recorded first: stopping a hotspot that never started finds it off.
	h.started, h.startedHere, h.check = true, true, hsCheckName
	if req.HotspotConfig == hotspotTemp {
		h.check, h.ssid = hsCheckSession, req.SSID
	}
	if req.SetBand5 {
		emit("info", "Switching your hotspot to 5 GHz")
	}
	emit("info", "Starting a direct Wi-Fi hotspot")
	set5 := "0"
	if req.SetBand5 {
		set5 = "1"
	}
	in := hsInput(req.HotspotConfig, req.SSID, req.Passphrase, req.Band, set5)
	t, err := s.psHotspot(ctx, scriptHotspotStart, in, req.Passphrase)

	if t[HotspotLineWasOn] == "1" {
		h.started, h.wasOn = false, true
		line(HotspotLineWasOn, "1")
		return "", err
	}
	mode := t[HotspotLineMode]
	switch mode {
	case "saved", "session", "legacy":
		line(HotspotLineMode, mode)
	default:
		mode = ""
	}
	if mode == "legacy" {
		h.check = hsCheckName
		oldSSID, ok1 := t.decodeB64(hsOldSSID)
		oldPass, ok2 := t.decodeB64(hsOldPass)
		band := t[hsOldBand]
		if ok1 && ok2 && (band == "na" || band == "0" || band == "1" || band == "2" || band == "3") {
			h.legacy = &savedAccessPoint{ssid: oldSSID, passphrase: oldPass, band: band}
		}
	}
	if name, ok := t.decodeB64(HotspotLineSSID); ok && checkHotspotName(name) == nil {
		if req.HotspotConfig == hotspotSaved {
			h.ssid = name
		}
		line(HotspotLineSSID, base64.StdEncoding.EncodeToString([]byte(name)))
	}
	if t[HotspotLineBandSet] == "1" || t[HotspotLineBandSet] == "0" {
		line(HotspotLineBandSet, t[HotspotLineBandSet])
	}
	code, haveStatus := t.int(HotspotLineStatus)
	if haveStatus {
		line(HotspotLineStatus, strconv.Itoa(code))
		if code != int(TetheringSuccess) {
			// Windows did not start it, so there is nothing to stop.
			h.started = false
			if code == int(TetheringAlreadyOn) {
				h.wasOn = true
			}
		}
	}
	if err != nil {
		return "", err
	}
	if _, ok := t[hsState]; ok {
		h.started = false
		return "", errors.New("the hotspot is in a state Windows does not name; it was not started")
	}
	if !haveStatus {
		return "", errors.New("the hotspot start reported no result")
	}
	if code != int(TetheringSuccess) {
		return "", fmt.Errorf("the hotspot did not start: %s (%d)", TetheringStatus(code).Key(), code)
	}
	switch b := t[HotspotLineBand]; b {
	case "auto", "2.4", "5", "6":
		line(HotspotLineBand, b)
	}
	return t[HotspotLineIP], nil
}

// hotspotRule checks the hotspot's address and makes the scoped firewall
// rule for it.
func (s *shareSession) hotspotRule(ctx context.Context, ip string, emit onLine) error {
	h := &s.state.hotspot
	subnet, err := hotspotSubnet(ip)
	if err != nil {
		return err
	}
	emit("info", HotspotLineIP+"="+ip)
	name := newHotspotRuleName()
	// Recorded, and sent for the manifest, before the rule exists:
	// removing a rule that is not there does nothing.
	h.rule = name
	emit("info", HotspotLineRule+"="+name)
	emit("info", "Allowing file sharing for the hotspot only")
	script := fmt.Sprintf(scriptHotspotRuleAdd, psString(name), psString(hsRuleDescription), psString(subnet))
	t, err := s.psHotspot(ctx, script, "")
	if err != nil {
		return fmt.Errorf("making the hotspot firewall rule: %w", err)
	}
	if t[HotspotLineFirewall] != name {
		return errors.New("making the hotspot firewall rule: it was not confirmed")
	}
	h.ruleOn = true
	emit("info", HotspotLineFirewall+"="+name)
	emit("info", HotspotLineOn+"=on")
	return nil
}

// teardownHotspot undoes the hotspot steps, newest first, keeping each
// recorded until it succeeds.
func (s *shareSession) teardownHotspot(ctx context.Context, emit onLine) []error {
	h := &s.state.hotspot
	var errs []error
	if h.rule != "" {
		script := fmt.Sprintf(scriptHotspotRuleRemove, psString(h.rule), psString(hsRuleDescription))
		if _, err := s.psHotspot(ctx, script, ""); err != nil {
			errs = append(errs, fmt.Errorf("removing the hotspot firewall rule: %w", err))
		} else {
			h.rule, h.ruleOn = "", false
		}
	}
	if h.started {
		if err := s.stopHotspot(ctx, emit); err != nil {
			errs = append(errs, fmt.Errorf("stopping the hotspot: %w", err))
		} else {
			h.started = false
		}
	}
	if h.legacy != nil && !h.started {
		l := h.legacy
		in := hsInput(l.ssid, l.passphrase, l.band)
		if _, err := s.psHotspot(ctx, scriptHotspotRestore, in, l.passphrase); err != nil {
			errs = append(errs, fmt.Errorf("restoring your saved hotspot name and password: %w", err))
		} else {
			h.legacy = nil
		}
	}
	if h.timeoutChanged {
		if _, err := s.psHotspot(ctx, scriptHotspotTimeoutOn, ""); err != nil {
			errs = append(errs, fmt.Errorf("restoring the hotspot timeout: %w", err))
		} else {
			h.timeoutChanged, h.timeoutWasOn = false, false
		}
	}
	if h.empty() {
		*h = hotspotState{}
	}
	return errs
}

// stopHotspot stops the hotspot Devpit started, when it can tell it is
// still Devpit's.
func (s *shareSession) stopHotspot(ctx context.Context, emit onLine) error {
	h := &s.state.hotspot
	if h.check == hsCheckSkip {
		emit("info", "Left the hotspot as it is: Windows cannot say whether it is still Devpit's")
		return nil
	}
	check := "name"
	if h.check == hsCheckSession {
		if !h.startedHere {
			// Only this worker's own start is ever stopped without a name.
			emit("info", "Left the hotspot as it is: Windows cannot say whether it is still Devpit's")
			return nil
		}
		check = "session"
	}
	emit("info", "Stopping the hotspot")
	t, err := s.psHotspot(ctx, fmt.Sprintf(scriptHotspotStop, psString(h.ssid), psString(check)), "")
	if err != nil {
		return err
	}
	switch t[hsStop] {
	case "off":
		return nil
	case "notours":
		emit("info", "Left the hotspot on: it is not the one Devpit started")
		return nil
	case "unsure":
		emit("info", "Left the hotspot as it is: Windows did not say it is still on")
		return nil
	}
	code, ok := t.int(hsStopStatus)
	if !ok {
		return errors.New("the stop reported no result")
	}
	if code != int(TetheringSuccess) {
		return fmt.Errorf("the stop answered %s (%d)", TetheringStatus(code).Key(), code)
	}
	return nil
}
