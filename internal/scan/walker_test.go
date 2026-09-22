package scan_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/scan"
	"github.com/zubairbinshaukat/devpit/internal/scan/rules"
)

// runScan walks opts and collects everything it finds. It drains the channel
// on its own goroutine, which is exactly the contract Run documents: the
// caller owns the channel and closes it after Run returns.
func runScan(t *testing.T, opts scan.Options) ([]scan.Item, scan.Stats, error) {
	t.Helper()
	ctx := t.Context()

	items := make(chan scan.Item, 256)
	collected := make([]scan.Item, 0, 32)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for it := range items {
			collected = append(collected, it)
		}
	}()

	stats, err := scan.Run(ctx, opts, items, nil)
	close(items)
	<-done

	return collected, stats, err
}

// projectOptions is the milestone 1 configuration over one root.
func projectOptions(root string) scan.Options {
	return scan.Options{
		Roots:      []string{root},
		Rules:      rules.Project(),
		ActiveDays: 7,
		Workers:    4,
	}
}

// relPaths turns items into root-relative paths with forward slashes, so
// assertions read the same on every platform.
func relPaths(t *testing.T, root string, items []scan.Item) []string {
	t.Helper()
	out := make([]string, 0, len(items))
	for _, it := range items {
		rel, err := filepath.Rel(root, it.Path)
		if err != nil {
			rel = it.Path
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.EqualFold(s, needle) {
			return true
		}
	}
	return false
}

func findItem(items []scan.Item, relSuffix string) (scan.Item, bool) {
	for _, it := range items {
		if strings.HasSuffix(strings.ToLower(filepath.ToSlash(it.Path)), strings.ToLower(relSuffix)) {
			return it, true
		}
	}
	return scan.Item{}, false
}

// Safety rule 9: a junk folder is only junk when its marker sits beside it.
// notaproject holds build, bin and target folders full of somebody's own
// files, with no build.gradle, no .csproj and no Cargo.toml anywhere near
// them. Not one of them may be listed.
func TestMarkerGating(t *testing.T) {
	root := buildTree(t)
	items, _, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := relPaths(t, root, items)

	for _, unwanted := range []string{"notaproject/build", "notaproject/bin", "notaproject/target"} {
		if contains(got, unwanted) {
			t.Errorf("listed %s, which has no marker file beside it; got %v", unwanted, got)
		}
	}
	for _, wanted := range []string{"rust/target", "dotnet/bin", "dotnet/obj", "web/dist"} {
		if !contains(got, wanted) {
			t.Errorf("did not list %s, which has its marker beside it; got %v", wanted, got)
		}
	}
}

// A node_modules with no package.json beside it is still listed, because
// hiding it would be worse, but it is flagged so the results table can refuse
// to pre-tick it.
func TestUnverifiedNodeModulesIsFlagged(t *testing.T) {
	root := buildTree(t)
	items, _, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	orphan, ok := findItem(items, "orphan/node_modules")
	if !ok {
		t.Fatalf("orphan/node_modules was not listed at all; got %v", relPaths(t, root, items))
	}
	if !orphan.Unverified {
		t.Error("orphan/node_modules has no package.json beside it but was not marked unverified")
	}

	verified, ok := findItem(items, "web/node_modules")
	if !ok {
		t.Fatal("web/node_modules was not listed")
	}
	if verified.Unverified {
		t.Error("web/node_modules has a package.json beside it but was marked unverified")
	}
}

// A project vendored inside another project's node_modules is pruned with
// the folder that contains it. Listing it separately would offer the user a
// row that disappears the moment they delete the row above it.
func TestNestedProjectIsPruned(t *testing.T) {
	root := buildTree(t)
	items, _, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, rel := range relPaths(t, root, items) {
		if strings.Contains(strings.ToLower(rel), "node_modules/vendored") {
			t.Errorf("listed %s from inside a pruned node_modules", rel)
		}
	}
	if !contains(relPaths(t, root, items), "api/node_modules") {
		t.Error("api/node_modules itself was not listed")
	}
}

// Safety rule 6: the never-touch list is always skipped.
func TestNeverTouchIsSkipped(t *testing.T) {
	root := buildTree(t)
	opts := projectOptions(root)
	opts.NeverTouch = []string{filepath.Join(root, "secret")}

	items, _, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, rel := range relPaths(t, root, items) {
		if strings.HasPrefix(strings.ToLower(rel), "secret/") {
			t.Errorf("listed %s from a never-touch folder", rel)
		}
	}

	// Without the entry the same folder is found, so the test is proving the
	// filter rather than a typo in the fixture.
	items, _, err = runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !contains(relPaths(t, root, items), "secret/node_modules") {
		t.Fatal("the fixture's secret/node_modules is not found even without a never-touch entry")
	}
}

// Windows paths are case-insensitive, so "Dist" is "dist".
func TestMatchingIsCaseInsensitive(t *testing.T) {
	root := buildTree(t)
	items, _, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !contains(relPaths(t, root, items), "caseweb/Dist") {
		t.Errorf("Dist was not matched against the dist rule; got %v", relPaths(t, root, items))
	}
}

// .git is never descended into: it is never junk and it is full of tiny
// files. The fixture hides a node_modules inside one to prove it.
func TestGitDirectoryIsNeverEntered(t *testing.T) {
	root := buildTree(t)
	items, _, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, rel := range relPaths(t, root, items) {
		if strings.Contains(strings.ToLower(rel), ".git/") {
			t.Errorf("listed %s from inside a .git directory", rel)
		}
	}
}

// Sizes come from the directory listing, not from a stat per file, and they
// are the sum of what is actually inside.
func TestSizesAreMeasured(t *testing.T) {
	root := buildTree(t)
	items, stats, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	web, ok := findItem(items, "web/node_modules")
	if !ok {
		t.Fatal("web/node_modules was not listed")
	}
	if web.Size != 2000 {
		t.Errorf("web/node_modules size = %d, want 2000", web.Size)
	}

	api, ok := findItem(items, "api/node_modules")
	if !ok {
		t.Fatal("api/node_modules was not listed")
	}
	// 1000 + 40 + 500 + 500: the pruned inner project's bytes still count
	// towards the folder that contains them.
	if api.Size != 2040 {
		t.Errorf("api/node_modules size = %d, want 2040", api.Size)
	}

	if stats.Found != uint64(len(items)) {
		t.Errorf("Stats.Found = %d but %d items were sent", stats.Found, len(items))
	}
	if stats.Bytes == 0 {
		t.Error("Stats.Bytes is zero after a scan that found items")
	}
	if stats.DirsChecked == 0 {
		t.Error("Stats.DirsChecked is zero after a scan that walked a tree")
	}
	if stats.Elapsed <= 0 {
		t.Error("Stats.Elapsed is not set")
	}
}

// Items carry enough about their project for the results table to group them
// and for the pre-selection to leave active projects alone.
func TestItemsCarryProjectAndActivity(t *testing.T) {
	root := buildTree(t)
	items, _, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	web, ok := findItem(items, "web/node_modules")
	if !ok {
		t.Fatal("web/node_modules was not listed")
	}
	if !strings.EqualFold(web.Project, filepath.Join(root, "web")) {
		t.Errorf("Project = %q, want %q", web.Project, filepath.Join(root, "web"))
	}
	if web.Name != "node_modules" {
		t.Errorf("Name = %q, want node_modules", web.Name)
	}
	if web.Rule == "" || web.RestoreHint == "" {
		t.Errorf("item is missing its rule name or restore hint: %+v", web)
	}
	if !web.Active {
		t.Error("a project whose files were written a moment ago is not marked active")
	}
	if web.Tier != scan.TierSafe {
		t.Errorf("node_modules tier = %v, want Safe", web.Tier)
	}
}

// Cancelling mid-scan keeps whatever was already found and reports why it
// stopped. A partial scan is a result, not a failure.
func TestCancellationKeepsPartialResults(t *testing.T) {
	root := buildTree(t)

	ctx, cancel := context.WithCancel(t.Context())
	items := make(chan scan.Item, 256)
	collected := make([]scan.Item, 0, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for it := range items {
			collected = append(collected, it)
			cancel() // Stop as soon as anything at all has been found.
		}
	}()

	stats, err := scan.Run(ctx, projectOptions(root), items, nil)
	close(items)
	<-done

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if len(collected) == 0 {
		t.Fatal("a cancelled scan threw away everything it had already found")
	}
	if stats.Found != uint64(len(collected)) {
		t.Errorf("Stats.Found = %d but %d items arrived", stats.Found, len(collected))
	}
}

// Cancelling before the walk starts is not an error worth a different code
// path: the scan simply finds nothing and says why.
func TestCancelledBeforeStart(t *testing.T) {
	root := buildTree(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	items := make(chan scan.Item, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range items { //nolint:revive // Draining is the point.
		}
	}()
	_, err := scan.Run(ctx, projectOptions(root), items, nil)
	close(items)
	<-done

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

// The progress callback fires while the scan runs and once more at the end,
// and the final call always carries the final numbers.
func TestProgressIsReported(t *testing.T) {
	root := buildTree(t)

	items := make(chan scan.Item, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range items { //nolint:revive // Draining is the point.
		}
	}()

	var last scan.Stats
	calls := 0
	stats, err := scan.Run(t.Context(), projectOptions(root), items, func(s scan.Stats) {
		calls++
		last = s
	})
	close(items)
	<-done

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls == 0 {
		t.Fatal("the progress callback was never called")
	}
	if last.Found != stats.Found || last.Bytes != stats.Bytes {
		t.Errorf("the last progress call %+v does not match the returned stats %+v", last, stats)
	}
}

// Options that cannot produce a scan are refused before anything is walked,
// with an error that names the problem.
func TestRunRefusesImpossibleOptions(t *testing.T) {
	root := buildTree(t)

	cases := []struct {
		name string
		opts scan.Options
		want error
	}{
		{"no roots", scan.Options{Rules: rules.Project()}, scan.ErrNoRoots},
		{"blank root", scan.Options{Roots: []string{"  "}, Rules: rules.Project()}, scan.ErrNoRoots},
		{"no rules", scan.Options{Roots: []string{root}}, scan.ErrNoRules},
		{"unc root", scan.Options{Roots: []string{`\\server\share`}, Rules: rules.Project()}, scan.ErrNetworkRoot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := make(chan scan.Item, 1)
			_, err := scan.Run(t.Context(), tc.opts, items, nil)
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

// A root that is a file rather than a directory is refused too.
func TestRunRefusesAFileAsRoot(t *testing.T) {
	root := buildTree(t)
	file := filepath.Join(root, "api", "package.json")

	items := make(chan scan.Item, 1)
	_, err := scan.Run(t.Context(), scan.Options{Roots: []string{file}, Rules: rules.Project()}, items, nil)
	if !errors.Is(err, scan.ErrNotADirectory) {
		t.Errorf("error = %v, want ErrNotADirectory", err)
	}
}

// A path longer than 260 characters is walked like any other. The standard
// library adds the \\?\ prefix; this test is here to prove Devpit does not
// undo that somewhere.
func TestLongPathsAreWalked(t *testing.T) {
	root := t.TempDir()
	deep, ok := deepPath(t, root)
	if !ok {
		t.Skip("this filesystem will not create a path over 260 characters")
	}
	writeFile(t, filepath.Join(deep, "package.json"), 40)
	writeFile(t, filepath.Join(deep, "node_modules", "dep", "index.js"), 123)

	items, _, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found, ok := findItem(items, "node_modules")
	if !ok {
		t.Fatalf("the node_modules at the end of a 300-character path was not found")
	}
	if found.Size != 123 {
		t.Errorf("size = %d, want 123", found.Size)
	}
}

// A read-only file is still counted. Whether it can be deleted is the clean
// engine's problem; the scanner's job is to measure it.
func TestReadOnlyFilesAreCounted(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "proj", "package.json"), 40)
	locked := filepath.Join(root, "proj", "node_modules", "dep", "index.js")
	writeFile(t, locked, 250)
	if err := os.Chmod(locked, 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	items, _, err := runScan(t, projectOptions(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, ok := findItem(items, "proj/node_modules")
	if !ok {
		t.Fatal("proj/node_modules was not listed")
	}
	if got.Size != 250 {
		t.Errorf("size = %d, want 250", got.Size)
	}
}

// Two roots that name the same folder are walked once, so nothing is listed
// or counted twice.
func TestDuplicateRootsAreWalkedOnce(t *testing.T) {
	root := buildTree(t)
	opts := projectOptions(root)
	opts.Roots = []string{root, root, strings.ToUpper(root)}

	items, _, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	seen := map[string]int{}
	for _, it := range items {
		seen[strings.ToLower(it.Path)]++
	}
	for path, n := range seen {
		if n > 1 {
			t.Errorf("%s was listed %d times", path, n)
		}
	}
}

// Large files are not part of the Project Junk scan and are found by the Full
// Scan rules, which match on size rather than on a folder name.
func TestLargeFileRuleMatchesBySize(t *testing.T) {
	root := t.TempDir()
	small := filepath.Join(root, "small.bin")
	big := filepath.Join(root, "big.iso")
	writeFile(t, small, 100)
	writeFile(t, big, 4096)

	opts := scan.Options{
		Roots:   []string{root},
		Workers: 2,
		Rules: []scan.Rule{{
			Name:        "test large file",
			Kind:        scan.KindLargeFile,
			MinSize:     1024,
			Tier:        scan.TierCareful,
			RestoreHint: "Download it again.",
		}},
	}
	items, _, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1: %v", len(items), relPaths(t, root, items))
	}
	if items[0].Path != big {
		t.Errorf("matched %s, want %s", items[0].Path, big)
	}
	if items[0].Size != 4096 {
		t.Errorf("size = %d, want 4096", items[0].Size)
	}
	if items[0].Kind != scan.KindLargeFile {
		t.Errorf("kind = %v, want KindLargeFile", items[0].Kind)
	}
}

// Location rules are probed at their fixed addresses rather than found by
// walking, and their %ENVVAR% placeholders come from the environment.
func TestLocationRulesAreProbed(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "fake-cache")
	writeFile(t, filepath.Join(cacheDir, "blob.bin"), 512)
	t.Setenv("DEVPIT_TEST_CACHE_HOME", root)

	opts := scan.Options{
		Roots:   []string{filepath.Join(root, "empty")},
		Workers: 2,
		Rules: []scan.Rule{{
			Name:        "test cache",
			Kind:        scan.KindPackageCache,
			Locations:   []string{`%DEVPIT_TEST_CACHE_HOME%\fake-cache`},
			Tier:        scan.TierSafe,
			RestoreHint: "It refills itself.",
		}},
	}
	mkdir(t, opts.Roots[0])

	items, _, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Size != 512 {
		t.Errorf("size = %d, want 512", items[0].Size)
	}
}

// A location whose environment variable is unset expands to nothing rather
// than to a path like "\fake-cache" at the root of the current drive.
func TestUnsetLocationPlaceholderIsDropped(t *testing.T) {
	r := scan.Rule{Locations: []string{`%DEVPIT_DEFINITELY_UNSET_VARIABLE%\cache`, `%TEMP%\devpit-probe`}}
	t.Setenv("TEMP", filepath.Join(t.TempDir(), "tmp"))

	got := r.ExpandLocations()
	if len(got) != 1 {
		t.Fatalf("ExpandLocations returned %v, want exactly the one expandable location", got)
	}
	if strings.Contains(got[0], "%") {
		t.Errorf("ExpandLocations left a placeholder in %q", got[0])
	}
}

// The default worker count follows min(32, 2*cores) and an explicit count is
// respected. This is behaviour a benchmark depends on, so it is pinned.
func TestScanCompletesWithASingleWorker(t *testing.T) {
	root := buildTree(t)
	opts := projectOptions(root)
	opts.Workers = 1

	items, _, err := runScan(t, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("a single-worker scan found nothing")
	}
}

// BenchmarkWalk measures a scan of the small fixture tree. The 100k-file tree
// lives behind gen.ps1 -Large and is not built here, because a benchmark that
// takes a minute to set up is a benchmark nobody runs.
func BenchmarkWalk(b *testing.B) {
	root := buildBenchTree(b)
	opts := projectOptions(root)

	b.ResetTimer()
	for b.Loop() {
		items := make(chan scan.Item, 256)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for range items { //nolint:revive // Draining is the point.
			}
		}()
		if _, err := scan.Run(context.Background(), opts, items, nil); err != nil {
			b.Fatalf("Run: %v", err)
		}
		close(items)
		<-done
	}
}

// buildBenchTree writes a tree wide enough for the walk to be worth timing:
// 40 projects, each with a few hundred files in node_modules.
func buildBenchTree(b *testing.B) string {
	b.Helper()
	root := b.TempDir()
	for p := range 40 {
		project := filepath.Join(root, "proj"+itoa(p))
		if err := os.MkdirAll(project, 0o755); err != nil {
			b.Fatalf("creating %s: %v", project, err)
		}
		if err := os.WriteFile(filepath.Join(project, "package.json"), []byte("{}"), 0o644); err != nil {
			b.Fatalf("writing package.json: %v", err)
		}
		for d := range 10 {
			dir := filepath.Join(project, "node_modules", "dep"+itoa(d))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				b.Fatalf("creating %s: %v", dir, err)
			}
			for f := range 20 {
				name := filepath.Join(dir, "f"+itoa(f)+".js")
				if err := os.WriteFile(name, []byte("// x"), 0o644); err != nil {
					b.Fatalf("writing %s: %v", name, err)
				}
			}
		}
	}
	return root
}

// itoa avoids pulling strconv into a file that needs one integer formatted.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
