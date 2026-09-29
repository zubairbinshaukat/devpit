package devpit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/host"
)

// fakeAdmin is a worker that records what it was asked and can fail.
type fakeAdmin struct {
	ops  []elevate.ShareRequest
	fail error
}

func (f *fakeAdmin) Share(_ context.Context, r elevate.ShareRequest, _ func(string)) error {
	f.ops = append(f.ops, r)
	return f.fail
}

func (f *fakeAdmin) Close() error { return nil }

// writeManifest leaves a manifest of a dead process in dir.
func writeManifest(t *testing.T, dir string) {
	t.Helper()
	// PID 0 is "no owner", which Leftover treats as gone.
	raw := `{"version":1,"pid":0,"user":"devpit-abcd","path":"D:\\Games","share":"Games-x7k2"}`
	if err := os.WriteFile(filepath.Join(dir, "share-host.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestShareCleanupWithNothingLeftSaysSoAndNeverAsksForAdmin(t *testing.T) {
	var out bytes.Buffer
	launched := false
	err := runShareCleanup(context.Background(), &out, t.TempDir(), func(context.Context) (host.Admin, error) {
		launched = true
		return &fakeAdmin{}, nil
	})
	if err != nil || launched || !strings.Contains(out.String(), "Nothing to clean up") {
		t.Errorf("err %v, launched %v, out %q", err, launched, out.String())
	}
}

func TestShareCleanupRemovesALeftoverShare(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir)
	admin := &fakeAdmin{}
	var out bytes.Buffer
	if err := runShareCleanup(context.Background(), &out, dir, func(context.Context) (host.Admin, error) { return admin, nil }); err != nil {
		t.Fatal(err)
	}
	if len(admin.ops) != 1 || admin.ops[0].Op != elevate.ShareOpCleanup || admin.ops[0].User != "devpit-abcd" {
		t.Errorf("ops = %+v", admin.ops)
	}
	if !strings.Contains(out.String(), "The old share is gone") {
		t.Errorf("out = %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "share-host.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the manifest is still there after a successful cleanup")
	}
}

func TestShareCleanupRemovesEveryLeftover(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir)
	raw := `{"version":1,"pid":0,"user":"devpit-wxyz","path":"D:\\Movies","share":"Movies-x7k2"}`
	if err := os.WriteFile(filepath.Join(dir, "share-host-devpit-wxyz.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	admin := &fakeAdmin{}
	var out bytes.Buffer
	if err := runShareCleanup(context.Background(), &out, dir, func(context.Context) (host.Admin, error) { return admin, nil }); err != nil {
		t.Fatal(err)
	}
	if len(admin.ops) != 2 || strings.Count(out.String(), "The old share is gone") != 2 {
		t.Errorf("ops = %+v, out %q", admin.ops, out.String())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "share-host*")); len(left) != 0 {
		t.Errorf("records left: %v", left)
	}
}

func TestShareCleanupSaysWhenThePromptWasDeclined(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir)
	var out bytes.Buffer
	err := runShareCleanup(context.Background(), &out, dir, func(context.Context) (host.Admin, error) {
		return nil, &elevate.DeclinedError{}
	})
	if err == nil || !strings.Contains(out.String(), "You said no to the admin prompt") {
		t.Errorf("err %v, out %q", err, out.String())
	}
	if _, serr := os.Stat(filepath.Join(dir, "share-host.json")); serr != nil {
		t.Error("the manifest was removed although nothing was cleaned up")
	}
}

func TestShareCommandIsRegistered(t *testing.T) {
	root := NewRootCmd()
	c, _, err := root.Find([]string{"share", "cleanup"})
	if err != nil || c.Name() != "cleanup" {
		t.Errorf("Find = %v %v", c, err)
	}
}
