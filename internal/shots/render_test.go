//go:build shots

package shots

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/version"
)

// Environment variables that steer TestRender.
const (
	// envRun turns the test on. Without it TestRender skips, so a plain
	// `go test ./...` never renders anything.
	envRun = "DEVPIT_SHOTS"
	// envOut is the folder the ANSI frames are written to. It defaults to a
	// folder under the system temp directory.
	envOut = "DEVPIT_SHOTS_OUT"
	// envOnly limits the run to some entries: a comma-separated list of names.
	envOnly = "DEVPIT_SHOTS_ONLY"
	// envManifest is the folder holding shots*.json. It defaults to the docs
	// site, and exists so a scene can be tried with a scratch manifest.
	envManifest = "DEVPIT_SHOTS_MANIFEST_DIR"
	// envText prints each frame, escapes removed, to the test log. It is for
	// working on a scene without leaving the terminal.
	envText = "DEVPIT_SHOTS_TEXT"
)

// indexFile is the list of what a run wrote, read by the image script.
type indexFile struct {
	// Rendered are the frames written, in manifest order.
	Rendered []renderedFrame `json:"rendered"`
	// Skipped are the manifest entries no scene exists for yet.
	Skipped []skippedFrame `json:"skipped"`
}

// renderedFrame is one frame in the index.
type renderedFrame struct {
	Name  string `json:"name"`
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
	Theme string `json:"theme"`
}

// skippedFrame is one entry a run could not render, with the reason.
type skippedFrame struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// TestRender draws every manifest entry that has a scene and writes each as a
// truecolor ANSI file, plus index.json:
//
//	DEVPIT_SHOTS=1 go test ./internal/shots -run TestRender
//
// An entry whose screen has no scene yet is skipped with a clear message, so
// the manifest can list shots for a screen that is still being built and a
// later run picks them up. An entry whose screen has scenes but not this
// state is a mistake in the manifest and fails the test.
func TestRender(t *testing.T) {
	if os.Getenv(envRun) == "" {
		t.Skipf("set %s=1 to render the documentation screenshots", envRun)
	}

	out := os.Getenv(envOut)
	if out == "" {
		out = filepath.Join(os.TempDir(), "devpit-shots")
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatalf("creating %s: %v", out, err)
	}

	dir := os.Getenv(envManifest)
	if dir == "" {
		dir = docsSite
	}
	entries, err := loadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	only := selection(os.Getenv(envOnly))

	// Screens show the build identity; the screenshots show a release.
	oldV, oldC, oldD := version.Version, version.Commit, version.Date
	version.Version, version.Commit, version.Date = demoVersion, "3f9c2ab", "2026-09-29"
	t.Cleanup(func() { version.Version, version.Commit, version.Date = oldV, oldC, oldD })

	known := knownScreens()
	var idx indexFile
	for _, e := range entries {
		if len(only) > 0 && !only[e.Name] {
			continue
		}
		sc, ok := registry[e.key()]
		if !ok {
			if known[e.Screen] {
				t.Errorf("%s: the screen %q has no state %q; the states it has are %s",
					e.Name, e.Screen, e.State, statesOf(e.Screen))
				continue
			}
			reason := fmt.Sprintf("no scene for screen %q yet", e.Screen)
			t.Logf("SKIPPED %s: %s", e.Name, reason)
			idx.Skipped = append(idx.Skipped, skippedFrame{Name: e.Name, Reason: reason})
			continue
		}
		t.Run(e.Name, func(t *testing.T) {
			s := sc(t, e)
			frame := s.frame()
			if ferr := checkFrame(frame, e); ferr != nil {
				t.Fatalf("%v\n%s", ferr, ansi.Strip(frame))
			}
			if os.Getenv(envText) != "" {
				t.Logf("%s (%dx%d, %s)\n%s", e.Name, e.Cols, e.Rows, e.Theme, ansi.Strip(frame))
			}
			path := filepath.Join(out, e.Name+".ans")
			if werr := os.WriteFile(path, []byte(frame), 0o600); werr != nil {
				t.Fatalf("writing %s: %v", path, werr)
			}
			idx.Rendered = append(idx.Rendered, renderedFrame{Name: e.Name, Cols: e.Cols, Rows: e.Rows, Theme: e.Theme})
		})
	}

	raw, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "index.json"), append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("writing the index: %v", err)
	}
	t.Logf("rendered %d frames, skipped %d, into %s", len(idx.Rendered), len(idx.Skipped), out)
}

// selection parses a comma-separated list of names into a set.
func selection(list string) map[string]bool {
	out := map[string]bool{}
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out[n] = true
		}
	}
	return out
}

// knownScreens is the set of screens that have at least one scene.
func knownScreens() map[string]bool {
	out := map[string]bool{}
	for k := range registry {
		out[strings.SplitN(k, "/", 2)[0]] = true
	}
	return out
}

// statesOf lists the states a screen has, for an error message.
func statesOf(screen string) string {
	var states []string
	for k := range registry {
		if s, st, _ := strings.Cut(k, "/"); s == screen {
			states = append(states, st)
		}
	}
	sort.Strings(states)
	return strings.Join(states, ", ")
}

// checkFrame refuses a frame that is not exactly the size the manifest asked
// for, or that is empty, so a screen that drew nothing cannot become an image.
func checkFrame(frame string, e entry) error {
	lines := strings.Split(frame, "\n")
	if len(lines) != e.Rows {
		return fmt.Errorf("%s: the frame has %d lines, the manifest asks for %d", e.Name, len(lines), e.Rows)
	}
	widest := 0
	for _, l := range lines {
		widest = max(widest, ansi.StringWidth(l))
	}
	if widest == 0 {
		return fmt.Errorf("%s: the frame is empty", e.Name)
	}
	if widest > e.Cols {
		return fmt.Errorf("%s: a line is %d cells wide, the manifest asks for %d", e.Name, widest, e.Cols)
	}
	if bytes.Contains([]byte(frame), []byte("\x00")) {
		return fmt.Errorf("%s: the frame holds a NUL byte", e.Name)
	}
	return nil
}
