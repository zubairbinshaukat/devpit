package install

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/tools/catalog"
)

const (
	bucketAddLine = "scoop bucket add xevrion https://github.com/xevrion/scoop-bucket"
	hatchzipLine  = "scoop install xevrion/hatchzip"
)

func bucketCatalog() []catalog.App {
	return []catalog.App{
		{
			Name: "hatchzip", Category: "CLI Tools", Scoop: "xevrion/hatchzip",
			Bucket: "xevrion https://github.com/xevrion/scoop-bucket", Detect: []string{"hatchzip.exe"},
		},
		{Name: "AppC", Category: "CLI Tools", Scoop: "appc", Detect: []string{"appc.exe"}},
	}
}

// runSelected selects every named app on a Scoop model, confirms, and runs to
// the summary.
func runSelected(t *testing.T, cat []catalog.App, step RunStepFunc, names ...string) Model {
	t.Helper()
	ctx := testCtx()
	m := New(
		WithDetectFunc(fakeDetectFn([]tools.Tool{{Name: "scoop", Found: true}})),
		WithLookPathFunc(fakeLookPath(nil)),
		WithLoadCatalogFunc(func() ([]catalog.App, error) { return cat, nil }),
		WithRunStepFunc(step),
	)
	m = reachList(t, m, ctx)
	if m.manager == nil || m.manager.Name() != "scoop" {
		t.Fatalf("manager = %v, want scoop", m.manager)
	}
	for _, name := range names {
		idx := rowIndex(m.rows, name)
		if idx < 0 {
			t.Fatalf("no row for %q", name)
		}
		m.rows[idx].selected = true
	}
	next, _ := m.Update(keyPress(13, ""), ctx)
	m = next.(Model)
	if m.state != stateConfirm {
		t.Fatalf("state = %v, want stateConfirm", m.state)
	}
	next, cmd := m.Update(keyPress('y', "y"), ctx)
	m = next.(Model)
	next, _ = m.Update(cmd(), ctx)
	m = next.(Model)
	if m.state != stateRunning {
		t.Fatalf("state = %v, want stateRunning", m.state)
	}
	var events []runEvent
	for ev := range m.events {
		events = append(events, ev)
	}
	next, _ = m.Update(runBatchMsg{events: events}, ctx)
	m = next.(Model)
	if m.state != stateSummary {
		t.Fatalf("state = %v, want stateSummary", m.state)
	}
	return m
}

// scriptedSteps answers each argv, joined by spaces, from replies (OK with no
// output when absent) and records the order they were run in.
func scriptedSteps(mu *sync.Mutex, calls *[]string, replies map[string]tools.StepResult) RunStepFunc {
	return func(_ context.Context, argv []string, _ time.Duration, onLine func(string)) tools.StepResult {
		line := strings.Join(argv, " ")
		mu.Lock()
		*calls = append(*calls, line)
		mu.Unlock()
		res, ok := replies[line]
		if !ok {
			res = tools.StepResult{OK: true}
		}
		for _, l := range res.LastLines {
			onLine(l)
		}
		return res
	}
}

func TestInstallRunsTheBucketStepThenTheInstall(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	m := runSelected(t, bucketCatalog(), scriptedSteps(&mu, &calls, nil), "hatchzip")

	want := []string{bucketAddLine, hatchzipLine}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("ran %v, want %v", calls, want)
	}
	if len(m.results) != 1 || !m.results[0].OK {
		t.Fatalf("results = %+v, want one OK result", m.results)
	}
}

// Safety rule 88: a bucket that already exists counts as added.
func TestInstallCountsAnExistingBucketAsAdded(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	replies := map[string]tools.StepResult{
		bucketAddLine: {ExitCode: 1, LastLines: []string{"The 'xevrion' bucket already exists."}},
	}
	m := runSelected(t, bucketCatalog(), scriptedSteps(&mu, &calls, replies), "hatchzip")

	if want := []string{bucketAddLine, hatchzipLine}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("ran %v, want %v (the install must follow an existing bucket)", calls, want)
	}
	if len(m.results) != 1 || !m.results[0].OK {
		t.Fatalf("results = %+v, want one OK result", m.results)
	}
}

// Safety rule 88: any other bucket failure stops that install, and only that
// one; the next app still runs with its argv unchanged.
func TestBucketFailureStopsThatAppOnly(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	replies := map[string]tools.StepResult{
		bucketAddLine: {ExitCode: 1, LastLines: []string{"Failed to clone 'https://github.com/xevrion/scoop-bucket'."}},
	}
	m := runSelected(t, bucketCatalog(), scriptedSteps(&mu, &calls, replies), "hatchzip", "AppC")

	want := []string{bucketAddLine, "scoop install appc"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("ran %v, want %v (no install of hatchzip, AppC unchanged)", calls, want)
	}
	if len(m.results) != 2 {
		t.Fatalf("results = %+v, want two", m.results)
	}
	hz, other := m.results[0], m.results[1]
	if hz.OK || !strings.Contains(hz.SkipReason, "Adding the xevrion bucket failed") {
		t.Errorf("hatchzip result = %+v, want failed with the bucket step named", hz)
	}
	if !other.OK {
		t.Errorf("AppC result = %+v, want OK", other)
	}
	if hz.Retry != bucketAddLine+" && "+hatchzipLine {
		t.Errorf("hatchzip Retry = %q, want both commands", hz.Retry)
	}

	view := m.View(testCtx())
	if len(hz.LastLines) != 1 || !strings.Contains(hz.LastLines[0], "Failed to clone") {
		t.Errorf("hatchzip LastLines = %v, want the bucket step's output", hz.LastLines)
	}
	for _, s := range []string{"xevrion bucket failed"} {
		if !strings.Contains(view, s) {
			t.Errorf("summary does not say %q:\n%s", s, view)
		}
	}
}

// Safety rule 88: a catalog bucket that is not in the known list runs nothing.
func TestAnUnknownBucketRunsNothing(t *testing.T) {
	cat := bucketCatalog()
	cat[0].Bucket = "evil https://evil.example/bucket"
	var mu sync.Mutex
	var calls []string
	m := runSelected(t, cat, scriptedSteps(&mu, &calls, nil), "hatchzip")

	if len(calls) != 0 {
		t.Fatalf("ran %v, want nothing", calls)
	}
	if len(m.results) != 1 || m.results[0].OK || m.results[0].SkipReason == "" {
		t.Fatalf("results = %+v, want one failed result with a reason", m.results)
	}
}
