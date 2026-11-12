//go:build !windows

package winapi

func isRemoteDrive(string) bool { return false }
