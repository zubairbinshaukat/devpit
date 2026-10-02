package devpit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
)

// `devpit agent status --json` is what the installer reads; install and
// remove go through the same states.
func TestAgentStatusInstallAndRemoveCommands(t *testing.T) {
	w := newAccWorld(t)
	status := func() map[string]any {
		t.Helper()
		code, out, errOut := w.run("agent", "status", "--json")
		var j map[string]any
		if code != ExitOK || json.Unmarshal([]byte(out), &j) != nil {
			t.Fatalf("status: %d %s %s", code, out, errOut)
		}
		return j
	}
	j := status()
	if j["claude_code"] != true || j["state"] != string(service.AgentNotInstalled) || j["can_install"] != true {
		t.Fatalf("before: %v", j)
	}
	if code, _, _ := w.run("agent", "install"); code != ExitNeedsYes {
		t.Fatalf("install without --yes and no terminal: %d", code)
	}
	code, out, _ := w.run("agent", "install", "--yes")
	if code != ExitOK || !strings.Contains(out, "Wrote") {
		t.Fatalf("install: %d %s", code, out)
	}
	if j = status(); j["state"] != string(service.AgentInstalled) || j["can_install"] != false {
		t.Fatalf("after: %v", j)
	}
	// Again: nothing to write, still exit 0 (the installer runs it on update).
	if code, out, _ = w.run("agent", "install", "--yes"); code != ExitOK || !strings.Contains(out, "Nothing to write") || strings.Contains(out, "Wrote") {
		t.Fatalf("install twice: %d %s", code, out)
	}
	// An older Devpit's skill is an update.
	skill := filepath.Join(w.root, "home", ".claude", "skills", "devpit", "SKILL.md")
	old := strings.Replace(service.AgentSkill, "; skill version 2 -->", " -->", 1)
	if err := os.WriteFile(skill, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if j = status(); j["state"] != string(service.AgentOlder) {
		t.Fatalf("older: %v", j)
	}
	if code, out, _ = w.run("agent", "status"); code != ExitOK || !strings.Contains(out, "update available") || !strings.Contains(out, "version 1 → 2") {
		t.Fatalf("status text: %s", out)
	}
	if code, _, _ = w.run("agent", "install", "--yes"); code != ExitOK {
		t.Fatal("update")
	}
	if b, _ := os.ReadFile(skill); string(b) != service.AgentSkill {
		t.Fatal("the older skill was not refreshed")
	}
	if code, out, _ = w.run("agent", "remove", "--yes"); code != ExitOK || !strings.Contains(out, "Removed") {
		t.Fatalf("remove: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Dir(skill)); !os.IsNotExist(err) {
		t.Fatal("remove left the devpit folder")
	}
}

// Without Claude Code, install writes nothing and still exits 0 with the
// AGENTS.md section.
func TestAgentInstallWithoutClaudeCode(t *testing.T) {
	w := newAccWorld(t)
	w.opts.LookPath = func(name string) (string, error) { return "", os.ErrNotExist }
	code, out, _ := w.run("agent", "install", "--yes")
	if code != ExitOK || !strings.Contains(out, "Claude Code was not found") || !strings.Contains(out, "## Accounts (Devpit)") {
		t.Fatalf("install: %d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(w.root, "home", ".claude")); !os.IsNotExist(err) {
		t.Fatal("a ~/.claude folder was made")
	}
}

// devpit-shim.exe is missing (only devpit.exe was replaced): the accounts
// table says so with the fix and the page, and a change that needs a shim
// stops before writing, with the same fix.
func TestShimProgramMissingThroughTheCommands(t *testing.T) {
	w := newAccWorld(t)
	w.addClaude("work", "z@work.com")
	if err := os.Remove(filepath.Join(w.root, "devpit-shim.exe")); err != nil {
		t.Fatal(err)
	}
	fix := "Run the installer again, or put devpit-shim.exe next to devpit.exe."
	code, out, _ := w.run("accounts", "--folder", w.work)
	if code != ExitOK || !strings.Contains(out, "devpit-shim.exe is missing") || !strings.Contains(out, "Fix: "+fix) ||
		!strings.Contains(out, "Docs: https://devpit.zubyr.dev/docs/troubleshooting/") {
		t.Fatalf("accounts: %d\n%s", code, out)
	}
	code, out, errOut := w.run("claude", "use", "work", "--folder", w.work, "--yes")
	if code != ExitFailed || !strings.Contains(errOut, fix) || strings.Contains(out, "Saving the rule") {
		t.Fatalf("use: %d\n%s\n%s", code, out, errOut)
	}
	if len(w.store().Rules) != 0 {
		t.Fatal("a rule that cannot work was saved")
	}
}
