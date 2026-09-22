package managers_test

import (
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
)

func TestNPMCommands(t *testing.T) {
	n := managers.NPM{}
	if n.Name() != "npm" {
		t.Errorf("Name() = %q", n.Name())
	}
	if n.NeedsElevation() {
		t.Error("NeedsElevation() = true, want false")
	}
	if got := n.ListCmd(); len(got) == 0 || got[0] != "npm" {
		t.Errorf("ListCmd() = %v", got)
	}
	steps := n.UpgradeAllCmds()
	if len(steps) != 1 {
		t.Fatalf("UpgradeAllCmds() has %d steps, want 1", len(steps))
	}
}

func TestNPMParseList(t *testing.T) {
	n := managers.NPM{}
	out := readTestdata(t, "npm_ls.json")
	got := n.ParseList(out)
	want := []managers.Installed{
		{Name: "npm", ID: "npm", Version: "10.2.4"},
		{Name: "pnpm", ID: "pnpm", Version: "8.15.1"},
		{Name: "yarn", ID: "yarn", Version: "1.22.19"},
	}
	assertInstalledEqual(t, got, want)
}

func TestNPMParseListInvalidJSON(t *testing.T) {
	n := managers.NPM{}
	got := n.ParseList("not json")
	if len(got) != 0 {
		t.Errorf("ParseList(invalid) = %v, want none", got)
	}
}

func TestNPMParseOutdated(t *testing.T) {
	n := managers.NPM{}
	out := readTestdata(t, "npm_outdated.json")
	got := n.ParseOutdated(out)
	want := []managers.Outdated{
		{Name: "npm", ID: "npm", Current: "10.2.0", Latest: "10.2.4"},
		{Name: "pnpm", ID: "pnpm", Current: "8.10.0", Latest: "8.15.1"},
	}
	assertOutdatedEqual(t, got, want)
}

func TestNPMParseOutdatedEmpty(t *testing.T) {
	n := managers.NPM{}
	out := readTestdata(t, "npm_outdated_empty.json")
	got := n.ParseOutdated(out)
	if len(got) != 0 {
		t.Errorf("ParseOutdated(empty) = %v, want none", got)
	}
}

func TestNPMParseOutdatedEmptyObject(t *testing.T) {
	n := managers.NPM{}
	got := n.ParseOutdated("{}")
	if len(got) != 0 {
		t.Errorf("ParseOutdated({}) = %v, want none", got)
	}
}
