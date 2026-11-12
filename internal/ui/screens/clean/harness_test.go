package clean_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	cleanengine "github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/config"
	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/clean"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// projectsFolder is the root every test pretends to scan. No test in this
// file touches a real path: the scanner, the delete engine, the retry, the
// sweep and the cache are all fakes.
const projectsFolder = `D:\work`

// cmdTimeout is how long a single command may take before the test gives up.
// Every command here is a channel read that a fake feeds, so anything slower
// than this is a deadlock, not a slow disk.
const cmdTimeout = 10 * time.Second

// stepLimit bounds a settle so a command that re-queues itself forever fails
// the test instead of hanging it.
const stepLimit = 500

// testContext builds the render context the router would hand the screen.
func testContext(cfg config.Config) uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      icons.Unicode(),
		Config:     cfg,
		Width:      100,
		Height:     30,
		BodyHeight: 26,
	}
}

// testConfig is a settled configuration pointing at the fake projects folder.
func testConfig() config.Config {
	cfg := config.Default()
	cfg.FirstRunDone = true
	cfg.DefaultProjectsFolder = projectsFolder
	return cfg
}

// stepClock returns a clock that jumps a second every time it is read, so
// the screen's 5 ms key debounce never swallows a test's key press.
func stepClock() func() time.Time {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		now = now.Add(time.Second)
		return now
	}
}

// harness drives a screen the way the router does: it calls Update, runs the
// commands that come back, and feeds their messages in again.
type harness struct {
	t     *testing.T
	s     uictx.Screen
	ctx   uictx.Context
	cmds  []tea.Cmd
	trace []tea.Msg

	// producedYes records a Yes answer that came out of the screen's own
	// dialog, as opposed to one a test injected by hand.
	producedYes bool
}

// newHarness starts a harness over a screen.
func newHarness(t *testing.T, s uictx.Screen, cfg config.Config) *harness {
	t.Helper()
	return &harness{t: t, s: s, ctx: testContext(cfg)}
}

// model returns the screen as a clean.Model.
func (h *harness) model() clean.Model {
	h.t.Helper()
	m, ok := h.s.(clean.Model)
	if !ok {
		h.t.Fatalf("the screen is a %T, not a clean.Model", h.s)
	}
	return m
}

// state is the screen's current step.
func (h *harness) state() string { return h.model().StateName() }

// send hands one message to the screen and queues whatever comes back.
func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	h.trace = append(h.trace, msg)
	next, cmd := h.s.Update(msg, h.ctx)
	h.s = next
	h.queue(cmd)
}

// key sends a printable key press.
func (h *harness) key(s string) { h.send(tea.KeyPressMsg{Code: []rune(s)[0], Text: s}) }

// keyPress builds a named key press, for tests that need the message value
// rather than the send.
func keyPress(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

// special sends a named key press.
func (h *harness) special(code rune) { h.send(tea.KeyPressMsg{Code: code}) }

// queue adds a command to the pending list.
func (h *harness) queue(cmd tea.Cmd) {
	if cmd != nil {
		h.cmds = append(h.cmds, cmd)
	}
}

// step runs one pending command and feeds its message back in. It reports
// whether there was anything to run.
func (h *harness) step() bool {
	h.t.Helper()
	if len(h.cmds) == 0 {
		return false
	}
	cmd := h.cmds[0]
	h.cmds = h.cmds[1:]

	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()

	select {
	case msg := <-out:
		h.dispatch(msg)
	case <-time.After(cmdTimeout):
		h.t.Fatal("a command did not finish; something is blocked")
	}
	return true
}

// dispatch unwraps a batch or feeds a single message back into the screen.
func (h *harness) dispatch(msg tea.Msg) {
	h.t.Helper()
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			h.queue(c)
		}
	default:
		if a, ok := msg.(confirm.AnsweredMsg); ok && a.Answer == confirm.AnswerYes {
			h.producedYes = true
		}
		h.send(msg)
	}
}

// settle runs every pending command, and everything they produce, until
// nothing is left.
func (h *harness) settle() {
	h.t.Helper()
	for i := 0; i < stepLimit; i++ {
		if !h.step() {
			return
		}
	}
	h.t.Fatalf("the screen never settled after %d commands", stepLimit)
}

// stepUntil runs pending commands until the screen reaches a step, and stops
// there without running anything further.
func (h *harness) stepUntil(want string) {
	h.t.Helper()
	for i := 0; i < stepLimit; i++ {
		if h.state() == want {
			return
		}
		if !h.step() {
			h.t.Fatalf("no commands left and the screen is on %q, not %q", h.state(), want)
		}
	}
	h.t.Fatalf("the screen never reached %q; it is on %q", want, h.state())
}

// selection is how many rows are ticked and how many bytes they hold.
func (h *harness) selection() int {
	n, _ := h.model().Table().SelectedCount()
	return n
}

// tickAll leaves every row ticked, whatever the pre-selection did.
func (h *harness) tickAll() {
	h.t.Helper()
	for i := 0; i < 3; i++ {
		if h.selection() == h.model().Table().Len() {
			return
		}
		h.key("a")
	}
	h.t.Fatal("a did not tick everything")
}

// untickAll leaves nothing ticked, whatever the pre-selection did.
func (h *harness) untickAll() {
	h.t.Helper()
	for i := 0; i < 3; i++ {
		if h.selection() == 0 {
			return
		}
		h.key("a")
	}
	h.t.Fatal("a did not untick everything")
}

// view renders the screen.
func (h *harness) view() string { return h.s.View(h.ctx) }

// sawConfirmedYes reports whether the screen's own dialog ever produced a
// Yes. A message a test injects by hand does not count, which is what makes
// the safety-rule-1 assertion mean something.
func (h *harness) sawConfirmedYes() bool { return h.producedYes }

// item builds a scan result.
func item(project, name string, size uint64, tier scan.Tier) scan.Item {
	return scan.Item{
		Path:        project + `\` + name,
		Name:        name,
		Project:     project,
		Size:        size,
		Tier:        tier,
		Kind:        scan.KindProjectJunk,
		Rule:        name,
		RestoreHint: "npm install brings it back",
		LastUsed:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
}

// scanReturning is a fake scanner that sends a fixed list of items.
func scanReturning(items ...scan.Item) clean.ScanFunc {
	return func(ctx context.Context, _ scan.Options, out chan<- scan.Item, progress func(scan.Stats)) (scan.Stats, error) {
		var bytes uint64
		for i, it := range items {
			select {
			case out <- it:
			case <-ctx.Done():
				return scan.Stats{}, ctx.Err()
			}
			bytes += it.Size
			if progress != nil {
				progress(scan.Stats{DirsChecked: uint64(10 * (i + 1)), Found: uint64(i + 1), Bytes: bytes})
			}
		}
		return scan.Stats{DirsChecked: uint64(10 * len(items)), Found: uint64(len(items)), Bytes: bytes, Elapsed: time.Second}, nil
	}
}

// noCache is a cache that never has anything.
func noCache(string) (*scan.Cache, error) { return nil, scan.ErrNoCache }

// noSave throws a finished scan away.
func noSave(*scan.Cache) error { return nil }

// baseEngines is the fake wiring every test starts from: nothing touches a
// disk and nothing deletes.
func baseEngines(items ...scan.Item) clean.Engines {
	return clean.Engines{
		Scan:      scanReturning(items...),
		LoadCache: noCache,
		SaveCache: noSave,
		Verify:    func(scan.Item) error { return nil },
		Clean: func(context.Context, []cleanengine.Item, cleanengine.Options, func(cleanengine.Progress)) cleanengine.Report {
			return cleanengine.Report{}
		},
		// No test reads Devpit's real record of unfinished extractions.
		Unfinished: unfinishedList(),
	}
}

// unfinishedList is a fake record of unfinished extractions holding the
// archives with these paths.
func unfinishedList(paths ...string) func(context.Context) []extractengine.Unfinished {
	return func(context.Context) []extractengine.Unfinished {
		out := make([]extractengine.Unfinished, 0, len(paths))
		for _, p := range paths {
			out = append(out, extractengine.Unfinished{
				Set:    archive.Set{Picked: p, First: p, Parts: []string{p}, Format: archive.FormatZip, Bytes: 34_000_000_000},
				OutDir: strings.TrimSuffix(p, ".zip"),
				Mode:   extractengine.ModeInPlace,
			})
		}
		return out
	}
}

// update rewrites the golden files: go test ./internal/ui/screens/clean -update
var update = flag.Bool("update", false, "rewrite the golden frames")

// goldenDir is the repository's shared golden directory.
const goldenDir = "../../../../testdata/golden"

// requireGolden compares a frame with testdata/golden/<name>.golden, or
// writes it with -update. Trailing spaces are ignored, as in the app's own
// goldens.
func requireGolden(t *testing.T, name, got string) {
	t.Helper()
	got = trimLines(got)
	path := filepath.Join(goldenDir, name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no golden %s: %v\nRun go test ./internal/ui/screens/clean -update", path, err)
	}
	if trimLines(string(want)) != got {
		t.Errorf("frame does not match %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

// trimLines trims trailing blanks from every line.
func trimLines(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.Join(lines, "\n")
}

// frameWithKeys is the body of a screen followed by its footer key hints.
func frameWithKeys(s uictx.Screen, ctx uictx.Context) string {
	var hints []string
	for _, b := range s.ShortHelp() {
		h := b.Help()
		hints = append(hints, h.Key+" "+h.Desc)
	}
	return ansi.Strip(s.View(ctx)) + "\n\n[keys] " + strings.Join(hints, "  ·  ") + "\n"
}

// sizedContext is the render context the app hands a screen at a size and
// tier: four rows of header above the body and two of footer under it.
func sizedContext(cfg config.Config, w, h int, tier icons.Tier) uictx.Context {
	return uictx.Context{
		Theme: theme.For(true), Icons: icons.For(tier), Config: cfg,
		Width: w, Height: h, BodyHeight: h - 6, BodyTop: 4,
	}
}
