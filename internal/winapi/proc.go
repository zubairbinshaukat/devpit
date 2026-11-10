package winapi

import (
	"strings"
	"time"
)

// ProcessStart reports whether the process with this PID is running and, when
// Windows will say, the time it was created. A PID alone cannot name a
// process: Windows reuses PIDs quickly, so a caller that must know it is the
// same process compares the creation time it saved. created is the zero time
// when the process runs but its start cannot be read (for example access
// denied); the caller then cannot tell, and must not assume it is gone. A PID
// that is not running, and any PID outside Windows, gives alive == false.
func ProcessStart(pid int) (created time.Time, alive bool) { return processStart(pid) }

// ProcessImage returns the full path of the program a running process was
// started from. It asks only for PROCESS_QUERY_LIMITED_INFORMATION, which
// Windows grants for most processes, elevated ones included; a refusal or a
// PID that is not running is an error, and the caller then does not know.
// Other platforms return [ErrUnsupported].
func ProcessImage(pid int) (string, error) { return processImage(pid) }

// BootTime returns when Windows last started, from the milliseconds since
// boot. No process that runs now started before it. Other platforms return
// [ErrUnsupported].
func BootTime() (time.Time, error) { return bootTime() }

// SameProgram reports whether two program paths name the same program by
// their file name, ignoring case and folders: "C:\Tools\devpit.exe" and
// "D:\scoop\shims\DEVPIT.EXE" do. An empty path never matches.
func SameProgram(a, b string) bool {
	// The file name is what follows the last separator; a path that ends in
	// one names no file. Both separators are read on every platform.
	base := func(p string) string {
		p = strings.TrimSpace(p)
		return p[strings.LastIndexAny(p, `\/`)+1:]
	}
	x, y := base(a), base(b)
	return x != "" && strings.EqualFold(x, y)
}
