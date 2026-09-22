package managers_test

import (
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
)

func TestWingetCommands(t *testing.T) {
	w := managers.Winget{}
	if w.Name() != "winget" {
		t.Errorf("Name() = %q", w.Name())
	}
	steps := w.UpgradeAllCmds()
	if len(steps) != 1 {
		t.Fatalf("UpgradeAllCmds() has %d steps, want 1", len(steps))
	}
	install := w.InstallCmd("Git.Git")
	found := false
	for _, a := range install {
		if a == "Git.Git" {
			found = true
		}
	}
	if !found {
		t.Errorf("InstallCmd(Git.Git) = %v, id missing", install)
	}
	if w.NeedsElevation() {
		t.Error("NeedsElevation() = true, want false")
	}
}

func TestWingetParseList(t *testing.T) {
	w := managers.Winget{}
	out := readTestdata(t, "winget_list.txt")
	got := w.ParseList(out)
	want := []managers.Installed{
		{Name: "7-Zip", ID: "7zip.7zip", Version: "23.01"},
		{Name: "Git", ID: "Git.Git", Version: "2.43.0"},
		{Name: "Microsoft Edge", ID: "Microsoft.Edge", Version: "120.0.0.0"},
		{Name: "Visual Studio Code", ID: "Microsoft.VSCode", Version: "1.85.1"},
	}
	assertInstalledEqual(t, got, want)
}

func TestWingetParseListNone(t *testing.T) {
	w := managers.Winget{}
	out := readTestdata(t, "winget_list_none.txt")
	got := w.ParseList(out)
	if len(got) != 0 {
		t.Errorf("ParseList(none) = %v, want none", got)
	}
}

func TestWingetParseOutdated(t *testing.T) {
	w := managers.Winget{}
	out := readTestdata(t, "winget_upgrade.txt")
	got := w.ParseOutdated(out)
	want := []managers.Outdated{
		{Name: "Git", ID: "Git.Git", Current: "2.42.0", Latest: "2.43.0"},
		{Name: "Microsoft Edge", ID: "Microsoft.Edge", Current: "120.0.0.0", Latest: "121.0.0.0"},
	}
	assertOutdatedEqual(t, got, want)
}

func TestWingetParseOutdatedNone(t *testing.T) {
	w := managers.Winget{}
	out := readTestdata(t, "winget_upgrade_none.txt")
	got := w.ParseOutdated(out)
	if len(got) != 0 {
		t.Errorf("ParseOutdated(none) = %v, want none", got)
	}
}
