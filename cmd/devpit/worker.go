package devpit

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
)

// Flag names of the reserved elevated-worker interface. They are declared here
// so the name is fixed from milestone 0 and every later version can recognise
// a worker launched by an older binary.
const (
	// FlagElevatedWorker puts the process into worker mode. It is hidden
	// because no user should ever type it: Devpit's TUI never runs elevated,
	// and the worker is only ever started by Devpit itself via
	// ShellExecuteExW with the "runas" verb.
	FlagElevatedWorker = "elevated-worker"
	// FlagPipe names the pipe the worker streams JSON lines back over.
	FlagPipe = "pipe"
)

// registerWorkerFlags attaches the reserved worker flags to the root command
// and hides them from help. internal/elevate's own copies of these two flag
// names (in launch_windows.go) have to match these exactly; that package
// cannot import this one to share the constants, since this package imports
// it.
func registerWorkerFlags(root *cobra.Command, flags *rootFlags) {
	fs := root.PersistentFlags()
	fs.BoolVar(&flags.elevatedWorker, FlagElevatedWorker, false,
		"internal: run as the elevated helper process")
	fs.StringVar(&flags.pipe, FlagPipe, "",
		"internal: named pipe the elevated helper reports over")

	for _, name := range []string{FlagElevatedWorker, FlagPipe} {
		// MarkHidden only fails when the flag does not exist, which cannot
		// happen two lines after it was defined.
		_ = fs.MarkHidden(name)
	}
}

// runElevatedWorker is the hidden worker's entire job: `devpit
// --elevated-worker --pipe <name>`. It never opens the TUI, never touches
// color output, and imports nothing from internal/ui or internal/app —
// docs/safety.md rule 18 requires the process UAC actually elevates to run
// no UI code at all, and internal/elevate.Serve enforces that on its side
// too by not importing them either.
func runElevatedWorker(ctx context.Context, flags *rootFlags) error {
	if flags.pipe == "" {
		return fmt.Errorf("devpit: --%s requires --%s <name>", FlagElevatedWorker, FlagPipe)
	}
	return elevate.Serve(ctx, flags.pipe, elevate.DefaultExecutor{})
}
