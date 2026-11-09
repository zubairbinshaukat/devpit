//go:build windows

package sysinfo

import (
	"errors"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// tbs.dll for the TPM.
var (
	modTbs                = windows.NewLazySystemDLL("tbs.dll")
	procTbsiGetDeviceInfo = modTbs.NewProc("Tbsi_GetDeviceInfo")
)

// tpmDeviceInfo is TPM_DEVICE_INFO (16 bytes).
type tpmDeviceInfo struct {
	StructVersion, TPMVersion, InterfaceType, ImpRevision uint32
}

// windowsNative reads everything about Windows that needs no PowerShell.
func windowsNative() Windows {
	var w Windows
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE); err == nil {
		str := func(n string) string { v, _, _ := k.GetStringValue(n); return strings.TrimSpace(v) }
		w.Edition = str("EditionID")
		w.DisplayVersion = str("DisplayVersion")
		if w.DisplayVersion == "" {
			w.DisplayVersion = str("ReleaseId")
		}
		w.Build = atoiOr(str("CurrentBuild"), 0)
		if v, _, err := k.GetIntegerValue("UBR"); err == nil && v < 1<<20 {
			w.UBR = int(v)
		}
		if v, _, err := k.GetIntegerValue("InstallDate"); err == nil && v > 0 && v < 1<<40 {
			w.Installed = time.Unix(int64(v), 0)
		}
		_ = k.Close()
	}
	if v := windows.RtlGetVersion(); v != nil && v.BuildNumber > 0 {
		w.Build = int(v.BuildNumber)
	}
	w.Name = windowsName(w.Build)

	w.OEMKeyInFirmware = msdmPresent()
	w.SecureBoot = secureBoot()
	w.TPM, w.TPMPresent = tpmVersion()
	w.Domain, w.DomainJoined = domainJoin()
	w.EntraJoined = hasSubkeys(`SYSTEM\CurrentControlSet\Control\CloudDomainJoin\JoinInfo`)
	w.MDM = mdmEnrolled()
	w.Autopilot, w.AutopilotTenant = autopilot()
	return w
}

// windowsName is the product by build: ProductName still says "Windows 10"
// on Windows 11.
func windowsName(build int) string {
	switch {
	case build >= 22000:
		return "Windows 11"
	case build >= 10240:
		return "Windows 10"
	case build > 0:
		return "Windows"
	}
	return ""
}

// secureBoot reads UEFISecureBootEnabled. A missing key is a legacy BIOS
// PC, where Secure Boot cannot be on.
func secureBoot() Tri {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\SecureBoot\State`, registry.QUERY_VALUE)
	if err != nil {
		if firmwareType() == "Legacy BIOS" {
			return TriNo
		}
		return TriUnknown
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("UEFISecureBootEnabled")
	if err != nil {
		return TriUnknown
	}
	return TriOf(v == 1)
}

// tpmVersion asks TBS for the TPM. TBS_E_TPM_NOT_FOUND is no TPM, or one
// turned off in the firmware.
func tpmVersion() (string, Tri) {
	if procTbsiGetDeviceInfo.Find() != nil {
		return "", TriUnknown
	}
	info := tpmDeviceInfo{StructVersion: 1}
	r, _, _ := procTbsiGetDeviceInfo.Call(unsafe.Sizeof(info), uintptr(unsafe.Pointer(&info)))
	switch {
	case r == 0x8028400F: // TBS_E_TPM_NOT_FOUND
		return "", TriNo
	case r != 0:
		return "", TriUnknown
	case info.TPMVersion == 2:
		return "2.0", TriYes
	case info.TPMVersion == 1:
		return "1.2", TriYes
	}
	return "", TriYes
}

// domainJoin reads NetGetJoinInformation.
func domainJoin() (string, Tri) {
	var name *uint16
	var kind uint32
	if err := windows.NetGetJoinInformation(nil, &name, &kind); err != nil {
		return "", TriUnknown
	}
	defer func() { _ = windows.NetApiBufferFree((*byte)(unsafe.Pointer(name))) }()
	if kind == windows.NetSetupDomainName {
		return windows.UTF16PtrToString(name), TriYes
	}
	return "", TriNo
}

// hasSubkeys says whether a key exists and has anything under it.
func hasSubkeys(path string) Tri {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return TriNo
		}
		return TriUnknown
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(16)
	if err != nil && len(names) == 0 {
		return TriNo
	}
	return TriOf(len(names) > 0)
}

// mdmEnrolled looks for an MDM enrolment ("MS DM Server").
func mdmEnrolled() Tri {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Enrollments`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return TriNo
		}
		return TriUnknown
	}
	defer k.Close()
	names, _ := k.ReadSubKeyNames(256)
	for _, n := range names {
		sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		id, _, _ := sk.GetStringValue("ProviderID")
		_ = sk.Close()
		if strings.EqualFold(strings.TrimSpace(id), "MS DM Server") {
			return TriYes
		}
	}
	return TriNo
}

// autopilot reads the Autopilot profile Windows cached. Registration itself
// lives in the cloud; a PC with nothing cached may still be registered.
func autopilot() (Tri, string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Provisioning\Diagnostics\Autopilot`, registry.QUERY_VALUE)
	if err != nil {
		return TriUnknown, ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("CloudAssignedTenantDomain")
	v = strings.TrimSpace(v)
	if v == "" {
		return TriUnknown, ""
	}
	return TriYes, v
}
