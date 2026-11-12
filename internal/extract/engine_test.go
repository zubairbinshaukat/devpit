package extract_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/extract/extracttest"
)

const (
	zipPath = `D:\Downloads\Game-Backup.zip`
	outDir  = `D:\Downloads\Game-Backup`
)

func request() extract.Request {
	return extract.Request{
		Set: archive.Set{
			Picked: zipPath, First: zipPath, Parts: []string{zipPath},
			Format: archive.FormatZip, Bytes: 6_000,
		},
		OutDir: outDir,
		Mode:   extract.ModeInPlace,
	}
}

// plan10k: 10,000 bytes in 4 entries.
func plan10k() string {
	return extracttest.Plan("Game-Backup.zip", "6.0 KB", "zip", "10.0 KB", 4, "31.5 GB", "10.0 KB", "3.0 KB", "31.5 GB")
}

func resumingPlan(left string) string {
	return "Game-Backup.zip  6.0 KB zip, 10.0 KB uncompressed in 2 entries\n" +
		"  resuming: " + left + " of 10.0 KB remaining\n" +
		"  available                    31.5 GB\n" +
		"  required                     3.0 KB (+2.0 GB reserved)\n"
}

type recorder struct {
	mu  sync.Mutex
	all []extract.Progress
}

func (r *recorder) add(p extract.Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.all = append(r.all, p)
}

func (r *recorder) list() []extract.Progress {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]extract.Progress(nil), r.all...)
}

func (r *recorder) maxDone() int64 {
	var m int64
	for _, p := range r.list() {
		m = max(m, p.DoneBytes)
	}
	return m
}

func (r *recorder) sawPhase(ph extract.Phase) bool {
	for _, p := range r.list() {
		if p.Phase == ph {
			return true
		}
	}
	return false
}

// env is one engine over fresh fakes.
type env struct {
	t    *testing.T
	eng  *extract.Engine
	f    *extracttest.Fakes
	deps extract.Deps
	rec  recorder
	t0   time.Time
}

func newEnv(t *testing.T, scripts ...extracttest.Script) *env {
	t.Helper()
	d, f := extracttest.NewDeps(scripts...)
	e := &env{t: t, f: f, deps: d}
	e.eng = extract.New(extracttest.Tool(), d)
	e.t0 = f.Clock.Now()
	f.FS.AddFile(zipPath, 6_000)
	return e
}

func (e *env) check(r extract.Request) extract.CheckResult {
	e.t.Helper()
	res, err := e.eng.Check(context.Background(), r)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) start(ctx context.Context, r extract.Request) *extract.Job {
	e.t.Helper()
	res := e.check(r)
	if res.Ticket == nil {
		e.t.Fatalf("check gave no ticket: %+v", res)
	}
	j, err := e.eng.Start(ctx, res.Ticket, e.rec.add)
	if err != nil {
		e.t.Fatal(err)
	}
	return j
}

func (e *env) wait(j *extract.Job) extract.Outcome {
	e.t.Helper()
	return extracttest.Wait(e.t, e.f.Clock, j, 250*time.Millisecond)
}

func (e *env) extractSpecs() []extract.Spec {
	var out []extract.Spec
	for _, s := range e.f.Runner.Specs() {
		if len(s.Args) > 0 && s.Args[0] == "extract" {
			out = append(out, s)
		}
	}
	return out
}

// --- Tickets (rules 76, 77) ---

// Safety rule 76: a nil, zero, foreign or used ticket never starts anything.
func TestStartRefusesAnUncheckedOrUsedTicket(t *testing.T) {
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Exit: 0})
	other := newEnv(t, extracttest.Check(0, plan10k()))
	foreign := other.check(request()).Ticket
	for name, tk := range map[string]*extract.Ticket{"nil": nil, "zero": {}, "foreign": foreign} {
		if _, err := e.eng.Start(context.Background(), tk, nil); !errors.Is(err, extract.ErrNoTicket) {
			t.Errorf("%s ticket: %v", name, err)
		}
	}
	res := e.check(request())
	j, err := e.eng.Start(context.Background(), res.Ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.wait(j)
	if _, err := e.eng.Start(context.Background(), res.Ticket, nil); !errors.Is(err, extract.ErrNoTicket) {
		t.Fatalf("used ticket: %v", err)
	}
	if n := len(e.extractSpecs()); n != 1 {
		t.Fatalf("%d extract runs, want 1", n)
	}
	if len(other.extractSpecs()) != 0 {
		t.Fatal("the other engine ran an extract")
	}
}

// Safety rule 76: a ticket starts exactly the request that was checked.
func TestTicketIsForThatRequestOnly(t *testing.T) {
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Check(0, plan10k()), extracttest.Script{Exit: 0})
	keep := request()
	keep.Mode = extract.ModeKeepArchive
	resA := e.check(request())
	resB := e.check(keep)
	if resA.Ticket == nil || resB.Ticket == nil || resA.Ticket == resB.Ticket {
		t.Fatal("tickets")
	}
	j, err := e.eng.Start(context.Background(), resB.Ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.wait(j)
	specs := e.extractSpecs()
	if len(specs) != 1 || !slices.Contains(specs[0].Args, "--keep-archive") || specs[0].Args[1] != zipPath || specs[0].Args[3] != outDir {
		t.Fatalf("specs = %+v", specs)
	}
}

// Safety rule 77: only exit 0 gives a ticket.
func TestOnlyExitZeroGivesATicket(t *testing.T) {
	for _, exit := range []int{0, 1, 2, 3, 4, 5, 130, -1, 99} {
		e := newEnv(t, extracttest.Check(exit, plan10k(), "error: x"))
		res := e.check(request())
		if (res.Ticket != nil) != (exit == 0) || (res.Verdict == extract.VerdictFits) != (exit == 0) || res.Exit != exit {
			t.Errorf("exit %d: verdict %v ticket %v", exit, res.Verdict, res.Ticket != nil)
		}
	}
}

func checkNeverRunsExtract(t *testing.T, exit int, want extract.Verdict) {
	t.Helper()
	e := newEnv(t, extracttest.Check(exit, plan10k(), "error: no"), extracttest.Script{Exit: 0})
	res := e.check(request())
	if res.Verdict != want || res.Ticket != nil {
		t.Fatalf("check = %+v", res)
	}
	if _, err := e.eng.Start(context.Background(), res.Ticket, nil); !errors.Is(err, extract.ErrNoTicket) {
		t.Fatalf("start = %v", err)
	}
	if len(e.extractSpecs()) != 0 || e.f.FS.Size(zipPath) != 6_000 || e.f.Records.Saves() != 0 {
		t.Fatal("something changed after a failed check")
	}
}

// Safety rule 77.
func TestNoSpaceAtCheckNeverRunsExtract(t *testing.T) {
	checkNeverRunsExtract(t, 3, extract.VerdictNoSpace)
}

// Safety rule 77.
func TestDamagedAtCheckNeverRunsExtract(t *testing.T) {
	checkNeverRunsExtract(t, 4, extract.VerdictDamaged)
}

// Safety rule 83: a request whose paths are relative, or whose folder is not
// the archive's own <stem> folder (beside it or in a chosen parent, rule 82),
// never reaches hatchzip.
func TestArgvPathsAreAbsolute(t *testing.T) {
	bad := []func(*extract.Request){
		func(r *extract.Request) { r.Set.First = `Game-Backup.zip` },
		func(r *extract.Request) { r.Set.First = `-y.zip` },
		func(r *extract.Request) { r.OutDir = `Game-Backup` },
		func(r *extract.Request) { r.OutDir = `-y` },
		func(r *extract.Request) { r.OutDir = `D:\` },
		func(r *extract.Request) { r.OutDir = `D:\Elsewhere\Other` },
		func(r *extract.Request) { r.OutDir = `D:\Elsewhere\Game-Backup (1)` },
		func(r *extract.Request) { r.OutDir = `D:\Elsewhere\Game-Backup (x)` },
		func(r *extract.Request) { r.OutDir = `D:\Downloads\a\b` },
		func(r *extract.Request) { r.OutDir = `\\server\share\Game-Backup` },
	}
	for i, mut := range bad {
		e := newEnv(t, extracttest.Check(0, plan10k()))
		r := request()
		mut(&r)
		if _, err := e.eng.Check(context.Background(), r); err == nil {
			t.Errorf("case %d: check accepted %+v", i, r)
		}
		if _, err := e.eng.Restore(context.Background(), extract.Request{Set: archive.Set{First: `x.zip`}}, nil); err == nil {
			t.Errorf("restore accepted a relative archive")
		}
		if n := len(e.f.Runner.Specs()); n != 0 {
			t.Errorf("case %d: %d runs", i, n)
		}
	}
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Exit: 0})
	e.wait(e.start(context.Background(), request()))
	for _, s := range e.f.Runner.Specs() {
		if !filepath.IsAbs(s.Exe) && !strings.HasPrefix(s.Exe, `C:\`) {
			t.Errorf("exe %q", s.Exe)
		}
		for _, a := range s.Args[1:] {
			if !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, `D:\`) {
				t.Errorf("relative path %q in %v", a, s.Args)
			}
		}
	}
}

// --- Password (rule 79) ---

// Safety rule 79: the password is in Spec.Password only, never in an argv,
// an outcome, a record or a progress report.
func TestPasswordIsNeverInArgv(t *testing.T) {
	const pw = "correct horse battery"
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Exit: 0, Stderr: "warning: something"})
	r := request()
	r.Password = extract.NewSecret(pw)
	o := e.wait(e.start(context.Background(), r))
	specs := e.f.Runner.Specs()
	if len(specs) != 2 {
		t.Fatalf("specs = %d", len(specs))
	}
	for _, s := range specs {
		if strings.Contains(strings.Join(s.Args, " "), "correct") || s.Password.Empty() {
			t.Fatalf("spec = %+v", s)
		}
		if strings.Contains(fmt.Sprintf("%v %+v %#v", s, s, s), "correct") {
			t.Fatal("a printed spec shows the password")
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", o), "correct") {
		t.Fatal("outcome shows the password")
	}
	b, _ := json.Marshal(e.f.Records.All())
	if strings.Contains(string(b), "correct") {
		t.Fatal("record holds the password")
	}
	for _, p := range e.rec.list() {
		if strings.Contains(fmt.Sprintf("%+v", p), "correct") {
			t.Fatal("progress shows the password")
		}
	}
}

// --- Poller (rule 78, 84) ---

// Safety rule 78: during a run the engine only Lstats and lists folder
// names, and the archive and its journal are only ever Lstat'ed.
func TestPollerUsesOnlyLstatAndFolderNames(t *testing.T) {
	files := []extracttest.File{{Name: "a.bin", Size: 2_500}, {Name: "sub/b.bin", Size: 2_500}, {Name: "c.bin", Size: 2_500}, {Name: "d.bin", Size: 2_500}}
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Extract(outDir, zipPath, 6_000, 2*time.Second, files...))
	e.f.FS.AddFile(archive.JournalPath(zipPath), 100)
	if o := e.wait(e.start(context.Background(), request())); o.Kind != extract.KindDone {
		t.Fatalf("outcome = %+v", o)
	}
	calls := e.f.FS.Calls()
	if len(calls) == 0 {
		t.Fatal("no calls recorded")
	}
	for _, c := range calls {
		op, p, _ := strings.Cut(c, " ")
		switch op {
		case "lstat":
		case "readdir":
			if !strings.HasPrefix(strings.ToLower(p), strings.ToLower(outDir)) {
				t.Errorf("listed a folder outside the output: %s", c)
			}
		default:
			t.Errorf("unexpected call %q", c)
		}
	}
}

// A part file renamed to its final name is counted once.
func TestAPartRenamedToItsFinalNameIsCountedOnce(t *testing.T) {
	part := outDir + `\a.bin.hatchzip-part`
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Hang: true, Steps: []extracttest.Step{
		{At: time.Second, Do: func(fs *extracttest.FakeFS) { fs.SetSize(part, 500) }},
		{At: 4 * time.Second, Do: func(fs *extracttest.FakeFS) { fs.SetSize(part, 1_000) }},
		{At: 5 * time.Second, Do: func(fs *extracttest.FakeFS) { fs.Rename(part, outDir+`\a.bin`) }},
	}})
	j := e.start(context.Background(), request())
	for range 60 {
		e.f.Clock.Advance(250 * time.Millisecond)
		time.Sleep(time.Millisecond)
	}
	j.Pause()
	e.wait(j)
	if got := e.rec.maxDone(); got != 1_000 {
		t.Fatalf("max done = %d, want 1000", got)
	}
	last := e.rec.list()[len(e.rec.list())-1]
	if last.DoneItems != 1 {
		t.Fatalf("items = %d", last.DoneItems)
	}
}

// Rule 84: the bar never goes backwards, even when a part file is rewritten
// from zero (spike X8), and stays at most 99% until exit 0.
func TestProgressIsMonotonicAndCappedUntilExitZero(t *testing.T) {
	part := outDir + `\big.bin.hatchzip-part`
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Exit: 0, Steps: []extracttest.Step{
		{At: 4 * time.Second, Do: func(fs *extracttest.FakeFS) { fs.SetSize(part, 6_000) }},
		{At: 6 * time.Second, Do: func(fs *extracttest.FakeFS) { fs.SetSize(part, 100) }},
		{At: 8 * time.Second, Do: func(fs *extracttest.FakeFS) { fs.SetSize(part, 12_000) }},
		{At: 10 * time.Second, Do: func(fs *extracttest.FakeFS) { fs.Rename(part, outDir+`\big.bin`) }},
		{At: 14 * time.Second},
	}})
	o := e.wait(e.start(context.Background(), request()))
	if o.Kind != extract.KindDone {
		t.Fatalf("outcome %+v", o)
	}
	all := e.rec.list()
	var prev int64
	for i, p := range all {
		if p.DoneBytes < prev {
			t.Fatalf("went backwards at %d: %d after %d", i, p.DoneBytes, prev)
		}
		prev = p.DoneBytes
		if i < len(all)-1 && (p.DoneBytes > 9_900 || p.DoneItems > 3) {
			t.Fatalf("passed 99%% before exit 0: %+v", p)
		}
	}
	if last := all[len(all)-1]; last.DoneBytes != 10_000 || last.DoneItems != 4 || last.ETA != 0 {
		t.Fatalf("final = %+v", last)
	}
	if !e.rec.sawPhase(extract.PhaseFinishing) {
		t.Fatal("the stalled end was never reported as finishing")
	}
}

// On a resume the bytes already extracted come from the plan, and files that
// were already there (including a full-size part left by a pause) add
// nothing until they grow.
func TestResumeBaselineComesFromThePlan(t *testing.T) {
	part := outDir + `\b.bin.hatchzip-part`
	e := newEnv(t, extracttest.Check(0, resumingPlan("4.0 KB")), extracttest.Script{Hang: true, Steps: []extracttest.Step{
		{At: 2 * time.Second, Do: func(fs *extracttest.FakeFS) { fs.SetSize(part, 0) }},
		{At: 5 * time.Second, Do: func(fs *extracttest.FakeFS) { fs.SetSize(part, 3_000) }},
	}})
	e.f.FS.AddFile(outDir+`\a.bin`, 6_000)
	e.f.FS.AddFile(part, 4_000) // left full size by a pause
	e.f.FS.AddFile(archive.JournalPath(zipPath), 10)
	j := e.start(context.Background(), request())
	for range 40 {
		e.f.Clock.Advance(250 * time.Millisecond)
		time.Sleep(time.Millisecond)
	}
	j.Pause()
	e.wait(j)
	all := e.rec.list()
	if all[0].DoneBytes != 6_000 {
		t.Fatalf("first report = %+v, want the plan's 6000 done", all[0])
	}
	if got := e.rec.maxDone(); got != 9_000 {
		t.Fatalf("max done = %d, want 6000 + 3000", got)
	}
	if all[0].TotalItems != 2 {
		t.Fatalf("items left = %d", all[0].TotalItems)
	}
}

// A final-named file is stat'ed until its size settles, then never again;
// files present at the start are never stat'ed again.
func TestFrozenFilesAreNotStatedAgain(t *testing.T) {
	old := outDir + `\old.bin`
	fresh := outDir + `\fresh.bin`
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Hang: true, Steps: []extracttest.Step{
		{At: time.Second, Do: func(fs *extracttest.FakeFS) { fs.SetSize(fresh, 2_000) }},
	}})
	e.f.FS.AddFile(old, 1_000)
	j := e.start(context.Background(), request())
	for range 80 {
		e.f.Clock.Advance(250 * time.Millisecond)
		time.Sleep(time.Millisecond)
	}
	j.Pause()
	e.wait(j)
	count := func(p string) int {
		n := 0
		for _, c := range e.f.FS.Calls() {
			if strings.EqualFold(c, "lstat "+p) {
				n++
			}
		}
		return n
	}
	if n := count(old); n != 1 {
		t.Errorf("old file stat'ed %d times, want once (the baseline)", n)
	}
	if n := count(fresh); n < 2 || n > 3 {
		t.Errorf("new file stat'ed %d times, want until stable (2–3)", n)
	}
}

// Links in the output are listed as entries but never entered or sized.
func TestLinksInTheOutputAreNotSized(t *testing.T) {
	link := outDir + `\link`
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Hang: true, Steps: []extracttest.Step{
		{At: time.Second, Do: func(fs *extracttest.FakeFS) { fs.AddLink(link); fs.AddFile(link+`\inner.bin`, 5_000) }},
	}})
	e.f.FS.AddDir(outDir)
	j := e.start(context.Background(), request())
	for range 40 {
		e.f.Clock.Advance(250 * time.Millisecond)
		time.Sleep(time.Millisecond)
	}
	j.Pause()
	e.wait(j)
	for _, c := range e.f.FS.Calls() {
		if strings.Contains(strings.ToLower(c), `\link\`) || strings.EqualFold(c, "readdir "+link) {
			t.Fatalf("entered the link: %s", c)
		}
	}
	if got := e.rec.maxDone(); got != 0 {
		t.Fatalf("a link was sized: %d", got)
	}
}

// Rule 84: while running, a known time left is never under a second.
func TestETAIsNeverZeroWhileRunning(t *testing.T) {
	files := []extracttest.File{{Name: "a.bin", Size: 2_500}, {Name: "b.bin", Size: 2_500}, {Name: "c.bin", Size: 2_500}, {Name: "d.bin", Size: 2_500}}
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Extract(outDir, zipPath, 6_000, 4*time.Second, files...))
	e.wait(e.start(context.Background(), request()))
	all := e.rec.list()
	sawSpeed := false
	for _, p := range all[:len(all)-1] {
		if p.Speed >= 1 {
			sawSpeed = true
			if p.ETA < time.Second {
				t.Fatalf("ETA %v while running: %+v", p.ETA, p)
			}
		}
	}
	if !sawSpeed {
		t.Fatal("no speed was ever measured")
	}
}

// --- Pause and kill (rule 80) ---

// Safety rule 80: Ctrl+Break, a second one after PauseGrace, then the Job
// kill after BreakGrace.
func TestPauseSendsBreakThenBreakThenKill(t *testing.T) {
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Hang: true, IgnoreBreaks: 2})
	e.f.FS.AddFile(archive.JournalPath(zipPath), 10)
	j := e.start(context.Background(), request())
	j.Pause()
	breaksAt := map[int]time.Duration{}
	deadline := time.Now().Add(10 * time.Second)
	for e.f.Runner.Kills() == 0 && time.Now().Before(deadline) {
		if n := e.f.Runner.Interrupts(); n > 0 {
			if _, ok := breaksAt[n]; !ok {
				breaksAt[n] = e.f.Clock.Now().Sub(e.t0)
			}
		}
		e.f.Clock.Advance(100 * time.Millisecond)
		time.Sleep(2 * time.Millisecond)
	}
	killAt := e.f.Clock.Now().Sub(e.t0)
	o := e.wait(j)
	if e.f.Runner.Interrupts() != 2 || e.f.Runner.Kills() != 1 {
		t.Fatalf("breaks %d kills %d", e.f.Runner.Interrupts(), e.f.Runner.Kills())
	}
	if breaksAt[1] > 200*time.Millisecond {
		t.Errorf("first break at %v, want at once", breaksAt[1])
	}
	if d := breaksAt[2] - breaksAt[1]; d < extract.DefaultPauseGrace || d > extract.DefaultPauseGrace+time.Second {
		t.Errorf("second break %v after the first, want %v", d, extract.DefaultPauseGrace)
	}
	if d := killAt - breaksAt[2]; d < extract.DefaultBreakGrace || d > extract.DefaultBreakGrace+time.Second {
		t.Errorf("kill %v after the second break, want %v", d, extract.DefaultBreakGrace)
	}
	if o.Kind != extract.KindStopped || !o.CanResume {
		t.Fatalf("outcome = %+v", o)
	}
	if !e.rec.sawPhase(extract.PhasePausing) || !e.rec.sawPhase(extract.PhaseStopping) {
		t.Fatal("pausing and stopping were not reported")
	}
}

// Safety rule 80: cancelling Start's context pauses with one Ctrl+Break; it
// never kills first.
func TestCancellingTheContextPausesNotKills(t *testing.T) {
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Hang: true})
	ctx, cancel := context.WithCancel(context.Background())
	j := e.start(ctx, request())
	cancel()
	o := e.wait(j)
	if o.Kind != extract.KindPaused || o.Exit != 130 || e.f.Runner.Interrupts() != 1 || e.f.Runner.Kills() != 0 {
		t.Fatalf("outcome %+v breaks %d kills %d", o, e.f.Runner.Interrupts(), e.f.Runner.Kills())
	}
}

// Safety rule 80: Devpit's own kill is "stopped", resumable, never "failed",
// although Go reports exit 1 for it.
func TestOurKillIsStoppedNotFailed(t *testing.T) {
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Hang: true, IgnoreBreaks: 2})
	e.f.FS.AddFile(archive.JournalPath(zipPath), 10)
	j := e.start(context.Background(), request())
	j.Pause()
	o := e.wait(j)
	if o.Kind != extract.KindStopped || o.Exit != 1 || !o.CanResume || o.AutoResumed {
		t.Fatalf("outcome = %+v", o)
	}
	if n := len(e.extractSpecs()); n != 1 {
		t.Fatalf("%d extract runs after our kill", n)
	}
}

// appStopGrace mirrors internal/app.StopGrace (not imported: the engine must
// not depend on the app); the test reads app.go to keep the mirror honest.
const appStopGrace = 10 * time.Second

// Safety rule 80: the whole escalation fits inside the app's Ctrl+C grace.
func TestPauseFitsInsideTheAppStopGrace(t *testing.T) {
	if extract.DefaultPauseGrace+extract.DefaultBreakGrace >= appStopGrace {
		t.Fatalf("%v + %v >= %v", extract.DefaultPauseGrace, extract.DefaultBreakGrace, appStopGrace)
	}
	src, err := os.ReadFile(filepath.Join("..", "app", "app.go"))
	if err != nil {
		t.Skip("app.go not readable:", err)
	}
	if !regexp.MustCompile(`const StopGrace = 10 \* time\.Second`).Match(src) {
		t.Fatal("app.StopGrace changed: update appStopGrace and check the grace periods")
	}
}

// Pause and context cancel together still send one Ctrl+Break.
func TestPauseIsIdempotent(t *testing.T) {
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Hang: true})
	ctx, cancel := context.WithCancel(context.Background())
	j := e.start(ctx, request())
	j.Pause()
	j.Pause()
	cancel()
	j.Pause()
	o := e.wait(j)
	j.Pause()
	if o.Kind != extract.KindPaused || e.f.Runner.Interrupts() != 1 {
		t.Fatalf("outcome %+v breaks %d", o, e.f.Runner.Interrupts())
	}
}

// --- Auto-resume (rule 85) ---

// bug79Line is what hatchzip 0.1.10 prints when it stops on its false "disk
// full" (issue #79).
const bug79Line = "error: not enough disk space to continue: 'd08\\file00388.bin' requires 401.4 KB and 0 B is available (2.0 GB reserved)."

// maxAutoResumes mirrors the engine's cap (rule 85).
const maxAutoResumes = 20

// fullEarly is the nth run of a job that stops on the false "disk full" with
// the given exit code after it made progress: files says it wrote one more
// 200-byte file, shrink that it cut 100 bytes off the archive.
func fullEarly(n, exit int, files, shrink bool) extracttest.Script {
	return extracttest.Script{Exit: exit, Stderr: bug79Line, Steps: []extracttest.Step{{Do: func(fs *extracttest.FakeFS) {
		if files {
			fs.SetSize(fmt.Sprintf(`%s\f%02d.bin`, outDir, n), 200)
		}
		if shrink {
			fs.SetSize(zipPath, int64(6_000-100*n))
		}
	}}}}
}

// fullEarlyRuns is runs 1..n of fullEarly, each extracting and shrinking.
func fullEarlyRuns(n int) []extracttest.Script {
	out := []extracttest.Script{extracttest.Check(0, plan10k())}
	for i := 1; i <= n; i++ {
		out = append(out, fullEarly(i, 1, true, true))
	}
	return out
}

// bug79Env is an engine with the journal beside the archive.
func bug79Env(t *testing.T, scripts ...extracttest.Script) *env {
	t.Helper()
	e := newEnv(t, scripts...)
	e.f.FS.AddFile(archive.JournalPath(zipPath), 10)
	return e
}

// Safety rule 85: a run that stops on hatchzip's false "disk full" with the
// journal there is resumed, again and again while each run makes progress,
// and it says so: the count is in the progress and the outcome, and the bar
// never goes backwards across the runs.
func TestBug79ResumesWhileProgressing(t *testing.T) {
	e := bug79Env(t, append(fullEarlyRuns(5), extracttest.Script{Exit: 0})...)
	o := e.wait(e.start(context.Background(), request()))
	if o.Kind != extract.KindDone || !o.AutoResumed || o.AutoResumes != 5 {
		t.Fatalf("outcome = %+v", o)
	}
	if n := len(e.extractSpecs()); n != 6 {
		t.Fatalf("%d extract runs, want 6", n)
	}
	var prev extract.Progress
	sawResume := false
	for _, p := range e.rec.list() {
		if p.DoneBytes < prev.DoneBytes || p.AutoResumes < prev.AutoResumes {
			t.Fatalf("progress went backwards: %+v after %+v", p, prev)
		}
		sawResume = sawResume || p.Phase == extract.PhaseAutoResume
		prev = p
	}
	if !sawResume || prev.AutoResumes != 5 || o.Last.AutoResumes != 5 {
		t.Fatalf("the auto-resumes were not reported: saw %v, last %d, outcome %d", sawResume, prev.AutoResumes, o.Last.AutoResumes)
	}
	if e.f.Awake.Total() != 1 || e.f.Awake.Held() != 0 {
		t.Fatalf("keep awake: total %d held %d", e.f.Awake.Total(), e.f.Awake.Held())
	}

	// Either sign of progress is enough, and exit 3 ("no space") counts
	// without the line: new bytes in the folder, or a smaller archive.
	for name, s := range map[string]extracttest.Script{
		"only new files, exit 1":       fullEarly(1, 1, true, false),
		"only a smaller archive":       fullEarly(1, 1, false, true),
		"exit 3":                       fullEarly(1, 3, true, true),
		"exit 3 without hatchzip line": {Exit: 3, Steps: fullEarly(1, 3, true, true).Steps},
	} {
		e := bug79Env(t, extracttest.Check(0, plan10k()), s, extracttest.Script{Exit: 0})
		o := e.wait(e.start(context.Background(), request()))
		if o.Kind != extract.KindDone || o.AutoResumes != 1 || len(e.extractSpecs()) != 2 {
			t.Errorf("%s: outcome %+v runs %d", name, o, len(e.extractSpecs()))
		}
	}
}

// Safety rule 85: a run that extracted nothing and left the archive as big
// as it was is not resumed: the disk really is full, and the failed card
// shows with the count of the resumes that did happen.
func TestBug79StopsWhenAnAttemptMakesNoProgress(t *testing.T) {
	stuck := extracttest.Script{Exit: 1, Stderr: bug79Line}

	// The first run gets nowhere: nothing is resumed.
	e := bug79Env(t, extracttest.Check(0, plan10k()), stuck, extracttest.Script{Exit: 0})
	o := e.wait(e.start(context.Background(), request()))
	if o.Kind != extract.KindFailed || o.AutoResumed || o.AutoResumes != 0 || !o.CanResume || len(e.extractSpecs()) != 1 {
		t.Fatalf("no progress in the first run: %+v, runs %d", o, len(e.extractSpecs()))
	}
	if e.rec.sawPhase(extract.PhaseAutoResume) {
		t.Fatal("a resume was announced that did not happen")
	}

	// Two runs progress, the third does not: two resumes, then the end.
	e = bug79Env(t, append(fullEarlyRuns(2), stuck, extracttest.Script{Exit: 0})...)
	o = e.wait(e.start(context.Background(), request()))
	if o.Kind != extract.KindFailed || o.AutoResumes != 2 || !o.CanResume || len(e.extractSpecs()) != 3 {
		t.Fatalf("no progress in the third run: %+v, runs %d", o, len(e.extractSpecs()))
	}
	if !strings.Contains(o.Error, "not enough disk space") {
		t.Fatalf("the card would not say why: %q", o.Error)
	}
}

// Safety rule 85: at most 20 automatic resumes per job, however well each
// run progresses.
func TestBug79StopsAtTheCap(t *testing.T) {
	e := bug79Env(t, append(fullEarlyRuns(maxAutoResumes+5), extracttest.Script{Exit: 0})...)
	o := e.wait(e.start(context.Background(), request()))
	if o.Kind != extract.KindFailed || o.AutoResumes != maxAutoResumes || !o.CanResume {
		t.Fatalf("outcome = %+v", o)
	}
	if n := len(e.extractSpecs()); n != maxAutoResumes+1 {
		t.Fatalf("%d extract runs, want the first and %d resumes", n, maxAutoResumes)
	}
}

// Safety rule 85: never after Devpit's own pause or kill, in the first run,
// in the wait before a resume, or in a resumed run.
func TestBug79NeverAfterOurPause(t *testing.T) {
	for name, s := range map[string]extracttest.Script{
		"pause, exit 130":         {Hang: true},
		"pause, hatchzip exits 1": {Hang: true, BreakExit: 1, Stderr: bug79Line, Steps: fullEarly(1, 1, true, true).Steps},
		"kill":                    {Hang: true, IgnoreBreaks: 2, Stderr: bug79Line, Steps: fullEarly(1, 1, true, true).Steps},
	} {
		e := bug79Env(t, extracttest.Check(0, plan10k()), s, extracttest.Script{Exit: 0})
		j := e.start(context.Background(), request())
		j.Pause()
		o := e.wait(j)
		if o.AutoResumed || o.AutoResumes != 0 || len(e.extractSpecs()) != 1 || o.Kind == extract.KindDone {
			t.Errorf("%s: outcome %+v runs %d", name, o, len(e.extractSpecs()))
		}
	}

	// A pause during the 2 s wait before the resume: nothing more runs, and
	// the resume that was announced is not counted.
	e := bug79Env(t, extracttest.Check(0, plan10k()), fullEarly(1, 1, true, true), extracttest.Script{Exit: 0})
	j := e.start(context.Background(), request())
	deadline := time.Now().Add(5 * time.Second)
	for !e.rec.sawPhase(extract.PhaseAutoResume) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	j.Pause()
	o := e.wait(j)
	if o.Kind != extract.KindPaused || o.AutoResumed || o.AutoResumes != 0 || o.Last.AutoResumes != 0 || len(e.extractSpecs()) != 1 {
		t.Fatalf("pause in the wait: %+v runs %d", o, len(e.extractSpecs()))
	}

	// A pause in a resumed run ends the job there, with one resume counted.
	e = bug79Env(t, extracttest.Check(0, plan10k()), fullEarly(1, 1, true, true),
		extracttest.Script{Hang: true, BreakExit: 1, Stderr: bug79Line, Steps: fullEarly(2, 1, true, true).Steps},
		extracttest.Script{Exit: 0})
	j = e.start(context.Background(), request())
	deadline = time.Now().Add(5 * time.Second)
	for len(e.extractSpecs()) < 2 && time.Now().Before(deadline) {
		e.f.Clock.Advance(250 * time.Millisecond)
		time.Sleep(time.Millisecond)
	}
	j.Pause()
	o = e.wait(j)
	if o.Kind == extract.KindDone || o.AutoResumes != 1 || len(e.extractSpecs()) != 2 {
		t.Fatalf("pause in a resumed run: %+v runs %d", o, len(e.extractSpecs()))
	}
}

// Safety rule 85: only hatchzip's "not enough disk space" stop is resumed.
// Any other exit 1, a solid RAR, any other exit code, or no journal beside
// the archive ends the job at once, even when the run made progress.
func TestBug79NotForOtherExit1Errors(t *testing.T) {
	progress := fullEarly(1, 1, true, true).Steps
	for name, s := range map[string]extracttest.Script{
		"file in use":         {Exit: 1, Stderr: `error: could not write textures\level07.pak: the file is in use by another program`, Steps: progress},
		"no message":          {Exit: 1, Steps: progress},
		"disk on stdout only": {Exit: 1, Stdout: bug79Line, Steps: progress},
		"solid RAR":           {Exit: 1, Stderr: "error: this RAR archive is solid: so it cannot be extracted in place", Steps: progress},
		"exit 4":              {Exit: 4, Stderr: bug79Line, Steps: progress},
		"exit 5":              {Exit: 5, Stderr: bug79Line, Steps: progress},
		"exit 2":              {Exit: 2, Stderr: bug79Line, Steps: progress},
		"exit 9":              {Exit: 9, Stderr: bug79Line, Steps: progress},
	} {
		e := bug79Env(t, extracttest.Check(0, plan10k()), s, extracttest.Script{Exit: 0})
		o := e.wait(e.start(context.Background(), request()))
		if o.AutoResumed || o.AutoResumes != 0 || len(e.extractSpecs()) != 1 || o.Kind == extract.KindDone {
			t.Errorf("%s: outcome %+v runs %d", name, o, len(e.extractSpecs()))
		}
	}

	// A reworded tail still counts: only the phrase is matched.
	reworded := fullEarly(1, 1, true, true)
	reworded.Stderr = "error: Not enough disk space to go on (need 941.7 MB, 169.7 MB free)"
	e := bug79Env(t, extracttest.Check(0, plan10k()), reworded, extracttest.Script{Exit: 0})
	if o := e.wait(e.start(context.Background(), request())); o.Kind != extract.KindDone || o.AutoResumes != 1 {
		t.Errorf("reworded line: outcome %+v", o)
	}

	// No journal: nothing to resume from.
	e = newEnv(t, extracttest.Check(0, plan10k()), fullEarly(1, 1, true, true), extracttest.Script{Exit: 0})
	o := e.wait(e.start(context.Background(), request()))
	if o.Kind != extract.KindFailed || o.AutoResumed || o.CanResume || len(e.extractSpecs()) != 1 {
		t.Fatalf("no journal: outcome %+v", o)
	}
}

// --- KeepAwake (rule 87) ---

// Safety rule 87: the PC is kept awake for exactly the run.
func TestKeepAwakeIsHeldForTheRunAndReleased(t *testing.T) {
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Hang: true})
	j := e.start(context.Background(), request())
	if e.f.Awake.Held() != 1 {
		t.Fatalf("held %d while running", e.f.Awake.Held())
	}
	j.Pause()
	e.wait(j)
	if e.f.Awake.Held() != 0 || e.f.Awake.Total() != 1 {
		t.Fatalf("held %d total %d after the end", e.f.Awake.Held(), e.f.Awake.Total())
	}
}

// --- Records (rules 79, 81) ---

// recordingRunner checks the record is saved before hatchzip extract starts.
type recordingRunner struct {
	inner   extract.Runner
	records *extracttest.Records
	mu      sync.Mutex
	seen    []int // records stored at each extract start
}

func (r *recordingRunner) Start(s extract.Spec) (extract.Process, error) {
	if len(s.Args) > 0 && s.Args[0] == "extract" {
		r.mu.Lock()
		r.seen = append(r.seen, len(r.records.All()))
		r.mu.Unlock()
	}
	return r.inner.Start(s)
}

func TestRecordIsWrittenBeforeExtractStarts(t *testing.T) {
	d, f := extracttest.NewDeps(extracttest.Check(0, plan10k()), extracttest.Script{Exit: 0})
	rr := &recordingRunner{inner: f.Runner, records: f.Records}
	d.Runner = rr
	eng := extract.New(extracttest.Tool(), d)
	res, err := eng.Check(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	j, err := eng.Start(context.Background(), res.Ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	extracttest.Wait(t, f.Clock, j, 250*time.Millisecond)
	if len(rr.seen) != 1 || rr.seen[0] != 1 {
		t.Fatalf("records at extract start = %v", rr.seen)
	}
	// A record store that cannot save stops the run before it starts.
	d2, f2 := extracttest.NewDeps(extracttest.Check(0, plan10k()), extracttest.Script{Exit: 0})
	f2.Records.Fail(errors.New("disk full"))
	eng2 := extract.New(extracttest.Tool(), d2)
	res2, _ := eng2.Check(context.Background(), request())
	if _, err := eng2.Start(context.Background(), res2.Ticket, nil); err == nil {
		t.Fatal("started without a record")
	}
	for _, s := range f2.Runner.Specs() {
		if s.Args[0] == "extract" {
			t.Fatal("extract ran without a record")
		}
	}
}

func TestRecordIsRemovedOnDone(t *testing.T) {
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Exit: 0})
	e.wait(e.start(context.Background(), request()))
	if recs := e.f.Records.All(); len(recs) != 0 {
		t.Fatalf("records after done = %+v", recs)
	}
	// Any other ending keeps it.
	e = newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Exit: 4})
	e.wait(e.start(context.Background(), request()))
	if recs := e.f.Records.All(); len(recs) != 1 || recs[0].First != zipPath || recs[0].OutDir != outDir || recs[0].Tool != extracttest.ToolVersion {
		t.Fatalf("records after a failure = %+v", recs)
	}
}

// Safety rule 81: Forget drops Devpit's record and touches no side file.
func TestForgetDropsOnlyTheRecord(t *testing.T) {
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Exit: 130})
	sides := []string{archive.JournalPath(zipPath), zipPath + archive.TailSuffix, zipPath + archive.FoldersSuffix, outDir + `\x.bin` + archive.PartSuffix}
	for _, s := range sides {
		e.f.FS.AddFile(s, 10)
	}
	e.wait(e.start(context.Background(), request()))
	if got := e.eng.Pending(context.Background()); len(got) != 1 {
		t.Fatalf("pending = %+v", got)
	}
	before := len(e.f.FS.Calls())
	if err := e.eng.Forget(zipPath); err != nil {
		t.Fatal(err)
	}
	if after := len(e.f.FS.Calls()); after != before {
		t.Fatalf("Forget touched the file system: %v", e.f.FS.Calls()[before:])
	}
	for _, s := range append(sides, zipPath) {
		if !e.f.FS.Exists(s) {
			t.Fatalf("%s is gone", s)
		}
	}
	if got := e.eng.Pending(context.Background()); len(got) != 0 {
		t.Fatalf("pending after forget = %+v", got)
	}
}

// Safety rule 81: a record whose journal is gone is dropped; nothing on disk
// is touched (only Lstat).
func TestAGoneJournalDropsTheRecordAndTouchesNothing(t *testing.T) {
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Exit: 130})
	e.f.FS.AddFile(archive.JournalPath(zipPath), 10)
	e.wait(e.start(context.Background(), request()))
	e.f.FS.Remove(archive.JournalPath(zipPath)) // hatchzip finished elsewhere
	before := len(e.f.FS.Calls())
	if got := e.eng.Pending(context.Background()); len(got) != 0 {
		t.Fatalf("pending = %+v", got)
	}
	for _, c := range e.f.FS.Calls()[before:] {
		if !strings.HasPrefix(c, "lstat ") {
			t.Fatalf("Pending did %q", c)
		}
	}
	if recs := e.f.Records.All(); len(recs) != 0 {
		t.Fatalf("record kept: %+v", recs)
	}
	if !e.f.FS.Exists(zipPath) {
		t.Fatal("the archive was touched")
	}
}

// Restore runs `restore <first>` and keeps the record while the journal is
// there.
func TestRestoreRunsAndReportsLines(t *testing.T) {
	e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Exit: 130}, extracttest.Script{
		Stdout: "Archive restored. Game-Backup.zip is a standard zip archive again and contains the 3 entries not yet extracted.\nExtracted files remain in D:\\Downloads\\Game-Backup.",
		Steps:  []extracttest.Step{{At: 0, Do: func(fs *extracttest.FakeFS) { fs.Remove(archive.JournalPath(zipPath)) }}},
	})
	e.f.FS.AddFile(archive.JournalPath(zipPath), 10)
	e.wait(e.start(context.Background(), request()))
	var lines []string
	o, err := e.eng.Restore(context.Background(), request(), func(s string) { lines = append(lines, s) })
	if err != nil {
		t.Fatal(err)
	}
	specs := e.f.Runner.Specs()
	if last := specs[len(specs)-1]; !slices.Equal(last.Args, []string{"restore", zipPath}) {
		t.Fatalf("argv = %v", last.Args)
	}
	if o.Kind != extract.KindDone || o.CanResume || len(lines) != 2 {
		t.Fatalf("outcome %+v lines %q", o, lines)
	}
	if recs := e.f.Records.All(); len(recs) != 0 {
		t.Fatalf("record kept after a restore: %+v", recs)
	}
}
