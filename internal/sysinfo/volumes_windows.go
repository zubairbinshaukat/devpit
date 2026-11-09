//go:build windows

package sysinfo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// volumesLive lists the fixed and removable drive letters.
func volumesLive() ([]Volume, error) {
	var buf [512]uint16
	n, err := windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
	if err != nil {
		return nil, err
	}
	winDrive := strings.ToUpper(filepath.VolumeName(os.Getenv("SystemRoot")))
	var out []Volume
	for _, root := range splitMulti(buf[:min(int(n), len(buf))]) {
		p, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		kind := windows.GetDriveType(p)
		if kind != windows.DRIVE_FIXED && kind != windows.DRIVE_REMOVABLE {
			continue
		}
		v := Volume{Letter: strings.ToUpper(root[:1]), Removable: kind == windows.DRIVE_REMOVABLE}
		v.WindowsIsHere = winDrive != "" && strings.HasPrefix(winDrive, v.Letter)
		var label, fs [64]uint16
		if windows.GetVolumeInformation(p, &label[0], uint32(len(label)), nil, nil, nil, &fs[0], uint32(len(fs))) == nil {
			v.Label = windows.UTF16ToString(label[:])
			v.FileSystem = windows.UTF16ToString(fs[:])
		} else if v.Removable {
			continue // no card in the reader
		}
		var free, total, totalFree uint64
		if windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree) == nil {
			v.FreeB, v.TotalB = free, total
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no drive letters")
	}
	return out, nil
}

// splitMulti splits a double-NUL-terminated UTF-16 list.
func splitMulti(u []uint16) []string {
	var out []string
	start := 0
	for i, c := range u {
		if c == 0 {
			if i > start {
				out = append(out, windows.UTF16ToString(u[start:i]))
			}
			start = i + 1
		}
	}
	return out
}

// networkLive lists the physical adapters: hardware interfaces only, so
// VPNs, Hyper-V switches and loopback are left out.
func networkLive() ([]Adapter, error) {
	size := uint32(15 << 10)
	var buf []byte
	for range 4 {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_SKIP_UNICAST|windows.GAA_FLAG_SKIP_ANYCAST|
			windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER, 0,
			(*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			return nil, err
		}
	}
	var out []Adapter
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		row := windows.MibIfRow2{InterfaceIndex: a.IfIndex}
		if windows.GetIfEntry2Ex(windows.MibIfTableNormal, &row) != nil {
			continue
		}
		if row.InterfaceAndOperStatusFlags&0x01 == 0 { // HardwareInterface
			continue
		}
		ad := Adapter{Description: windows.UTF16ToString(row.Description[:])}
		switch row.Type {
		case 71:
			ad.Kind = "Wi-Fi"
		case 6:
			ad.Kind = "Ethernet"
		default:
			ad.Kind = "Other"
		}
		ad.Connected = row.MediaConnectState == 1
		if ad.Connected {
			ad.SpeedBps = max(row.TransmitLinkSpeed, row.ReceiveLinkSpeed)
			if ad.SpeedBps == ^uint64(0) {
				ad.SpeedBps = 0
			}
		}
		if l := int(row.PhysicalAddressLength); l > 0 && l <= len(row.PhysicalAddress) {
			parts := make([]string, l)
			for i := range l {
				parts[i] = fmt.Sprintf("%02X", row.PhysicalAddress[i])
			}
			ad.MAC = strings.Join(parts, "-")
		}
		out = append(out, ad)
	}
	return out, nil
}
