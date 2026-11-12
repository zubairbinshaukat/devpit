//go:build windows

package extract_test

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/extract/extracttest"
	"github.com/zubairbinshaukat/devpit/internal/protect"
	"github.com/zubairbinshaukat/devpit/internal/tools"
)

// Integration tests against the real hatchzip (section 10.2, decision 13).
// The smoke test runs whenever hatchzip is on PATH; the long ones also need
// DEVPIT_IT_HATCHZIP=1. Archives are written here with Go's archive/zip in
// t.TempDir() (test-only: the product never writes an archive).

func realTool(t *testing.T) extract.Tool {
	t.Helper()
	p, err := exec.LookPath("hatchzip")
	if err != nil {
		t.Skip("hatchzip is not on PATH")
	}
	p, _ = filepath.Abs(p)
	out, err := exec.Command(p, "--version").Output() // #nosec G204 -- the hatchzip found on PATH, test only
	if err != nil {
		t.Skip("hatchzip --version failed:", err)
	}
	m := regexp.MustCompile(`(\d+\.\d+\.\d+)`).FindString(string(out))
	tool, err := extract.UseTool(tools.Tool{Found: true, Path: p, Version: m})
	if err != nil {
		t.Skip("hatchzip not usable:", err)
	}
	return tool
}

func optIn(t *testing.T) {
	t.Helper()
	if os.Getenv("DEVPIT_IT_HATCHZIP") != "1" {
		t.Skip("set DEVPIT_IT_HATCHZIP=1 to run the long hatchzip tests")
	}
}

// realEngine is the real runner and file system with in-memory records and
// the run logs in a temporary folder (DEVPIT_IT_KEEP_LOGS=<dir> keeps them
// there instead, to read after the run).
func realEngine(t *testing.T, tool extract.Tool, mut func(*extract.Deps)) (*extract.Engine, *extracttest.Records) {
	t.Helper()
	d := extract.Real()
	recs := &extracttest.Records{}
	d.Records = recs
	logDir := filepath.Join(t.TempDir(), "logs")
	if keep := os.Getenv("DEVPIT_IT_KEEP_LOGS"); keep != "" {
		logDir = keep
	}
	d.LogDir = func() (string, error) { return logDir, nil }
	d.Poll = 100 * time.Millisecond
	d.Rescan = 300 * time.Millisecond
	if mut != nil {
		mut(&d)
	}
	return extract.New(tool, d), recs
}

// makeZip writes a stored zip of n files of size bytes (random content, in
// 4 folders) and returns its path and each entry's SHA256.
func makeZip(t *testing.T, dir, name string, n, size int) (string, map[string]string) {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p) // #nosec G304 -- test archive in t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	r := rand.New(rand.NewPCG(uint64(n), uint64(size)))
	buf := make([]byte, size)
	hashes := map[string]string{}
	for i := range n {
		for j := range buf {
			buf[j] = byte(r.Uint32())
		}
		entry := fmt.Sprintf("d%d/file%05d.bin", i%4, i)
		w, err := zw.CreateHeader(&zip.FileHeader{Name: entry, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(buf); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(buf)
		hashes[filepath.FromSlash(entry)] = hex.EncodeToString(sum[:])
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p, hashes
}

func checkTree(t *testing.T, out string, hashes map[string]string) {
	t.Helper()
	for rel, want := range hashes {
		b, err := os.ReadFile(filepath.Join(out, rel)) // #nosec G304 -- the test's own output
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatalf("%s: SHA256 differs", rel)
		}
	}
}

func identify(t *testing.T, p string) extract.Request {
	t.Helper()
	set, err := extract.Identify(p, extract.Real().FS, protect.Default(os.LookupEnv), os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	return extract.Request{Set: set, OutDir: extract.DefaultOutDir(set, extract.Real().FS)}
}

func mustCheck(t *testing.T, eng *extract.Engine, r extract.Request) extract.CheckResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := eng.Check(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func waitReal(t *testing.T, j *extract.Job, limit time.Duration) extract.Outcome {
	t.Helper()
	select {
	case o := <-j.Done():
		return o
	case <-time.After(limit):
		j.Pause()
		t.Fatalf("the run did not end within %v", limit)
	}
	return extract.Outcome{}
}

// TestRealSmokeCheckAndExtract runs whenever hatchzip is on PATH.
func TestRealSmokeCheckAndExtract(t *testing.T) {
	tool := realTool(t)
	dir := t.TempDir()
	p, hashes := makeZip(t, dir, "smoke.zip", 20, 50_000)
	eng, recs := realEngine(t, tool, nil)
	r := identify(t, p)
	r.Mode = extract.ModeKeepArchive
	res := mustCheck(t, eng, r)
	if res.Verdict != extract.VerdictFits || res.Plan.Entries < 20 || res.Plan.TotalBytes == 0 ||
		res.Plan.Available == 0 || res.Plan.Required == 0 || len(res.Plan.Unknown) != 0 {
		t.Fatalf("check = %+v", res)
	}
	var mu sync.Mutex
	var reports []extract.Progress
	j, err := eng.Start(context.Background(), res.Ticket, func(pr extract.Progress) { mu.Lock(); reports = append(reports, pr); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	o := waitReal(t, j, time.Minute)
	if o.Kind != extract.KindDone || o.Exit != 0 {
		t.Fatalf("outcome = %+v", o)
	}
	checkTree(t, r.OutDir, hashes)
	if _, err := os.Stat(p); err != nil {
		t.Fatal("keep mode removed the archive")
	}
	if len(recs.All()) != 0 {
		t.Fatalf("record left: %+v", recs.All())
	}
	for _, p := range []string{res.LogPath, o.LogPath} {
		b, err := os.ReadFile(p) // #nosec G304 -- the test's own log
		if err != nil {
			t.Fatal(err)
		}
		log := string(b)
		for _, want := range []string{"hatchzip " + tool.Path, "argv: ", "out|", "process ended: exit 0", "outcome: exit 0", "duration "} {
			if !strings.Contains(log, want) {
				t.Errorf("%s has no %q:\n%s", p, want, log)
			}
		}
	}
}

// Safety rule 78 (integration): an in-place run with the poller at 100 ms
// finishes, every file matches, and the bar behaves.
func TestRealExtractWithThePollerFinishes(t *testing.T) {
	tool := realTool(t)
	optIn(t)
	dir := t.TempDir()
	p, hashes := makeZip(t, dir, "poller.zip", 3000, 170_000) // ~510 MB
	eng, _ := realEngine(t, tool, nil)
	r := identify(t, p)
	res := mustCheck(t, eng, r)
	if res.Ticket == nil {
		t.Fatalf("check = %+v", res)
	}
	var mu sync.Mutex
	var reports []extract.Progress
	j, err := eng.Start(context.Background(), res.Ticket, func(pr extract.Progress) { mu.Lock(); reports = append(reports, pr); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	o := waitReal(t, j, 5*time.Minute)
	if o.Kind != extract.KindDone {
		t.Fatalf("outcome = %+v", o)
	}
	checkTree(t, r.OutDir, hashes)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("the archive is still there after an in-place run")
	}
	mu.Lock()
	defer mu.Unlock()
	var prev int64
	for _, pr := range reports {
		if pr.DoneBytes < prev {
			t.Fatalf("progress went backwards: %d after %d", pr.DoneBytes, prev)
		}
		prev = pr.DoneBytes
	}
	if last := reports[len(reports)-1]; last.DoneBytes != last.TotalBytes || last.TotalBytes == 0 {
		t.Fatalf("final = %+v", last)
	}
	t.Logf("%d reports, auto-resumed %v", len(reports), o.AutoResumed)
}

// waitFraction waits until the run reports at least frac of its total.
func waitFraction(t *testing.T, ch <-chan extract.Progress, frac float64) {
	t.Helper()
	deadline := time.After(2 * time.Minute)
	for {
		select {
		case pr := <-ch:
			if pr.TotalBytes > 0 && float64(pr.DoneBytes) >= frac*float64(pr.TotalBytes) {
				return
			}
		case <-deadline:
			t.Fatal("the run never got that far")
		}
	}
}

func progressChan() (chan extract.Progress, func(extract.Progress)) {
	ch := make(chan extract.Progress, 1)
	return ch, func(pr extract.Progress) {
		select {
		case ch <- pr:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- pr:
			default:
			}
		}
	}
}

// Safety rule 80 (integration): Pause gives exit 130 quickly, the journal and
// the record stay, Pending lists it, a new check says resuming, and a new run
// finishes with every file intact.
func TestRealPauseExits130AndResumes(t *testing.T) {
	tool := realTool(t)
	optIn(t)
	dir := t.TempDir()
	p, hashes := makeZip(t, dir, "pause.zip", 3000, 170_000)
	eng, recs := realEngine(t, tool, nil)
	r := identify(t, p)
	res := mustCheck(t, eng, r)
	ch, onProgress := progressChan()
	j, err := eng.Start(context.Background(), res.Ticket, onProgress)
	if err != nil {
		t.Fatal(err)
	}
	waitFraction(t, ch, 0.3)
	asked := time.Now()
	j.Pause()
	o := waitReal(t, j, extract.DefaultPauseGrace+extract.DefaultBreakGrace+2*time.Second)
	if o.Kind == extract.KindDone {
		t.Skip("the run finished before the pause landed")
	}
	if o.Kind != extract.KindPaused || o.Exit != 130 || !o.CanResume {
		t.Fatalf("outcome = %+v", o)
	}
	t.Logf("paused %v after asking", time.Since(asked))
	if _, jerr := os.Lstat(archive.JournalPath(r.Set.First)); jerr != nil {
		t.Fatal("no journal after the pause")
	}
	if pend := eng.Pending(context.Background()); len(pend) != 1 || len(recs.All()) != 1 {
		t.Fatalf("pending = %+v", pend)
	}
	r2 := identify(t, p)
	if r2.OutDir != r.OutDir {
		t.Fatalf("resume folder %s, want %s", r2.OutDir, r.OutDir)
	}
	res2 := mustCheck(t, eng, r2)
	if res2.Ticket == nil || !res2.Plan.Resuming {
		t.Fatalf("resume check = %+v", res2)
	}
	j2, err := eng.Start(context.Background(), res2.Ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o := waitReal(t, j2, 5*time.Minute); o.Kind != extract.KindDone {
		t.Fatalf("resume outcome = %+v", o)
	}
	checkTree(t, r.OutDir, hashes)
}

// deafRunner ignores Ctrl+Break, so the escalation ends with the Job kill.
type deafRunner struct{ inner extract.Runner }

func (d deafRunner) Start(s extract.Spec) (extract.Process, error) {
	p, err := d.inner.Start(s)
	if err != nil {
		return nil, err
	}
	return deafProcess{p}, nil
}

type deafProcess struct{ extract.Process }

func (deafProcess) Interrupt() error { return nil }

// Safety rule 80/87 (integration): a hard kill through the Job is reported
// as stopped, and a resume finishes.
func TestRealHardKillThenResume(t *testing.T) {
	tool := realTool(t)
	optIn(t)
	dir := t.TempDir()
	p, hashes := makeZip(t, dir, "kill.zip", 3000, 170_000)
	eng, _ := realEngine(t, tool, func(d *extract.Deps) {
		d.Runner = deafRunner{inner: d.Runner}
		d.PauseGrace = 200 * time.Millisecond
		d.BreakGrace = 200 * time.Millisecond
	})
	r := identify(t, p)
	res := mustCheck(t, eng, r)
	ch, onProgress := progressChan()
	j, err := eng.Start(context.Background(), res.Ticket, onProgress)
	if err != nil {
		t.Fatal(err)
	}
	waitFraction(t, ch, 0.5)
	j.Pause()
	o := waitReal(t, j, 30*time.Second)
	if o.Kind == extract.KindDone {
		t.Skip("the run finished before the kill landed")
	}
	if o.Kind != extract.KindStopped || !o.CanResume {
		t.Fatalf("outcome = %+v", o)
	}
	eng2, _ := realEngine(t, tool, nil)
	r2 := identify(t, p)
	res2 := mustCheck(t, eng2, r2)
	if res2.Ticket == nil {
		t.Fatalf("resume check = %+v", res2)
	}
	j2, err := eng2.Start(context.Background(), res2.Ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o := waitReal(t, j2, 5*time.Minute); o.Kind != extract.KindDone {
		t.Fatalf("resume outcome = %+v", o)
	}
	checkTree(t, r.OutDir, hashes)
}

// argvRunner records every argv it starts.
type argvRunner struct {
	inner extract.Runner
	mu    sync.Mutex
	argv  []string
}

func (a *argvRunner) Start(s extract.Spec) (extract.Process, error) {
	a.mu.Lock()
	a.argv = append(a.argv, strings.Join(append([]string{s.Exe}, s.Args...), " "))
	a.mu.Unlock()
	return a.inner.Start(s)
}

// Safety rules 79 and 86 (integration): with stdin NUL and no password,
// hatchzip exits 5 at once instead of waiting; the right password through the
// environment works; no argv ever holds it.
func TestAPasswordArchiveWithoutAPasswordExitsFiveNotHangs(t *testing.T) {
	tool := realTool(t)
	optIn(t)
	const password = "fixture-pass" // testdata/README.md: not a secret
	const wrongPassword = "Wr0ng-Fixture-Pw"
	dir := t.TempDir()
	p := filepath.Join(dir, "encrypted.zip")
	src, err := os.Open(filepath.Join("testdata", "encrypted.zip"))
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.Create(p) // #nosec G304 -- test copy in t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(dst, src)
	_ = src.Close()
	_ = dst.Close()
	ar := &argvRunner{}
	eng, _ := realEngine(t, tool, func(d *extract.Deps) { ar.inner = d.Runner; d.Runner = ar })
	r := identify(t, p)
	r.Mode = extract.ModeKeepArchive

	started := time.Now()
	if res := mustCheck(t, eng, r); res.Verdict != extract.VerdictNeedPassword || res.Exit != 5 {
		t.Fatalf("no password: %+v", res)
	}
	if d := time.Since(started); d > 10*time.Second {
		t.Fatalf("exit 5 took %v", d)
	}
	r.Password = extract.NewSecret(wrongPassword)
	if res := mustCheck(t, eng, r); res.Verdict != extract.VerdictWrongPassword {
		t.Fatalf("wrong password: %+v", res)
	}
	r.Password = extract.NewSecret(password)
	res := mustCheck(t, eng, r)
	if res.Verdict != extract.VerdictFits {
		t.Fatalf("right password: %+v", res)
	}
	j, err := eng.Start(context.Background(), res.Ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o := waitReal(t, j, time.Minute); o.Kind != extract.KindDone {
		t.Fatalf("outcome = %+v", o)
	}
	ar.mu.Lock()
	defer ar.mu.Unlock()
	for _, a := range ar.argv {
		if strings.Contains(a, password) || strings.Contains(a, wrongPassword) {
			t.Fatalf("argv holds a password: %s", a)
		}
	}
	if os.Getenv("HATCHZIP_PASSWORD") != "" {
		t.Fatal("the test process's own environment holds a password")
	}
	// Safety rule 91 (integration): no run log holds either password.
	ents, err := os.ReadDir(filepath.Dir(res.LogPath))
	if err != nil || len(ents) < 4 {
		t.Fatalf("logs: %d, %v", len(ents), err)
	}
	for _, en := range ents {
		b, err := os.ReadFile(filepath.Join(filepath.Dir(res.LogPath), en.Name())) // #nosec G304 -- the test's own log
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), password) || strings.Contains(string(b), wrongPassword) {
			t.Fatalf("%s holds a password", en.Name())
		}
	}
}
