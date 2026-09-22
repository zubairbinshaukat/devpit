package winapi

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Restart Manager limits from RestartManager.h.
const (
	cchRMSessionKey  = 32  // CCH_RM_SESSION_KEY
	cchRMMaxAppName  = 255 // CCH_RM_MAX_APP_NAME
	cchRMMaxSvcName  = 63  // CCH_RM_MAX_SVC_NAME
	rmMaxGetListPass = 4   // give up after this many resize attempts
)

// maxRMProcInfo caps how many holders are asked for in one go. Restart Manager
// rarely reports more than a handful and an unbounded allocation from a value
// the API hands back is not something Devpit wants to trust.
const maxRMProcInfo = 256

// rmUniqueProcess mirrors RM_UNIQUE_PROCESS.
type rmUniqueProcess struct {
	ProcessID        uint32
	ProcessStartTime windows.Filetime
}

// rmProcessInfo mirrors RM_PROCESS_INFO.
type rmProcessInfo struct {
	Process          rmUniqueProcess
	AppName          [cchRMMaxAppName + 1]uint16
	ServiceShortName [cchRMMaxSvcName + 1]uint16
	ApplicationType  uint32
	AppStatus        uint32
	TSSessionID      uint32
	Restartable      int32
}

// rmProcs bundles the lazily resolved rstrtmgr.dll entry points. It is built
// per call rather than kept in a package-level variable so the package keeps
// its no-globals rule; LazyDLL itself caches the module handle.
type rmProcs struct {
	start    *windows.LazyProc
	register *windows.LazyProc
	getList  *windows.LazyProc
	end      *windows.LazyProc
}

func loadRestartManager() (rmProcs, error) {
	dll := windows.NewLazySystemDLL("rstrtmgr.dll")
	if err := dll.Load(); err != nil {
		return rmProcs{}, fmt.Errorf("winapi: load rstrtmgr.dll: %w", err)
	}
	p := rmProcs{
		start:    dll.NewProc("RmStartSession"),
		register: dll.NewProc("RmRegisterResources"),
		getList:  dll.NewProc("RmGetList"),
		end:      dll.NewProc("RmEndSession"),
	}
	for _, proc := range []*windows.LazyProc{p.start, p.register, p.getList, p.end} {
		if err := proc.Find(); err != nil {
			return rmProcs{}, fmt.Errorf("winapi: rstrtmgr.dll is missing %s: %w", proc.Name, err)
		}
	}
	return p, nil
}

// rmError turns a Restart Manager return code into an error. These functions
// return a Win32 error code directly instead of setting the last-error value.
func rmError(op string, ret uintptr) error {
	if ret == 0 {
		return nil
	}
	return fmt.Errorf("winapi: %s: %w", op, syscall.Errno(ret)) //nolint:errorlint // ret is the error code, not a wrapped error
}

// LockHolders asks the Restart Manager which processes hold open handles to
// the given files. An empty result means nothing was found, which is not an
// error: the Restart Manager only knows about handles it can attribute, and a
// file may be locked by something it cannot name.
//
// The caller is responsible for keeping the list short; every path is
// registered with the session.
func LockHolders(paths []string) ([]LockHolder, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	procs, err := loadRestartManager()
	if err != nil {
		return nil, err
	}

	var session uint32
	// RmStartSession wants a buffer of CCH_RM_SESSION_KEY + 1 wide characters.
	key := make([]uint16, cchRMSessionKey+1)
	ret, _, _ := procs.start.Call(
		uintptr(unsafe.Pointer(&session)),
		0,
		uintptr(unsafe.Pointer(&key[0])),
	)
	if startErr := rmError("RmStartSession", ret); startErr != nil {
		return nil, startErr
	}
	defer func() { _, _, _ = procs.end.Call(uintptr(session)) }()

	names := make([]*uint16, 0, len(paths))
	for _, p := range paths {
		wide, convErr := windows.UTF16PtrFromString(p)
		if convErr != nil {
			continue
		}
		names = append(names, wide)
	}
	if len(names) == 0 {
		return nil, nil
	}

	ret, _, _ = procs.register.Call(
		uintptr(session),
		uintptr(len(names)),
		uintptr(unsafe.Pointer(&names[0])),
		0, 0,
		0, 0,
	)
	if regErr := rmError("RmRegisterResources", ret); regErr != nil {
		return nil, regErr
	}

	infos, listErr := rmGetList(procs, session)
	if listErr != nil {
		return nil, listErr
	}

	holders := make([]LockHolder, 0, len(infos))
	for i := range infos {
		holders = append(holders, LockHolder{
			PID:  infos[i].Process.ProcessID,
			Name: windows.UTF16ToString(infos[i].AppName[:]),
		})
	}
	return holders, nil
}

// rmGetList calls RmGetList, growing the buffer while the API says it needs
// more room.
func rmGetList(procs rmProcs, session uint32) ([]rmProcessInfo, error) {
	var (
		needed uint32
		have   uint32
		size   uint32
		reason uint32
		buf    []rmProcessInfo
	)
	for pass := 0; pass < rmMaxGetListPass; pass++ {
		have = size
		var first unsafe.Pointer
		if len(buf) > 0 {
			first = unsafe.Pointer(&buf[0])
		}
		ret, _, _ := procs.getList.Call(
			uintptr(session),
			uintptr(unsafe.Pointer(&needed)),
			uintptr(unsafe.Pointer(&have)),
			uintptr(first),
			uintptr(unsafe.Pointer(&reason)),
		)
		switch syscall.Errno(ret) { //nolint:errorlint // ret is a raw Win32 code
		case 0:
			return buf[:have], nil
		case windows.ERROR_MORE_DATA:
			if needed == 0 {
				return nil, nil
			}
			if needed > maxRMProcInfo {
				needed = maxRMProcInfo
			}
			size = needed
			buf = make([]rmProcessInfo, size)
		default:
			return nil, rmError("RmGetList", ret)
		}
	}
	return nil, fmt.Errorf("winapi: RmGetList kept asking for more room after %d passes", rmMaxGetListPass)
}
