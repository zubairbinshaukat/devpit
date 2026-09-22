package rules

import "github.com/zubairbinshaukat/devpit/internal/scan"

// WinTemp returns the Windows temporary and diagnostic folders.
//
// %WINDIR%\Temp is deliberately absent. It needs administrator rights, the
// TUI never runs elevated, and the scanner's own filters refuse anything
// under the Windows directory on purpose. It belongs to the elevated worker
// in milestone 5 and will arrive as a Command, not as a rule.
//
// The user's own %TEMP% is Review rather than Safe: installers, editors and
// language servers keep live files there, and a folder in use is a folder the
// delete engine will find locked. Nothing is lost by asking first.
func WinTemp() []scan.Rule {
	return []scan.Rule{
		{
			Name:        "user temp",
			Kind:        scan.KindWinTemp,
			Locations:   []string{`%TEMP%`, `%LOCALAPPDATA%\Temp`},
			Tier:        scan.TierReview,
			RestoreHint: "Nothing to restore; anything still in use stays, because Windows keeps it locked.",
			Description: "Your temporary files folder.",
		},
		{
			Name:        "crash dumps",
			Kind:        scan.KindWinTemp,
			Locations:   []string{`%LOCALAPPDATA%\CrashDumps`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to restore; these are post-mortem dumps of programs that already crashed.",
			Description: "Memory dumps left by crashed programs.",
		},
		{
			Name: "windows error reports",
			Kind: scan.KindWinTemp,
			Locations: []string{
				`%LOCALAPPDATA%\Microsoft\Windows\WER\ReportQueue`,
				`%LOCALAPPDATA%\Microsoft\Windows\WER\ReportArchive`,
			},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to restore; these reports have already been sent or expired.",
			Description: "Queued and archived Windows error reports.",
		},
		{
			Name:      "internet cache",
			Kind:      scan.KindWinTemp,
			Locations: []string{`%LOCALAPPDATA%\Microsoft\Windows\INetCache`},
			Tier:      scan.TierSafe,
			RestoreHint: "Nothing to restore; Windows and Internet Explorer components refill it " +
				"as they are used.",
			Description: "Windows' web cache.",
		},
		{
			Name:        "shader caches",
			Kind:        scan.KindWinTemp,
			Locations:   []string{`%LOCALAPPDATA%\D3DSCache`, `%LOCALAPPDATA%\NVIDIA\DXCache`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to restore; the graphics driver rebuilds shaders on first use.",
			Description: "Compiled GPU shader caches.",
		},
		{
			Name:        "delivery optimisation",
			Kind:        scan.KindWinTemp,
			Locations:   []string{`%LOCALAPPDATA%\Microsoft\Windows\DeliveryOptimization\Cache`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to restore; Windows Update re-downloads anything it still needs.",
			Description: "Windows Update peer-to-peer download cache.",
		},
	}
}
