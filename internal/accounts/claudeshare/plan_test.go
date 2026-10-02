package claudeshare

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scan(t *testing.T, r Roots) *Inventory {
	t.Helper()
	inv, err := Scan(r)
	must(t, err)
	return inv
}

func TestInventoryListsEveryRowWithTheLockedOnesLast(t *testing.T) {
	f := newFixture(t)
	f.rich(t)
	put(t, f.src(".credentials.json"), "{}")
	inv := scan(t, f.r)
	var kinds []Kind
	for _, it := range inv.Items {
		kinds = append(kinds, it.Kind)
	}
	if len(kinds) != len(Kinds()) {
		t.Fatalf("rows = %v", kinds)
	}
	for i, k := range Kinds() {
		if kinds[i] != k {
			t.Fatalf("row %d = %s, want %s", i, kinds[i], k)
		}
	}
	login, _ := inv.Item(KindLogin)
	acct, _ := inv.Item(KindAccountInfo)
	for _, it := range []Item{login, acct} {
		if !it.Locked || it.Label != LabelLocked || len(it.Modes) != 1 || it.Modes[0] != ModeLocked {
			t.Fatalf("%s is not locked: %+v", it.Title, it)
		}
	}
	if !login.Source.Exists || login.Target.Exists {
		t.Fatalf("login presence = %+v / %+v", login.Source, login.Target)
	}

	skills, _ := inv.Item(KindSkills)
	states := map[string]EntryState{}
	for _, e := range skills.Entries {
		states[e.Name] = e.State
	}
	want := map[string]EntryState{"alpha": EntrySourceOnly, "same": EntrySame, "diff": EntryDiffers, "mine": EntryTargetOnly}
	for n, s := range want {
		if states[n] != s {
			t.Errorf("skill %s = %s, want %s", n, states[n], s)
		}
	}
	if _, ok := states["synced"]; ok {
		t.Error(`skills\synced is per account and must never be listed`)
	}
	if skills.Conflicts != 1 || skills.Source.Count != 3 || skills.Target.Count != 3 {
		t.Errorf("skills counts: conflicts %d, source %d, target %d", skills.Conflicts, skills.Source.Count, skills.Target.Count)
	}
	if got := inv.NotSharedYet(); got != "1 new skill in work is not shared yet: mine" {
		t.Errorf("NotSharedYet = %q", got)
	}

	mcp, _ := inv.Item(KindMCP)
	if mcp.Label != LabelCareful || mcp.Asks != 2 || len(mcp.Secrets) != 1 || mcp.Secrets[0] != "github" {
		t.Errorf("MCP row = %+v", mcp)
	}
	settings, _ := inv.Item(KindSettings)
	if len(settings.Secrets) != 1 || settings.Secrets[0] != "env" {
		t.Errorf("settings secrets = %v", settings.Secrets)
	}
	for _, e := range settings.Entries {
		if e.Name == "hooks" || e.Name == "enabledPlugins" || e.Name == "extraKnownMarketplaces" {
			t.Errorf("settings lists %s, which belongs to another row", e.Name)
		}
	}
	plugins, _ := inv.Item(KindPlugins)
	if plugins.Default != ModeSameList || plugins.Offers(ModeShare) {
		t.Errorf("plugins must be Same list only, never a shared folder: %+v", plugins.Modes)
	}
}

func TestPresetsAreTheFourQuickAnswers(t *testing.T) {
	f := newFixture(t)
	f.rich(t)
	inv := scan(t, f.r)
	cases := map[Preset]map[Kind]Mode{
		PresetSync: {
			KindSkills: ModeShare, KindAgents: ModeShare, KindCommands: ModeShare, KindClaudeMD: ModeShare,
			KindPlugins: ModeSameList, KindSettings: ModeCopy, KindHooks: ModeCopy, KindMCP: ModeSkip, KindHistory: ModeSkip,
		},
		PresetCopyOnce: {
			KindSkills: ModeCopy, KindAgents: ModeCopy, KindCommands: ModeCopy, KindClaudeMD: ModeCopy,
			KindPlugins: ModeSameList, KindSettings: ModeCopy, KindHooks: ModeCopy, KindMCP: ModeSkip, KindHistory: ModeSkip,
		},
		PresetEmpty: {
			KindSkills: ModeSkip, KindAgents: ModeSkip, KindCommands: ModeSkip, KindClaudeMD: ModeSkip,
			KindPlugins: ModeSkip, KindSettings: ModeSkip, KindHooks: ModeSkip, KindMCP: ModeSkip, KindHistory: ModeSkip,
		},
	}
	cases[PresetChoose] = cases[PresetSync]
	for p, want := range cases {
		sel := inv.Selection(p)
		for k, m := range want {
			if got := sel.Choices[k]; got.Mode != m || got.Policy != PolicyKeep {
				t.Errorf("%s: %s = %+v, want %s/keep", p.Title(), k, got, m)
			}
		}
		if _, ok := sel.Choices[KindLogin]; ok {
			t.Errorf("%s selects the login row", p)
		}
		if sel.AllowSecrets {
			t.Errorf("%s allows secrets", p)
		}
	}
	empty, err := BuildPlan(inv, inv.Selection(PresetEmpty))
	must(t, err)
	if !empty.Empty() {
		t.Fatalf("Start empty plans:\n%s", dump(f.views(empty)))
	}
}

func TestLockedRowsCanNeverBeSelected(t *testing.T) {
	f := newFixture(t)
	inv := scan(t, f.r)
	for _, k := range []Kind{KindLogin, KindAccountInfo} {
		for _, m := range []Mode{ModeCopy, ModeShare, ModeLocked} {
			_, err := BuildPlan(inv, Selection{Choices: map[Kind]Choice{k: {Mode: m}}})
			if !errors.Is(err, errLockedRow) {
				t.Errorf("%s/%s: err = %v", k, m, err)
			}
		}
	}
	if _, err := BuildPlan(inv, Selection{Choices: map[Kind]Choice{KindPlugins: {Mode: ModeShare}}}); err == nil {
		t.Error("plugins accepted Share; a shared plugins folder must be impossible")
	}
}

func TestShareConflictsFollowThePolicy(t *testing.T) {
	cases := []struct {
		policy Policy
		want   []stepView
		absent []stepView
		local  []string
	}{
		{
			policy: PolicyKeep,
			want: []stepView{
				{OpLink, "S/skills/alpha", "T/skills/alpha"},
				{OpMove, "T/skills/diff", "T/skills/diff.from-work"},
				{OpLink, "S/skills/diff", "T/skills/diff"},
				{OpMove, "T/skills/mine", "S/skills/mine"},
				{OpLink, "S/skills/mine", "T/skills/mine"},
				{OpMove, "T/skills/same", "T/devpit-backup-2026-10-02/skills/same"},
				{OpLink, "S/skills/same", "T/skills/same"},
			},
			local: []string{"diff.from-work"},
		},
		{
			policy: PolicyReplace,
			want: []stepView{
				{OpMove, "T/skills/diff", "T/devpit-backup-2026-10-02/skills/diff"},
				{OpLink, "S/skills/diff", "T/skills/diff"},
			},
			absent: []stepView{{OpMove, "T/skills/mine", "S/skills/mine"}},
			local:  []string{"mine"},
		},
		{
			policy: PolicyKeepBoth,
			want: []stepView{
				{OpMove, "T/skills/diff", "S/skills/diff-work"},
				{OpLink, "S/skills/diff", "T/skills/diff"},
				{OpLink, "S/skills/diff-work", "T/skills/diff-work"},
				{OpMove, "T/skills/mine", "S/skills/mine"},
			},
		},
	}
	for _, c := range cases {
		t.Run(string(c.policy), func(t *testing.T) {
			f := newFixture(t)
			f.rich(t)
			inv := scan(t, f.r)
			p, err := BuildPlan(inv, Selection{Choices: map[Kind]Choice{KindSkills: {Mode: ModeShare, Policy: c.policy}}})
			must(t, err)
			vs := f.views(p)
			for _, w := range c.want {
				if !hasStep(vs, w) {
					t.Errorf("missing %v in\n%s", w, dump(vs))
				}
			}
			for _, a := range c.absent {
				if hasStep(vs, a) {
					t.Errorf("unexpected %v in\n%s", a, dump(vs))
				}
			}
			for _, v := range vs {
				if strings.Contains(strings.ToLower(v.from+v.to), "synced") {
					t.Errorf(`skills\synced touched: %v`, v)
				}
				if v.op == OpMove && strings.HasPrefix(v.from, "S/") {
					t.Errorf("something in the default account moves: %v", v)
				}
			}
			for _, s := range p.Steps {
				must(t, f.r.checkStep(s))
				if s.Op == OpMove && strings.HasPrefix(f.view(s).to, "S/") && !s.AddsToDefault {
					t.Errorf("a move into the default account is not flagged: %s", s.Text)
				}
			}
			if c.policy != PolicyReplace && len(p.AddsToDefault) == 0 {
				t.Error("the plan must say clearly that it adds files to the default account")
			}
			last := p.Steps[len(p.Steps)-1]
			if last.Op != OpState {
				t.Fatalf("the sharing record is not saved last: %v", f.view(last))
			}
			for _, n := range c.local {
				if !strings.Contains(string(last.Content), n) {
					t.Errorf("the record does not keep %s local: %s", n, last.Content)
				}
			}
		})
	}
}

func TestFoldersAndCLAUDEmdShare(t *testing.T) {
	f := newFixture(t)
	f.rich(t)
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, inv.Selection(PresetSync))
	must(t, err)
	t.Log("preview:\n" + strings.Join(p.Preview(), "\n"))
	vs := f.views(p)
	for _, w := range []stepView{
		{OpMove, "T/agents/b.md", "S/agents/b.md"},
		{OpMove, "T/agents/c.md", "S/agents/c.from-work.md"},
		{OpRmdir, "", "T/agents"},
		{OpLink, "S/agents", "T/agents"},
		{OpLink, "S/commands", "T/commands"},
		{OpImport, "S/CLAUDE.md", "T/CLAUDE.md"},
		{OpMergeJSON, "S/settings.json", "T/settings.json"},
	} {
		if !hasStep(vs, w) {
			t.Errorf("missing %v in\n%s", w, dump(vs))
		}
	}
	// Folders are emptied before they are removed, and linked after.
	idx := map[stepView]int{}
	for i, v := range vs {
		idx[v] = i
	}
	moved, removed, linked := idx[stepView{OpMove, "T/agents/b.md", "S/agents/b.md"}],
		idx[stepView{OpRmdir, "", "T/agents"}], idx[stepView{OpLink, "S/agents", "T/agents"}]
	if moved >= removed || removed >= linked {
		t.Errorf("agents steps out of order:\n%s", dump(vs))
	}
	for _, s := range p.Steps {
		must(t, f.r.checkStep(s))
		if s.Kind == KindMCP || s.Kind == KindHistory {
			t.Errorf("a Careful or Skip row was planned: %s", s.Text)
		}
	}
}

func TestCopyModeNeverOverwrites(t *testing.T) {
	cases := []struct {
		policy Policy
		want   []stepView
	}{
		{PolicyKeep, []stepView{{OpCopy, "S/skills/diff", "T/skills/diff.from-default"}}},
		{PolicyReplace, []stepView{
			{OpMove, "T/skills/diff", "T/devpit-backup-2026-10-02/skills/diff"},
			{OpCopy, "S/skills/diff", "T/skills/diff"},
		}},
		{PolicyKeepBoth, []stepView{
			{OpMove, "T/skills/diff", "T/skills/diff-work"},
			{OpCopy, "S/skills/diff", "T/skills/diff"},
		}},
	}
	for _, c := range cases {
		t.Run(string(c.policy), func(t *testing.T) {
			f := newFixture(t)
			f.rich(t)
			inv := scan(t, f.r)
			p, err := BuildPlan(inv, Selection{Choices: map[Kind]Choice{KindSkills: {Mode: ModeCopy, Policy: c.policy}}})
			must(t, err)
			vs := f.views(p)
			for _, w := range append(c.want, stepView{OpCopy, "S/skills/alpha", "T/skills/alpha"}) {
				if !hasStep(vs, w) {
					t.Errorf("missing %v in\n%s", w, dump(vs))
				}
			}
			for _, v := range vs {
				if v.op == OpLink {
					t.Errorf("copy mode made a link: %v", v)
				}
			}
		})
	}
}

func TestNamesThatAreTakenGetANumber(t *testing.T) {
	f := newFixture(t)
	f.rich(t)
	put(t, f.dst("skills", "diff.from-work", "SKILL.md"), "an older rename\n")
	must(t, os.MkdirAll(f.dst("devpit-backup-2026-10-02"), 0o700))
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, Selection{Choices: map[Kind]Choice{KindSkills: {Mode: ModeShare, Policy: PolicyKeep}}})
	must(t, err)
	vs := f.views(p)
	for _, w := range []stepView{
		{OpMove, "T/skills/diff", "T/skills/diff.from-work-2"},
		{OpMove, "T/skills/same", "T/devpit-backup-2026-10-02-2/skills/same"},
	} {
		if !hasStep(vs, w) {
			t.Errorf("missing %v in\n%s", w, dump(vs))
		}
	}
}

func TestSecretsAreCopiedOnlyOnTheCarefulPath(t *testing.T) {
	f := newFixture(t)
	f.rich(t)
	inv := scan(t, f.r)
	sel := Selection{Choices: map[Kind]Choice{
		KindSettings: {Mode: ModeCopy}, KindMCP: {Mode: ModeCopy},
	}}
	p, err := BuildPlan(inv, sel)
	must(t, err)
	for _, s := range p.Steps {
		if s.JSON == nil {
			continue
		}
		for _, op := range s.JSON.Ops {
			if op.Path[len(op.Path)-1] == "env" || op.Path[len(op.Path)-1] == "github" {
				t.Errorf("%v copied without the Careful path", op.Path)
			}
		}
	}
	if len(p.Secrets) != 0 {
		t.Errorf("secrets = %v", p.Secrets)
	}
	if !strings.Contains(strings.Join(p.Notes, "\n"), "env was left out") {
		t.Errorf("notes = %v", p.Notes)
	}

	sel.AllowSecrets = true
	p, err = BuildPlan(inv, sel)
	must(t, err)
	got := map[string]bool{}
	for _, s := range p.Steps {
		if s.JSON != nil {
			for _, op := range s.JSON.Ops {
				got[op.Path[len(op.Path)-1]] = true
			}
		}
	}
	if !got["env"] || !got["github"] || !got["files"] {
		t.Errorf("careful path ops = %v", got)
	}
	if strings.Join(p.Secrets, ",") != "env,github" {
		t.Errorf("secrets named = %v", p.Secrets)
	}
	for _, line := range p.Preview() {
		if strings.Contains(line, "not-a-real-value") {
			t.Fatalf("a value reached the preview: %s", line)
		}
	}
}

func TestNoLinkMeansCopyWithAReason(t *testing.T) {
	f := newFixture(t)
	f.rich(t)
	f.r.probe = func(string, string) (bool, string) {
		return false, "the default account and this account are on different drives"
	}
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, inv.Selection(PresetSync))
	must(t, err)
	for _, s := range p.Steps {
		if s.Op == OpLink {
			t.Fatalf("a link was planned: %s", s.Text)
		}
	}
	if len(p.Fallbacks) < 3 || !strings.Contains(p.Fallbacks[0], "different drives") {
		t.Fatalf("fallbacks = %v", p.Fallbacks)
	}
	if !hasStep(f.views(p), stepView{OpImport, "S/CLAUDE.md", "T/CLAUDE.md"}) {
		t.Error("CLAUDE.md needs no link and should still be shared by its import line")
	}

	// After Apply found that Windows refuses links here (LinkError).
	f.r.probe = func(string, string) (bool, string) { return true, "" }
	inv = scan(t, f.r)
	sel := inv.Selection(PresetSync)
	sel.CopyInsteadOfLinks = true
	p, err = BuildPlan(inv, sel)
	must(t, err)
	for _, s := range p.Steps {
		if s.Op == OpLink {
			t.Fatalf("a link was planned after links were refused: %s", s.Text)
		}
	}
	if len(p.Fallbacks) != 3 {
		t.Fatalf("fallbacks = %v", p.Fallbacks)
	}
}

func TestDefaultWithoutSkillsAndTargetWithout(t *testing.T) {
	f := newFixture(t)
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, Selection{Choices: map[Kind]Choice{KindSkills: {Mode: ModeShare}}})
	must(t, err)
	for _, s := range p.Steps {
		if s.Op != OpState {
			t.Errorf("unexpected step: %s", s.Text)
		}
	}
}

func TestTargetChecks(t *testing.T) {
	f := newFixture(t)
	r := f.r
	r.Target = filepath.Join(f.base, "missing")
	if _, err := Scan(r); !errors.Is(err, ErrTargetMissing) {
		t.Errorf("missing: %v", err)
	}
	other := filepath.Join(f.base, "photos")
	put(t, filepath.Join(other, "cat.jpg"), "meow")
	r.Target = other
	if _, err := Scan(r); !errors.Is(err, ErrNotClaudeFolder) {
		t.Errorf("not a Claude folder: %v", err)
	}
	r.Target = f.def
	if _, err := Scan(r); !errors.Is(err, ErrSameFolder) {
		t.Errorf("same folder: %v", err)
	}
	r.Target = filepath.Join(f.def, "nested")
	must(t, os.MkdirAll(r.Target, 0o700))
	if _, err := Scan(r); !errors.Is(err, ErrSameFolder) {
		t.Errorf("inside the default: %v", err)
	}
	empty := filepath.Join(f.base, "fresh")
	must(t, os.MkdirAll(empty, 0o700))
	r.Target = empty
	if _, err := Scan(r); err != nil {
		t.Errorf("an empty folder (not signed in yet) is fine: %v", err)
	}
}

func TestLinkVerdictReasons(t *testing.T) {
	c := volInfo{root: `C:\`, serial: 1, fs: "NTFS", reparse: true, supports: true}
	d := volInfo{root: `D:\`, serial: 2, fs: "NTFS", reparse: true, supports: true}
	fat := volInfo{root: `C:\`, serial: 1, fs: "exFAT", supports: true}
	net := c
	net.remote = true
	cases := []struct {
		name           string
		src, dst, rs   string
		sv, dv         volInfo
		od             []string
		ok             bool
		reasonContains string
	}{
		{"same NTFS drive", `C:\u\.claude\skills`, `C:\a\work\skills`, `C:\u\.claude\skills`, c, c, nil, true, ""},
		{"different drives", `C:\u\.claude\skills`, `D:\a\work\skills`, `C:\u\.claude\skills`, c, d, nil, false, "different drives"},
		{"exFAT", `C:\u\.claude\skills`, `C:\a\work\skills`, `C:\u\.claude\skills`, c, fat, nil, false, "exFAT"},
		{"network", `C:\u\.claude\skills`, `\\nas\share\work\skills`, `C:\u\.claude\skills`, c, c, nil, false, "network"},
		{"mapped", `C:\u\.claude\skills`, `Z:\work\skills`, `C:\u\.claude\skills`, c, net, nil, false, "network"},
		{"subst", `S:\.claude\skills`, `C:\a\work\skills`, `C:\u\.claude\skills`, c, c, nil, false, "subst"},
		{"onedrive", `C:\u\.claude\skills`, `C:\u\OneDrive\work\skills`, `C:\u\.claude\skills`, c, c, []string{`C:\u\OneDrive`}, false, "OneDrive"},
		{"not windows", `C:\u\.claude\skills`, `C:\a\work\skills`, `C:\u\.claude\skills`, volInfo{}, volInfo{}, nil, false, "Windows"},
	}
	for _, tc := range cases {
		ok, why := linkVerdict(tc.src, tc.dst, tc.rs, tc.dst, tc.sv, tc.dv, tc.od)
		if ok != tc.ok || (tc.reasonContains != "" && !strings.Contains(why, tc.reasonContains)) {
			t.Errorf("%s: ok=%v why=%q", tc.name, ok, why)
		}
	}
}

func TestCheckStepRefusesWhatMustNeverHappen(t *testing.T) {
	f := newFixture(t)
	r := f.r
	bad := []Step{
		{Op: OpMove, From: f.src("skills", "a"), To: f.dst("skills", "a")},
		{Op: OpUnlink, From: f.dst("x"), To: f.src("skills", "a")},
		{Op: OpRmdir, To: f.src("agents")},
		{Op: OpLink, From: f.src("skills", "a"), To: f.src("skills", "b")},
		{Op: OpCopy, From: f.src(".credentials.json"), To: f.dst(".credentials.json")},
		{Op: OpCopy, From: f.dst("x"), To: f.src("x")},
		{Op: OpLink, From: f.src("statsig"), To: f.dst("statsig")},
		{Op: OpCopy, From: f.src("backups"), To: f.dst("backups")},
		{Op: OpCopy, From: f.src("x.lock"), To: f.dst("x.lock")},
		{Op: OpMove, From: f.dst(".claude.json"), To: f.dst("devpit-backup", ".claude.json")},
		{Op: OpImport, From: f.src("CLAUDE.md"), To: f.src("CLAUDE.md")},
		{Op: OpWriteFile, To: f.dst("sessions", "x")},
		{Op: OpMergeJSON, To: f.defJSON, JSON: &JSONMerge{File: f.defJSON}},
		{Op: OpMergeJSON, To: f.dst(".claude.json"), JSON: &JSONMerge{
			File: f.dst(".claude.json"),
			Ops:  []MemberOp{{Path: []string{"oauthAccount"}, Action: ActAdd}},
		}},
		{Op: OpState, To: f.src(stateFileName)},
		{Op: OpMove, From: f.dst("skills"), To: f.target},
	}
	for _, s := range bad {
		if err := r.checkStep(s); err == nil {
			t.Errorf("allowed: %s %v", s.Op, f.view(s))
		}
	}
}

func TestDenyList(t *testing.T) {
	for _, n := range []string{".credentials.json", ".CREDENTIALS.JSON", ".claude.json", ".claude.json.backup", ".claude.json.lock", ".credentials.json."} {
		if !deniedAnywhere(n) {
			t.Errorf("%s not denied", n)
		}
	}
	for _, n := range []string{"backups", "daemon", "sessions", "ide", "statsig", "shell-snapshots", ".oauth_refresh.lock", "x.lock", stateFileName} {
		if !deniedTop(n) {
			t.Errorf("%s not denied at the top", n)
		}
	}
	for _, n := range []string{"skills", "agents", "CLAUDE.md", "settings.json", "yarn.lock.md", "projects"} {
		if deniedTop(n) {
			t.Errorf("%s denied", n)
		}
	}
	if deniedAnywhere("yarn.lock") {
		t.Error("a lock file deep inside a skill is not a login file")
	}
}
