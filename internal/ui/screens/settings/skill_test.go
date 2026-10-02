package settings

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// skillStep feeds one message to the skill screen.
func skillStep(t *testing.T, s skillScreen, ctx uictx.Context, msg tea.Msg) (skillScreen, tea.Cmd) {
	t.Helper()
	next, cmd := s.Update(msg, ctx)
	ns, ok := next.(skillScreen)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return ns, cmd
}

// drain runs every command the screen returns, feeding its messages back,
// the way the program loop would.
func drain(t *testing.T, s skillScreen, ctx uictx.Context, cmd tea.Cmd) skillScreen {
	t.Helper()
	queue := flattenCmd(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		var next tea.Cmd
		s, next = skillStep(t, s, ctx, msg)
		queue = append(queue, flattenCmd(next)...)
	}
	return s
}

func skillView(s skillScreen, ctx uictx.Context) string { return ansi.Strip(s.View(ctx)) }

// The screen says what the skill does, what it never does and where it
// goes, before anything else.
func TestSkillScreenExplainsItself(t *testing.T) {
	ctx := sized(config.Default(), icons.Unicode(), 100, 30)
	out := skillView(newSkillScreen(sessionOf(skillWith(service.AgentCreate, service.AgentCreate))), ctx)
	for _, want := range []string{
		"What it lets Claude Code do", "What it never does",
		"Never changes anything without your yes", "Never reads account folders, login files or tokens",
		"Where it goes", skillDefault, "work", "○ not installed", "[ Install ]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the screen lacks %q:\n%s", want, out)
		}
	}
}

// Install asks first and the answer starts on No: Enter alone installs
// nothing. Yes writes each place as a live row and ends on a done card,
// and the shared session (the Settings row) learns the new state.
func TestSkillInstallAsksFirst(t *testing.T) {
	ctx := sized(config.Default(), icons.Unicode(), 100, 30)
	f := skillWith(service.AgentCreate, service.AgentCreate)
	s := newSkillScreen(sessionOf(f))

	s, _ = skillStep(t, s, ctx, keyPress("enter"))
	if s.phase != skillConfirming || s.confirm.Answer() != confirm.AnswerNo {
		t.Fatalf("Enter on Install: phase %v, answer %v; want a confirm on No", s.phase, s.confirm.Answer())
	}
	if !strings.Contains(skillView(s, ctx), "Install the Devpit skill for Claude Code?") {
		t.Errorf("no question:\n%s", skillView(s, ctx))
	}
	s, cmd := skillStep(t, s, ctx, keyPress("enter"))
	s = drain(t, s, ctx, cmd)
	if s.phase != skillLooking || len(f.installs) != 0 {
		t.Fatalf("Enter on the No default: phase %v, installs %v", s.phase, f.installs)
	}

	s, _ = skillStep(t, s, ctx, keyPress("enter"))
	// y answers through a message; the run starts on it.
	s, cmd = skillStep(t, s, ctx, keyPress("y"))
	s = drain(t, s, ctx, cmd)
	if !slices.Equal(f.installs, []string{skillDefault, skillWork}) {
		t.Fatalf("installed %v", f.installs)
	}
	out := skillView(s, ctx)
	if s.phase != skillDone || !strings.Contains(out, "Done in 2 places") || strings.Count(out, "✓ default")+strings.Count(out, "✓ work") != 2 {
		t.Fatalf("after the run:\n%s", out)
	}
	if s.sess.status.Overall() != service.AgentInstalled {
		t.Errorf("the shared session still says %q", s.sess.status.Overall())
	}
	if r := skillRow(s.sess); r.value != "installed" {
		t.Errorf("the Settings row says %q", r.value)
	}
}

// An older skill offers Update and Remove; x removes after a yes.
func TestSkillUpdateAndRemove(t *testing.T) {
	ctx := sized(config.Default(), icons.Unicode(), 100, 30)
	f := skillWith(service.AgentCurrent, service.AgentUpdate)
	s := newSkillScreen(sessionOf(f))
	if !slices.Equal(s.actions, []skillAction{actUpdate, actRemove}) {
		t.Fatalf("actions %v", s.actions)
	}
	out := skillView(s, ctx)
	for _, want := range []string{"Update available", "! older", "● installed", "[ Update ]", "[ Remove ]"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	s, _ = skillStep(t, s, ctx, keyPress("x"))
	if !strings.Contains(skillView(s, ctx), "Remove the Devpit skill?") {
		t.Fatalf("x did not ask:\n%s", skillView(s, ctx))
	}
	s, cmd := skillStep(t, s, ctx, keyPress("y"))
	s = drain(t, s, ctx, cmd)
	if !slices.Equal(f.removes, []string{skillDefault, skillWork}) || s.phase != skillDone {
		t.Fatalf("removed %v, phase %v", f.removes, s.phase)
	}
	if !strings.Contains(skillView(s, ctx), "Removed from 2 places") {
		t.Errorf("no done card:\n%s", skillView(s, ctx))
	}
}

// A devpit skill someone else wrote is never touched: no button, and the
// screen says why.
func TestSkillInTheWayCannotInstall(t *testing.T) {
	ctx := sized(config.Default(), icons.Unicode(), 100, 30)
	f := skillWith(service.AgentForeign, service.AgentForeign)
	f.targets[0].Note = "a devpit skill Devpit did not write."
	s := newSkillScreen(sessionOf(f))
	if len(s.actions) != 0 {
		t.Fatalf("actions %v", s.actions)
	}
	out := skillView(s, ctx)
	for _, want := range []string{"A different skill called devpit is already there", "✗ not Devpit's", "A devpit skill Devpit did not write."} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if _, cmd := skillStep(t, s, ctx, keyPress("i")); cmd != nil {
		t.Error("i did something")
	}
}

// Without Claude Code, or when the look failed, the screen says so in
// plain words and offers nothing.
func TestSkillNothingToDo(t *testing.T) {
	ctx := sized(config.Default(), icons.Unicode(), 100, 30)
	for _, c := range []struct {
		f    *fakeSkill
		want string
	}{
		{&fakeSkill{claude: false}, "Claude Code is not installed on this PC"},
		{&fakeSkill{statusErr: errors.New("access is denied")}, "Windows would not let Devpit write there"},
	} {
		s := newSkillScreen(sessionOf(c.f))
		if len(s.actions) != 0 || !strings.Contains(skillView(s, ctx), c.want) {
			t.Errorf("actions %v:\n%s", s.actions, skillView(s, ctx))
		}
	}
}

// A place that fails says why in plain words; the others still finish.
func TestSkillFailureInPlainWords(t *testing.T) {
	ctx := sized(config.Default(), icons.Unicode(), 100, 30)
	f := skillWith(service.AgentCreate, service.AgentCreate)
	f.fail = map[string]error{skillWork: errors.New(skillWork + " is read-only, so Devpit left it as it is")}
	s := newSkillScreen(sessionOf(f))
	s, _ = skillStep(t, s, ctx, keyPress("enter"))
	s, cmd := skillStep(t, s, ctx, keyPress("y"))
	s = drain(t, s, ctx, cmd)
	out := skillView(s, ctx)
	for _, want := range []string{"✗ work", "read-only", "Done in 1 place", "1 place did not work"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

// The screen is busy while it writes, so Esc cannot leave halfway.
func TestSkillBusyWhileRunning(t *testing.T) {
	ctx := sized(config.Default(), icons.Unicode(), 100, 30)
	s := newSkillScreen(sessionOf(skillWith(service.AgentCreate, service.AgentCreate)))
	s, _ = skillStep(t, s, ctx, keyPress("enter"))
	s, _ = skillStep(t, s, ctx, confirm.AnsweredMsg{ID: "skill", Answer: confirm.AnswerYes})
	if !s.Busy() || s.phase != skillRunning {
		t.Fatalf("phase %v busy %v", s.phase, s.Busy())
	}
	if !strings.Contains(skillView(s, ctx), "Installing") {
		t.Errorf("no live rows:\n%s", skillView(s, ctx))
	}
}

// A click on a button asks; ← and → move between buttons.
func TestSkillButtonsByMouseAndArrows(t *testing.T) {
	ctx := sized(config.Default(), icons.Unicode(), 100, 30)
	s := newSkillScreen(sessionOf(skillWith(service.AgentCurrent, service.AgentUpdate)))
	s, _ = skillStep(t, s, ctx, keyPress("right"))
	if s.pick != 1 {
		t.Fatalf("→ picked %d", s.pick)
	}
	lines, at := s.layout(ctx)
	x := strings.Index(ansi.Strip(lines[at.buttons]), "[ Update ]") + 2
	s, _ = skillStep(t, s, ctx, tea.MouseClickMsg{X: x, Y: ctx.BodyTop + at.buttons, Button: tea.MouseLeft})
	if s.phase != skillConfirming || s.action != actUpdate {
		t.Errorf("clicking Update: phase %v action %v", s.phase, s.action)
	}
}

// No line is wider than the terminal in any state, tier or width, with
// long account folders.
func TestSkillScreenFits(t *testing.T) {
	long := skillWith(service.AgentForeign, service.AgentUpdate)
	long.targets[1].File = `C:\Users\someone-with-a-long-name\.devpit\accounts\claude\a-very-long-account-name\skills\devpit\SKILL.md`
	long.targets[0].Note = "a devpit folder without Devpit's SKILL.md, holding other files."
	for _, f := range append(skillStates(), long) {
		for _, set := range []icons.Set{icons.Unicode(), icons.ASCII()} {
			for w := 60; w <= 200; w += 5 {
				ctx := sized(config.Default(), set, w, 30)
				s := newSkillScreen(sessionOf(f))
				for _, l := range strings.Split(s.View(ctx), "\n") {
					if lw := ansi.StringWidth(l); lw > w {
						t.Fatalf("%s at %d: %d cells: %q", set.Tier, w, lw, ansi.Strip(l))
					}
				}
			}
		}
	}
}
