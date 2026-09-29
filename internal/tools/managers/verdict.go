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
	// VerdictCrashed means the program was stopped by Windows because it
	// crashed (an access violation, a corrupted heap, a failed safety
	// check).
	VerdictCrashed
	// VerdictNeedsAdmin means the upgrade can only be done with
	// administrator rights, for example an MSIX app that installs a
	// service (0x80073D28). The Update screen retries these once at the
	// end through the elevated worker.
	VerdictNeedsAdmin
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
	case VerdictCrashed:
		return "crashed"
	case VerdictNeedsAdmin:
		return "needs-admin"
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
	// Next is a plain-words next step ("Close Contoso App, then run the update
	// again."), or "" when there is nothing more to say than Text.
	Next string
	// Code is the exit code as the table knows it (unsigned), or 0 when
	// the verdict did not come from a code.
	Code uint32
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
	textInUse     = "in use, close {app} and retry"
	textPinned    = "pinned"
	textCancelled = "cancelled"
	textTimeout   = "timed out"
)

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
	return ExplainApp(manager, "", exitCode, lastLines)
}

// ExplainApp is [Explain] with the app's name, so a text that tells the user
// to close something can name it ("close Contoso App and retry"). An empty app
// reads as "it".
//
// Every exit code is looked up in the table in codes.go: a crash status such
// as 3221226505 (0xC0000409), a winget HRESULT, an MSIX error, or a Windows
// Installer code. The negative int32 and the unsigned forms of one code are
// the same code.
func ExplainApp(manager, app string, exitCode int, lastLines []string) Verdict {
	if exitCode == -1 {
		return Verdict{Kind: VerdictTimeout, Text: textTimeout, Next: "It did not finish in time. Try again, or update it by hand."}
	}

	if v, ok := explainByOutput(manager, lastLines); ok {
		// An in-use, held or pinned refusal reads the same whatever the
		// exit code; a restart or up-to-date hint only counts on success.
		switch v.Kind {
		case VerdictInUse, VerdictPinned:
			return v.forApp(app)
		case VerdictRestart, VerdictUpToDate:
			if exitCode == 0 {
				return v.forApp(app)
			}
		}
	}

	if exitCode != 0 {
		code := uint32(exitCode) //nolint:gosec // deliberate wrap: the int32 and uint32 forms of an exit code must compare equal.
		if row, ok := lookupCode(manager, code); ok {
			return row.verdict(app)
		}
		if manager == "winget" {
			if row, ok := explainFromOutput(lastLines); ok {
				return row.verdict(app)
			}
		}
		// An HRESULT or status reads as hex, the way every source writes
		// it; a small code is an installer's own.
		if exitCode < 0 || exitCode > 0xFFFF {
			return Verdict{Kind: VerdictFailed, Text: fmt.Sprintf("error 0x%08X", code)}
		}
	}
	if exitCode == 0 {
		return Verdict{Kind: VerdictOK, Text: textOK}
	}
	return Verdict{Kind: VerdictFailed, Text: fmt.Sprintf("exit code %d", exitCode)}
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
				return Verdict{Kind: VerdictPinned, Text: "held"}, true
			case strings.Contains(l, "is still running"), strings.Contains(l, "close all instances"):
				return Verdict{Kind: VerdictInUse, Text: textInUse}, true
			case strings.Contains(l, "latest version") && !strings.Contains(l, "updating"):
				// "git: 2.43.0 (latest version)", "Latest versions for
				// all apps are installed!"
				return Verdict{Kind: VerdictUpToDate, Text: textUpToDate}, true
			}
		case "npm":
			if strings.Contains(l, "ebusy") || strings.Contains(l, "resource busy or locked") {
				return Verdict{Kind: VerdictInUse, Text: textInUse}, true
			}
		case "choco":
			if strings.Contains(l, "is pinned") {
				return Verdict{Kind: VerdictPinned, Text: textPinned}, true
			}
		case "winget":
			if strings.Contains(l, "no available upgrade found") || strings.Contains(l, "no newer package versions are available") {
				return Verdict{Kind: VerdictUpToDate, Text: textUpToDate}, true
			}
		}
	}
	for _, line := range lastLines {
		if isRestartHint(strings.ToLower(line)) {
			return Verdict{Kind: VerdictRestart, Text: textRestart}, true
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

// forApp fills the app's name into a verdict that came from a line of
// output rather than a code row, and gives an in-use verdict its next step.
func (v Verdict) forApp(app string) Verdict {
	if v.Kind == VerdictInUse && v.Next == "" {
		v.Next = "Close {app}, including its tray icon, then run the update again."
	}
	v.Text = fill(v.Text, 0, app)
	v.Next = fill(v.Next, 0, app)
	return v
}
