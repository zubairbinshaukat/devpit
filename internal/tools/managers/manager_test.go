package managers_test

import (
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools"
	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
)

func TestAllListsEveryManagerOnce(t *testing.T) {
	all := managers.All()
	if len(all) != 4 {
		t.Fatalf("len(All()) = %d, want 4", len(all))
	}
	seen := make(map[string]bool)
	for _, m := range all {
		if seen[m.Name()] {
			t.Errorf("duplicate manager %q", m.Name())
		}
		seen[m.Name()] = true
	}
	for _, name := range []string{"scoop", "winget", "choco", "npm"} {
		if !seen[name] {
			t.Errorf("All() missing %q", name)
		}
	}
}

func TestPreferredOverrideWins(t *testing.T) {
	detected := []tools.Tool{{Name: "scoop", Found: true}}
	got := managers.Preferred(detected, "choco")
	if got == nil || got.Name() != "choco" {
		t.Fatalf("Preferred() = %v, want choco", got)
	}
}

func TestPreferredOverrideUnknownFallsThrough(t *testing.T) {
	detected := []tools.Tool{{Name: "winget", Found: true}}
	got := managers.Preferred(detected, "not-a-manager")
	if got == nil || got.Name() != "winget" {
		t.Fatalf("Preferred() = %v, want winget", got)
	}
}

func TestPreferredOrderScoopFirst(t *testing.T) {
	detected := []tools.Tool{
		{Name: "choco", Found: true},
		{Name: "winget", Found: true},
		{Name: "scoop", Found: true},
	}
	got := managers.Preferred(detected, "")
	if got == nil || got.Name() != "scoop" {
		t.Fatalf("Preferred() = %v, want scoop", got)
	}
}

func TestPreferredOrderWingetOverChoco(t *testing.T) {
	detected := []tools.Tool{
		{Name: "choco", Found: true},
		{Name: "winget", Found: true},
	}
	got := managers.Preferred(detected, "")
	if got == nil || got.Name() != "winget" {
		t.Fatalf("Preferred() = %v, want winget", got)
	}
}

func TestPreferredNoneFound(t *testing.T) {
	detected := []tools.Tool{
		{Name: "scoop", Found: false},
		{Name: "winget", Found: false},
	}
	got := managers.Preferred(detected, "")
	if got != nil {
		t.Fatalf("Preferred() = %v, want nil", got)
	}
}

func TestPreferredIgnoresNotFoundTools(t *testing.T) {
	detected := []tools.Tool{
		{Name: "scoop", Found: false},
		{Name: "choco", Found: true},
	}
	got := managers.Preferred(detected, "")
	if got == nil || got.Name() != "choco" {
		t.Fatalf("Preferred() = %v, want choco", got)
	}
}
