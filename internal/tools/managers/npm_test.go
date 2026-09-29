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

func TestNPMUpgradeCommands(t *testing.T) {
	n := managers.NPM{}
	assertArgv(t, "UpgradeCmd", n.UpgradeCmd("typescript"), []string{"npm", "install", "-g", "typescript@latest"})
	check := n.CheckCmds()
	if len(check) != 1 {
		t.Fatalf("CheckCmds() = %v, want one step", check)
	}
	assertArgv(t, "CheckCmds()[0]", check[0], []string{"npm", "outdated", "-g", "--json"})
	if got := n.CleanupCmds([]string{"typescript"}); got != nil {
		t.Errorf("CleanupCmds() = %v, want nil", got)
	}
}

func TestNPMParseOutdatedReport(t *testing.T) {
	n := managers.NPM{}
	rep := n.ParseOutdatedReport(readTestdata(t, "npm_outdated_multi.json"))
	if !rep.Parsed {
		t.Fatal("Parsed = false, want true")
	}
	// typescript is outdated in two places (an array): the first wins.
	// "same" is already at its latest and is left out.
	assertOutdatedEqual(t, rep.Packages, []managers.Outdated{
		{Name: "npm", ID: "npm", Current: "10.2.0", Latest: "10.2.4"},
		{Name: "typescript", ID: "typescript", Current: "5.3.3", Latest: "5.4.5"},
	})

	for _, out := range []string{"", "  \n", "{}"} {
		if r := n.ParseOutdatedReport(out); !r.Parsed || len(r.Packages) != 0 {
			t.Errorf("ParseOutdatedReport(%q) = %+v, want Parsed with no packages", out, r)
		}
	}
	for _, out := range []string{
		"npm ERR! code ENOENT",
		`{"error": {"code": "ENOTFOUND", "summary": "request to https://registry.npmjs.org failed"}}`,
	} {
		if r := n.ParseOutdatedReport(out); r.Parsed {
			t.Errorf("ParseOutdatedReport(%q).Parsed = true, want false", out)
		}
	}
}
