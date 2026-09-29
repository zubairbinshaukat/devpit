//go:build windows

package winapi

import (
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

// SetThreadExecutionState flags, from winbase.h.
const (
	// esContinuous keeps the setting until it is cleared, instead of
	// resetting the idle timer once.
	esContinuous = 0x80000000
	// esSystemRequired stops the idle timer from putting the PC to sleep.
	esSystemRequired = 0x00000001
)

// procSetThreadExecutionState is the one kernel32 entry point this file
// needs, resolved lazily so nothing loads until sharing or receiving starts.
var procSetThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// keepAwake implements [KeepAwake].
//
// SetThreadExecutionState belongs to the calling OS thread, not the process,
// and Go moves goroutines between threads. So the request is made from one
// goroutine that locks itself to its thread and stays there until release:
// asking from a goroutine that later migrated would leave the flag on a
// thread nobody can clear.
func keepAwake() (func(), error) {
	stop := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		r, _, callErr := procSetThreadExecutionState.Call(uintptr(esContinuous | esSystemRequired))
		if r == 0 {
			result <- callErr
			runtime.UnlockOSThread()
			return
		}
		result <- nil
		<-stop
		// Clearing the flags is what lets the PC sleep again. The thread is
		// then dropped rather than unlocked, so the runtime retires it and
		// no state can leak into another goroutine.
		_, _, _ = procSetThreadExecutionState.Call(uintptr(esContinuous))
	}()
	if err := <-result; err != nil {
		return func() {}, err
	}
	var once sync.Once
	return func() { once.Do(func() { close(stop) }) }, nil
}
