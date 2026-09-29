package tools_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/tools"
)

// echoArgv returns a command that prints "hello" to stdout, portable across
// the two shells Devpit actually runs on: cmd.exe on Windows and a POSIX
// shell everywhere else (used only so `go test ./internal/tools/...` also
// works on the Linux CI box).
func echoArgv() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo", "hello"}
	}
	return []string{"sh", "-c", "echo hello"}
}

func TestRunStepSuccess(t *testing.T) {
	var lines []string
	res := tools.RunStep(context.Background(), echoArgv(), time.Minute, func(line string) {
		lines = append(lines, line)
	})

	if !res.OK {
		t.Fatalf("OK = false, want true (exit code %d, lines %v)", res.ExitCode, res.LastLines)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if res.Elapsed <= 0 {
		t.Errorf("Elapsed = %v, want > 0", res.Elapsed)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "hello") {
		t.Errorf("onLine lines = %v, want to contain %q", lines, "hello")
	}
	if len(res.LastLines) == 0 || !strings.Contains(strings.Join(res.LastLines, "\n"), "hello") {
		t.Errorf("LastLines = %v, want to contain %q", res.LastLines, "hello")
	}
}

func TestRunStepFailure(t *testing.T) {
	var argv []string
	if runtime.GOOS == "windows" {
		argv = []string{"cmd", "/c", "exit", "3"}
	} else {
		argv = []string{"sh", "-c", "exit 3"}
	}

	res := tools.RunStep(context.Background(), argv, time.Minute, nil)
	if res.OK {
		t.Fatalf("OK = true, want false")
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
}

func TestRunStepTimeout(t *testing.T) {
	// A single process that sleeps for 1s, deliberately not the
	// cmd.exe-spawns-ping.exe shape: this test proves RunStep's timeout
	// fires and returns promptly even if the tree-kill mechanism it also
	// exercises did nothing at all — the command's own short runtime bounds
	// the worst case. See TestRunStepTimeoutKillsTheTree (Windows only) for
	// the case that mechanism exists to fix: a grandchild that inherits the
	// stdout/stderr pipe and outlives the direct child.
	var argv []string
	if runtime.GOOS == "windows" {
		argv = []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 1"}
	} else {
		argv = []string{"sh", "-c", "sleep 1"}
	}

	res := tools.RunStep(context.Background(), argv, 200*time.Millisecond, nil)
	if res.OK {
		t.Fatalf("OK = true, want false (should have timed out)")
	}
	if res.Elapsed >= 2*time.Second {
		t.Errorf("Elapsed = %v, want well under 2s (timeout should have killed it)", res.Elapsed)
	}
}

func TestRunStepEmptyArgv(t *testing.T) {
	res := tools.RunStep(context.Background(), nil, time.Second, nil)
	if res.OK {
		t.Fatalf("OK = true, want false for empty argv")
	}
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", res.ExitCode)
	}
}

func TestRunStepDefaultTimeoutDoesNotPanic(t *testing.T) {
	// Passing 0 falls back to DefaultStepTimeout; just prove it starts and
	// finishes a trivial command rather than waiting the full 10 minutes.
	res := tools.RunStep(context.Background(), echoArgv(), 0, nil)
	if !res.OK {
		t.Fatalf("OK = false, want true")
	}
}

func TestRunStepLinesTransientAndLastLines(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("printf with \\r escapes needs a POSIX shell")
	}
	// A progress bar redrawn in place with bare \r, a spinner frame that
	// cleans to nothing, then two real lines, one of them CRLF-terminated.
	argv := []string{"sh", "-c", `printf '  \342\226\210\342\226\210\342\226\222\342\226\222  1 MB / 4 MB\r  \342\226\210\342\226\210\342\226\210\342\226\222  3 MB / 4 MB\r   - \rDownloaded\r\nInstalled\n'`}

	var got []tools.Line
	var texts []string
	res := tools.RunStepLines(context.Background(), argv, time.Minute, func(l tools.Line) {
		got = append(got, l)
	})
	resText := tools.RunStep(context.Background(), argv, time.Minute, func(s string) {
		texts = append(texts, s)
	})

	want := []tools.Line{
		{Text: "1 MB / 4 MB", Transient: true},
		{Text: "3 MB / 4 MB", Transient: true},
		{Text: "Downloaded"},
		{Text: "Installed"},
	}
	if len(got) != len(want) {
		t.Fatalf("lines = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	if strings.Join(res.LastLines, "|") != "Downloaded|Installed" {
		t.Errorf("LastLines = %q, want only the non-transient lines", res.LastLines)
	}
	if strings.Join(resText.LastLines, "|") != "Downloaded|Installed" {
		t.Errorf("RunStep LastLines = %q, want only the non-transient lines", resText.LastLines)
	}
	if strings.Join(texts, "|") != "1 MB / 4 MB|3 MB / 4 MB|Downloaded|Installed" {
		t.Errorf("RunStep onLine got %q, want every line's text, transient included", texts)
	}
}
