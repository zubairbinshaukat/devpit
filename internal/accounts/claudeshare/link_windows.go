//go:build windows

package claudeshare

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// fsPath turns an absolute path into its extended-length form (\\?\C:\...),
// which every file call in this package uses. It lifts the 260-character
// limit and keeps names Win32 would otherwise rewrite, such as one that ends
// in a dot or a space.
func fsPath(p string) string {
	if p == "" || strings.HasPrefix(p, `\\?\`) || !filepath.IsAbs(p) {
		return p
	}
	p = filepath.Clean(p)
	if strings.HasPrefix(p, `\\`) {
		return `\\?\UNC\` + p[2:]
	}
	return `\\?\` + p
}

// isReparse reports whether fi (from os.Lstat or os.ReadDir) is a reparse
// point: a junction, a symbolic link, or anything else Windows redirects.
// The attribute bit is checked as well as Go's mode bits, as in scan.
func isReparse(fi os.FileInfo) bool {
	if fi == nil {
		return false
	}
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return true
	}
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok && d != nil {
		return d.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
	}
	return false
}

// openLink opens p itself, never what it points to, with the given access.
func openLink(p string, access uint32) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(fsPath(p))
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(name, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
}

// reparseOf reads the reparse data of an open handle: its tag and, for a
// junction or a symbolic link, the path it points to.
func reparseOf(h windows.Handle) (uint32, string, error) {
	const size = windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE
	buf := make([]byte, size)
	var n uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_GET_REPARSE_POINT, nil, 0,
		&buf[0], size, &n, nil); err != nil {
		return 0, "", err
	}
	if n < 8 {
		return 0, "", errors.New("the link's data is too short to read")
	}
	tag := binary.LittleEndian.Uint32(buf)
	var start int
	switch tag {
	case windows.IO_REPARSE_TAG_MOUNT_POINT:
		start = 16
	case windows.IO_REPARSE_TAG_SYMLINK:
		start = 20
	default:
		return tag, "", nil
	}
	if int(n) < start {
		return tag, "", errors.New("the link's data is too short to read")
	}
	data := buf[:n]
	sub := utf16At(data, start+int(binary.LittleEndian.Uint16(data[8:])), int(binary.LittleEndian.Uint16(data[10:])))
	if sub == "" {
		sub = utf16At(data, start+int(binary.LittleEndian.Uint16(data[12:])), int(binary.LittleEndian.Uint16(data[14:])))
	}
	return tag, ntToWin32(sub), nil
}

func utf16At(b []byte, off, size int) string {
	if off < 0 || size <= 0 || off+size > len(b) {
		return ""
	}
	u := make([]uint16, size/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[off+2*i:])
	}
	return string(utf16.Decode(u))
}

// ntToWin32 turns \??\C:\x or \??\UNC\server\share into a Win32 path.
func ntToWin32(p string) string {
	p = strings.TrimPrefix(p, `\??\`)
	p = strings.TrimPrefix(p, `\\?\`)
	if rest, ok := strings.CutPrefix(p, `UNC\`); ok {
		return `\\` + rest
	}
	return p
}

// readLink says whether p is a link and where it points, without following
// it.
func readLink(p string) (linkInfo, error) {
	fi, err := os.Lstat(fsPath(p))
	if err != nil {
		return linkInfo{}, err
	}
	if !isReparse(fi) {
		return linkInfo{}, nil
	}
	h, err := openLink(p, 0)
	if err != nil {
		return linkInfo{kind: linkOther}, err
	}
	defer windows.CloseHandle(h) //nolint:errcheck // query-only handle
	tag, target, err := reparseOf(h)
	if err != nil {
		return linkInfo{kind: linkOther}, err
	}
	switch tag {
	case windows.IO_REPARSE_TAG_MOUNT_POINT:
		if strings.HasPrefix(strings.ToLower(target), `volume{`) {
			return linkInfo{kind: linkOther, target: target}, nil
		}
		return linkInfo{kind: linkJunction, target: target}, nil
	case windows.IO_REPARSE_TAG_SYMLINK:
		return linkInfo{kind: linkSymlink, target: target}, nil
	}
	return linkInfo{kind: linkOther}, nil
}

// mountPointBuffer builds the REPARSE_DATA_BUFFER of a junction to target.
func mountPointBuffer(target string) ([]byte, error) {
	sub := utf16.Encode([]rune(`\??\` + target))
	pr := utf16.Encode([]rune(target))
	subBytes, prBytes := len(sub)*2, len(pr)*2
	dataLen := 8 + subBytes + 2 + prBytes + 2
	if 8+dataLen > windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE {
		return nil, fmt.Errorf("the path %s is too long for a link", target)
	}
	buf := make([]byte, 8+dataLen)
	binary.LittleEndian.PutUint32(buf[0:], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:], uint16(dataLen))     //nolint:gosec // bounded above
	binary.LittleEndian.PutUint16(buf[10:], uint16(subBytes))   //nolint:gosec // bounded above
	binary.LittleEndian.PutUint16(buf[12:], uint16(subBytes+2)) //nolint:gosec // bounded above
	binary.LittleEndian.PutUint16(buf[14:], uint16(prBytes))    //nolint:gosec // bounded above
	off := 16
	for _, u := range sub {
		binary.LittleEndian.PutUint16(buf[off:], u)
		off += 2
	}
	off += 2
	for _, u := range pr {
		binary.LittleEndian.PutUint16(buf[off:], u)
		off += 2
	}
	return buf, nil
}

// createJunction makes link a junction to target. It needs no admin rights
// and no Developer Mode. link must not exist; target must be a full local
// path. If anything fails, the empty folder it made is removed again.
func createJunction(link, target string) error {
	if !filepath.IsAbs(target) || strings.HasPrefix(target, `\\`) {
		return fmt.Errorf("a link needs a full local path, not %s", target)
	}
	target = filepath.Clean(strings.TrimPrefix(target, `\\?\`))
	buf, err := mountPointBuffer(target)
	if err != nil {
		return err
	}
	if err = os.Mkdir(fsPath(link), 0o700); err != nil {
		return err
	}
	h, err := openLink(link, windows.GENERIC_WRITE)
	if err != nil {
		_ = removeEmptyDir(link)
		return fmt.Errorf("opening %s to make it a link: %w", link, err)
	}
	var n uint32
	err = windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &n, nil) //nolint:gosec // bounded by MAXIMUM_REPARSE_DATA_BUFFER_SIZE
	_ = windows.CloseHandle(h)
	if err != nil {
		_ = removeEmptyDir(link)
		return fmt.Errorf("making %s a link: %w", link, err)
	}
	return nil
}

func removeEmptyDir(p string) error {
	name, err := windows.UTF16PtrFromString(fsPath(p))
	if err != nil {
		return err
	}
	return windows.RemoveDirectory(name)
}

// removeLink removes the link at p and nothing else. It opens p itself
// (never what it points to), checks on that same handle that it is a
// reparse point, and, when want is set, that it is a junction to want, and
// only then marks that handle's object for deletion. Deleting a reparse
// point never touches what it points to, and the check and the delete act
// on one open object, so nothing can be swapped in between.
func removeLink(p, want string) error {
	h, err := openLink(p, windows.DELETE|windows.FILE_READ_ATTRIBUTES)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = windows.CloseHandle(h)
		}
	}()
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return &NotALinkError{Path: p}
	}
	tag, target, err := reparseOf(h)
	if err != nil {
		return err
	}
	if want != "" {
		if tag != windows.IO_REPARSE_TAG_MOUNT_POINT {
			return fmt.Errorf("%s is a link Devpit did not make (not a junction); it was left alone", p)
		}
		if !samePath(target, want) {
			return fmt.Errorf("%s now points to %s, not %s; it was left alone", p, target, want)
		}
	}
	del := byte(1) // FILE_DISPOSITION_INFO{DeleteFile: TRUE}
	if err := windows.SetFileInformationByHandle(h, windows.FileDispositionInfo, &del, 1); err != nil {
		return fmt.Errorf("removing the link %s: %w", p, err)
	}
	closed = true
	return windows.CloseHandle(h)
}

// renameNoReplace renames from to to, and fails if to exists: a rename here
// never replaces anything.
func renameNoReplace(from, to string) error {
	f, err := windows.UTF16PtrFromString(fsPath(from))
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(fsPath(to))
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(f, t, 0); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}

// replaceFile renames from over to (for atomic saves of files Devpit edits).
func replaceFile(from, to string) error {
	f, err := windows.UTF16PtrFromString(fsPath(from))
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(fsPath(to))
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(f, t, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}

// inUse reports whether err means another program has the file or folder
// open (a running Claude Code session, an editor, a virus scanner).
func inUse(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

// volumeOf describes the volume p is on, after junctions and subst drives.
func volumeOf(p string) (volInfo, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return volInfo{}, err
	}
	const size = 1024
	buf := make([]uint16, size)
	if err = windows.GetVolumePathName(name, &buf[0], size); err != nil {
		return volInfo{}, err
	}
	root := windows.UTF16ToString(buf)
	rootp, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return volInfo{}, err
	}
	var serial, maxComp, flags uint32
	fsName := make([]uint16, 64)
	if err := windows.GetVolumeInformation(rootp, nil, 0, &serial, &maxComp, &flags, &fsName[0], uint32(len(fsName))); err != nil { //nolint:gosec // small constant
		return volInfo{}, err
	}
	return volInfo{
		root:     root,
		serial:   serial,
		fs:       windows.UTF16ToString(fsName),
		reparse:  flags&windows.FILE_SUPPORTS_REPARSE_POINTS != 0,
		remote:   windows.GetDriveType(rootp) == windows.DRIVE_REMOTE,
		supports: true,
	}, nil
}
