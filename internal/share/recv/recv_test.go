package recv_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/zubairbinshaukat/devpit/internal/share/job"
	"github.com/zubairbinshaukat/devpit/internal/share/netstat"
	"github.com/zubairbinshaukat/devpit/internal/share/recv"
	"github.com/zubairbinshaukat/devpit/internal/share/robocopy"
)

// utf16le encodes a log the way /UNILOG writes it.
func utf16le(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// memLog is a growing log.
type memLog struct {
	mu sync.Mutex
	b  []byte
}

func (m *memLog) append(b []byte) {
	m.mu.Lock()
	m.b = append(m.b, b...)
	m.mu.Unlock()
}

func (m *memLog) ReadFrom(off int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if off >= int64(len(m.b)) {
		return nil, nil
	}
	return append([]byte(nil), m.b[off:]...), nil
}

// step is one robocopy run in a script: what it writes, and how it ends.
type step struct {
	text string
	code int
	hold chan struct{} // if set, the run waits for it (or cancellation)
}

// scriptRunner plays robocopy from a script, one step per run.
type scriptRunner struct {
	mu    sync.Mutex
	logs  map[string]*memLog
	steps []step
	runs  []string
	touch bool // also create the log file on disk, as robocopy does
}

func newScriptRunner(steps ...step) *scriptRunner {
	return &scriptRunner{logs: map[string]*memLog{}, steps: steps}
}

func (s *scriptRunner) log(path string) *memLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logs[path] == nil {
		s.logs[path] = &memLog{}
	}
	return s.logs[path]
}

func (s *scriptRunner) Run(ctx context.Context, args []string) (int, error) {
	var path string
	for _, a := range args {
		if p, ok := strings.CutPrefix(a, "/UNILOG:"); ok {
			path = p
		}
	}
	s.mu.Lock()
	s.runs = append(s.runs, strings.Join(args, " "))
	if s.touch && path != "" {
		_ = os.WriteFile(path, []byte{0xFF, 0xFE}, 0o600) // the file robocopy would create
	}
	var st step
	if len(s.steps) > 0 {
		st, s.steps = s.steps[0], s.steps[1:]
	}
	s.mu.Unlock()
	lg := s.log(path)
	// Write in two pieces so a run is visible while it is going.
	raw := utf16le(st.text)
	mid := len(raw) / 2 &^ 1
	lg.append(raw[:mid])
	time.Sleep(15 * time.Millisecond)
	lg.append(raw[mid:])
	if st.hold != nil {
		select {
		case <-st.hold:
		case <-ctx.Done():
			return -1, ctx.Err()
		}
	}
	return st.code, nil
}

// Log builds the log source for a path the way Deps.Log wants.
func (s *scriptRunner) Log(path string) robocopy.LogSource { return s.log(path) }

func files(names ...string) string {
	var b strings.Builder
	for _, n := range names {
		b.WriteString("\t\t 1000\t\\\\10.0.0.9\\Games\\" + n + "\n")
	}
	return b.String()
}

const summaryOK = "----\n\n               Total    Copied   Skipped  Mismatch    FAILED    Extras\n" +
	"    Dirs :         1         1         0         0         0         0\n" +
	"   Files :         3         3         0         0         0         0\n" +
	"   Bytes :      3000      3000         0         0         0         0\n"

// rig is a receiving session over fakes.
type rig struct {
	s      *recv.Session
	runner *scriptRunner
	dir    string
	sleeps []time.Duration
	mu     sync.Mutex

	reachable atomic.Bool
	dials     atomic.Int32
	conn      *fakeConn
	awake     atomic.Int32
	released  atomic.Int32
	net       *fakeNet
	free      uint64
	fsName    string
}

type fakeNet struct {
	mu   sync.Mutex
	out  map[string]string
	exit map[string]int
}

func (f *fakeNet) Run(_ context.Context, name string, args ...string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := name + " " + strings.Join(args, " ")
	return f.out[k], f.exit[k], nil
}

type fakeConn struct {
	mu           sync.Mutex
	connected    []string
	disconnected []string
	err          error
}

func (c *fakeConn) Connect(remote, user, pw string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = append(c.connected, remote+"|"+user+"|"+pw)
	return c.err
}

func (c *fakeConn) Disconnect(remote string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnected = append(c.disconnected, remote)
	return nil
}

func newRig(t *testing.T, steps ...step) *rig {
	t.Helper()
	r := &rig{
		runner: newScriptRunner(steps...), dir: t.TempDir(), conn: &fakeConn{}, net: &fakeNet{out: map[string]string{}, exit: map[string]int{}},
		free: 1 << 40, fsName: "NTFS",
	}
	r.reachable.Store(true)
	var received atomic.Uint64
	deps := recv.Deps{
		Net:  r.net,
		Conn: r.conn,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			r.dials.Add(1)
			if !r.reachable.Load() {
				return nil, errors.New("unreachable")
			}
			a, b := net.Pipe()
			_ = b.Close()
			return a, nil
		},
		Robo:       robocopy.Tool{Runner: r.runner, Poll: 2 * time.Millisecond},
		Log:        r.runner.Log,
		FreeSpace:  func(string) (uint64, error) { return r.free, nil },
		FileSystem: func(string) (string, error) { return r.fsName, nil },
		Counters: func(string) (netstat.Counters, error) {
			return func() (uint64, uint64, error) { return received.Add(50), 0, nil }, nil
		},
		KeepAwake: func() (func(), error) { r.awake.Add(1); return func() { r.released.Add(1) }, nil },
		Sleep: func(_ context.Context, d time.Duration) {
			r.mu.Lock()
			r.sleeps = append(r.sleeps, d)
			r.mu.Unlock()
			// The other PC "comes back" after the first wait.
			r.reachable.Store(true)
		},
		Tick:     3 * time.Millisecond,
		StateDir: r.dir,
		LogDir:   filepath.Join(r.dir, "logs"),
	}
	r.s = recv.New(deps)
	return r
}

func startJob(r *rig, bytes, files int64) job.Job {
	return r.s.Start("10.0.0.9", "Games", "bob", `D:\Games`, robocopy.Plan{Files: files, Bytes: bytes})
}

func TestRunCopiesAndReportsProgressSpeedAndTheEnd(t *testing.T) {
	r := newRig(t, step{text: files("a", "b", "c") + summaryOK, code: 1})
	j := startJob(r, 3000, 3)
	var events []recv.Event
	out, err := r.s.Run(context.Background(), j, func(e recv.Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if !out.Done || out.Paused || len(out.Failed) != 0 || out.DoneBytes != 3000 || out.DoneFiles != 3 {
		t.Errorf("outcome = %+v", out)
	}
	if len(events) < 2 {
		t.Fatalf("%d events, want several", len(events))
	}
	var sawSpeed, sawRecent bool
	var last recv.Event
	for _, e := range events {
		if e.TotalBytes != 3000 || e.TotalFiles != 3 {
			t.Fatalf("event totals = %d / %d", e.TotalBytes, e.TotalFiles)
		}
		if e.DoneBytes < last.DoneBytes {
			t.Fatalf("progress went backwards: %d after %d", e.DoneBytes, last.DoneBytes)
		}
		if e.DoneBytes >= e.TotalBytes && e.Phase == recv.PhaseCopying && e.DoneBytes > 3000 {
			t.Fatalf("progress passed the total: %d", e.DoneBytes)
		}
		sawSpeed = sawSpeed || e.Speed > 0
		sawRecent = sawRecent || len(e.Recent) > 0
		last = e
	}
	if !sawSpeed {
		t.Error("no event carried a speed from the adapter counters")
	}
	if !sawRecent {
		t.Error("no event listed the finished files")
	}
	saved, ok, err := r.s.LoadJob()
	if err != nil || !ok || saved.State != job.StateDone {
		t.Errorf("saved job = %+v %v %v", saved, ok, err)
	}
	if r.awake.Load() != 1 || r.released.Load() != 1 {
		t.Errorf("keep-awake: %d starts, %d releases", r.awake.Load(), r.released.Load())
	}
	if !strings.Contains(r.runner.runs[0], `\\10.0.0.9\Games D:\Games`) || !strings.Contains(r.runner.runs[0], "/MT:16") {
		t.Errorf("robocopy ran as %q", r.runner.runs[0])
	}
}

func TestProgressNeverShowsTheTotalBeforeRobocopyDoes(t *testing.T) {
	hold := make(chan struct{})
	r := newRig(t, step{text: files("a"), code: 1, hold: hold})
	j := startJob(r, 1_000_000, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var maxDone int64
	go func() {
		time.Sleep(80 * time.Millisecond)
		close(hold)
	}()
	_, _ = r.s.Run(ctx, j, func(e recv.Event) {
		mu.Lock()
		if e.DoneBytes > maxDone {
			maxDone = e.DoneBytes
		}
		mu.Unlock()
	})
	mu.Lock()
	defer mu.Unlock()
	if maxDone >= 1_000_000 {
		t.Errorf("progress reached %d of 1000000 from the adapter estimate alone", maxDone)
	}
}

func TestANetworkDropWaitsForTheOtherPCAndResumesAcrossRuns(t *testing.T) {
	errLine := "2026/09/29 10:15:02 ERROR 64 (0x00000040) Copying File D:\\Games\\c\n"
	r := newRig(t,
		step{text: files("a", "b") + errLine, code: 9},
		step{text: files("c") + summaryOK, code: 1},
	)
	r.reachable.Store(false) // the drop
	j := startJob(r, 3000, 3)
	var phases []recv.Phase
	var notes []string
	out, err := r.s.Run(context.Background(), j, func(e recv.Event) {
		if len(phases) == 0 || phases[len(phases)-1] != e.Phase {
			phases = append(phases, e.Phase)
		}
		if e.Phase == recv.PhaseWaiting {
			notes = append(notes, e.Note)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Done || out.DoneBytes != 3000 || out.DoneFiles != 3 {
		t.Fatalf("outcome = %+v, want everything done across both runs", out)
	}
	sawWaiting := false
	for _, p := range phases {
		sawWaiting = sawWaiting || p == recv.PhaseWaiting
	}
	if !sawWaiting {
		t.Errorf("phases = %v, want a waiting phase while the PC was gone", phases)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "stopped answering") {
		t.Errorf("notes = %v", notes)
	}
	if len(r.runner.runs) != 2 {
		t.Errorf("robocopy ran %d times, want 2", len(r.runner.runs))
	}
	if len(r.sleeps) == 0 || r.sleeps[0] != 2*time.Second {
		t.Errorf("sleeps = %v, want the first backoff to be 2s", r.sleeps)
	}
}

func TestARetryingFileIsShownAsRetrying(t *testing.T) {
	hold := make(chan struct{})
	r := newRig(t, step{text: files("a") + "2026/09/29 10:15:02 ERROR 121 (0x00000079) Copying File D:\\Games\\b\n", code: 1, hold: hold})
	j := startJob(r, 5000, 2)
	var saw atomic.Bool
	// robocopy keeps running until the notice is seen, so a loaded machine
	// cannot end the run before the log was read; the timer only stops a
	// broken build from hanging.
	var once sync.Once
	release := func() { once.Do(func() { close(hold) }) }
	stop := time.AfterFunc(5*time.Second, release)
	defer stop.Stop()
	_, _ = r.s.Run(context.Background(), j, func(e recv.Event) {
		if e.Phase == recv.PhaseRetrying && strings.Contains(e.Note, "again") {
			saw.Store(true)
			release()
		}
	})
	if !saw.Load() {
		t.Error("no event said the network hiccuped and a file was being tried again")
	}
}

func TestRepeatedFailuresWithNothingNewStopWithTheFailedFiles(t *testing.T) {
	bad := "2026/09/29 10:15:02 ERROR 5 (0x00000005) Copying File D:\\Games\\secret.bin\n" +
		"2026/09/29 10:15:03 ERROR 112 (0x00000070) Copying File D:\\Games\\big.bin\n"
	r := newRig(t, step{text: bad, code: 9}, step{text: bad, code: 9}, step{text: bad, code: 9}, step{text: bad, code: 9})
	j := startJob(r, 3000, 3)
	out, err := r.s.Run(context.Background(), j, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Done || out.Paused || len(out.Failed) != 2 {
		t.Fatalf("outcome = %+v", out)
	}
	if len(r.runner.runs) != 3 {
		t.Errorf("robocopy ran %d times, want 3 tries with no progress", len(r.runner.runs))
	}
	got := map[string]recv.FailedFile{}
	for _, f := range out.Failed {
		got[filepath.Base(strings.ReplaceAll(f.Path, `\`, "/"))] = f
	}
	if got["big.bin"].Code != 112 || got["big.bin"].Reason != "This disk is full" ||
		got["secret.bin"].Code != 5 || got["secret.bin"].Reason != "Access denied" {
		t.Errorf("failed = %+v", got)
	}
	saved, _, _ := r.s.LoadJob()
	if saved.State != job.StateFailed || len(saved.Failed) != 2 {
		t.Errorf("saved = %+v", saved)
	}
}

// secondRunOfARecordedLockedCopy is what robocopy writes on a later run of
// the recorded copy with one locked file (robocopy's testdata): the finished
// files are skipped, so only the extra that lives in the destination and the
// locked file with its retry are left. The paths are moved onto this rig's
// share and destination.
func secondRunOfARecordedLockedCopy(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "robocopy", "testdata", "recorded_en_copy_locked.unilog"))
	if err != nil {
		t.Fatal(err)
	}
	var keep []string
	for l := range strings.SplitSeq(robocopy.DecodeLog(b), "\n") {
		if strings.Contains(l, `\src\a.bin`) || strings.Contains(l, `\src\sub\`) || strings.Contains(l, `\src\写真\`) {
			continue
		}
		keep = append(keep, l)
	}
	text := strings.Join(keep, "\n")
	text = strings.ReplaceAll(text, `C:\rec\src`, `\\10.0.0.9\Games`)
	return strings.ReplaceAll(text, `C:\rec\dst`, `D:\Games`)
}

// TestALockedFileStopsAfterThreeRunsInsteadOfLoopingForever pins a bug found
// with a real robocopy log: robocopy writes a failed file's line, and its
// retry's line, and lists a destination-only file each run. All three were
// counted as finished files, so a run where nothing copied still looked like
// progress, the "stalled" counter never grew, and a copy with one locked
// file ran robocopy again and again for as long as Devpit stayed open.
func TestALockedFileStopsAfterThreeRunsInsteadOfLoopingForever(t *testing.T) {
	log := secondRunOfARecordedLockedCopy(t)
	r := newRig(t, step{text: log, code: 11}, step{text: log, code: 11}, step{text: log, code: 11}, step{text: log, code: 11})
	out, err := r.s.Run(context.Background(), startJob(r, 1048616, 4), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Done || len(r.runner.runs) != 3 {
		t.Fatalf("outcome %+v after %d runs, want a stop after 3 runs with nothing new", out, len(r.runner.runs))
	}
	if len(out.Failed) != 1 || out.Failed[0].Code != 32 || out.Failed[0].Path != `\\10.0.0.9\Games\locked.txt` {
		t.Errorf("failed = %+v", out.Failed)
	}
	if out.DoneBytes != 0 || out.DoneFiles != 0 {
		t.Errorf("done = %d bytes %d files; nothing was copied", out.DoneBytes, out.DoneFiles)
	}
}

func TestAnErrorAfterProgressDoesNotCountAsStalled(t *testing.T) {
	bad := func(names ...string) string {
		return files(names...) + "2026/09/29 10:15:03 ERROR 32 (0x00000020) Copying File D:\\Games\\locked.bin\n"
	}
	r := newRig(t,
		step{text: bad("a"), code: 9}, step{text: bad("b"), code: 9}, step{text: bad("c"), code: 9},
		step{text: bad("d"), code: 9}, step{text: files("locked.bin") + summaryOK, code: 1})
	out, err := r.s.Run(context.Background(), startJob(r, 5000, 5), nil)
	if err != nil || !out.Done {
		t.Fatalf("outcome = %+v, err %v; want it to keep going while files keep finishing", out, err)
	}
}

func TestCancellingPausesAndTheSavedJobResumes(t *testing.T) {
	hold := make(chan struct{})
	r := newRig(t, step{text: files("a", "b"), code: 1, hold: hold})
	j := startJob(r, 3000, 3)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	out, err := r.s.Run(ctx, j, nil)
	if err != nil || !out.Paused || out.Done {
		t.Fatalf("outcome = %+v, err %v; want a paused copy", out, err)
	}
	saved, ok, _ := r.s.LoadJob()
	if !ok || saved.State != job.StatePaused || saved.DoneBytes != 2000 || !saved.Resumable() {
		t.Fatalf("saved = %+v", saved)
	}
	if saved.Remaining() != 1000 {
		t.Errorf("remaining = %d", saved.Remaining())
	}
	if r.released.Load() != 1 {
		t.Error("the PC was kept awake after the copy was stopped")
	}

	// Resume: a new session, the saved job, and only the rest is copied.
	r2 := newRig(t, step{text: files("c") + summaryOK, code: 1})
	r2.dir = r.dir
	out2, err := r2.s.Run(context.Background(), saved, nil)
	if err != nil || !out2.Done || out2.DoneBytes != 3000 {
		t.Errorf("resumed outcome = %+v, err %v", out2, err)
	}
}

func TestClosingDevpitMidCopyLeavesARunningRecordToResume(t *testing.T) {
	hold := make(chan struct{})
	r := newRig(t, step{text: files("a"), code: 1, hold: hold})
	j := startJob(r, 3000, 3)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = r.s.Run(ctx, j, nil)
		close(done)
	}()
	// Wait for the first save rather than for a fixed time: on a loaded
	// Windows machine the goroutine can take longer than any fixed pause to
	// get there, and a read that raced the save used to make it fail.
	var saved job.Job
	var ok bool
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		var err error
		if saved, ok, err = r.s.LoadJob(); err != nil {
			t.Fatalf("reading the record while copying: %v", err)
		}
		if ok {
			break
		}
	}
	if !ok || saved.State != job.StateRunning || !saved.Resumable() {
		t.Errorf("record while copying = %+v %v; a crash now must leave something to resume", saved, ok)
	}
	if saved.User != "bob" || strings.Contains(strings.ToLower(saved.LastError), "password") {
		t.Errorf("record = %+v", saved)
	}
	cancel()
	<-done
}

func TestASignInFailureNumberSurvivesSoItCanBeExplained(t *testing.T) {
	r := newRig(t)
	r.conn.err = syscall.Errno(1326)
	err := r.s.SignInShare("10.0.0.9", "Games", netstat.Credentials{User: "bob", Password: "pw"})
	var e syscall.Errno
	if !errors.As(err, &e) || e != 1326 {
		t.Errorf("err = %v", err)
	}
	if strings.Contains(err.Error(), "pw") {
		t.Errorf("the error text leaks the password: %v", err)
	}
}

func TestListRejectsABadAddressWithoutRunningAnything(t *testing.T) {
	r := newRig(t)
	if _, _, err := r.s.List(context.Background(), "192.168.1.5 & calc"); !errors.Is(err, netstat.ErrBadHost) {
		t.Errorf("err = %v", err)
	}
}

func TestListAndSignInHost(t *testing.T) {
	r := newRig(t)
	r.net.out[`net view \\10.0.0.9`] = "Shared resources\n\nShare name  Type  Used as  Comment\n\n" + strings.Repeat("-", 40) +
		"\nGames        Disk         hi\nThe command completed successfully.\n"
	host, shares, err := r.s.List(context.Background(), `\\10.0.0.9`)
	if err != nil || host != "10.0.0.9" || len(shares) != 1 || shares[0].Name != "Games" {
		t.Fatalf("List = %q %v %v", host, shares, err)
	}
	shares, err = r.s.SignInHost(context.Background(), "10.0.0.9", netstat.Credentials{User: "bob", Password: "pw"})
	if err != nil || len(shares) != 1 {
		t.Fatalf("SignInHost = %v %v", shares, err)
	}
	if got := r.conn.connected[0]; got != `\\10.0.0.9\IPC$|bob|pw` {
		t.Errorf("signed in as %q", got)
	}
	r.s.Disconnect("10.0.0.9", "Games")
	if len(r.conn.disconnected) != 2 {
		t.Errorf("disconnected = %v, want the share and IPC$", r.conn.disconnected)
	}
}

func TestListTurnsAnAccessDeniedIntoItsNumber(t *testing.T) {
	r := newRig(t)
	r.net.out[`net view \\10.0.0.9`] = "Fehler 5\n"
	r.net.exit[`net view \\10.0.0.9`] = 2
	_, _, err := r.s.List(context.Background(), "10.0.0.9")
	var e syscall.Errno
	if !errors.As(err, &e) || e != 5 {
		t.Errorf("err = %v", err)
	}
}

// dryLog is a dry run log for a source of the given size and files.
func dryLog(bytes, files int, extra string) string {
	return extra + "----\n\n               Total    Copied   Skipped  Mismatch    FAILED    Extras\n" +
		"    Dirs :         1         1         0         0         0         0\n" +
		"   Files :  " + strconv.Itoa(files) + "  " + strconv.Itoa(files) + "         0         0         0         0\n" +
		"   Bytes :  " + strconv.Itoa(bytes) + "  " + strconv.Itoa(bytes) + "         0         0         0         0\n"
}

func TestPreflightChecksSizeSpaceFileSystemAndPaths(t *testing.T) {
	tests := []struct {
		name    string
		log     string
		free    uint64
		fs      string
		ok      bool
		wantKey string
	}{
		{"fine", dryLog(3000, 3, files("a")), 1 << 30, "NTFS", true, ""},
		{"not enough space", dryLog(5000, 3, files("a")), 1000, "NTFS", false, "no-space"},
		{"exactly enough space", dryLog(1000, 1, files("a")), 4096, "NTFS", true, ""},
		{"bytes fit but the clusters do not", dryLog(1000, 1, files("a")), 1000, "NTFS", false, "no-space"},
		{"many small files", dryLog(100_000, 100, strings.Repeat(files("x"), 100)), 200_000, "NTFS", false, "no-space"},
		{"fat32 with a big file", dryLog(5<<30, 1, "\t\t 5368709120\t\\\\10.0.0.9\\Games\\big.iso\n"), 1 << 40, "FAT32", false, "fat32"},
		{"exfat with a big file", dryLog(5<<30, 1, "\t\t 5368709120\t\\\\10.0.0.9\\Games\\big.iso\n"), 1 << 40, "exFAT", true, ""},
		{"fat32 with small files", dryLog(3000, 3, files("a")), 1 << 40, "FAT32", true, ""},
		{"nothing to copy", dryLog(0, 0, ""), 1 << 30, "NTFS", false, "nothing"},
		{"long paths only warn", dryLog(1000, 1, "\t\t 1000\t\\\\10.0.0.9\\Games\\"+strings.Repeat("d", 270)+"\n"), 1 << 30, "NTFS", true, "long-paths"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, step{text: tt.log, code: 1})
			r.free, r.fsName = tt.free, tt.fs
			c, err := r.s.Preflight(context.Background(), "10.0.0.9", "Games", `D:\Games`)
			if err != nil {
				t.Fatal(err)
			}
			if c.OK() != tt.ok {
				t.Errorf("OK = %v, want %v; warnings %+v", c.OK(), tt.ok, c.Warnings)
			}
			found := ""
			for _, w := range c.Warnings {
				found = string(w.Kind)
				if w.Text == "" {
					t.Errorf("a warning has no text: %+v", w)
				}
			}
			if found != tt.wantKey {
				t.Errorf("warning = %q, want %q", found, tt.wantKey)
			}
		})
	}
}

// TestLogsDoNotPileUp pins a disk leak: every dry run and every robocopy run
// wrote a new log that listed each file with its full path, and none was ever
// deleted. Now each run deletes its own, and week-old logs of crashed runs go.
func TestLogsDoNotPileUp(t *testing.T) {
	r := newRig(t, step{text: dryLog(3000, 3, files("a")), code: 1}, step{text: files("a", "b", "c") + summaryOK, code: 1})
	r.runner.touch = true
	logs := filepath.Join(r.dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	stale, fresh := filepath.Join(logs, "robocopy-copy-old.log"), filepath.Join(logs, "robocopy-copy-other.log")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Preflight(context.Background(), "10.0.0.9", "Games", `D:\Games`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Run(context.Background(), startJob(r, 3000, 3), nil); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(logs)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != filepath.Base(fresh) {
		t.Errorf("logs left = %v, want only the recent one of another run", names)
	}
}

func TestPreflightExplainsARobocopyFailureByItsNumber(t *testing.T) {
	r := newRig(t, step{text: "2026/09/29 10:15:02 ERROR 1326 (0x0000052E) Accessing Source Directory \\\\10.0.0.9\\Games\\\n", code: 16})
	_, err := r.s.Preflight(context.Background(), "10.0.0.9", "Games", `D:\Games`)
	var e syscall.Errno
	if !errors.As(err, &e) || e != 1326 {
		t.Errorf("err = %v, want Errno 1326", err)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := map[int64]string{
		0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 1536: "1.5 KB", 1 << 20: "1.0 MB", 5 << 30: "5.0 GB",
		80 << 30: "80.0 GB", 150 << 30: "150 GB", 1 << 40: "1.0 TB",
	}
	for n, want := range tests {
		if got := recv.FormatBytes(n); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDropConnectionsClosesTheOldSignIns(t *testing.T) {
	r := newRig(t)
	r.net.out["net use"] = "OK   \\\\10.0.0.9\\IPC$   Microsoft Windows Network\nOK  Z:  \\\\10.0.0.9\\Games  Microsoft Windows Network\n"
	n, err := r.s.DropConnections(context.Background(), "10.0.0.9")
	if err != nil || n != 2 || len(r.conn.disconnected) != 2 {
		t.Errorf("closed %d %v, err %v", n, r.conn.disconnected, err)
	}
}
