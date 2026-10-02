//go:build shots

package shots

import "github.com/zubairbinshaukat/devpit/internal/config"

// registry maps "screen/state", as written in the manifest, to the scene
// that draws it. Adding a shot for a screen that exists is one line here and
// one entry in shots.json.
var registry = map[string]scene{
	"install/list":    installFlow("", nil, nil, "Install"),
	"install/confirm": installFlow("", []string{"GitHub CLI", "pnpm", "ripgrep"}, []string{"enter"}, "Install 3"),
	"install/running": installFlow("pnpm", []string{"GitHub CLI", "pnpm", "ripgrep"}, []string{"enter", "y", "wait:49%", "clock:9s"}, "Downloading installer"),
	"install/summary": installFlow("", []string{"GitHub CLI", "pnpm", "ripgrep"}, []string{"enter", "y"}, "Installed"),

	"update/checking": updateFlow(nil, nil, nil, "Checking"),
	"update/results":  updateFlow(nil, nil, []string{"wait:Selected"}, "Selected"),
	"update/confirm":  updateFlow(nil, nil, []string{"wait:Selected", "enter"}, "Update 12"),
	"update/running":  updateFlow(map[string]outcome{"Git.Git": outHold}, nil, []string{"wait:Selected", "enter", "y"}, "MB / 62.1 MB"),
	"update/summary":  updateFlow(nil, nil, []string{"wait:Selected", "enter", "y"}, "Updated"),
	"update/summary-problems": updateFlow(map[string]outcome{
		"Microsoft.VisualStudioCode": outInUse, "Git.Git": outCrash, "Docker.DockerDesktop": outNeedsAdmin,
	}, declineAdmin, []string{"wait:Selected", "enter", "y"}, "Updated"),
	"update/admin-retry": updateFlow(map[string]outcome{"Docker.DockerDesktop": outNeedsAdmin}, holdAdmin,
		[]string{"wait:Selected", "enter", "y"}, "administrator"),

	"clean/menu": cleanFlow{found: projectJunk(), want: []string{"Resume interrupted deletes"}}.scene(),
	"clean/picker": cleanFlow{
		found: projectJunk(), steps: []string{"enter"}, want: []string{"D:\\work\\client-sites"},
		cfgFn: func(c *config.Config) { c.DefaultProjectsFolder = "" },
	}.scene(),
	"clean/scanning":             cleanFlow{found: projectJunk(), hold: 9, steps: []string{"enter"}, want: []string{"9 found"}}.scene(),
	"clean/results":              cleanFlow{found: projectJunk(), steps: []string{"enter"}, want: []string{"Selected:"}}.scene(),
	"clean/results-full":         cleanFlow{found: fullScan(), steps: []string{"down", "enter"}, want: []string{"Selected:"}}.scene(),
	"clean/results-node-modules": cleanFlow{found: nodeModulesOnly(), steps: []string{"enter"}, want: []string{"Selected:"}}.scene(),
	"clean/confirm-careful": cleanFlow{
		found: fullScan(), steps: []string{"down", "enter", "wait:Selected:", "a", "d"}, want: []string{"DELETE"},
	}.scene(),
	"clean/deleting": cleanFlow{
		found: projectJunk(), del: fakeClean(4, nil), steps: []string{"enter", "wait:Selected:", "d", "y"}, want: []string{"Deleting"},
	}.scene(),
	"clean/summary": cleanFlow{
		found: projectJunk(), steps: []string{"enter", "wait:Selected:", "d", "y"}, want: []string{"Freed"},
	}.scene(),
	"clean/summary-locked": cleanFlow{
		found: projectJunk(), steps: []string{"enter", "wait:Selected:", "d", "y"}, want: []string{"Code.exe"},
		del: fakeClean(0, map[string]string{
			shop + `\node_modules`:   "Code.exe",
			mobile + `\node_modules`: "node.exe",
		}),
	}.scene(),
	"clean/confirm": cleanFlow{found: projectJunk(), steps: []string{"enter", "wait:Selected:", "d"}, want: []string{"Delete"}}.scene(),
	"home/menu":     sceneHome,

	"firstrun/welcome": firstRun(0, "Welcome"),
	"firstrun/icons":   firstRun(1, "Step 2"),
	"firstrun/theme":   firstRun(2, "Step 3"),
	"firstrun/privacy": firstRun(3, "Step 4"),

	"settings/menu":        settingsAt(0, false, "Icons"),
	"settings/menu-stats":  settingsAt(settingsStats, false, "Usage stats"),
	"settings/menu-font":   settingsAt(settingsFont, false, "Icon font"),
	"settings/folders":     settingsAt(settingsFolders, true, "Projects"),
	"settings/never-touch": settingsAt(settingsNever, true, "Never"),
	"settings/dev-ports":   settingsAt(settingsPorts, true, "Dev ports"),
	"settings/font":        settingsAt(settingsFont, true, "Not installed"),
	"settings/probe":       settingsAt(settingsProbe, true, "render"),
	"settings/about":       settingsAt(settingsAbout, true, "Version"),
	"settings/agent-skill": settingsAt(settingsSkill, true, "AI agent skill for Claude Code"),

	"whatsnew/card": sceneWhatsNew,

	"network/menu":        networkAt(-1, nil, "My IP addresses"),
	"network/ip":          networkAt(0, nil, "203.0.113.42"),
	"network/ping-input":  networkAt(1, nil, "1.1.1.1"),
	"network/ping-done":   networkAt(1, []string{"enter"}, "avg 14ms"),
	"network/dns-confirm": networkAt(2, nil, "Flush the DNS"),

	"accounts/page":            accountsAt(nil, nil, "Convex"),
	"accounts/tool-claude":     accountsAt(nil, openRow(rowClaude), "Bring your Claude Code setup over"),
	"accounts/picker":          accountsAt(nil, openRow(rowClaude, "enter"), "Sign in with another account"),
	"accounts/scope":           accountsAt(nil, openRow(rowClaude, "enter", "enter"), "Another folder"),
	"accounts/preview":         accountsAt(nil, openRow(rowClaude, "enter", "up", "enter", "enter"), "Make this change?"),
	"accounts/done":            accountsAt(nil, openRow(rowClaude, "enter", "up", "enter", "enter", "y"), "back to Accounts"),
	"accounts/verify":          accountsAt(nil, []string{"v"}, "Everything checked is as it should be"),
	"accounts/browse":          accountsAt(nil, []string{"b"}, "ACCOUNTS SET HERE"),
	"accounts/git":             accountsAt(nil, openRow(rowGit), "Pushes as"),
	"accounts/git-preview":     accountsAt(nil, openRow(rowGit, "enter", "down", "enter", "enter"), "Make this change?"),
	"accounts/ssh-key":         accountsAt(nil, openRow(rowGit, "down", "down", "down", "down", "enter", "enter"), "copy the public key"),
	"accounts/just-once":       accountsAt(nil, openRow(rowClaude, "down", "down", "enter", "enter"), "Nothing is saved"),
	"accounts/claude-ask":      accountsAt(nil, openRow(rowClaude, claudeSetupSteps...), "Bring your Claude Code setup to"),
	"accounts/claude-items":    accountsAt(nil, openRow(rowClaude, append(claudeSetupSteps, "down", "down", "down", "enter")...), "Choose what"),
	"accounts/claude-conflict": accountsAt(withConflicts, openRow(rowClaude, append(claudeSetupSteps, "enter")...), "Some items are already in"),
	"accounts/import":          accountsImport("Found accounts already on this PC"),

	"portsnet/menu": parentMenu(keyPortsNet, portsNetLead),
	"apps/menu":     parentMenu(keyApps, appsLead),

	"ports/menu":         portsAt(nil, "Kill a port"),
	"ports/kill-port":    portsAt([]string{"enter"}, "3000"),
	"ports/kill-confirm": portsAt([]string{"enter", "enter"}, "node.exe"),
	"ports/busy-list":    portsAt([]string{"down", "enter"}, "5173"),
	"ports/node-list":    portsAt([]string{"down", "down", "enter"}, "node.exe"),

	"share/sharing": shareAt(shareHosting, shareHostIP),
	"share/copying": shareAt(shareCopying, "47.0 GB"),
}
