// Package extract drives the external tool hatchzip to extract a big archive
// in place: it runs `hatchzip check`, `extract` and `restore` through a
// fakeable [Runner], parses the check's plan for display, polls progress with
// Lstat and folder names only, maps exit codes to outcomes, pauses with
// Ctrl+Break before it ever kills, resumes once by itself after hatchzip's
// false "disk full" (bug #79), and remembers unfinished extractions.
//
// Devpit never reads or writes an archive's bytes itself; everything that
// touches the archive is hatchzip. The package never imports Bubble Tea or
// any UI package; screens bridge it with channels and tea.Cmd.
//
// This file holds the exported types frozen at work package A0
// (plans/v0.5.4-extract.md, section 6.2). The functions live beside it:
// secret.go, tool.go, validate.go, find.go, argv.go, plan.go, outcome.go,
// poll.go, progress.go, job.go, engine.go, record.go, pending.go, runner*.go,
// fs.go, and log.go / logfile.go (the run log, rule 91).
package extract

import (
	"context"
	"errors"
	"io/fs"
	"sync"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// MinVersion is the oldest hatchzip Devpit drives; newer is fine.
const MinVersion = "0.1.10"

// OfferMinBytes is the smallest archive (summed over its parts, decimal
// units as hatchzip prints them) the picker lists and Share offers.
const OfferMinBytes = 1_000_000_000

// Mode is how hatchzip treats the archive.
type Mode int

const (
	// ModeInPlace removes the archive piece by piece as its files are written.
	ModeInPlace Mode = iota
	// ModeKeepArchive leaves the archive as it is (needs the full space).
	ModeKeepArchive
)

// Request is one extraction: which set, into which folder, how.
type Request struct {
	Set      archive.Set
	OutDir   string
	Mode     Mode
	Password Secret
}

// Tool is a usable hatchzip.
type Tool struct{ Path, Version string }

var (
	// ErrToolMissing is hatchzip not found, or found without an absolute path.
	ErrToolMissing = errors.New("hatchzip is not installed")
	// ErrToolTooOld is a hatchzip older than [MinVersion].
	ErrToolTooOld = errors.New("hatchzip is older than " + MinVersion)
)

var (
	// ErrNetworkPath is a UNC path or a mapped network drive.
	ErrNetworkPath = errors.New("network paths are not extracted")
	// ErrProtected is a system or account folder.
	ErrProtected = errors.New("archives in that place are not extracted")
	// ErrReparsePoint is an archive reached through a link or junction.
	ErrReparsePoint = errors.New("the archive is reached through a link")
	// ErrNotAFile is a path that is not a regular file.
	ErrNotAFile = errors.New("that is not a regular file")
)

// Found is one archive set FindArchives found.
type Found struct {
	Set        archive.Set
	Unfinished bool
	Mod        time.Time
}

// Plan is the check's text, for display only (section 6.4). Sizes are
// approximate (hatchzip rounds them); Entries is exact. While resuming,
// hatchzip's header counts the entries left, not the total (spike X3).
type Plan struct {
	Name            string
	MoreParts       int
	Format          string
	ArchiveBytes    int64 // approx
	TotalBytes      int64 // approx
	Entries         int   // exact
	Resuming        bool
	LeftBytes       int64 // approx, when Resuming
	Available       int64 // approx
	StandardNeeds   int64 // approx, in place only
	Required        int64 // approx
	Reserved        int64 // approx (keep-free)
	AvailableAfter  int64 // approx, when it fits
	ShortBy         int64 // approx, when it does not fit
	AllDone         bool
	KeepArchiveFits bool // hatchzip's "enough space to keep the archive" note
	Notes           []string
	Unknown         []string // lines the parser did not recognise
}

// Verdict is what a check's exit code means.
type Verdict int

const (
	VerdictFits          Verdict = iota // exit 0
	VerdictNoSpace                      // exit 3
	VerdictDamaged                      // exit 4
	VerdictNeedPassword                 // exit 5, no password given
	VerdictWrongPassword                // exit 5, a password was given
	VerdictSolid                        // exit 1 + solid message, in place (X11)
	VerdictBadArgs                      // exit 2
	VerdictFailed                       // anything else
)

// Ticket proves a check passed for exactly this request. Only Check makes
// one; Start consumes it once.
type Ticket struct {
	eng    *Engine
	req    Request
	plan   Plan
	serial uint64
	used   bool // guarded by eng.mu
}

// CheckResult is one `hatchzip check`.
type CheckResult struct {
	Verdict Verdict
	Exit    int
	Plan    Plan
	Error   string   // hatchzip's first "error:" line, trimmed, or ""
	Lines   []string // last 50 lines, for "l details"
	Ticket  *Ticket  // non-nil only when Verdict == VerdictFits
	LogPath string   // this check's run log (rule 91), or "" when none was written
	LogErr  error    // why the run log could not be written, if it could not; never fails the check
}

// Phase is what a running extraction is doing.
type Phase int

const (
	PhaseStarting Phase = iota
	PhaseWriting
	PhaseFinishing
	PhasePausing
	PhaseStopping
	PhaseAutoResume
)

// Progress is one poll of a running extraction.
type Progress struct {
	Phase        Phase
	DoneBytes    int64 // monotonic; <= 99% of TotalBytes until exit 0
	TotalBytes   int64 // approx, from the plan
	Speed        float64
	ETA          time.Duration // 0 while the speed is unknown, else at least 1 s until exit 0
	DoneItems    int           // <= Entries-1 until exit 0
	TotalItems   int
	Current      string // base name of the .hatchzip-part being written
	ArchiveBytes int64  // in place: Lstat sum of the parts now
	FreeBytes    uint64 // the output volume
	Elapsed      time.Duration
	AutoResumes  int // automatic resumes so far, counting the one starting now (rule 85)
}

// Kind is how an extraction ended.
type Kind int

const (
	KindDone          Kind = iota
	KindPaused             // exit 130 (our Ctrl+Break or the user's)
	KindStopped            // Devpit killed it after the grace periods; resumable
	KindNoSpace            // exit 3 from extract
	KindDamaged            // exit 4
	KindNeedPassword       // exit 5
	KindWrongPassword      // exit 5 with a password
	KindSolid              // solid RAR refused in place
	KindBadArgs            // exit 2
	KindFailed             // exit 1 (after any auto-resumes) or unknown
)

// Outcome is how a run ended.
type Outcome struct {
	Kind        Kind
	Exit        int
	Error       string
	Lines       []string
	Last        Progress
	AutoResumed bool // AutoResumes > 0
	AutoResumes int  // how many times Devpit resumed it by itself (rule 85)
	CanResume   bool // the journal exists now
	Elapsed     time.Duration
	LogPath     string // this run's log (rule 91), or "" when none was written
	LogErr      error  // why the run log could not be (fully) written; never fails the run
}

// ErrNoTicket is Start without a passed check for this request.
var ErrNoTicket = errors.New("extract: no passed check for this request")

// Spec is one hatchzip run (section 6.3).
type Spec struct {
	Exe      string   // Tool.Path, absolute
	Args     []string // from argv.go only
	Password Secret   // becomes HATCHZIP_PASSWORD in the child env, nothing else
}

// Line is one output line of a run, ANSI stripped.
type Line struct {
	Text   string
	Stderr bool
}

// Process is a started hatchzip.
type Process interface {
	Lines() <-chan Line // both pipes, ANSI stripped, closed at exit
	Interrupt() error   // Ctrl+Break to the child's group
	Kill()              // Job Object kill, whole tree
	Wait() (exit int, err error)
}

// Runner starts hatchzip.
type Runner interface {
	Start(spec Spec) (Process, error)
}

// StatFS is everything the engine may do to the file system while hatchzip
// runs. It has no Open on purpose (rule 78).
type StatFS interface {
	Lstat(name string) (fs.FileInfo, error)
	ReadDirNames(dir string) ([]string, error) // names only; DirEntry.Info is never used
}

// RecordStore keeps Devpit's own list of started extractions.
type RecordStore interface {
	Load() ([]Record, error)
	Save([]Record) error
}

// Record is one started extraction. It has no password field (rule 79).
type Record struct {
	First   string    `json:"first"`
	OutDir  string    `json:"outDir"`
	Mode    Mode      `json:"mode"`
	Started time.Time `json:"started"`
	Tool    string    `json:"tool"` // hatchzip version used
}

// Deps is everything the engine uses from outside; all of it is injectable.
type Deps struct {
	Runner     Runner
	FS         StatFS
	Now        func() time.Time
	Sleep      func(ctx context.Context, d time.Duration) // fake clock in tests
	Free       func(path string) (winapi.DiskSpace, error)
	KeepAwake  func() (release func(), err error)
	Records    RecordStore
	Poll       time.Duration // 1 s
	Rescan     time.Duration // 3 s: re-list folder names
	PauseGrace time.Duration // first Ctrl+Break -> second (5 s, X8)
	BreakGrace time.Duration // second Ctrl+Break -> Job kill (3 s, X8)
	// LogDir is the folder of the run logs (rule 91): <config.CacheDir>\logs
	// in Real. nil writes no log. A log is never written into the output
	// folder, and a failure to write it never fails a run.
	LogDir func() (string, error)
}

// Default timings. Spike X8 measured 21–441 ms from the first Ctrl+Break to
// exit 130, even inside a single 2.5 GB entry, so these are generous.
const (
	DefaultPoll       = time.Second
	DefaultRescan     = 3 * time.Second
	DefaultPauseGrace = 5 * time.Second
	DefaultBreakGrace = 3 * time.Second
)

// Engine runs hatchzip for Devpit.
type Engine struct {
	tool Tool
	d    Deps

	mu     sync.Mutex
	serial uint64
}

// Job is one running extraction.
type Job struct {
	pauseOnce sync.Once
	pause     chan struct{}
	done      chan Outcome
	logPath   string
}

// Unfinished is an extraction whose journal still exists.
type Unfinished struct {
	Set     archive.Set
	OutDir  string
	Mode    Mode
	Started time.Time
}
