package docsgen

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// fakeTree is a tiny command tree with one of each thing the renderer treats
// specially: a reserved command, a hidden flag, a pipe in a usage string and
// an example. It is built here rather than taken from cmd/devpit so the test
// pins the renderer's rules and not the app's current commands.
func fakeTree() *cobra.Command {
	root := &cobra.Command{Use: "tool", Short: "Root tool", Version: "1"}
	root.PersistentFlags().Bool("secret", false, "internal only")
	_ = root.PersistentFlags().MarkHidden("secret")

	run := &cobra.Command{
		Use:     "run [path]",
		Short:   "Run it (not implemented yet)",
		Example: "tool run .",
		Run:     func(*cobra.Command, []string) {},
	}
	run.Flags().String("mode", "fast", "a|b mode")

	live := &cobra.Command{Use: "live", Short: "Works today", Run: func(*cobra.Command, []string) {}}
	root.AddCommand(run, live)
	return root
}

// TestRenderMarksReservedCommands pins that the not-implemented marker turns
// into a visible notice and is not left in the description text.
func TestRenderMarksReservedCommands(t *testing.T) {
	page := Render(fakeTree(), Options{})
	if !strings.Contains(page, ":::caution[Not available yet]") {
		t.Error("reserved command has no notice")
	}
	if strings.Contains(page, "(not implemented yet)") {
		t.Error("raw marker leaked into the page")
	}
	live := page[strings.Index(page, "## `tool live`"):]
	if end := strings.Index(live[3:], "\n## "); end >= 0 {
		live = live[:end+3]
	}
	if strings.Contains(live, "Not available yet") {
		t.Error("a working command was marked reserved")
	}
}

// TestRenderHidesHiddenFlagsAndEscapesCells pins that hidden flags never
// appear and that a pipe in usage text cannot break a table row.
func TestRenderHidesHiddenFlagsAndEscapesCells(t *testing.T) {
	page := Render(fakeTree(), Options{})
	if strings.Contains(page, "--secret") {
		t.Error("hidden flag is on the page")
	}
	if !strings.Contains(page, `a\|b mode`) {
		t.Error("pipe in a table cell is not escaped")
	}
}

// TestRenderShowsExamplesAndEnv pins that examples and the environment table
// are written when present.
func TestRenderShowsExamplesAndEnv(t *testing.T) {
	page := Render(fakeTree(), Options{Env: []EnvVar{{Name: "TOOL_X", Effect: "does x"}}})
	for _, want := range []string{"**Examples**", "tool run .", "## Environment variables", "| `TOOL_X` | does x |"} {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}
