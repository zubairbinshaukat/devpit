package winapi

import (
	"os"

	"golang.org/x/sys/windows"
)

// SystemDrive returns the root of the drive Windows is installed on, for
// example "C:\". It falls back to "C:\" when the environment is unset.
func SystemDrive() string {
	if d := os.Getenv("SystemDrive"); d != "" {
		return d + `\`
	}
	return `C:\`
}

// FreeSpace reports the free and total bytes of the volume containing path.
// It calls GetDiskFreeSpaceExW; it never shells out and never touches the
// network.
func FreeSpace(path string) (DiskSpace, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return DiskSpace{}, err
	}
	var freeToCaller, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeToCaller, &total, &totalFree); err != nil {
		return DiskSpace{}, err
	}
	return DiskSpace{FreeBytes: freeToCaller, TotalBytes: total}, nil
}
