package app_test

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/settings"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// fakeSkill answers the Settings screen's look at the AI agent skill from
// memory, so opening Settings in a test never opens the real Accounts
// engine or reads a real ~/.claude.
type fakeSkill struct {
	claude  bool
	targets []service.AgentTarget
}

// Paths the fake uses.
const (
	skillDefault = `C:\Users\you\.claude\skills\devpit\SKILL.md`
	skillWork    = `C:\Users\you\.devpit\accounts\claude\work\skills\devpit\SKILL.md`
)

// skillAs is Claude Code with the default and work accounts in these states.
func skillAs(def, work service.AgentTargetState) fakeSkill {
	return fakeSkill{claude: true, targets: []service.AgentTarget{
		{Account: "default", File: skillDefault, State: def},
		{Account: "work", File: skillWork, State: work},
	}}
}

func (f fakeSkill) AgentStatus(context.Context) (service.AgentStatus, error) {
	return service.AgentStatus{
		ClaudeCode: f.claude, Version: service.AgentSkillVersion,
		Targets: append([]service.AgentTarget(nil), f.targets...),
	}, nil
}

// AgentInstall marks the places installed. The targets slice is shared by
// every copy of the fake, so the next look sees the change.
func (f fakeSkill) AgentInstall(ts []service.AgentTarget) ([]string, error) {
	var out []string
	for _, t := range ts {
		f.set(t.File, service.AgentCurrent)
		out = append(out, t.File)
	}
	return out, nil
}

func (f fakeSkill) AgentRemove(files []string) ([]string, error) {
	for _, file := range files {
		f.set(file, service.AgentCreate)
	}
	return files, nil
}

// set changes the state of the place with this file.
func (f fakeSkill) set(file string, st service.AgentTargetState) {
	for i := range f.targets {
		if f.targets[i].File == file {
			f.targets[i].State = st
		}
	}
}

// opens hands the fake out as the engine.
func (f fakeSkill) opens() func() (settings.SkillService, error) {
	return func() (settings.SkillService, error) { return f, nil }
}

// noTick is a clock that never fires.
func noTick(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }

// settingsWith is Settings over the given skill, with a clock that never
// fires.
func settingsWith(f fakeSkill) func() uictx.Screen {
	return func() uictx.Screen {
		return settings.NewWith(settings.Options{Skill: f.opens(), Tick: noTick, Version: "0.4.0"})
	}
}

// settingsScreen is Settings as every test opens it: Claude Code is here and
// the skill is not installed.
func settingsScreen() uictx.Screen {
	return settingsWith(skillAs(service.AgentCreate, service.AgentCreate))()
}
