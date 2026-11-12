//go:build windows

package extract_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/extract"
)

// Safety rule 82 on the real file system: a chosen parent that is a junction,
// or inside a login folder (rule 30), is refused; a plain folder gets a new
// <stem> folder.
func TestOutDirInRefusesJunctionsAndLoginFoldersOnDisk(t *testing.T) {
	fsys := extract.Real().FS
	root := t.TempDir()
	t.Setenv("USERPROFILE", root)
	set := request().Set

	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := extract.OutDirIn(plain, set, fsys); err != nil || got != filepath.Join(plain, "Game-Backup") {
		t.Fatalf("plain = %q, %v", got, err)
	}
	junction := filepath.Join(root, "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, plain).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, out)
	}
	if _, err := extract.OutDirIn(junction, set, fsys); !errors.Is(err, extract.ErrReparsePoint) {
		t.Errorf("a junction: %v", err)
	}
	ssh := filepath.Join(root, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := extract.OutDirIn(ssh, set, fsys); !errors.Is(err, extract.ErrProtected) {
		t.Errorf("inside .ssh: %v", err)
	}
}
