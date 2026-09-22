package winapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// rmProcessInfo and rmUniqueProcess must be byte-identical to the real
// RM_PROCESS_INFO / RM_UNIQUE_PROCESS structs from RestartManager.h: RmGetList
// writes into the caller's buffer using the OS's idea of the struct's size and
// field offsets, not Go's. A mismatch would not fail loudly -- it would have
// the OS write past the end of (or at the wrong offsets within) a
// Go-allocated slice, corrupting the heap in a way that only crashes later
// and only sometimes, depending on what the corrupted bytes land on. These
// sizes are computed by hand in the type's own doc comment; this test is the
// machine-checked version of that arithmetic.
func TestRmStructSizesMatchTheWin32Definitions(t *testing.T) {
	// RM_UNIQUE_PROCESS: DWORD dwProcessId; FILETIME ProcessStartTime; — both
	// 4-byte-aligned fields, no padding: 4 + 8 = 12 bytes.
	if got, want := unsafe.Sizeof(rmUniqueProcess{}), uintptr(12); got != want {
		t.Errorf("sizeof(rmUniqueProcess) = %d, want %d (sizeof(RM_UNIQUE_PROCESS))", got, want)
	}
	// RM_PROCESS_INFO: RM_UNIQUE_PROCESS Process (12); WCHAR
	// strAppName[CCH_RM_MAX_APP_NAME+1] (256*2=512); WCHAR
	// strServiceShortName[CCH_RM_MAX_SVC_NAME+1] (64*2=128); RM_APP_TYPE
	// ApplicationType (4, C enum); ULONG AppStatus (4); DWORD TSSessionId (4);
	// BOOL bRestartable (4). Every field lands on a 4-byte boundary already,
	// so there is no padding anywhere: 12+512+128+4+4+4+4 = 668 bytes.
	if got, want := unsafe.Sizeof(rmProcessInfo{}), uintptr(668); got != want {
		t.Errorf("sizeof(rmProcessInfo) = %d, want %d (sizeof(RM_PROCESS_INFO))", got, want)
	}
	// The array lengths themselves, since a silent off-by-one here would not
	// change the struct's total size if it were compensated elsewhere, but
	// would still desync every field that follows it.
	if got, want := len(rmProcessInfo{}.AppName), cchRMMaxAppName+1; got != want {
		t.Errorf("len(AppName) = %d, want %d", got, want)
	}
	if got, want := len(rmProcessInfo{}.ServiceShortName), cchRMMaxSvcName+1; got != want {
		t.Errorf("len(ServiceShortName) = %d, want %d", got, want)
	}
}

// The Restart Manager names the process that holds a file open. Devpit uses
// this to say "it's open in Code.exe" instead of "access denied"; here the
// holder is the test binary itself.
func TestLockHoldersNamesTheProcessHoldingAFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "held.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(wide, windows.GENERIC_READ, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("CreateFile with FILE_SHARE_NONE: %v", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()

	holders, err := LockHolders([]string{path})
	if err != nil {
		t.Fatalf("LockHolders: %v", err)
	}
	self := filepath.Base(os.Args[0])
	for _, holder := range holders {
		if strings.EqualFold(holder.Name, self) {
			if holder.PID != uint32(os.Getpid()) {
				t.Errorf("PID = %d, want this process %d", holder.PID, os.Getpid())
			}
			return
		}
	}
	t.Fatalf("holders = %+v, want one named %q", holders, self)
}

// A file nothing has open has no holders, and that is not an error.
func TestLockHoldersReturnsNothingForAFreeFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "free.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	holders, err := LockHolders([]string{path})
	if err != nil {
		t.Fatalf("LockHolders: %v", err)
	}
	if len(holders) != 0 {
		t.Fatalf("holders = %+v, want none", holders)
	}
}

func TestLockHoldersIgnoresAnEmptyList(t *testing.T) {
	t.Parallel()
	holders, err := LockHolders(nil)
	if err != nil || holders != nil {
		t.Fatalf("LockHolders(nil) = %+v, %v", holders, err)
	}
}
