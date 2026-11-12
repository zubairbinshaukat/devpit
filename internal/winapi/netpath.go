package winapi

import "strings"

// IsNetworkDrive reports whether path is a UNC path (\\server\share, also in
// its \\?\UNC\ form) or lives on a drive Windows reports as DRIVE_REMOTE (a
// mapped network drive). The UNC test is a plain string test, so it holds on
// every platform; the drive test is Windows only and false elsewhere.
//
// clean/sys_windows.go and scan/attrs_windows.go keep their own copies of the
// drive test; they belong to other packages and are not refactored here.
func IsNetworkDrive(path string) bool {
	if isUNCPath(path) {
		return true
	}
	return isRemoteDrive(path)
}

func isUNCPath(p string) bool {
	p = strings.ReplaceAll(p, "/", `\`)
	if strings.HasPrefix(p, `\\?\`) || strings.HasPrefix(p, `\\.\`) {
		return strings.HasPrefix(strings.ToUpper(p[4:]), `UNC\`)
	}
	return strings.HasPrefix(p, `\\`)
}
