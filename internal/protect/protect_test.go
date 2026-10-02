package protect_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/protect"
)

// fakeEnv is a LookupEnv over a map, so no test reads or changes the real
// environment.
func fakeEnv(vars map[string]string) protect.LookupEnv {
	return func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}
}

// tempHome returns a fake profile and roaming folder under a fresh temporary
// directory, and the env that names them.
func tempHome(t *testing.T) (home, appData string, env protect.LookupEnv) {
	t.Helper()
	home = filepath.Join(t.TempDir(), "me")
	appData = filepath.Join(home, "AppData", "Roaming")
	return home, appData, fakeEnv(map[string]string{"USERPROFILE": home, "APPDATA": appData})
}

// Every built-in location is protected, along with what sits inside it, and
// each reason names what lives there.
func TestDefaultCoversEveryLoginFolder(t *testing.T) {
	t.Parallel()
	home, appData, env := tempHome(t)
	l := protect.Default(env)

	for _, p := range []string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".claude", "local", "node_modules"),
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".claude.lock"),
		filepath.Join(home, ".claude.json.lock"),
		filepath.Join(home, ".devpit", "accounts", "claude", "work"),
		filepath.Join(home, ".claude-switch"),
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".convex", "config.json"),
		filepath.Join(home, ".supabase", "access-token"),
		filepath.Join(home, ".wrangler", "config", "default.toml"),
		filepath.Join(home, ".config", "configstore", "firebase-tools.json"),
		filepath.Join(appData, "GitHub CLI", "hosts.yml"),
		filepath.Join(appData, "xdg.data", "com.vercel.cli", "auth.json"),
		filepath.Join(appData, "xdg.config", ".wrangler", "config", "default.toml"),
		filepath.Join(appData, "Anthropic", "claude"),
		filepath.Join(appData, "Claude", "config.json"),
		filepath.Join(appData, "devpit", "config.toml"),
		filepath.Join(home, ".devpit", "git", "devpit.json"),
	} {
		ok, why := l.Inside(p)
		if !ok {
			t.Errorf("Inside(%s) = false, want true", p)
			continue
		}
		if !strings.Contains(why, "holds ") {
			t.Errorf("Inside(%s) reason %q does not say what lives there", p, why)
		}
		if ok, _ := l.Covers(p); !ok {
			t.Errorf("Covers(%s) = false, want true", p)
		}
	}
}

// Neighbours that share a name prefix, and ordinary caches next door, are not
// protected: the comparison is per component.
func TestDefaultLeavesNeighboursAlone(t *testing.T) {
	t.Parallel()
	home, appData, env := tempHome(t)
	l := protect.Default(env)

	for _, p := range []string{
		filepath.Join(home, ".claude-backup"),
		filepath.Join(home, ".claudex"),
		filepath.Join(home, ".sshd"),
		filepath.Join(home, ".config", "other"),
		filepath.Join(home, "work", "api", "node_modules"),
		filepath.Join(appData, "Code", "Cache"),
		filepath.Join(appData, "npm-cache"),
	} {
		if ok, why := l.Covers(p); ok {
			t.Errorf("Covers(%s) = true (%s), want false", p, why)
		}
	}
}

// Inside and WouldRemove are the two directions. A parent of a protected
// folder is not inside it, but deleting the parent would remove it.
func TestInsideAndWouldRemoveAreTheTwoDirections(t *testing.T) {
	t.Parallel()
	home, appData, env := tempHome(t)
	l := protect.Default(env)

	for _, parent := range []string{home, filepath.Dir(appData), appData, filepath.Join(home, ".config")} {
		if ok, why := l.Inside(parent); ok {
			t.Errorf("Inside(%s) = true (%s), want false", parent, why)
		}
		ok, why := l.WouldRemove(parent)
		if !ok {
			t.Errorf("WouldRemove(%s) = false, want true", parent)
		}
		if !strings.Contains(why, "contains ") {
			t.Errorf("WouldRemove(%s) reason %q does not name what it contains", parent, why)
		}
		if ok, _ := l.Covers(parent); !ok {
			t.Errorf("Covers(%s) = false, want true", parent)
		}
	}

	inside := filepath.Join(home, ".ssh", "id_ed25519")
	if ok, _ := l.WouldRemove(inside); ok {
		t.Errorf("WouldRemove(%s) = true: deleting a file inside .ssh does not remove .ssh", inside)
	}
	if ok, _ := l.WouldRemove(filepath.Join(home, ".ssh")); !ok {
		t.Error("WouldRemove(.ssh) = false: the folder itself is protected")
	}
}

// The comparison ignores case, separator style, trailing separators and
// dot segments.
func TestMatchingIgnoresSpelling(t *testing.T) {
	t.Parallel()
	home, _, env := tempHome(t)
	l := protect.Default(env)
	ssh := filepath.Join(home, ".ssh")

	for _, p := range []string{
		strings.ToUpper(ssh),
		ssh + string(os.PathSeparator),
		ssh + string(os.PathSeparator) + string(os.PathSeparator),
		filepath.ToSlash(ssh) + "/id_rsa",
		filepath.Join(home, "work", "..", ".ssh", "id_rsa"),
		filepath.Join(home, ".", ".ssh"),
	} {
		if ok, _ := l.Inside(p); !ok {
			t.Errorf("Inside(%q) = false, want true", p)
		}
	}
}

// A relative path is resolved against the working directory before it is
// compared, both in a query and in an extra root.
func TestRelativePathsAreMadeAbsolute(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	l := protect.List{}.With("acct-folder")
	if ok, _ := l.Inside(filepath.Join(wd, "acct-folder", "node_modules")); !ok {
		t.Error("a relative extra root was not resolved against the working directory")
	}
	l = protect.List{}.With(filepath.Join(wd, "acct-folder"))
	if ok, _ := l.Inside(filepath.Join("acct-folder", "x")); !ok {
		t.Error("a relative query was not resolved against the working directory")
	}
}

// An unset, empty, blank or relative variable drops every rule built on it.
// It must never become a rule on `\.claude` or `.claude` relative to the
// working directory, a drive root, or an empty string that matches all.
func TestUnsetVariablesNeverWidenARule(t *testing.T) {
	t.Parallel()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for name, vars := range map[string]map[string]string{
		"nothing set":   {},
		"empty":         {"USERPROFILE": "", "APPDATA": "", "CLAUDE_CONFIG_DIR": ""},
		"blank":         {"USERPROFILE": "   ", "APPDATA": "\t", "CLAUDE_CONFIG_DIR": " "},
		"relative":      {"USERPROFILE": "me", "APPDATA": filepath.Join("me", "AppData"), "CLAUDE_CONFIG_DIR": ".claude"},
		"dot":           {"USERPROFILE": ".", "APPDATA": "..", "CLAUDE_CONFIG_DIR": "."},
		"rooted folder": {"CLAUDE_CONFIG_DIR": string(os.PathSeparator)},
	} {
		l := protect.Default(fakeEnv(vars))
		if l.Len() != 0 {
			t.Errorf("%s: Default built %d entries, want none: %+v", name, l.Len(), l.Entries())
		}
		for _, p := range []string{
			filepath.Join(wd, ".claude"),
			filepath.Join(wd, "me", ".ssh"),
			string(os.PathSeparator) + ".claude",
			wd,
			"",
		} {
			if ok, why := l.Covers(p); ok {
				t.Errorf("%s: Covers(%q) = true (%s), want false", name, p, why)
			}
		}
	}
}

// A variable that is set never produces a drive root or a relative entry,
// and an empty query matches nothing.
func TestEntriesAreAbsoluteAndNeverARoot(t *testing.T) {
	t.Parallel()
	_, _, env := tempHome(t)
	l := protect.Default(env)
	if l.Len() == 0 {
		t.Fatal("Default built no entries from a full environment")
	}
	for _, e := range l.Entries() {
		if !filepath.IsAbs(e.Path) {
			t.Errorf("entry %q is not absolute", e.Path)
		}
		if filepath.Dir(e.Path) == e.Path {
			t.Errorf("entry %q is a root", e.Path)
		}
		if e.What == "" {
			t.Errorf("entry %q says nothing about what lives there", e.Path)
		}
	}
	for _, p := range []string{"", " ", "\t"} {
		if ok, _ := l.Covers(p); ok {
			t.Errorf("Covers(%q) = true, want false", p)
		}
	}
}

// CLAUDE_CONFIG_DIR names a second Claude folder; it is protected, and so is
// its lock.
func TestClaudeConfigDirIsProtected(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "claude-work")
	l := protect.Default(fakeEnv(map[string]string{"CLAUDE_CONFIG_DIR": dir}))
	for _, p := range []string{dir, filepath.Join(dir, "projects"), dir + ".lock"} {
		if ok, _ := l.Inside(p); !ok {
			t.Errorf("Inside(%s) = false, want true", p)
		}
	}
}

// With adds account folders at runtime, says what they are, and never
// changes the list it was called on.
func TestWithAddsRootsWithoutChangingTheOriginal(t *testing.T) {
	t.Parallel()
	_, _, env := tempHome(t)
	base := protect.Default(env)
	acct := filepath.Join(t.TempDir(), "imported", "claude-work")

	more := base.With(acct, "", "  ")
	if more.Len() != base.Len()+1 {
		t.Fatalf("With added %d entries, want 1 (blanks are ignored)", more.Len()-base.Len())
	}
	ok, why := more.Inside(filepath.Join(acct, "local", "node_modules"))
	if !ok {
		t.Fatal("a registered account root did not protect what is inside it")
	}
	if !strings.Contains(why, protect.AccountFolder) {
		t.Errorf("reason %q does not say it is an account folder", why)
	}
	if ok, _ := base.Inside(acct); ok {
		t.Error("With changed the list it was called on")
	}

	named := base.WithReason("the work Claude account", acct)
	if _, why := named.Inside(acct); !strings.Contains(why, "the work Claude account") {
		t.Errorf("WithReason's words are missing from %q", why)
	}

	u := protect.List{}.Union(more)
	if ok, _ := u.Inside(acct); !ok {
		t.Error("Union lost an entry")
	}
	if u.Union(more).Len() != more.Len() {
		t.Error("Union duplicated entries it already had")
	}
}

// The zero List protects nothing and never panics.
func TestZeroListProtectsNothing(t *testing.T) {
	t.Parallel()
	var l protect.List
	for _, p := range []string{"", "x", string(os.PathSeparator)} {
		if ok, _ := l.Covers(p); ok {
			t.Errorf("zero List Covers(%q) = true", p)
		}
	}
	if protect.Default(nil).Len() != 0 {
		t.Error("Default(nil) built entries")
	}
}
