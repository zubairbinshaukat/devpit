package tools_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools"
)

// neverRun fails the test if anything is started: Windows Terminal shows a
// Help box and beeps when it is run with --version.
func neverRun(t *testing.T) tools.Option {
	t.Helper()
	return tools.WithRun(func(_ context.Context, path string, args ...string) (string, error) {
		t.Errorf("detection started %s %v; it must not run this tool", path, args)
		return "", errors.New("must not run")
	})
}

// fakeEnv answers environment lookups from a map.
func fakeEnv(env map[string]string) tools.Option {
	return tools.WithEnv(func(k string) string { return env[k] })
}

func TestWindowsTerminalIsFoundWithoutRunningIt(t *testing.T) {
	missing := func(string) error { return errors.New("no") }
	present := func(string) error { return nil }
	cases := []struct {
		name  string
		found map[string]string
		env   map[string]string
		stat  func(string) error
		want  bool
		path  string
	}{
		{"on PATH", map[string]string{"wt": `C:\Users\a\AppData\Local\Microsoft\WindowsApps\wt.exe`}, nil, missing, true, `C:\Users\a\AppData\Local\Microsoft\WindowsApps\wt.exe`},
		{"alias folder only", nil, map[string]string{"LOCALAPPDATA": `C:\Users\a\AppData\Local`}, present, true, ""},
		{"inside a Terminal tab", nil, map[string]string{"WT_SESSION": "abc"}, missing, true, ""},
		{"nowhere", nil, map[string]string{"LOCALAPPDATA": `C:\x`}, missing, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := tools.New(
				tools.WithLookPath(fakeLookPath(c.found)),
				neverRun(t), fakeEnv(c.env), tools.WithStat(c.stat),
				tools.WithReadDir(func(string) ([]string, error) { return nil, errors.New("denied") }),
			)
			got := d.Get(context.Background(), "wt")
			if got.Found != c.want {
				t.Fatalf("Found = %v, want %v", got.Found, c.want)
			}
			if c.path != "" && got.Path != c.path {
				t.Errorf("Path = %q, want %q", got.Path, c.path)
			}
			if got.Version != "" {
				t.Errorf("Version = %q, want empty when the package folder cannot be read", got.Version)
			}
		})
	}
}

func TestWindowsTerminalVersionComesFromThePackageFolder(t *testing.T) {
	d := tools.New(
		tools.WithLookPath(fakeLookPath(map[string]string{"wt": "wt.exe"})),
		neverRun(t),
		fakeEnv(map[string]string{"ProgramFiles": `C:\Program Files`}),
		tools.WithReadDir(func(string) ([]string, error) {
			return []string{
				"Microsoft.WindowsTerminalPreview_1.99.1.0_x64__8wekyb3d8bbwe",
				"Microsoft.WindowsTerminal_1.9.1942.0_x64__8wekyb3d8bbwe",
				"Microsoft.WindowsTerminal_1.21.3231.0_x64__8wekyb3d8bbwe",
				"Microsoft.WindowsTerminal_1.21.3231.0_neutral_split.language-en__8wekyb3d8bbwe",
				"Other.App_9.9.9.9_x64__abc",
			}, nil
		}),
	)
	if got := d.Get(context.Background(), "wt").Version; got != "1.21.3231.0" {
		t.Errorf("Version = %q, want 1.21.3231.0 (highest stable package, not Preview)", got)
	}
}

func TestStoreStubsForPythonAreNeverRun(t *testing.T) {
	stub := `C:\Users\a\AppData\Local\Microsoft\WindowsApps\python.exe`
	d := tools.New(
		tools.WithLookPath(fakeLookPath(map[string]string{"python": stub, "pip": stub})),
		neverRun(t),
	)
	for _, name := range []string{"python", "pip"} {
		if got := d.Get(context.Background(), name); got.Found {
			t.Errorf("%s: a Microsoft Store stub counted as installed", name)
		}
	}

	realTools := tools.New(
		tools.WithLookPath(fakeLookPath(map[string]string{"python": `C:\Python312\python.exe`})),
		tools.WithRun(func(context.Context, string, ...string) (string, error) { return "Python 3.12.1", nil }),
	)
	if got := realTools.Get(context.Background(), "python"); !got.Found || got.Version != "3.12.1" {
		t.Errorf("real python = %+v, want found 3.12.1", got)
	}
}

// Detecting every tool must start only tools that are safe to start, and
// never wt.
func TestAllNeverStartsWindowsTerminal(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	d := tools.New(
		tools.WithLookPath(func(f string) (string, error) { return `C:\bin\` + f + ".exe", nil }),
		tools.WithRun(func(_ context.Context, path string, _ ...string) (string, error) {
			mu.Lock()
			ran = append(ran, path)
			mu.Unlock()
			return "1.2.3", nil
		}),
	)
	d.All(context.Background())
	for _, p := range ran {
		if strings.Contains(p, "wt.exe") {
			t.Errorf("All started %s", p)
		}
	}
	if len(ran) == 0 {
		t.Error("All started nothing at all; the fake run was never used")
	}
}
