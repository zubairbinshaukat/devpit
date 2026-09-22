package clean

import (
	"testing"

	cleanengine "github.com/zubairbinshaukat/devpit/internal/clean"
	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// The scanner's tiers and the delete engine's are separate enums. This screen
// is where a scanned item becomes a thing to delete, so this is where a
// mismatch would turn into a permanently deleted Careful item. Every named
// tier maps to its counterpart, and anything else maps to Careful, which is
// the tier that goes to the Recycle Bin rather than straight out.
func TestTierMappingIsExhaustiveAndSafeByDefault(t *testing.T) {
	t.Parallel()

	for from, want := range map[scan.Tier]cleanengine.Tier{
		scan.TierSafe:    cleanengine.TierSafe,
		scan.TierReview:  cleanengine.TierReview,
		scan.TierCareful: cleanengine.TierCareful,
	} {
		if got := toCleanTier(from); got != want {
			t.Errorf("toCleanTier(%v) = %v, want %v", from, got, want)
		}
	}

	// Every tier the scanner could grow, and a few values it never will.
	for _, n := range []int{-1, 3, 4, 99} {
		if got := toCleanTier(scan.Tier(n)); got != cleanengine.TierCareful {
			t.Errorf("toCleanTier(%d) = %v, want Careful: an unknown tier must take the safest path", n, got)
		}
	}

	// "default" has to mean "a tier the scanner grew since this was written",
	// not "a tier this switch forgot". Careful is the scanner's last tier
	// today: the moment it gains another, that tier stops printing "Unknown"
	// and this fails, which is the reminder to name it above.
	if scan.Tier(int(scan.TierCareful)+1).String() != "Unknown" {
		t.Fatalf("the scanner grew a tier past %v; toCleanTier must name it", scan.TierCareful)
	}
}
