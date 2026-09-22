package scan

import (
	"io/fs"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// isReparseInfo reports the FILE_ATTRIBUTE_REPARSE_POINT bit straight from
// the directory listing. It is the second guard behind isLeafMode: the mode
// test covers what Go classifies, this covers what Windows recorded.
func isReparseInfo(fi fs.FileInfo) bool {
	return attributesOf(fi)&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// isCloudAttr reports the two attributes OneDrive, Dropbox and Google Drive
// set on a placeholder. RECALL_ON_DATA_ACCESS means the file is dehydrated;
// RECALL_ON_OPEN means even opening it starts a download.
func isCloudAttr(fi fs.FileInfo) bool {
	const mask = windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS |
		windows.FILE_ATTRIBUTE_RECALL_ON_OPEN
	return attributesOf(fi)&mask != 0
}

// attributesOf pulls the Win32 attribute word out of a FileInfo produced by
// os.ReadDir or os.Lstat. It returns zero for anything else, so a caller on a
// synthetic FileInfo degrades to the mode-based checks rather than panicking.
func attributesOf(fi fs.FileInfo) uint32 {
	if fi == nil {
		return 0
	}
	if data, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok && data != nil {
		return data.FileAttributes
	}
	if found, ok := fi.Sys().(*syscall.Win32finddata); ok && found != nil {
		return found.FileAttributes
	}
	return 0
}

// fileIdentity returns a value that is equal for two paths that are hard
// links to the same data, plus the number of links that data has. The second
// return value is false when the identity could not be obtained, which is the
// normal outcome for a file another process holds exclusively.
func fileIdentity(path string) (identity fileID, links uint32, ok bool) {
	p, err := windows.UTF16PtrFromString(longPath(path))
	if err != nil {
		return fileID{}, 0, false
	}
	h, err := windows.CreateFile(
		p,
		0, // no access rights: metadata only.
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return fileID{}, 0, false
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return fileID{}, 0, false
	}
	return fileID{
		volume: uint64(info.VolumeSerialNumber),
		index:  uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow),
	}, info.NumberOfLinks, true
}

// longPath prefixes an absolute path with \\?\ so calls that do not go
// through the standard library still work past 260 characters.
func longPath(path string) string {
	if len(path) < 248 || len(path) >= 4 && path[:4] == `\\?\` {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if pathIsUNC(abs) {
		return `\\?\UNC` + abs[1:]
	}
	return `\\?\` + abs
}

// isRemoteDrive reports whether path sits on a mapped network drive. UNC
// paths are caught earlier, by pathIsUNC.
func isRemoteDrive(path string) bool {
	vol := filepath.VolumeName(normalizePath(path))
	if vol == "" || len(vol) != 2 {
		return false
	}
	root, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return false
	}
	return windows.GetDriveType(root) == windows.DRIVE_REMOTE
}
