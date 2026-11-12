//go:build windows

package extract

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

func testExe(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "hatchzip.exe")
}

// Safety rule 86: hatchzip is its own process group on Devpit's console: no
// new console, no detached process, no hidden window.
func TestRunnerUsesANewProcessGroupAndNoNewConsole(t *testing.T) {
	cmd, err := buildCmd(Spec{Exe: testExe(t), Args: []string{"check", `D:\a.zip`, "-o", `D:\a`}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	attr := cmd.SysProcAttr
	if attr == nil || attr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("SysProcAttr = %+v", attr)
	}
	for name, flag := range map[string]uint32{
		"CREATE_NO_WINDOW": windows.CREATE_NO_WINDOW, "DETACHED_PROCESS": windows.DETACHED_PROCESS,
		"CREATE_NEW_CONSOLE": windows.CREATE_NEW_CONSOLE,
	} {
		if attr.CreationFlags&flag != 0 {
			t.Errorf("%s is set", name)
		}
	}
	if attr.HideWindow {
		t.Error("HideWindow is set")
	}
	if cmd.Stdout != nil || cmd.Stderr != nil {
		t.Error("output must go to pipes made at Start, not to the screen")
	}
	if _, err := buildCmd(Spec{Exe: "hatchzip.exe"}, nil); !errors.Is(err, errRelativeExe) {
		t.Fatalf("relative exe: %v", err)
	}
}

// Safety rule 86: stdin is nil, which os/exec opens as the null device, so
// hatchzip can never wait for a key or a password.
func TestStdinIsNul(t *testing.T) {
	cmd, err := buildCmd(Spec{Exe: testExe(t)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Stdin != nil {
		t.Fatalf("stdin = %T, want nil (the null device)", cmd.Stdin)
	}
	// Prove it with a real child: findstr reads stdin to its end; on NUL it
	// ends at once instead of waiting.
	findstr := filepath.Join(os.Getenv("SystemRoot"), "System32", "findstr.exe")
	if _, serr := os.Stat(findstr); serr != nil {
		t.Skip("findstr not found")
	}
	p, err := realRunner{}.Start(Spec{Exe: findstr, Args: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	for range p.Lines() {
	}
	if exit, err := p.Wait(); err != nil || exit != 1 {
		t.Fatalf("findstr on NUL: exit %d, %v (want 1: no match, no wait)", exit, err)
	}
}

// Safety rule 87: the process is put in a kill-on-close Job right after it
// starts, and a failed Assign leaves no job behind.
func TestTheJobIsAssignedRightAfterStart(t *testing.T) {
	src := stripComments(readSource(t, "runner_windows.go"))
	start := strings.Index(src, "if err := cmd.Start(); err != nil {")
	assign := strings.Index(src, "p.job = assignJob(cmd.Process.Pid, winapi.NewJob)")
	pump := strings.Index(src, "pumpAll(stdout, stderr)")
	if start < 0 || assign < start || pump < assign {
		t.Fatal("the Job must be assigned right after cmd.Start, before anything else")
	}
	if j := assignJob(-1, winapi.NewJob); j != nil {
		t.Fatal("a failed Assign kept the job")
	}
	if j := assignJob(os.Getpid(), func() (*winapi.Job, error) { return nil, errors.New("no job") }); j != nil {
		t.Fatal("no job expected")
	}
	// A real child is in its job: the job kill ends it.
	ping := filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE")
	p, err := realRunner{}.Start(Spec{Exe: ping, Args: []string{"-n", "30", "127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	rp := p.(*realProcess)
	if rp.job == nil {
		t.Fatal("no job was assigned")
	}
	go func() {
		for range p.Lines() {
		}
	}()
	rp.job.Kill()
	if exit, _ := p.Wait(); exit != winapi.KilledExitCode {
		t.Fatalf("exit after the job kill = %d", exit)
	}
}
