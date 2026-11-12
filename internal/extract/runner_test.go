package extract

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Safety rule 79: the password reaches hatchzip only as HATCHZIP_PASSWORD in
// the child's own environment.
func TestPasswordIsOnlyInTheChildEnv(t *testing.T) {
	env := childEnv([]string{"PATH=C:\\bin", "HOME=x"}, NewSecret("hunter2"))
	var hits []string
	for _, kv := range env {
		if strings.Contains(kv, "hunter2") {
			hits = append(hits, kv)
		}
	}
	if len(hits) != 1 || hits[0] != "HATCHZIP_PASSWORD=hunter2" {
		t.Fatalf("env = %q", env)
	}
	if env := childEnv([]string{"PATH=x"}, Secret{}); len(env) != 1 || env[0] != "PATH=x" {
		t.Fatalf("no password: env = %q", env)
	}
}

// Safety rule 79: an inherited HATCHZIP_PASSWORD, in any case, never reaches
// hatchzip; only the request's password does.
func TestAnInheritedPasswordIsDropped(t *testing.T) {
	inherited := []string{"HATCHZIP_PASSWORD=leaked", "hatchzip_password=leaked2", "Hatchzip_Password=leaked3", "=C:=C:\\", "OTHER=1"}
	for _, pw := range []Secret{{}, NewSecret("mine")} {
		env := childEnv(inherited, pw)
		joined := strings.Join(env, "\n")
		if strings.Contains(joined, "leaked") {
			t.Fatalf("inherited password kept: %q", env)
		}
		if !strings.Contains(joined, "OTHER=1") || !strings.Contains(joined, "=C:=C:\\") {
			t.Fatalf("other variables lost: %q", env)
		}
		if strings.Contains(joined, "HATCHZIP_PASSWORD=mine") != !pw.Empty() {
			t.Fatalf("env = %q", env)
		}
	}
}

// Safety rule 79: the record never holds a password, by type and on disk.
func TestTheRecordNeverHoldsAPassword(t *testing.T) {
	b, err := json.Marshal(Record{First: `D:\x.zip`, OutDir: `D:\x`, Started: time.Unix(0, 0).UTC(), Tool: "0.1.10"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ToLower(b), []byte("pass")) {
		t.Fatalf("record JSON has a password field: %s", b)
	}
	dir := t.TempDir()
	store := fileRecords{dir: func() (string, error) { return dir, nil }}
	if serr := store.Save([]Record{{First: `D:\x.zip`, OutDir: `D:\x`, Tool: "0.1.10"}}); serr != nil {
		t.Fatal(serr)
	}
	recs, err := store.Load()
	if err != nil || len(recs) != 1 || recs[0].OutDir != `D:\x` {
		t.Fatalf("load = %+v, %v", recs, err)
	}
	if err := store.Save(nil); err != nil {
		t.Fatal(err)
	}
	if recs, err := store.Load(); err != nil || len(recs) != 0 {
		t.Fatalf("after clearing: %+v, %v", recs, err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*")); len(m) != 0 {
		t.Fatalf("clearing left files: %v", m)
	}
}

func TestPumpSplitsCleansAndBounds(t *testing.T) {
	in := "a\r\nb\rc\n\n\x1b[31mred\x1b[0m\n" + strings.Repeat("x", 10_000) + "\n  last"
	ch := make(chan Line, 16)
	pump(strings.NewReader(in), true, ch)
	close(ch)
	var got []string
	for ln := range ch {
		if !ln.Stderr {
			t.Fatal("stderr flag lost")
		}
		got = append(got, ln.Text)
	}
	if len(got) != 6 || got[0] != "a" || got[1] != "b" || got[2] != "c" || got[3] != "red" || len(got[4]) != maxLine || got[5] != "  last" {
		t.Fatalf("lines = %q", got)
	}
}

func TestPumpAllMergesBothPipesAndCloses(t *testing.T) {
	lines, done := pumpAll(strings.NewReader("plan\n  available 1 GB\n"), strings.NewReader("error: x\n"))
	var out, errs int
	for ln := range lines {
		if ln.Stderr {
			errs++
		} else {
			out++
		}
	}
	<-done
	if out != 2 || errs != 1 {
		t.Fatalf("stdout %d stderr %d", out, errs)
	}
}
