package managers_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools/catalog"
	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
)

func hatchzipApp() catalog.App {
	return catalog.App{
		Name:     "hatchzip",
		Category: "CLI Tools",
		Scoop:    "xevrion/hatchzip",
		Bucket:   "xevrion https://github.com/xevrion/scoop-bucket",
	}
}

func lines(s string) []string { return strings.Split(strings.TrimSpace(s), "\n") }

// Safety rule 88: a bucket's URL is fixed, and the add command is built from
// it and nothing else.
func TestBucketAddArgvIsFixed(t *testing.T) {
	name, url, ok := hatchzipApp().ScoopBucket()
	if !ok {
		t.Fatal("ScoopBucket() not ok for the hatchzip entry")
	}
	got := managers.Scoop{}.BucketAddCmd(name, url)
	want := []string{"scoop", "bucket", "add", "xevrion", "https://github.com/xevrion/scoop-bucket"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BucketAddCmd = %v, want %v", got, want)
	}
	if got := managers.KnownBuckets()["xevrion"]; got != "https://github.com/xevrion/scoop-bucket" {
		t.Errorf("KnownBuckets()[xevrion] = %q", got)
	}
	// The map is a fresh copy: changing it never changes the next call.
	managers.KnownBuckets()["xevrion"] = "https://evil.example/bucket"
	if got := managers.KnownBuckets()["xevrion"]; got != "https://github.com/xevrion/scoop-bucket" {
		t.Errorf("KnownBuckets() is shared mutable state: %q", got)
	}

	steps, err := managers.InstallPlan(managers.Scoop{}, hatchzipApp())
	if err != nil {
		t.Fatalf("InstallPlan: %v", err)
	}
	if !reflect.DeepEqual(steps[0].Argv, want) {
		t.Errorf("plan's first step = %v, want %v", steps[0].Argv, want)
	}
}

// Safety rule 88: "already exists" counts as added; any other bucket failure
// does not.
func TestAlreadyExistsCountsAsAdded(t *testing.T) {
	cases := []struct {
		file string
		exit int
		want bool
	}{
		{"scoop_bucket_add_ok.txt", 0, false},
		{"scoop_bucket_add_ok2.txt", 0, false},
		{"scoop_bucket_add_exists.txt", 0, true},
		{"scoop_bucket_add_exists.txt", 1, true},
		{"scoop_bucket_add_exists_old.txt", 1, true},
		{"scoop_bucket_add_failed.txt", 1, false},
		{"scoop_bucket_add_offline.txt", 1, false},
	}
	for _, c := range cases {
		got := managers.BucketAlreadyAdded(c.exit, lines(readTestdata(t, c.file)))
		if got != c.want {
			t.Errorf("BucketAlreadyAdded(%d, %s) = %v, want %v", c.exit, c.file, got, c.want)
		}
	}
	if !managers.BucketAlreadyAdded(1, []string{"THE 'XEVRION' BUCKET ALREADY EXISTS."}) {
		t.Error("the match is not case-insensitive")
	}
	if managers.BucketAlreadyAdded(1, nil) {
		t.Error("no output counted as already added")
	}

	steps, err := managers.InstallPlan(managers.Scoop{}, hatchzipApp())
	if err != nil || steps[0].OKIfLine == nil {
		t.Fatalf("the bucket step has no OKIfLine (err %v)", err)
	}
	if !steps[0].OKIfLine(1, lines(readTestdata(t, "scoop_bucket_add_exists.txt"))) {
		t.Error("the bucket step does not accept the recorded already-exists output")
	}
}

func TestInstallPlanWithoutABucketIsOneStep(t *testing.T) {
	git := catalog.App{Name: "Git", Scoop: "git", Winget: "Git.Git", Choco: "git"}
	for _, m := range []managers.Manager{managers.Scoop{}, managers.Winget{}, managers.Choco{}} {
		id := map[string]string{"scoop": "git", "winget": "Git.Git", "choco": "git"}[m.Name()]
		steps, err := managers.InstallPlan(m, git)
		if err != nil {
			t.Fatalf("%s: InstallPlan: %v", m.Name(), err)
		}
		if len(steps) != 1 {
			t.Fatalf("%s: %d steps, want 1", m.Name(), len(steps))
		}
		if want := m.InstallCmd(id); !reflect.DeepEqual(steps[0].Argv, want) {
			t.Errorf("%s: argv = %v, want %v (unchanged)", m.Name(), steps[0].Argv, want)
		}
		if steps[0].OKIfLine != nil {
			t.Errorf("%s: a plain install must succeed on exit 0 only", m.Name())
		}
	}

	// A bucket under a manager that has no buckets is ignored, not run.
	steps, err := managers.InstallPlan(managers.Winget{}, hatchzipApp())
	if err != nil || len(steps) != 1 {
		t.Errorf("winget with a bucket: %d steps, err %v; want one step", len(steps), err)
	}
}

func TestInstallPlanAddsTheBucketThenInstalls(t *testing.T) {
	steps, err := managers.InstallPlan(managers.Scoop{}, hatchzipApp())
	if err != nil {
		t.Fatalf("InstallPlan: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("%d steps, want 2", len(steps))
	}
	want := [][]string{
		{"scoop", "bucket", "add", "xevrion", "https://github.com/xevrion/scoop-bucket"},
		{"scoop", "install", "xevrion/hatchzip"},
	}
	for i := range want {
		if !reflect.DeepEqual(steps[i].Argv, want[i]) {
			t.Errorf("step %d argv = %v, want %v", i, steps[i].Argv, want[i])
		}
	}
	if steps[0].Label != "Adding the xevrion bucket" || steps[1].Label != "Installing hatchzip" {
		t.Errorf("labels = %q, %q", steps[0].Label, steps[1].Label)
	}
}

// Safety rule 88: a bucket not in the built-in list, or in it with another
// URL, is refused before anything runs.
func TestInstallPlanRefusesAnUnknownBucket(t *testing.T) {
	for _, bucket := range []string{
		"evil https://evil.example/bucket",
		"xevrion https://evil.example/scoop-bucket",
		"xevrion",
		"xevrion https://github.com/xevrion/scoop-bucket extra",
	} {
		app := hatchzipApp()
		app.Bucket = bucket
		steps, err := managers.InstallPlan(managers.Scoop{}, app)
		if !errors.Is(err, managers.ErrUnknownBucket) {
			t.Errorf("bucket %q: err = %v, want ErrUnknownBucket", bucket, err)
		}
		if len(steps) != 0 {
			t.Errorf("bucket %q: got steps %v with the error", bucket, steps)
		}
	}
}
