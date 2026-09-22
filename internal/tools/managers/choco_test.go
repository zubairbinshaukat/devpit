package managers_test

import (
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
)

func TestChocoCommands(t *testing.T) {
	c := managers.Choco{}
	if c.Name() != "choco" {
		t.Errorf("Name() = %q", c.Name())
	}
	if !c.NeedsElevation() {
		t.Error("NeedsElevation() = false, want true")
	}
	steps := c.UpgradeAllCmds()
	if len(steps) != 1 {
		t.Fatalf("UpgradeAllCmds() has %d steps, want 1", len(steps))
	}
	if got := steps[0]; got[1] != "upgrade" || got[2] != "all" {
		t.Errorf("UpgradeAllCmds()[0] = %v", got)
	}
}

func TestChocoParseList(t *testing.T) {
	c := managers.Choco{}
	out := readTestdata(t, "choco_list.txt")
	got := c.ParseList(out)
	want := []managers.Installed{
		{Name: "git", ID: "git", Version: "2.43.0"},
		{Name: "nodejs-lts", ID: "nodejs-lts", Version: "20.11.0"},
		{Name: "7zip", ID: "7zip", Version: "23.1.0"},
	}
	assertInstalledEqual(t, got, want)
}

func TestChocoParseOutdated(t *testing.T) {
	c := managers.Choco{}
	out := readTestdata(t, "choco_outdated.txt")
	got := c.ParseOutdated(out)
	want := []managers.Outdated{
		{Name: "git", ID: "git", Current: "2.42.0", Latest: "2.43.0"},
		{Name: "nodejs-lts", ID: "nodejs-lts", Current: "20.10.0", Latest: "20.11.0"},
	}
	assertOutdatedEqual(t, got, want)
}

func TestChocoParseOutdatedNone(t *testing.T) {
	c := managers.Choco{}
	out := readTestdata(t, "choco_outdated_none.txt")
	got := c.ParseOutdated(out)
	if len(got) != 0 {
		t.Errorf("ParseOutdated(none) = %v, want none", got)
	}
}
