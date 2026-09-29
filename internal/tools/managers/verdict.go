package managers

import (
	"fmt"
	"strings"
)

// VerdictKind is the broad outcome of upgrading one package, the part a
// caller branches on (count it as updated, offer a retry, suggest a
// restart). [Verdict.Text] carries the wording.
type VerdictKind int

const (
	// VerdictOK means the package was upgraded.
	VerdictOK VerdictKind = iota
	// VerdictRestart means the package was upgraded but needs a restart
	// (of the machine) to finish.
	VerdictRestart
	// VerdictUpToDate means there was nothing to do: the package was
	// already at its latest version by the time the upgrade ran.
	VerdictUpToDate
	// VerdictInUse means the upgrade could not replace the package's
	// files because the application is running.
	VerdictInUse
	// VerdictPinned means the manager refused because the package is
	// pinned (winget, Chocolatey) or held (Scoop).
	VerdictPinned
	// VerdictCancelled means someone cancelled the upgrade, typically by
	// declining the installer's own UAC prompt.
	VerdictCancelled
	// VerdictFailed is every other failure.
	VerdictFailed
	// VerdictTimeout means the step never finished: it was killed by its
	// timeout (or never started at all).
	VerdictTimeout
)

// String returns the kind's name, for logs and test failures.
func (k VerdictKind) String() string {
	switch k {
	case VerdictOK:
		return "ok"
	case VerdictRestart:
		return "restart"
	case VerdictUpToDate:
		return "up-to-date"
	case VerdictInUse:
		return "in-use"
	case VerdictPinned:
		return "pinned"
	case VerdictCancelled:
		return "cancelled"
	case VerdictFailed:
		return "failed"
	case VerdictTimeout:
		return "timeout"
	}
	return fmt.Sprintf("VerdictKind(%d)", int(k))
}

// Verdict is what one package upgrade came to, in a form fit for a
// summary row.
type Verdict struct {
	// Kind is the broad outcome.
	Kind VerdictKind
	// Text is short, lowercase and user-facing, e.g. "in use, close it
	// and retry" or "error 0x8A150049".
	Text string
}

// OK reports whether the verdict counts as a success for a summary tally:
// upgraded, upgraded pending a restart, or already current.
func (v Verdict) OK() bool {
	return v.Kind == VerdictOK || v.Kind == VerdictRestart || v.Kind == VerdictUpToDate
}

// The texts shared by more than one manager, so the same outcome reads the
// same whichever manager produced it.
const (
	textOK        = "updated"
	textRestart   = "updated, restart needed"
	textUpToDate  = "already up to date"
	textInUse     = "in use, close it and retry"
	textPinned    = "pinned"
	textCancelled = "cancelled"
	textTimeout   = "timed out"
)

// wingetCodes maps the winget HRESULTs an upgrade commonly ends with to a
// verdict, keyed by the code's uint32 form (see [Explain] for why). The
// full list is winget's doc/windows/package-manager/winget/returnCodes.md.
var wingetCodes = map[uint32]Verdict{
	0x8A150101: {VerdictInUse, textInUse},                                   // INSTALL_PACKAGE_IN_USE
	0x8A150103: {VerdictInUse, textInUse},                                   // INSTALL_FILE_IN_USE
	0x8A150111: {VerdictInUse, textInUse},                                   // INSTALL_PACKAGE_IN_USE_BY_APPLICATION
	0x8A150109: {VerdictRestart, textRestart},                               // INSTALL_REBOOT_REQUIRED_TO_FINISH
	0x8A15010A: {VerdictRestart, textRestart},                               // INSTALL_REBOOT_REQUIRED_FOR_INSTALL
	0x8A15010B: {VerdictRestart, textRestart},                               // INSTALL_REBOOT_INITIATED
	0x8A15002B: {VerdictUpToDate, textUpToDate},                             // UPDATE_NOT_APPLICABLE
	0x8A15010D: {VerdictUpToDate, textUpToDate},                             // INSTALL_ALREADY_INSTALLED
	0x8A150061: {VerdictUpToDate, textUpToDate},                             // PACKAGE_ALREADY_INSTALLED
	0x8A150068: {VerdictPinned, textPinned},                                 // PACKAGE_IS_PINNED
	0x8A15010C: {VerdictCancelled, textCancelled},                           // INSTALL_CANCELLED_BY_USER
	0x8A150102: {VerdictFailed, "another install is running"},               // INSTALL_INSTALL_IN_PROGRESS
	0x8A150105: {VerdictFailed, "disk full"},                                // INSTALL_DISK_FULL
	0x8A150107: {VerdictFailed, "no network"},                               // INSTALL_NO_NETWORK
	0x8A150049: {VerdictFailed, "msi install failed"},                       // MSI_INSTALL_FAILED
	0x8A150010: {VerdictFailed, "no installer for this machine"},            // NO_APPLICABLE_INSTALLER
	0x8A150050: {VerdictFailed, "installed version unknown, can't upgrade"}, // UPGRADE_VERSION_UNKNOWN
}

// Explain turns how one package upgrade ended (its exit code and the last
// lines [tools.StepResult] kept) into a [Verdict]. manager is the
// [Manager.Name] that ran it.
//
// winget reports failures as HRESULTs. A Windows exit code is a DWORD, and
// Go's ExitCode returns it as an int that may arrive either as the
// sign-extended int32 (0x8A150101 as -1978335999) or, depending on how it
// was relayed (the elevated worker, a test), as the plain uint32 value.
// Both are normalised to uint32 before lookup.
//
// An exit code of -1 is [tools.RunStep]'s "never finished": killed by its
// timeout, cancelled, or never started. It is reported as a timeout.
//
// A zero exit code is still checked for hints in lastLines, since winget
// and Chocolatey exit 0 on success that needs a restart, and Scoop exits 0
// on almost everything, including refusing to touch a held or running app.
// Hints are English phrases; under another display language the code alone
// decides.
func Explain(manager string, exitCode int, lastLines []string) Verdict {
	if exitCode == -1 {
		return Verdict{VerdictTimeout, textTimeout}
	}

	if v, ok := explainByOutput(manager, lastLines); ok {
		// An in-use, held or pinned refusal reads the same whatever the
		// exit code; a restart or up-to-date hint only counts on success.
		switch v.Kind {
		case VerdictInUse, VerdictPinned:
			return v
		case VerdictRestart, VerdictUpToDate:
			if exitCode == 0 {
				return v
			}
		}
	}

	switch manager {
	case "winget":
		code := uint32(exitCode) //nolint:gosec // deliberate wrap: the int32 and uint32 forms of an HRESULT must compare equal.
		if v, ok := wingetCodes[code]; ok {
			return v
		}
		// An HRESULT reads as hex, the way winget's docs and every search
		// result for it write it; a small code is an installer's own.
		if exitCode < 0 || exitCode > 0xFFFF {
			return Verdict{VerdictFailed, fmt.Sprintf("error 0x%08X", code)}
		}
	case "choco":
		// Chocolatey passes an installer's "reboot required" codes
		// through when its usePackageExitCodes feature is on (the
		// default).
		if exitCode == 1641 || exitCode == 3010 {
			return Verdict{VerdictRestart, textRestart}
		}
	}
	if exitCode == 0 {
		return Verdict{VerdictOK, textOK}
	}
	return Verdict{VerdictFailed, fmt.Sprintf("exit code %d", exitCode)}
}

// explainByOutput looks through lastLines for the English phrases that say
// more than an exit code does. ok is false when none matched.
func explainByOutput(manager string, lastLines []string) (Verdict, bool) {
	for _, line := range lastLines {
		l := strings.ToLower(line)
		switch manager {
		case "scoop":
			switch {
			case strings.Contains(l, "is held to version"):
				return Verdict{VerdictPinned, "held"}, true
			case strings.Contains(l, "is still running"), strings.Contains(l, "close all instances"):
				return Verdict{VerdictInUse, textInUse}, true
			case strings.Contains(l, "latest version") && !strings.Contains(l, "updating"):
				// "git: 2.43.0 (latest version)", "Latest versions for
				// all apps are installed!"
				return Verdict{VerdictUpToDate, textUpToDate}, true
			}
		case "npm":
			if strings.Contains(l, "ebusy") || strings.Contains(l, "resource busy or locked") {
				return Verdict{VerdictInUse, textInUse}, true
			}
		case "choco":
			if strings.Contains(l, "is pinned") {
				return Verdict{VerdictPinned, textPinned}, true
			}
		case "winget":
			if strings.Contains(l, "no available upgrade found") || strings.Contains(l, "no newer package versions are available") {
				return Verdict{VerdictUpToDate, textUpToDate}, true
			}
		}
	}
	for _, line := range lastLines {
		if isRestartHint(strings.ToLower(line)) {
			return Verdict{VerdictRestart, textRestart}, true
		}
	}
	return Verdict{}, false
}

// isRestartHint reports whether a lowercased output line asks for the
// machine to be restarted. A line asking only for a new shell ("restart
// your terminal to pick up PATH") is not one: that needs no reboot.
func isRestartHint(l string) bool {
	if !strings.Contains(l, "restart") && !strings.Contains(l, "reboot") {
		return false
	}
	for _, notMachine := range []string{"shell", "terminal", "console", "powershell"} {
		if strings.Contains(l, notMachine) {
			return false
		}
	}
	return true
}
