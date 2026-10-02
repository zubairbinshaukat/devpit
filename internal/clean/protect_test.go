package clean

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/protect"
)

// homeEnv is a protect.LookupEnv naming home as the user profile, so a test
// protects a fake profile and never the real one.
func homeEnv(home string) protect.LookupEnv {
	return func(k string) (string, bool) {
		if k == "USERPROFILE" {
			return home, true
		}
		return "", false
	}
}

// plantJunk writes a junk-looking Node project at dir: a package.json and a
// node_modules beside it, which is exactly what the scanner's node_modules
// rule calls Safe. It returns the node_modules path.
func plantJunk(t *testing.T, dir string) string {
	t.Helper()
	nm := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(filepath.Join(nm, "dep"), 0o755); err != nil {
		t.Fatal(err)
	}
	for p, body := range map[string]string{
		filepath.Join(dir, "package.json"):         "{}",
		filepath.Join(nm, "dep", "index.js"):       "module.exports = 1",
		filepath.Join(filepath.Dir(dir), "canary"): "still here",
	} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return nm
}

// Safety rule 30, the delete half: the pre-flight refuses junk planted inside
// a login folder, the login folder itself, and the profile that holds it,
// with a reason that says what lives there. Run, dry or not, deletes none of
// it.
func TestPreflightRefusesLoginAndAccountFolders(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "me")
	nm := plantJunk(t, filepath.Join(home, ".claude", "local"))
	opts := Options{Protect: protect.Default(homeEnv(home))}

	for _, p := range []string{nm, filepath.Join(home, ".claude"), home} {
		err := Preflight(Item{Path: p}, opts)
		if !errors.Is(err, ErrProtected) {
			t.Errorf("Preflight(%s) = %v, want ErrProtected", p, err)
			continue
		}
		var pe *ProtectedError
		if !errors.As(err, &pe) || !strings.Contains(pe.Why, "Claude Code") {
			t.Errorf("Preflight(%s) = %v, want a ProtectedError naming Claude Code", p, err)
		}
	}

	for _, dry := range []bool{true, false} {
		o := opts
		o.DryRun = dry
		rep := Run(context.Background(), []Item{{Path: nm, Size: 10, Tier: TierSafe}}, o, nil)
		if len(rep.Deleted) != 0 || len(rep.Skipped) != 1 {
			t.Fatalf("dry=%v: deleted %d, skipped %d, want 0 and 1", dry, len(rep.Deleted), len(rep.Skipped))
		}
		reason := rep.Skipped[0].Reason
		for _, want := range []string{"Refused", "node_modules", "Claude Code", "never deletes login"} {
			if !strings.Contains(reason, want) {
				t.Errorf("dry=%v: reason %q is missing %q", dry, reason, want)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(nm, "dep", "index.js")); err != nil {
		t.Fatalf("something inside .claude was removed: %v", err)
	}
}

// An account root registered at runtime is refused the same way, wherever it
// lives, and so is a junk-looking folder that holds one.
func TestPreflightRefusesARegisteredAccountRoot(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	acct := filepath.Join(base, "imported", "claude-work")
	inside := plantJunk(t, acct)

	holder := filepath.Join(base, "proj", "node_modules")
	nested := filepath.Join(holder, "acct")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	opts := Options{Protect: protect.List{}.With(acct, nested)}
	for _, p := range []string{inside, acct, holder} {
		if err := Preflight(Item{Path: p}, opts); !errors.Is(err, ErrProtected) {
			t.Errorf("Preflight(%s) = %v, want ErrProtected", p, err)
		}
	}
	// A sibling that holds no account is still fine.
	other := plantJunk(t, filepath.Join(base, "web"))
	if err := Preflight(Item{Path: other}, opts); err != nil {
		t.Errorf("Preflight(%s) = %v, want nil", other, err)
	}
}

// No caller can switch the list off: with no Protect option at all, the
// built-in list is read from the environment the delete runs in.
func TestPreflightAppliesTheBuiltInListWithoutBeingAsked(t *testing.T) {
	home := filepath.Join(t.TempDir(), "me")
	nm := plantJunk(t, filepath.Join(home, ".ssh", "tools"))
	t.Setenv("USERPROFILE", home)

	if err := Preflight(Item{Path: nm}, Options{}); !errors.Is(err, ErrProtected) {
		t.Fatalf("Preflight(%s) = %v, want ErrProtected", nm, err)
	}
	if got := reasonFor(Item{Path: nm}, Preflight(Item{Path: nm}, Options{})); !strings.Contains(got, "SSH keys") {
		t.Errorf("reason %q does not say SSH keys live there", got)
	}
}
