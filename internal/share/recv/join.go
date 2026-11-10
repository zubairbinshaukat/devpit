package recv

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/store"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// Wifi is the receiver's Wi-Fi card: the read-only probe and the join calls
// of [winapi]. Tests replace it with a fake; [WinWifi] is the real one.
type Wifi interface {
	// Interfaces lists the Wi-Fi cards and their current link.
	Interfaces() ([]winapi.WifiInterface, error)
	// Scan lists the nearby networks.
	Scan(guid string) ([]winapi.WifiNetwork, error)
	// Visible reports whether ssid is in the nearby list Windows already
	// has, without asking for a scan. It only reads; an error means the
	// caller does not know.
	Visible(guid, ssid string) (bool, error)
	// HasProfile reports whether a saved profile called name exists.
	HasProfile(guid, name string) (bool, error)
	// Join adds a per-user manual-mode profile for ssid and connects with
	// it, waiting no longer than ctx.
	Join(ctx context.Context, guid, ssid, pass string) error
	// ConnectProfile connects with a saved profile, waiting no longer than
	// ctx.
	ConnectProfile(ctx context.Context, guid, profile string) error
	// Leave deletes tempProfile (when not "") and reconnects to previous
	// (when not ""); the reconnect waits no longer than ctx.
	Leave(ctx context.Context, guid, tempProfile, previous string) error
	// Gateway returns the IPv4 default gateway of the card, "" when it has
	// none yet. It only reads.
	Gateway(guid string) (string, error)
	// Wired reports whether the PC is on a cable: an Ethernet adapter that
	// is up and has a default gateway. It only reads.
	Wired() (bool, error)
}

// WinWifi is [Wifi] over the Windows WLAN service.
type WinWifi struct{}

// Interfaces implements [Wifi].
func (WinWifi) Interfaces() ([]winapi.WifiInterface, error) { return winapi.WifiInterfaces() }

// Scan implements [Wifi].
func (WinWifi) Scan(guid string) ([]winapi.WifiNetwork, error) { return winapi.WifiScan(guid) }

// Visible implements [Wifi].
func (WinWifi) Visible(guid, ssid string) (bool, error) { return winapi.WifiVisible(guid, ssid) }

// HasProfile implements [Wifi].
func (WinWifi) HasProfile(guid, name string) (bool, error) { return winapi.WifiHasProfile(guid, name) }

// Join implements [Wifi].
func (WinWifi) Join(ctx context.Context, guid, ssid, pass string) error {
	return winapi.WifiJoin(ctx, guid, ssid, pass)
}

// ConnectProfile implements [Wifi].
func (WinWifi) ConnectProfile(ctx context.Context, guid, profile string) error {
	return winapi.WifiConnectProfile(ctx, guid, profile)
}

// Leave implements [Wifi].
func (WinWifi) Leave(ctx context.Context, guid, tempProfile, previous string) error {
	return winapi.WifiLeave(ctx, guid, tempProfile, previous)
}

// Gateway implements [Wifi].
func (WinWifi) Gateway(guid string) (string, error) { return winapi.InterfaceGateway(guid) }

// Wired implements [Wifi].
func (WinWifi) Wired() (bool, error) { return winapi.HasWiredGateway() }

// DefaultHotspotHost is the address of the PC that shares over Windows Mobile
// Hotspot, unless its Internet Connection Sharing range was changed. It is the
// answer when the joined card reports no private gateway.
const DefaultHotspotHost = "192.168.137.1"

// gatewayTries and gatewayStep bound how long [Session.Join] waits for the
// card's gateway: the address can arrive a moment after the link is up.
const (
	gatewayTries = 10
	gatewayStep  = 500 * time.Millisecond
)

// HotspotPrefix starts the name of a hotspot Devpit made with a temporary
// name; [Session.FindHotspots] lists those first.
const HotspotPrefix = "Devpit-"

// joinFileName is the join record's name, beside the resume record.
const joinFileName = "share-join.json"

// joinVersion is bumped when the join record's shape changes.
const joinVersion = 1

// linkSettle is how long after joining the link is read, so the rate the
// card reports is the negotiated one rather than the first frame's.
const linkSettle = 3 * time.Second

// RejoinInterval is the least time between two tries to put the PC back on
// the hotspot during a copy. A try that finds nothing can knock the PC off
// the network it fell back to for as long as the connect waits, so it is
// made only when the hotspot is in range, and at most this often.
const RejoinInterval = 60 * time.Second

// leaveTimeout bounds [Session.Leave]: the delete is quick, and the wait for
// the old network is cut off after this. A PC that is slower to reconnect is
// told to pick its network.
const leaveTimeout = 20 * time.Second

// ExitLeaveTimeout bounds [LeaveOnExit]. It stays under the time the app
// gives a busy screen to stop, so quitting never hangs on Wi-Fi.
const ExitLeaveTimeout = 8 * time.Second

// Errors of the join flow.
var (
	// ErrNoWifiCard means this PC has no Wi-Fi card Devpit can use, so the
	// hotspot path is not offered.
	ErrNoWifiCard = errors.New("this PC has no Wi-Fi card Devpit can use")
	// ErrNeedPassword means there is no saved profile for the hotspot and
	// no hotspot password was given.
	ErrNeedPassword = errors.New("type the hotspot password from the other PC's card")
	// ErrHotspotInUse means another Devpit window, still running, made the
	// Wi-Fi join on this PC and is copying over it, so this one must not undo
	// or replace it. [HotspotInUseNote] is the sentence for the screen.
	ErrHotspotInUse = errors.New("another Devpit window is using the Devpit hotspot right now")
	// ErrScanDenied means Windows would not list the nearby networks (for
	// example with location access off). The screen then asks for the
	// hotspot's name, which is on the other PC's card, and joins by name:
	// [Session.Join] needs no scan result. It wraps [winapi.ErrWifiDenied].
	ErrScanDenied = errors.New("the nearby Wi-Fi networks could not be listed: Windows did not allow it")
)

// HotspotInUseNote is the one line the screen shows when a join record
// belongs to another running Devpit window.
const HotspotInUseNote = "Another Devpit window is using the Devpit hotspot right now."

// ownerSlack allows for rounding of the creation time through the join
// record's JSON when it is compared with the process's own.
const ownerSlack = time.Second

// JoinRecord is what Devpit changed on this PC's Wi-Fi, saved before the
// change so that a crash can be undone at the next open of Share Files. It
// never holds the hotspot password.
type JoinRecord struct {
	// Version is the record's format.
	Version int `json:"version"`
	// Interface is the Wi-Fi card's GUID.
	Interface string `json:"interface"`
	// SSID is the hotspot's name.
	SSID string `json:"ssid"`
	// Profile is the profile Devpit connected with: its own, or the one the
	// user already had.
	Profile string `json:"profile"`
	// TempProfile is the profile Devpit added, "" when an existing one was
	// used. Created says the same in words: only a profile the record says
	// Devpit created is ever deleted.
	TempProfile string `json:"temp_profile"`
	Created     bool   `json:"created_by_devpit"`
	// Previous is the profile the card was on before, "" when none.
	Previous string `json:"previous"`
	// Time is when the record was written.
	Time time.Time `json:"time"`
	// PID and ProcessStart name the Devpit process that made the join. The
	// start time tells that process from an unrelated one that was given its
	// PID later. A record without them (an older Devpit wrote it) has no
	// owner and counts as a leftover. ProcessStart is the zero time when
	// Windows would not say.
	PID          int       `json:"pid"`
	ProcessStart time.Time `json:"process_start"`
}

// LinkInfo is one Wi-Fi link as the card reports it.
type LinkInfo struct {
	// OnWifi is false for a PC whose Wi-Fi card is not connected: wired, or
	// on nothing.
	OnWifi bool
	// Profile is the profile the card is connected with.
	Profile string
	// PHY, RxKbps, TxKbps and Channel are the link's details, 0 when unknown.
	PHY            uint32
	RxKbps, TxKbps uint32
	Channel        uint32
	// Standard names the link's generation, such as "Wi-Fi 6", or "".
	Standard string
	// Wired is true, for a PC not on Wi-Fi, when an Ethernet adapter that is
	// up has a default gateway: the PC is on a cable. False means it is on
	// nothing, or that could not be read.
	Wired bool
}

// rate is the link rate that matters for a receiver, which mostly downloads:
// the receive rate, or the send rate when that is all the card says.
func (l LinkInfo) rate() uint64 {
	if l.RxKbps > 0 {
		return uint64(l.RxKbps)
	}
	return uint64(l.TxKbps)
}

// WorseLinkMarginPercent is how far below the previous link's rate the
// hotspot link must be before it counts as worse. Link rates move a step or
// two with signal and load (one modulation step is roughly 10 to 20 percent
// at the same channel width), so a smaller margin would switch back on
// noise; a narrower hotspot channel, such as a 20 MHz soft AP against an 80
// MHz router link, is far below it and is caught. The hotspot also saves the
// router hop, so an equal rate is not worse. To be tuned with the spike S7
// measurements.
const WorseLinkMarginPercent = 25

// LinkVerdict is the measure-then-decide answer after a join: whether the
// hotspot link is worse than the link the PC had before, both links so the
// screen can show them, and one plain sentence. It promises no speed.
type LinkVerdict struct {
	Worse         bool
	Before, After LinkInfo
	// Reason is the sentence to show. It is set whenever Worse is, and also
	// for a PC that was on no network before (Worse false: there is nothing
	// better to go back to).
	Reason string
	// Unknown is true when both links are Wi-Fi but a rate is not known
	// (Windows would not say, for example with location access off), so the
	// links could not be compared. It is never a reason to switch.
	Unknown bool
}

// The verdict sentences. None names a number or promises a speed.
const (
	reasonWired      = "This PC is on a cable, not on Wi-Fi, so Devpit keeps its own connection."
	reasonNoWifiYet  = "This PC was not on Wi-Fi before, so Devpit kept the direct link."
	reasonNoLink     = "The direct link did not come up."
	reasonWorseLink  = "The direct link is slower than your Wi-Fi."
	noteLeaveRetried = "Devpit could not remove its Wi-Fi entry yet; it tries again next time Share Files opens."
)

// UnknownLinkNote is the sentence for a verdict whose links could not be
// compared ([LinkVerdict].Unknown). It promises no speed.
const UnknownLinkNote = "Windows did not say how fast the links are, so Devpit kept the direct link."

// CompareLinks decides whether the hotspot link (after) is worse than the
// link before the join. A PC on a cable (not on Wi-Fi, with a wired gateway)
// is told to stay wired; a PC that was on no network keeps the direct link,
// since there is nothing better to go back to. An unknown rate on either side
// is never a reason to switch.
func CompareLinks(before, after LinkInfo) LinkVerdict {
	v := LinkVerdict{Before: before, After: after}
	switch {
	case !before.OnWifi && before.Wired:
		v.Worse, v.Reason = true, reasonWired
	case !after.OnWifi:
		v.Worse, v.Reason = true, reasonNoLink
	case !before.OnWifi:
		v.Reason = reasonNoWifiYet
	default:
		b, a := before.rate(), after.rate()
		switch {
		case b == 0 || a == 0:
			v.Unknown = true
		case a*100 < b*(100-WorseLinkMarginPercent):
			v.Worse, v.Reason = true, reasonWorseLink
		}
	}
	return v
}

// linkOf reads one card's link.
func linkOf(w winapi.WifiInterface) LinkInfo {
	if !w.Connected {
		return LinkInfo{}
	}
	return LinkInfo{
		OnWifi: true, Profile: w.Profile, PHY: w.PHY, RxKbps: w.RxKbps, TxKbps: w.TxKbps, Channel: w.Channel,
		Standard: winapi.WifiStandard([]uint32{w.PHY}),
	}
}

// WifiCard returns the Wi-Fi card to join with: the connected one, else the
// first. False means no card (or no WLAN service), and the hotspot path is
// not offered.
func (s *Session) WifiCard() (winapi.WifiInterface, bool) {
	if s.deps.Wifi == nil {
		return winapi.WifiInterface{}, false
	}
	list, err := s.deps.Wifi.Interfaces()
	if err != nil || len(list) == 0 {
		return winapi.WifiInterface{}, false
	}
	for _, w := range list {
		if w.Connected {
			return w, true
		}
	}
	return list[0], true
}

// card returns the card with this GUID.
func (s *Session) card(guid string) (winapi.WifiInterface, bool) {
	if s.deps.Wifi == nil {
		return winapi.WifiInterface{}, false
	}
	list, err := s.deps.Wifi.Interfaces()
	if err != nil {
		return winapi.WifiInterface{}, false
	}
	for _, w := range list {
		if strings.EqualFold(w.GUID, guid) {
			return w, true
		}
	}
	return winapi.WifiInterface{}, false
}

// CanJoin reports whether this PC can join a hotspot at all.
func (s *Session) CanJoin() bool {
	_, ok := s.WifiCard()
	return ok
}

// CurrentLink reads the link the PC has now, for the screen's "before" line
// and for telling a wired PC to stay wired before anything is joined.
func (s *Session) CurrentLink() (LinkInfo, error) {
	w, ok := s.WifiCard()
	if !ok {
		return LinkInfo{}, ErrNoWifiCard
	}
	return s.linkNow(w), nil
}

// linkNow is linkOf, plus whether a PC that is not on Wi-Fi is on a cable.
func (s *Session) linkNow(w winapi.WifiInterface) LinkInfo {
	l := linkOf(w)
	if !l.OnWifi {
		l.Wired, _ = s.deps.Wifi.Wired()
	}
	return l
}

// FindHotspots scans and returns every nearby network, Devpit-named
// hotspots first, each group strongest first. A list Windows refuses (for
// example with location access off) is [ErrScanDenied]: the screen then
// asks for the hotspot's name and joins with it.
func (s *Session) FindHotspots() ([]winapi.WifiNetwork, error) {
	w, ok := s.WifiCard()
	if !ok {
		return nil, ErrNoWifiCard
	}
	nets, err := s.deps.Wifi.Scan(w.GUID)
	if errors.Is(err, winapi.ErrWifiDenied) {
		return nil, fmt.Errorf("%w: %w", ErrScanDenied, err)
	}
	if err != nil {
		return nil, err
	}
	return SortHotspots(nets), nil
}

// SortHotspots puts Devpit-named networks first and keeps the order the
// scan gave (strongest first) inside each group.
func SortHotspots(nets []winapi.WifiNetwork) []winapi.WifiNetwork {
	out := slices.Clone(nets)
	slices.SortStableFunc(out, func(a, b winapi.WifiNetwork) int {
		da, db := strings.HasPrefix(a.SSID, HotspotPrefix), strings.HasPrefix(b.SSID, HotspotPrefix)
		switch {
		case da && !db:
			return -1
		case db && !da:
			return 1
		}
		return 0
	})
	return out
}

// JoinRequest is the hotspot to join.
type JoinRequest struct {
	// SSID is the hotspot's name: from a scan, or typed from the other PC's
	// card when Windows would not list the networks ([ErrScanDenied]).
	SSID string
	// Password is the hotspot password. It is used once and never saved.
	// It may be "" when the PC already has a saved profile for the hotspot.
	Password string
	// Profile is the saved profile the scan reported for the hotspot, if
	// any; it may be named differently from the SSID. "" for a typed name:
	// a saved profile named like the SSID is still found and used.
	Profile string
}

// JoinResult is how a join went.
type JoinResult struct {
	// Record is what was saved about the join.
	Record JoinRecord
	// UsedSaved is true when the PC's own saved profile was used.
	UsedSaved bool
	// Verdict compares the link before with the hotspot link.
	Verdict LinkVerdict
	// Undo is filled when the join failed and was undone.
	Undo LeaveResult
	// Host is the sharing PC's address on the hotspot: the joined card's
	// private IPv4 default gateway, else [DefaultHotspotHost]. It is set only
	// after a successful join.
	Host string
	// Check is [Session.CheckHost] on Host right after the join. When
	// Check.Local is true this PC holds the other PC's address itself (a
	// leftover of its own Mobile Hotspot) and cannot reach it; the screen
	// offers [Session.FreeAddress] when Check.Fixable. It is empty when the
	// join failed or the check could not run.
	Check HostCheck
}

// SavedProfile returns the name of a saved profile for ssid, or "". hint is
// a profile name the scan reported.
func (s *Session) SavedProfile(guid, ssid, hint string) (string, error) {
	for _, name := range []string{hint, ssid} {
		if name == "" {
			continue
		}
		has, err := s.deps.Wifi.HasProfile(guid, name)
		if err != nil {
			return "", err
		}
		if has {
			return name, nil
		}
	}
	return "", nil
}

// Join joins the other PC's hotspot. It writes the join record first, then
// connects: with the PC's own saved profile when there is one (no password,
// and that profile is never deleted), else with a new manual-mode profile.
// It then reads the new link and compares it with the old one, and checks
// that this PC does not hold the other PC's address itself (JoinResult.Check).
// A failed
// join is undone before it returns, and the result says what was put back.
func (s *Session) Join(ctx context.Context, req JoinRequest) (JoinResult, error) {
	w, ok := s.WifiCard()
	if !ok {
		return JoinResult{}, ErrNoWifiCard
	}
	// A join that was never undone (a crash) goes first, so its record is
	// not overwritten and its profile not lost.
	// A join a live Devpit window owns is not a leftover: leave it alone and
	// say so.
	if undone, err := s.UndoLeftoverJoin(); err != nil {
		return JoinResult{}, err
	} else if undone.InUse {
		return JoinResult{}, ErrHotspotInUse
	}
	if w2, ok := s.card(w.GUID); ok {
		w = w2
	}
	before := s.linkNow(w)
	saved, err := s.SavedProfile(w.GUID, req.SSID, req.Profile)
	if err != nil {
		return JoinResult{}, fmt.Errorf("reading the saved Wi-Fi networks: %w", err)
	}
	if saved == "" && req.Password == "" {
		return JoinResult{}, ErrNeedPassword
	}
	if saved == "" {
		// A typed name is checked before anything is written or changed.
		if nerr := winapi.CheckNetworkName(req.SSID); nerr != nil {
			return JoinResult{}, nerr
		}
	}
	rec := JoinRecord{
		Version: joinVersion, Interface: w.GUID, SSID: req.SSID, Profile: saved,
		Time: s.deps.Now().UTC(),
	}
	rec.PID = s.pid()
	rec.ProcessStart, _ = s.processStart(rec.PID)
	if before.OnWifi {
		rec.Previous = before.Profile
	}
	if saved == "" {
		rec.Profile, rec.TempProfile, rec.Created = req.SSID, req.SSID, true
	}
	if rec.Previous == rec.Profile {
		rec.Previous = "" // already on the hotspot: nothing to go back to
	}
	if serr := s.saveJoin(rec); serr != nil {
		return JoinResult{}, fmt.Errorf("saving the join record: %w", serr)
	}

	res := JoinResult{UsedSaved: !rec.Created}
	switch {
	case !rec.Created && before.OnWifi && before.Profile == rec.Profile:
		// Already connected with it.
	case !rec.Created:
		err = s.deps.Wifi.ConnectProfile(ctx, w.GUID, rec.Profile)
	default:
		err = s.deps.Wifi.Join(ctx, w.GUID, req.SSID, req.Password)
		if errors.Is(err, winapi.ErrProfileExists) {
			// A profile of that name appeared since the check: it is the
			// user's, so use it and never delete it.
			rec.TempProfile, rec.Created = "", false
			res.UsedSaved = true
			if err = s.saveJoin(rec); err == nil {
				err = s.deps.Wifi.ConnectProfile(ctx, w.GUID, rec.Profile)
			}
		}
	}
	res.Record = rec
	if err != nil {
		// The undo runs even when ctx was cancelled (Esc during the join),
		// with its own bound.
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), leaveTimeout)
		undo, uerr := s.leaveRecord(uctx, rec)
		cancel()
		res.Undo = undo
		if uerr != nil {
			return res, fmt.Errorf("joining the hotspot: %w (and undoing it: %w)", err, uerr)
		}
		return res, fmt.Errorf("joining the hotspot: %w", err)
	}
	s.hotspot = req.SSID

	s.deps.Sleep(ctx, linkSettle)
	after := LinkInfo{}
	if w2, ok := s.card(w.GUID); ok {
		after = linkOf(w2)
	}
	res.Verdict = CompareLinks(before, after)
	res.Host = s.hotspotHost(ctx, w.GUID)
	if s.deps.Addresses != nil {
		// A failed check is not a failed join: Check stays empty.
		if c, cerr := s.CheckHost(ctx, res.Host); cerr == nil {
			res.Check = c
		}
	}
	return res, nil
}

// hotspotHost reads the joined card's default gateway, waiting up to about 5
// seconds for it to arrive, and returns it when it is a private IPv4 address.
// Anything else (no gateway, an error, a public address) gives
// [DefaultHotspotHost]: a hotspot gateway is never public, and the usual
// address is the best guess.
func (s *Session) hotspotHost(ctx context.Context, guid string) string {
	for i := range gatewayTries {
		if gw, err := s.deps.Wifi.Gateway(guid); err == nil && gw != "" {
			if privateIPv4(gw) {
				return gw
			}
			break // the card has a gateway, and it is not a hotspot's
		}
		if i == gatewayTries-1 || ctx.Err() != nil {
			break
		}
		s.deps.Sleep(ctx, gatewayStep)
	}
	return DefaultHotspotHost
}

// privateIPv4 reports whether s is an IPv4 address in a private range.
func privateIPv4(s string) bool {
	a, err := netip.ParseAddr(s)
	return err == nil && a.Is4() && a.IsPrivate()
}

// LeaveResult is what [Session.Leave] put back.
type LeaveResult struct {
	// Left is true when there was a join to undo.
	Left bool
	// Deleted is true when Devpit's own profile was deleted.
	Deleted bool
	// Reconnected is true when the PC is back on Previous.
	Reconnected bool
	Previous    string
	// InUse is true when the join record belongs to another running Devpit
	// window: nothing was undone, and the record is untouched.
	InUse bool
	// Note is one plain sentence for the screen, "" when there is nothing
	// to say.
	Note string
}

// Leave undoes the join: it deletes the profile only when the record says
// Devpit created it, reconnects to the previous network, and removes the
// record once that is done. With no record it does nothing, and a record that
// another running Devpit window made is left alone (the result says InUse).
// It is safe to call on every exit path, any number of times. The wait for
// the old network is bounded; see [Session.LeaveContext].
func (s *Session) Leave() (LeaveResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), leaveTimeout)
	defer cancel()
	return s.LeaveContext(ctx)
}

// LeaveContext is [Session.Leave] with the wait for the old network ending
// when ctx does. Devpit's own Wi-Fi entry is deleted first either way; a
// reconnect cut short leaves a note to pick the network in Windows.
func (s *Session) LeaveContext(ctx context.Context) (LeaveResult, error) {
	rec, ok, err := s.loadJoin()
	if err != nil || !ok {
		s.hotspot = ""
		return LeaveResult{}, err
	}
	if s.ownedByOtherWindow(rec) {
		s.hotspot = ""
		return LeaveResult{InUse: true}, nil
	}
	return s.leaveRecord(ctx, rec)
}

// LeaveOnExit is the receiver's exit hook, the twin of the sharing side's
// host.ShutdownAll: the program calls it once after the screen loop has
// ended, however it ended. When the join record in stateDir belongs to this
// very process (its PID and start time), it leaves the hotspot within ctx:
// Devpit's own Wi-Fi entry is deleted first, then the old network is
// reconnected as far as time allows. A record of any other process, or none,
// is left alone; the next open of Share Files undoes a leftover.
func LeaveOnExit(ctx context.Context, stateDir string) (LeaveResult, error) {
	return New(Deps{Wifi: WinWifi{}, StateDir: stateDir}).LeaveIfOwned(ctx)
}

// LeaveIfOwned is [LeaveOnExit] over this session's dependencies.
func (s *Session) LeaveIfOwned(ctx context.Context) (LeaveResult, error) {
	rec, ok, err := s.loadJoin()
	if err != nil || !ok {
		return LeaveResult{}, err
	}
	if !s.ownedBySelf(rec) {
		return LeaveResult{}, nil
	}
	return s.leaveRecord(ctx, rec)
}

// UndoLeftoverJoin is called when Share Files opens: a join record on disk
// means Devpit stopped without leaving, so it leaves now. The result's Note
// is the one line the screen shows. A record owned by another Devpit window
// that is still running is not a leftover: it is left alone, the result says
// InUse, and its Note is [HotspotInUseNote]. A record of this process, of a
// process that has gone, or with no owner at all is undone.
func (s *Session) UndoLeftoverJoin() (LeaveResult, error) {
	rec, ok, err := s.loadJoin()
	if err != nil || !ok {
		return LeaveResult{}, err
	}
	if s.ownedByOtherWindow(rec) {
		return LeaveResult{InUse: true, Note: HotspotInUseNote}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), leaveTimeout)
	defer cancel()
	r, err := s.leaveRecord(ctx, rec)
	if err == nil && r.Left {
		r.Note = strings.TrimSpace("Devpit undid a Wi-Fi join left from last time. " + r.Note)
	}
	return r, err
}

// pid is this process's ID.
func (s *Session) pid() int {
	if s.deps.PID != 0 {
		return s.deps.PID
	}
	return os.Getpid()
}

// processStart reads a process's creation time and whether it still runs.
func (s *Session) processStart(pid int) (time.Time, bool) {
	if s.deps.ProcessStart != nil {
		return s.deps.ProcessStart(pid)
	}
	return winapi.ProcessStart(pid)
}

// processImage reads the program a process runs, "" when Windows will not
// say.
func (s *Session) processImage(pid int) string {
	read := s.deps.ProcessImage
	if read == nil {
		read = winapi.ProcessImage
	}
	img, err := read(pid)
	if err != nil {
		return ""
	}
	return img
}

// executable is this program's path, "" when unknown.
func (s *Session) executable() string {
	read := s.deps.Executable
	if read == nil {
		read = os.Executable
	}
	p, err := read()
	if err != nil {
		return ""
	}
	return p
}

// bootTime is when Windows last started, the zero time when unknown.
func (s *Session) bootTime() time.Time {
	read := s.deps.BootTime
	if read == nil {
		read = winapi.BootTime
	}
	t, err := read()
	if err != nil {
		return time.Time{}
	}
	return t
}

// ownedByOtherWindow reports whether rec was made by a different Devpit
// process that is still running. A record of this process, of a process that
// is gone, of a PID that now belongs to another program (its start time is
// not the one saved, or it runs a program that is not Devpit), written
// before Windows last started, or with no owner is not. Only when none of
// that can be told is the join taken as in use.
func (s *Session) ownedByOtherWindow(rec JoinRecord) bool {
	if rec.PID <= 0 || rec.PID == s.pid() {
		return false
	}
	created, alive := s.processStart(rec.PID)
	if !alive {
		return false
	}
	if !created.IsZero() && !rec.ProcessStart.IsZero() {
		return created.Sub(rec.ProcessStart).Abs() <= ownerSlack
	}
	// The start time cannot be compared (access denied, or the record has
	// none). A record from before the last restart is a leftover whatever
	// runs under that PID now.
	if boot := s.bootTime(); !boot.IsZero() && !rec.Time.IsZero() && rec.Time.Before(boot) {
		return false
	}
	// A PID that runs another program is not a Devpit window.
	if img, self := s.processImage(rec.PID), s.executable(); img != "" && self != "" && !winapi.SameProgram(img, self) {
		return false
	}
	return true // cannot tell: never undo a join that may be in use
}

// ownedBySelf reports whether rec was made by this very process: its PID,
// and its start time when both are known.
func (s *Session) ownedBySelf(rec JoinRecord) bool {
	pid := s.pid()
	if rec.PID <= 0 || rec.PID != pid {
		return false
	}
	created, _ := s.processStart(pid)
	if created.IsZero() || rec.ProcessStart.IsZero() {
		return true
	}
	return created.Sub(rec.ProcessStart).Abs() <= ownerSlack
}

// leaveRecord undoes rec. This PC is no longer meant to be on the hotspot
// after it, whether or not it worked, so the session forgets its hotspot at
// once: a later copy is a normal one. A failed delete keeps the record on
// disk for the next try.
func (s *Session) leaveRecord(ctx context.Context, rec JoinRecord) (LeaveResult, error) {
	s.hotspot = ""
	r := LeaveResult{Left: true}
	if s.deps.Wifi == nil {
		return r, ErrNoWifiCard
	}
	temp := ""
	if rec.Created && rec.TempProfile != "" {
		temp = rec.TempProfile
	}
	prev := rec.Previous
	if prev == rec.Profile {
		prev = ""
	}
	r.Previous = prev
	var err error
	if temp != "" || prev != "" {
		err = s.deps.Wifi.Leave(ctx, rec.Interface, temp, prev)
	}
	switch {
	case err == nil:
		r.Deleted, r.Reconnected = temp != "", prev != ""
		if r.Reconnected {
			r.Note = "This PC is back on " + prev + "."
		}
	case errors.Is(err, winapi.ErrReconnect):
		// The profile is gone; only the reconnect failed. The record has
		// done its job.
		r.Deleted = temp != ""
		if errors.Is(err, winapi.ErrPreviousGone) {
			r.Note = "Devpit removed its Wi-Fi entry. Pick your Wi-Fi network to reconnect."
		} else {
			r.Note = "Devpit removed its Wi-Fi entry, but could not reconnect to " + prev + ". Pick it in the Windows Wi-Fi list."
		}
	default:
		return r, fmt.Errorf("leaving the hotspot: %w", err)
	}
	if err := s.clearJoin(); err != nil {
		return r, err
	}
	return r, nil
}

// ResumeHotspot is what Resume must do before copying a hotspot job.
type ResumeHotspot struct {
	// Rejoin is true when the PC is not on the job's hotspot now.
	Rejoin bool
	SSID   string
	// NeedPassword is true when there is no saved profile for the hotspot:
	// Devpit never stores the hotspot password, so it asks again.
	NeedPassword bool
	// Profile is the saved profile to join with, when there is one.
	Profile string
}

// ResumeNeeds tells Resume how to get back on the job's hotspot. A normal
// job needs nothing.
func (s *Session) ResumeNeeds(j job.Job) (ResumeHotspot, error) {
	if !j.Hotspot() {
		return ResumeHotspot{}, nil
	}
	w, ok := s.WifiCard()
	if !ok {
		return ResumeHotspot{}, ErrNoWifiCard
	}
	r := ResumeHotspot{Rejoin: true, SSID: j.SSID}
	saved, err := s.SavedProfile(w.GUID, j.SSID, "")
	if err != nil {
		return ResumeHotspot{}, err
	}
	if saved != "" && w.Connected && w.Profile == saved {
		return ResumeHotspot{SSID: j.SSID}, nil
	}
	r.Profile, r.NeedPassword = saved, saved == ""
	return r, nil
}

// rejoin puts the PC back on the job's hotspot with the profile the join
// record names, when it has dropped off. It reports whether it reconnected.
//
// A connect to a hotspot that is not there would knock the PC off the
// network it fell back to for as long as the connect waits, so it tries only
// when the hotspot is in the nearby list Windows already has (no scan), or,
// when that list cannot be read, only when the card is on no network at all.
// It tries at most once per [RejoinInterval] by the session's clock, and the
// connect ends with ctx, so stopping the copy is prompt.
func (s *Session) rejoin(ctx context.Context, j job.Job) bool {
	if !j.Hotspot() || s.deps.Wifi == nil {
		return false
	}
	rec, ok, err := s.loadJoin()
	if err != nil || !ok || rec.SSID != j.SSID || rec.Profile == "" {
		return false
	}
	w, found := s.card(rec.Interface)
	if found && w.Connected && w.Profile == rec.Profile {
		return false // still on it: the other PC itself is not answering
	}
	now := s.deps.Now()
	if !s.lastRejoin.IsZero() && now.Sub(s.lastRejoin) < RejoinInterval {
		return false
	}
	visible, verr := s.deps.Wifi.Visible(rec.Interface, rec.SSID)
	switch {
	case verr == nil && !visible:
		return false // not in range yet
	case verr != nil && (!found || w.Connected):
		return false // cannot tell, and the card is on another network
	}
	s.lastRejoin = now
	return s.deps.Wifi.ConnectProfile(ctx, rec.Interface, rec.Profile) == nil
}

// joinLive reports whether this session's join is still in force: it joined
// a hotspot, has not left it, and the record on disk names it.
func (s *Session) joinLive() bool {
	if s.hotspot == "" {
		return false
	}
	rec, ok, err := s.loadJoin()
	return err == nil && ok && rec.SSID == s.hotspot && s.ownedBySelf(rec)
}

func (s *Session) joinPath() string { return filepath.Join(s.deps.StateDir, joinFileName) }

func (s *Session) saveJoin(r JoinRecord) error { return store.WriteJSON(s.joinPath(), r) }

func (s *Session) clearJoin() error { return store.Remove(s.joinPath()) }

// LoadJoin returns the join record, if any.
func (s *Session) LoadJoin() (JoinRecord, bool, error) { return s.loadJoin() }

func (s *Session) loadJoin() (JoinRecord, bool, error) {
	var r JoinRecord
	err := store.ReadJSON(s.joinPath(), &r)
	if errors.Is(err, os.ErrNotExist) {
		return JoinRecord{}, false, nil
	}
	if err != nil {
		return JoinRecord{}, false, err
	}
	return r, true, nil
}
