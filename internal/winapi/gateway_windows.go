//go:build windows

package winapi

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// gatewayFlags asks GetAdaptersAddresses for the gateway list and leaves out
// the lists this file does not read. GAA_FLAG_INCLUDE_GATEWAYS (0x80) fills
// IP_ADAPTER_ADDRESSES.FirstGatewayAddress, which golang.org/x/sys/windows
// exposes as IpAdapterAddresses.FirstGatewayAddress.
// Docs: https://learn.microsoft.com/en-us/windows/win32/api/iptypes/ns-iptypes-ip_adapter_addresses_lh
const gatewayFlags = windows.GAA_FLAG_INCLUDE_GATEWAYS | windows.GAA_FLAG_SKIP_UNICAST |
	windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST | windows.GAA_FLAG_SKIP_DNS_SERVER

// interfaceGateway implements [InterfaceGateway]. AdapterName is the adapter's
// GUID as ANSI text, "{XXXXXXXX-...}", the same form as [WifiInterface].GUID.
func interfaceGateway(guid string) (string, error) {
	if guid == "" {
		return "", errors.New("winapi: no interface GUID")
	}
	size := uint32(15 << 10)
	var buf []byte
	var err error
	for range 4 {
		buf = make([]byte, size)
		err = windows.GetAdaptersAddresses(windows.AF_INET, gatewayFlags, 0,
			(*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil || !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			break
		}
	}
	if errors.Is(err, windows.ERROR_NO_DATA) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("GetAdaptersAddresses: %w", err)
	}
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		if !sameGUID(windows.BytePtrToString(a.AdapterName), guid) {
			continue
		}
		for g := a.FirstGatewayAddress; g != nil; g = g.Next {
			ip := g.Address.IP()
			if ip4 := ip.To4(); ip4 != nil && !ip4.IsUnspecified() {
				return ip4.String(), nil
			}
		}
		return "", nil
	}
	return "", nil
}
