package clean

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/protect"
)

// Rule 30 at the sweep: a tombstone that sits inside a login folder is left
// alone, the same as one on the never-touch list. A crash mid-delete can not
// be turned into a way of deleting inside .claude.
func TestSweepRefusesATombstoneInsideALoginFolder(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "me")
	parent := filepath.Join(home, ".claude", "local")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := makeTree(t, parent, "node_modules")
	tomb := tombstoneDir(t, target)

	opts := Options{Workers: 4, Protect: protect.Default(homeEnv(home))}
	rep := Sweep(context.Background(), []string{home}, opts, nil)
	if len(rep.Deleted) != 0 {
		t.Fatalf("the sweep deleted %+v", rep.Deleted)
	}
	if len(rep.Skipped) != 1 || !errors.Is(rep.Skipped[0].Err, ErrProtected) {
		t.Fatalf("skipped = %+v, want one ErrProtected", rep.Skipped)
	}
	if len(countFiles(t, tomb)) == 0 {
		t.Error("the tombstone was emptied")
	}

	res := Retry(context.Background(), Item{Path: target, Size: 64, Tier: TierSafe}, opts, nil)
	if !errors.Is(res.Err, ErrProtected) {
		t.Fatalf("Retry err = %v, want ErrProtected", res.Err)
	}
}

// Rules 8 and 30 together: a junction from a project's node_modules into a
// login folder is removed as a link, exactly as before, and the login folder
// it pointed at survives. Protecting login folders does not make Devpit
// refuse a folder because of where a link inside it points, and it does not
// make the delete follow the link either.
func TestAJunctionToALoginFolderIsRemovedAsALinkAndTheLoginSurvives(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	home := filepath.Join(base, "me")
	login := filepath.Join(home, ".claude")
	if err := os.MkdirAll(login, 0o755); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(login, ".credentials.json")
	if err := os.WriteFile(canary, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	junk := makeTree(t, filepath.Join(base, "proj"), "node_modules")
	mklinkJunction(t, filepath.Join(junk, "claude"), login)

	opts := Options{Protect: protect.Default(homeEnv(home))}
	rep := Run(context.Background(), []Item{{Path: junk, Size: 1, Tier: TierSafe}}, opts, nil)
	if len(rep.Skipped) != 0 {
		t.Fatalf("skipped: %+v", rep.Skipped)
	}
	if _, err := os.Lstat(junk); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the junk tree survived: %v", err)
	}
	if _, err := os.Stat(canary); err != nil {
		t.Fatalf("the login folder was deleted through the link: %v", err)
	}

	// The other way round: a login folder reached through a junction is
	// refused by the reparse rule before the protect list is even asked.
	link := filepath.Join(base, "shortcut")
	mklinkJunction(t, link, home)
	if err := Preflight(Item{Path: filepath.Join(link, ".claude")}, opts); !errors.Is(err, ErrReparsePoint) && !errors.Is(err, ErrProtected) {
		t.Errorf("Preflight through a junction into .claude = %v, want a refusal", err)
	}
}
