package extract

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/version"
)

// The run log (rule 91). Every Check, Start (with its automatic resume) and
// Restore writes one plain-text file in Devpit's cache folder, so a failed
// run can be explained afterwards: what was asked, what hatchzip was given
// and printed, what Devpit decided, and how it ended. Each line is written
// straight to the file (no buffer), so a crash leaves every line before it;
// the outcome is fsynced.
//
// The password never reaches it: the argv has none (rule 79), the
// environment is written as HATCHZIP_PASSWORD=<set> or <not set>, and every
// line has the password's text replaced, in case a tool ever echoed it.
// Failing to write the log never fails the run; the first error is kept for
// Outcome.LogErr. The log is never in the output folder (rule 78 stays: it
// writes only its own file and reads nothing).

// logTime is RFC 3339 with milliseconds.
const logTime = "2006-01-02T15:04:05.000Z07:00"

// logSampleEvery is how often a progress sample is logged.
const logSampleEvery = 10 * time.Second

// maxPollErrs bounds the distinct poller errors logged per run.
const maxPollErrs = 100

// errLogInOutput refuses a log folder inside the output folder.
var errLogInOutput = errors.New("extract log: the log folder is inside the output folder")

// logSink is an open log file.
type logSink interface {
	io.Writer
	Sync() error
	Close() error
}

// runLog is one run's log. A nil *runLog writes nothing.
type runLog struct {
	mu       sync.Mutex
	w        logSink // nil when it could not be opened, or after close
	path     string
	now      func() time.Time
	secret   string // replaced in every line
	err      error  // the first failure
	pollErrs map[string]bool
	sampled  time.Time
}

// openLog opens the log of one op ("check", "extract", "restore") for r, or
// returns nil when the engine has no log folder.
func (e *Engine) openLog(op string, r Request) *runLog {
	if e.d.LogDir == nil {
		return nil
	}
	lg := &runLog{now: e.d.Now, secret: r.Password.v, pollErrs: map[string]bool{}}
	dir, err := e.d.LogDir()
	switch {
	case err != nil:
	case !isAbsPath(dir) && !strings.HasPrefix(dir, "/"):
		err = fmt.Errorf("extract log: the log folder %q is not absolute", dir)
	case r.OutDir != "" && within(dir, r.OutDir):
		err = errLogInOutput
	}
	if err == nil {
		var f logSink
		f, lg.path, err = openLogFile(dir, logBase(e.d.Now(), r.Set.First))
		if err == nil {
			lg.w = f
		}
	}
	if err != nil {
		lg.fail(fmt.Errorf("extract log: %w", err))
		return lg
	}
	e.logHeader(lg, op, r)
	if rerr := rotateLogs(dir, logKeep); rerr != nil {
		lg.printf("log rotation: %v", rerr)
	}
	return lg
}

// logBase is extract-<YYYYMMDD-HHMMSS>-<stem>, the stem reduced to letters,
// digits, dot, dash and underscore, at most 60 of them.
func logBase(now time.Time, first string) string {
	stem := ""
	if first != "" {
		stem = archive.Stem(baseName(norm(first)))
	}
	var b strings.Builder
	for _, c := range stem {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 60 {
			break
		}
	}
	s := strings.Trim(b.String(), ".")
	if s == "" {
		s = "archive"
	}
	return "extract-" + now.Format("20060102-150405") + "-" + s
}

func (l *runLog) fail(err error) {
	if l.err == nil {
		l.err = err
	}
}

// printf writes one timestamped line.
func (l *runLog) printf(format string, args ...any) {
	if l == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writeLocked(msg)
}

func (l *runLog) writeLocked(msg string) {
	if l.w == nil {
		return
	}
	if l.secret != "" {
		msg = strings.ReplaceAll(msg, l.secret, "(hidden)")
	}
	msg = strings.NewReplacer("\r", `\r`, "\n", `\n`).Replace(msg)
	if _, err := io.WriteString(l.w, l.now().Format(logTime)+" "+msg+"\n"); err != nil {
		l.fail(fmt.Errorf("extract log: %w", err))
		_ = l.w.Close()
		l.w = nil
	}
}

// output logs one hatchzip line verbatim.
func (l *runLog) output(ln Line) {
	if ln.Stderr {
		l.printf("err|%s", ln.Text)
		return
	}
	l.printf("out|%s", ln.Text)
}

// spec logs the full argv and whether a password goes in the environment.
func (l *runLog) spec(s Spec) {
	if l == nil {
		return
	}
	parts := []string{quoteArg(s.Exe)}
	for _, a := range s.Args {
		parts = append(parts, quoteArg(a))
	}
	l.printf("argv: %s", strings.Join(parts, " "))
	pw := "<not set>"
	if !s.Password.Empty() {
		pw = "<set>"
	}
	l.printf("env: %s=%s", passwordEnv, pw)
}

func quoteArg(a string) string {
	if a == "" || strings.ContainsAny(a, " \t\"") {
		return `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
	}
	return a
}

// plan logs the parsed plan.
func (l *runLog) plan(p Plan) {
	if l == nil {
		return
	}
	l.printf("plan: name=%q moreParts=%d format=%s archive=%s total=%s entries=%d resuming=%v left=%s",
		p.Name, p.MoreParts, p.Format, logBytes(p.ArchiveBytes), logBytes(p.TotalBytes), p.Entries, p.Resuming, logBytes(p.LeftBytes))
	l.printf("plan: available=%s standardNeeds=%s required=%s reserved=%s availableAfter=%s shortBy=%s allDone=%v keepArchiveFits=%v",
		logBytes(p.Available), logBytes(p.StandardNeeds), logBytes(p.Required), logBytes(p.Reserved),
		logBytes(p.AvailableAfter), logBytes(p.ShortBy), p.AllDone, p.KeepArchiveFits)
	for _, n := range p.Notes {
		l.printf("plan note: %s", n)
	}
	for _, u := range p.Unknown {
		l.printf("plan unknown line: %s", u)
	}
}

// progress logs one progress sample.
func (l *runLog) progress(what string, pr Progress) {
	if l == nil {
		return
	}
	pct := ""
	if pr.TotalBytes > 0 {
		pct = fmt.Sprintf(" (%.1f%%)", float64(pr.DoneBytes)*100/float64(pr.TotalBytes))
	}
	l.printf("progress %s: phase=%s done=%s of %s%s items=%d/%d speed=%s/s eta=%s archive=%s free=%s current=%q elapsed=%s",
		what, phaseName(pr.Phase), logBytes(pr.DoneBytes), logBytes(pr.TotalBytes), pct, pr.DoneItems, pr.TotalItems,
		shortBytes(int64(pr.Speed)), pr.ETA.Round(time.Second), logBytes(pr.ArchiveBytes), logBytes(int64(min(pr.FreeBytes, 1<<62))),
		pr.Current, pr.Elapsed.Round(time.Millisecond))
}

// sample logs pr when the first sample or logSampleEvery since the last.
func (l *runLog) sample(now time.Time, pr Progress) {
	if l == nil {
		return
	}
	l.mu.Lock()
	first := l.sampled.IsZero()
	due := first || now.Sub(l.sampled) >= logSampleEvery
	if due {
		l.sampled = now
	}
	l.mu.Unlock()
	switch {
	case first:
		l.progress("first", pr)
	case due:
		l.progress("sample", pr)
	}
}

// pollErr logs a poller error once per distinct message, up to maxPollErrs.
func (l *runLog) pollErr(op string, err error) {
	if l == nil || err == nil {
		return
	}
	key := op + ": " + err.Error()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pollErrs[key] {
		return
	}
	switch n := len(l.pollErrs); {
	case n > maxPollErrs:
		return
	case n == maxPollErrs:
		l.pollErrs[key] = true
		l.writeLocked("poller: more errors, not logged")
		return
	}
	l.pollErrs[key] = true
	l.writeLocked("poller error: " + key)
}

// close fsyncs and closes the file. Safe to call twice.
func (l *runLog) close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w == nil {
		return
	}
	if err := l.w.Sync(); err != nil {
		l.fail(fmt.Errorf("extract log: %w", err))
	}
	if err := l.w.Close(); err != nil {
		l.fail(fmt.Errorf("extract log: %w", err))
	}
	l.w = nil
}

// result is the log's path ("" when it was never opened) and its first error.
func (l *runLog) result() (string, error) {
	if l == nil {
		return "", nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.path, l.err
}

// logHeader writes who ran what, on which request, with how much space.
func (e *Engine) logHeader(lg *runLog, op string, r Request) {
	if lg == nil {
		return
	}
	lg.printf("devpit %s (commit %s, built %s)", version.Version, version.Commit, version.Date)
	lg.printf("hatchzip %s version %s", e.tool.Path, e.tool.Version)
	lg.printf("os %s/%s, go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())
	lg.printf("op %s", op)
	lg.printf("archive %s (format %s, picked %s, %d part(s), %s at pick)", r.Set.First, r.Set.Format, r.Set.Picked, len(r.Set.Parts), logBytes(r.Set.Bytes))
	for i, p := range r.Set.Parts {
		size := "missing"
		if e.d.FS != nil {
			if fi, err := e.d.FS.Lstat(p); err == nil {
				size = logBytes(fi.Size())
			} else {
				size = "lstat: " + err.Error()
			}
		}
		lg.printf("part %d/%d %s %s", i+1, len(r.Set.Parts), p, size)
	}
	if r.OutDir != "" {
		lg.printf("out %s (same drive as the archive: %v)", r.OutDir, SameDrive(r.OutDir, r.Set.First))
	}
	lg.printf("mode %s", modeName(r.Mode))
	pw := "<not set>"
	if !r.Password.Empty() {
		pw = "<set>"
	}
	lg.printf("password %s=%s", passwordEnv, pw)
	e.logFree(lg, "archive drive", dirPath(r.Set.First))
	if r.OutDir != "" {
		e.logFree(lg, "out drive", r.OutDir)
	}
}

// logFree logs the free space of the volume holding p (its nearest existing
// folder).
func (e *Engine) logFree(lg *runLog, what, p string) {
	if e.d.Free == nil || p == "" {
		return
	}
	ds, at, err := e.freeAt(p)
	if err != nil {
		lg.printf("free %s %s: %v", what, p, err)
		return
	}
	lg.printf("free %s %s: %s free of %s", what, at, logBytes(int64(min(ds.FreeBytes, 1<<62))), logBytes(int64(min(ds.TotalBytes, 1<<62))))
}

// logBytes is "1.23 GB (1234567890)": hatchzip's base-1000 units plus the
// exact count.
func logBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB (%d)", float64(n)/float64(div), "KMGTP"[exp], n)
}

// shortBytes is logBytes without the exact count.
func shortBytes(n int64) string {
	s, _, _ := strings.Cut(logBytes(n), " (")
	return s
}

func modeName(m Mode) string {
	if m == ModeKeepArchive {
		return "keep archive"
	}
	return "in place"
}

func phaseName(p Phase) string {
	switch p {
	case PhaseStarting:
		return "starting"
	case PhaseWriting:
		return "writing"
	case PhaseFinishing:
		return "finishing"
	case PhasePausing:
		return "pausing"
	case PhaseStopping:
		return "stopping"
	case PhaseAutoResume:
		return "auto-resume"
	}
	return "phase(" + strconv.Itoa(int(p)) + ")"
}

func kindName(k Kind) string {
	names := [...]string{"done", "paused", "stopped", "no space", "damaged", "need password", "wrong password", "solid", "bad args", "failed"}
	if k >= 0 && int(k) < len(names) {
		return names[k]
	}
	return "kind(" + strconv.Itoa(int(k)) + ")"
}

func verdictName(v Verdict) string {
	names := [...]string{"fits", "no space", "damaged", "need password", "wrong password", "solid", "bad args", "failed"}
	if v >= 0 && int(v) < len(names) {
		return names[v]
	}
	return "verdict(" + strconv.Itoa(int(v)) + ")"
}
