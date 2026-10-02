package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// skillV1 is the skill an older Devpit wrote: the marker without a version.
const skillV1 = "---\nname: devpit\ndescription: old\n---\n" + agentMarkerStem + " -->\n\n# Devpit accounts\n"

func writeSkill(t *testing.T, file, body string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(file), 0o700))
	must(t, os.WriteFile(file, []byte(body), 0o600))
}

func readSkill(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	must(t, err)
	return string(b)
}

func (w *world) defaultSkill() string {
	return filepath.Join(w.home, ".claude", "skills", "devpit", "SKILL.md")
}

func (w *world) agentStatus() AgentStatus {
	w.t.Helper()
	st, err := w.s.AgentStatus(context.Background())
	must(w.t, err)
	return st
}

// The skill keeps the rules an agent must follow, word for word: check with
// read commands, find the one docs page through llms.txt (Markdown by adding
// .md), fall back to --help offline, ask first, and never read account
// folders. Its frontmatter has only the standard fields, and its marker
// carries the version.
func TestSkillPinsTheAgentRules(t *testing.T) {
	for _, want := range []string{
		"devpit accounts verify --json", "devpit <tool> --json",
		"https://devpit.zubyr.dev/llms.txt", "ONE page", "add `.md` to the page URL",
		"devpit <command> --help",
		agentAskFirst, agentNoRead + " (",
		"Never pass `--yes` before the user agreed to that exact change.",
		"wrong account", "why Devpit did something",
	} {
		if !strings.Contains(AgentSkill, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
	if agentAskFirst != "Ask the user before any change, and only after they agree run it with `--yes`." ||
		agentNoRead != "Never read account folders or login files" {
		t.Fatal("the pinned sentences changed; that is a safety rule (docs/safety.md)")
	}
	front := strings.SplitN(strings.TrimPrefix(AgentSkill, "---\n"), "\n---\n", 2)[0]
	var keys []string
	for _, l := range strings.Split(front, "\n") {
		keys = append(keys, strings.SplitN(l, ":", 2)[0])
	}
	if strings.Join(keys, ",") != "name,description" {
		t.Fatalf("frontmatter keys = %v", keys)
	}
	if agentSkillVersionText != strconv.Itoa(AgentSkillVersion) || markedVersion([]byte(AgentSkill)) != AgentSkillVersion {
		t.Fatal("the marker's version and AgentSkillVersion differ")
	}
	if markedVersion([]byte(skillV1)) != 1 || markedVersion([]byte("mine")) != 0 {
		t.Fatal("an older marker must read as version 1, no marker as 0")
	}
	if !strings.HasPrefix(AgentMarker, agentMarkerStem) {
		t.Fatal("the marker must start with the stem older skills carry")
	}
}

// Install writes where it is missing, refreshes Devpit's own older skill,
// leaves a foreign one and a newer Devpit's one alone, and running it again
// changes nothing.
func TestAgentInstallRefreshesOnlyItsOwnOlderSkill(t *testing.T) {
	w := newWorld(t)
	work := w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	oss := w.addAccount(accounts.ToolClaude, "oss", "z@oss.dev")
	team := w.addAccount(accounts.ToolClaude, "team", "z@team.dev")
	writeSkill(t, w.defaultSkill(), skillV1)
	foreign := filepath.Join(work.Dir, "skills", "devpit", "SKILL.md")
	writeSkill(t, foreign, "my own devpit notes")
	newer := filepath.Join(oss.Dir, "skills", "devpit", "SKILL.md")
	newerBody := strings.Replace(AgentSkill, "skill version "+agentSkillVersionText, "skill version 99", 1)
	writeSkill(t, newer, newerBody)

	st := w.agentStatus()
	states := map[string]AgentTargetState{}
	for _, tg := range st.Targets {
		states[tg.Account] = tg.State
	}
	want := map[string]AgentTargetState{"default": AgentUpdate, "work": AgentForeign, "oss": AgentNewer, "team": AgentCreate}
	if fmt.Sprint(states) != fmt.Sprint(want) || !st.ClaudeCode || st.Overall() != AgentOlder || !st.CanInstall() {
		t.Fatalf("status = %+v (%s)", states, st.Overall())
	}
	done, err := w.s.AgentInstall(st.Targets)
	must(t, err)
	if len(done) != 2 {
		t.Fatalf("written = %v", done)
	}
	if readSkill(t, w.defaultSkill()) != AgentSkill || readSkill(t, filepath.Join(team.Dir, "skills", "devpit", "SKILL.md")) != AgentSkill {
		t.Fatal("the skill is not current where it was written")
	}
	if readSkill(t, foreign) != "my own devpit notes" || readSkill(t, newer) != newerBody {
		t.Fatal("a foreign or newer skill was touched")
	}
	st = w.agentStatus()
	if st.CanInstall() || st.Overall() != AgentInstalled {
		t.Fatalf("after install: %s", st.Overall())
	}
	// Twice changes nothing.
	again, err := w.s.AgentInstall(st.Targets)
	if err != nil || len(again) != 0 {
		t.Fatalf("second install wrote %v (%v)", again, err)
	}
	// Remove takes out Devpit's own (the newer one too) and nothing else.
	removed, err := w.s.AgentRemove(st.Removable())
	must(t, err)
	if len(removed) != 3 {
		t.Fatalf("removed = %v", removed)
	}
	if readSkill(t, foreign) != "my own devpit notes" {
		t.Fatal("remove touched a foreign skill")
	}
	if _, err := os.Stat(filepath.Dir(w.defaultSkill())); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the emptied devpit folder was left behind")
	}
}

// Without Claude Code on the PC there is nowhere to put the skill: the
// status says so and install writes nothing, not even ~/.claude.
func TestAgentWithoutClaudeCode(t *testing.T) {
	w := newWorld(t)
	w.s.Deps.LookPath = func(name string) (string, error) {
		return "", fmt.Errorf("%s: %w", name, accounts.ErrNotFoundTool)
	}
	st := w.agentStatus()
	if st.ClaudeCode || st.Overall() != AgentNoClaude || st.CanInstall() || st.ClaudeCodeWhy == "" {
		t.Fatalf("status = %+v", st)
	}
	if _, err := w.s.AgentInstall(st.Targets); !errors.Is(err, ErrNoClaudeCode) {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".claude")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("install made a ~/.claude folder without Claude Code")
	}
	// A ~/.claude folder alone is enough: Claude Code is never run to check.
	must(t, os.MkdirAll(filepath.Join(w.home, ".claude"), 0o700))
	if st := w.agentStatus(); !st.ClaudeCode || st.Overall() != AgentNotInstalled {
		t.Fatalf("with ~/.claude: %+v %s", st, st.Overall())
	}
	for _, c := range w.fake.Calls() {
		if c.Name == "claude" {
			t.Fatalf("claude was run: %v", c.Args)
		}
	}
}

// A devpit folder with no SKILL.md is Devpit's to fill when it is empty or
// holds only a write a crash cut short (which install sweeps up), and
// someone else's when it holds anything else.
func TestAgentEmptyFolderAndCrashLeftovers(t *testing.T) {
	w := newWorld(t)
	work := w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	must(t, os.MkdirAll(filepath.Dir(w.defaultSkill()), 0o700))
	leftover := filepath.Join(filepath.Dir(w.defaultSkill()), "SKILL.md.tmp-12345")
	must(t, os.WriteFile(leftover, []byte("half a ski"), 0o600))
	other := filepath.Join(work.Dir, "skills", "devpit", "notes.txt")
	writeSkill(t, other, "mine")

	st := w.agentStatus()
	if st.Targets[0].State != AgentCreate || st.Targets[1].State != AgentForeign || st.Targets[1].Note == "" {
		t.Fatalf("targets = %+v", st.Targets)
	}
	_, err := w.s.AgentInstall(st.Targets)
	must(t, err)
	if _, serr := os.Stat(leftover); !errors.Is(serr, os.ErrNotExist) {
		t.Fatal("the crash leftover is still there")
	}
	if _, serr := os.Stat(filepath.Join(work.Dir, "skills", "devpit", "SKILL.md")); !errors.Is(serr, os.ErrNotExist) {
		t.Fatal("a skill was written into someone else's devpit folder")
	}
	// A leftover next to Devpit's skill is tidied by remove too, and a
	// folder that still holds the person's own file is kept.
	must(t, os.WriteFile(leftover, []byte("half"), 0o600))
	mine := filepath.Join(filepath.Dir(w.defaultSkill()), "my-notes.md")
	must(t, os.WriteFile(mine, []byte("keep"), 0o600))
	removed, err := w.s.AgentRemove([]string{w.defaultSkill()})
	if err != nil || len(removed) != 1 {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if _, err := os.Stat(leftover); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("remove left the crash leftover")
	}
	if readSkill(t, mine) != "keep" {
		t.Fatal("remove took the person's own file")
	}
}

// A read-only Devpit skill is left as it is, with a plain error, while the
// other places are still done.
func TestAgentReadOnlySkillIsLeftAlone(t *testing.T) {
	w := newWorld(t)
	work := w.addAccount(accounts.ToolClaude, "work", "z@work.com")
	writeSkill(t, w.defaultSkill(), skillV1)
	must(t, os.Chmod(w.defaultSkill(), 0o444))
	t.Cleanup(func() { _ = os.Chmod(w.defaultSkill(), 0o600) })

	st := w.agentStatus()
	done, err := w.s.AgentInstall(st.Targets)
	if err == nil || !strings.Contains(err.Error(), "read-only") || len(done) != 1 || !strings.HasPrefix(done[0], work.Dir) {
		t.Fatalf("install: %v %v", done, err)
	}
	if readSkill(t, w.defaultSkill()) != skillV1 {
		t.Fatal("a read-only skill was replaced")
	}
	removed, err := w.s.AgentRemove(w.agentStatus().Removable())
	if err == nil || !strings.Contains(err.Error(), "read-only") || len(removed) != 1 {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if readSkill(t, w.defaultSkill()) != skillV1 {
		t.Fatal("a read-only skill was removed")
	}
}
