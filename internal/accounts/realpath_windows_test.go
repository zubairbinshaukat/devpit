//go:build windows

package accounts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestARuleFollowsARealJunction pins the junction case end to end: a rule on
// the real folder applies when the person works in a junction to it, and
// the resolution says it matched through the resolved path. The junction is
// made with mklink /J, which needs no administrator rights.
func TestARuleFollowsARealJunction(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "RealProject")
	link := filepath.Join(root, "linked")
	must(t, os.MkdirAll(filepath.Join(realDir, "src"), 0o700))
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, realDir).CombinedOutput(); err != nil { //nolint:gosec // test
		t.Skipf("mklink /J: %v %s", err, out)
	}

	got, err := RealPath(filepath.Join(link, "src", "not-yet"))
	must(t, err)
	want, _ := RealPath(realDir)
	if !strings.EqualFold(got, want+`\src\not-yet`) {
		t.Fatalf("RealPath = %q, want %q", got, want+`\src\not-yet`)
	}

	s := NewStore()
	must(t, s.AddAccount(Account{Tool: ToolClaude, Name: "work", Dir: `C:\a\w`}))
	must(t, s.SetRule(want, ToolClaude, "work"))
	r, err := Resolve(s, ToolClaude, filepath.Join(link, "src"), ResolveOptions{RealPath: RealPath})
	must(t, err)
	if r.Account.Name != "work" || r.Via != ViaResolved || !strings.Contains(r.Why(), "through") {
		t.Fatalf("got %+v (%s)", r, r.Why())
	}
	// Without the second look, the junction path matches nothing.
	r, _ = Resolve(s, ToolClaude, filepath.Join(link, "src"), ResolveOptions{})
	if !r.IsDefault() {
		t.Fatalf("got %v", r)
	}
}

func TestRealPathOfAMissingDriveIsTheInput(t *testing.T) {
	got, err := RealPath(`Q:\nowhere\at\all`)
	must(t, err)
	if got != `Q:\nowhere\at\all` {
		t.Fatalf("got %q", got)
	}
}
