//go:build windows

package adapters

import (
	"strings"

	"golang.org/x/sys/windows"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// wranglerTrueCase returns folder normalized with each existing component
// in its real on-disk letter case, as the file system reports it. Unlike
// accounts.RealPath it does not follow junctions or subst drives: Wrangler
// compares the path the terminal is in, as Node's path.resolve gives it.
// Components that do not exist yet keep the case they were typed in.
func wranglerTrueCase(folder string) string {
	norm, err := accounts.NormalizeFolder(folder, "")
	if err != nil {
		return folder
	}
	vol := accounts.FolderVolume(norm)
	rest := strings.TrimPrefix(strings.TrimPrefix(norm, strings.TrimSuffix(vol, `\`)), `\`)
	if rest == "" {
		return norm
	}
	parts := strings.Split(rest, `\`)
	cur := strings.TrimSuffix(vol, `\`)
	for i, part := range parts {
		next := cur + `\` + part
		name, ok := onDiskName(next)
		if !ok {
			return cur + `\` + strings.Join(parts[i:], `\`)
		}
		cur = cur + `\` + name
	}
	return cur
}

// onDiskName is the real name of the last component of path.
func onDiskName(path string) (string, bool) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	var fd windows.Win32finddata
	h, err := windows.FindFirstFile(p, &fd)
	if err != nil {
		return "", false
	}
	_ = windows.FindClose(h)
	name := windows.UTF16ToString(fd.FileName[:])
	if name == "" || name == "." || name == ".." {
		return "", false
	}
	return name, true
}
