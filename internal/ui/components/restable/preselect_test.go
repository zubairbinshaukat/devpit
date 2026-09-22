package restable

import (
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/scan"
)

// safeItem is an ordinary Safe find: the one thing preselect is allowed to
// tick. Every test below starts from it and changes one field.
func safeItem() scan.Item {
	return scan.Item{
		Path:        `D:\work\api\node_modules`,
		Name:        "node_modules",
		Project:     `D:\work\api`,
		Size:        1_200_000_000,
		Tier:        scan.TierSafe,
		Kind:        scan.KindProjectJunk,
		Rule:        "node_modules",
		RestoreHint: "npm install brings it back",
		LastUsed:    time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
}

// TestPreselectTicksPlainSafeItems is the control: without it the three
// tests below would pass on a preselect that ticks nothing at all.
func TestPreselectTicksPlainSafeItems(t *testing.T) {
	marks := preselect([]scan.Item{safeItem()})
	if len(marks) != 1 || !marks[0] {
		t.Fatalf("a plain Safe item was not pre-ticked: %v", marks)
	}
}

// TestPreselectNeverTicksCareful is safety rule 4: a Careful item is never
// pre-ticked, whatever else is true about it.
func TestPreselectNeverTicksCareful(t *testing.T) {
	cases := map[string]scan.Item{
		"plain careful": func() scan.Item {
			it := safeItem()
			it.Tier = scan.TierCareful
			return it
		}(),
		"careful and idle": func() scan.Item {
			it := safeItem()
			it.Tier = scan.TierCareful
			it.LastUsed = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			return it
		}(),
		"careful and verified": func() scan.Item {
			it := safeItem()
			it.Tier = scan.TierCareful
			it.Markers = []string{"package.json"}
			return it
		}(),
	}
	for name, it := range cases {
		if marks := preselect([]scan.Item{it}); marks[0] {
			t.Errorf("%s: a Careful item was pre-ticked", name)
		}
	}
}

// TestPreselectNeverTicksActive is safety rule 5: a project touched inside
// the active window is never pre-ticked, even when the find is Safe.
func TestPreselectNeverTicksActive(t *testing.T) {
	it := safeItem()
	it.Active = true
	it.LastUsed = time.Now()

	if marks := preselect([]scan.Item{it}); marks[0] {
		t.Error("an active project was pre-ticked")
	}
}

// TestPreselectNeverTicksUnverified covers the node_modules-without-a-
// package.json case: it is listed, because it is almost certainly junk, and
// it is never pre-ticked, because "almost certainly" is not the standard.
func TestPreselectNeverTicksUnverified(t *testing.T) {
	it := safeItem()
	it.Unverified = true

	if marks := preselect([]scan.Item{it}); marks[0] {
		t.Error("an unverified match was pre-ticked")
	}
}

// TestPreselectNeverTicksCloud keeps Devpit from ticking something whose
// bytes are not even on this machine; reading one would download it.
func TestPreselectNeverTicksCloud(t *testing.T) {
	it := safeItem()
	it.Cloud = true
	it.Size = 0

	if marks := preselect([]scan.Item{it}); marks[0] {
		t.Error("a cloud placeholder was pre-ticked")
	}
}

// TestPreselectDoesNotTickReview keeps the Review tier a deliberate choice:
// it is recoverable, but rebuilding it costs time or bandwidth.
func TestPreselectDoesNotTickReview(t *testing.T) {
	it := safeItem()
	it.Tier = scan.TierReview

	if marks := preselect([]scan.Item{it}); marks[0] {
		t.Error("a Review item was pre-ticked")
	}
}

// TestSetItemsAndAppendPreserveTheRule proves the rule holds on both ways
// items reach the table, including the incremental one a live scan uses.
func TestSetItemsAndAppendPreserveTheRule(t *testing.T) {
	careful := safeItem()
	careful.Tier = scan.TierCareful
	careful.Path = `D:\work\api\.next`
	careful.Name = ".next"

	m := New().SetItems([]scan.Item{safeItem()})
	if n, _ := m.SelectedCount(); n != 1 {
		t.Fatalf("SetItems ticked %d items, want 1", n)
	}

	m = m.Append([]scan.Item{careful})
	n, _ := m.SelectedCount()
	if n != 1 {
		t.Fatalf("Append ticked a Careful item: %d selected, want 1", n)
	}
	for _, it := range m.Selected() {
		if it.Tier == scan.TierCareful {
			t.Fatal("a Careful item came back from Selected after Append")
		}
	}
}
