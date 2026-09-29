//go:build shots

package shots

import (
	"context"
	"fmt"
	"image/color"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/app"
	cleanengine "github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/selfupdate"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/header"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// waitLimit is how long a scene may wait for a screen to reach a state. Every
// engine behind a scene is a fake that answers at once, so the only real time
// spent is the screens' own 100 to 150 ms batching ticks; a scene that waits
// longer than this is stuck, and fails loudly with the frame it is stuck on.
const waitLimit = 20 * time.Second

// quiet is how long the message stream must stay silent for settle to
// return. It is longer than a command that does work on its own goroutine and
// shorter than the screens' 100 ms ticks.
const quiet = 30 * time.Millisecond

// session drives the real app model the way the Bubble Tea program loop would:
// messages go in, every command that comes out runs on its own goroutine, and
// what it returns is fed back in. Nothing here fakes a View: a frame is what
// the screens draw after the same key presses a person would make.
type session struct {
	t    *testing.T
	m    tea.Model
	w, h int

	// inbox carries the results of running commands back to the loop.
	inbox chan tea.Msg
	done  chan struct{}
	stop  sync.Once
	// cleanups run when the session closes: they release fakes that block.
	cleanups []func()
	// clock is the demo clock of the screen under test, when it has one; the
	// "clock:" step moves it.
	clock *demoClock
}

// newSession builds the app over cfg with the given screens, sized and themed
// as the manifest entry asks, and feeds the header the demo machine facts.
// The app's own Init is never run: it would probe the real disk, run
// version commands and check for updates. The header gets the same messages
// from constants instead.
func newSession(t *testing.T, e entry, cfg config.Config, screens map[string]func() uictx.Screen) *session {
	t.Helper()

	env := icons.MapEnv(map[string]string{
		"WT_SESSION":           "demo",
		uictx.ReducedMotionEnv: "1",
	})
	m := app.New(app.Options{
		Config:     cfg,
		Env:        env,
		SaveConfig: func(config.Config) error { return nil },
		Sweep: func(context.Context, []string, cleanengine.Options, func(cleanengine.Progress)) cleanengine.Report {
			return cleanengine.Report{}
		},
		ScreenFactory: screens,
		ToolVersions: func(context.Context) header.ToolVersionsMsg {
			return header.ToolVersionsMsg{Node: demoNode, Git: demoGit}
		},
		CheckUpdate: func(context.Context) (selfupdate.Result, error) { return selfupdate.Result{}, nil },
	})

	s := &session{
		t: t, m: m, w: e.Cols, h: e.Rows,
		inbox: make(chan tea.Msg, 4096),
		done:  make(chan struct{}),
	}
	t.Cleanup(s.close)

	var bg color.Color // nil reads as a dark terminal, as in the golden tests.
	if e.Theme == "light" {
		bg = color.RGBA{R: 0xEF, G: 0xF1, B: 0xF5, A: 0xFF}
	}
	s.feed(tea.WindowSizeMsg{Width: e.Cols, Height: e.Rows})
	s.feed(tea.BackgroundColorMsg{Color: bg})
	s.feed(header.ToolVersionsMsg{Node: demoNode, Git: demoGit})
	s.feed(header.DiskMsg{Space: winapi.DiskSpace{FreeBytes: demoDiskFree, TotalBytes: demoDiskTotal}})
	return s
}

// onClose registers f to run when the session ends.
func (s *session) onClose(f func()) { s.cleanups = append(s.cleanups, f) }

// close stops the loop and releases every blocked fake.
func (s *session) close() {
	s.stop.Do(func() {
		close(s.done)
		for _, f := range s.cleanups {
			f()
		}
	})
}

// feed delivers one message to the app and starts the command it returns.
func (s *session) feed(msg tea.Msg) {
	next, cmd := s.m.Update(msg)
	s.m = next
	s.spawn(cmd)
}

// spawn runs a command on its own goroutine and posts what it returns.
func (s *session) spawn(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if msg == nil {
			return
		}
		select {
		case s.inbox <- msg:
		case <-s.done:
		}
	}()
}

// dispatch handles one message that came back from a command.
func (s *session) dispatch(msg tea.Msg) {
	switch m := msg.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			s.spawn(c)
		}
	case tea.QuitMsg:
		// A scene never quits the program.
	default:
		if cmds, ok := sequenceOf(msg); ok {
			go s.runSequence(cmds)
			return
		}
		s.feed(msg)
	}
}

// sequenceOf unpacks tea.Sequence's message, whose type Bubble Tea does not
// export. It is a slice of commands, and the reflect check is exact about the
// type name so nothing else is ever mistaken for one.
func sequenceOf(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Name() != "sequenceMsg" {
		return nil, false
	}
	cmds := make([]tea.Cmd, 0, v.Len())
	for i := range v.Len() {
		if c, ok := v.Index(i).Interface().(tea.Cmd); ok {
			cmds = append(cmds, c)
		}
	}
	return cmds, true
}

// runSequence runs commands one after the other, as tea.Sequence promises.
func (s *session) runSequence(cmds []tea.Cmd) {
	for _, c := range cmds {
		if c == nil {
			continue
		}
		msg := c()
		if msg == nil {
			continue
		}
		select {
		case s.inbox <- msg:
		case <-s.done:
			return
		}
	}
}

// pumpFor processes commands' messages until none has arrived for quiet.
func (s *session) settle() {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-s.inbox:
			s.dispatch(msg)
		case <-time.After(quiet):
			return
		}
	}
}

// frame is the app's whole frame: header, body and footer, with escapes.
func (s *session) frame() string { return s.m.View().Content }

// text is the frame without escapes.
func (s *session) text() string { return ansi.Strip(s.frame()) }

// key presses named keys ("enter", "down", "esc", "tab", "space", "backspace")
// or a single printable character, settling after each.
func (s *session) key(names ...string) {
	s.t.Helper()
	for _, n := range names {
		s.feed(keyMsg(n))
		s.settle()
	}
}

// typed types text one character at a time.
func (s *session) typed(text string) {
	s.t.Helper()
	for _, r := range text {
		s.feed(tea.KeyPressMsg{Code: r, Text: string(r)})
		s.settle()
	}
}

// send delivers a message a fake or a screen would send, and settles.
func (s *session) send(msg tea.Msg) {
	s.feed(msg)
	s.settle()
}

// waitFor pumps until every substring is on screen, or fails with the frame.
func (s *session) waitFor(subs ...string) {
	s.t.Helper()
	s.waitUntil(fmt.Sprintf("the screen to show %q", subs), func(text string) bool {
		for _, sub := range subs {
			if !strings.Contains(text, sub) {
				return false
			}
		}
		return true
	})
}

// waitGone pumps until the substring has left the screen.
func (s *session) waitGone(sub string) {
	s.t.Helper()
	s.waitUntil(fmt.Sprintf("%q to leave the screen", sub), func(text string) bool { return !strings.Contains(text, sub) })
}

// waitUntil pumps until cond holds for the plain frame, or fails with it.
func (s *session) waitUntil(what string, cond func(text string) bool) {
	s.t.Helper()
	deadline := time.Now().Add(waitLimit)
	for {
		if cond(s.text()) {
			s.settle()
			if cond(s.text()) {
				return
			}
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("gave up waiting for %s; the frame is:\n%s", what, s.text())
		}
		select {
		case msg := <-s.inbox:
			s.dispatch(msg)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// named maps the key names a scene uses to Bubble Tea key codes.
var named = map[string]rune{
	"enter":     tea.KeyEnter,
	"esc":       tea.KeyEscape,
	"tab":       tea.KeyTab,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"left":      tea.KeyLeft,
	"right":     tea.KeyRight,
	"backspace": tea.KeyBackspace,
	"space":     tea.KeySpace,
	"pgdown":    tea.KeyPgDown,
	"pgup":      tea.KeyPgUp,
	"home":      tea.KeyHome,
	"end":       tea.KeyEnd,
}

// keyMsg builds the key press for a name from named, or for a single typed
// character.
func keyMsg(name string) tea.KeyPressMsg {
	if code, ok := named[name]; ok {
		msg := tea.KeyPressMsg{Code: code}
		if name == "space" {
			msg.Text = " "
		}
		return msg
	}
	r := []rune(name)
	if len(r) != 1 {
		panic("shots: unknown key name " + name)
	}
	return tea.KeyPressMsg{Code: r[0], Text: name}
}

// run plays a script: each item is a key name, a run of typed text written as
// "type:text", a wait written as "wait:text" that pauses until the text is on
// screen, or "clock:37s", which moves the demo clock on and lets the screen
// draw the new time. It is how a scene says what a person would do.
func (s *session) run(steps ...string) {
	s.t.Helper()
	for _, st := range steps {
		switch {
		case strings.HasPrefix(st, "wait:"):
			s.waitFor(strings.TrimPrefix(st, "wait:"))
		case strings.HasPrefix(st, "clock:"):
			d, err := time.ParseDuration(strings.TrimPrefix(st, "clock:"))
			if err != nil || s.clock == nil {
				s.t.Fatalf("bad clock step %q (%v)", st, err)
			}
			s.clock.advance(d)
			s.pause(3 * tick)
		case strings.HasPrefix(st, "type:"):
			s.typed(strings.TrimPrefix(st, "type:"))
		default:
			s.key(st)
		}
	}
}

// tick is the screens' redraw interval; a scene that needs the screen to have
// seen an event waits a few of them.
const tick = 100 * time.Millisecond

// pause lets d of real time pass while processing whatever arrives.
func (s *session) pause(d time.Duration) {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		select {
		case msg := <-s.inbox:
			s.dispatch(msg)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
