package settings

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
)

// SkillService is the part of the Accounts engine the AI agent skill row and
// screen use. *service.Service is one; tests and the screenshot renderer
// pass a fake, so no test ever looks at a real ~/.claude.
type SkillService interface {
	// AgentStatus says whether Claude Code is here and, for every place the
	// skill goes, what is there now. It only reads.
	AgentStatus(ctx context.Context) (service.AgentStatus, error)
	// AgentInstall writes the skill to the targets whose state is create or
	// update, and returns the files it wrote.
	AgentInstall(targets []service.AgentTarget) ([]string, error)
	// AgentRemove takes out the given files when they are Devpit's own.
	AgentRemove(files []string) ([]string, error)
}

// The real engine is one.
var _ SkillService = (*service.Service)(nil)

// openSkillService opens the real engine. It runs in a command, never on the
// render path: opening finishes or rolls back an account change a crash left
// half done, the same as opening Accounts does.
func openSkillService() (SkillService, error) { return service.Open(service.Options{}) }

// Options build the settings screen. Every zero field is the real thing.
type Options struct {
	// Skill opens the engine behind the AI agent skill row; the real Accounts
	// engine when nil.
	Skill func() (SkillService, error)
	// Tick schedules a delayed message (the "saved" mark fading); tea.Tick
	// when nil. Tests pass one that never fires.
	Tick func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd
	// Version is the running version for About and What's new;
	// version.Short() when "".
	Version string
}

// skillSession is what the settings list and the skill screen know about the
// skill. They share it, so an install made on the skill screen is on the
// row the moment the person comes back.
type skillSession struct {
	open func() (SkillService, error)
	svc  SkillService
	// loaded is false until the first look has come back.
	loaded bool
	status service.AgentStatus
	err    error
}

// skillLoadedMsg is the result of a look at the skill.
type skillLoadedMsg struct {
	sess   *skillSession
	svc    SkillService
	status service.AgentStatus
	err    error
}

// load opens the engine when needed and looks at the skill, off the render
// path.
func (s *skillSession) load() tea.Cmd {
	open, svc := s.open, s.svc
	return func() tea.Msg {
		if svc == nil {
			var err error
			if svc, err = open(); err != nil {
				return skillLoadedMsg{sess: s, err: err}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		st, err := svc.AgentStatus(ctx)
		return skillLoadedMsg{sess: s, svc: svc, status: st, err: err}
	}
}

// apply stores a look's result.
func (s *skillSession) apply(msg skillLoadedMsg) {
	if msg.sess != s {
		return
	}
	s.loaded = true
	if msg.svc != nil {
		s.svc = msg.svc
	}
	s.status, s.err = msg.status, msg.err
}

// SkillOffer looks at the skill for the What's new card: show is true when
// Claude Code is on this PC and Devpit's skill is missing there or older
// than this Devpit's (older). It only reads, and a failed look offers
// nothing. open is the real engine when nil.
func SkillOffer(open func() (SkillService, error)) (show, older bool) {
	if open == nil {
		open = openSkillService
	}
	svc, err := open()
	if err != nil {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := svc.AgentStatus(ctx)
	if err != nil || !st.ClaudeCode {
		return false, false
	}
	switch st.Overall() {
	case service.AgentNotInstalled:
		return true, false
	case service.AgentOlder:
		return true, true
	}
	return false, false
}
