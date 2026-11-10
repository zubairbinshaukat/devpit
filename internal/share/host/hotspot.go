package host

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// Mode is how a share reaches the other PC.
type Mode string

// The modes. The zero value is normal sharing.
const (
	// ModeNormal shares over the network the PC is already on.
	ModeNormal Mode = ""
	// ModeHotspot starts a direct Wi-Fi hotspot for this share and shares
	// over it.
	ModeHotspot Mode = "hotspot"
)

// HotspotChoice is which hotspot settings a hotspot share starts with (owner
// decision 17). The zero value is the saved settings as they are.
type HotspotChoice int

// The three options of the band screen.
const (
	// HotspotSaved starts the hotspot with the name, password and band saved
	// in Windows ("keep it as it is", or the saved band is already 5 GHz).
	HotspotSaved HotspotChoice = iota
	// HotspotSavedSwitchTo5 switches the saved hotspot to 5 GHz, keeping
	// its name and password, then starts it. The switch stays (decision 18).
	HotspotSavedSwitchTo5
	// HotspotTemp starts it with a temporary Devpit-xxxx name and an 8-digit
	// password on 5 GHz. Offered only when [Suggestion.TempSupported].
	HotspotTemp
)

// hotspotProbeLimit bounds the read-only probe behind [Manager.Suggest]. A
// late probe means no suggestion.
const hotspotProbeLimit = 5 * time.Second

// SuggestReason says why hotspot mode is or is not suggested. The values are
// stable keys for the screen and the tests.
type SuggestReason string

// The reasons, one per row of the plan's suggestion table.
const (
	// ReasonSuggested: every rule holds.
	ReasonSuggested SuggestReason = "suggested"
	// ReasonOldCard: the Wi-Fi card is older than Wi-Fi 6 or Windows would
	// not say. Offered, not suggested.
	ReasonOldCard SuggestReason = "old-card"
	// ReasonOn24GHz: the PC is on a 2.4 GHz network. Offered, not suggested.
	ReasonOn24GHz SuggestReason = "on-2.4ghz"
	// ReasonWired: the internet connection is not Wi-Fi. No row; a cable is
	// already the fastest link.
	ReasonWired SuggestReason = "wired"
	// ReasonNoConnection: there is no internet connection, or the Wi-Fi card
	// says it is not connected. No row.
	ReasonNoConnection SuggestReason = "no-connection"
	// ReasonNotAllowed: Windows or an admin does not allow a hotspot here
	// (TetheringCapability not Enabled). No row, no message.
	ReasonNotAllowed SuggestReason = "not-allowed"
	// ReasonNo5GHz: the card cannot run a 5 GHz hotspot. No row.
	ReasonNo5GHz SuggestReason = "no-5ghz"
	// ReasonAlreadyOn: the hotspot is on already; it is never taken over.
	ReasonAlreadyOn SuggestReason = "already-on"
	// ReasonOldWindows: this Windows has no hotspot band setting. No row.
	ReasonOldWindows SuggestReason = "old-windows"
	// ReasonProbeFailed: the probe failed, was late, or gave an answer it
	// does not name. No row, no error shown.
	ReasonProbeFailed SuggestReason = "probe-failed"
)

// Suggestion is the answer of [Manager.Suggest]: whether to show the
// "Direct hotspot" row, whether it is the suggested (first, pre-selected)
// one, and the facts about the saved hotspot the band screen and the card
// need.
type Suggestion struct {
	// Offer says the row is shown at all.
	Offer bool
	// Suggested says the row is first, pre-selected and labelled
	// "suggested". It implies Offer.
	Suggested bool
	// Reason is why, as a stable key.
	Reason SuggestReason
	// Hint is a plain line for the screen, or "" for none: why the row is
	// offered lower ("may be slower than your router"), or that a cable is
	// the fastest link. It never holds a speed.
	Hint string
	// Adapter is the internet connection the hotspot shares from, which is
	// also the normal network for the fallback; pass it as
	// [Options.Adapter] with [ModeHotspot].
	Adapter Adapter
	// SavedSSID and SavedBand are the hotspot settings saved in Windows.
	// SavedBand is "auto", "2.4", "5" or "6".
	SavedSSID, SavedBand string
	// SavedPassword is the user's own saved hotspot password, for the card
	// only. It lives in memory: never on disk, in a log or sent to the
	// worker.
	SavedPassword string
	// Band5Supported and Band6Supported say the card can run a hotspot on
	// that band.
	Band5Supported, Band6Supported bool
	// TempSupported says this Windows can start a temporary name and
	// password without touching the saved settings ([HotspotTemp]).
	TempSupported bool
	// NeedsBandChoice says the saved band is not 5 or 6 GHz, so the band
	// screen is shown before the start.
	NeedsBandChoice bool
}

// HotspotFacts is what the read-only probe reads from Windows.
type HotspotFacts struct {
	// NoProfile says Windows has no internet connection profile, which a
	// hotspot needs.
	NoProfile bool
	// Capability is the tethering capability of that profile.
	Capability elevate.TetheringCapability
	// State is TetheringOperationalState: 0 unknown, 1 on, 2 off, 3 in
	// transition.
	State int
	// SSID and Password are the saved hotspot settings. Password stays in
	// memory.
	SSID, Password string
	// Band is the saved band, "auto", "2.4", "5" or "6", or "" when this
	// Windows has no band setting.
	Band string
	// Band5 and Band6 are IsBandSupported for 5 and 6 GHz.
	Band5, Band6 bool
	// Session says the per-session configuration type constructs.
	Session bool
}

// The tethering operational states.
const (
	tetherStateOn           = 1
	tetherStateOff          = 2
	tetherStateInTransition = 3
)

// hotspotProbeScript reads, and only reads, the hotspot facts. It is a
// constant: nothing is ever put into it. Every value is a machine token; the
// name and the password travel as base64 so no text is parsed. None of the
// calls changes anything: capability, state and the saved configuration are
// read, IsBandSupported asks, and the session type is only constructed.
const hotspotProbeScript = `# devpit hs-suggest-probe
$ErrorActionPreference='Stop'
function B64($s) { [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([string]$s)) }
try {
$TM=[Windows.Networking.NetworkOperators.NetworkOperatorTetheringManager,Windows.Networking.NetworkOperators,ContentType=WindowsRuntime]
$NI=[Windows.Networking.Connectivity.NetworkInformation,Windows.Networking.Connectivity,ContentType=WindowsRuntime]
$p=$NI::GetInternetConnectionProfile()
if($null -eq $p){ Write-Output 'HSERR=noprofile'; exit 0 }
$cap=[int]$TM::GetTetheringCapabilityFromConnectionProfile($p)
Write-Output ('HSCAP=' + $cap)
if($cap -ne 0){ exit 0 }
$m=$TM::CreateFromConnectionProfile($p)
Write-Output ('HSSTATE=' + [int]$m.TetheringOperationalState)
$ap=$m.GetCurrentAccessPointConfiguration()
Write-Output ('HSSSID=' + (B64 $ap.Ssid))
Write-Output ('HSPASS=' + (B64 $ap.Passphrase))
$BandT=$null
try { $BandT=[Windows.Networking.NetworkOperators.TetheringWiFiBand,Windows.Networking.NetworkOperators,ContentType=WindowsRuntime] } catch { $BandT=$null }
if($null -ne $BandT -and $null -ne $ap.Band){
  $names=@('auto','2.4','5','6')
  $b=[int]$ap.Band
  if($b -ge 0 -and $b -le 3){ Write-Output ('HSBAND=' + $names[$b]) }
  $s5='na'
  try { if($ap.IsBandSupported([Enum]::ToObject($BandT, 2))){ $s5='1' } else { $s5='0' } } catch { $s5='na' }
  Write-Output ('HSB5=' + $s5)
  $s6='na'
  try { if($ap.IsBandSupported([Enum]::ToObject($BandT, 3))){ $s6='1' } else { $s6='0' } } catch { $s6='na' }
  Write-Output ('HSB6=' + $s6)
}
$ok='0'
try {
  $ST=[Windows.Networking.NetworkOperators.NetworkOperatorTetheringSessionAccessPointConfiguration,Windows.Networking.NetworkOperators,ContentType=WindowsRuntime]
  $x=$ST::new()
  if($null -ne $x){ $ok='1' }
} catch { $ok='0' }
Write-Output ('HSSESSION=' + $ok)
} catch { Write-Output 'HSERR=error' }
`

// errProbe is a probe whose answer is incomplete or not understood.
var errProbe = errors.New("the hotspot check gave no usable answer")

// parseHotspotProbe reads the probe's tokens. Anything it does not name, or
// an answer that is missing a part, is an error, which means no suggestion.
func parseHotspotProbe(out string) (HotspotFacts, error) {
	t := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := elevate.ParseHotspotLine(l); ok {
			t[k] = v
		}
	}
	var f HotspotFacts
	if v, ok := t["HSERR"]; ok {
		if v == "noprofile" {
			f.NoProfile = true
			return f, nil
		}
		return HotspotFacts{}, errProbe
	}
	c, err := strconv.Atoi(t["HSCAP"])
	if err != nil || c < 0 || c > 100 {
		return HotspotFacts{}, errProbe
	}
	f.Capability = elevate.TetheringCapability(c)
	if f.Capability != elevate.TetheringEnabled {
		return f, nil
	}
	if f.State, err = strconv.Atoi(t["HSSTATE"]); err != nil {
		return HotspotFacts{}, errProbe
	}
	ssid, ok1 := decodeToken(t, "HSSSID")
	pass, ok2 := decodeToken(t, "HSPASS")
	if !ok1 || !ok2 {
		return HotspotFacts{}, errProbe
	}
	f.SSID, f.Password = ssid, pass
	switch b := t["HSBAND"]; b {
	case "auto", "2.4", "5", "6":
		f.Band = b
	}
	f.Band5, f.Band6 = t["HSB5"] == "1", t["HSB6"] == "1"
	f.Session = t["HSSESSION"] == "1"
	return f, nil
}

// decodeToken reads a base64 token.
func decodeToken(t map[string]string, k string) (string, bool) {
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

// RunHotspotProbe is the real hotspot probe: Windows PowerShell 5.1 by its
// full System32 path, not elevated, a constant read-only script on stdin,
// hidden window, and a time limit. Outside Windows it always fails.
func RunHotspotProbe(ctx context.Context) (HotspotFacts, error) {
	out, err := runHotspotProbe(ctx, hotspotProbeScript, hotspotProbeLimit)
	if err != nil {
		return HotspotFacts{}, err
	}
	return parseHotspotProbe(out)
}

// The hint lines. None names a speed.
const (
	hintMaybeSlower = "May be slower than your router."
	hintCable       = "A cable is the fastest link."
)

// Suggest applies the suggestion rule (plan section 3): hotspot mode is
// suggested only when the internet connection is a connected Wi-Fi card of
// Wi-Fi 6 or newer, Windows allows a hotspot, the card can run one on 5 GHz,
// the hotspot is not on already, and this Windows has the band setting. An
// older card or a 2.4 GHz network offers it lower, not suggested. Anything
// else, including a failed or late probe, offers nothing and shows no error.
//
// It only reads, needs no admin, and returns within the 5 s probe limit
// whatever the probe does.
func (m *Manager) Suggest(ctx context.Context) Suggestion {
	s := m.suggest(ctx)
	m.mu.Lock()
	m.saved = savedHotspot{ssid: s.SavedSSID, password: s.SavedPassword}
	m.mu.Unlock()
	return s
}

// suggest is Suggest without remembering the saved settings.
func (m *Manager) suggest(ctx context.Context) Suggestion {
	none := func(r SuggestReason) Suggestion { return Suggestion{Reason: r} }
	adapters, err := m.deps.Net.Adapters(ctx)
	if err != nil {
		return none(ReasonNoConnection)
	}
	var best *Adapter
	for i := range adapters {
		if adapters[i].Best {
			best = &adapters[i]
			break
		}
	}
	if best == nil {
		return none(ReasonNoConnection)
	}
	if !best.Wireless {
		return Suggestion{Reason: ReasonWired, Hint: hintCable, Adapter: *best}
	}
	// The card itself, when the WLAN service answers. Without it the IP
	// helper's "this is Wi-Fi" is the fallback for "connected".
	wifi, _ := m.deps.Wifi()
	card, haveCard := wifiByGUID(wifi, best.GUID)
	if haveCard && !card.Connected {
		return none(ReasonNoConnection)
	}

	f, err := m.probe(ctx)
	if err != nil {
		return none(ReasonProbeFailed)
	}
	if f.NoProfile {
		return none(ReasonNoConnection)
	}
	if f.Capability != elevate.TetheringEnabled {
		return none(ReasonNotAllowed)
	}
	switch f.State {
	case tetherStateOn, tetherStateInTransition:
		return none(ReasonAlreadyOn)
	case tetherStateOff:
	default:
		return none(ReasonProbeFailed)
	}
	if f.Band == "" {
		return none(ReasonOldWindows)
	}
	if !f.Band5 {
		return none(ReasonNo5GHz)
	}
	s := Suggestion{
		Offer: true, Suggested: true, Reason: ReasonSuggested, Adapter: *best,
		SavedSSID: f.SSID, SavedPassword: f.Password, SavedBand: f.Band,
		Band5Supported: f.Band5, Band6Supported: f.Band6, TempSupported: f.Session,
		NeedsBandChoice: f.Band != "5" && f.Band != "6",
	}
	modern := winapi.IsWifi6OrNewer(card.PHYs)
	if !haveCard {
		modern = best.Standard == "Wi-Fi 6" || best.Standard == "Wi-Fi 7"
	}
	switch {
	case !modern:
		s.Suggested, s.Reason, s.Hint = false, ReasonOldCard, hintMaybeSlower
	case haveCard && on24GHz(card, f):
		s.Suggested, s.Reason, s.Hint = false, ReasonOn24GHz, hintMaybeSlower
	}
	return s
}

// on24GHz reports whether the card's current channel says it is on 2.4 GHz.
// Windows gives a channel number and no frequency, and 6 GHz channels also
// start at 1, so a channel from 1 to 14 means 2.4 GHz only on a card that
// cannot do 6 GHz: no 6 GHz hotspot band and no Wi-Fi 7 PHY (Wi-Fi 6 and 6E
// share one PHY type, so HE alone says nothing). Otherwise the band is
// unknown, which is never a reason to demote.
func on24GHz(card winapi.WifiInterface, f HotspotFacts) bool {
	if card.Channel < 1 || card.Channel > 14 {
		return false
	}
	if f.Band6 {
		return false
	}
	for _, p := range card.PHYs {
		if p == winapi.PHYEHT {
			return false
		}
	}
	return true
}

// probe runs the hotspot probe within its limit. It never waits past the
// limit, even for a probe that ignores its context.
func (m *Manager) probe(ctx context.Context) (HotspotFacts, error) {
	ctx, cancel := context.WithTimeout(ctx, m.deps.probeLimit)
	defer cancel()
	type result struct {
		f   HotspotFacts
		err error
	}
	ch := make(chan result, 1)
	go func() {
		f, err := m.deps.HotspotProbe(ctx)
		ch <- result{f, err}
	}()
	select {
	case r := <-ch:
		if r.err == nil && ctx.Err() != nil {
			r.err = ctx.Err()
		}
		return r.f, r.err
	case <-ctx.Done():
		return HotspotFacts{}, ctx.Err()
	}
}

// savedHotspot is the saved hotspot name and password from the last
// [Manager.Suggest], kept in memory for the card of a saved-mode share.
type savedHotspot struct {
	ssid, password string
}

// HotspotFallbackError is a hotspot start that did not work, after which
// Devpit could not simply carry on with normal sharing. Key is the stable
// name of what happened (an [elevate.TetheringStatus] key, or "already-on",
// "not-allowed", "unknown"), and Message the plain words to show (plan 8.2).
//
// When Err is [ErrNeedPrivate] the normal network is Public: the hotspot was
// undone, the worker is kept, and the screen asks the usual Private question
// and calls [Manager.Start] again with [ModeNormal] (and MakePrivate on a
// yes, which needs no second admin prompt) or [Manager.Stop] on a no.
type HotspotFallbackError struct {
	Key, Message string
	Err          error
}

// Error implements error.
func (e *HotspotFallbackError) Error() string {
	if e.Err == nil {
		return e.Message
	}
	return e.Message + " (" + e.Err.Error() + ")"
}

// Unwrap returns the reason normal sharing could not go on by itself.
func (e *HotspotFallbackError) Unwrap() error { return e.Err }

// Messages for hotspot failures the 8.2 table words by capability.
const msgNotAllowed = "Windows does not allow a hotspot on this PC right now. Sharing over your network instead."

// hotspotOutcome is what the hotspot op reported, token by token.
type hotspotOutcome struct {
	wasOn     bool
	capCode   int
	haveCap   bool
	errToken  bool
	status    int
	haveState bool
	ssid      string
	band      string
	ip        string
	rule      string
	on        bool
	saveErr   error
}

// fallback names what went wrong for the screen: a key and the 8.2 message.
func (o hotspotOutcome) fallback() (key, message string) {
	switch {
	case o.wasOn:
		s := elevate.TetheringAlreadyOn
		return s.Key(), s.Message()
	case o.haveCap && o.capCode != int(elevate.TetheringEnabled):
		return "not-allowed", msgNotAllowed
	case o.haveState && o.status != int(elevate.TetheringSuccess):
		s := elevate.TetheringStatus(o.status)
		return s.Key(), s.Message()
	default:
		// HSERR, a thrown error, no address, a failed rule: code 1.
		s := elevate.TetheringUnknown
		return s.Key(), s.Message()
	}
}

// Steps lists the setup steps [Manager.Start] reports for opts, in order,
// so a screen can draw them all before they run. private says the chosen
// network is already Private (or a domain network): in normal mode the
// network step is then left out, and in hotspot mode the normal path is
// prepared too (the firewall step). Keys are stable; labels are the engine's
// wording.
func Steps(opts Options, private bool) []Step {
	if opts.Mode != ModeHotspot {
		if private {
			return []Step{stepFirewall, stepLogin, stepFolder, stepShare}
		}
		return []Step{stepNetwork, stepFirewall, stepLogin, stepFolder, stepShare}
	}
	var out []Step
	if opts.Hotspot == HotspotSavedSwitchTo5 {
		out = append(out, stepBand5)
	}
	out = append(out, stepHotspot, stepHotspotFirewall)
	if private {
		out = append(out, stepFirewall)
	}
	return append(out, stepLogin, stepFolder, stepShare)
}
