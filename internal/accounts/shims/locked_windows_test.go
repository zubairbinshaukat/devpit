//go:build windows

package shims

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// envHelperSleep makes this test binary, started as a stand-in for a running
// tool, wait until its stdin closes.
const envHelperSleep = "DEVPIT_SHIMS_TEST_SLEEP"

// TestHelperRunningTool is not a test: it is the running tool of
// TestALockedShimIsRenamedAsideAndSweptLater.
func TestHelperRunningTool(t *testing.T) {
	if os.Getenv(envHelperSleep) != "1" {
		t.Skip("helper process only")
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
	}
	os.Exit(0)
}

// A shim in use by a running tool cannot be overwritten on Windows. The
// refresh renames it aside, puts the new copy in its place (the running
// tool keeps going), and a later sync removes the aside file once the tool
// has exited.
func TestALockedShimIsRenamedAsideAndSweptLater(t *testing.T) {
	m, _, _ := newManager(t)
	dst := m.ShimPath(accounts.ToolClaude)
	if err := os.MkdirAll(m.Dir, 0o755); err != nil { //nolint:gosec // test
		t.Fatal(err)
	}
	// An old shim: a real program (this test binary), running.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = copyFile(self, dst); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(dst, "-test.run=^TestHelperRunningTool$") //nolint:gosec // test
	cmd.Env = append(os.Environ(), envHelperSleep+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = stdin.Close()
			_ = cmd.Wait()
		}
	}
	t.Cleanup(stop)
	// Windows really refuses to overwrite it while it runs.
	if err = os.WriteFile(dst, []byte("x"), 0o700); err == nil { //nolint:gosec // test
		t.Skip("this system lets a running program be overwritten; nothing to test")
	}

	created, err := m.Ensure(accounts.ToolClaude)
	if err != nil || !created {
		t.Fatalf("Ensure over a running shim: %v %v", created, err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "shim v1" {
		t.Fatalf("the new shim is not in place: %d bytes", len(b))
	}
	if asides := oldFiles(t, m.Dir); len(asides) != 1 {
		t.Fatalf("aside files = %v", asides)
	}

	// While the tool still runs, a sync leaves the aside file be.
	s := accounts.NewStore()
	if err := s.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "work", Dir: `C:\a`}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Sync(s); err != nil {
		t.Fatal(err)
	}
	stop()
	// Once it has exited, the next sync sweeps it up.
	if _, _, err := m.Sync(s); err != nil {
		t.Fatal(err)
	}
	if asides := oldFiles(t, m.Dir); len(asides) != 0 {
		t.Fatalf("aside files left after the tool exited: %v", asides)
	}
}

func oldFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.Contains(e.Name(), ".exe.old-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // test
	if err != nil {
		return err
	}
	defer in.Close()           //nolint:errcheck // read-only
	out, err := os.Create(dst) //nolint:gosec // test
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
