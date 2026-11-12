package extract

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/config"
	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/extract/extracttest"
	"github.com/zubairbinshaukat/devpit/internal/protect"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// update rewrites the golden files: go test ./internal/ui/screens/extract -update
var update = flag.Bool("update", false, "rewrite the golden frames")

// goldenDir is the repository's shared golden directory.
const goldenDir = "../../../../testdata/golden"

// The archive every test extracts, and where it goes. No test touches a real
// path: the disk, hatchzip, the clock and Scoop are all fakes.
const (
	zipPath  = `D:\Downloads\Game-Backup.zip`
	zipName  = "Game-Backup.zip"
	zipOut   = `D:\Downloads\Game-Backup`
	zipBytes = int64(84_200_000_000)
	typedPw  = "hunter2"
)

// testCtx is the render context the app hands a screen at a size and tier:
// four rows of header above the body and two of footer under it.
func testCtx(w, h int, tier icons.Tier) uictx.Context {
	return uictx.Context{
		Theme: theme.For(true), Icons: icons.For(tier), Config: config.Default(),
		Width: w, Height: h, BodyHeight: h - 6, BodyTop: 4,
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
		t.Fatalf("no golden %s: %v\nRun go test ./internal/ui/screens/extract -update", path, err)
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

// frameWithKeys is the body of the screen followed by its footer key hints,
// so a golden also pins what the footer would offer in that state.
func frameWithKeys(m Model, ctx uictx.Context) string {
	var hints []string
	for _, b := range m.ShortHelp() {
		h := b.Help()
		hints = append(hints, h.Key+" "+h.Desc)
	}
	return platformNeutral(strip(m.View(ctx))) + "\n\n[keys] " + strings.Join(hints, "  ·  ") + "\n"
}

// platformNeutral makes a frame the same on every OS. The folder picker's
// Browse row is the one thing here that depends on the build: off Windows
// pathpicker disables it and says so, and this package cannot set that row.
// The frame is pinned in its Windows form, the one Devpit ships.
func platformNeutral(frame string) string {
	return strings.ReplaceAll(frame, "Only available on Windows", "Open the Windows folder browser")
}

// stepClock returns a clock that jumps a second every time it is read, so the
// screen's key debounce never swallows a test's key press. It starts two days
// after the fake engine's epoch, which is when a recorded extraction started.
func stepClock() func() time.Time {
	var mu sync.Mutex
	now := extracttest.Epoch().Add(48 * time.Hour)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(time.Second)
		return now
	}
}

// noTick is the spinner timer of a test: it never fires.
func noTick(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }

// foundTool is a detection result for a usable hatchzip.
func foundTool(version string) func(context.Context) tools.Tool {
	return func(context.Context) tools.Tool {
		return tools.Tool{Name: "hatchzip", Exe: "hatchzip", Path: extracttest.ToolPath, Version: version, Found: true}
	}
}

// missing is a detection result for a tool that is not installed.
func missing(context.Context) tools.Tool { return tools.Tool{} }

// scoopFound is a detection result for Scoop.
func scoopFound(context.Context) tools.Tool {
	return tools.Tool{Name: "scoop", Path: `C:\Users\test\scoop\shims\scoop.cmd`, Version: "0.5.2", Found: true}
}

// winOutDir is the output folder of a fresh extraction, built with Windows
// separators whatever OS the test runs on.
func winOutDir(set archive.Set) string {
	i := strings.LastIndexAny(set.First, `\/`)
	return set.First[:i+1] + archive.Stem(set.First)
}

// The folders of the change-folder tests, and the fixed log a run leaves. No
// frame ever holds a path of the machine the tests run on.
const (
	otherParent = `E:\Games`
	otherOut    = `E:\Games\Game-Backup`
	sysParent   = `C:\Windows`
	logFile     = `C:\Users\test\AppData\Local\devpit\logs\extract-20260104-030405-Game-Backup.log`
)

// fakeOutDirIn is a fake extract.OutDirIn: the new folder is the archive's
// stem inside the picked one, and the network and Windows' own folder are
// refused. The real one asks Windows what kind of drive a letter is.
func fakeOutDirIn(parent string, set archive.Set) (string, error) {
	switch {
	case strings.HasPrefix(parent, `\\`):
		return "", extractengine.ErrNetworkPath
	case strings.HasPrefix(strings.ToLower(parent), strings.ToLower(sysParent)):
		return "", extractengine.ErrProtected
	}
	return strings.TrimRight(parent, `\`) + `\` + archive.Stem(set.First), nil
}

// identifyOn is a fake extract.Identify over the fake disk: the naming
// rules, a network refusal, and an Lstat of the first volume.
func identifyOn(fsys *extracttest.FakeFS) func(string) (archive.Set, error) {
	return func(path string) (archive.Set, error) {
		if strings.HasPrefix(path, `\\`) {
			return archive.Set{}, extractengine.ErrNetworkPath
		}
		f, first, ok, err := archive.Classify(path)
		if !ok {
			return archive.Set{}, err
		}
		fi, err := fsys.Lstat(first)
		if err != nil || !fi.Mode().IsRegular() {
			// Nothing there, or not a file: the engine's own refusal, so the
			// picker's sentence is tested against the error it really gets.
			if _, ierr := extractengine.Identify(path, fsys, protect.List{}, nil); ierr != nil {
				return archive.Set{}, ierr
			}
		}
		if err != nil {
			return archive.Set{}, err
		}
		return archive.Set{Picked: path, First: first, Parts: []string{first}, Format: f, Bytes: fi.Size()}, nil
	}
}

// zipSet is the test archive as a set.
func zipSet() archive.Set {
	return archive.Set{Picked: zipPath, First: zipPath, Parts: []string{zipPath}, Format: archive.FormatZip, Bytes: zipBytes}
}

// stepRun is a scripted install step.
type stepRun struct {
	lines []string
	exit  int
	// hold keeps the step running until its context ends.
	hold bool
}

// fakeSteps runs scripted install steps in order and records every argv.
type fakeSteps struct {
	mu    sync.Mutex
	runs  []stepRun
	argvs [][]string
}

func (f *fakeSteps) run(ctx context.Context, argv []string, _ time.Duration, onLine func(string)) tools.StepResult {
	f.mu.Lock()
	f.argvs = append(f.argvs, append([]string(nil), argv...))
	var r stepRun
	if len(f.runs) > 0 {
		r, f.runs = f.runs[0], f.runs[1:]
	}
	f.mu.Unlock()
	for _, l := range r.lines {
		onLine(l)
	}
	if r.hold {
		<-ctx.Done()
		return tools.StepResult{ExitCode: -1, LastLines: r.lines}
	}
	return tools.StepResult{OK: r.exit == 0, ExitCode: r.exit, LastLines: r.lines}
}

func (f *fakeSteps) calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.argvs...)
}

// rig is one screen over the fakes.
type rig struct {
	*harness
	fakes *extracttest.Fakes
	eng   *extractengine.Engine
	steps *fakeSteps
	found []extractengine.Found
	// onType says the folder picker's cursor is already on "Type a path…".
	onType bool
}

// rigOpt changes a rig before its screen is built.
type rigOpt func(*rigSetup)

type rigSetup struct {
	opts    []Option
	scripts []extracttest.Script
	found   []extractengine.Found
	steps   []stepRun
	noDisk  bool
}

// with adds screen options, which win over the rig's defaults.
func with(opts ...Option) rigOpt { return func(s *rigSetup) { s.opts = append(s.opts, opts...) } }

// scripted queues hatchzip runs: each check, extract or restore takes one.
func scripted(scripts ...extracttest.Script) rigOpt {
	return func(s *rigSetup) { s.scripts = append(s.scripts, scripts...) }
}

// finds sets what the archive search returns.
func finds(found ...extractengine.Found) rigOpt {
	return func(s *rigSetup) { s.found = found }
}

// installs scripts the install steps.
func installs(runs ...stepRun) rigOpt { return func(s *rigSetup) { s.steps = runs } }

// newRig builds a screen over a real engine on the extracttest fakes, with
// the test archive on the fake disk, hatchzip found and Scoop found.
func newRig(t *testing.T, setup ...rigOpt) *rig {
	t.Helper()
	var s rigSetup
	for _, o := range setup {
		o(&s)
	}
	deps, fakes := extracttest.NewDeps(s.scripts...)
	if !s.noDisk {
		fakes.FS.AddFile(zipPath, zipBytes)
	}
	eng := extractengine.New(extracttest.Tool(), deps)
	steps := &fakeSteps{runs: s.steps}
	r := &rig{fakes: fakes, eng: eng, steps: steps, found: s.found}
	if r.found == nil {
		// Unless a test says otherwise, the search finds the test archive, so
		// it is the picker's first row.
		r.found = []extractengine.Found{{Set: zipSet(), Mod: extracttest.Epoch()}}
	}

	opts := []Option{
		WithExtractor(eng),
		WithDetect(foundTool(extracttest.ToolVersion)),
		WithScoop(scoopFound),
		WithRunStep(steps.run),
		WithClock(stepClock()),
		WithIdentify(identifyOn(fakes.FS)),
		WithFind(func(context.Context, []string) []extractengine.Found { return r.found }),
		WithStatFS(fakes.FS),
		WithOutDir(winOutDir),
		WithFree(func(string) (uint64, bool) { return 15_700_000_000, true }),
		WithBrowse(func(string) (string, error) { return "", nil }),
		WithFolderPicker(fakes.FS.Lstat, func(string) (string, error) { return "", nil }),
		WithOutDirIn(fakeOutDirIn),
		withTick(noTick),
	}
	opts = append(opts, s.opts...)
	r.harness = newHarness(t, New(opts...), fakes.Clock)
	return r
}

// harness drives the screen the way the program loop does: every command
// runs on its own goroutine and its message is fed back in. Commands that
// wait on a running extraction simply stay out until it moves.
type harness struct {
	t     *testing.T
	m     Model
	ctx   uictx.Context
	clock *extracttest.Clock

	inbox   chan tea.Msg
	pending atomic.Int64

	pushed []uictx.Screen
	popped int
	status []string
	// yes records the dialogs whose Yes came out of the screen's own dialog,
	// as opposed to one a test injected by hand.
	yes map[string]bool
}

func newHarness(t *testing.T, m Model, clock *extracttest.Clock) *harness {
	t.Helper()
	h := &harness{
		t: t, m: m, clock: clock,
		ctx:   testCtx(80, 24, icons.TierUnicode),
		inbox: make(chan tea.Msg, 4096),
		yes:   map[string]bool{},
	}
	t.Cleanup(func() { h.m.Stop() })
	return h
}

// start runs the screen's Init, as the router does when it pushes it.
func (h *harness) start() *harness {
	h.t.Helper()
	h.run(h.m.Init())
	h.settle()
	return h
}

// run starts a command on its own goroutine.
func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	h.pending.Add(1)
	go func() {
		defer h.pending.Add(-1)
		if msg := cmd(); msg != nil {
			h.inbox <- msg
		}
	}()
}

// deliver hands the screen a message a test made up.
func (h *harness) deliver(msg tea.Msg) {
	h.t.Helper()
	h.handle(msg, false)
}

// handle hands one message to the screen, or acts on it as the router would.
// produced says the message came out of one of the screen's own commands.
func (h *harness) handle(msg tea.Msg, produced bool) {
	h.t.Helper()
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			h.run(c)
		}
	case uictx.PushScreenMsg:
		h.pushed = append(h.pushed, m.Screen)
	case uictx.PopScreenMsg:
		h.popped++
	case uictx.StatusMsg:
		h.status = append(h.status, m.Text)
	default:
		if a, ok := msg.(confirm.AnsweredMsg); ok && produced && a.Answer == confirm.AnswerYes {
			h.yes[a.ID] = true
		}
		next, cmd := h.m.update(msg, h.ctx)
		h.m = next
		h.run(cmd)
	}
}

// quiet is how long settle waits for a command that has not answered before
// deciding it is one that waits on something else: a running extraction, a
// held install step.
const quiet = 60 * time.Millisecond

// settle feeds back every message the commands produce until none is left.
// It does not move the fake clock.
func (h *harness) settle() {
	h.t.Helper()
	for i := 0; i < 5000; i++ {
		select {
		case msg := <-h.inbox:
			h.handle(msg, true)
			continue
		default:
		}
		if h.pending.Load() == 0 && len(h.inbox) == 0 {
			return
		}
		select {
		case msg := <-h.inbox:
			h.handle(msg, true)
		case <-time.After(quiet):
			return
		}
	}
	h.t.Fatal("the screen never settled")
}

// send delivers messages one at a time, settling after each.
func (h *harness) send(msgs ...tea.Msg) *harness {
	h.t.Helper()
	for _, m := range msgs {
		h.deliver(m)
		h.settle()
	}
	return h
}

// until moves the fake clock forward, feeding messages back, until the screen
// reaches a state. A run that never gets there fails the test.
func (h *harness) until(state string) *harness {
	h.t.Helper()
	return h.untilFunc("state "+state, func(m Model) bool { return m.StateName() == state })
}

// untilFunc is until with any condition.
func (h *harness) untilFunc(what string, done func(Model) bool) *harness {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done(h.m) {
		if time.Now().After(deadline) {
			h.t.Fatalf("the screen never reached %s; it is on %q\n%s", what, h.m.StateName(), h.view())
		}
		select {
		case msg := <-h.inbox:
			h.handle(msg, true)
			continue
		default:
		}
		if h.clock != nil {
			h.clock.Advance(250 * time.Millisecond)
		}
		time.Sleep(time.Millisecond)
	}
	return h
}

// Key helpers.

func keyCode(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func text(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(s)[0], Text: s} }

func enter() tea.Msg { return keyCode(tea.KeyEnter) }

func esc() tea.Msg { return keyCode(tea.KeyEscape) }

// key sends one typed character.
func (h *harness) key(s string) *harness { return h.send(text(s)) }

// typed sends each character of s as a key press.
func (h *harness) typed(s string) *harness {
	h.t.Helper()
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return h
}

// view is the current frame with escapes stripped.
func (h *harness) view() string { return strip(h.m.View(h.ctx)) }

// state is the screen's current step.
func (h *harness) state() string { return h.m.StateName() }

// wantState fails unless the screen is on a step.
func (h *harness) wantState(want string) *harness {
	h.t.Helper()
	if got := h.state(); got != want {
		h.t.Fatalf("the screen is on %q, want %q\n%s", got, want, h.view())
	}
	return h
}

// wantView fails unless the frame says every fragment. Where a line folds,
// and a card's border, are not part of what a frame says.
func (h *harness) wantView(fragments ...string) *harness {
	h.t.Helper()
	v := flat(h.view())
	for _, f := range fragments {
		if !strings.Contains(v, flat(f)) {
			h.t.Errorf("the frame does not say %q:\n%s", f, h.view())
		}
	}
	return h
}

// flat is a frame as running text: borders dropped, every run of blanks and
// line ends one space.
func flat(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("│", " ", "╭", " ", "╮", " ", "╰", " ", "╯", " ", "─", " ").Replace(s)), " ")
}

// Scripts.

// planFits is the check of the 3.4 mock-up: it fits in place.
func planFits() extracttest.Script { return extracttest.Check(0, extracttest.SamplePlan()) }

// planNote is a passed check with hatchzip notes under the plan.
func planNote(notes ...string) extracttest.Script {
	out := extracttest.SamplePlan()
	for _, n := range notes {
		out += "  note: " + n + "\n"
	}
	return extracttest.Check(0, out)
}

// The texts below are hatchzip 0.1.10's own, as recorded in
// plans/v0.5.4-spikes.md.

// keepNote is hatchzip saying there is room to keep the archive.
const keepNote = "there is also enough space to keep the archive (--keep-archive)"

// Errors hatchzip prints for a password, a solid RAR and a restore.
const (
	errPassword = "error: this archive is password protected\nPass --password or set HATCHZIP_PASSWORD when not running in a terminal."
	errWrongPw  = "Incorrect password.\nerror: incorrect password\nPass --password or set HATCHZIP_PASSWORD when not running in a terminal."
	errSolid    = "error: this RAR archive is solid: solid archives cannot be extracted in place. Run the command again with --keep-archive to extract it."
	restoredOut = "Archive restored. Game-Backup.zip is a standard zip archive again and contains the 5098 entries not yet extracted.\nExtracted files remain in D:\\Downloads\\Game-Backup.\n"
)

// needsPassword is a check or a run that stops for a password (exit 5).
func needsPassword() extracttest.Script { return extracttest.Check(5, "", errPassword) }

// wrongPassword is a check that was given a password that does not open it.
func wrongPassword() extracttest.Script { return extracttest.Check(5, "", errWrongPw) }

// planKeep is the plan of a check run with --keep-archive.
func planKeep() extracttest.Script {
	return extracttest.Check(0, "Game-Backup.zip  84.2 GB zip, 96.0 GB uncompressed in 12408 entries\n"+
		"  available                    212.4 GB\n"+
		"  required                     96.0 GB (+2.0 GB reserved)\n"+
		"  available after              114.4 GB\n"+
		"  note: the archive is kept, so space is required for all extracted files\n")
}

// otherNote is hatchzip saying the output folder is on another drive.
const otherNote = "the destination is on another drive, so removing the archive does not free space there"

// planOther is the plan of a check in place whose output folder is on another
// drive: the full size is needed there, and so keeping the archive fits too.
func planOther() extracttest.Script {
	return extracttest.Check(0, "Game-Backup.zip  84.2 GB zip, 96.0 GB uncompressed in 12408 entries\n"+
		"  available                    212.4 GB\n"+
		"  standard extraction needs    96.0 GB\n"+
		"  required                     96.0 GB (+2.0 GB reserved)\n"+
		"  note: "+otherNote+"\n"+
		"  available after              116.4 GB\n"+
		"  note: "+keepNote+"\n")
}

// planOtherKeep is the same check run with --keep-archive.
func planOtherKeep() extracttest.Script {
	return extracttest.Check(0, "Game-Backup.zip  84.2 GB zip, 96.0 GB uncompressed in 12408 entries\n"+
		"  available                    212.4 GB\n"+
		"  required                     96.0 GB (+2.0 GB reserved)\n"+
		"  note: the archive is kept, so space is required for all extracted files\n"+
		"  available after              116.4 GB\n")
}

// toFolder opens the folder picker from the plan card, over a disk that has
// the folders the change-folder tests pick.
func (r *rig) toFolder() *rig {
	r.t.Helper()
	for _, dir := range []string{otherParent, sysParent, `D:\work`} {
		r.fakes.FS.AddDir(dir)
	}
	r.toCheck()
	r.m.projects = `D:\work`
	r.key("o").wantState("folder")
	return r
}

// typeFolder types a folder into the open picker and presses Enter on it.
func (r *rig) typeFolder(path string) *rig {
	r.t.Helper()
	if !r.m.folder.Typing() {
		// "Type a path…" comes after the archive's own folder and the
		// projects folder, when one is set.
		if !r.onType {
			r.send(keyCode(tea.KeyDown))
			if r.m.projects != "" {
				r.send(keyCode(tea.KeyDown))
			}
		}
		r.onType = true
		r.send(enter())
	}
	if !r.m.folder.Typing() {
		r.t.Fatalf("the folder picker is not typing:\n%s", r.view())
	}
	r.typed(path).send(enter())
	return r
}

// planResuming is the check of an unfinished extraction.
func planResuming() extracttest.Script {
	return extracttest.Check(0, "Game-Backup.zip  34.0 GB zip, 96.0 GB uncompressed in 12408 entries\n"+
		"  resuming: 39.4 GB of 96.0 GB remaining\n"+
		"  available                    15.9 GB\n"+
		"  standard extraction needs    39.4 GB\n"+
		"  required                     7.4 GB (+2.0 GB reserved)\n"+
		"  available after              6.5 GB\n")
}

// planNoFit is a check that found too little space (exit 3).
func planNoFit() extracttest.Script {
	return extracttest.Check(3, "Game-Backup.zip  84.2 GB zip, 96.0 GB uncompressed in 12408 entries\n"+
		"  available                    9.6 GB\n"+
		"  standard extraction needs    96.0 GB\n"+
		"  required                     11.8 GB (+2.0 GB reserved)\n"+
		"Insufficient disk space: 4.2 GB more is required. Free up space or lower --keep-free.\n")
}

// extractAll is a scripted in-place extraction of the test archive that
// finishes: three files, one every two seconds.
func extractAll() extracttest.Script {
	return extracttest.Extract(zipOut, zipPath, zipBytes, 2*time.Second,
		extracttest.File{Name: "textures/level01.pak", Size: 30_000_000_000},
		extracttest.File{Name: "textures/level07.pak", Size: 36_000_000_000},
		extracttest.File{Name: "music/theme.ogg", Size: 30_000_000_000},
	)
}

// extractHangs is an extraction that writes one part and then keeps going
// until it is told to pause.
func extractHangs() extracttest.Script {
	return extracttest.Script{
		Stdout: extracttest.SamplePlan(),
		Steps: []extracttest.Step{{At: time.Second, Do: func(fs *extracttest.FakeFS) {
			fs.SetSize(zipOut+`\textures\level07.pak.hatchzip-part`, 20_000_000_000)
			fs.SetSize(zipPath, 64_200_000_000)
			fs.AddFile(zipPath+archive.JournalSuffix, 4096)
		}}},
		Hang: true,
	}
}

// toCheck drives a fresh rig from the picker to the plan card.
func (r *rig) toCheck() *rig {
	r.t.Helper()
	r.start()
	r.send(enter())
	r.until("check")
	return r
}

// toConfirm drives a rig to the run confirmation.
func (r *rig) toConfirm() *rig {
	r.t.Helper()
	r.toCheck()
	r.send(enter())
	r.wantState("confirm")
	return r
}

// toRunning drives a rig into a running extraction.
func (r *rig) toRunning() *rig {
	r.t.Helper()
	r.toConfirm()
	r.key("y")
	r.wantState("running")
	return r
}
