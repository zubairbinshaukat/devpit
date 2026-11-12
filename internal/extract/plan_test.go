package extract

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/plans/*.golden.json")

// Safety rule 84: every recorded plan parses to its golden Plan.
func TestRecordedPlansParse(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "plans", "*.txt"))
	if err != nil || len(files) < 8 {
		t.Fatalf("fixtures: %v %d", err, len(files))
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f) // #nosec G304 -- test fixture
			if err != nil {
				t.Fatal(err)
			}
			got := ParsePlan(string(raw))
			golden := strings.TrimSuffix(f, ".txt") + ".golden.json"
			if *update {
				b, _ := json.MarshalIndent(got, "", "  ")
				if werr := os.WriteFile(golden, append(b, '\n'), 0o600); werr != nil {
					t.Fatal(werr)
				}
				return
			}
			b, err := os.ReadFile(golden) // #nosec G304 -- test fixture
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			var want Plan
			if err := json.Unmarshal(b, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(norm0(got), norm0(want)) {
				t.Fatalf("plan = %+v\nwant %+v", got, want)
			}
			// CRLF line ends parse the same.
			if crlf := ParsePlan(strings.ReplaceAll(string(raw), "\n", "\r\n")); !reflect.DeepEqual(norm0(crlf), norm0(got)) {
				t.Fatalf("CRLF plan = %+v", crlf)
			}
		})
	}
}

// norm0 makes nil and empty slices equal for comparison.
func norm0(p Plan) Plan {
	if len(p.Notes) == 0 {
		p.Notes = nil
	}
	if len(p.Unknown) == 0 {
		p.Unknown = nil
	}
	return p
}

func TestRecordedPlanValues(t *testing.T) {
	read := func(name string) Plan {
		raw, err := os.ReadFile(filepath.Join("testdata", "plans", name)) // #nosec G304 -- test fixture
		if err != nil {
			t.Fatal(err)
		}
		return ParsePlan(string(raw))
	}
	if p := read("zip_resuming.txt"); !p.Resuming || p.LeftBytes != 808_000_000 || p.TotalBytes != 2_000_000_000 || p.Entries != 2029 {
		t.Errorf("resuming = %+v", p)
	}
	if p := read("rar_parts.txt"); p.Name != "archive.part1.rar" || p.MoreParts != 2 || p.Format != "rar" {
		t.Errorf("rar = %+v", p)
	}
	if p := read("7z_split.txt"); p.Name != "archive.7z.001" || p.MoreParts != 2 || p.Format != "7z" {
		t.Errorf("7z = %+v", p)
	}
	if p := read("targz.txt"); p.Format != "tar.gz" || p.Entries != 7 {
		t.Errorf("tar.gz = %+v", p)
	}
	if p := read("zip_nofit.txt"); p.ShortBy != 87_500_000_000 || p.Reserved != 200_000_000_000 || p.AvailableAfter != 0 {
		t.Errorf("nofit = %+v", p)
	}
	if p := read("handmade_all_done.txt"); !p.AllDone || !p.Resuming || p.LeftBytes != 0 {
		t.Errorf("all done = %+v", p)
	}
	if p := read("handmade_header_only.txt"); p.Entries != 1 || p.TotalBytes != 1_500_000_000_000 {
		t.Errorf("header only = %+v", p)
	}
	if p := read("handmade_reworded.txt"); p.Entries != 12408 || len(p.Unknown) != 3 || p.Available != 0 || p.Required != 0 {
		t.Errorf("reworded = %+v", p)
	}
}

// Safety rule 84: lines the parser does not know are kept aside and change
// nothing it does know.
func TestUnknownPlanLinesAreKeptNotJudged(t *testing.T) {
	base := "archive.zip  84.2 GB zip, 96.0 GB uncompressed in 12408 entries\n" +
		"  available                    31.5 GB\n" +
		"  required                     13.8 GB (+2.0 GB reserved)\n"
	extra := base + "  verdict: DOES NOT FIT\nerror: nothing fits\n  available after  maybe\n"
	a, b := ParsePlan(base), ParsePlan(extra)
	if len(b.Unknown) != 3 {
		t.Fatalf("unknown = %q", b.Unknown)
	}
	b.Unknown = nil
	if !reflect.DeepEqual(norm0(a), norm0(b)) {
		t.Fatalf("an unknown line changed the plan:\n%+v\n%+v", a, b)
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{
		"7 B": 7, "401.4 KB": 401_400, "3.0 MB": 3_000_000, "84.2 GB": 84_200_000_000,
		"1.5 TB": 1_500_000_000_000, "0 B": 0, " 2.0 GB ": 2_000_000_000,
		"": 0, "GB": 0, "12": 0, "1.2 PB": 0, "x GB": 0, "-1 GB": 0, "1,2 GB": 0, "2GB": 0,
	} {
		if got := parseSize(in); got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestPlanSurvivesCRLFAndMissingLines(t *testing.T) {
	p := ParsePlan("archive.zip  3.0 MB zip, 3.0 MB uncompressed in 7 entries\r\n\r\n  available after              1.0 GB\r\n")
	if p.Entries != 7 || p.AvailableAfter != 1_000_000_000 || p.Available != 0 || len(p.Unknown) != 0 {
		t.Fatalf("plan = %+v", p)
	}
	// The same lines without their indentation (a runner that trimmed them).
	raw, err := os.ReadFile(filepath.Join("testdata", "plans", "zip_resuming.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var trimmed []string
	for _, ln := range strings.Split(string(raw), "\n") {
		trimmed = append(trimmed, strings.TrimSpace(ln))
	}
	if a, b := ParsePlan(string(raw)), ParsePlan(strings.Join(trimmed, "\n")); !reflect.DeepEqual(norm0(a), norm0(b)) {
		t.Fatalf("trimmed lines parse differently:\n%+v\n%+v", a, b)
	}
	if p := ParsePlan(""); !reflect.DeepEqual(norm0(p), Plan{}) {
		t.Fatalf("empty = %+v", p)
	}
}

// The keep-archive note only sets the display default; it is still a note.
func TestKeepArchiveNoteSetsTheDefaultOnly(t *testing.T) {
	with := ParsePlan("  note: there is also enough space to keep the archive (--keep-archive)\n")
	if !with.KeepArchiveFits || len(with.Notes) != 1 {
		t.Fatalf("plan = %+v", with)
	}
	kept := ParsePlan("  note: the archive is kept, so space is required for all extracted files\n")
	if kept.KeepArchiveFits {
		t.Fatal("the keep-archive mode's own note set KeepArchiveFits")
	}
	// The verdict ignores it: same text, every exit.
	for _, exit := range []int{0, 3, 4} {
		if v := mapCheck(exit, "", false, ModeInPlace); (v == VerdictFits) != (exit == 0) {
			t.Errorf("exit %d -> %v", exit, v)
		}
	}
}
