package tools_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/tools"
)

// fakeLookPath resolves only the exe names present in found, mapping each to
// a fake absolute path.
func fakeLookPath(found map[string]string) tools.LookPathFunc {
	return func(file string) (string, error) {
		if path, ok := found[file]; ok {
			return path, nil
		}
		return "", errors.New("not found")
	}
}

func TestGetFoundTool(t *testing.T) {
	d := tools.New(
		tools.WithLookPath(fakeLookPath(map[string]string{"git": `C:\bin\git.exe`})),
		tools.WithRun(func(_ context.Context, path string, args ...string) (string, error) {
			return "git version 2.43.0.windows.1", nil
		}),
	)

	got := d.Get(context.Background(), "git")
	if !got.Found {
		t.Fatalf("Found = false, want true")
	}
	if got.Path != `C:\bin\git.exe` {
		t.Errorf("Path = %q", got.Path)
	}
	if got.Version != "2.43.0" {
		t.Errorf("Version = %q, want 2.43.0", got.Version)
	}
}

func TestGetNotFoundTool(t *testing.T) {
	d := tools.New(
		tools.WithLookPath(fakeLookPath(nil)),
		tools.WithRun(func(context.Context, string, ...string) (string, error) {
			t.Fatal("run should not be called when LookPath fails")
			return "", nil
		}),
	)

	got := d.Get(context.Background(), "git")
	if got.Found {
		t.Fatalf("Found = true, want false")
	}
	if got.Name != "git" {
		t.Errorf("Name = %q", got.Name)
	}
}

func TestGetUnknownName(t *testing.T) {
	d := tools.New()
	got := d.Get(context.Background(), "not-a-real-tool")
	if got.Found {
		t.Fatalf("Found = true for unknown tool")
	}
	if got.Name != "not-a-real-tool" {
		t.Errorf("Name = %q", got.Name)
	}
}

func TestGetMemoizesAndRunsOnce(t *testing.T) {
	var lookups, runs int32
	d := tools.New(
		tools.WithLookPath(func(file string) (string, error) {
			atomic.AddInt32(&lookups, 1)
			return `C:\bin\` + file, nil
		}),
		tools.WithRun(func(context.Context, string, ...string) (string, error) {
			atomic.AddInt32(&runs, 1)
			return "1.0.0", nil
		}),
	)

	ctx := context.Background()
	for range 5 {
		d.Get(ctx, "node")
	}
	if lookups != 1 {
		t.Errorf("lookups = %d, want 1", lookups)
	}
	if runs != 1 {
		t.Errorf("runs = %d, want 1", runs)
	}
}

func TestGetVersionProbeFailureStillFound(t *testing.T) {
	d := tools.New(
		tools.WithLookPath(fakeLookPath(map[string]string{"docker": `C:\bin\docker.exe`})),
		tools.WithRun(func(context.Context, string, ...string) (string, error) {
			return "", errors.New("boom")
		}),
	)

	got := d.Get(context.Background(), "docker")
	if !got.Found {
		t.Fatalf("Found = false, want true (LookPath succeeded)")
	}
	if got.Version != "" {
		t.Errorf("Version = %q, want empty", got.Version)
	}
}

func TestAllReturnsEveryToolInOrder(t *testing.T) {
	found := make(map[string]string)
	for _, name := range tools.Names() {
		found[name] = `C:\bin\` + name
	}
	d := tools.New(
		tools.WithLookPath(fakeLookPath(found)),
		tools.WithRun(func(context.Context, string, ...string) (string, error) {
			return "1.2.3", nil
		}),
	)

	all := d.All(context.Background())
	names := tools.Names()
	if len(all) != len(names) {
		t.Fatalf("len(all) = %d, want %d", len(all), len(names))
	}
	for i, tool := range all {
		if tool.Name != names[i] {
			t.Errorf("all[%d].Name = %q, want %q", i, tool.Name, names[i])
		}
		if !tool.Found {
			t.Errorf("all[%d] (%s) Found = false", i, tool.Name)
		}
	}
}

func TestAllRunsConcurrentlyWithBoundedPool(t *testing.T) {
	var inFlight, maxInFlight int32
	d := tools.New(
		tools.WithPoolSize(3),
		tools.WithLookPath(func(file string) (string, error) {
			return `C:\bin\` + file, nil
		}),
		tools.WithRun(func(context.Context, string, ...string) (string, error) {
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				m := atomic.LoadInt32(&maxInFlight)
				if cur <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			return "1.0.0", nil
		}),
	)

	d.All(context.Background())
	if maxInFlight > 3 {
		t.Errorf("maxInFlight = %d, want <= 3", maxInFlight)
	}
	if maxInFlight < 2 {
		t.Errorf("maxInFlight = %d, want concurrency > 1 to prove fan-out happened", maxInFlight)
	}
}

func TestParseVersionLenient(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"git version 2.43.0.windows.1", "2.43.0"},
		{"Python 3.12.1", "3.12.1"},
		{"go version go1.26.0 windows/amd64", "1.26.0"},
		{"no digits here", ""},
	}
	// parseVersion is unexported; exercise it indirectly through Get.
	for _, c := range cases {
		d := tools.New(
			tools.WithLookPath(fakeLookPath(map[string]string{"git": "git"})),
			tools.WithRun(func(context.Context, string, ...string) (string, error) {
				return c.in, nil
			}),
		)
		got := d.Get(context.Background(), "git").Version
		if got != c.want {
			t.Errorf("parseVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNamesIsStable(t *testing.T) {
	want := []string{
		"winget", "scoop", "choco", "docker", "node", "npm", "pnpm", "yarn",
		"pip", "python", "cargo", "go", "git", "code", "wt",
	}
	got := tools.Names()
	if len(got) != len(want) {
		t.Fatalf("len(Names()) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Names()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func ExampleDetector_Get() {
	d := tools.New(
		tools.WithLookPath(func(string) (string, error) { return "", errors.New("not found") }),
		tools.WithRun(func(context.Context, string, ...string) (string, error) { return "", nil }),
	)
	tool := d.Get(context.Background(), "cargo")
	fmt.Println(tool.Found)
	// Output: false
}
