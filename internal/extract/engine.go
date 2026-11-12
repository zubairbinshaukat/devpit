package extract

import (
	"context"
	"errors"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// errNoRunner is an engine built without a runner or a file system.
var errNoRunner = errors.New("extract: the engine has no runner or file system")

// Real wires the real dependencies: the Windows runner (other platforms
// refuse to start hatchzip), the Lstat-only file system, the real clock,
// free space, KeepAwake, and the record file and run logs in Devpit's cache
// folder.
func Real() Deps {
	return Deps{
		Runner:     realRunner{},
		FS:         osFS{},
		Now:        time.Now,
		Sleep:      sleepCtx,
		Free:       winapi.FreeSpace,
		KeepAwake:  winapi.KeepAwake,
		Records:    fileRecords{},
		LogDir:     defaultLogDir,
		Poll:       DefaultPoll,
		Rescan:     DefaultRescan,
		PauseGrace: DefaultPauseGrace,
		BreakGrace: DefaultBreakGrace,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// New builds an engine over tool and d. Zero timings get their defaults and
// a nil Now or Sleep the real clock.
func New(tool Tool, d Deps) *Engine {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Sleep == nil {
		d.Sleep = sleepCtx
	}
	if d.Poll <= 0 {
		d.Poll = DefaultPoll
	}
	if d.Rescan <= 0 {
		d.Rescan = DefaultRescan
	}
	if d.PauseGrace <= 0 {
		d.PauseGrace = DefaultPauseGrace
	}
	if d.BreakGrace <= 0 {
		d.BreakGrace = DefaultBreakGrace
	}
	return &Engine{tool: tool, d: d}
}

func (e *Engine) ready() error {
	if e.tool.Path == "" {
		return ErrToolMissing
	}
	if e.d.Runner == nil || e.d.FS == nil {
		return errNoRunner
	}
	return nil
}

// Check runs `hatchzip check` for r. Only exit 0 gives a ticket (rule 77):
// every other verdict leaves nothing that could start an extract. The run is
// logged (rule 91); the result says where.
func (e *Engine) Check(ctx context.Context, r Request) (CheckResult, error) {
	if err := e.ready(); err != nil {
		return CheckResult{}, err
	}
	lg := e.openLog("check", r)
	start := e.d.Now()
	res, err := e.check(ctx, r, lg)
	if err != nil {
		lg.printf("check ended without a result: %v", err)
	}
	lg.printf("duration %s", e.d.Now().Sub(start).Round(time.Millisecond))
	lg.close()
	res.LogPath, res.LogErr = lg.result()
	return res, err
}

func (e *Engine) check(ctx context.Context, r Request, lg *runLog) (CheckResult, error) {
	if err := checkRequest(r, true); err != nil {
		return CheckResult{}, err
	}
	// A check only reads; stopping it early is a plain kill.
	out, err := e.runOnce(ctx, Spec{Exe: e.tool.Path, Args: checkArgs(r), Password: r.Password}, nil, func(p Process) {
		lg.printf("cancelled: killing the check")
		p.Kill()
	}, lg)
	if err != nil {
		return CheckResult{}, err
	}
	res := CheckResult{
		Verdict: mapCheck(out.exit, out.stderrText(), !r.Password.Empty(), r.Mode),
		Exit:    out.exit,
		Plan:    ParsePlan(out.stdoutText()),
		Error:   out.errLine,
		Lines:   out.lines,
	}
	lg.plan(res.Plan)
	lg.printf("outcome: exit %d, verdict %s, error %q", res.Exit, verdictName(res.Verdict), res.Error)
	if res.Verdict == VerdictFits {
		e.mu.Lock()
		e.serial++
		res.Ticket = &Ticket{eng: e, req: r, plan: res.Plan, serial: e.serial}
		e.mu.Unlock()
	}
	return res, nil
}

// take marks t used, or refuses it.
func (e *Engine) take(t *Ticket) error {
	if t == nil {
		return ErrNoTicket
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if t.eng != e || t.used || t.serial == 0 {
		return ErrNoTicket
	}
	t.used = true
	return nil
}

// Start refuses a nil, foreign or used ticket (ErrNoTicket). It records the
// run (6.9), holds KeepAwake, starts extract, and reports progress until the
// end. Cancelling ctx pauses (6.3): it never kills first. The whole job,
// with its automatic resume, is one log (rule 91): [Job.LogPath].
func (e *Engine) Start(ctx context.Context, t *Ticket, onProgress func(Progress)) (*Job, error) {
	if err := e.take(t); err != nil {
		return nil, err
	}
	if err := e.ready(); err != nil {
		return nil, err
	}
	r := t.req
	lg := e.openLog("extract", r)
	fail := func(what string, err error) (*Job, error) {
		lg.printf("not started: %s: %v", what, err)
		lg.close()
		return nil, err
	}
	lg.plan(t.plan)
	if err := checkRequest(r, true); err != nil {
		return fail("request", err)
	}
	if err := e.saveRecord(r); err != nil {
		return fail("record", err)
	}
	release := func() {}
	if e.d.KeepAwake != nil {
		rel, err := e.d.KeepAwake()
		switch {
		case err != nil:
			lg.printf("keep awake: %v", err)
		case rel != nil:
			release = rel
		}
	}
	tr := e.newTracker(r, t.plan, onProgress)
	tr.setLog(lg)
	spec := Spec{Exe: e.tool.Path, Args: extractArgs(r), Password: r.Password}
	lg.spec(spec)
	p, err := e.d.Runner.Start(spec)
	if err != nil {
		release()
		_ = e.removeRecord(r.Set.First)
		return fail("hatchzip did not start", err)
	}
	lg.printf("started")
	j := &Job{pause: make(chan struct{}), done: make(chan Outcome, 1)}
	j.logPath, _ = lg.result()
	go e.run(ctx, j, r, p, tr, release, lg)
	return j, nil
}

// Restore runs `hatchzip restore` for r, passing each output line to onLine.
// Cancelling ctx sends a Ctrl+Break (restore resumes cleanly when run again).
func (e *Engine) Restore(ctx context.Context, r Request, onLine func(string)) (Outcome, error) {
	if err := e.ready(); err != nil {
		return Outcome{}, err
	}
	lg := e.openLog("restore", r)
	start := e.d.Now()
	o, err := e.restore(ctx, r, onLine, lg, start)
	if err != nil {
		lg.printf("restore ended without a result: %v", err)
	}
	lg.printf("duration %s", e.d.Now().Sub(start).Round(time.Millisecond))
	lg.close()
	o.LogPath, o.LogErr = lg.result()
	return o, err
}

func (e *Engine) restore(ctx context.Context, r Request, onLine func(string), lg *runLog, start time.Time) (Outcome, error) {
	if err := checkRequest(r, false); err != nil {
		return Outcome{}, err
	}
	out, err := e.runOnce(ctx, Spec{Exe: e.tool.Path, Args: restoreArgs(r), Password: r.Password}, onLine, func(p Process) {
		ierr := p.Interrupt()
		lg.printf("cancelled: Ctrl+Break sent to restore (error %v)", ierr)
	}, lg)
	if err != nil {
		return Outcome{}, err
	}
	o := Outcome{
		Kind:    classify(out.exit, false, ModeKeepArchive, !r.Password.Empty(), out.stderrText()),
		Exit:    out.exit,
		Error:   out.errLine,
		Lines:   out.lines,
		Elapsed: e.d.Now().Sub(start),
	}
	o.CanResume = e.journalExists(r.Set.First)
	if o.Kind == KindDone && !o.CanResume {
		_ = e.removeRecord(r.Set.First)
	}
	lg.printf("outcome: exit %d, kind %s, journal there %v, error %q", o.Exit, kindName(o.Kind), o.CanResume, o.Error)
	return o, nil
}

// runOnce runs spec to the end, collecting its output. When ctx ends, stop is
// called once on the process.
func (e *Engine) runOnce(ctx context.Context, spec Spec, onLine func(string), stop func(Process), lg *runLog) (ranOut, error) {
	lg.spec(spec)
	started := e.d.Now()
	p, err := e.d.Runner.Start(spec)
	if err != nil {
		lg.printf("hatchzip did not start: %v", err)
		return ranOut{}, err
	}
	unhook := context.AfterFunc(ctx, func() { stop(p) })
	defer unhook()
	var o ranOut
	for ln := range p.Lines() {
		lg.output(ln)
		o.add(ln)
		if onLine != nil {
			onLine(ln.Text)
		}
	}
	exit, werr := p.Wait()
	lg.printf("process ended: exit %d after %s (wait error %v)", exit, e.d.Now().Sub(started).Round(time.Millisecond), werr)
	if ctx.Err() != nil {
		return o, ctx.Err()
	}
	if werr != nil {
		exit = -1
	}
	o.exit = exit
	return o, nil
}

// freeAt is the free space of the volume holding p, read at p or its nearest
// folder above that exists (the output folder may not exist yet).
func (e *Engine) freeAt(p string) (winapi.DiskSpace, string, error) {
	var first error
	for q := p; q != ""; q = parentPath(q) {
		ds, err := e.d.Free(q)
		if err == nil {
			return ds, q, nil
		}
		if first == nil {
			first = err
		}
	}
	return winapi.DiskSpace{}, p, first
}
