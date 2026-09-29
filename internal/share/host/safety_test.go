package host

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
)

// env is a fixed environment for the folder check.
func env(k string) string {
	return map[string]string{
		"WINDIR": `C:\Windows`, "SystemRoot": `C:\WINDOWS`, "ProgramFiles": `C:\Program Files`,
		"ProgramFiles(x86)": `C:\Program Files (x86)`, "ProgramData": `C:\ProgramData`,
		"USERPROFILE": `C:\Users\bob`,
	}[k]
}

func TestCheckFolder(t *testing.T) {
	ok := []string{`D:\Games`, `C:\Users\bob\Documents\Games`, `C:\Users\bob\Desktop`, `E:\Backup\2026`, `C:\Work`}
	for _, p := range ok {
		if err := CheckFolder(p, env); err != nil {
			t.Errorf("CheckFolder(%q) = %v, want it allowed", p, err)
		}
	}
	bad := []string{
		`D:\`, `D:`, `c:/`, `C:\Windows`, `C:\windows\System32`, `C:\Program Files\Steam`, `C:\ProgramData`,
		`C:\Users`, `C:\Users\`, `C:\Users\bob`, `C:\USERS\BOB\`, `C:\Users\bob\AppData\Roaming`, `C:\Users\bob\.ssh`,
		`C:\Users\alice`, `C:\Users\alice\AppData`, `\\server\share`, `Games`, `D:\Games\..\..`, ``,
	}
	for _, p := range bad {
		err := CheckFolder(p, env)
		if !errors.Is(err, ErrUnsafeFolder) {
			t.Errorf("CheckFolder(%q) = %v, want it refused", p, err)
			continue
		}
		if !strings.Contains(err.Error(), "Choose") && !strings.Contains(err.Error(), "choose") {
			t.Errorf("CheckFolder(%q) says %q, which does not say what to do", p, err)
		}
	}
}

func TestAnUnsafeFolderIsRefusedBeforeTheAdminPrompt(t *testing.T) {
	r := newRig(t, CategoryPrivate)
	r.m.deps.Getenv = env
	_, err := r.m.Start(context.Background(), Options{Path: `C:\Users\bob`, Adapter: lan}, nil)
	if !errors.Is(err, ErrUnsafeFolder) {
		t.Fatalf("err = %v", err)
	}
	if r.launches != 0 || r.m.Active() {
		t.Error("the worker was started for a folder that is refused")
	}
	if _, ok, _ := LoadManifest(r.dir); ok {
		t.Error("a manifest was written for a refused folder")
	}
}

// TestTwoSharesAtOnceKeepTheirOwnRecords pins a bug: every share wrote the
// same share-host.json, so with two Devpit windows sharing, the second
// overwrote the first's record and the first one's Stop deleted the second's.
// A crash of the second then left a share nobody would ever clean up.
func TestTwoSharesAtOnceKeepTheirOwnRecords(t *testing.T) {
	a := newRig(t, CategoryPrivate)
	b := newRig(t, CategoryPrivate)
	b.dir = a.dir
	b.m.deps.Dir, b.m.deps.Rand, b.m.deps.PID = a.dir, seeded(99), 8765
	ca, err := a.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := b.m.Start(context.Background(), Options{Path: `D:\Movies`, Adapter: lan}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ca.User == cb.User {
		t.Fatal("the two shares got the same account")
	}
	all, err := LoadManifests(a.dir)
	if err != nil || len(all) != 2 {
		t.Fatalf("manifests = %+v, %v; want one per share", all, err)
	}
	if err = a.m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	all, _ = LoadManifests(a.dir)
	if len(all) != 1 || all[0].User != cb.User {
		t.Fatalf("after the first stop: %+v, want only the second share's record", all)
	}
	// The second Devpit dies: its share is the leftover.
	m, ok, err := Leftover(a.dir, func(int, time.Time) bool { return false }, 1)
	if err != nil || !ok || m.User != cb.User {
		t.Errorf("leftover = %+v %v %v", m, ok, err)
	}
	admin := &fakeAdmin{}
	if err = CleanUp(context.Background(), a.dir, m, func(context.Context) (Admin, error) { return admin, nil }); err != nil {
		t.Fatal(err)
	}
	if all, _ = LoadManifests(a.dir); len(all) != 0 {
		t.Errorf("records left after cleanup: %+v", all)
	}
}

func TestLeftoverSkipsLiveOwnersAndReturnsTheDeadOne(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	for _, m := range []Manifest{
		{PID: 10, User: "devpit-live", StartedAt: t0},
		{PID: 20, User: "devpit-dead", StartedAt: t0.Add(time.Minute)},
	} {
		if err := saveManifest(dir, m); err != nil {
			t.Fatal(err)
		}
	}
	m, ok, err := Leftover(dir, func(pid int, _ time.Time) bool { return pid == 10 }, 1)
	if err != nil || !ok || m.User != "devpit-dead" {
		t.Errorf("leftover = %+v %v %v", m, ok, err)
	}
}

// TestAPIDReusedByAnotherProgramDoesNotHideALeftover: Windows hands a dead
// Devpit's PID to the next process soon. That process was created after the
// share started, so it cannot be its owner.
func TestAPIDReusedByAnotherProgramDoesNotHideALeftover(t *testing.T) {
	started := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	if !sameOwner(started.Add(-time.Hour), started) {
		t.Error("the process that started the share is not its owner")
	}
	if sameOwner(started.Add(time.Minute), started) {
		t.Error("a process created after the share started was taken for its owner")
	}
}

func TestProcessAliveOnThisMachine(t *testing.T) {
	self := os.Getpid()
	if !ProcessAlive(self, time.Time{}) || !ProcessAlive(self, time.Now().Add(time.Minute)) {
		t.Error("this very process is not alive")
	}
	if runtime.GOOS == "windows" && ProcessAlive(self, time.Now().Add(-24*time.Hour)) {
		t.Error("this process was created today, so it cannot own a share started yesterday")
	}
}

// TestCleanupOfARunThatDiedBeforeTheGrantDoesNotTouchTheFolder pins a
// cleanup that could never finish: the manifest named the folder from the
// start, so a run that died before its account existed made the worker run
// icacls /remove:g for an account Windows cannot find, which fails, and the
// cleanup was offered, and failed, on every launch.
func TestCleanupOfARunThatDiedBeforeTheGrantDoesNotTouchTheFolder(t *testing.T) {
	r := newRig(t, CategoryPrivate)
	var atAccount, atGrant Manifest
	r.admin.onOp = func(req elevate.ShareRequest) {
		switch req.Op {
		case elevate.ShareOpAccount:
			atAccount, _, _ = LoadManifest(r.dir)
		case elevate.ShareOpGrant:
			atGrant, _, _ = LoadManifest(r.dir)
		}
	}
	if _, err := r.m.Start(context.Background(), Options{Path: `D:\Games`, Adapter: lan}, nil); err != nil {
		t.Fatal(err)
	}
	if req := atAccount.request(); req.Path != "" || atAccount.Path != `D:\Games` {
		t.Errorf("before the account: cleanup path %q, manifest path %q; want no folder step but the path kept for display", req.Path, atAccount.Path)
	}
	if req := atGrant.request(); req.Path != `D:\Games` {
		t.Errorf("at the grant: cleanup path %q, want the folder", req.Path)
	}
}
