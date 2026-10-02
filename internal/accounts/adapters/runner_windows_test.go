//go:build windows

package adapters

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// Copies of the test binary act as tools, by file name:
//
//	faketool.exe  prints its arguments and CLAUDE_CONFIG_DIR, exits 1
//	slowtool.exe  sleeps 30 s
func TestMain(m *testing.M) {
	switch strings.ToLower(strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")) {
	case "faketool":
		fmt.Printf("args=%s dir=%s\n", strings.Join(os.Args[1:], ","), os.Getenv("CLAUDE_CONFIG_DIR"))
		fmt.Fprintln(os.Stderr, "token=ghp_PLANTEDstderr0123456789abcdef")
		os.Exit(1)
	case "slowtool":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func copySelf(t *testing.T, dir, name string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o700); err != nil { //nolint:gosec // an executable
		t.Fatal(err)
	}
}

func TestExecRunnerRunsTheRealToolOutsideTheShimFolder(t *testing.T) {
	shimDir, realDir := t.TempDir(), t.TempDir()
	copySelf(t, shimDir, "slowtool.exe") // a "shim" that would hang if run
	copySelf(t, shimDir, "faketool.exe")
	copySelf(t, realDir, "faketool.exe")
	copySelf(t, realDir, "slowtool.exe")
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+realDir)
	t.Setenv("CLAUDE_CONFIG_DIR", "inherited")

	var logs []string
	r := ExecRunner{ShimDir: shimDir, Log: func(s string) { logs = append(logs, s) }}
	res, err := r.Run(context.Background(), Cmd{Name: "faketool", Args: []string{"auth", "status"}, Env: []string{`CLAUDE_CONFIG_DIR=C:\acct`}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 1 || !strings.EqualFold(filepath.Dir(res.Path), realDir) {
		t.Fatalf("result = %+v", res)
	}
	if got := strings.TrimSpace(string(res.Stdout)); got != `args=auth,status dir=C:\acct` {
		t.Fatalf("stdout = %q", got)
	}
	res, err = r.Run(context.Background(), Cmd{Name: "faketool", Unset: []string{"CLAUDE_CONFIG_DIR"}})
	if err != nil || strings.TrimSpace(string(res.Stdout)) != "args= dir=" {
		t.Fatalf("unset: %q %v", res.Stdout, err)
	}
	for _, l := range logs {
		if strings.Contains(l, "PLANTED") {
			t.Fatalf("log leaked a token: %q", l)
		}
	}
	if len(logs) == 0 || !strings.Contains(logs[0], "ran faketool auth status (exit 1") {
		t.Fatalf("logs = %q", logs)
	}

	start := time.Now()
	_, err = r.Run(context.Background(), Cmd{Name: "slowtool", Timeout: 500 * time.Millisecond})
	if !errors.Is(err, accounts.ErrTimeout) || time.Since(start) > 10*time.Second {
		t.Fatalf("timeout: %v after %v", err, time.Since(start))
	}

	if _, err := r.Run(context.Background(), Cmd{Name: "nosuchtool"}); !errors.Is(err, accounts.ErrNotFoundTool) {
		t.Fatalf("missing tool: %v", err)
	}
	if _, err := r.Run(context.Background(), Cmd{Name: "firebase", Args: []string{"login:list", "--json"}}); !errors.Is(err, ErrForbiddenCommand) {
		t.Fatalf("forbidden: %v", err)
	}
}
