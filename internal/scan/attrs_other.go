//go:build !windows

package scan

import "io/fs"

// isReparseInfo has no attribute word to read outside Windows, so the
// mode-based check in isLeafMode is the whole story there.
func isReparseInfo(fs.FileInfo) bool { return false }

// isCloudAttr always reports false outside Windows: the placeholder
// attributes are a Windows cloud-files feature.
func isCloudAttr(fs.FileInfo) bool { return false }

// fileIdentity is not implemented outside Windows. Reporting false means the
// sizer counts every file once by path, which is correct for a tree without
// hard links and only over-counts a tree with them.
func fileIdentity(string) (fileID, uint32, bool) { return fileID{}, 0, false }

// longPath is a no-op outside Windows.
func longPath(path string) string { return path }

// isRemoteDrive always reports false outside Windows; there are no mapped
// drive letters to check.
func isRemoteDrive(string) bool { return false }
