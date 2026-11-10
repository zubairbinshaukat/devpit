package job_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/share/job"
)

func sample() job.Job {
	return job.Job{
		Host: "192.168.1.5", Share: "Games", User: "devpit-abcd", Dest: `D:\Games`,
		TotalFiles: 100, TotalBytes: 80 << 30, DoneFiles: 60, DoneBytes: 47 << 30,
		State: job.StatePaused, LastError: "The other PC stopped answering.",
		Started: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC), Updated: time.Date(2026, 9, 29, 10, 20, 0, 0, time.UTC),
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := job.Save(dir, sample()); err != nil {
		t.Fatal(err)
	}
	got, ok, err := job.Load(dir)
	if err != nil || !ok {
		t.Fatalf("Load = %v %v", ok, err)
	}
	want := sample()
	if got.Host != want.Host || got.DoneBytes != want.DoneBytes || got.State != want.State || got.Version != 1 ||
		!got.Updated.Equal(want.Updated) {
		t.Errorf("got %+v", got)
	}
	if got.Remaining() != 33<<30 {
		t.Errorf("remaining = %d", got.Remaining())
	}
}

// A record written before 0.5.1 has no mode or hotspot name, and still loads
// as a normal copy that can be resumed.
func TestAnOldJobRecordStillLoads(t *testing.T) {
	dir := t.TempDir()
	old := `{"version":1,"host":"192.168.1.5","share":"Games","user":"devpit-abcd","dest":"D:\\Games",` +
		`"total_files":100,"total_bytes":1000,"done_files":60,"done_bytes":600,"state":"paused",` +
		`"started":"2026-09-29T10:00:00Z","updated":"2026-09-29T10:20:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, "share-recv.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	j, ok, err := job.Load(dir)
	if err != nil || !ok {
		t.Fatalf("Load = %v %v", ok, err)
	}
	if j.Mode != "" || j.SSID != "" || j.Hotspot() || !j.Resumable() || j.DoneBytes != 600 {
		t.Errorf("old record = %+v", j)
	}
}

func TestAHotspotJobRoundTrips(t *testing.T) {
	dir := t.TempDir()
	j := sample()
	j.Mode, j.SSID, j.Host = job.ModeHotspot, "Devpit-x7k2", "192.168.137.1"
	if err := job.Save(dir, j); err != nil {
		t.Fatal(err)
	}
	got, _, err := job.Load(dir)
	if err != nil || !got.Hotspot() || got.SSID != "Devpit-x7k2" {
		t.Errorf("got %+v, %v", got, err)
	}
	// A normal copy writes neither field.
	if err := job.Save(dir, sample()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "share-recv.json"))
	if strings.Contains(string(raw), `"mode"`) || strings.Contains(string(raw), `"ssid"`) {
		t.Errorf("a normal record carries hotspot fields:\n%s", raw)
	}
}

func TestTheRecordNeverHoldsAPassword(t *testing.T) {
	dir := t.TempDir()
	hs := sample()
	hs.Mode, hs.SSID = job.ModeHotspot, "Devpit-x7k2"
	if err := job.Save(dir, hs); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "share-recv.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "pass") {
		t.Errorf("the record mentions a password:\n%s", raw)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for k := range m {
		if strings.Contains(strings.ToLower(k), "pass") || strings.Contains(strings.ToLower(k), "secret") {
			t.Errorf("field %q looks like a secret", k)
		}
	}
}

func TestLoadWithNothingSavedAndClear(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := job.Load(dir); ok || err != nil {
		t.Errorf("empty dir = %v %v", ok, err)
	}
	if err := job.Save(dir, sample()); err != nil {
		t.Fatal(err)
	}
	if err := job.Clear(dir); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := job.Load(dir); ok {
		t.Error("still there after Clear")
	}
	if err := job.Clear(dir); err != nil {
		t.Errorf("clearing twice: %v", err)
	}
}

func TestADamagedRecordIsAnErrorNotAnEmptyResult(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "share-recv.json"), []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := job.Load(dir); ok || err == nil {
		t.Errorf("damaged = %v %v", ok, err)
	}
}

func TestResumable(t *testing.T) {
	j := sample()
	for _, st := range []job.State{job.StateRunning, job.StatePaused, job.StateFailed} {
		j.State = st
		if !j.Resumable() {
			t.Errorf("%s is not resumable", st)
		}
	}
	j.State = job.StateDone
	if j.Resumable() {
		t.Error("a finished copy is resumable")
	}
	j = sample()
	j.Host = ""
	if j.Resumable() {
		t.Error("a record with no host is resumable")
	}
	if (job.Job{TotalBytes: 5, DoneBytes: 9}).Remaining() != 0 {
		t.Error("remaining went negative")
	}
}

func TestSaveLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	for range 5 {
		if err := job.Save(dir, sample()); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("%d files in the directory, want just the record", len(entries))
	}
}
