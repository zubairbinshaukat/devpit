package clean_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	cleanengine "github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/clean"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

const (
	keyEnter = tea.KeyEnter
	keyEsc   = tea.KeyEscape
)

// safeItems is a fixture with nothing in it that needs a typed word, so a
// plain y answers its confirmation.
func safeItems() []scan.Item {
	return []scan.Item{
		item(`D:\work\api`, "node_modules", 1_200_000_000, scan.TierSafe),
		item(`D:\work\web`, "node_modules", 500_000_000, scan.TierSafe),
	}
}

// start drives a fresh screen from the submenu to the results table.
func start(t *testing.T, engines clean.Engines) *harness {
	t.Helper()
	cfg := testConfig()
	screen := clean.New().WithEngines(engines).WithClock(stepClock())
	h := newHarness(t, screen, cfg)

	h.special(keyEnter) // "Project Junk"
	h.settle()

	if got := h.state(); got != "results" {
		t.Fatalf("the screen is on %q after a scan, want results", got)
	}
	return h
}

// TestScanStreamsBatchesIntoTable proves the scan reaches the table through
// the batching stream rather than one message per item, and that everything
// the scanner sent arrives.
func TestScanStreamsBatchesIntoTable(t *testing.T) {
	items := []scan.Item{
		item(`D:\work\api`, "node_modules", 1_200_000_000, scan.TierSafe),
		item(`D:\work\api`, "dist", 80_000_000, scan.TierReview),
		item(`D:\work\web`, "node_modules", 500_000_000, scan.TierSafe),
		item(`D:\work\old`, "target", 300_000_000, scan.TierSafe),
	}

	engines := baseEngines()
	engines.Scan = func(ctx context.Context, _ scan.Options, out chan<- scan.Item, progress func(scan.Stats)) (scan.Stats, error) {
		// Two waves, far enough apart that the 150 ms batcher cuts them into
		// separate batches.
		for i, it := range items {
			if i == 2 {
				time.Sleep(200 * time.Millisecond)
			}
			select {
			case out <- it:
			case <-ctx.Done():
				return scan.Stats{}, ctx.Err()
			}
			if progress != nil {
				progress(scan.Stats{Found: uint64(i + 1)})
			}
		}
		return scan.Stats{Found: uint64(len(items)), DirsChecked: 99, Elapsed: time.Second}, nil
	}

	h := start(t, engines)

	if got := h.model().Table().Len(); got != len(items) {
		t.Fatalf("the table holds %d items, want %d", got, len(items))
	}

	batches := 0
	for _, msg := range h.trace {
		if strings.Contains(strings.ToLower(msgName(msg)), "scanbatch") {
			batches++
		}
	}
	if batches < 2 {
		t.Errorf("the scan arrived in %d batch(es); the stream is not batching", batches)
	}

	out := ansi.Strip(h.view())
	if !strings.Contains(out, "node_modules") {
		t.Errorf("the results are not on screen:\n%s", out)
	}
	// Only the Safe items are pre-ticked, and only they count in the footer.
	if !strings.Contains(out, "Selected: 3 items") {
		t.Errorf("the selection footer is wrong:\n%s", out)
	}
}

// TestCannotReachDeletingWithoutConfirm is safety rule 1.
//
// It throws every key sequence that might plausibly start a delete at the
// results table — Enter spam, d, y, a stray Yes answer arriving out of turn,
// d with nothing ticked — and asserts the delete engine was never called.
// The last block is the control: the one legitimate sequence does call it,
// so a preselect that ticked nothing could not make this test pass by
// accident.
func TestCannotReachDeletingWithoutConfirm(t *testing.T) {
	var calls int
	var callArgs [][]cleanengine.Item

	engines := baseEngines(safeItems()...)
	engines.Clean = func(_ context.Context, items []cleanengine.Item, _ cleanengine.Options, _ func(cleanengine.Progress)) cleanengine.Report {
		calls++
		callArgs = append(callArgs, items)
		return cleanengine.Report{Freed: 1, Elapsed: time.Second}
	}

	h := start(t, engines)

	// Enter spam. Each Enter opens the confirmation; the next one answers it
	// with the default, which is No.
	for i := 0; i < 6; i++ {
		h.special(keyEnter)
		h.settle()
	}
	if calls != 0 {
		t.Fatalf("Enter spam reached the delete engine (%d calls)", calls)
	}

	// d then the ways of saying no.
	for _, no := range []func(){
		func() { h.key("n") },
		func() { h.special(keyEsc) },
	} {
		h.stepUntil("results")
		h.key("d")
		h.settle()
		if got := h.state(); got != "confirm" {
			t.Fatalf("d put the screen on %q, want confirm", got)
		}
		no()
		h.settle()
		if calls != 0 {
			t.Fatalf("answering no reached the delete engine (%d calls)", calls)
		}
	}

	// y pressed on the results table itself is not a confirmation.
	h.stepUntil("results")
	h.key("y")
	h.settle()
	if calls != 0 {
		t.Fatalf("y on the results table reached the delete engine (%d calls)", calls)
	}

	// A Yes answer arriving while the screen is not showing a dialog is
	// ignored: the state machine, not the message, decides.
	h.send(confirm.AnsweredMsg{ID: "clean.delete", Answer: confirm.AnswerYes})
	h.settle()
	if calls != 0 {
		t.Fatalf("an out-of-turn Yes reached the delete engine (%d calls)", calls)
	}
	if got := h.state(); got == "deleting" {
		t.Fatal("an out-of-turn Yes put the screen into deleting")
	}

	// d with nothing ticked never even opens the dialog.
	h.stepUntil("results")
	h.untickAll()
	h.key("d")
	h.settle()
	if got := h.state(); got == "confirm" || got == "deleting" {
		t.Fatalf("d with an empty selection put the screen on %q", got)
	}
	if calls != 0 {
		t.Fatalf("d with an empty selection reached the delete engine (%d calls)", calls)
	}

	if h.sawConfirmedYes() {
		t.Fatal("this test produced a Yes answer; it is no longer testing what it says")
	}

	// The control: tick, d, y. Now, and only now, the engine runs.
	h.tickAll()
	h.key("d")
	h.settle()
	if got := h.state(); got != "confirm" {
		t.Fatalf("d put the screen on %q, want confirm", got)
	}
	h.key("y")
	h.settle()

	if calls != 1 {
		t.Fatalf("the confirmed delete called the engine %d times, want 1", calls)
	}
	if !h.sawConfirmedYes() {
		t.Fatal("the engine ran without a Yes answer in the trace")
	}
	if len(callArgs[0]) != len(safeItems()) {
		t.Errorf("the engine was handed %d items, want %d", len(callArgs[0]), len(safeItems()))
	}
}

// TestCarefulSelectionRequiresTypedWord is safety rule 3 applied to this
// screen: a selection holding a Careful item cannot be confirmed with y.
func TestCarefulSelectionRequiresTypedWord(t *testing.T) {
	var calls int
	engines := baseEngines(
		item(`D:\work\api`, "node_modules", 1_200_000_000, scan.TierSafe),
		item(`D:\work\api`, ".next", 40_000_000, scan.TierCareful),
	)
	engines.Clean = func(context.Context, []cleanengine.Item, cleanengine.Options, func(cleanengine.Progress)) cleanengine.Report {
		calls++
		return cleanengine.Report{Freed: 1, Elapsed: time.Second}
	}

	h := start(t, engines)

	// The Careful item is not pre-ticked, so tick everything by hand.
	h.tickAll()
	h.key("d")
	h.settle()
	if got := h.state(); got != "confirm" {
		t.Fatalf("d put the screen on %q, want confirm", got)
	}
	if out := ansi.Strip(h.view()); !strings.Contains(out, "Type DELETE to continue") {
		t.Fatalf("the Careful confirmation does not ask for the word:\n%s", out)
	}

	// y alone types a letter; it does not answer.
	h.key("y")
	h.settle()
	if calls != 0 {
		t.Fatalf("y bypassed the typed word (%d calls)", calls)
	}
	if got := h.state(); got != "confirm" {
		t.Fatalf("y left the screen on %q, want confirm", got)
	}

	// Enter with the wrong word answers No.
	h.special(keyEnter)
	h.settle()
	if calls != 0 {
		t.Fatalf("enter with the wrong word deleted (%d calls)", calls)
	}

	// The real thing: the exact word, then Enter.
	h.stepUntil("results")
	h.key("d")
	h.settle()
	for _, r := range "DELETE" {
		h.key(string(r))
	}
	h.settle()
	if calls != 0 {
		t.Fatalf("typing the word alone deleted (%d calls)", calls)
	}
	h.special(keyEnter)
	h.settle()

	if calls != 1 {
		t.Fatalf("the typed-word confirmation called the engine %d times, want 1", calls)
	}
}

// TestCarefulWordIsCaseSensitive keeps "delete" from passing for "DELETE".
func TestCarefulWordIsCaseSensitive(t *testing.T) {
	var calls int
	engines := baseEngines(item(`D:\work\api`, ".next", 40_000_000, scan.TierCareful))
	engines.Clean = func(context.Context, []cleanengine.Item, cleanengine.Options, func(cleanengine.Progress)) cleanengine.Report {
		calls++
		return cleanengine.Report{}
	}

	h := start(t, engines)
	h.tickAll()
	h.key("d")
	h.settle()
	for _, r := range "delete" {
		h.key(string(r))
	}
	h.special(keyEnter)
	h.settle()

	if calls != 0 {
		t.Fatalf("a lower-case word confirmed a Careful delete (%d calls)", calls)
	}
}

// TestEscDuringDeleteFinishesItemAndShowsSummary proves the interrupt
// promise in docs/safety.md: Esc cancels the context the engine is watching,
// the engine finishes the item in flight, and the screen reports what was
// done instead of dropping the user back onto a half-finished table.
func TestEscDuringDeleteFinishesItemAndShowsSummary(t *testing.T) {
	started := make(chan struct{}, 1)

	engines := baseEngines(safeItems()...)
	engines.Clean = func(ctx context.Context, items []cleanengine.Item, _ cleanengine.Options, progress func(cleanengine.Progress)) cleanengine.Report {
		started <- struct{}{}
		if progress != nil {
			progress(cleanengine.Progress{Index: 0, Total: len(items), Path: items[0].Path})
		}
		<-ctx.Done() // the item in flight finishes, then the run stops
		return cleanengine.Report{
			Freed:   items[0].Size,
			Deleted: []cleanengine.Result{{Item: items[0], Reason: "Deleted api\\node_modules."}},
			Skipped: []cleanengine.Result{{
				Item:   items[1],
				Err:    context.Canceled,
				Reason: "Stopped before web\\node_modules — nothing was removed. Run the clean again to finish.",
			}},
			Elapsed: 2 * time.Second,
		}
	}

	h := start(t, engines)
	h.tickAll()
	h.key("d")
	h.settle()
	h.key("y")
	h.stepUntil("deleting")

	select {
	case <-started:
	case <-time.After(cmdTimeout):
		t.Fatal("the delete engine never started")
	}

	h.special(keyEsc)
	h.settle()

	if got := h.state(); got != "summary" {
		t.Fatalf("after Esc the screen is on %q, want summary", got)
	}
	out := ansi.Strip(h.view())
	if !strings.Contains(out, "Freed") {
		t.Errorf("the summary does not say what was freed:\n%s", out)
	}
	if !strings.Contains(out, "Run the clean again to finish") {
		t.Errorf("the summary does not name what was left:\n%s", out)
	}
}

// TestStaleCacheBadge proves the previous scan is on screen straight away,
// under a badge that says it is not fresh.
func TestStaleCacheBadge(t *testing.T) {
	release := make(chan struct{})
	cached := []scan.Item{item(`D:\work\api`, "node_modules", 999, scan.TierSafe)}
	fresh := item(`D:\work\api`, "node_modules", 1_200_000_000, scan.TierSafe)

	engines := baseEngines()
	engines.LoadCache = func(root string) (*scan.Cache, error) {
		return &scan.Cache{Root: root, Items: cached, When: time.Now().Add(-time.Hour)}, nil
	}
	engines.Scan = func(ctx context.Context, _ scan.Options, out chan<- scan.Item, _ func(scan.Stats)) (scan.Stats, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return scan.Stats{}, ctx.Err()
		}
		out <- fresh
		return scan.Stats{Found: 1, Bytes: fresh.Size, Elapsed: time.Second}, nil
	}

	cfg := testConfig()
	h := newHarness(t, clean.New().WithEngines(engines).WithClock(stepClock()), cfg)
	h.special(keyEnter)

	// Step only far enough for the cache to land; the scan is still blocked.
	for i := 0; i < 10 && !h.model().Stale(); i++ {
		h.step()
	}

	if !h.model().Stale() {
		t.Fatal("the cached scan was never shown")
	}
	if got := h.state(); got != "scanning" {
		t.Fatalf("the screen is on %q while the cache is showing, want scanning", got)
	}
	out := ansi.Strip(h.view())
	if !strings.Contains(out, clean.StaleBadge) {
		t.Fatalf("the stale badge is missing:\n%s", out)
	}
	if !strings.Contains(out, "node_modules") {
		t.Fatalf("the cached rows are not on screen:\n%s", out)
	}

	close(release)
	h.settle()

	if h.model().Stale() {
		t.Error("the badge survived the fresh scan")
	}
	got := h.model().Table().Items()
	if len(got) != 1 || got[0].Size != fresh.Size {
		t.Errorf("the fresh scan did not replace the cached rows: %+v", got)
	}
}

// TestLockedItemOffersRetry covers the one failure a user can actually fix,
// and proves R runs the retry rather than the whole delete again.
func TestLockedItemOffersRetry(t *testing.T) {
	locked := item(`D:\work\api`, "node_modules", 1_200_000_000, scan.TierSafe)
	lockErr := &cleanengine.LockedError{
		Path:    locked.Path,
		Holders: []cleanengine.Holder{{PID: 4242, Name: "Code.exe"}},
		Err:     errors.New("the process cannot access the file"),
	}

	var retries int
	engines := baseEngines(locked)
	engines.Clean = func(_ context.Context, items []cleanengine.Item, _ cleanengine.Options, _ func(cleanengine.Progress)) cleanengine.Report {
		return cleanengine.Report{
			Skipped: []cleanengine.Result{{
				Item:    items[0],
				Err:     lockErr,
				Reason:  "Couldn't delete api\\node_modules — it's open in Code.exe. Close it and press R to retry.",
				Holders: lockErr.Holders,
			}},
			Elapsed: time.Second,
		}
	}
	engines.Retry = func(_ context.Context, it cleanengine.Item, _ cleanengine.Options, _ func(cleanengine.Progress)) cleanengine.Result {
		retries++
		return cleanengine.Result{Item: it, Reason: "Deleted api\\node_modules."}
	}

	h := start(t, engines)
	h.key("d")
	h.settle()
	h.key("y")
	h.settle()

	if got := h.state(); got != "summary" {
		t.Fatalf("after a locked delete the screen is on %q, want summary", got)
	}
	out := ansi.Strip(h.view())
	for _, want := range []string{"Code.exe", "press R to retry"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary is missing %q:\n%s", want, out)
		}
	}

	h.send(tea.KeyPressMsg{Code: 'R', Text: "R"})
	h.settle()

	if retries != 1 {
		t.Fatalf("R ran the retry %d times, want 1", retries)
	}
	after := ansi.Strip(h.view())
	if strings.Contains(after, "press R to retry") {
		t.Errorf("the locked notice survived a successful retry:\n%s", after)
	}
	if !strings.Contains(after, "Freed 1.1 GB") {
		t.Errorf("the retried bytes are not in the summary:\n%s", after)
	}
}

// TestSummaryUpdatesTheLifetimeTotalAndRecentFolders proves the two pieces
// of state a finished clean leaves behind.
func TestSummaryUpdatesTheLifetimeTotalAndRecentFolders(t *testing.T) {
	engines := baseEngines(safeItems()...)
	engines.Clean = func(_ context.Context, items []cleanengine.Item, _ cleanengine.Options, _ func(cleanengine.Progress)) cleanengine.Report {
		return cleanengine.Report{Freed: 2_000_000_000, Deleted: []cleanengine.Result{{Item: items[0]}}, Elapsed: time.Second}
	}

	h := start(t, engines)
	h.key("d")
	h.settle()
	h.key("y")
	h.settle()

	var saved *config.Config
	for _, msg := range h.trace {
		if c, ok := msg.(uictx.ConfigChangedMsg); ok && c.Persist {
			cfg := c.Config
			saved = &cfg
		}
	}
	if saved == nil {
		t.Fatal("the finished clean saved no configuration")
	}
	if saved.LifetimeFreedBytes != 2_000_000_000 {
		t.Errorf("LifetimeFreedBytes = %d, want 2000000000", saved.LifetimeFreedBytes)
	}
	if len(saved.RecentFolders) == 0 || saved.RecentFolders[0] != projectsFolder {
		t.Errorf("RecentFolders = %v, want %s first", saved.RecentFolders, projectsFolder)
	}
}

// TestSummaryEnterPopsBackHome closes the loop: every long action ends on a
// card and Enter leaves it.
func TestSummaryEnterPopsBackHome(t *testing.T) {
	engines := baseEngines(safeItems()...)
	engines.Clean = func(_ context.Context, items []cleanengine.Item, _ cleanengine.Options, _ func(cleanengine.Progress)) cleanengine.Report {
		return cleanengine.Report{Freed: 10, Deleted: []cleanengine.Result{{Item: items[0]}}}
	}

	h := start(t, engines)
	h.key("d")
	h.settle()
	h.key("y")
	h.settle()
	h.special(keyEnter)
	h.settle()

	for _, msg := range h.trace {
		if _, ok := msg.(uictx.PopScreenMsg); ok {
			return
		}
	}
	t.Fatal("enter on the summary did not pop the screen")
}

// TestResumeSweepsTheRecentFolders proves the fourth submenu row reaches the
// tombstone sweep rather than a scan.
func TestResumeSweepsTheRecentFolders(t *testing.T) {
	var roots []string
	engines := baseEngines()
	engines.Sweep = func(_ context.Context, r []string, _ cleanengine.Options, _ func(cleanengine.Progress)) cleanengine.Report {
		roots = append([]string(nil), r...)
		return cleanengine.Report{Deleted: []cleanengine.Result{{Item: cleanengine.Item{Path: `D:\work\api\node_modules.devpit-ab12cd34`}}}}
	}

	cfg := testConfig()
	cfg.RecentFolders = []string{`D:\side`}
	h := newHarness(t, clean.New().WithEngines(engines).WithClock(stepClock()), cfg)

	// Down three rows to "Resume interrupted deletes", then Enter.
	for i := 0; i < 3; i++ {
		h.send(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	h.special(keyEnter)
	h.settle()

	if len(roots) != 2 || roots[0] != projectsFolder || roots[1] != `D:\side` {
		t.Fatalf("the sweep was given %v, want the default folder then the recent one", roots)
	}
	if got := h.state(); got != "summary" {
		t.Fatalf("the sweep finished on %q, want summary", got)
	}
	if out := ansi.Strip(h.view()); !strings.Contains(out, "Finished 1 interrupted delete") {
		t.Errorf("the sweep summary is wrong:\n%s", out)
	}
}

// TestEmptyFolderIsNeverABlankScreen covers the empty state from plan.md.
func TestEmptyFolderIsNeverABlankScreen(t *testing.T) {
	h := start(t, baseEngines())
	out := ansi.Strip(h.view())
	if strings.TrimSpace(out) == "" {
		t.Fatal("an empty scan drew nothing at all")
	}
	if !strings.Contains(out, "Nothing found") {
		t.Errorf("an empty scan does not say so:\n%s", out)
	}
}

// TestTinyTerminalGetsAResizeNotice keeps the screen from drawing garbage
// below 80×24.
func TestTinyTerminalGetsAResizeNotice(t *testing.T) {
	h := start(t, baseEngines(safeItems()...))
	h.ctx.Width, h.ctx.Height = 60, 20
	if out := ansi.Strip(h.view()); !strings.Contains(out, "bigger window") {
		t.Fatalf("a 60×20 terminal did not get the notice:\n%s", out)
	}
}

// TestDuplicateKeyPressesAreDebounced covers the Windows consoles that
// deliver every key twice.
func TestDuplicateKeyPressesAreDebounced(t *testing.T) {
	frozen := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	engines := baseEngines(safeItems()...)
	screen := clean.New().WithEngines(engines).WithClock(func() time.Time { return frozen })
	h := newHarness(t, screen, testConfig())

	// Two identical Enters inside the debounce window must act once, which
	// here means one scan rather than two.
	h.special(keyEnter)
	h.special(keyEnter)
	h.settle()

	if got := h.state(); got != "results" {
		t.Fatalf("the screen is on %q, want results", got)
	}
	if got := h.model().Table().Len(); got != len(safeItems()) {
		t.Fatalf("the table holds %d items, want %d; the echo started a second scan", got, len(safeItems()))
	}
}

// msgName is the type name of a message, which is how this test counts the
// batches without reaching into the package's unexported types.
func msgName(msg tea.Msg) string { return fmt.Sprintf("%T", msg) }
