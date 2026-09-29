package recv

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/errmap"
	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
)

// Phase is what a running copy is doing right now.
type Phase int

// The phases.
const (
	// PhaseCopying is robocopy moving files.
	PhaseCopying Phase = iota
	// PhaseRetrying is robocopy waiting to try a file again after an error.
	PhaseRetrying
	// PhaseWaiting is Devpit waiting for the other PC to answer again.
	PhaseWaiting
)

// Event is one update from a running copy.
type Event struct {
	// Phase is what the copy is doing.
	Phase Phase
	// DoneBytes and TotalBytes are the copy's overall progress, counted
	// across every run of it. DoneFiles and TotalFiles likewise.
	DoneBytes, TotalBytes int64
	DoneFiles, TotalFiles int64
	// Speed is bytes per second, measured on the network adapter.
	Speed float64
	// ETA is the time left at that speed, or zero when not known yet.
	ETA time.Duration
	// Recent are the latest finished files, newest last.
	Recent []string
	// Attempt counts how many times robocopy has been started.
	Attempt int
	// Elapsed is the time since this run began.
	Elapsed time.Duration
	// NextTry is how long until the next probe, in PhaseWaiting.
	NextTry time.Duration
	// Note is a plain-words line about what is happening, such as why the
	// copy is waiting.
	Note string
}

// FailedFile is a file that did not copy.
type FailedFile struct {
	// Path is the file.
	Path string
	// Code is the Win32 error number.
	Code int
	// Reason is the plain-words title for the code, or "error N".
	Reason string
}

// Outcome is how a run ended.
type Outcome struct {
	// Exit is robocopy's last exit code.
	Exit robocopy.Exit
	// Done is true when everything copied.
	Done bool
	// Paused is true when the run was stopped, and can be resumed.
	Paused bool
	// Failed lists the files that did not copy.
	Failed []FailedFile
	// DoneBytes and DoneFiles are the totals across all runs.
	DoneBytes, DoneFiles int64
	// Elapsed is how long this run took.
	Elapsed time.Duration
	// Message is one plain sentence about the end.
	Message string
}

// maxStalled is how many robocopy runs in a row may end with failures and no
// new finished file before Devpit stops trying and reports. Waiting for a PC
// that is gone is different and is not counted: that is retried for as long
// as the user leaves Devpit open.
const maxStalled = 3

// Start creates the copy's record from a check and returns it, ready for
// [Session.Run].
func (s *Session) Start(host, share, user, dest string, plan robocopy.Plan) job.Job {
	now := s.deps.Now()
	return job.Job{
		Host: host, Share: share, User: user, Dest: dest,
		TotalFiles: plan.Files, TotalBytes: plan.Bytes,
		State: job.StateRunning, Started: now, Updated: now,
	}
}

// Run copies until everything is there, the context is cancelled, or robocopy
// keeps failing with nothing new copied. The whole life of a copy is here:
//
//  1. run robocopy; read its log as it grows;
//  2. measure speed on the network adapter and send an event each tick;
//  3. when robocopy ends with failures, check whether the other PC still
//     answers on port 445; if not, wait with a growing pause until it does,
//     then run robocopy again, which skips what is finished;
//  4. save the record on every step, so closing Devpit at any moment leaves
//     something to resume.
//
// onEvent is called on Run's own goroutine only. Cancelling ctx stops
// robocopy and returns a paused outcome.
func (s *Session) Run(ctx context.Context, j job.Job, onEvent func(Event)) (Outcome, error) {
	release, _ := s.deps.KeepAwake()
	defer release()

	started := s.deps.Now()
	var meter *netstat.Meter
	if s.deps.Counters != nil {
		if c, err := s.deps.Counters(j.Host); err == nil {
			meter = netstat.NewMeter(c, s.deps.Now)
		}
	}
	save := func(state job.State, lastErr string, failed []string) {
		j.State, j.LastError, j.Failed, j.Updated = state, lastErr, failed, s.deps.Now()
		_ = job.Save(s.deps.StateDir, j)
	}
	save(job.StateRunning, "", nil)

	ev := Event{TotalBytes: j.TotalBytes, TotalFiles: j.TotalFiles, DoneBytes: j.DoneBytes, DoneFiles: j.DoneFiles}
	emit := func(e Event) {
		e.TotalBytes, e.TotalFiles = j.TotalBytes, j.TotalFiles
		e.Elapsed = s.deps.Now().Sub(started)
		ev = e
		if onEvent != nil {
			onEvent(e)
		}
	}

	stalled := 0
	for attempt := 1; ; attempt++ {
		res, prog, err := s.runOnce(ctx, j, attempt, meter, ev, emit)
		j.DoneBytes += prog.BytesDone
		j.DoneFiles += prog.FilesDone
		if ctx.Err() != nil {
			save(job.StatePaused, "Stopped. Press Resume to carry on.", nil)
			return Outcome{
				Paused: true, DoneBytes: j.DoneBytes, DoneFiles: j.DoneFiles,
				Elapsed: s.deps.Now().Sub(started), Message: "Stopped. Press Resume to carry on.",
			}, nil
		}
		if err != nil {
			save(job.StateFailed, err.Error(), nil)
			return Outcome{
				DoneBytes: j.DoneBytes, DoneFiles: j.DoneFiles, Elapsed: s.deps.Now().Sub(started),
				Message: err.Error(),
			}, fmt.Errorf("copying: %w", err)
		}
		if res.Exit.Success() {
			save(job.StateDone, "", nil)
			return Outcome{
				Exit: res.Exit, Done: true, DoneBytes: j.DoneBytes, DoneFiles: j.DoneFiles,
				Elapsed: s.deps.Now().Sub(started), Message: res.Exit.Describe(),
			}, nil
		}

		if prog.FilesDone == 0 && prog.BytesDone == 0 {
			stalled++
		} else {
			stalled = 0
		}
		failed := failedFiles(prog)
		reachable := netstat.Reachable(ctx, s.deps.Dial, j.Host, 3*time.Second)
		if !reachable {
			// The other PC is gone: wait for it, however long that takes,
			// and start again. Nothing is lost, robocopy skips finished
			// files.
			emit(Event{
				Phase: PhaseWaiting, DoneBytes: j.DoneBytes, DoneFiles: j.DoneFiles, Attempt: attempt,
				Note: "The other PC stopped answering.",
			})
			save(job.StatePaused, "The other PC stopped answering.", nil)
			werr := netstat.WaitReachable(ctx, s.deps.Dial, j.Host, s.deps.Sleep, func(d time.Duration) {
				emit(Event{
					Phase: PhaseWaiting, DoneBytes: j.DoneBytes, DoneFiles: j.DoneFiles, Attempt: attempt, NextTry: d,
					Note: "The other PC stopped answering.",
				})
			})
			if werr != nil {
				save(job.StatePaused, "Stopped. Press Resume to carry on.", nil)
				return Outcome{
					Paused: true, DoneBytes: j.DoneBytes, DoneFiles: j.DoneFiles,
					Elapsed: s.deps.Now().Sub(started), Message: "Stopped. Press Resume to carry on.",
				}, nil
			}
			stalled = 0
			continue
		}
		if stalled >= maxStalled {
			paths := make([]string, len(failed))
			for i, f := range failed {
				paths[i] = f.Path
			}
			msg := res.Exit.Describe()
			save(job.StateFailed, msg, paths)
			return Outcome{
				Exit: res.Exit, Failed: failed, DoneBytes: j.DoneBytes, DoneFiles: j.DoneFiles,
				Elapsed: s.deps.Now().Sub(started), Message: msg,
			}, nil
		}
	}
}

// runOnce runs robocopy one time and returns its result and what it finished.
// A goroutine runs robocopy; this one measures speed each tick and turns the
// latest log progress into events, so every callback runs on the caller's
// goroutine.
func (s *Session) runOnce(ctx context.Context, j job.Job, attempt int, meter *netstat.Meter, prev Event, emit func(Event)) (robocopy.Result, robocopy.Progress, error) {
	logPath, err := s.newLogPath("copy")
	if err != nil {
		return robocopy.Result{}, robocopy.Progress{}, err
	}
	src := netstat.UNC(j.Host, j.Share)
	log := s.deps.Log(logPath)
	// Robo.Copy has read the whole log by the time it returns, and every
	// return below waits for it.
	defer s.dropLog(logPath)

	progCh := make(chan robocopy.Progress, 1)
	type done struct {
		res robocopy.Result
		err error
	}
	doneCh := make(chan done, 1)
	go func() {
		res, err := s.deps.Robo.Copy(ctx, src, j.Dest, logPath, log, func(p robocopy.Progress) {
			select { // keep only the newest
			case <-progCh:
			default:
			}
			select {
			case progCh <- p:
			default:
			}
		})
		doneCh <- done{res, err}
	}()

	var cur robocopy.Progress
	if meter != nil {
		// Baseline for this run. Without Rebase, "received" kept counting
		// from the first run, so after a network drop the second run started
		// with the first run's bytes added on top of what was already saved,
		// and the bar jumped to nearly full.
		meter.Rebase()
		meter.Sample()
	}
	tick := time.NewTicker(s.deps.Tick)
	defer tick.Stop()
	speed := prev.Speed
	var received uint64
	build := func() Event {
		remaining := j.TotalBytes - j.DoneBytes
		doneNow := cur.BytesDone
		// The adapter's count includes a file that is still copying, which
		// the file lines cannot see. Use it while it is ahead, but never let
		// it reach the total before robocopy says so.
		if est := min(int64(received), remaining-1); est > doneNow { //nolint:gosec // received is far below MaxInt64
			doneNow = max(est, 0)
		}
		e := Event{
			Phase:     PhaseCopying,
			DoneBytes: j.DoneBytes + doneNow, DoneFiles: j.DoneFiles + cur.FilesDone,
			Speed: speed, Recent: cur.Recent, Attempt: attempt,
		}
		if cur.Retrying {
			e.Phase = PhaseRetrying
			e.Note = "The network hiccuped. Trying that file again."
		}
		e.ETA = netstat.ETA(j.TotalBytes-e.DoneBytes, speed)
		return e
	}
	emit(build())
	for {
		select {
		case p := <-progCh:
			cur = p
		case <-tick.C:
			if meter != nil {
				speed, received = meter.Sample()
			}
			emit(build())
		case d := <-doneCh:
			select {
			case p := <-progCh:
				cur = p
			default:
			}
			if d.res.Progress.FilesDone >= cur.FilesDone {
				cur = d.res.Progress
			}
			return d.res, cur, d.err
		}
	}
}

// failedFiles turns the tracker's failures into the list shown at the end,
// each with the plain-words reason for its number.
func failedFiles(p robocopy.Progress) []FailedFile {
	var out []FailedFile
	for _, path := range p.FailedPaths() {
		code := p.Failed[path]
		reason := fmt.Sprintf("error %d", code)
		if e, ok := errmap.Lookup(code, errmap.PhaseCopy); ok {
			reason = e.Title
		}
		out = append(out, FailedFile{Path: path, Code: code, Reason: reason})
	}
	return out
}

// CodeFromRunError finds the Win32 error number in a robocopy failure's last
// log lines, so the screen can explain it with [errmap].
func CodeFromRunError(err error) (int, bool) {
	var re *robocopy.RunError
	if !errors.As(err, &re) {
		return 0, false
	}
	for l := range strings.SplitSeq(re.Log, "\n") {
		if pl := robocopy.ParseLine(l); pl.Kind == robocopy.KindError {
			return pl.Code, true
		}
	}
	return 0, false
}

// ExplainPreflight rewrites a dry run failure so its error number can be
// found by errmap. Other errors are returned unchanged.
func ExplainPreflight(err error) error {
	if code, ok := CodeFromRunError(err); ok {
		return fmt.Errorf("checking the shared folder: %w", syscall.Errno(code))
	}
	return err
}
