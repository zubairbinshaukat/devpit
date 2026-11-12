package extract

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/extract/archive"
)

func argvRequest(mode Mode) Request {
	first := `D:\Downloads\Game-Backup.zip`
	return Request{
		Set:    archive.Set{Picked: first, First: first, Parts: []string{first}, Format: archive.FormatZip},
		OutDir: `D:\Downloads\Game-Backup`,
		Mode:   mode,
	}
}

// Safety rule 76: -y is only in the extract argv, and extractArgs is called
// only by Engine.Start and the auto-resume loop in job.go.
func TestYesFlagIsOnlyInTheExtractArgv(t *testing.T) {
	for _, mode := range []Mode{ModeInPlace, ModeKeepArchive} {
		r := argvRequest(mode)
		if slices.Contains(checkArgs(r), "-y") || slices.Contains(restoreArgs(r), "-y") {
			t.Fatal("-y outside the extract argv")
		}
		if !slices.Contains(extractArgs(r), "-y") {
			t.Fatal("extract argv lacks -y")
		}
	}
	calls := sourceMatches(t, regexp.MustCompile(`extractArgs\(`))
	want := []string{"argv.go", "engine.go", "job.go"}
	if !slices.Equal(calls, want) {
		t.Fatalf("extractArgs( appears in %v, want %v", calls, want)
	}
	if n := strings.Count(readSource(t, "engine.go"), "extractArgs("); n != 1 {
		t.Fatalf("engine.go calls extractArgs %d times, want 1 (Start)", n)
	}
	if n := strings.Count(readSource(t, "job.go"), "extractArgs("); n != 1 {
		t.Fatalf("job.go calls extractArgs %d times, want 1 (the auto-resume)", n)
	}
}

// Safety rule 83: only the allowed words and flags, never a password flag.
func TestArgvUsesOnlyTheAllowedWords(t *testing.T) {
	allowedVerb := map[string]bool{"check": true, "extract": true, "restore": true}
	allowedFlag := map[string]bool{"-o": true, "-y": true, "--keep-archive": true}
	for _, mode := range []Mode{ModeInPlace, ModeKeepArchive} {
		r := argvRequest(mode)
		r.Password = NewSecret("hunter2")
		for _, argv := range [][]string{checkArgs(r), extractArgs(r), restoreArgs(r)} {
			if !allowedVerb[argv[0]] {
				t.Fatalf("verb %q", argv[0])
			}
			for _, a := range argv[1:] {
				if strings.HasPrefix(a, "-") && !allowedFlag[a] {
					t.Fatalf("flag %q in %v", a, argv)
				}
				if strings.Contains(a, "hunter2") {
					t.Fatalf("password in argv %v", argv)
				}
			}
		}
	}
	// No source builds argv with a password flag or -q.
	for _, bad := range []string{`"-p"`, `"--password"`, `"-q"`, `"--quiet"`, `"--replace"`, `"--skip-existing"`, `"cmd"`, `"powershell"`} {
		if files := sourceMatches(t, regexp.MustCompile(regexp.QuoteMeta(bad))); len(files) > 0 {
			t.Errorf("%s appears in %v", bad, files)
		}
	}
}

// readSource reads one non-test source file of this package.
func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name) // #nosec G304 -- this package's own source
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sourceFiles lists this package's non-test Go files.
func sourceFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out
}

// sourceMatches lists the non-test files whose code (comments stripped)
// matches re.
func sourceMatches(t *testing.T, re *regexp.Regexp) []string {
	t.Helper()
	var hits []string
	for _, f := range sourceFiles(t) {
		if re.MatchString(stripComments(readSource(t, f))) {
			hits = append(hits, f)
		}
	}
	return hits
}

var lineComment = regexp.MustCompile(`(?m)//.*$`)

// stripComments drops // comments (the package has no /* */ ones), so a
// comment naming a call is not counted as the call.
func stripComments(src string) string { return lineComment.ReplaceAllString(src, "") }
