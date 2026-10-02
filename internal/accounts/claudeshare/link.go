package claudeshare

import (
	"fmt"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

type linkKind int

const (
	linkNone linkKind = iota
	linkJunction
	linkSymlink
	linkOther
)

// linkInfo is what readLink found at a path.
type linkInfo struct {
	kind   linkKind
	target string
}

func (l linkInfo) isLink() bool { return l.kind != linkNone }

// volInfo describes the volume a folder is on.
type volInfo struct {
	root     string
	serial   uint32
	fs       string
	reparse  bool
	remote   bool
	supports bool // the facts were read (false on other systems)
}

// NotALinkError is returned when Devpit was about to remove a link and found
// a real file or folder: it removes nothing.
type NotALinkError struct{ Path string }

func (e *NotALinkError) Error() string {
	return fmt.Sprintf("%s is a real folder, not a link; Devpit removes only links it made, so it left it alone", e.Path)
}

// InUseError is a rename or a link that failed because another program has
// the folder open.
type InUseError struct {
	Path string
	Err  error
}

func (e *InUseError) Error() string {
	return fmt.Sprintf("%s is in use, most likely by a running Claude Code session. Close Claude Code in this account and try again; nothing was changed", e.Path)
}

func (e *InUseError) Unwrap() error { return e.Err }

// samePath reports whether two Windows paths name the same folder.
func samePath(a, b string) bool {
	if accounts.SameFolder(a, b) {
		return true
	}
	ra, err1 := accounts.RealPath(a)
	rb, err2 := accounts.RealPath(b)
	return err1 == nil && err2 == nil && accounts.SameFolder(ra, rb)
}

// linkVerdict decides, from facts already gathered, whether a junction at
// dst (in the account) to src (in the default home) is safe to make. It is
// pure so every reason is tested on every OS.
func linkVerdict(src, dst, srcReal, dstReal string, sv, dv volInfo, oneDrive []string) (bool, string) {
	if accounts.IsNetworkPath(src) || accounts.IsNetworkPath(dst) || sv.remote || dv.remote {
		return false, "one of the folders is on a network drive, and links only work on this PC's own drives"
	}
	if !sv.supports || !dv.supports {
		return false, "links are made only on Windows"
	}
	for _, od := range oneDrive {
		if od == "" {
			continue
		}
		if accounts.FolderContains(od, src) || accounts.FolderContains(od, dst) ||
			accounts.FolderContains(od, srcReal) || accounts.FolderContains(od, dstReal) {
			return false, "one of the folders is inside OneDrive (" + od + "), which does not keep links"
		}
	}
	if !strings.EqualFold(accounts.FolderVolume(src), accounts.FolderVolume(srcReal)) ||
		!strings.EqualFold(accounts.FolderVolume(dst), accounts.FolderVolume(dstReal)) {
		return false, "one of the folders reaches another drive through a subst drive or a link, and a link to it could break"
	}
	if sv.serial != dv.serial || !strings.EqualFold(sv.root, dv.root) {
		return false, "the default account and this account are on different drives, and a link would break whenever that drive is not there"
	}
	if !dv.reparse {
		fs := dv.fs
		if fs == "" {
			fs = "its file system"
		}
		return false, "the drive " + dv.root + " uses " + fs + ", which cannot hold links (FAT or exFAT)"
	}
	return true, ""
}

// linkable says whether links from the account folder into the default home
// can be made, and why not. src and dst are folders that should exist; the
// nearest existing parent is used when they do not.
func (r Roots) linkable(src, dst string) (bool, string) {
	if r.probe != nil {
		return r.probe(src, dst)
	}
	srcReal, err := accounts.RealPath(src)
	if err != nil {
		return false, "Devpit could not check " + src + ": " + err.Error()
	}
	dstReal, err := accounts.RealPath(dst)
	if err != nil {
		return false, "Devpit could not check " + dst + ": " + err.Error()
	}
	sv, err := volumeOf(nearestExisting(srcReal))
	if err != nil {
		return false, "Devpit could not check the drive of " + src + ": " + err.Error()
	}
	dv, err := volumeOf(nearestExisting(dstReal))
	if err != nil {
		return false, "Devpit could not check the drive of " + dst + ": " + err.Error()
	}
	var od []string
	for _, k := range []string{"OneDrive", "OneDriveConsumer", "OneDriveCommercial"} {
		if v := r.env(k); v != "" && !accounts.IsDriveRoot(v) {
			od = append(od, v)
		}
	}
	return linkVerdict(src, dst, srcReal, dstReal, sv, dv, od)
}
