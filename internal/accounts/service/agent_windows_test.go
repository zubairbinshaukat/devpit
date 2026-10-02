//go:build windows

package service

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// junction makes link a junction to target with mklink /J (no administrator
// rights needed), or skips the test.
func junction(t *testing.T, link, target string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(link), 0o700))
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil { //nolint:gosec // test
		t.Skipf("mklink /J: %v %s", err, out)
	}
}

// Several Claude Code accounts: one shares the default account's whole
// skills folder, one links just the devpit skill (as Devpit's own sharing
// does). Both get the default's copy through the link: nothing is written
// through it, no link is replaced with a file, and remove takes out only the
// default's file and leaves the links.
func TestAgentSkillThroughSharedLinks(t *testing.T) {
	w := newWorld(t)
	shared := w.addAccount(accounts.ToolClaude, "shared", "z@shared.dev")
	perSkill := w.addAccount(accounts.ToolClaude, "perskill", "z@per.dev")
	defSkills := filepath.Join(w.home, ".claude", "skills")
	must(t, os.MkdirAll(filepath.Join(defSkills, "devpit"), 0o700))
	junction(t, filepath.Join(shared.Dir, "skills"), defSkills)
	junction(t, filepath.Join(perSkill.Dir, "skills", "devpit"), filepath.Join(defSkills, "devpit"))

	st := w.agentStatus()
	if st.Targets[0].State != AgentCreate || st.Targets[1].State != AgentShared || st.Targets[2].State != AgentShared ||
		st.Targets[1].SharedWith != accounts.DefaultName || !st.Targets[1].ThroughLink || !st.Targets[2].ThroughLink {
		t.Fatalf("targets = %+v", st.Targets)
	}
	done, err := w.s.AgentInstall(st.Targets)
	must(t, err)
	if len(done) != 1 || done[0] != w.defaultSkill() {
		t.Fatalf("written = %v", done)
	}
	st = w.agentStatus()
	if st.Overall() != AgentInstalled || st.CanInstall() || len(st.Removable()) != 1 {
		t.Fatalf("after install: %s %+v", st.Overall(), st.Targets)
	}
	// Even asked to, remove never deletes through a shared link.
	viaLink := filepath.Join(shared.Dir, "skills", "devpit", "SKILL.md")
	if removed, _ := w.s.AgentRemove([]string{viaLink}); len(removed) != 0 {
		t.Fatalf("removed through a link: %v", removed)
	}
	removed, err := w.s.AgentRemove(st.Removable())
	must(t, err)
	if len(removed) != 1 {
		t.Fatalf("removed = %v", removed)
	}
	for _, l := range []string{filepath.Join(shared.Dir, "skills"), filepath.Join(perSkill.Dir, "skills", "devpit")} {
		if !isLink(l) {
			t.Fatalf("%s is no longer a link", l)
		}
	}
}

// The default account's skills folder is itself a link (to a dotfiles
// folder, say): the skill is written where it really lives, once, and the
// status says it arrives through a link.
func TestAgentSkillWhenTheDefaultSkillsFolderIsALink(t *testing.T) {
	w := newWorld(t)
	dotfiles := filepath.Join(w.root, "dotfiles", "skills")
	must(t, os.MkdirAll(dotfiles, 0o700))
	junction(t, filepath.Join(w.home, ".claude", "skills"), dotfiles)

	st := w.agentStatus()
	if st.Targets[0].State != AgentCreate || !st.Targets[0].ThroughLink {
		t.Fatalf("targets = %+v", st.Targets)
	}
	_, err := w.s.AgentInstall(st.Targets)
	must(t, err)
	if readSkill(t, filepath.Join(dotfiles, "devpit", "SKILL.md")) != AgentSkill {
		t.Fatal("the skill is not where the link leads")
	}
	if !isLink(filepath.Join(w.home, ".claude", "skills")) {
		t.Fatal("the link was replaced")
	}
	if got := w.agentStatus().Overall(); got != AgentViaLink {
		t.Fatalf("overall = %s", got)
	}
	if _, err := w.s.AgentRemove(w.agentStatus().Removable()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dotfiles, "devpit")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("remove left the emptied devpit folder")
	}
	if !isLink(filepath.Join(w.home, ".claude", "skills")) {
		t.Fatal("remove took the link")
	}
}
