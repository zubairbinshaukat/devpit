//go:build windows

package winapi

import (
	"os"
	"testing"
	"time"
)

func TestProcessStartOfThisProcess(t *testing.T) {
	created, alive := ProcessStart(os.Getpid())
	if !alive {
		t.Fatal("this process is not alive")
	}
	if created.IsZero() || created.After(time.Now().Add(time.Second)) || time.Since(created) > 24*time.Hour {
		t.Errorf("start time of this test process = %v", created)
	}
	again, _ := ProcessStart(os.Getpid())
	if !again.Equal(created) {
		t.Errorf("start time changed between reads: %v then %v", created, again)
	}
}

// Read-only: names this process's own program and the boot time.
func TestProcessImageAndBootTimeOfThisProcess(t *testing.T) {
	img, err := ProcessImage(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !SameProgram(img, self) {
		t.Errorf("image %q is not this program %q", img, self)
	}
	if _, perr := ProcessImage(-1); perr == nil {
		t.Error("PID -1 has an image")
	}
	boot, err := BootTime()
	if err != nil {
		t.Fatal(err)
	}
	created, _ := ProcessStart(os.Getpid())
	if boot.IsZero() || boot.After(created.Add(time.Second)) || boot.After(time.Now()) {
		t.Errorf("boot %v, this process started %v", boot, created)
	}
}

func TestProcessStartOfAPIDThatIsNotRunning(t *testing.T) {
	// PID 0 is the idle process and never a Devpit; a negative number is no PID.
	if _, alive := ProcessStart(-1); alive {
		t.Error("PID -1 reported alive")
	}
	if _, alive := ProcessStart(0); alive {
		t.Error("PID 0 reported alive")
	}
}
