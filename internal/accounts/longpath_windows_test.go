//go:build windows

package accounts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// shortPathOf is p's 8.3 form, or p when the volume makes no short names.
func shortPathOf(t *testing.T, p string) string {
	t.Helper()
	in, err := windows.UTF16PtrFromString(p)
	must(t, err)
	buf := make([]uint16, 4*windows.MAX_PATH)
	n, err := windows.GetShortPathName(in, &buf[0], uint32(len(buf)))
	if err != nil || int(n) >= len(buf) {
		return p
	}
	return windows.UTF16ToString(buf[:n])
}

// LongFolderWithShortName makes a folder with a long name and returns its
// long and 8.3 paths, or skips when the volume makes no short names.
func longFolderWithShortName(t *testing.T) (long, short string) {
	t.Helper()
	long = filepath.Join(LongPath(t.TempDir()), "A folder with a long name")
	must(t, os.MkdirAll(long, 0o700))
	short = shortPathOf(t, long)
	if strings.EqualFold(short, long) {
		t.Skip("this volume makes no 8.3 short names (fsutil 8dot3name), so there is nothing to expand")
	}
	return long, short
}

// A folder reached through its 8.3 short name (GitHub's runners have TEMP
// under C:\Users\RUNNER~1) comes back under its long name, for a folder
// that does not exist yet too; a path without a short name is only
// normalized; junctions are not followed.
func TestLongPathExpandsShortNames(t *testing.T) {
	long, short := longFolderWithShortName(t)
	if got := LongPath(short); !strings.EqualFold(got, long) {
		t.Fatalf("LongPath(%s) = %s, want %s", short, got, long)
	}
	if got := LongPath(short + `\not\there yet`); !strings.EqualFold(got, long+`\not\there yet`) {
		t.Fatalf("a missing child: %s", got)
	}
	if got := LongPath(`c:/Work/api/`); got != `C:\Work\api` {
		t.Fatalf("no short name: %q", got)
	}
	if got := LongPath("not a path?"); got != "not a path?" {
		t.Fatalf("a bad path: %q", got)
	}

	// A rule made for the short name is kept under the long name, so it
	// matches a terminal in the long one, and the short one too.
	s := NewStore()
	must(t, s.AddAccount(Account{Tool: ToolClaude, Name: "work", Dir: `C:\a`}))
	after, err := Change{Tool: ToolClaude, Account: "work", Scope: FolderScope(short)}.ApplyTo(s)
	must(t, err)
	if len(after.Rules) != 1 || !strings.EqualFold(after.Rules[0].Folder, long) {
		t.Fatalf("rules = %+v", after.Rules)
	}
	for _, in := range []string{long, short, filepath.Join(short, "src")} {
		r, err := Resolve(after, ToolClaude, in, ResolveOptions{RealPath: RealPath})
		must(t, err)
		if r.Account.Name != "work" {
			t.Errorf("in %s: %s", in, r.Account.Name)
		}
	}
}
