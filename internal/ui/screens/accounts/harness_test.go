package accounts

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/accounts/demo"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// update rewrites the golden files: go test ./internal/ui/screens/accounts -update
var update = flag.Bool("update", false, "rewrite the golden frames")

// goldenDir is the repository's shared golden directory.
const goldenDir = "../../../../testdata/golden"

// The demo engine is what these screens are driven through.
var _ Service = (*demo.Service)(nil)

// testCtx is the render context of a test: dark theme, given size and tier,
// the body the header (with its tab row) and the footer leave.
func testCtx(w, h int, tier icons.Tier) uictx.Context {
	return uictx.Context{
		Theme: theme.For(true), Icons: icons.For(tier), Config: config.Default(),
		Width: w, Height: h, BodyHeight: h - 5, BodyTop: 3, ReducedMotion: true,
	}
}

// fakeInfo is a folder that exists.
type fakeInfo struct{ os.FileInfo }

func (fakeInfo) IsDir() bool { return true }

// testOptions are the options of a test: the demo engine, no timers, a
// sign-in that runs in place of handing the terminal over, and a recording
// clipboard.
func testOptions(svc *demo.Service, copied *[]string) Options {
	return Options{
		Folder: demo.Folder,
		Open:   func() (Service, error) { return svc, nil },
		Exec: func(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd {
			return func() tea.Msg {
				c.SetStdin(strings.NewReader(""))
				c.SetStdout(io.Discard)
				c.SetStderr(io.Discard)
				return fn(c.Run())
			}
		},
		Copy: func(s string) tea.Cmd {
			*copied = append(*copied, s)
			return nil
		},
		Tick: func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil },
		Stat: func(p string) (os.FileInfo, error) {
			if svc.Gone[strings.ToLower(p)] {
				return nil, os.ErrNotExist
			}
			return fakeInfo{}, nil
		},
		Getwd:   func() (string, error) { return demo.Folder, nil },
		Browse:  func(string) (string, error) { return `D:\elsewhere`, nil },
		RunOnce: func(service.OnceCommand) (int, error) { return 0, nil },
	}
}

// harness drives the Accounts screens the way the program loop would, with
// a small router: pushes, pops and replaces act on its own stack, and every
// command that comes out is run and its message fed back.
type harness struct {
	t      *testing.T
	stack  []uictx.Screen
	ctx    uictx.Context
	svc    *demo.Service
	copied []string
	status []string
	// wait is how long settle waits for one command; settleWait when zero.
	// A test over the real engine, which reads real files, waits longer.
	wait time.Duration
}

// newHarness opens the page over svc, at 80x24 on the unicode tier.
func newHarness(t *testing.T, svc *demo.Service) *harness {
	t.Helper()
	h := &harness{t: t, svc: svc, ctx: testCtx(80, 24, icons.TierUnicode)}
	return h.open(testOptions(svc, &h.copied))
}

// open puts the page over opts at the bottom of the stack and opens it.
func (h *harness) open(opts Options) *harness {
	h.t.Helper()
	page := NewWith(opts)
	h.stack = []uictx.Screen{page}
	h.settle(page.Init())
	return h
}

// top is the visible screen.
func (h *harness) top() uictx.Screen { return h.stack[len(h.stack)-1] }

// send delivers messages and settles every command they cause.
func (h *harness) send(msgs ...tea.Msg) *harness {
	h.t.Helper()
	for _, m := range msgs {
		h.deliver(m)
	}
	return h
}

// deliver is one message through the router.
func (h *harness) deliver(msg tea.Msg) {
	h.t.Helper()
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			h.settle(c)
		}
	case uictx.PushScreenMsg:
		h.stack = append(h.stack, m.Screen)
		h.settle(m.Screen.Init())
	case uictx.PopScreenMsg:
		if len(h.stack) > 1 {
			uictx.Stop(h.top())
			h.stack = h.stack[:len(h.stack)-1]
		}
	case uictx.ReplaceScreenMsg:
		uictx.Stop(h.top())
		h.stack[len(h.stack)-1] = m.Screen
		h.settle(m.Screen.Init())
	case uictx.StatusMsg:
		h.status = append(h.status, m.Text)
	case tea.KeyPressMsg:
		// The router's own Esc: back, unless the screen is busy.
		if m.Code == tea.KeyEscape && !uictx.Busy(h.top()) && len(h.stack) > 1 {
			uictx.Stop(h.top())
			h.stack = h.stack[:len(h.stack)-1]
			return
		}
		h.update(msg)
	default:
		if cmds, ok := sequenceOf(msg); ok {
			for _, c := range cmds {
				h.settle(c)
			}
			return
		}
		h.update(msg)
	}
}

// update hands msg to the top screen.
func (h *harness) update(msg tea.Msg) {
	next, cmd := h.top().Update(msg, h.ctx)
	h.stack[len(h.stack)-1] = next
	h.settle(cmd)
}

// sequenceOf unpacks tea.Sequence's message, whose type is not exported.
func sequenceOf(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != reflect.TypeOf(tea.Cmd(nil)) {
		return nil, false
	}
	out := make([]tea.Cmd, v.Len())
	for i := range out {
		out[i], _ = v.Index(i).Interface().(tea.Cmd)
	}
	return out, true
}

// settle runs a command and everything it leads to.
func (h *harness) settle(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		return
	}
	wait := h.wait
	if wait == 0 {
		wait = settleWait
	}
	msg, ok := runFor(cmd, wait)
	if !ok {
		return
	}
	h.deliver(msg)
}

// settleWait is how long settle waits for one command. The demo engine
// answers at once; a command slower than this is a timer (a cursor blink)
// and is dropped.
const settleWait = 80 * time.Millisecond

// runFor runs a command on its own goroutine and reports whether it
// answered within d. The demo engine answers at once; anything slower is a
// timer, which a test drops.
func runFor(c tea.Cmd, d time.Duration) (tea.Msg, bool) {
	ch := make(chan tea.Msg, 1)
	go func() { ch <- c() }()
	select {
	case m := <-ch:
		return m, true
	case <-time.After(d):
		return nil, false
	}
}

// press builds the press of a named key or a typed character.
func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// keys presses each in turn.
func (h *harness) keys(ks ...string) *harness {
	h.t.Helper()
	for _, k := range ks {
		h.send(press(k))
	}
	return h
}

// typed types s one character at a time.
func (h *harness) typed(s string) *harness {
	h.t.Helper()
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return h
}

// click is a left click at a body row and column. The pointer passes over
// the spot first, the way it does on a real terminal, so a list that
// highlights a row on hover and acts on a click of the highlighted row
// behaves here as it does there.
func (h *harness) click(x, row int) *harness {
	h.t.Helper()
	h.send(tea.MouseMotionMsg{X: x, Y: row + h.ctx.BodyTop})
	return h.send(tea.MouseClickMsg{Button: tea.MouseLeft, X: x, Y: row + h.ctx.BodyTop})
}

// clickText clicks the first body row that shows text, at its column.
func (h *harness) clickText(text string) *harness {
	h.t.Helper()
	for i, l := range strings.Split(h.view(), "\n") {
		if j := strings.Index(l, text); j >= 0 {
			return h.click(ansi.StringWidth(l[:j])+1, i)
		}
	}
	h.t.Fatalf("no %q on screen:\n%s", text, h.view())
	return h
}

// view is the top screen's body with escapes stripped.
func (h *harness) view() string { return ansi.Strip(h.top().View(h.ctx)) }

// mustSee fails unless the screen shows every text. Runs of spaces and
// line breaks count as one space, so a sentence wrapped across lines is
// still found.
func (h *harness) mustSee(texts ...string) *harness {
	h.t.Helper()
	v := h.view()
	flat := strings.Join(strings.Fields(v), " ")
	for _, s := range texts {
		if !strings.Contains(v, s) && !strings.Contains(flat, strings.Join(strings.Fields(s), " ")) {
			h.t.Fatalf("the screen does not show %q:\n%s", s, v)
		}
	}
	return h
}

// --- golden frames ---

// frame is what the app would draw for a screen: its body cut to the body
// height, then its footer hints, so a golden also pins the footer.
func frame(scr uictx.Screen, ctx uictx.Context) string {
	// The folder picker's Browse row depends on the OS (off Windows it is
	// greyed out and says so); frames are pinned in the Windows form, the
	// one Devpit ships, as Share Files' are.
	view := strings.ReplaceAll(ansi.Strip(scr.View(ctx)), "Only available on Windows", "Open the Windows folder browser")
	body := strings.Split(view, "\n")
	if len(body) > ctx.BodyHeight {
		body = body[:ctx.BodyHeight]
	}
	for len(body) < ctx.BodyHeight {
		body = append(body, "")
	}
	var hints []string
	for _, b := range scr.ShortHelp() {
		hl := b.Help()
		hints = append(hints, hl.Key+" "+hl.Desc)
	}
	return strings.Join(body, "\n") + "\n[keys] " + strings.Join(hints, "  ·  ") + "\n"
}

// requireGolden compares got with testdata/golden/<name>.golden, or writes
// it with -update. Trailing spaces are ignored, as in the app's goldens.
func requireGolden(t *testing.T, name, got string) {
	t.Helper()
	got = normalise(got)
	path := filepath.Join(goldenDir, name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden %s: %v\nRun go test ./internal/ui/screens/accounts -update", path, err)
	}
	if normalise(string(want)) != got {
		t.Errorf("frame does not match %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

// normalise trims trailing blanks from every line.
func normalise(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.Join(lines, "\n")
}
