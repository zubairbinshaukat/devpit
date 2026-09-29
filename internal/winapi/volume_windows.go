//go:build windows

package winapi

import "golang.org/x/sys/windows"

// fileSystem implements [FileSystem]: find the volume's root with
// GetVolumePathName, then ask GetVolumeInformation for its file system name.
func fileSystem(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	root := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(p, &root[0], uint32(len(root))); err != nil { //nolint:gosec // the buffer is MAX_PATH+1
		return "", err
	}
	name := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumeInformation(&root[0], nil, 0, nil, nil, nil, &name[0], uint32(len(name))); err != nil { //nolint:gosec // the buffer is MAX_PATH+1
		return "", err
	}
	return windows.UTF16ToString(name), nil
}
