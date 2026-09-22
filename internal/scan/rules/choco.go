package rules

import "github.com/zubairbinshaukat/devpit/internal/scan"

// Choco returns the Chocolatey cleanups.
//
// Chocolatey installs under %ProgramData%, which no unelevated process can
// write to, so every step here is marked NeedsElevation and runs in the
// hidden elevated worker rather than in the TUI. The declaration is here now
// so the Full Scan screen can list the step and say "needs administrator"
// instead of silently omitting it; the worker that runs it arrives in
// milestone 5.
func Choco() []scan.Command {
	return []scan.Command{
		{
			Name:           "Chocolatey package cache",
			Kind:           scan.KindChoco,
			Tier:           scan.TierSafe,
			Exe:            "choco",
			Args:           []string{"cache", "remove", "--expired"},
			NeedsElevation: true,
			Description:    "Installers Chocolatey downloaded and no longer needs.",
			RestoreHint:    "Nothing to do; Chocolatey downloads an installer again the next time it needs one.",
		},
	}
}
