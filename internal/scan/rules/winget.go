package rules

import "github.com/zubairbinshaukat/devpit/internal/scan"

// Winget returns the winget cleanups.
//
// winget has no command that empties its download cache, so unlike Docker and
// Chocolatey this one is a plain location rule: the installers it keeps sit
// in the App Installer package's local cache and are ordinary files. The
// package family name is fixed by Microsoft and is safe to hard-code.
func Winget() []scan.Rule {
	const appInstaller = `%LOCALAPPDATA%\Packages\Microsoft.DesktopAppInstaller_8wekyb3d8bbwe`

	return []scan.Rule{
		{
			Name:        "winget download cache",
			Kind:        scan.KindWinget,
			Locations:   []string{appInstaller + `\LocalState\downloadedInstallers`, appInstaller + `\TempState`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; winget downloads an installer again the next time it needs one.",
			Description: "Installers winget has already used.",
		},
	}
}
