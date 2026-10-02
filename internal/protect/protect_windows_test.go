package protect_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/zubairbinshaukat/devpit/internal/protect"
)

// winList is the built-in list for a profile that does not exist on this
// machine, so nothing below resolves through the real disk.
func winList() protect.List {
	return protect.Default(fakeEnv(map[string]string{
		"USERPROFILE": `Q:\Users\me`,
		"APPDATA":     `Q:\Users\me\AppData\Roaming`,
	}))
}

// The matcher's edge cases, as Windows spells them.
func TestMatcherEdgeCases(t *testing.T) {
	t.Parallel()
	l := winList()

	cases := []struct {
		path        string
		inside      bool
		wouldRemove bool
	}{
		// The folder itself, in every spelling.
		{`Q:\Users\me\.claude`, true, true},
		{`q:\users\ME\.CLAUDE`, true, true},
		{`Q:\Users\me\.claude\`, true, true},
		{`Q:/Users/me/.claude/local/node_modules`, true, false},
		{`Q:\Users\me\work\..\.claude\local`, true, false},
		{`\\?\Q:\Users\me\.claude\local\node_modules`, true, false},
		{`//?/Q:/Users/me/.ssh`, true, true},
		{`\\.\Q:\Users\me\.ssh\id_rsa`, true, false},
		{`\??\Q:\Users\me\.ssh`, true, true},
		// Win32 drops trailing dots and spaces, so these open the real folder.
		{`Q:\Users\me\.ssh.`, true, true},
		{`Q:\Users\me\.ssh \id_rsa`, true, false},
		// Files and lock siblings.
		{`Q:\Users\me\.claude.json`, true, true},
		{`Q:\Users\me\.claude.json.lock`, true, false},
		{`Q:\Users\me\.claude.lock`, true, false},
		{`Q:\Users\me\.claude.lock\owner`, true, false},
		// Roaming tool folders, including the ones with spaces and dots.
		{`Q:\Users\me\AppData\Roaming\GitHub CLI\hosts.yml`, true, false},
		{`Q:\Users\me\AppData\Roaming\xdg.data\com.vercel.cli`, true, true},
		{`Q:\Users\me\AppData\Roaming\xdg.config\.wrangler\config`, true, false},
		{`Q:\Users\me\.config\configstore\firebase-tools.json`, true, false},
		// Parents: never inside, always would remove.
		{`Q:\Users\me`, false, true},
		{`Q:\Users`, false, true},
		{`Q:\`, false, true},
		{`Q:`, false, true},
		{`\\?\Q:\`, false, true},
		{`Q:\Users\me\AppData`, false, true},
		{`Q:\Users\me\.config`, false, true},
		// Neighbours.
		{`Q:\Users\me\.claude-backup`, false, false},
		{`Q:\Users\me\.claudes\x`, false, false},
		{`Q:\Users\me\.ssh2`, false, false},
		{`Q:\Users\me\.config\gh`, false, false},
		{`Q:\Users\me\AppData\Roaming\GitHub Desktop`, false, false},
		{`Q:\Users\me\AppData\Roaming\xdg.data\some.app`, false, false},
		{`Q:\Users\someone\.ssh`, false, false},
		{`D:\work\api\node_modules`, false, false},
		{`\\?\UNC\server\share\.ssh`, false, false},
		// Nothing at all.
		{``, false, false},
		{`   `, false, false},
	}
	for _, tc := range cases {
		in, why := l.Inside(tc.path)
		if in != tc.inside {
			t.Errorf("Inside(%q) = %v (%s), want %v", tc.path, in, why, tc.inside)
		}
		wr, why := l.WouldRemove(tc.path)
		if wr != tc.wouldRemove {
			t.Errorf("WouldRemove(%q) = %v (%s), want %v", tc.path, wr, why, tc.wouldRemove)
		}
		if cov, _ := l.Covers(tc.path); cov != (tc.inside || tc.wouldRemove) {
			t.Errorf("Covers(%q) = %v, want %v", tc.path, cov, tc.inside || tc.wouldRemove)
		}
	}
}

// Extra roots are matched with the same rules, including a UNC root and a
// `\\?\` spelling of the root itself.
func TestExtraRootsUseTheSameMatcher(t *testing.T) {
	t.Parallel()
	l := protect.List{}.With(`\\?\Q:\accounts\work\`, `\\server\share\acct`)
	for _, p := range []string{`q:\ACCOUNTS\work\.credentials.json`, `\\SERVER\share\acct\x`, `\\?\UNC\server\share\acct`} {
		if ok, _ := l.Inside(p); !ok {
			t.Errorf("Inside(%q) = false, want true", p)
		}
	}
	if ok, _ := l.WouldRemove(`\\server\share`); !ok {
		t.Error("WouldRemove of the share root that holds an account = false")
	}
}

// A drive-relative or rooted-without-drive variable is not absolute, so it is
// dropped rather than resolved against the current drive or directory.
func TestRootedButNotAbsoluteVariablesAreDropped(t *testing.T) {
	t.Parallel()
	for _, v := range []string{`\Users\me`, `C:Users\me`, `C:`, `C:\`, `\\?\C:\`} {
		l := protect.Default(fakeEnv(map[string]string{"USERPROFILE": v, "CLAUDE_CONFIG_DIR": v, "APPDATA": v}))
		for _, e := range l.Entries() {
			if filepath.Dir(e.Path) == e.Path {
				t.Errorf("USERPROFILE=%q produced a root entry %q", v, e.Path)
			}
			if !filepath.IsAbs(e.Path) {
				t.Errorf("USERPROFILE=%q produced a relative entry %q", v, e.Path)
			}
		}
		if ok, why := l.Covers(`D:\work\api\node_modules`); ok {
			t.Errorf("USERPROFILE=%q: an unrelated path is covered (%s)", v, why)
		}
	}
}

// An 8.3 short name is a second spelling of the same folder, on either side
// of the comparison.
func TestShortNamesAreExpanded(t *testing.T) {
	t.Parallel()
	long := filepath.Join(t.TempDir(), "averyveryverylongaccountfolder")
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}
	short := shortName(t, long)

	if ok, _ := (protect.List{}).With(long).Inside(filepath.Join(short, "node_modules")); !ok {
		t.Errorf("the list names %s, and its short name %s got through", long, short)
	}
	if ok, _ := (protect.List{}).With(short).Inside(filepath.Join(long, "node_modules")); !ok {
		t.Errorf("the list names %s, and its long name %s got through", short, long)
	}
	// A child that does not exist yet still matches under a short parent.
	if ok, _ := (protect.List{}).With(filepath.Join(long, "not-yet")).Inside(filepath.Join(short, "not-yet", "x")); !ok {
		t.Error("a missing folder under a short-named parent was not matched")
	}
}

// A protected folder that is a junction to somewhere else protects the place
// it points at, which is how claude-acc links an account's folders. Scanning
// the target's drive must not find junk inside it.
func TestAJunctionedFolderProtectsItsTarget(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	home := filepath.Join(base, "me")
	target := filepath.Join(base, "elsewhere", "claude-data")
	if err := os.MkdirAll(filepath.Join(target, "local"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".claude")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("mklink /J is unavailable here (%v): %s", err, out)
	}

	l := protect.Default(fakeEnv(map[string]string{"USERPROFILE": home}))
	if ok, _ := l.Inside(filepath.Join(target, "local", "node_modules")); !ok {
		t.Error("the junction's target is not protected")
	}
	if ok, _ := l.WouldRemove(filepath.Dir(target)); !ok {
		t.Error("deleting the folder holding the junction's target is not refused")
	}
	if ok, _ := l.Inside(filepath.Join(link, "local")); !ok {
		t.Error("the junction itself is not protected")
	}
}

// shortName returns the 8.3 name of an existing path, or skips when the
// volume does not make them.
func shortName(t *testing.T, long string) string {
	t.Helper()
	in, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	const size = windows.MAX_PATH
	buf := make([]uint16, size)
	n, err := windows.GetShortPathName(in, &buf[0], size)
	if err != nil || n == 0 {
		t.Skipf("GetShortPathName: %v", err)
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, long) || !strings.Contains(short, "~") {
		t.Skip("this volume does not create 8.3 short names")
	}
	return short
}
