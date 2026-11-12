package extract

import (
	"context"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	extractengine "github.com/zubairbinshaukat/devpit/internal/extract"
	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/filepicker"
	"github.com/zubairbinshaukat/devpit/internal/winapi"
)

// Extractor is the engine as the screen uses it. *extract.Engine is the real
// one; tests pass a real engine over the extracttest fakes, or a canned stub.
type Extractor interface {
	// Check runs `hatchzip check`. Only a passed check carries a ticket.
	Check(ctx context.Context, r extractengine.Request) (extractengine.CheckResult, error)
	// Start runs the extraction a ticket was given for. Cancelling ctx
	// pauses; it never kills first.
	Start(ctx context.Context, t *extractengine.Ticket, onProgress func(extractengine.Progress)) (*extractengine.Job, error)
	// Restore rebuilds the archive of an unfinished extraction.
	Restore(ctx context.Context, r extractengine.Request, onLine func(string)) (extractengine.Outcome, error)
	// Pending lists the unfinished extractions Devpit has a record of.
	Pending(ctx context.Context) []extractengine.Unfinished
	// Forget drops Devpit's own record of one. It touches nothing else.
	Forget(first string) error
}

// RunStepFunc runs one install command, streaming its output. The default is
// [tools.RunStep]; it has the shape of the Install screen's RunStepFunc.
type RunStepFunc = func(ctx context.Context, argv []string, timeout time.Duration, onLine func(string)) tools.StepResult

// Option configures the screen built by [New].
type Option func(*Model)

// WithArchive opens the screen on one archive instead of the picker: at its
// space check, or at Resume / Restore when it has an unfinished extraction.
// The path is validated like a typed one, and nothing starts without the
// check and the confirmation.
func WithArchive(path string) Option {
	return func(m *Model) { m.openPath = path }
}

// WithArchives limits the picker to these archives: no typed path, no file
// picker, no search. Share uses it after a receive that brought several.
func WithArchives(sets []archive.Set) Option {
	return func(m *Model) {
		m.onlySets = append([]archive.Set(nil), sets...)
		m.limited = true
	}
}

// WithExtractor replaces the engine, for tests and goldens. The same value
// is used with and without a detected hatchzip.
func WithExtractor(x Extractor) Option {
	return func(m *Model) {
		m.eng = x
		m.injected = x != nil
	}
}

// WithDetect replaces how hatchzip is looked for.
func WithDetect(f func(ctx context.Context) tools.Tool) Option {
	return func(m *Model) {
		if f != nil {
			m.detect = f
		}
	}
}

// WithScoop replaces how Scoop is looked for. It is only asked when hatchzip
// is missing or too old.
func WithScoop(f func(ctx context.Context) tools.Tool) Option {
	return func(m *Model) {
		if f != nil {
			m.scoop = f
		}
	}
}

// WithRunStep replaces how an install command is run.
func WithRunStep(f RunStepFunc) Option {
	return func(m *Model) {
		if f != nil {
			m.runStep = f
		}
	}
}

// WithClock replaces the screen's clock.
func WithClock(now func() time.Time) Option {
	return func(m *Model) {
		if now != nil {
			m.now = now
		}
	}
}

// WithIdentify replaces how a typed, browsed or handed-over path becomes an
// archive set. The default is extract.Identify over the real disk.
func WithIdentify(f filepicker.IdentifyFunc) Option {
	return func(m *Model) {
		if f != nil {
			m.identify = f
		}
	}
}

// WithFind replaces the search for big archives in the given folders.
func WithFind(f func(ctx context.Context, dirs []string) []extractengine.Found) Option {
	return func(m *Model) {
		if f != nil {
			m.find = f
		}
	}
}

// WithStatFS replaces the file system the screen itself looks at: the journal
// beside a picked archive and the default output folder.
func WithStatFS(fsys extractengine.StatFS) Option {
	return func(m *Model) {
		if fsys != nil {
			m.fsys = fsys
		}
	}
}

// WithOutDir replaces how the output folder of a fresh extraction is chosen.
// The default is extract.DefaultOutDir.
func WithOutDir(f func(set archive.Set) string) Option {
	return func(m *Model) {
		if f != nil {
			m.outDir = f
		}
	}
}

// WithOutDirIn replaces how the new folder inside a picked one is chosen, and
// whether Devpit extracts there at all. The default is extract.OutDirIn.
func WithOutDirIn(f func(parent string, set archive.Set) (string, error)) Option {
	return func(m *Model) {
		if f != nil {
			m.outDirIn = f
		}
	}
}

// WithFolderPicker replaces the disk the folder picker checks a typed or
// browsed folder against, and its Windows folder dialog.
func WithFolderPicker(stat func(string) (os.FileInfo, error), browse func(start string) (string, error)) Option {
	return func(m *Model) {
		m.folderStat, m.folderBrowse = stat, browse
	}
}

// WithFree replaces how free space is read for the done card.
func WithFree(f func(path string) (uint64, bool)) Option {
	return func(m *Model) {
		if f != nil {
			m.free = f
		}
	}
}

// WithBrowse replaces the Windows file picker and says it is available, so a
// frame is the same on every OS.
func WithBrowse(f func(start string) (string, error)) Option {
	return func(m *Model) {
		if f != nil {
			m.browse = f
		}
	}
}

// withTick replaces the timer behind the spinner, so no test schedules a real
// one.
func withTick(f func(time.Duration, func(time.Time) tea.Msg) tea.Cmd) Option {
	return func(m *Model) { m.tick = f }
}

// The real outside world.

// realFS is the disk, through the engine's Lstat-and-names-only interface.
func realFS() extractengine.StatFS { return extractengine.Real().FS }

// detectTool is the default hatchzip and Scoop detection.
func detectTool(name string) func(ctx context.Context) tools.Tool {
	return func(ctx context.Context) tools.Tool { return tools.New().Get(ctx, name) }
}

// realIdentify validates a path against the real disk and the protected
// folders: the built-in ones and every folder Accounts knows holds a sign-in.
func realIdentify(path string) (archive.Set, error) {
	return extractengine.Identify(path, realFS(), accounts.Protected(os.LookupEnv), os.Getenv)
}

// findTimeout bounds the search for archives, so a slow disk can only make
// the list shorter, never make the picker wait.
const findTimeout = 3 * time.Second

// The search's limits: the listed folders themselves and one level under
// them, and no more than this many directory entries.
const (
	findDepth      = 1
	findMaxEntries = 5000
)

// realFind looks for big archives in dirs.
func realFind(ctx context.Context, dirs []string) []extractengine.Found {
	ctx, cancel := context.WithTimeout(ctx, findTimeout)
	defer cancel()
	return extractengine.FindArchives(ctx, realFS(), dirs, extractengine.OfferMinBytes, findDepth, findMaxEntries)
}

// realFree reads the free space of the volume holding path.
func realFree(path string) (uint64, bool) {
	ds, err := winapi.FreeSpace(path)
	if err != nil {
		return 0, false
	}
	return ds.FreeBytes, true
}

// downloadsFolder is the user's Downloads folder, or "" when it is unknown.
func downloadsFolder() string {
	if dir, err := winapi.DownloadsFolder(); err == nil && dir != "" {
		return dir
	}
	home := os.Getenv("USERPROFILE")
	if home == "" {
		return ""
	}
	return filepath.Join(home, "Downloads")
}
