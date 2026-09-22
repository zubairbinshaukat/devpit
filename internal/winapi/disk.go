// Package winapi holds thin, dependency-free wrappers around the handful of
// Win32 calls Devpit needs. Every syscall lives in a _windows.go file and has
// a matching _other.go stub so the whole module still vets and tests on Linux.
package winapi

// DiskSpace describes the free and total bytes of a volume.
type DiskSpace struct {
	// FreeBytes is the space available to the calling user.
	FreeBytes uint64
	// TotalBytes is the size of the volume.
	TotalBytes uint64
}

// UsedBytes returns the number of bytes in use on the volume.
func (d DiskSpace) UsedBytes() uint64 {
	if d.TotalBytes < d.FreeBytes {
		return 0
	}
	return d.TotalBytes - d.FreeBytes
}
