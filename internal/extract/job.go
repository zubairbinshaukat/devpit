package extract

import (
	"context"
	"strconv"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
)

// Pause is the same as cancelling Start's ctx; non-blocking, idempotent.
func (j *Job) Pause() { j.pauseOnce.Do(func() { close(j.pause) }) }

// Done delivers exactly one Outcome, then is closed.
func (j *Job) Done() <-chan Outcome { return j.done }

// LogPath is the job's run log (rule 91), or "" when none is written.
func (j *Job) LogPath() string { return j.logPath }

// autoResumeDelay is the wait before each automatic resume (6.7).
const autoResumeDelay = 2 * time.Second

// maxAutoResumes caps the automatic resumes of one job (rule 85).
const maxAutoResumes = 20

// waited is how one hatchzip process ended.
type waited struct {
	exit    int
	killed  bool // Devpit ended it through its Job after the grace periods
	pausing bool // Devpit asked it to pause (ctx ended or Pause)
	out     ranOut
}

// run drives the job from the first process to its Outcome: the pause
// escalation (rule 80), the automatic resumes (rule 85), the record, the
// progress reports and the run log (rule 91).
func (e *Engine) run(ctx context.Context, j *Job, r Request, p Process, tr *tracker, release func(), lg *runLog) {
	defer release()
	tr.emit(func(pr *Progress) { pr.Phase = PhaseStarting })

	// Pausing, not ctx, ends the run, so the helpers outlive ctx's cancel.
	detached := context.WithoutCancel(ctx)
	pollCtx, stopPoll := context.WithCancel(detached)
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		tr.loop(pollCtx)
	}()

	var (
		w            waited
		lines        []string
		resumes      int
		pausedInWait bool
	)
	before := tr.first // taken before the first run started
	for {
		w = e.watch(ctx, j, p, tr, lg)
		lines = appendKeep(lines, w.out.lines...)
		ok, why := e.mayAutoResume(w, r)
		if ok {
			// The run is over, so the folder is measured once more: what
			// this run extracted decides whether another one is worth it.
			after := tr.measure()
			ok, why = resumeVerdict(resumes, before, after)
			before = after
		}
		lg.printf("auto-resume (bug #79): %s, %s", yesNo(ok), why)
		if !ok {
			break
		}
		// Bug #79: hatchzip reported the disk full too early, and closing the
		// archive freed the space it had already given back. Run it again,
		// with the same request and the password still in memory.
		attempt := resumes + 1
		tr.emit(func(pr *Progress) { pr.Phase, pr.AutoResumes = PhaseAutoResume, attempt })
		if !e.wait(ctx, j, autoResumeDelay) {
			lg.printf("auto-resume: asked to pause during the %s wait; nothing runs", autoResumeDelay)
			pausedInWait = true // asked to pause while waiting: nothing runs
			break
		}
		spec := Spec{Exe: e.tool.Path, Args: extractArgs(r), Password: r.Password}
		lg.spec(spec)
		next, err := e.d.Runner.Start(spec)
		if err != nil {
			lg.printf("auto-resume: hatchzip did not start: %v", err)
			break
		}
		lg.printf("auto-resume: started (attempt %d of %d)", attempt, maxAutoResumes)
		resumes = attempt
		p = next
		tr.emit(func(pr *Progress) { pr.Phase = PhaseStarting })
	}
	// A resume that was announced but never ran is not counted.
	if tr.last().AutoResumes != resumes {
		tr.emit(func(pr *Progress) { pr.AutoResumes = resumes })
	}
	stopPoll()
	<-pollDone

	kind := classify(w.exit, w.killed, r.Mode, !r.Password.Empty(), w.out.stderrText())
	if pausedInWait {
		// Paused between the false "disk full" and the resume: nothing is
		// running, the journal is there, so it is a pause, not a failure.
		kind = KindPaused
	}
	o := Outcome{
		Kind:        kind,
		Exit:        w.exit,
		Error:       w.out.errLine,
		Lines:       lines,
		AutoResumed: resumes > 0,
		AutoResumes: resumes,
		Elapsed:     e.d.Now().Sub(tr.start),
	}
	if kind == KindDone {
		tr.emit(func(pr *Progress) {
			pr.DoneBytes = pr.TotalBytes
			pr.DoneItems = pr.TotalItems
			pr.ETA = 0
		})
		_ = e.removeRecord(r.Set.First)
	} else {
		o.CanResume = e.journalExists(r.Set.First)
	}
	o.Last = tr.last()
	lg.progress("last", o.Last)
	lg.printf("outcome: exit %d, kind %s, auto-resumed %v (%d of at most %d), journal there %v, error %q", o.Exit, kindName(o.Kind), o.AutoResumed, o.AutoResumes, maxAutoResumes, o.CanResume, o.Error)
	lg.printf("duration %s", o.Elapsed.Round(time.Millisecond))
	lg.close()
	o.LogPath, o.LogErr = lg.result()
	j.done <- o
	close(j.done)
}

// watch waits for one process to end, escalating a pause: Ctrl+Break, a
// second Ctrl+Break after PauseGrace, then the Job kill after BreakGrace.
// Every line and every step is logged.
func (e *Engine) watch(ctx context.Context, j *Job, p Process, tr *tracker, lg *runLog) waited {
	started := e.d.Now()
	outDone := make(chan ranOut, 1)
	go func() {
		var o ranOut
		for ln := range p.Lines() {
			lg.output(ln)
			o.add(ln)
		}
		outDone <- o
	}()
	waitCh := make(chan waitResult, 1)
	go func() {
		exit, err := p.Wait()
		waitCh <- waitResult{exit, err}
	}()

	escCtx, stopEsc := context.WithCancel(context.WithoutCancel(ctx))
	defer stopEsc()
	var (
		w       waited
		wr      waitResult
		stage   int
		grace   <-chan struct{}
		cancel  = ctx.Done()
		pauseCh = (<-chan struct{})(j.pause)
		asked   time.Time
	)
	beginPause := func(why string) {
		cancel, pauseCh = nil, nil
		w.pausing = true
		stage = 1
		asked = e.d.Now()
		tr.emit(func(pr *Progress) { pr.Phase = PhasePausing })
		err := p.Interrupt()
		lg.printf("pause: %s; first Ctrl+Break sent (error %v); a second one in %s if still running", why, err, e.d.PauseGrace)
		grace = e.after(escCtx, e.d.PauseGrace)
	}
loop:
	for {
		select {
		case wr = <-waitCh:
			break loop
		case <-cancel:
			beginPause("the context ended")
		case <-pauseCh:
			beginPause("Pause was called")
		case <-grace:
			switch stage {
			case 1:
				stage = 2
				tr.emit(func(pr *Progress) { pr.Phase = PhaseStopping })
				err := p.Interrupt()
				lg.printf("pause: still running %s after the first Ctrl+Break; second Ctrl+Break sent (error %v); Job kill in %s if still running",
					e.d.Now().Sub(asked).Round(time.Millisecond), err, e.d.BreakGrace)
				grace = e.after(escCtx, e.d.BreakGrace)
			case 2:
				stage = 3
				w.killed = true
				grace = nil
				lg.printf("pause: still running %s after the first Ctrl+Break; ending hatchzip's Job (kill)", e.d.Now().Sub(asked).Round(time.Millisecond))
				p.Kill()
			}
		}
	}
	w.out = <-outDone
	w.exit = wr.exit
	lg.printf("process ended: exit %d after %s (wait error %v, killed by Devpit %v, pause asked %v)",
		wr.exit, e.d.Now().Sub(started).Round(time.Millisecond), wr.err, w.killed, w.pausing)
	if wr.err != nil && !w.killed {
		w.exit = -1
	}
	w.out.exit = w.exit
	return w
}

type waitResult struct {
	exit int
	err  error
}

// mayAutoResume is the first half of the rule 85 gate, about how the run
// ended: not paused or killed by Devpit, exit 3, or exit 1 with hatchzip's
// "not enough disk space" line, not a solid RAR, a hatchzip that still has
// bug #79, and the journal beside the archive. resumeVerdict is the second
// half. why says which condition decided, for the run log.
func (e *Engine) mayAutoResume(w waited, r Request) (ok bool, why string) {
	stderr := w.out.stderrText()
	switch {
	case w.killed:
		return false, "Devpit ended it"
	case w.pausing:
		return false, "Devpit asked it to pause"
	case w.exit != 1 && w.exit != 3:
		return false, "exit " + strconv.Itoa(w.exit) + " is not 1 or 3"
	case classify(w.exit, false, r.Mode, !r.Password.Empty(), stderr) == KindSolid:
		return false, "a solid RAR is refused in place"
	case w.exit == 1 && !isDiskFullMessage(stderr):
		return false, "exit 1 without hatchzip's \"" + diskFullMessage + "\" error"
	case !bug79Applies(e.tool.Version, bug79FixedIn):
		return false, "hatchzip " + e.tool.Version + " has the fix"
	case !e.journalExists(r.Set.First):
		return false, "no journal beside the archive"
	}
	return true, ""
}

// resumeVerdict is the second half of the rule 85 gate, about the job:
// fewer than maxAutoResumes so far, and the run that just ended (between
// before and after) extracted something or made the archive smaller. A run
// that got nowhere would get nowhere again: the disk really is full.
func resumeVerdict(resumes int, before, after attemptMark) (ok bool, why string) {
	did := "extracted " + shortBytes(after.extracted-before.extracted)
	if shrank := before.archive - after.archive; after.archive > 0 && shrank > 0 {
		did += ", archive smaller by " + shortBytes(shrank)
	}
	switch {
	case !after.progressed(before):
		return false, "the previous attempt made no progress (" + did + ", archive no smaller)"
	case resumes >= maxAutoResumes:
		return false, "the cap of " + strconv.Itoa(maxAutoResumes) + " automatic resumes is reached (previous attempt " + did + ")"
	}
	return true, "attempt " + strconv.Itoa(resumes+1) + " of " + strconv.Itoa(maxAutoResumes) +
		", previous attempt " + did + "; resuming in " + autoResumeDelay.String()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (e *Engine) journalExists(first string) bool {
	_, err := e.d.FS.Lstat(archive.JournalPath(first))
	return err == nil
}

// wait sleeps d on the engine's clock; false when ctx ended or the job was
// asked to pause meanwhile.
func (e *Engine) wait(ctx context.Context, j *Job, d time.Duration) bool {
	sleepCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-j.pause:
			cancel()
		case <-sleepCtx.Done():
		}
	}()
	e.d.Sleep(sleepCtx, d)
	return sleepCtx.Err() == nil
}

// after closes the returned channel once d has passed on the engine's clock.
func (e *Engine) after(ctx context.Context, d time.Duration) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		e.d.Sleep(ctx, d)
		if ctx.Err() == nil {
			close(ch)
		}
	}()
	return ch
}
