package importer

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// ProfileLine is claude-acc's line in a PowerShell profile:
//
//	# Claude Code Account Switcher
//	Invoke-Expression ((& 'C:\Users\you\.claude-switch\bin\claude-acc.exe' init pwsh) -join "`n")
//
// Only that line is kept; nothing else of the profile is.
type ProfileLine struct {
	File string
	// Line is 1-based.
	Line int
	Text string
	// HeaderLine is the line number of the "# Claude Code Account Switcher"
	// comment right above it, or 0.
	HeaderLine int
}

const claudeAccHeader = "# Claude Code Account Switcher"

// DefaultProfileFiles lists the PowerShell profiles claude-acc may have
// written to: the current-user profiles of PowerShell 7 and Windows
// PowerShell (console, all hosts, VS Code), under Documents, a OneDrive
// Documents, and the real Documents folder when it is elsewhere.
func DefaultProfileFiles(home string) []string {
	docs := []string{filepath.Join(home, "Documents"), filepath.Join(home, "OneDrive", "Documents")}
	if d := documentsFolder(); d != "" {
		docs = append([]string{d}, docs...)
	}
	var out []string
	seen := map[string]bool{}
	for _, d := range docs {
		for _, shell := range []string{"PowerShell", "WindowsPowerShell"} {
			for _, f := range []string{"Microsoft.PowerShell_profile.ps1", "profile.ps1", "Microsoft.VSCode_profile.ps1"} {
				p := filepath.Join(d, shell, f)
				if k := strings.ToLower(p); !seen[k] {
					seen[k] = true
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// isInitLine is claude-acc's own test (install.rs is_claude_acc_init_line),
// ignoring case as PowerShell does, and skipping comments.
func isInitLine(l string) bool {
	low := strings.ToLower(strings.TrimSpace(l))
	if strings.HasPrefix(low, "#") {
		return false
	}
	return strings.Contains(low, "claude-acc") && strings.Contains(low, "init") &&
		(strings.Contains(low, "invoke-expression") || strings.Contains(low, "eval") || strings.Contains(low, "iex "))
}

// findProfileLines reads the profiles, read only, and keeps claude-acc's
// init lines alone.
func findProfileLines(d Deps) ([]ProfileLine, []string) {
	files := d.ProfileFiles
	if files == nil {
		files = DefaultProfileFiles(d.Home)
	}
	var out []ProfileLine
	var probs []string
	for _, f := range files {
		if !exists(f) {
			continue
		}
		data, err := readSmall(f, 4<<20)
		if err != nil {
			probs = append(probs, fmt.Sprintf("The PowerShell profile %s could not be read to look for claude-acc's line.", f))
			continue
		}
		lines := strings.Split(string(data), "\n")
		for i, raw := range lines {
			l := strings.TrimRight(raw, "\r")
			if !isInitLine(l) {
				continue
			}
			pl := ProfileLine{File: f, Line: i + 1, Text: accounts.Scrub(strings.TrimSpace(l))}
			if i > 0 && strings.TrimSpace(strings.TrimRight(lines[i-1], "\r")) == claudeAccHeader {
				pl.HeaderLine = i
			}
			out = append(out, pl)
		}
	}
	return out, probs
}

// Step is one step of the guide to retire claude-acc.
type Step struct {
	Title  string
	Detail []string
}

// RemovalGuide is the plain steps to retire claude-acc safely once its
// accounts are in Devpit. Devpit itself edits no profile and deletes
// nothing here: the person does each step.
func RemovalGuide(f Found) []Step {
	c := f.ClaudeAcc
	if c == nil {
		return nil
	}
	steps := []Step{{
		Title: "Check that Devpit picks the same accounts",
		Detail: []string{
			"Run `devpit accounts verify`, or open Accounts in a few of the linked folders: Claude Code should show the account claude-acc used there.",
		},
	}}

	prof := Step{Title: "Remove claude-acc's line from your PowerShell profile"}
	if len(c.ProfileLines) == 0 {
		prof.Detail = append(prof.Detail, "Devpit found no claude-acc line in the usual PowerShell profiles. If you added one by hand elsewhere, remove it there.")
	}
	for _, pl := range c.ProfileLines {
		where := fmt.Sprintf("%s, line %d:", pl.File, pl.Line)
		if pl.HeaderLine > 0 {
			where = fmt.Sprintf("%s, lines %d-%d:", pl.File, pl.HeaderLine, pl.Line)
			prof.Detail = append(prof.Detail, where, "    "+claudeAccHeader, "    "+pl.Text)
			continue
		}
		prof.Detail = append(prof.Detail, where, "    "+pl.Text)
	}
	prof.Detail = append(prof.Detail, "Devpit does not edit your profile; open it with `notepad $PROFILE` and delete the line yourself.")
	steps = append(steps, prof)

	term := Step{Title: "Open a new terminal", Detail: []string{
		"claude-acc's line sets CLAUDE_CONFIG_DIR every time a terminal changes folder. A terminal started after the line is gone has no such variable.",
	}}
	if c.SessionVar != "" {
		term.Detail = append(term.Detail, "This terminal still has CLAUDE_CONFIG_DIR="+c.SessionVar+". Devpit's shim overrides it for Claude Code, but close this terminal to be sure.")
	}
	steps = append(steps, term)

	prog := Step{Title: "Remove the program", Detail: []string{
		"Delete " + filepath.Join(c.Dir, "bin") + " (claude-acc.exe, and claude-acc.old if it is there). If you added that folder to PATH yourself, take it out of PATH too.",
	}}
	steps = append(steps, prog)

	keep := Step{Title: "Keep the account folders", Detail: []string{
		"Devpit uses claude-acc's account folders where they are, so they still hold your sign-ins. Do not delete " + filepath.Join(c.Dir, "accounts") + " or anything in it while Devpit uses them.",
		"Do not run `claude-acc remove` for an account you imported: it moves the account's folder to the Recycle Bin, and deletes it outright with --purge or when the Recycle Bin cannot take it. Either way that account is signed out.",
		"The config and links files are not read by anything once claude-acc is gone; they can stay.",
	}}
	return append(steps, keep)
}
