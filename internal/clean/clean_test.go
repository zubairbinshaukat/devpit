package clean

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

func TestTierStringNamesTheThreeTiers(t *testing.T) {
	t.Parallel()
	for tier, want := range map[Tier]string{TierSafe: "Safe", TierReview: "Review", TierCareful: "Careful"} {
		if got := tier.String(); got != want {
			t.Errorf("Tier(%d).String() = %q, want %q", int(tier), got, want)
		}
	}
	if got := Tier(9).String(); got != "Tier(9)" {
		t.Errorf("unknown tier = %q", got)
	}
}

func TestTierNumberingMirrorsTheScanner(t *testing.T) {
	t.Parallel()
	if TierSafe != 0 || TierReview != 1 || TierCareful != 2 {
		t.Fatalf("tier numbering drifted: %d/%d/%d", TierSafe, TierReview, TierCareful)
	}
}

// Safety rule 13: --volumes is never passed. The refusal happens before the
// command is started, so a command that does not exist is still refused rather
// than reported as missing.
func TestRunCommandRefusesVolumes(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{"system", "prune", "--volumes"},
		{"system", "prune", "-f", "--volumes"},
		{"system", "prune", "--volumes=true"},
	}
	for _, args := range cases {
		out, reclaimed, err := RunCommand(context.Background(), "docker", args...)
		if !errors.Is(err, ErrVolumesRefused) {
			t.Errorf("RunCommand(%v) err = %v, want ErrVolumesRefused", args, err)
		}
		if out != "" || reclaimed != 0 {
			t.Errorf("RunCommand(%v) ran anyway: out=%q reclaimed=%d", args, out, reclaimed)
		}
	}
}

// Rule 13 again, in the spellings an exact-match check on "--volumes" would
// wave straight through. The scanner's rule table forbids all three; so does
// this, independently, on the way to running anything.
func TestRunCommandRefusesShortVolumesFlag(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{"system", "prune", "-v"},
		{"system", "prune", "-f", "-v"},
		{"system", "prune", "--all-volumes"},
		{"system", "prune", "-V"},
	}
	for _, args := range cases {
		out, reclaimed, err := RunCommand(context.Background(), "docker", args...)
		if !errors.Is(err, ErrVolumesRefused) {
			t.Errorf("RunCommand(%v) err = %v, want ErrVolumesRefused", args, err)
		}
		if out != "" || reclaimed != 0 {
			t.Errorf("RunCommand(%v) ran anyway: out=%q reclaimed=%d", args, out, reclaimed)
		}
	}
}

// RunScanCommand runs a step through the step's own ForbiddenArgs, which is
// the rule-table half of rule 13.
func TestRunScanCommandRefusesAStepsOwnForbiddenArgs(t *testing.T) {
	t.Parallel()
	cmd := scan.Command{
		Name:          "Docker: prune",
		Exe:           "devpit-no-such-command",
		Args:          []string{"system", "prune", "--volumes"},
		ForbiddenArgs: []string{"--volumes"},
		RestoreHint:   "Rebuild the images.",
	}
	if _, _, err := RunScanCommand(context.Background(), cmd); !errors.Is(err, ErrVolumesRefused) {
		t.Fatalf("err = %v, want ErrVolumesRefused", err)
	}
	cmd.Args = []string{"system", "prune", "-f"}
	if _, _, err := RunScanCommand(context.Background(), cmd); errors.Is(err, ErrVolumesRefused) {
		t.Fatalf("a prune without volumes was refused: %v", err)
	}
}

// The two Tier enums are separate types with no compiler link between them.
// Everything that converts one to the other assumes they agree, so this is
// the pin: if the scanner ever renumbers, this test fails before a Careful
// item can be deleted as if it were Safe.
func TestScanTiersMatchCleanTiers(t *testing.T) {
	t.Parallel()
	pairs := []struct {
		scanTier  scan.Tier
		cleanTier Tier
	}{
		{scan.TierSafe, TierSafe},
		{scan.TierReview, TierReview},
		{scan.TierCareful, TierCareful},
	}
	for _, p := range pairs {
		if int(p.scanTier) != int(p.cleanTier) {
			t.Errorf("scan.%v = %d but clean.%v = %d", p.scanTier, int(p.scanTier), p.cleanTier, int(p.cleanTier))
		}
		if p.scanTier.String() != p.cleanTier.String() {
			t.Errorf("scan tier %d is %q and clean tier %d is %q", int(p.scanTier), p.scanTier, int(p.cleanTier), p.cleanTier)
		}
	}
	if int(scan.TierCareful) != 2 {
		t.Fatalf("the scanner has %d tiers' worth of numbering; this pin needs updating", int(scan.TierCareful))
	}
}

func TestRunCommandAllowsAPruneWithoutVolumes(t *testing.T) {
	t.Parallel()
	// A command that certainly does not exist proves the refusal did not fire:
	// the error is about the missing binary, not about --volumes.
	_, _, err := RunCommand(context.Background(), "devpit-no-such-command", "system", "prune", "-f")
	if errors.Is(err, ErrVolumesRefused) {
		t.Fatalf("a prune without --volumes was refused: %v", err)
	}
	if err == nil {
		t.Fatal("expected the missing binary to fail")
	}
}

func TestRunCommandRefusesAnEmptyName(t *testing.T) {
	t.Parallel()
	if _, _, err := RunCommand(context.Background(), "  "); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("err = %v, want ErrEmptyPath", err)
	}
}

func TestParseReclaimedReadsTheToolsOutput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		out  string
		want uint64
	}{
		{"Total reclaimed space: 1.216GB", 1_216_000_000},
		{"Total reclaimed space: 0B", 0},
		{"deleted: sha256:abc\n\nTotal reclaimed space: 512MB\n", 512_000_000},
		{"Reclaimed 340 MB of disk space", 340_000_000},
		{"reclaimed: 2 GiB", 2 * 1024 * 1024 * 1024},
		{"nothing to say here", 0},
		{"Total reclaimed space: 1.5GB\nTotal reclaimed space: 2.5GB\n", 2_500_000_000},
	}
	for _, c := range cases {
		if got := ParseReclaimed(c.out); got != c.want {
			t.Errorf("ParseReclaimed(%q) = %d, want %d", c.out, got, c.want)
		}
	}
}

func TestIsTombstoneMatchesOnlyDevpitNames(t *testing.T) {
	t.Parallel()
	yes := []string{
		"node_modules.devpit-ab12cd34",
		`D:\work\api\target.devpit-00000000`,
		"weird name.devpit-zzzzzzzz",
	}
	no := []string{
		"node_modules",
		"node_modules.devpit-",
		"node_modules.devpit-short",
		"node_modules.devpit-TOOLONGXX1",
		"node_modules.devpit-AB12CD34", // uppercase is not in the alphabet
		"devpit-ab12cd34",
	}
	for _, n := range yes {
		if !IsTombstone(n) {
			t.Errorf("IsTombstone(%q) = false, want true", n)
		}
	}
	for _, n := range no {
		if IsTombstone(n) {
			t.Errorf("IsTombstone(%q) = true, want false", n)
		}
	}
}

func TestTombstoneNamesAreUnique(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	seen := make(map[string]struct{})
	for i := 0; i < 32; i++ {
		src := filepath.Join(dir, "src")
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		tomb, err := tombstone(src)
		if err != nil {
			t.Fatal(err)
		}
		if tomb.MarkerErr != nil {
			t.Fatalf("tombstone() could not write its marker: %v", tomb.MarkerErr)
		}
		if !IsTombstone(tomb.Path) {
			t.Fatalf("tombstone() produced %q, which IsTombstone rejects", tomb.Path)
		}
		if _, dup := seen[tomb.Path]; dup {
			t.Fatalf("tombstone() repeated %q", tomb.Path)
		}
		seen[tomb.Path] = struct{}{}
	}
}

func TestWithinIsComponentAwareAndCaseInsensitive(t *testing.T) {
	t.Parallel()
	sep := string(os.PathSeparator)
	root := filepath.Join("X:"+sep, "work")
	cases := []struct {
		child, parent string
		want          bool
	}{
		{root, root, true},
		{filepath.Join(root, "api"), root, true},
		{filepath.Join(strings.ToUpper(root), "API"), root, true},
		{filepath.Join("X:"+sep, "workbench"), root, false},
		{root, filepath.Join(root, "api"), false},
		{root, "", false},
	}
	for _, c := range cases {
		if got := within(c.child, c.parent); got != c.want {
			t.Errorf("within(%q, %q) = %v, want %v", c.child, c.parent, got, c.want)
		}
	}
}

func TestPreflightRefusesAnEmptyPath(t *testing.T) {
	t.Parallel()
	if err := Preflight(Item{}, Options{}); !errors.Is(err, ErrEmptyPath) {
		t.Fatalf("err = %v, want ErrEmptyPath", err)
	}
}

// Safety rule 6, second enforcement point: the pre-flight refuses the
// never-touch list independently of the scanner's root filter, in both
// directions.
func TestPreflightRefusesTheNeverTouchList(t *testing.T) {
	t.Parallel()
	guarded := t.TempDir()
	inside := filepath.Join(guarded, "node_modules")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	opts := Options{NeverTouch: []string{strings.ToUpper(guarded)}}

	if err := Preflight(Item{Path: inside}, opts); !errors.Is(err, ErrNeverTouch) {
		t.Errorf("a path inside a protected directory: err = %v, want ErrNeverTouch", err)
	}
	if err := Preflight(Item{Path: guarded}, opts); !errors.Is(err, ErrNeverTouch) {
		t.Errorf("the protected directory itself: err = %v, want ErrNeverTouch", err)
	}
	if err := Preflight(Item{Path: filepath.Dir(guarded)}, opts); !errors.Is(err, ErrNeverTouch) {
		t.Errorf("a parent that would take the protected directory with it: err = %v, want ErrNeverTouch", err)
	}
}

// Safety rule 7: Devpit's own executable is always on the never-touch list,
// even when the user's list is empty.
func TestPreflightRefusesDevpitsOwnExecutable(t *testing.T) {
	t.Parallel()
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable: %v", err)
	}
	for _, p := range []string{exe, filepath.Dir(exe), filepath.Dir(filepath.Dir(exe))} {
		if err := Preflight(Item{Path: p}, Options{}); !errors.Is(err, ErrOwnExecutable) {
			t.Errorf("Preflight(%q) err = %v, want ErrOwnExecutable", p, err)
		}
	}
}

// Safety rule 10: the rule is re-verified immediately before deleting.
func TestPreflightRefusesWhenVerifyFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("package.json is no longer beside it")
	it := Item{Path: target, Verify: func() error { return boom }}

	err := Preflight(it, Options{})
	if !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("err = %v, want ErrVerifyFailed", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to carry the verifier's own error", err)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatal("the pre-flight removed something")
	}
}

func TestPreflightAcceptsAPlainTempDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	verified := false
	it := Item{Path: target, Verify: func() error { verified = true; return nil }}
	if err := Preflight(it, Options{}); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !verified {
		t.Fatal("Verify was not called")
	}
}

func TestPreflightReportsAPathThatIsAlreadyGone(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "never-existed")
	err := Preflight(Item{Path: missing}, Options{})
	if !errors.Is(err, ErrGone) {
		t.Fatalf("err = %v, want ErrGone", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want it to wrap fs.ErrNotExist", err)
	}
}

// Safety rule 11, the UNC half, which is a string test and so runs everywhere.
func TestPreflightRefusesUNCPaths(t *testing.T) {
	t.Parallel()
	for _, p := range []string{`\\fileserver\builds\node_modules`, `\\fileserver\builds`} {
		if err := Preflight(Item{Path: p}, Options{}); !errors.Is(err, ErrNetworkPath) && !errors.Is(err, ErrDriveRoot) {
			t.Errorf("Preflight(%q) err = %v, want a network or root refusal", p, err)
		}
	}
}

func TestReasonsAreSentencesAPersonCanAct(t *testing.T) {
	t.Parallel()
	it := Item{Path: filepath.Join("D:", "work", "api", "node_modules")}
	locked := &LockedError{Path: it.Path, Holders: []Holder{{PID: 4242, Name: "Code.exe"}}}
	got := reasonFor(it, locked)
	for _, want := range []string{"api", "node_modules", "Code.exe", "press R to retry"} {
		if !strings.Contains(got, want) {
			t.Errorf("reason %q is missing %q", got, want)
		}
	}
	if got := reasonFor(it, &LockedError{Path: it.Path}); !strings.Contains(got, "another program") {
		t.Errorf("a lock with no named holder reads %q", got)
	}
	space := &InsufficientSpaceError{Path: it.Path, Need: 2 << 30, Free: 1 << 20}
	if got := reasonFor(it, space); !strings.Contains(got, "permanently") {
		t.Errorf("a full drive reads %q, and should suggest a permanent delete", got)
	}
}

func TestHumanBytes(t *testing.T) {
	t.Parallel()
	cases := map[uint64]string{
		0:               "0 B",
		999:             "999 B",
		1024:            "1.0 KB",
		1536:            "1.5 KB",
		1024 * 1024:     "1.0 MB",
		3 << 30:         "3.0 GB",
		5 * (1 << 40):   "5.0 TB",
		7 * (1 << 50):   "7.0 PB",
		1<<20 + 1<<19:   "1.5 MB",
		uint64(1) << 60: "1024.0 PB",
	}
	for n, want := range cases {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestOptionsWorkersDefaultsToTwelve(t *testing.T) {
	t.Parallel()
	if got := (Options{}).workers(); got != DefaultWorkers {
		t.Fatalf("workers() = %d, want %d", got, DefaultWorkers)
	}
	if got := (Options{Workers: 3}).workers(); got != 3 {
		t.Fatalf("workers() = %d, want 3", got)
	}
}
