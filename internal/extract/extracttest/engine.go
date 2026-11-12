package extracttest

import (
	"fmt"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// ToolPath is the hatchzip path the fake engine runs.
const ToolPath = `C:\Users\test\scoop\shims\hatchzip.exe`

// ToolVersion is the hatchzip version the fake engine reports.
const ToolVersion = "0.1.10"

// FreeBytes is what the fake Deps.Free reports for every path.
const FreeBytes uint64 = 31_500_000_000

// Records is an in-memory extract.RecordStore.
type Records struct {
	mu    sync.Mutex
	recs  []extract.Record
	saves int
	err   error
}

// Load implements extract.RecordStore.
func (r *Records) Load() ([]extract.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]extract.Record(nil), r.recs...), nil
}

// Save implements extract.RecordStore. It fails with the error set by Fail.
func (r *Records) Save(recs []extract.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.saves++
	r.recs = append([]extract.Record(nil), recs...)
	return nil
}

// Fail makes every later Save return err (nil heals it).
func (r *Records) Fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

// All is the stored records.
func (r *Records) All() []extract.Record {
	recs, _ := r.Load()
	return recs
}

// Saves counts successful saves.
func (r *Records) Saves() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saves
}

// Awake counts KeepAwake holds.
type Awake struct {
	mu       sync.Mutex
	held     int
	released int
}

// KeepAwake is the fake winapi.KeepAwake.
func (a *Awake) KeepAwake() (func(), error) {
	a.mu.Lock()
	a.held++
	a.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			a.released++
			a.mu.Unlock()
		})
	}, nil
}

// Held is how many holds are taken and not released.
func (a *Awake) Held() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.held - a.released
}

// Total is how many holds were ever taken.
func (a *Awake) Total() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.held
}

// Fakes is every fake behind one engine.
type Fakes struct {
	Runner  *FakeRunner
	FS      *FakeFS
	Clock   *Clock
	Records *Records
	Awake   *Awake
}

// NewDeps wires extract.Deps over fresh fakes, with the default timings.
func NewDeps(scripts ...Script) (extract.Deps, *Fakes) {
	f := &Fakes{
		FS:      NewFakeFS(),
		Clock:   NewClock(Epoch()),
		Records: &Records{},
		Awake:   &Awake{},
	}
	f.Runner = NewFakeRunner(scripts...).Bind(f.FS, f.Clock)
	d := extract.Deps{
		Runner:     f.Runner,
		FS:         f.FS,
		Now:        f.Clock.Now,
		Sleep:      f.Clock.Sleep,
		Free:       func(string) (winapi.DiskSpace, error) { return winapi.DiskSpace{FreeBytes: FreeBytes}, nil },
		KeepAwake:  f.Awake.KeepAwake,
		Records:    f.Records,
		Poll:       extract.DefaultPoll,
		Rescan:     extract.DefaultRescan,
		PauseGrace: extract.DefaultPauseGrace,
		BreakGrace: extract.DefaultBreakGrace,
	}
	return d, f
}

// Tool is the fake hatchzip.
func Tool() extract.Tool { return extract.Tool{Path: ToolPath, Version: ToolVersion} }

// Engine returns a real *extract.Engine over the fakes and a fake clock.
func Engine(t testing.TB, scripts ...Script) (*extract.Engine, *FakeRunner, *FakeFS, *Clock) {
	t.Helper()
	d, f := NewDeps(scripts...)
	return extract.New(Tool(), d), f.Runner, f.FS, f.Clock
}

// Wait advances the clock by step until the job ends, and returns its
// outcome. It fails the test after 10 s of real time.
func Wait(t testing.TB, c *Clock, j *extract.Job, step time.Duration) extract.Outcome {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case o := <-j.Done():
			return o
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("extracttest: the job did not end")
		}
		c.Advance(step)
		time.Sleep(2 * time.Millisecond)
	}
}

// Check is a scripted `hatchzip check`: stdout is the plan text.
func Check(exit int, stdout string, stderr ...string) Script {
	s := Script{Stdout: stdout, Exit: exit}
	for _, e := range stderr {
		s.Stderr += e + "\n"
	}
	return s
}

// File is one entry an Extract script writes.
type File struct {
	Name string // relative to the output folder, with / separators
	Size int64
}

// Extract is a scripted in-place `hatchzip extract` writing files into out,
// one per step: half of the part file, then all of it, then the rename to the
// final name. archive (when not empty) shrinks to zero by the end. It exits 0.
func Extract(out, archive string, archiveSize int64, step time.Duration, files ...File) Script {
	var s Script
	at := time.Duration(0)
	for i, f := range files {
		final := path.Join(clean(out), f.Name)
		part := final + ".hatchzip-part"
		size := f.Size
		at += step / 2
		s.Steps = append(s.Steps, Step{At: at, Do: func(fs *FakeFS) { fs.SetSize(part, size/2) }})
		at += step / 2
		left := archiveSize - archiveSize*int64(i+1)/int64(len(files))
		s.Steps = append(s.Steps, Step{At: at, Do: func(fs *FakeFS) {
			fs.SetSize(part, size)
			fs.Rename(part, final)
			if archive != "" {
				fs.SetSize(archive, left)
			}
		}})
	}
	if archive != "" {
		s.Steps = append(s.Steps, Step{At: at, Do: func(fs *FakeFS) { fs.Remove(archive) }})
	}
	return s
}

// Plan writes a check plan the way hatchzip 0.1.10 prints one that fits in
// place (sizes in its rounded base-1000 form, like "84.2 GB").
func Plan(name, archiveSize, format, total string, entries int, available, standard, required, after string) string {
	return fmt.Sprintf("%s  %s %s, %s uncompressed in %d entries\n"+
		"  available                    %s\n"+
		"  standard extraction needs    %s\n"+
		"  required                     %s (+2.0 GB reserved)\n"+
		"  available after              %s\n",
		name, archiveSize, format, total, entries, available, standard, required, after)
}

// SamplePlan is the plan of section 3.4's mock-up.
func SamplePlan() string {
	return Plan("Game-Backup.zip", "84.2 GB", "zip", "96.0 GB", 12408, "31.5 GB", "96.0 GB", "13.8 GB", "15.7 GB")
}
