package rules

import "github.com/zubairbinshaukat/devpit/internal/scan"

// Editors returns the caches editors, IDEs and browser-driving test runners
// leave behind.
//
// Nothing here touches settings, keybindings, extensions or indexes of a
// user's own projects. A JetBrains "caches" folder is regenerated on the next
// project open; a JetBrains "config" folder is the user's setup, and it is
// not in this list.
func Editors() []scan.Rule {
	return []scan.Rule{
		{
			Name: "vs code caches",
			Kind: scan.KindEditorCache,
			Locations: []string{
				`%APPDATA%\Code\Cache`,
				`%APPDATA%\Code\CachedData`,
				`%APPDATA%\Code\CachedExtensionVSIXs`,
				`%APPDATA%\Code\Code Cache`,
				`%APPDATA%\Code\GPUCache`,
				`%APPDATA%\Code\logs`,
				`%APPDATA%\Code - Insiders\Cache`,
				`%APPDATA%\Code - Insiders\CachedData`,
			},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; VS Code rebuilds its caches the next time it starts.",
			Description: "VS Code's startup and GPU caches.",
		},
		{
			Name:        "vs code cli",
			Kind:        scan.KindEditorCache,
			Locations:   []string{`%USERPROFILE%\.vscode-cli`, `%USERPROFILE%\.vscode-server`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; the CLI and remote server download themselves again when used.",
			Description: "Downloaded VS Code CLI and remote server builds.",
		},
		{
			Name: "jetbrains caches",
			Kind: scan.KindEditorCache,
			Locations: []string{
				`%LOCALAPPDATA%\JetBrains\*\caches`,
				`%LOCALAPPDATA%\JetBrains\*\index`,
				`%LOCALAPPDATA%\JetBrains\*\log`,
			},
			Tier:        scan.TierReview,
			RestoreHint: "Nothing to do; the IDE reindexes each project the next time you open it, which takes a few minutes.",
			Description: "IntelliJ, Rider, PyCharm and WebStorm indexes and logs.",
		},
		{
			Name:        "visual studio cache",
			Kind:        scan.KindEditorCache,
			Locations:   []string{`%LOCALAPPDATA%\Microsoft\VisualStudio\*\ComponentModelCache`},
			Tier:        scan.TierSafe,
			RestoreHint: "Nothing to do; Visual Studio rebuilds the MEF cache on the next start.",
			Description: "Visual Studio's component model cache.",
		},
		{
			Name:        "playwright browsers",
			Kind:        scan.KindEditorCache,
			Locations:   []string{`%LOCALAPPDATA%\ms-playwright`},
			Tier:        scan.TierReview,
			RestoreHint: "Run npx playwright install to download the browsers again.",
			Description: "Browser builds Playwright downloaded for tests.",
		},
		{
			Name:        "cypress binaries",
			Kind:        scan.KindEditorCache,
			Locations:   []string{`%LOCALAPPDATA%\Cypress\Cache`},
			Tier:        scan.TierReview,
			RestoreHint: "Run npx cypress install to download the runner again.",
			Description: "Cypress test runner binaries.",
		},
		{
			Name:        "puppeteer browsers",
			Kind:        scan.KindEditorCache,
			Locations:   []string{`%USERPROFILE%\.cache\puppeteer`},
			Tier:        scan.TierReview,
			RestoreHint: "Run npx puppeteer browsers install chrome to download it again.",
			Description: "Chrome builds Puppeteer downloaded for tests.",
		},
		{
			Name:        "android caches",
			Kind:        scan.KindEditorCache,
			Locations:   []string{`%USERPROFILE%\.android\cache`, `%LOCALAPPDATA%\Google\AndroidStudio*\caches`},
			Tier:        scan.TierReview,
			RestoreHint: "Nothing to do; Android Studio and the SDK tools rebuild their caches when used.",
			Description: "Android Studio and SDK caches.",
		},
	}
}
