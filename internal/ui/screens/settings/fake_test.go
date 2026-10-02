package settings

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
)

// fakeSkill is the skill engine over a list of places held in memory. It
// never looks at a real ~/.claude.
type fakeSkill struct {
	mu      sync.Mutex
	claude  bool
	targets []service.AgentTarget
	// fail makes install or remove of a file fail with this error.
	fail map[string]error
	// statusErr makes AgentStatus fail.
	statusErr error
	installs  []string
	removes   []string
}

func (f *fakeSkill) AgentStatus(context.Context) (service.AgentStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusErr != nil {
		return service.AgentStatus{}, f.statusErr
	}
	return service.AgentStatus{
		ClaudeCode: f.claude, Version: service.AgentSkillVersion,
		Targets: append([]service.AgentTarget(nil), f.targets...),
	}, nil
}

func (f *fakeSkill) AgentInstall(ts []service.AgentTarget) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var done []string
	var errs []error
	for _, t := range ts {
		if err := f.fail[t.File]; err != nil {
			errs = append(errs, err)
			continue
		}
		for i := range f.targets {
			if f.targets[i].File == t.File && (t.State == service.AgentCreate || t.State == service.AgentUpdate) {
				f.targets[i].State = service.AgentCurrent
				done = append(done, t.File)
				f.installs = append(f.installs, t.File)
			}
		}
	}
	return done, errors.Join(errs...)
}

func (f *fakeSkill) AgentRemove(files []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var done []string
	for _, file := range files {
		if err := f.fail[file]; err != nil {
			return done, err
		}
		for i := range f.targets {
			if strings.EqualFold(f.targets[i].File, file) {
				f.targets[i].State = service.AgentCreate
				done = append(done, file)
				f.removes = append(f.removes, file)
			}
		}
	}
	return done, nil
}

// Paths the fakes use.
const (
	skillDefault = `C:\Users\dev\.claude\skills\devpit\SKILL.md`
	skillWork    = `C:\Users\dev\.devpit\accounts\claude\work\skills\devpit\SKILL.md`
)

// skillWith is a fake with Claude Code installed and the default and work
// accounts in the given states.
func skillWith(def, work service.AgentTargetState) *fakeSkill {
	return &fakeSkill{claude: true, targets: []service.AgentTarget{
		{Account: "default", File: skillDefault, State: def},
		{Account: "work", File: skillWork, State: work},
	}}
}

// opener hands the fake to the screen.
func opener(f *fakeSkill) func() (SkillService, error) {
	return func() (SkillService, error) { return f, nil }
}

// noTick never fires, so a test never waits for "saved" to fade.
func noTick(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }

// newTest is the settings screen over a fake skill and a still clock, with
// the skill already looked at.
func newTest(f *fakeSkill) Model {
	m := NewWith(Options{Skill: opener(f), Tick: noTick, Version: "0.4.0"})
	if msg, ok := m.Init()().(skillLoadedMsg); ok {
		m.skill.apply(msg)
	}
	return m
}
