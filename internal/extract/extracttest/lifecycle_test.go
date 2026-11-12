package extracttest_test

import (
	"context"
	"errors"
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

func plan10k() string {
	return extracttest.Plan("Game-Backup.zip", "6.0 KB", "zip", "10.0 KB", 4, "31.5 GB", "10.0 KB", "3.0 KB", "31.5 GB")
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

// TestFakeLifecycleCheckStartDone runs check -> ticket -> start -> progress
// -> done over the fakes, as the screens' tests will.
func TestFakeLifecycleCheckStartDone(t *testing.T) {
	files := []extracttest.File{{Name: "a.bin", Size: 2_500}, {Name: "b.bin", Size: 2_500}, {Name: "dir/c.bin", Size: 2_500}, {Name: "d.bin", Size: 2_500}}
	eng, runner, fsys, clock := extracttest.Engine(t,
		extracttest.Check(0, plan10k()),
		extracttest.Extract(outDir, zipPath, 6_000, 2*time.Second, files...),
	)
	fsys.AddFile(zipPath, 6_000)

	res, err := eng.Check(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != extract.VerdictFits || res.Ticket == nil || res.Plan.Entries != 4 || res.Plan.TotalBytes != 10_000 {
		t.Fatalf("check = %+v", res)
	}

	var rec recorder
	job, err := eng.Start(context.Background(), res.Ticket, rec.add)
	if err != nil {
		t.Fatal(err)
	}
	o := extracttest.Wait(t, clock, job, 250*time.Millisecond)
	if o.Kind != extract.KindDone || o.Exit != 0 {
		t.Fatalf("outcome = %+v", o)
	}
	if o.Last.DoneBytes != 10_000 || o.Last.DoneItems != 4 {
		t.Fatalf("last = %+v", o.Last)
	}
	if _, ok := <-job.Done(); ok {
		t.Fatal("Done delivered a second outcome")
	}

	var prev int64
	sawWriting := false
	all := rec.list()
	for i, p := range all {
		if p.DoneBytes < prev {
			t.Fatalf("progress went backwards: %d after %d", p.DoneBytes, prev)
		}
		prev = p.DoneBytes
		if i == len(all)-1 {
			break // the final report, after exit 0, is the full total
		}
		if p.Phase == extract.PhaseWriting {
			sawWriting = true
		}
		if p.DoneBytes > 9_900 {
			t.Fatalf("passed 99%% before exit 0: %+v", p)
		}
	}
	if !sawWriting {
		t.Fatal("no writing progress was reported")
	}

	specs := runner.Specs()
	if len(specs) != 2 || specs[0].Args[0] != "check" || specs[1].Args[0] != "extract" ||
		slices.Contains(specs[0].Args, "-y") || !slices.Contains(specs[1].Args, "-y") {
		t.Fatalf("specs = %+v", specs)
	}
	if specs[1].Exe != extracttest.ToolPath {
		t.Fatalf("exe = %q", specs[1].Exe)
	}
	if fsys.Exists(zipPath) {
		t.Fatal("the scripted archive should be gone after an in-place run")
	}
	if got := eng.Pending(context.Background()); len(got) != 0 {
		t.Fatalf("pending after done = %+v", got)
	}

	// The ticket is single use.
	if _, err := eng.Start(context.Background(), res.Ticket, nil); !errors.Is(err, extract.ErrNoTicket) {
		t.Fatalf("second start = %v", err)
	}
	if _, err := eng.Start(context.Background(), nil, nil); !errors.Is(err, extract.ErrNoTicket) {
		t.Fatalf("nil ticket = %v", err)
	}
}

func TestFakeLifecycleNoTicketUnlessFits(t *testing.T) {
	for exit, want := range map[int]extract.Verdict{
		3: extract.VerdictNoSpace, 4: extract.VerdictDamaged, 5: extract.VerdictNeedPassword,
		2: extract.VerdictBadArgs, 1: extract.VerdictFailed, 9: extract.VerdictFailed,
	} {
		eng, _, _, _ := extracttest.Engine(t, extracttest.Check(exit, plan10k(), "error: something"))
		res, err := eng.Check(context.Background(), request())
		if err != nil {
			t.Fatal(err)
		}
		if res.Verdict != want || res.Ticket != nil || res.Error != "something" {
			t.Errorf("exit %d: %+v", exit, res)
		}
	}
}

// TestFakeLifecyclePauseEscalates: a run that ignores both Ctrl+Breaks is
// killed after the grace periods and reported as stopped, never failed.
func TestFakeLifecyclePauseEscalates(t *testing.T) {
	eng, runner, fsys, clock := extracttest.Engine(t,
		extracttest.Check(0, plan10k()),
		extracttest.Script{Hang: true, IgnoreBreaks: 2},
	)
	fsys.AddFile(zipPath, 6_000)
	fsys.AddFile(archive.JournalPath(zipPath), 10)
	res, err := eng.Check(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	var rec recorder
	job, err := eng.Start(context.Background(), res.Ticket, rec.add)
	if err != nil {
		t.Fatal(err)
	}
	job.Pause()
	job.Pause() // idempotent
	o := extracttest.Wait(t, clock, job, 500*time.Millisecond)
	if o.Kind != extract.KindStopped || !o.CanResume {
		t.Fatalf("outcome = %+v", o)
	}
	if runner.Interrupts() != 2 || runner.Kills() != 1 {
		t.Fatalf("breaks %d kills %d", runner.Interrupts(), runner.Kills())
	}
	var phases []string
	for _, p := range rec.list() {
		phases = append(phases, phaseName(p.Phase))
	}
	joined := strings.Join(phases, " ")
	if !strings.Contains(joined, "pausing") || !strings.Contains(joined, "stopping") {
		t.Fatalf("phases = %s", joined)
	}
	if got := eng.Pending(context.Background()); len(got) != 1 || got[0].OutDir != outDir {
		t.Fatalf("pending = %+v", got)
	}
	if err := eng.Forget(zipPath); err != nil {
		t.Fatal(err)
	}
	if got := eng.Pending(context.Background()); len(got) != 0 {
		t.Fatalf("pending after forget = %+v", got)
	}
	if !fsys.Exists(archive.JournalPath(zipPath)) {
		t.Fatal("Forget touched the journal")
	}
}

// TestFakeLifecycleCancelPauses: cancelling the context pauses with one
// Ctrl+Break, which an obedient run answers with exit 130.
func TestFakeLifecycleCancelPauses(t *testing.T) {
	eng, runner, fsys, clock := extracttest.Engine(t,
		extracttest.Check(0, plan10k()),
		extracttest.Script{Hang: true},
	)
	fsys.AddFile(zipPath, 6_000)
	res, err := eng.Check(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	job, err := eng.Start(ctx, res.Ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	o := extracttest.Wait(t, clock, job, 100*time.Millisecond)
	if o.Kind != extract.KindPaused || o.Exit != 130 {
		t.Fatalf("outcome = %+v", o)
	}
	if runner.Interrupts() != 1 || runner.Kills() != 0 {
		t.Fatalf("breaks %d kills %d", runner.Interrupts(), runner.Kills())
	}
}

func phaseName(p extract.Phase) string {
	switch p {
	case extract.PhaseStarting:
		return "starting"
	case extract.PhaseWriting:
		return "writing"
	case extract.PhaseFinishing:
		return "finishing"
	case extract.PhasePausing:
		return "pausing"
	case extract.PhaseStopping:
		return "stopping"
	case extract.PhaseAutoResume:
		return "autoresume"
	}
	return "?"
}
