package specs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/sysinfo"
	"github.com/zubairbinshaukat/devpit/internal/sysinfo/sysinfofake"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

var fixedNow = time.Date(2026, 11, 10, 14, 2, 0, 0, time.UTC)

// testCtx is a render context: body rows as the app gives them.
func testCtx(w, h int, set icons.Set) uictx.Context {
	return uictx.Context{
		Theme: theme.For(true), Icons: set, Config: config.Default(),
		Width: w, Height: h, BodyHeight: h - 6, BodyTop: 3, ReducedMotion: true,
	}
}

// noTick never fires.
func noTick(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }

// rec records what the screen did outside itself.
type rec struct {
	mu     sync.Mutex
	copied []string
}

func (r *rec) copyFn(text string) tea.Cmd {
	r.mu.Lock()
	r.copied = append(r.copied, text)
	r.mu.Unlock()
	return nil
}

// opts is the screen over a fake source.
func opts(src sysinfofake.Source, r *rec) Options {
	return Options{
		Source:   func() sysinfo.Source { return src },
		Elevated: func(context.Context) ([]sysinfo.Reliability, error) { return nil, &elevate.DeclinedError{} },
		Copy:     r.copyFn,
		Save:     func(string, []byte) (string, error) { return `C:\Users\you\Desktop\report.md`, nil },
		Now:      func() time.Time { return fixedNow },
		Tick:     noTick,
		Version:  "0.5.0",
	}
}

// drive runs cmd and every command it leads to, feeding the messages back.
func drive(t *testing.T, m Model, ctx uictx.Context, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 500; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if b, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, b...)
			continue
		}
		next, more := m.Update(msg, ctx)
		m = next.(Model)
		queue = append(queue, more)
	}
	return m
}

// loaded is the screen after every section of r arrived.
func loaded(t *testing.T, r sysinfo.Report, ctx uictx.Context, fail map[sysinfo.Section]error) Model {
	t.Helper()
	src := sysinfofake.New(r)
	src.Fail = fail
	m := New(opts(src, &rec{}))
	return drive(t, m, ctx, m.Init())
}

func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func press(t *testing.T, m Model, ctx uictx.Context, k string) Model {
	t.Helper()
	next, cmd := m.Update(keyMsg(k), ctx)
	return drive(t, next.(Model), ctx, cmd)
}

// states are every way the page can look.
func states(t *testing.T, ctx uictx.Context) map[string]Model {
	t.Helper()
	missingFail := map[sysinfo.Section]error{sysinfo.SecDisks: sysinfofake.ErrBlocked}
	out := map[string]Model{
		"reading":  New(opts(sysinfofake.New(sysinfofake.Good()), &rec{})),
		"good":     loaded(t, sysinfofake.Good(), ctx, nil),
		"flags":    loaded(t, sysinfofake.Flags(), ctx, nil),
		"desktop":  loaded(t, sysinfofake.Desktop(), ctx, nil),
		"missing":  loaded(t, sysinfofake.Missing(), ctx, missingFail),
		"soldered": loaded(t, sysinfofake.Soldered(), ctx, nil),
	}
	partial := New(opts(sysinfofake.New(sysinfofake.Good()), &rec{}))
	partial = drive(t, partial, ctx, read(partial.run.ctx, 0, sysinfo.SecBattery, time.Second,
		sysinfofake.New(sysinfofake.Good()).Battery, func(r *sysinfo.Report, v sysinfo.Battery) { r.Battery = v }))
	out["partial"] = partial
	out["bottom"] = press(t, out["flags"], ctx, "end")
	out["export"] = press(t, out["good"], ctx, "c")
	out["declined"] = press(t, out["good"], ctx, "e")
	running := out["good"]
	running.rel = relRunning
	out["elevating"] = running
	return out
}

func TestNoLineWiderThanTheTerminal(t *testing.T) {
	for _, w := range []int{80, 90, 100, 140} {
		for _, h := range []int{24, 30} {
			for _, set := range []icons.Set{icons.Nerd(), icons.Unicode(), icons.ASCII()} {
				ctx := testCtx(w, h, set)
				for name, m := range states(t, ctx) {
					view := m.View(ctx)
					lines := strings.Split(view, "\n")
					if len(lines) > ctx.BodyHeight {
						t.Errorf("%dx%d %s %s: %d lines for %d rows", w, h, set.Tier, name, len(lines), ctx.BodyHeight)
					}
					for i, l := range lines {
						if lw := ansi.StringWidth(l); lw > w {
							t.Errorf("%dx%d %s %s line %d is %d wide: %q", w, h, set.Tier, name, i, lw, ansi.Strip(l))
						}
						if set.Tier == icons.TierASCII {
							for _, r := range ansi.Strip(l) {
								if r > 0x7E && !strings.ContainsRune("●◆▲", r) {
									t.Errorf("%s %s ascii line %d has %q: %q", name, set.Tier, i, r, ansi.Strip(l))
									break
								}
							}
						}
					}
					// The full page must fit too, not only the window.
					for i, l := range m.layout(ctx).body {
						if lw := ansi.StringWidth(tierText(l, set.Tier == icons.TierASCII)); lw > w {
							t.Errorf("%dx%d %s %s body line %d is %d wide: %q", w, h, set.Tier, name, i, lw, ansi.Strip(l))
						}
					}
				}
			}
		}
	}
}

// fullText is the whole page as plain text.
func fullText(m Model, ctx uictx.Context) string {
	p := m.layout(ctx)
	return ansi.Strip(strings.Join(append(p.head, p.body...), "\n"))
}

func TestEveryFlagHasAWordNotOnlyColour(t *testing.T) {
	ctx := testCtx(100, 30, icons.Unicode())
	m := loaded(t, sysinfofake.Flags(), ctx, nil)
	text := fullText(m, ctx)
	for _, f := range sysinfo.Assess(m.rep, fixedNow) {
		if f.Level < sysinfo.Warn {
			continue
		}
		glyph := ctx.Icons.Warn
		if f.Level == sysinfo.Bad {
			glyph = ctx.Icons.Fail
		}
		first := strings.Fields(f.Text)
		if !strings.Contains(text, glyph+" ") || !strings.Contains(text, strings.Join(first[:min(4, len(first))], " ")) {
			t.Errorf("finding %q is not on the page with its shape", f.Text)
		}
	}
	for _, want := range []string{"54% very worn", "rule of thumb", "single-channel", "warning", "not activated", "Autopilot"} {
		if !strings.Contains(text, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(strings.ToLower(text), "dual-channel") || strings.Contains(strings.ToLower(text), "dual channel") {
		t.Error("the page must never claim dual-channel")
	}
	if strings.Contains(text, "°") || strings.Contains(strings.ToLower(text), "temperature") {
		t.Error("the page must never show a temperature")
	}
}

func TestStopCancelsTheReadAndUnblocks(t *testing.T) {
	src := sysinfofake.New(sysinfofake.Good())
	src.Block = map[sysinfo.Section]chan struct{}{sysinfo.SecDisks: make(chan struct{})}
	defer close(src.Block[sysinfo.SecDisks])
	m := New(opts(src, &rec{}))
	cmd := read(m.run.ctx, m.gen, sysinfo.SecDisks, time.Minute, src.Disks, func(r *sysinfo.Report, v sysinfo.DiskInfo) { r.Disks = v })
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	time.Sleep(20 * time.Millisecond)
	m.Stop()
	select {
	case msg := <-done:
		sm := msg.(sectionMsg)
		if !errors.Is(sm.err, context.Canceled) {
			t.Fatalf("err = %v", sm.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not unblock the read")
	}
	if m.Busy() {
		t.Fatal("reading must never hold Esc")
	}
}

func TestStaleGenerationIsDropped(t *testing.T) {
	ctx := testCtx(100, 30, icons.Unicode())
	m := loaded(t, sysinfofake.Good(), ctx, nil)
	next, _ := m.Update(keyMsg("r"), ctx) // gen 1, report cleared
	m = next.(Model)
	stale := sectionMsg{gen: 0, sec: sysinfo.SecBattery, apply: func(r *sysinfo.Report) { r.Battery.ChargePct = 1 }}
	next, _ = m.Update(stale, ctx)
	m = next.(Model)
	if m.rep.Have[sysinfo.SecBattery] {
		t.Fatal("a message from the old read was merged")
	}
}

func TestRefreshReadsAgain(t *testing.T) {
	ctx := testCtx(100, 30, icons.Unicode())
	src := sysinfofake.New(sysinfofake.Good())
	m := New(opts(src, &rec{}))
	m = drive(t, m, ctx, m.Init())
	first := src.Calls()
	if first != len(sysinfo.Sections()) || m.reading() {
		t.Fatalf("first read: %d calls, reading=%v", first, m.reading())
	}
	oldRun := m.run
	next, cmd := m.Update(keyMsg("r"), ctx)
	m = next.(Model)
	if !m.reading() {
		t.Fatal("refresh should start from nothing")
	}
	if oldRun.ctx.Err() == nil {
		t.Fatal("refresh must cancel the old read")
	}
	m = drive(t, m, ctx, cmd)
	if src.Calls() != 2*first || m.reading() {
		t.Fatalf("second read: %d calls", src.Calls())
	}
}

func TestExportAsksAndDefaultsToNo(t *testing.T) {
	ctx := testCtx(100, 30, icons.Unicode())
	r := &rec{}
	src := sysinfofake.New(sysinfofake.Good())
	m := New(opts(src, r))
	m = drive(t, m, ctx, m.Init())
	m = press(t, m, ctx, "c")
	if !m.asking || m.confirm.Answer() != confirm.AnswerNo {
		t.Fatal("copy must ask first, starting on No")
	}
	if len(r.copied) != 0 {
		t.Fatal("copied before the answer")
	}
	if !strings.Contains(ansi.Strip(m.View(ctx)), "Include serial numbers and MAC addresses?") {
		t.Fatal("the question is not on screen")
	}
	m = press(t, m, ctx, "enter")
	if m.asking || len(r.copied) != 1 {
		t.Fatalf("enter should answer No and copy once: asking=%v copies=%d", m.asking, len(r.copied))
	}
	for _, secret := range []string{"CB0000TEST", "S6TESTSERIAL", "00-11-22-33-44-55", "BATTEST"} {
		if strings.Contains(r.copied[0], secret) {
			t.Errorf("the default copy carries %q", secret)
		}
	}
	m = press(t, m, ctx, "c")
	_ = press(t, m, ctx, "y")
	if len(r.copied) != 2 || !strings.Contains(r.copied[1], "CB0000TEST") {
		t.Fatal("yes should include the serials")
	}
}

func TestSaveNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	name := sysinfo.ReportFileName(`Contoso: Book/14 "G3"`, fixedNow)
	if name != "PC specs - Contoso Book14 G3 - 2026-11-10.md" {
		t.Fatalf("name = %q", name)
	}
	p1, err := sysinfo.SaveNew(dir, name, []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	p2, err := sysinfo.SaveNew(dir, name, []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 || filepath.Base(p2) != "PC specs - Contoso Book14 G3 - 2026-11-10 (2).md" {
		t.Fatalf("paths = %q %q", p1, p2)
	}
	if b, _ := os.ReadFile(p1); string(b) != "first" {
		t.Fatal("the first report was overwritten")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("temporary files left behind: %v", entries)
	}

	// The screen saves only after the question, and says where.
	ctx := testCtx(100, 30, icons.Unicode())
	var saves []string
	o := opts(sysinfofake.New(sysinfofake.Good()), &rec{})
	o.Save = func(n string, _ []byte) (string, error) { saves = append(saves, n); return filepath.Join(dir, n), nil }
	m := New(o)
	m = drive(t, m, ctx, m.Init())
	m = press(t, m, ctx, "s")
	if len(saves) != 0 {
		t.Fatal("saved before the answer")
	}
	m = press(t, m, ctx, "enter")
	if len(saves) != 1 || !strings.HasPrefix(saves[0], "PC specs - Contoso Book 14 G3 - ") {
		t.Fatalf("saves = %v", saves)
	}
	_, cmd := m.Update(savedMsg{path: filepath.Join(dir, saves[0])}, ctx)
	if st, ok := cmd().(uictx.StatusMsg); !ok || !strings.Contains(st.Text, dir) {
		t.Fatalf("status should name the full path: %+v", st)
	}
}

func TestWheelScrollsAndStripClickJumps(t *testing.T) {
	ctx := testCtx(80, 24, icons.Unicode())
	m := loaded(t, sysinfofake.Good(), ctx, nil)
	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown}, ctx)
	m = next.(Model)
	if m.scroll != 3 {
		t.Fatalf("wheel scrolled to %d", m.scroll)
	}
	strip := ansi.Strip(m.layout(ctx).head[1])
	x := strings.Index(strip, "Storage")
	next, _ = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: x + 1, Y: ctx.BodyTop + 1}, ctx)
	m = next.(Model)
	p := m.layout(ctx)
	if m.scroll != p.anchors[3] {
		t.Fatalf("click on Storage scrolled to %d, want %d", m.scroll, p.anchors[3])
	}
	if !strings.Contains(ansi.Strip(p.body[m.scroll+1]), "Storage") {
		t.Fatalf("Storage is not at the top: %q", ansi.Strip(p.body[m.scroll+1]))
	}
	m = press(t, m, ctx, "right")
	if m.scroll != p.anchors[4] {
		t.Fatalf("right went to %d, want %d", m.scroll, p.anchors[4])
	}
}

func TestElevateOfferedOnlyWhenWearMissing(t *testing.T) {
	ctx := testCtx(100, 30, icons.Unicode())
	m := loaded(t, sysinfofake.Good(), ctx, nil)
	if !m.canElevate() || !strings.Contains(fullText(m, ctx), "[e] read them") {
		t.Fatal("wear is missing: [e] should be offered")
	}
	r := sysinfofake.Good()
	r.Disks.Disks[0].Rel = &sysinfo.Reliability{WearPct: 3, PowerOnHours: 1200, ReadErrors: 0}
	m = loaded(t, r, ctx, nil)
	if m.canElevate() || strings.Contains(fullText(m, ctx), "[e]") {
		t.Fatal("wear is there: [e] must not be offered")
	}
	if !strings.Contains(fullText(m, ctx), "3% of rated life used") {
		t.Fatal("the counters are not shown")
	}
	before := m
	m = press(t, m, ctx, "e")
	if m.rel != before.rel {
		t.Fatal("e did something with nothing to read")
	}
	m = loaded(t, sysinfofake.Missing(), ctx, map[sysinfo.Section]error{sysinfo.SecDisks: sysinfofake.ErrBlocked})
	if m.canElevate() {
		t.Fatal("no drives listed: nothing to read as admin")
	}
}

func TestDeclinedUACKeepsEverythingElse(t *testing.T) {
	ctx := testCtx(100, 30, icons.Unicode())
	m := loaded(t, sysinfofake.Good(), ctx, nil)
	before := fullText(m, ctx)
	next, _ := m.Update(keyMsg("e"), ctx)
	if next.(Model).rel != relRunning {
		t.Fatal("e should start the admin read")
	}
	_, scmd := next.(Model).Update(relMsg{gen: m.gen, err: &elevate.DeclinedError{}}, ctx)
	if st, ok := scmd().(uictx.StatusMsg); !ok || !strings.Contains(st.Text, "Skipped: needs admin") {
		t.Fatalf("status = %+v", st)
	}
	m = press(t, m, ctx, "e")
	if m.rel != relDeclined {
		t.Fatalf("declined: rel=%v", m.rel)
	}
	after := fullText(m, ctx)
	if !strings.Contains(after, "Skipped: needs admin. Everything else is shown.") {
		t.Fatal("the drive row should say it was skipped")
	}
	for _, keep := range []string{"Samsung PM9A1", "92% good", "Windows 11 Pro"} {
		if !strings.Contains(before, keep) || !strings.Contains(after, keep) {
			t.Errorf("%q lost after the declined prompt", keep)
		}
	}
}
