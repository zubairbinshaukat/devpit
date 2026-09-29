//go:build windows

package elevate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// These tests create real, local named pipes in this (unelevated) process.
// None of them elevates, creates an account or changes the system.

// testPipeSDDL is the production access list plus this test's own user, so
// an unelevated in-process "worker" can connect.
func testPipeSDDL(t *testing.T) string {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser: %v", err)
	}
	return workerPipeSDDL + "(A;;GA;;;" + u.User.Sid.String() + ")"
}

// dialPipe opens the client end of name the way [Serve] does.
func dialPipe(name string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, windows.SECURITY_SQOS_PRESENT|windows.SECURITY_ANONYMOUS, 0)
}

// newTestPipe creates a pipe named for pid with the test access list and
// closes it when the test ends.
func newTestPipe(t *testing.T, pid int) (string, *pipeConn) {
	t.Helper()
	name := pipeName(pid, mustHex(t))
	h, err := createPipeWithSDDL(name, testPipeSDDL(t))
	if err != nil {
		t.Fatalf("createPipeWithSDDL: %v", err)
	}
	conn, err := newPipeConn(h)
	if err != nil {
		t.Fatalf("newPipeConn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return name, conn
}

// The real pipe admits only SYSTEM and Administrators: an ordinary,
// unelevated program of the same user cannot open it, so it can neither
// pose as the worker nor read what the TUI sends it.
func TestTheProductionPipeKeepsOutAnUnelevatedProcess(t *testing.T) {
	if elevated, _ := IsElevated(); elevated {
		t.Skip("this test process is elevated, so it is allowed in by design")
	}
	name := pipeName(os.Getpid(), mustHex(t))
	h, err := createServerPipe(name)
	if err != nil {
		t.Fatalf("createServerPipe: %v", err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // test cleanup

	c, err := dialPipe(name)
	if err == nil {
		_ = windows.CloseHandle(c)
		t.Fatal("an unelevated process opened the worker pipe")
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("dial error = %v, want ERROR_ACCESS_DENIED", err)
	}
}

// A pipe name that already exists is never taken over: the TUI's create
// fails instead of quietly joining a pipe someone else made first.
func TestAPipeNameThatIsAlreadyTakenIsRefused(t *testing.T) {
	name, _ := newTestPipe(t, os.Getpid())
	h, err := createServerPipe(name)
	if err == nil {
		_ = windows.CloseHandle(h)
		t.Fatal("a second pipe with the same name was created")
	}
}

// The worker serves only the process whose PID is in the pipe name. A pipe
// made by anyone else is refused before a single request is read.
func TestServeRefusesAPipeOwnedByAnotherProcess(t *testing.T) {
	// The name claims PID 4 (the System process); this test process owns it.
	name, conn := newTestPipe(t, 4)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.waitForConnect(ctx.Done())
	}()

	fe := &fakeExecutor{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Serve(ctx, name, fe)
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("Serve = %v, want a refusal", err)
	}
	if len(fe.execCalls) != 0 {
		t.Fatal("the worker ran something for a pipe it refused")
	}
}

// A name that is not a local Devpit pipe is refused without being dialled.
func TestServeRefusesANameItDidNotGetFromLaunch(t *testing.T) {
	for _, name := range []string{
		`\\evilhost\pipe\devpit-1234-abcdef01`,
		`\\.\pipe\someone-else`,
		`C:\Windows\Temp\x`,
	} {
		err := Serve(context.Background(), name, &fakeExecutor{})
		if err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("Serve(%q) = %v, want a refusal", name, err)
		}
	}
}

// The TUI accepts only the worker it launched on its end of the pipe.
func TestVerifyClientAcceptsOnlyTheLaunchedWorker(t *testing.T) {
	name, conn := newTestPipe(t, os.Getpid())
	connected := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		connected <- conn.waitForConnect(ctx.Done())
	}()
	c, err := dialPipe(name)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer windows.CloseHandle(c) //nolint:errcheck // test cleanup
	if cerr := <-connected; cerr != nil {
		t.Fatalf("waitForConnect: %v", cerr)
	}

	self := uint32(os.Getpid()) //nolint:gosec // a Windows PID is a DWORD
	if verr := verifyClient(conn.h, self, ""); verr != nil {
		t.Errorf("the launched worker was refused: %v", verr)
	}
	if verifyClient(conn.h, self+4, "") == nil {
		t.Error("a client that is not the launched worker was accepted")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyClient(conn.h, 0, exe); err != nil {
		t.Errorf("with no worker PID, a client running exe was refused: %v", err)
	}
	if err := verifyClient(conn.h, 0, `C:\Windows\System32\notepad.exe`); err == nil {
		t.Error("with no worker PID, a client running another program was accepted")
	}

	// And the worker's own check of the server passes for its real owner.
	if err := verifyServer(c, self); err != nil {
		t.Errorf("verifyServer refused the real owner: %v", err)
	}
	if err := verifyServer(c, self+4); err == nil {
		t.Error("verifyServer accepted the wrong owner")
	}
}

// The worker must be able to write a job's output while its loop is waiting
// for the next request. On a pipe handle opened without FILE_FLAG_OVERLAPPED
// Windows lets only one I/O run at a time, so the first output line waited
// forever behind the pending read and every elevated command hung. This runs
// the real Serve over a real pipe (no process is started: fakeExecutor
// streams two lines and succeeds).
func TestTheWorkerStreamsOutputOverARealPipe(t *testing.T) {
	name, conn := newTestPipe(t, os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serveErr := make(chan error, 1)
	go func() { serveErr <- Serve(ctx, name, &fakeExecutor{}) }()
	if err := conn.waitForConnect(ctx.Done()); err != nil {
		t.Fatalf("waitForConnect: %v", err)
	}
	client, err := newClient(ctx, conn)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	for i := range 3 {
		var lines []string
		code, err := client.Exec(ctx, []string{"anything"}, 0, func(_, text string) { lines = append(lines, text) })
		if err != nil || code != 0 || len(lines) != 2 {
			t.Fatalf("Exec #%d = %d, %v, lines %q; want 0, nil and two lines", i, code, err, lines)
		}
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve = %v, want nil after shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
}

// Closing a pipeConn wakes a Read that is blocked on it, and a second Close
// is harmless.
func TestClosingThePipeWakesABlockedRead(t *testing.T) {
	name, conn := newTestPipe(t, os.Getpid())
	connected := make(chan error, 1)
	go func() { connected <- conn.waitForConnect(nil) }()
	c, err := dialPipe(name)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer windows.CloseHandle(c) //nolint:errcheck // test cleanup
	if err := <-connected; err != nil {
		t.Fatalf("waitForConnect: %v", err)
	}

	readDone := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 16))
		readDone <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("a read on a closed pipe succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not wake the blocked Read")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := conn.Write([]byte("x")); err == nil {
		t.Fatal("a write after Close succeeded")
	}
}

// The worker's command line survives quoting exactly, including the cases
// the old hand-rolled quoting broke: a trailing backslash inside quotes, an
// embedded quote after backslashes, and an empty argument.
func TestJoinArgsRoundTripsThroughTheWindowsParser(t *testing.T) {
	args := []string{
		workerFlag, pipeFlag, pipeName(1234, "abcdef0123456789"),
		`C:\my dir\`, `a\"b`, `say "hi"`, "", `plain`,
	}
	got, err := windows.DecomposeCommandLine("devpit.exe " + joinArgs(args))
	if err != nil {
		t.Fatalf("DecomposeCommandLine: %v", err)
	}
	got = got[1:]
	if len(got) != len(args) {
		t.Fatalf("parsed %d args %q, want %d %q", len(got), got, len(args), args)
	}
	for i := range args {
		if got[i] != args[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], args[i])
		}
	}
}

// Saying no to the UAC prompt (ERROR_CANCELLED, 1223) is a DeclinedError,
// which the screens turn into a friendly "skipped (needs admin)".
func TestADeclinedPromptIsADeclinedError(t *testing.T) {
	var declined *DeclinedError
	if err := launchError("devpit.exe", windows.ERROR_CANCELLED); !errors.As(err, &declined) {
		t.Errorf("ERROR_CANCELLED became %v, want a *DeclinedError", err)
	}
	if err := launchError("devpit.exe", windows.ERROR_FILE_NOT_FOUND); errors.As(err, &declined) {
		t.Errorf("a missing file became a DeclinedError: %v", err)
	}
}

// mklinkJunction makes a directory junction, which needs no admin rights.
func mklinkJunction(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Skipf("cannot make a junction here: %v: %s", err, out)
	}
}

// %WINDIR%\Temp is writable by every user, so anyone can plant a junction
// in it. An elevated delete must never follow one out of the cleanup root.
func TestRemoveNeverFollowsAJunctionOutOfTheRoot(t *testing.T) {
	base := t.TempDir()
	t.Setenv("WINDIR", "")
	t.Setenv("PROGRAMDATA", "")
	t.Setenv("LOCALAPPDATA", base)
	root := filepath.Join(base, "CrashDumps")
	outside := filepath.Join(base, "precious")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	victim := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "j")
	mklinkJunction(t, link, outside)

	ex := DefaultExecutor{}
	if err := ex.Remove(context.Background(), filepath.Join(link, "keep.txt")); err == nil {
		t.Error("a delete through a junction was allowed")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("the file behind the junction is gone: %v", err)
	}
	// Removing the junction itself removes the link, not what it points to.
	if err := ex.Remove(context.Background(), link); err != nil {
		t.Fatalf("removing the junction: %v", err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("removing the junction removed its target: %v", err)
	}
	// A plain file inside the root is still removed.
	plain := filepath.Join(root, "app.dmp")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ex.Remove(context.Background(), plain); err != nil {
		t.Fatalf("removing a plain file: %v", err)
	}
	if _, err := os.Stat(plain); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the plain file is still there: %v", err)
	}
}

// A folder reached through a junction is refused for sharing, whatever the
// junction's own name looks like.
func TestShareRefusesAFolderReachedThroughAJunction(t *testing.T) {
	// The temp folder is inside AppData, which is refused for its own sake.
	t.Setenv("USERPROFILE", "")
	base := t.TempDir()
	target := filepath.Join(base, "real")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "looks-harmless")
	mklinkJunction(t, link, target)

	s := newShareSession(&shareFake{})
	if err := s.checkFolder(target); err != nil {
		t.Errorf("a plain folder was refused: %v", err)
	}
	if err := s.checkFolder(link); err == nil || !strings.Contains(err.Error(), "leads to") {
		t.Errorf("a junction was accepted: %v", err)
	}
}
