//go:build windows

package tools

import (
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/windows"
)

// ioReparseTagAppExecLink is IO_REPARSE_TAG_APPEXECLINK, the tag of the app
// execution aliases in %LOCALAPPDATA%\Microsoft\WindowsApps.
const ioReparseTagAppExecLink = 0x8000001B

// reparseHeaderSize is the ReparseTag, ReparseDataLength and Reserved fields
// that start every REPARSE_DATA_BUFFER.
const reparseHeaderSize = 8

// readAppExecLink is the default [WithReadAlias]: the full path of the exe
// the app execution alias at path stands for. It opens the alias itself, not
// what it points to (FILE_FLAG_OPEN_REPARSE_POINT), asks for no read or
// write access, and reads the reparse data with FSCTL_GET_REPARSE_POINT.
// Nothing is started.
func readAppExecLink(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var buf [windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE]byte
	var n uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_GET_REPARSE_POINT, nil, 0,
		&buf[0], windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE, &n, nil); err != nil {
		return "", err
	}
	if n < reparseHeaderSize {
		return "", errNotAppExecLink
	}
	if tag := binary.LittleEndian.Uint32(buf[:]); tag != ioReparseTagAppExecLink {
		return "", fmt.Errorf("%w: reparse tag 0x%08X", errNotAppExecLink, tag)
	}
	size := int(binary.LittleEndian.Uint16(buf[4:]))
	end := min(reparseHeaderSize+size, int(n))
	return parseAppExecLink(buf[reparseHeaderSize:end])
}
