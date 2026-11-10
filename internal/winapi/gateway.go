package winapi

import "strings"

// InterfaceGateway returns the IPv4 default gateway of the network adapter
// whose interface GUID is guid, in the text form "192.168.137.1". guid has the
// form of [WifiInterface].GUID, "{XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX}"; case
// and the braces do not matter.
//
// It returns "" and nil when the adapter is there but has no IPv4 gateway (for
// example while the address is still being handed out), and "" and nil when
// no adapter has that GUID. It only reads: it never changes an address, a
// route or a setting. Other platforms return [ErrUnsupported].
func InterfaceGateway(guid string) (string, error) { return interfaceGateway(guid) }

// sameGUID compares two interface GUID texts, ignoring case and braces.
func sameGUID(a, b string) bool {
	trim := func(s string) string { return strings.Trim(strings.TrimSpace(s), "{}") }
	a, b = trim(a), trim(b)
	return a != "" && strings.EqualFold(a, b)
}
