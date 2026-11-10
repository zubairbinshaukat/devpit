package host

import (
	"context"
	"encoding/base64"
	"net"
	"regexp"
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
)

// The worker's hotspot config words.
const (
	hotspotConfigSaved = "saved"
	hotspotConfigTemp  = "temp"
)

// hotspotBand is the band a temporary hotspot asks for: 5 GHz (decision 3).
const hotspotBand = "5"

// hotspotAdapterName is the card's adapter name when the hotspot's adapter
// cannot be found by its address.
const hotspotAdapterName = "Direct hotspot"

// hsRuleName is the shape of the worker's hotspot rule. A name of any other
// shape is never recorded, because the worker would refuse the whole cleanup
// over it.
var hsRuleName = regexp.MustCompile(`^devpit-hs-[a-z0-9]{4,12}$`)

// hotspotRequest builds the hotspot op for the user's choice. In saved mode
// it carries no name, no password and no band: the worker reads the saved
// settings itself, and the user's own password never reaches it.
func hotspotRequest(choice HotspotChoice, user, hsPass string) elevate.ShareRequest {
	req := elevate.ShareRequest{Op: elevate.ShareOpHotspot, HotspotConfig: hotspotConfigSaved}
	switch choice {
	case HotspotSavedSwitchTo5:
		req.SetBand5 = true
	case HotspotTemp:
		req.HotspotConfig = hotspotConfigTemp
		req.SSID, req.Passphrase, req.Band = HotspotSSID(user), hsPass, hotspotBand
	}
	return req
}

// runHotspot runs the hotspot op. Before it, the manifest says a hotspot is
// about to be started (so a crash at any point leaves a record that covers
// it); as each machine line arrives it is recorded and the manifest saved.
// The returned outcome carries what the worker reported; its saveErr is a
// manifest that could not be written.
func (m *Manager) runHotspot(ctx context.Context, admin Admin, opts Options, user, hsPass string, step func(Step)) (hotspotOutcome, error) {
	req := hotspotRequest(opts.Hotspot, user, hsPass)
	m.update(func(mm *Manifest) {
		mm.HotspotConfig, mm.HotspotStarted = req.HotspotConfig, true
		if req.HotspotConfig == hotspotConfigTemp {
			mm.HotspotSSID = req.SSID
		}
	})
	var o hotspotOutcome
	if err := m.save(); err != nil {
		o.saveErr = err
		return o, err
	}
	hotspotStepShown := false
	showHotspot := func() {
		if !hotspotStepShown {
			hotspotStepShown = true
			step(stepHotspot)
		}
	}
	if req.SetBand5 {
		step(stepBand5)
	} else {
		showHotspot()
	}
	onLine := func(l string) {
		name, v, ok := elevate.ParseHotspotLine(l)
		if !ok {
			// The worker says this line just before the start runs.
			if l == stepHotspot.Label {
				showHotspot()
			}
			return
		}
		changed := false
		switch name {
		case elevate.HotspotLineError:
			o.errToken = true
		case elevate.HotspotLineCapability:
			if n, err := strconv.Atoi(v); err == nil {
				o.capCode, o.haveCap = n, true
			}
		case elevate.HotspotLineWasOn:
			if v == "1" {
				o.wasOn = true
				m.update(func(mm *Manifest) { mm.HotspotStarted = false })
				changed = true
			}
		case elevate.HotspotLineTimeout:
			if v == "1" {
				// Recorded before the worker turns it off: turning it back
				// on is harmless.
				m.update(func(mm *Manifest) { mm.HotspotTimeoutWasOn, mm.HotspotTimeoutChanged = true, true })
				changed = true
			}
		case elevate.HotspotLineMode:
			if v == "legacy" && req.HotspotConfig == hotspotConfigTemp {
				m.update(func(mm *Manifest) { mm.HotspotLegacy = true })
				changed = true
			}
		case elevate.HotspotLineSSID:
			showHotspot()
			if b, err := base64.StdEncoding.DecodeString(v); err == nil && validHotspotName(string(b)) {
				o.ssid = string(b)
				if req.HotspotConfig == hotspotConfigSaved {
					m.update(func(mm *Manifest) { mm.HotspotSSID = o.ssid })
					changed = true
				}
			}
		case elevate.HotspotLineStatus:
			if n, err := strconv.Atoi(v); err == nil {
				o.status, o.haveState = n, true
				if n != int(elevate.TetheringSuccess) {
					// Windows did not start it: nothing to stop.
					m.update(func(mm *Manifest) { mm.HotspotStarted = false })
					changed = true
				}
			}
		case elevate.HotspotLineBand:
			o.band = v
		case elevate.HotspotLineIP:
			o.ip = v
		case elevate.HotspotLineRule:
			if hsRuleName.MatchString(v) {
				o.rule = v
				m.update(func(mm *Manifest) { mm.HotspotRule = v })
				changed = true
			}
			step(stepHotspotFirewall)
		case elevate.HotspotLineOn:
			o.on = v == "on"
		}
		if changed {
			if err := m.save(); err != nil && o.saveErr == nil {
				o.saveErr = err
			}
		}
	}
	err := admin.Share(ctx, req, onLine)
	return o, err
}

// validHotspotName is the check the worker applies to a recorded name: 1 to
// 32 bytes of text with no control character.
func validHotspotName(s string) bool {
	if s == "" || len(s) > 32 || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// hotspotAdapter finds the adapter holding the hotspot's address. The
// hotspot's adapter only exists after the op, so the adapters are read
// again. When none holds it, the address alone is used (index 0: the byte
// counter then reports unknown). ok is false when the op did not bring the
// hotspot up.
func (m *Manager) hotspotAdapter(ctx context.Context, o hotspotOutcome) (Adapter, bool) {
	if o.wasOn || !o.on {
		return Adapter{}, false
	}
	ip := net.ParseIP(o.ip).To4()
	if ip == nil {
		return Adapter{}, false
	}
	if list, err := m.deps.Net.Adapters(ctx); err == nil {
		for _, a := range list {
			if a.IP.Equal(ip) {
				a.Best = false
				return a, true
			}
		}
	}
	return Adapter{Name: hotspotAdapterName, IP: ip}, true
}

// prepareNormalPath opens the normal network too (decision 15), so the other
// PC can fall back to it: only when that network is already Private, with
// the existing firewall op and no extra prompt. It returns the normal
// address for the card, or "" when the path was not prepared. A failure here
// is not a failure of the share: the hotspot still works.
func (m *Manager) prepareNormalPath(ctx context.Context, admin Admin, normal Adapter, step func(Step), recordRules func(string)) string {
	if normal.IP == nil {
		return ""
	}
	cat, err := m.deps.Net.CategoryOf(ctx, normal.Index)
	if err != nil || cat != CategoryPrivate {
		return ""
	}
	step(stepFirewall)
	if err := admin.Share(ctx, elevate.ShareRequest{Op: elevate.ShareOpFirewall}, recordRules); err != nil {
		return ""
	}
	return normal.IP.String()
}

// fillHotspotCard adds the hotspot's name, password, band and WIFI: string.
// In saved mode the password is the user's own, read by the last Suggest,
// and only used when the running name is still the one it read.
func (m *Manager) fillHotspotCard(card *Card, opts Options, user, hsPass string, o hotspotOutcome) {
	card.Mode, card.Band = ModeHotspot, o.band
	if opts.Hotspot == HotspotTemp {
		card.SSID, card.HotspotPassword = HotspotSSID(user), hsPass
		if o.ssid != "" {
			card.SSID = o.ssid
		}
	} else {
		m.mu.Lock()
		saved := m.saved
		m.mu.Unlock()
		card.SSID, card.OwnPassword = o.ssid, true
		if card.SSID == "" {
			card.SSID = saved.ssid
		}
		if saved.ssid != "" && saved.ssid == card.SSID {
			card.HotspotPassword = saved.password
		}
	}
	if card.SSID != "" && card.HotspotPassword != "" {
		card.WifiString = WifiString(card.SSID, card.HotspotPassword)
	}
}
