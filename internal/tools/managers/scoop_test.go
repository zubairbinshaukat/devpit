package managers_test

import (
	"os"
	"testing"

	"github.com/zubairbinshaukat/devpit/internal/tools/managers"
)

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	return string(b)
}

func TestScoopCommands(t *testing.T) {
	s := managers.Scoop{}
	if s.Name() != "scoop" {
		t.Errorf("Name() = %q", s.Name())
	}
	if got := s.ListCmd(); len(got) == 0 || got[0] != "scoop" {
		t.Errorf("ListCmd() = %v", got)
	}
	if got := s.OutdatedCmd(); len(got) == 0 || got[1] != "status" {
		t.Errorf("OutdatedCmd() = %v", got)
	}
	steps := s.UpgradeAllCmds()
	if len(steps) != 4 {
		t.Fatalf("UpgradeAllCmds() has %d steps, want 4", len(steps))
	}
	if got := s.InstallCmd("git"); got[len(got)-1] != "git" {
		t.Errorf("InstallCmd(git) = %v", got)
	}
	if s.NeedsElevation() {
		t.Error("NeedsElevation() = true, want false")
	}
}

func TestScoopParseList(t *testing.T) {
	s := managers.Scoop{}
	out := readTestdata(t, "scoop_list.txt")
	got := s.ParseList(out)
	want := []managers.Installed{
		{Name: "7zip", ID: "7zip", Version: "23.01"},
		{Name: "git", ID: "git", Version: "2.43.0"},
		{Name: "nodejs-lts", ID: "nodejs-lts", Version: "20.11.0"},
	}
	assertInstalledEqual(t, got, want)
}

func TestScoopParseListEmpty(t *testing.T) {
	s := managers.Scoop{}
	out := readTestdata(t, "scoop_list_empty.txt")
	got := s.ParseList(out)
	if len(got) != 0 {
		t.Errorf("ParseList(empty) = %v, want none", got)
	}
}

func TestScoopParseOutdated(t *testing.T) {
	s := managers.Scoop{}
	out := readTestdata(t, "scoop_status_outdated.txt")
	got := s.ParseOutdated(out)
	want := []managers.Outdated{
		{Name: "git", ID: "git", Current: "2.42.0", Latest: "2.43.0"},
		{Name: "nodejs-lts", ID: "nodejs-lts", Current: "20.10.0", Latest: "20.11.0"},
	}
	assertOutdatedEqual(t, got, want)
}

func TestScoopParseOutdatedUpToDate(t *testing.T) {
	s := managers.Scoop{}
	out := readTestdata(t, "scoop_status_uptodate.txt")
	got := s.ParseOutdated(out)
	if len(got) != 0 {
		t.Errorf("ParseOutdated(up to date) = %v, want none", got)
	}
}

func assertInstalledEqual(t *testing.T, got, want []managers.Installed) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func assertOutdatedEqual(t *testing.T, got, want []managers.Outdated) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
