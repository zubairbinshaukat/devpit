//go:build windows

package launch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The test binary doubles as every program these tests start. Copies of it
// pick their part from their own file name, so a child never inherits its
// parent's part through the environment:
//
//	args.exe, node.exe   print their arguments as JSON, exit DEVPIT_LAUNCH_EXIT
//	ctrlc.exe            write DEVPIT_LAUNCH_READY, then wait for Ctrl+C:
//	                     exit 3 if it comes, 7 after 10 s
//	spawner.exe          start a long ping, write its pid to
//	                     DEVPIT_LAUNCH_PIDFILE, exit 0 (or stay, with
//	                     DEVPIT_LAUNCH_STAY=1)
//
// The test binary itself, with DEVPIT_LAUNCH_HELPER=launcher, is the
// launcher: it runs Run on DEVPIT_LAUNCH_TARGET with its own arguments.
func TestMain(m *testing.M) {
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0])))
	switch name {
	case "args", "node":
		args := os.Args[1:]
		if args == nil {
			args = []string{}
		}
		_ = json.NewEncoder(os.Stdout).Encode(args)
		code, _ := strconv.Atoi(os.Getenv("DEVPIT_LAUNCH_EXIT"))
		os.Exit(code)
	case "ctrlc":
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		_ = os.WriteFile(os.Getenv("DEVPIT_LAUNCH_READY"), []byte("ready"), 0o600)
		select {
		case <-ch:
			os.Exit(3)
		case <-time.After(10 * time.Second):
			os.Exit(7)
		}
	case "spawner":
		ping := filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE")
		c := exec.Command(ping, "-n", "60", "127.0.0.1")
		if err := c.Start(); err != nil {
			os.Exit(20)
		}
		_ = os.WriteFile(os.Getenv("DEVPIT_LAUNCH_PIDFILE"), []byte(strconv.Itoa(c.Process.Pid)), 0o600)
		if os.Getenv("DEVPIT_LAUNCH_STAY") == "1" {
			time.Sleep(60 * time.Second)
		}
		os.Exit(0)
	}
	if os.Getenv("DEVPIT_LAUNCH_HELPER") == "launcher" {
		if os.Getenv("DEVPIT_LAUNCH_CTRLC") == "1" {
			// A parent that ignores Ctrl+C hands that down to every process
			// it starts, and a git hook's shell is such a parent: without
			// this the child would never see the Ctrl+C sent below.
			_, _, _ = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler").Call(0, 0)
			go func() {
				// Only once the child listens: a Ctrl+C that lands before its
				// handler is in place would end it with 0xC000013A instead.
				ready := os.Getenv("DEVPIT_LAUNCH_READY")
				for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
					if _, err := os.Stat(ready); err == nil {
						break
					}
				}
				_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0)
			}()
		}
		code, err := Run(Spec{Path: os.Getenv("DEVPIT_LAUNCH_TARGET"), Args: os.Args[1:]})
		if err != nil {
			fmt.Fprintln(os.Stderr, "launcher:", err)
		}
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func self(t *testing.T) string {
	t.Helper()
	p, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// tool copies the test binary to dir\name.
func tool(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(self(t))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o700); err != nil { //nolint:gosec // an executable
		t.Fatal(err)
	}
	return p
}

// launcher returns a command that runs the launcher on target.
func launcher(t *testing.T, target string, env []string, args ...string) *exec.Cmd {
	t.Helper()
	c := exec.Command(self(t), args...)
	c.Env = append(os.Environ(), append([]string{"DEVPIT_LAUNCH_HELPER=launcher", "DEVPIT_LAUNCH_TARGET=" + target}, env...)...)
	return c
}

// result runs c and returns its exit code, stdout and stderr.
func result(t *testing.T, c *exec.Cmd) (int, string, string) {
	t.Helper()
	var out, errb strings.Builder
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		t.Fatal(err)
	}
	return c.ProcessState.ExitCode(), out.String(), errb.String()
}

func decodeArgs(t *testing.T, out string) []string {
	t.Helper()
	var got []string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("child output %q: %v", out, err)
	}
	return got
}

func TestExitCodeAndArgumentsPassThroughAnExe(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned.txt")
	args := hostileArgs(marker)
	code, out, errOut := result(t, launcher(t, tool(t, dir, "args.exe"), []string{"DEVPIT_LAUNCH_EXIT=42"}, args...))
	if code != 42 {
		t.Fatalf("exit = %d (%s), want 42", code, errOut)
	}
	if got := decodeArgs(t, out); strings.Join(got, "\x00") != strings.Join(args, "\x00") {
		t.Fatalf("arguments changed:\n got %q\nwant %q", got, args)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an argument ran a command")
	}
}

func TestALargeExitCodePassesThrough(t *testing.T) {
	code, _, _ := result(t, launcher(t, tool(t, t.TempDir(), "args.exe"), []string{"DEVPIT_LAUNCH_EXIT=-1073741510"}))
	if uint32(code) != 0xC000013A { //nolint:gosec // comparing bit patterns
		t.Fatalf("exit = %#x, want 0xC000013A", uint32(code)) //nolint:gosec // display
	}
}

func TestNpmShimRunsNodeDirectlyWithEveryArgumentIntact(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned.txt")
	// A fake npm prefix: vercel.cmd (the real cmd-shim text), node.exe (the
	// test binary, printing its arguments) and the script it names.
	shim := filepath.Join(dir, "vercel.cmd")
	if err := os.WriteFile(shim, []byte(strings.ReplaceAll(npmShim, "\n", "\r\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	tool(t, dir, "node.exe")
	script := filepath.Join(dir, "node_modules", "vercel", "dist", "vc.js")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("// vercel"), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err := Prepare(shim, FindOptions{})
	if err != nil || target.Kind != KindNodeScript || target.Path != filepath.Join(dir, "node.exe") {
		t.Fatalf("Prepare = %+v, %v", target, err)
	}

	args := hostileArgs(marker)
	code, out, errOut := result(t, launcher(t, shim, []string{"DEVPIT_LAUNCH_EXIT=5"}, args...))
	if code != 5 {
		t.Fatalf("exit = %d (%s), want 5", code, errOut)
	}
	want := append([]string{script}, args...)
	if got := decodeArgs(t, out); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("arguments changed:\n got %q\nwant %q", got, want)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an argument ran a command")
	}
}

// TestNpmShimWithRealNode repeats the hostile-argument check with the real
// Node.js when it is installed: Node, not Go, parses the command line there.
func TestNpmShimWithRealNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned.txt")
	shim := filepath.Join(dir, "tool.cmd")
	if err := os.WriteFile(shim, []byte(strings.ReplaceAll(strings.ReplaceAll(npmShim, "vercel\\dist\\vc.js", "t\\cli.js"), "\n", "\r\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "node_modules", "t", "cli.js")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("process.stdout.write(JSON.stringify(process.argv.slice(2)))"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(node)+string(os.PathListSeparator)+os.Getenv("PATH"))
	args := hostileArgs(marker)
	code, out, errOut := result(t, launcher(t, shim, nil, args...))
	if code != 0 {
		t.Fatalf("exit = %d (%s)", code, errOut)
	}
	if got := decodeArgs(t, out); strings.Join(got, "\x00") != strings.Join(args, "\x00") {
		t.Fatalf("arguments changed:\n got %q\nwant %q", got, args)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an argument ran a command")
	}
}

func TestOtherBatchFilesGetSafeArgumentsAndRefuseTheRest(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned.txt")
	argsExe := tool(t, dir, "args.exe")
	script := filepath.Join(dir, "my tool.cmd")
	if err := os.WriteFile(script, []byte("@echo off\r\n\""+argsExe+"\" %*\r\nexit /b %ERRORLEVEL%\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	safe := []string{"deploy", "--prod", "a b", "", `C:\dir\`, "k=v", "a,b", "x@y.com", "1.2.3", "C:/x/y", "+x", "a:b"}
	code, out, errOut := result(t, launcher(t, script, []string{"DEVPIT_LAUNCH_EXIT=9"}, safe...))
	if code != 9 {
		t.Fatalf("exit = %d (%s), want 9", code, errOut)
	}
	if got := decodeArgs(t, out); strings.Join(got, "\x00") != strings.Join(safe, "\x00") {
		t.Fatalf("arguments changed:\n got %q\nwant %q", got, safe)
	}

	for _, a := range hostileArgs(marker) {
		if SafeBatchArg(a) {
			continue
		}
		code, out, errOut := result(t, launcher(t, script, nil, "ok", a))
		if out != "" || code == 0 || !strings.Contains(errOut, "cannot be passed safely") {
			t.Errorf("arg %q: started anyway (exit %d, out %q, err %q)", a, code, out, errOut)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an argument ran a command")
	}
}

// TestGoAloneIsNotSafeForBatchFiles records why this package exists: plain
// os/exec on a .cmd lets cmd.exe reinterpret the arguments. It only logs
// what this Go version does, as a reminder to revisit the package the day
// Go starts quoting for batch files.
func TestGoAloneIsNotSafeForBatchFiles(t *testing.T) {
	dir := t.TempDir()
	argsExe := tool(t, dir, "args.exe")
	script := filepath.Join(dir, "t.cmd")
	if err := os.WriteFile(script, []byte("@\""+argsExe+"\" %*\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "pwned.txt")
	out, err := exec.Command(script, "x", `"&echo INJECTED>`+marker+`&"`, "%PATH%").CombinedOutput()
	_, statErr := os.Stat(marker)
	t.Logf("%s, plain os/exec on a .cmd: err=%v, injected=%v, output=%.100q", runtime.Version(), err, statErr == nil, out)
}

func TestCtrlCGoesToTheChildAndTheLauncherPassesItsCodeBack(t *testing.T) {
	c := launcher(t, tool(t, t.TempDir(), "ctrlc.exe"), []string{
		"DEVPIT_LAUNCH_CTRLC=1",
		"DEVPIT_LAUNCH_READY=" + filepath.Join(t.TempDir(), "ready"),
	})
	// A console of its own, hidden, so the Ctrl+C reaches only the launcher
	// and its child.
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	err := c.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code != 3 {
		t.Fatalf("launcher exit = %d (%#x); want 3: the child got Ctrl+C and the launcher lived to pass its code back", code, uint32(code)) //nolint:gosec // display
	}
}

func TestKillingTheLauncherKillsTheTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	c := launcher(t, tool(t, t.TempDir(), "spawner.exe"), []string{"DEVPIT_LAUNCH_PIDFILE=" + pidFile, "DEVPIT_LAUNCH_STAY=1"})
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	h := openPid(t, waitPid(t, pidFile))
	defer windows.CloseHandle(h) //nolint:errcheck // test
	if err := c.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = c.Wait()
	if ev, _ := windows.WaitForSingleObject(h, 5000); ev != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(h, 1)
		t.Fatal("the grandchild outlived a killed launcher")
	}
}

func TestWhatTheToolLeavesRunningSurvivesANormalExit(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	code, _, errOut := result(t, launcher(t, tool(t, t.TempDir(), "spawner.exe"), []string{"DEVPIT_LAUNCH_PIDFILE=" + pidFile}))
	if code != 0 {
		t.Fatalf("launcher exit %d: %s", code, errOut)
	}
	h := openPid(t, waitPid(t, pidFile))
	defer windows.CloseHandle(h)         //nolint:errcheck // test
	defer windows.TerminateProcess(h, 0) //nolint:errcheck // test cleanup
	if ev, _ := windows.WaitForSingleObject(h, 1500); ev == windows.WAIT_OBJECT_0 {
		t.Fatal("a process the tool meant to leave running was killed when the launcher exited")
	}
}

func openPid(t *testing.T, pid int) windows.Handle {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid)) //nolint:gosec // a pid we started
	if err != nil {
		t.Fatalf("opening pid %d: %v", pid, err)
	}
	return h
}

func waitPid(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(file); err == nil && len(b) > 0 {
			if pid, err := strconv.Atoi(string(b)); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the child never wrote its pid")
	return 0
}
