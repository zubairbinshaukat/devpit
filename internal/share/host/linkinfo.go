package host

import (
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// linkRow is what the IP helper says about one interface.
type linkRow struct {
	// GUID is the interface GUID as text.
	GUID string
	// Bps is the negotiated link speed, 0 when unknown or not connected.
	Bps uint64
	// Wireless is true for interface type 71 (IF_TYPE_IEEE80211).
	Wireless bool
}

// linkSpeed turns the IP helper's two link speeds into one: the faster, and 0
// for the "unknown" value (all ones) or a link that is not connected. Nothing
// is guessed.
func linkSpeed(connected bool, tx, rx uint64) uint64 {
	if !connected {
		return 0
	}
	s := max(tx, rx)
	if s == ^uint64(0) {
		return 0
	}
	return s
}

// annotate fills in each adapter's GUID, link speed, Wi-Fi flag and Wi-Fi
// standard from the IP helper rows (by interface index) and the Wi-Fi cards
// (by GUID). Anything missing stays zero, which means unknown.
func annotate(list []Adapter, rows map[uint32]linkRow, wifi []winapi.WifiInterface) []Adapter {
	for i := range list {
		r, ok := rows[list[i].Index]
		if !ok {
			continue
		}
		list[i].GUID, list[i].LinkBps, list[i].Wireless = r.GUID, r.Bps, r.Wireless
		if w, ok := wifiByGUID(wifi, r.GUID); ok {
			list[i].Wireless = true
			list[i].Standard = winapi.WifiStandard(w.PHYs)
		}
	}
	return list
}

// wifiByGUID finds the Wi-Fi card with the given interface GUID.
func wifiByGUID(wifi []winapi.WifiInterface, guid string) (winapi.WifiInterface, bool) {
	if guid == "" {
		return winapi.WifiInterface{}, false
	}
	for _, w := range wifi {
		if strings.EqualFold(w.GUID, guid) {
			return w, true
		}
	}
	return winapi.WifiInterface{}, false
}
