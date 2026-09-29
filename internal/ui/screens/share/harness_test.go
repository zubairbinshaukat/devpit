package share

import (
	"context"
	"flag"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// update rewrites the golden files: go test ./internal/ui/screens/share -update
var update = flag.Bool("update", false, "rewrite the golden frames")

// goldenDir is the repository's shared golden directory.
const goldenDir = "../../../../testdata/golden"

func TestMain(m *testing.M) {
	// No test may schedule a real spinner timer.
	spinTick = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	os.Exit(m.Run())
}

// testCtx is the render context of a test: dark theme, given size and tier.
func testCtx(w, h int, tier icons.Tier) uictx.Context {
	return uictx.Context{
		Theme: theme.For(true), Icons: icons.For(tier), Config: config.Default(),
		Width: w, Height: h, BodyHeight: h - 5, BodyTop: 3,
	}
}

// strip removes escape sequences from a frame.
func strip(s string) string { return ansi.Strip(s) }

// requireGolden compares got with testdata/golden/<name>.golden, or writes it
// with -update. Trailing spaces are ignored, as in the app's own goldens.
func requireGolden(t *testing.T, name, got string) {
	t.Helper()
	got = normalise(got)
	path := filepath.Join(goldenDir, name+".golden")
	if *update {
		if err := os.MkdirAll(goldenDir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden %s: %v\nRun go test ./internal/ui/screens/share -update", path, err)
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

// harness drives a screen the way the program loop would: keys and messages
// go in, and every command that comes out is run and its message fed back.
type harness struct {
	t      *testing.T
	scr    uictx.Screen
	ctx    uictx.Context
	pushed []uictx.Screen
	popped int
	status []string
}

func newHarness(t *testing.T, scr uictx.Screen) *harness {
	t.Helper()
	return &harness{t: t, scr: scr, ctx: testCtx(80, 24, icons.TierUnicode)}
}

// send delivers a message and settles every command it causes.
func (h *harness) send(msgs ...tea.Msg) *harness {
	h.t.Helper()
	for _, m := range msgs {
		next, cmd := h.scr.Update(m, h.ctx)
		h.scr = next
		h.settle(cmd)
	}
	return h
}

// init runs the screen's Init command.
func (h *harness) init() *harness {
	h.t.Helper()
	h.settle(h.scr.Init())
	return h
}

// settle runs a command and everything it leads to.
func (h *harness) settle(cmd tea.Cmd) {
	h.t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 500 {
			h.t.Fatal("commands kept coming: a command loop that never ends")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		// A command that has not answered in a moment is a background timer,
		// such as the text field's cursor blink: it is dropped. Commands that
		// do the screen's real work answer at once, because the fakes do.
		msg, ok := runFor(c, settleWait)
		if !ok {
			continue
		}
		switch m := msg.(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, m...)
		case uictx.PushScreenMsg:
			h.pushed = append(h.pushed, m.Screen)
		case uictx.PopScreenMsg:
			h.popped++
		case uictx.StatusMsg:
			h.status = append(h.status, m.Text)
		default:
			next, more := h.scr.Update(msg, h.ctx)
			h.scr = next
			queue = append(queue, more)
		}
	}
}

// settleWait is how long settle waits for one command.
const settleWait = 40 * time.Millisecond

// runFor runs a command on its own goroutine and reports whether it answered
// within d.
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

// runWithTimeout runs a command on its own goroutine and fails the test if it
// does not return, which is how a deadlock would show up.
func runWithTimeout(t *testing.T, c tea.Cmd) tea.Msg {
	t.Helper()
	ch := make(chan tea.Msg, 1)
	go func() { ch <- c() }()
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("a command did not return")
		return nil
	}
}

// keyCode builds the key message of a named key.
func keyCode(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

// text builds a key press of one typed character.
func text(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: rune(s[0]), Text: s} }

// typed sends each character of s as a key press.
func (h *harness) typed(s string) *harness {
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return h
}

// view is the current frame with escapes stripped.
func (h *harness) view() string { return strip(h.scr.View(h.ctx)) }

// fakeHoster is a scripted sharing engine.
type fakeHoster struct {
	mu       sync.Mutex
	adapters []host.Adapter
	cat      host.Category
	catErr   error
	adErr    error
	startErr error
	stopErr  error
	card     host.Card
	steps    []host.Step
	started  []host.Options
	stops    int
	active   bool
	stopped  chan struct{}
}

func newFakeHoster() *fakeHoster {
	return &fakeHoster{
		adapters: []host.Adapter{{Name: "Ethernet", Index: 4, IP: net.ParseIP("192.168.1.5"), Best: true}},
		cat:      host.CategoryPrivate,
		card: host.Card{
			IP: "192.168.1.5", Adapter: "Ethernet", Share: "Games-x7k2", User: "devpit-ab12", Password: "Xk3mPq9RtVw2NbLc",
			Path:     `D:\Games`,
			NetUse:   `net use \\192.168.1.5\Games-x7k2 /user:devpit-ab12 Xk3mPq9RtVw2NbLc`,
			Robocopy: `robocopy \\192.168.1.5\Games-x7k2 "D:\Games-x7k2" /E /MT:16 /Z /R:3 /W:5`,
			Cleanup:  `net use \\192.168.1.5\Games-x7k2 /delete`,
		},
		steps:   []host.Step{{Key: "firewall"}, {Key: "login"}, {Key: "folder"}, {Key: "share"}},
		stopped: make(chan struct{}, 4),
	}
}

func (f *fakeHoster) Adapters(context.Context) ([]host.Adapter, error) { return f.adapters, f.adErr }

func (f *fakeHoster) CategoryOf(context.Context, host.Adapter) (host.Category, error) {
	return f.cat, f.catErr
}

func (f *fakeHoster) Start(_ context.Context, o host.Options, onStep func(host.Step)) (host.Card, error) {
	f.mu.Lock()
	f.started = append(f.started, o)
	f.mu.Unlock()
	for _, s := range f.steps {
		onStep(s)
	}
	if f.startErr != nil {
		return host.Card{}, f.startErr
	}
	f.mu.Lock()
	f.active = true
	f.mu.Unlock()
	c := f.card
	c.SwitchedNetwork = o.MakePrivate
	return c, nil
}

func (f *fakeHoster) Stop(context.Context) error {
	f.mu.Lock()
	f.stops++
	err := f.stopErr
	if err == nil {
		f.active = false
	}
	f.mu.Unlock()
	f.stopped <- struct{}{}
	return err
}

func (f *fakeHoster) Active() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

// fakeReceiver is a scripted receiving engine.
type fakeReceiver struct {
	mu sync.Mutex

	listHost   string
	shares     []netstat.Share
	listErr    error
	signHost   []netstat.Share
	signHostEr error
	signShare  error
	dropN      int
	dropErr    error
	check      recv.Check
	checkErr   error
	events     []recv.Event
	outcome    recv.Outcome
	runErr     error
	holdRun    chan struct{}

	calls      []string
	signedIn   []netstat.Credentials
	cleared    int
	disconnect []string
	checkCalls int
}

func (f *fakeReceiver) note(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeReceiver) List(_ context.Context, typed string) (string, []netstat.Share, error) {
	f.note("list " + typed)
	return f.listHost, f.shares, f.listErr
}

func (f *fakeReceiver) SignInHost(_ context.Context, h string, c netstat.Credentials) ([]netstat.Share, error) {
	f.note("signin-host " + h)
	f.mu.Lock()
	f.signedIn = append(f.signedIn, c)
	f.mu.Unlock()
	return f.signHost, f.signHostEr
}

func (f *fakeReceiver) SignInShare(h, s string, c netstat.Credentials) error {
	f.note("signin-share " + h + " " + s)
	f.mu.Lock()
	f.signedIn = append(f.signedIn, c)
	f.mu.Unlock()
	return f.signShare
}

func (f *fakeReceiver) DropConnections(_ context.Context, h string) (int, error) {
	f.note("drop " + h)
	return f.dropN, f.dropErr
}

func (f *fakeReceiver) Preflight(_ context.Context, h, s, d string) (recv.Check, error) {
	f.note("preflight " + h + " " + s + " " + d)
	f.mu.Lock()
	f.checkCalls++
	n := f.checkCalls
	f.mu.Unlock()
	if f.checkErr != nil && n == 1 {
		return recv.Check{}, f.checkErr
	}
	return f.check, nil
}

func (f *fakeReceiver) Start(h, s, u, d string, p robocopy.Plan) job.Job {
	f.note("start " + u)
	return job.Job{Host: h, Share: s, User: u, Dest: d, TotalFiles: p.Files, TotalBytes: p.Bytes, State: job.StateRunning}
}

func (f *fakeReceiver) Run(ctx context.Context, j job.Job, onEvent func(recv.Event)) (recv.Outcome, error) {
	f.note("run " + j.Host)
	for _, e := range f.events {
		onEvent(e)
	}
	if f.holdRun != nil {
		select {
		case <-f.holdRun:
		case <-ctx.Done():
			return recv.Outcome{Paused: true, DoneBytes: j.DoneBytes}, nil
		}
	}
	return f.outcome, f.runErr
}

func (f *fakeReceiver) Disconnect(h, s string) {
	f.mu.Lock()
	f.disconnect = append(f.disconnect, h+`\`+s)
	f.mu.Unlock()
}

func (f *fakeReceiver) ClearJob() error {
	f.mu.Lock()
	f.cleared++
	f.mu.Unlock()
	return nil
}

func (f *fakeReceiver) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// testDeps returns Deps over the given fakes, with a recording clipboard.
func testDeps(h *fakeHoster, r *fakeReceiver, copied *[]string) Deps {
	return Deps{
		NewHost: func() Hoster { return h },
		NewRecv: func() Receiver { return r },
		Copy: func(s string) tea.Cmd {
			*copied = append(*copied, s)
			return nil
		},
	}
}
