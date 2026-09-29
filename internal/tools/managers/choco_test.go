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

func TestChocoUpgradeCommands(t *testing.T) {
	c := managers.Choco{}
	assertArgv(t, "UpgradeCmd", c.UpgradeCmd("git"), []string{"choco", "upgrade", "git", "-y"})
	check := c.CheckCmds()
	if len(check) != 1 {
		t.Fatalf("CheckCmds() = %v, want one step", check)
	}
	assertArgv(t, "CheckCmds()[0]", check[0], c.OutdatedCmd())
	if got := c.CleanupCmds([]string{"git"}); got != nil {
		t.Errorf("CleanupCmds() = %v, want nil", got)
	}
}

func TestChocoParseOutdatedReport(t *testing.T) {
	c := managers.Choco{}
	rep := c.ParseOutdatedReport(readTestdata(t, "choco_outdated_pinned.txt"))
	if !rep.Parsed {
		t.Fatal("Parsed = false, want true")
	}
	assertOutdatedEqual(t, rep.Packages, []managers.Outdated{
		{Name: "git", ID: "git", Current: "2.42.0", Latest: "2.43.0"},
		{Name: "nodejs-lts", ID: "nodejs-lts", Current: "20.10.0", Latest: "20.11.0", Pinned: true},
	})

	none := c.ParseOutdatedReport(readTestdata(t, "choco_outdated_none.txt"))
	if !none.Parsed || len(none.Packages) != 0 {
		t.Errorf("none report = %+v, want Parsed with no packages", none)
	}
	if bad := c.ParseOutdatedReport("choco : The term 'choco' is not recognized"); bad.Parsed {
		t.Error("unrecognised output Parsed = true, want false")
	}
}
