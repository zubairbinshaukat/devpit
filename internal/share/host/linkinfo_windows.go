//go:build windows

package host

import "golang.org/x/sys/windows"

// ifTypeIEEE80211 is IF_TYPE_IEEE80211, a Wi-Fi interface.
const ifTypeIEEE80211 = 71

// linkRows reads the GUID, link speed and type of each interface from the IP
// helper (GetIfEntry2Ex). It needs no administrator rights. An interface
// Windows will not describe is left out.
func linkRows(indexes []uint32) map[uint32]linkRow {
	out := make(map[uint32]linkRow, len(indexes))
	for _, i := range indexes {
		row := windows.MibIfRow2{InterfaceIndex: i}
		if windows.GetIfEntry2Ex(windows.MibIfTableNormal, &row) != nil {
			continue
		}
		out[i] = linkRow{
			GUID:     row.InterfaceGuid.String(),
			Bps:      linkSpeed(row.MediaConnectState == 1, row.TransmitLinkSpeed, row.ReceiveLinkSpeed),
			Wireless: row.Type == ifTypeIEEE80211,
		}
	}
	return out
}
