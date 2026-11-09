//go:build windows

package sysinfo

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Win32 layouts are pinned to their documented sizes, on every
// architecture Devpit ships for (x64 and ARM64 share them).
func TestWin32StructSizes(t *testing.T) {
	for name, c := range map[string]struct{ got, want uintptr }{
		"DISPLAYCONFIG_PATH_INFO":               {unsafe.Sizeof(dcPathInfo{}), 72},
		"DISPLAYCONFIG_MODE_INFO":               {unsafe.Sizeof(dcModeInfo{}), 64},
		"DISPLAYCONFIG_DEVICE_INFO_HEADER":      {unsafe.Sizeof(dcHeader{}), 20},
		"DISPLAYCONFIG_TARGET_DEVICE_NAME":      {unsafe.Sizeof(dcTargetName{}), 420},
		"DISPLAYCONFIG_SOURCE_DEVICE_NAME":      {unsafe.Sizeof(dcSourceName{}), 84},
		"DISPLAYCONFIG_GET_ADVANCED_COLOR_INFO": {unsafe.Sizeof(dcAdvancedColor{}), 32},
		"DEVMODEW":                              {unsafe.Sizeof(devMode{}), 220},
		"BATTERY_QUERY_INFORMATION":             {unsafe.Sizeof(batteryQueryInformation{}), 12},
		"BATTERY_INFORMATION":                   {unsafe.Sizeof(batteryInformationT{}), 36},
		"BATTERY_WAIT_STATUS":                   {unsafe.Sizeof(batteryWaitStatus{}), 20},
		"BATTERY_STATUS":                        {unsafe.Sizeof(batteryStatusT{}), 16},
		"SYSTEM_POWER_STATUS":                   {unsafe.Sizeof(systemPowerStatus{}), 12},
		"MEMORYSTATUSEX":                        {unsafe.Sizeof(memoryStatusEx{}), 64},
		"TPM_DEVICE_INFO":                       {unsafe.Sizeof(tpmDeviceInfo{}), 16},
	} {
		if c.got != c.want {
			t.Errorf("%s is %d bytes, want %d", name, c.got, c.want)
		}
	}
	if off := unsafe.Offsetof(devMode{}.PelsWidth); off != 172 {
		t.Errorf("DEVMODEW.dmPelsWidth at %d, want 172", off)
	}
	if off := unsafe.Offsetof(devMode{}.Frequency); off != 184 {
		t.Errorf("DEVMODEW.dmDisplayFrequency at %d, want 184", off)
	}
	if off := unsafe.Offsetof(dcTargetName{}.MonitorDevicPath); off != 164 {
		t.Errorf("monitorDevicePath at %d, want 164", off)
	}
}

// Reading this PC never panics and answers in time. Battery, screens and
// PowerShell may be absent; SMBIOS, the processor and memory must work.
func TestLiveReadsThisMachine(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the real machine")
	}
	src := NewLive()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	start := time.Now()
	var wg sync.WaitGroup
	errs := make(map[string]error)
	var mu sync.Mutex
	run := func(name string, f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := f()
			mu.Lock()
			errs[name] = err
			mu.Unlock()
		}()
	}
	var sys System
	var cpu CPU
	var mem Memory
	run("system", func() (err error) { sys, err = Bounded(ctx, NativeLimit, src.System); return err })
	run("cpu", func() (err error) { cpu, err = Bounded(ctx, NativeLimit, src.CPU); return err })
	run("memory", func() (err error) { mem, err = Bounded(ctx, NativeLimit, src.Memory); return err })
	run("graphics", func() error { _, err := Bounded(ctx, NativeLimit, src.Graphics); return err })
	run("displays", func() error { _, err := Bounded(ctx, NativeLimit, src.Displays); return err })
	run("volumes", func() error { _, err := Bounded(ctx, NativeLimit, src.Volumes); return err })
	run("battery", func() error { _, err := Bounded(ctx, NativeLimit, src.Battery); return err })
	run("network", func() error { _, err := Bounded(ctx, NativeLimit, src.Network); return err })
	run("devices", func() error { _, err := Bounded(ctx, NativeLimit, src.Devices); return err })
	run("disks", func() error { _, err := Bounded(ctx, BatchSectionLimit, src.Disks); return err })
	run("windows", func() error { _, err := Bounded(ctx, BatchSectionLimit, src.Windows); return err })
	wg.Wait()
	if d := time.Since(start); d > 25*time.Second {
		t.Fatalf("took %v", d)
	}
	for _, must := range []string{"system", "cpu", "memory"} {
		if errs[must] != nil {
			t.Errorf("%s: %v", must, errs[must])
		}
	}
	if cpu.Cores == 0 || cpu.Threads < cpu.Cores {
		t.Errorf("cpu = %+v", cpu)
	}
	if mem.UsableB == 0 || mem.InstalledB < mem.UsableB/2 {
		t.Errorf("memory totals = %d installed, %d usable", mem.InstalledB, mem.UsableB)
	}
	t.Logf("read in %v: %s %s (%s), %d cores / %d threads, %d slots; errors: %v",
		time.Since(start).Round(time.Millisecond), sys.Maker, sys.Model, sys.Chassis, cpu.Cores, cpu.Threads, len(mem.Slots), errs)
}

// A PowerShell run that overruns its limit is killed and reported as a
// timeout, not waited for.
func TestPowerShellTimeoutKillsIt(t *testing.T) {
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	ping := filepath.Join(sys, "PING.EXE")
	if _, lerr := exec.LookPath(ping); lerr != nil {
		t.Skip("ping.exe is not here")
	}
	start := time.Now()
	_, err = runProgram(context.Background(), ping, []string{"-n", "30", "127.0.0.1"}, "", 300*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the run was waited for: %v", d)
	}
	exe, err := powerShellPath()
	if err != nil || filepath.Base(exe) != "powershell.exe" || !filepath.IsAbs(exe) {
		t.Fatalf("PowerShell path = %q %v", exe, err)
	}
}
