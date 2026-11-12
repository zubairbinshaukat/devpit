package extract_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/extract/extracttest"
)

// The run log (rule 91): one plain-text file per Check, Start and Restore in
// Devpit's log folder.

// logEnv is newEnv with its run logs in a temporary folder.
func logEnv(t *testing.T, scripts ...extracttest.Script) (*env, string) {
	t.Helper()
	e := newEnv(t, scripts...)
	dir := filepath.Join(t.TempDir(), "logs")
	e.deps.LogDir = func() (string, error) { return dir, nil }
	e.eng = extract.New(extracttest.Tool(), e.deps)
	return e, dir
}

func readLog(t *testing.T, p string) string {
	t.Helper()
	if p == "" {
		t.Fatal("no log path")
	}
	b, err := os.ReadFile(p) // #nosec G304 -- the test's own log in t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// allLogs is every file in dir, joined.
func allLogs(t *testing.T, dir string) (text string, names []string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, en := range ents {
		names = append(names, en.Name())
		b.WriteString(readLog(t, filepath.Join(dir, en.Name())))
	}
	return b.String(), names
}

func mustContain(t *testing.T, log string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(log, w) {
			t.Errorf("the log has no %q:\n%s", w, log)
		}
	}
}

var logLine = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}(Z|[+-]\d{2}:\d{2}) \S`)

// Safety rule 91: the password never reaches a log, not even when hatchzip
// prints it: check, extract with its automatic resume, and restore.
func TestTheRunLogNeverHoldsThePassword(t *testing.T) {
	const secret = "Fake-Secret-Value-91x"
	e, dir := logEnv(t,
		extracttest.Check(0, plan10k()+"echo "+secret+"\n", "warning: "+secret),
		extracttest.Script{Exit: 1, Stdout: "writing " + secret + "\n", Stderr: bug79Line + "\n", Steps: fullEarly(1, 1, true, true).Steps},
		extracttest.Script{Exit: 0, Stderr: "error: " + secret + " again\n"},
		extracttest.Script{Exit: 0, Stdout: "restored " + secret + "\n"})
	e.f.FS.AddFile(archive.JournalPath(zipPath), 10)
	r := request()
	r.Password = extract.NewSecret(secret)
	o := e.wait(e.start(context.Background(), r))
	if !o.AutoResumed || o.LogPath == "" || o.LogErr != nil {
		t.Fatalf("outcome = %+v", o)
	}
	ro, err := e.eng.Restore(context.Background(), r, nil)
	if err != nil || ro.LogPath == "" {
		t.Fatalf("restore = %+v, %v", ro, err)
	}
	text, names := allLogs(t, dir)
	if len(names) != 3 {
		t.Fatalf("logs %v, want check, extract and restore", names)
	}
	if strings.Contains(text, secret) || strings.Contains(strings.ToLower(text), strings.ToLower(secret)) {
		t.Fatal("a log holds the password")
	}
	mustContain(t, text, "HATCHZIP_PASSWORD=<set>", "out|echo (hidden)", "err|warning: (hidden)")
	if strings.Count(text, "env: HATCHZIP_PASSWORD=<set>") != 4 {
		t.Errorf("want the env line for each of the 4 runs:\n%s", text)
	}
}

// Safety rule 91: the log has the request, the argv, every output line, the
// plan, the exit code and the outcome, each line timestamped, and the outcome
// and Job agree on its path.
func TestTheRunLogHasTheArgvTheExitAndEveryLine(t *testing.T) {
	e, dir := logEnv(t,
		extracttest.Check(0, plan10k(), "warning: check stderr"),
		extracttest.Script{Exit: 0, Stdout: "line A\n  line B\n", Stderr: "err C\n"})
	res := e.check(request())
	if res.LogPath == "" || res.LogErr != nil {
		t.Fatalf("check = %+v", res)
	}
	clog := readLog(t, res.LogPath)
	mustContain(t, clog,
		"devpit ", "hatchzip "+extracttest.ToolPath+" version "+extracttest.ToolVersion, "os ", "op check",
		"archive "+zipPath, "part 1/1 "+zipPath+" 6.00 KB (6000)", "out "+outDir+" (same drive as the archive: true)",
		"mode in place", "password HATCHZIP_PASSWORD=<not set>", "free archive drive", "free out drive",
		"argv: "+extracttest.ToolPath+" check "+zipPath+" -o "+outDir, "env: HATCHZIP_PASSWORD=<not set>",
		"out|Game-Backup.zip  6.0 KB zip", "out|  available", "err|warning: check stderr",
		"plan: name=\"Game-Backup.zip\"", "entries=4", "process ended: exit 0", "outcome: exit 0, verdict fits", "duration ")
	j, err := e.eng.Start(context.Background(), res.Ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	o := e.wait(j)
	if o.LogPath == "" || o.LogPath != j.LogPath() || o.LogPath == res.LogPath || o.LogErr != nil {
		t.Fatalf("log paths: outcome %q job %q check %q (%v)", o.LogPath, j.LogPath(), res.LogPath, o.LogErr)
	}
	xlog := readLog(t, o.LogPath)
	mustContain(t, xlog, "op extract",
		"argv: "+extracttest.ToolPath+" extract "+zipPath+" -o "+outDir+" -y",
		"out|line A", "out|  line B", "err|err C", "process ended: exit 0",
		"auto-resume (bug #79): no, exit 0 is not 1 or 3",
		"progress last: phase=", "outcome: exit 0, kind done", "duration ")
	for _, ln := range strings.Split(strings.TrimSuffix(clog+xlog, "\n"), "\n") {
		if !logLine.MatchString(ln) {
			t.Errorf("not a timestamped line: %q", ln)
		}
	}
	for _, p := range []string{res.LogPath, o.LogPath} {
		if filepath.Dir(p) != dir || !regexp.MustCompile(`^extract-20260102-030405-Game-Backup(-\d+)?\.log$`).MatchString(filepath.Base(p)) {
			t.Errorf("log path %s", p)
		}
	}
}

// Safety rule 91: every step of a pause, and the bug #79 decision, is in the
// log with its timing.
func TestTheRunLogHasThePauseStepsAndTheAutoResume(t *testing.T) {
	e, _ := logEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Hang: true, IgnoreBreaks: 2})
	e.f.FS.AddFile(archive.JournalPath(zipPath), 10)
	j := e.start(context.Background(), request())
	j.Pause()
	o := e.wait(j)
	if o.Kind != extract.KindStopped {
		t.Fatalf("outcome = %+v", o)
	}
	mustContain(t, readLog(t, o.LogPath),
		"pause: Pause was called; first Ctrl+Break sent", "a second one in 5s",
		"after the first Ctrl+Break; second Ctrl+Break sent", "Job kill in 3s",
		"after the first Ctrl+Break; ending hatchzip's Job (kill)",
		"killed by Devpit true", "auto-resume (bug #79): no, Devpit ended it", "kind stopped")

	// Safety rule 85: each decision is logged with its reason and the
	// attempt number.
	e2, _ := logEnv(t, extracttest.Check(0, plan10k()),
		fullEarly(1, 1, true, true), fullEarly(2, 3, true, true), extracttest.Script{Exit: 1, Stderr: bug79Line + "\n"})
	e2.f.FS.AddFile(archive.JournalPath(zipPath), 10)
	o2 := e2.wait(e2.start(context.Background(), request()))
	log2 := readLog(t, o2.LogPath)
	mustContain(t, log2,
		"auto-resume (bug #79): yes, attempt 1 of 20, previous attempt extracted 200 B, archive smaller by 100 B; resuming in 2s",
		"auto-resume: started (attempt 1 of 20)",
		"auto-resume (bug #79): yes, attempt 2 of 20, previous attempt extracted 200 B, archive smaller by 100 B",
		"auto-resume: started (attempt 2 of 20)",
		"auto-resume (bug #79): no, the previous attempt made no progress (extracted 0 B, archive no smaller)",
		"auto-resumed true (2 of at most 20)")
	if strings.Count(log2, "argv: ") != 3 {
		t.Errorf("want all three extract argvs:\n%s", log2)
	}

	e3, _ := logEnv(t, extracttest.Check(0, plan10k()),
		extracttest.Script{Exit: 1, Stderr: "error: could not write a.bin: the file is in use\n"})
	e3.f.FS.AddFile(archive.JournalPath(zipPath), 10)
	o3 := e3.wait(e3.start(context.Background(), request()))
	mustContain(t, readLog(t, o3.LogPath),
		`auto-resume (bug #79): no, exit 1 without hatchzip's "not enough disk space" error`,
		"auto-resumed false (0 of at most 20)")
}

// Safety rule 91 (crash safety): lines are in the file while the run is
// still going, so a crash leaves them; a progress sample is written every
// 10 s.
func TestTheRunLogIsWrittenAsItGoes(t *testing.T) {
	e, _ := logEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Hang: true, Stdout: "hello 1\n"})
	j := e.start(context.Background(), request())
	deadline := time.Now().Add(5 * time.Second)
	var text string
	for time.Now().Before(deadline) {
		e.f.Clock.Advance(500 * time.Millisecond)
		time.Sleep(2 * time.Millisecond)
		b, _ := os.ReadFile(j.LogPath()) // #nosec G304 -- the test's own log
		text = string(b)
		if strings.Contains(text, "out|hello 1") && strings.Count(text, "progress ") >= 3 {
			break
		}
	}
	select {
	case <-j.Done():
		t.Fatal("the run ended by itself")
	default:
	}
	mustContain(t, text, "argv: ", "started", "out|hello 1", "progress first:", "progress sample:")
	if strings.Contains(text, "outcome:") {
		t.Fatal("the outcome is logged before the end")
	}
	j.Pause()
	o := e.wait(j)
	mustContain(t, readLog(t, o.LogPath), "progress last:", "outcome: exit 130, kind paused")
}

// Safety rule 91: the newest 30 logs are kept, and only files named like a
// run log in the log folder are ever counted or deleted.
func TestRunLogsAreRotatedKeepingThirty(t *testing.T) {
	e, dir := logEnv(t, extracttest.Check(0, plan10k()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var old []string
	for i := range 35 {
		name := fmt.Sprintf("extract-20250101-0000%02d-old.log", i)
		old = append(old, name)
		writeFile(t, filepath.Join(dir, name))
	}
	others := []string{"notes.txt", "extract-keep.log", "other-20250101-000000-x.log", "Extract-20200101-000000-x.log", "extract-20200101-000000-x.log.bak"}
	for _, n := range others {
		writeFile(t, filepath.Join(dir, n))
	}
	sub := filepath.Join(dir, "extract-20200101-000000-dir.log")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	beside := filepath.Join(filepath.Dir(dir), "extract-20200101-000000-beside.log")
	writeFile(t, beside)

	res := e.check(request())
	names := dirNames(t, dir)
	var logs []string
	for _, n := range names {
		if regexp.MustCompile(`^extract-\d{8}-\d{6}-[A-Za-z0-9._-]+\.log$`).MatchString(n) && n != filepath.Base(sub) {
			logs = append(logs, n)
		}
	}
	if len(logs) != 30 {
		t.Fatalf("%d logs kept, want 30: %v", len(logs), logs)
	}
	if !slices.Contains(logs, filepath.Base(res.LogPath)) {
		t.Fatal("the new log was rotated away")
	}
	for _, n := range old[:6] {
		if slices.Contains(names, n) {
			t.Errorf("old log %s kept", n)
		}
	}
	for _, n := range append(others, filepath.Base(sub)) {
		if !slices.Contains(names, n) {
			t.Errorf("%s was deleted", n)
		}
	}
	if _, err := os.Stat(beside); err != nil {
		t.Error("a file outside the log folder was deleted")
	}
}

// Safety rule 91: a log that cannot be written never fails or changes a run;
// the outcome says why there is no log. A log folder inside the output
// folder is refused before anything is created.
func TestALogFailureNeverFailsTheRun(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	writeFile(t, blocker)
	for name, dirFn := range map[string]func() (string, error){
		"a file in the way":     func() (string, error) { return filepath.Join(blocker, "logs"), nil },
		"no cache folder":       func() (string, error) { return "", errors.New("no cache") },
		"inside the out folder": func() (string, error) { return outDir + `\logs`, nil },
		"relative":              func() (string, error) { return "logs", nil },
	} {
		e := newEnv(t, extracttest.Check(0, plan10k()), extracttest.Script{Exit: 0})
		e.deps.LogDir = dirFn
		e.eng = extract.New(extracttest.Tool(), e.deps)
		res := e.check(request())
		if res.Ticket == nil || res.LogPath != "" || res.LogErr == nil {
			t.Fatalf("%s: check = %+v", name, res)
		}
		j, err := e.eng.Start(context.Background(), res.Ticket, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		o := e.wait(j)
		if o.Kind != extract.KindDone || o.LogPath != "" || o.LogErr == nil || j.LogPath() != "" {
			t.Fatalf("%s: outcome = %+v", name, o)
		}
	}
	if _, err := os.Stat("logs"); err == nil {
		t.Fatal("a relative log folder was created")
	}
	// No log folder at all (extracttest's default): no log, no error.
	e := newEnv(t, extracttest.Check(0, plan10k()))
	if res := e.check(request()); res.LogPath != "" || res.LogErr != nil {
		t.Fatalf("check = %+v", res)
	}
}

func writeFile(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, en := range ents {
		names = append(names, en.Name())
	}
	return names
}
