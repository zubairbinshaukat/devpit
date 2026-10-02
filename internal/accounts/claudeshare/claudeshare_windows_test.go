//go:build windows

package claudeshare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// These tests make real NTFS junctions in folders under t.TempDir(). They
// never touch the real %USERPROFILE%\.claude, .claude.json or .devpit.

func get(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(fsPath(path))
	must(t, err)
	return string(b)
}

func newEngine(t *testing.T) *accounts.Engine {
	t.Helper()
	e := accounts.NewEngine(accounts.PathsIn(t.TempDir(), t.TempDir()))
	e.LockWait = 2 * time.Second
	must(t, accounts.SaveFile(e.Paths.Store, accounts.NewStore()))
	Register(e)
	return e
}

// realFixture is a rich fixture whose link check is the real one.
func realFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	f.r.probe = nil
	f.rich(t)
	return f
}

type node struct {
	kind   string
	hash   string
	target string
	ro     bool
}

// snapshot lists every file, folder and link under the roots (never
// through a link), with file hashes, the read-only bit and link targets.
func snapshot(t *testing.T, roots ...string) map[string]node {
	t.Helper()
	out := map[string]node{}
	for i, root := range roots {
		if !exists(root) {
			continue
		}
		ents, err := walk(root)
		must(t, err)
		for _, e := range ents {
			key := fmt.Sprintf("%d:%s", i, e.rel)
			p := filepath.Join(root, e.rel)
			switch {
			case e.link:
				li, err := readLink(p)
				must(t, err)
				out[key] = node{kind: "link", target: strings.ToUpper(li.target)}
			case e.fi.IsDir():
				out[key] = node{kind: "dir"}
			default:
				h, err := fileHash(p)
				must(t, err)
				out[key] = node{kind: "file", hash: h, ro: e.fi.Mode().Perm()&0o200 == 0}
			}
		}
	}
	return out
}

func diffSnap(a, b map[string]node) []string {
	var d []string
	for k, v := range a {
		if w, ok := b[k]; !ok {
			d = append(d, "gone: "+k)
		} else if v != w {
			d = append(d, fmt.Sprintf("changed: %s %+v -> %+v", k, v, w))
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			d = append(d, "new: "+k)
		}
	}
	sort.Strings(d)
	return d
}

func sameSnap(t *testing.T, what string, a, b map[string]node) {
	t.Helper()
	if d := diffSnap(a, b); len(d) > 0 {
		t.Fatalf("%s: the tree differs:\n%s", what, strings.Join(d, "\n"))
	}
}

func isJunctionTo(t *testing.T, link, target string) bool {
	t.Helper()
	li, err := readLink(link)
	return err == nil && li.kind == linkJunction && samePath(li.target, target)
}

func apply(t *testing.T, eng *accounts.Engine, p Plan) ([]accounts.Event, accounts.Entry) {
	t.Helper()
	var evs []accounts.Event
	ent, err := Apply(context.Background(), eng, p, func(ev accounts.Event) { evs = append(evs, ev) })
	if err != nil {
		t.Fatalf("apply: %v\nplan:\n%s", err, strings.Join(p.Preview(), "\n"))
	}
	return evs, ent
}

func fullSelection(inv *Inventory, pol Policy) Selection {
	sel := inv.Selection(PresetSync)
	for k, c := range sel.Choices {
		c.Policy = pol
		sel.Choices[k] = c
	}
	sel.Choices[KindMCP] = Choice{Mode: ModeCopy, Policy: pol}
	sel.Choices[KindHistory] = Choice{Mode: ModeCopy, Policy: pol}
	sel.AllowSecrets = true
	return sel
}

func TestJunctionIsMadeReadAndRemovedAsALinkOnly(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "real")
	put(t, filepath.Join(target, "precious.txt"), "keep me")
	link := filepath.Join(base, "link")
	must(t, createJunction(link, target))
	if !isJunctionTo(t, link, target) {
		t.Fatal("not a junction to the target")
	}
	if get(t, filepath.Join(link, "precious.txt")) != "keep me" {
		t.Fatal("cannot read through the junction")
	}
	if st := measure(base); st.Files != 1 || st.Links != 1 || st.Bytes != 7 {
		t.Fatalf("sizing went through the link: %+v", st)
	}
	var nl *NotALinkError
	if err := removeLink(target, ""); !errors.As(err, &nl) {
		t.Fatalf("removing a real folder as a link: %v", err)
	}
	if err := removeLink(link, filepath.Join(base, "elsewhere")); err == nil || !exists(link) {
		t.Fatalf("a link to somewhere else was removed: %v", err)
	}
	must(t, removeLink(link, target))
	if exists(link) {
		t.Fatal("the link is still there")
	}
	if get(t, filepath.Join(target, "precious.txt")) != "keep me" {
		t.Fatal("removing the link touched its target")
	}
	if err := createJunction(link, `\\server\share\x`); err == nil {
		t.Fatal("a junction to a network path was made")
	}
}

func TestRemoveTreeNeverFollowsAJunction(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	put(t, filepath.Join(outside, "precious.txt"), "keep me")
	tree := filepath.Join(base, "tree")
	put(t, filepath.Join(tree, "a", "b", "f.txt"), "x")
	must(t, createJunction(filepath.Join(tree, "a", "link"), outside))
	must(t, createJunction(filepath.Join(tree, "loop"), tree))
	must(t, removeTree(tree))
	if exists(tree) {
		t.Fatal("tree not removed")
	}
	if get(t, filepath.Join(outside, "precious.txt")) != "keep me" {
		t.Fatal("removeTree followed a junction")
	}
}

func TestCopyNeverFollowsALinkOrCopiesIntoItself(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	put(t, filepath.Join(src, "f.txt"), "content")
	put(t, filepath.Join(src, ".credentials.json"), "{}")
	outside := filepath.Join(base, "outside")
	put(t, filepath.Join(outside, "big.bin"), strings.Repeat("x", 1000))
	must(t, createJunction(filepath.Join(src, "loop"), src))
	must(t, createJunction(filepath.Join(src, "out"), outside))
	rep, err := copyTree(src, filepath.Join(base, "dst"))
	must(t, err)
	if rep.Files != 1 || len(rep.SkippedLinks) != 2 || len(rep.SkippedDenied) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if exists(filepath.Join(base, "dst", "loop")) || exists(filepath.Join(base, "dst", "out")) || exists(filepath.Join(base, "dst", ".credentials.json")) {
		t.Fatal("a link or a login file was copied")
	}
	if _, err := copyTree(src, filepath.Join(src, "inner")); err == nil {
		t.Fatal("copied a folder into itself")
	}
	if _, err := copyTree(filepath.Join(src, "out"), filepath.Join(base, "x")); err == nil {
		t.Fatal("copied through a link")
	}
	// A destination reached through a link into the source is caught too.
	must(t, createJunction(filepath.Join(base, "via"), src))
	if _, err := copyTree(src, filepath.Join(base, "via", "inner")); err == nil {
		t.Fatal("copied into itself through a link")
	}
}

// TestApplyThenUndoRestoresTheExactTree is the round trip: every row, every
// conflict policy, real junctions; a second run changes nothing; undo puts
// back every byte, link and read-only bit of both accounts; the default
// account only ever gains files.
func TestApplyThenUndoRestoresTheExactTree(t *testing.T) {
	for _, pol := range []Policy{PolicyKeep, PolicyReplace, PolicyKeepBoth} {
		t.Run(string(pol), func(t *testing.T) {
			f := realFixture(t)
			ro := f.src("skills", "alpha", "readonly.txt")
			put(t, ro, "read only")
			must(t, os.Chmod(ro, 0o400))
			t.Cleanup(func() { _ = os.Chmod(ro, 0o600) })
			eng := newEngine(t)
			before := snapshot(t, f.def, f.defJSON, f.target)

			inv := scan(t, f.r)
			p, err := BuildPlan(inv, fullSelection(inv, pol))
			must(t, err)
			evs, ent := apply(t, eng, p)
			last := evs[len(evs)-1]
			if !last.Final || last.EntryID != ent.ID || ent.State != accounts.EntryDone {
				t.Fatalf("final event = %+v, entry %+v", last, ent)
			}

			if !isJunctionTo(t, f.dst("skills", "alpha"), f.src("skills", "alpha")) ||
				!isJunctionTo(t, f.dst("agents"), f.src("agents")) ||
				!isJunctionTo(t, f.dst("commands"), f.src("commands")) {
				t.Fatal("links missing")
			}
			if get(t, f.dst("skills", "alpha", "readonly.txt")) != "read only" {
				t.Fatal("cannot read a shared skill through its link")
			}
			if ok, _ := hasImportBlock([]byte(get(t, f.dst("CLAUDE.md")))); !ok {
				t.Fatal("CLAUDE.md has no import")
			}
			var cj map[string]any
			must(t, json.Unmarshal([]byte(get(t, f.dst(".claude.json"))), &cj))
			if cj["numStartups"].(float64) != 3 || cj["mcpServers"].(map[string]any)["github"] == nil {
				t.Fatalf(".claude.json = %v", cj)
			}
			if !exists(f.dst("projects", "C--work-app", "s1.jsonl")) {
				t.Fatal("history not copied")
			}
			if exists(f.dst("skills", "synced")) && isJunctionTo(t, f.dst("skills", "synced"), f.src("skills", "synced")) {
				t.Fatal(`skills\synced was linked`)
			}
			// The default account only gained things.
			mid := snapshot(t, f.def, f.defJSON, f.target)
			for k, v := range before {
				if strings.HasPrefix(k, "0:") || strings.HasPrefix(k, "1:") {
					if mid[k] != v {
						t.Fatalf("the default account's %s changed: %+v -> %+v", k, v, mid[k])
					}
				}
			}

			again, err := BuildPlan(scan(t, f.r), fullSelection(inv, pol))
			must(t, err)
			if !again.Empty() {
				t.Fatalf("running twice changes something:\n%s", strings.Join(again.Preview(), "\n"))
			}

			res, err := eng.Undo()
			must(t, err)
			if res.Entry.ID != ent.ID {
				t.Fatalf("undid %s, not %s", res.Entry.ID, ent.ID)
			}
			sameSnap(t, "after undo", before, snapshot(t, f.def, f.defJSON, f.target))
		})
	}
}

func TestStopSharingGivesARealCopyAndUndoLinksAgain(t *testing.T) {
	f := realFixture(t)
	ro := f.src("skills", "alpha", "readonly.txt")
	put(t, ro, "read only")
	must(t, os.Chmod(ro, 0o400))
	t.Cleanup(func() { _ = os.Chmod(ro, 0o600); _ = os.Chmod(f.dst("skills", "alpha", "readonly.txt"), 0o600) })
	eng := newEngine(t)
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, inv.Selection(PresetSync))
	must(t, err)
	apply(t, eng, p)
	shared := snapshot(t, f.def, f.target)

	for _, c := range []struct {
		k    Kind
		name string
	}{{KindSkills, "alpha"}, {KindAgents, ""}, {KindClaudeMD, ""}} {
		sp, perr := PlanStopSharing(f.r, c.k, c.name)
		must(t, perr)
		apply(t, eng, sp)
	}
	if li, _ := readLink(f.dst("skills", "alpha")); li.isLink() {
		t.Fatal("alpha is still a link")
	}
	if same, serr := sameContent(f.src("skills", "alpha"), f.dst("skills", "alpha")); serr != nil || !same {
		t.Fatalf("the copy differs from what the link showed: %v", serr)
	}
	if fi, _ := lstat(f.dst("skills", "alpha", "readonly.txt")); fi.Mode().Perm()&0o200 != 0 {
		t.Fatal("a read-only file lost its read-only bit")
	}
	if li, _ := readLink(f.dst("agents")); li.isLink() || !exists(f.dst("agents", "a.md")) {
		t.Fatal("agents is not a real folder with the files")
	}
	md := get(t, f.dst("CLAUDE.md"))
	if strings.Contains(md, mdBegin) || !strings.HasPrefix(md, "Default rules.") || !strings.Contains(md, "Work rules.") {
		t.Fatalf("CLAUDE.md = %q", md)
	}
	if !exists(f.src("skills", "alpha", "SKILL.md")) || !exists(f.src("agents", "a.md")) {
		t.Fatal("stopping a share touched the default account")
	}
	st, err := StatusOf(f.r)
	must(t, err)
	for _, s := range st {
		if s.Kind == KindSkills && s.Name == "alpha" && s.Status != StatusCopy {
			t.Errorf("alpha status = %s", s.Status)
		}
	}
	if _, err := PlanStopSharing(f.r, KindSkills, "alpha"); !errors.Is(err, ErrNotShared) {
		t.Fatalf("stopping twice: %v", err)
	}
	for range 3 {
		_, err := eng.Undo()
		must(t, err)
	}
	sameSnap(t, "after undoing the three stops", shared, snapshot(t, f.def, f.target))
}

func TestShareInsteadMovesALocalSkillIntoTheDefault(t *testing.T) {
	f := realFixture(t)
	eng := newEngine(t)
	inv := scan(t, f.r)
	sel := Selection{Choices: map[Kind]Choice{KindSkills: {Mode: ModeShare, Policy: PolicyReplace}}}
	p, err := BuildPlan(inv, sel)
	must(t, err)
	apply(t, eng, p)
	st, err := StatusOf(f.r)
	must(t, err)
	found := false
	for _, s := range st {
		if s.Kind == KindSkills && s.Name == "mine" {
			found = s.Status == StatusKeptLocal && s.CanShare
		}
	}
	if !found {
		t.Fatalf("mine is not kept local: %+v", st)
	}
	sp, err := PlanShareInstead(f.r, KindSkills, []string{"mine"}, PolicyKeep)
	must(t, err)
	apply(t, eng, sp)
	if !isJunctionTo(t, f.dst("skills", "mine"), f.src("skills", "mine")) || get(t, f.src("skills", "mine", "SKILL.md")) != "made in work\n" {
		t.Fatal("mine was not moved into the default and linked back")
	}
	again, err := PlanShareInstead(f.r, KindSkills, []string{"mine"}, PolicyKeep)
	must(t, err)
	if !again.Empty() {
		t.Fatalf("sharing twice: %v", again.Preview())
	}
}

func TestMaterialiseAllLeavesNoLinksAndLinksWorkWithoutDevpit(t *testing.T) {
	f := realFixture(t)
	eng := newEngine(t)
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, inv.Selection(PresetSync))
	must(t, err)
	apply(t, eng, p)
	// Links are plain junctions to an ordinary folder: anything can read
	// through them without Devpit.
	if get(t, f.dst("agents", "a.md")) != "agent a\n" || get(t, f.dst("skills", "same", "SKILL.md")) != "same\n" {
		t.Fatal("cannot read through the links")
	}
	shared := snapshot(t, f.def, f.target)
	mp, err := PlanMaterialiseAll(f.r)
	must(t, err)
	apply(t, eng, mp)
	ents, err := walk(f.target)
	must(t, err)
	for _, e := range ents {
		if e.link {
			t.Fatalf("a link is left: %s", e.rel)
		}
	}
	for _, s := range []string{"alpha", "same", "diff"} {
		if same, _ := sameContent(f.src("skills", s), f.dst("skills", s)); !same {
			t.Fatalf("skills\\%s is not a faithful copy", s)
		}
	}
	if same, _ := sameContent(f.src("agents"), f.dst("agents")); !same {
		t.Fatal("agents is not a faithful copy")
	}
	if strings.Contains(get(t, f.dst("CLAUDE.md")), mdBegin) {
		t.Fatal("CLAUDE.md still imports")
	}
	if res, eerr := EnsureSkillLinks(f.r); eerr != nil || len(res.Created) > 0 {
		t.Fatalf("EnsureSkillLinks after cleanup: %+v %v", res, eerr)
	}
	_, err = eng.Undo()
	must(t, err)
	sameSnap(t, "after undoing the cleanup", shared, snapshot(t, f.def, f.target))
}

func TestBrokenLinksAreShownAndRepaired(t *testing.T) {
	f := realFixture(t)
	eng := newEngine(t)
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, Selection{Choices: map[Kind]Choice{KindSkills: {Mode: ModeShare}}})
	must(t, err)
	apply(t, eng, p)

	must(t, removeTree(f.src("skills", "alpha"))) // the default account dropped the skill
	put(t, f.src("skills", "beta", "SKILL.md"), "beta\n")
	old := filepath.Join(f.base, "old-home", ".claude", "skills", "beta")
	must(t, createJunction(f.dst("skills", "beta"), old)) // a link to a home that moved

	st, err := StatusOf(f.r)
	must(t, err)
	broken := map[string]bool{}
	for _, s := range st {
		if s.Status == StatusBroken && s.CanRepair {
			broken[s.Name] = true
		}
	}
	if !broken["alpha"] || !broken["beta"] {
		t.Fatalf("broken = %v (%+v)", broken, st)
	}
	rp, err := PlanRepair(f.r, KindSkills, "alpha")
	must(t, err)
	apply(t, eng, rp)
	if exists(f.dst("skills", "alpha")) {
		t.Fatal("the dead link is still there")
	}
	rp, err = PlanRepair(f.r, KindSkills, "beta")
	must(t, err)
	apply(t, eng, rp)
	if !isJunctionTo(t, f.dst("skills", "beta"), f.src("skills", "beta")) {
		t.Fatal("beta was not pointed at the default home")
	}
	_, err = eng.Undo()
	must(t, err)
	if !isJunctionTo(t, f.dst("skills", "beta"), old) {
		t.Fatal("undo did not put the old link back")
	}
}

func TestLinksMadeBySomethingElseAreLeftAlone(t *testing.T) {
	f := realFixture(t)
	elsewhere := filepath.Join(f.base, "elsewhere")
	put(t, filepath.Join(elsewhere, "skill", "SKILL.md"), "theirs\n")
	put(t, filepath.Join(elsewhere, "cmds", "x.md"), "x\n")
	put(t, f.src("skills", "foreign", "SKILL.md"), "ours\n")
	must(t, createJunction(f.dst("skills", "foreign"), filepath.Join(elsewhere, "skill")))
	must(t, createJunction(f.dst("commands"), filepath.Join(elsewhere, "cmds")))
	eng := newEngine(t)
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, inv.Selection(PresetSync))
	must(t, err)
	if !strings.Contains(strings.Join(p.Warnings, "\n"), "made by something else") {
		t.Fatalf("warnings = %v", p.Warnings)
	}
	apply(t, eng, p)
	if !isJunctionTo(t, f.dst("skills", "foreign"), filepath.Join(elsewhere, "skill")) ||
		!isJunctionTo(t, f.dst("commands"), filepath.Join(elsewhere, "cmds")) {
		t.Fatal("a foreign link was changed")
	}
	if _, err := PlanMaterialiseAll(f.r); err != nil {
		t.Fatal(err)
	}
}

func TestAFolderInUseChangesNothing(t *testing.T) {
	f := realFixture(t)
	eng := newEngine(t)
	before := snapshot(t, f.def, f.defJSON, f.target)
	h, err := os.Open(f.dst("skills", "diff", "SKILL.md")) // no FILE_SHARE_DELETE, like an editor
	must(t, err)
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, inv.Selection(PresetSync))
	must(t, err)
	var evs []accounts.Event
	_, err = Apply(context.Background(), eng, p, func(ev accounts.Event) { evs = append(evs, ev) })
	_ = h.Close()
	var iu *InUseError
	if !errors.As(err, &iu) || !strings.Contains(err.Error(), "put back") {
		t.Fatalf("err = %v", err)
	}
	last := evs[len(evs)-1]
	if last.State != accounts.StepFailed || !last.Final || !strings.Contains(last.Err.Error(), "Claude Code") {
		t.Fatalf("last event = %+v", last)
	}
	sameSnap(t, "after a refused apply", before, snapshot(t, f.def, f.defJSON, f.target))
	hist, _, err := eng.History()
	must(t, err)
	if len(hist) != 1 || hist[0].State != accounts.EntryRolledBack {
		t.Fatalf("history = %+v", hist)
	}
}

func TestLongUnicodeTrailingDotAndCaseOnlyNames(t *testing.T) {
	f := newFixture(t)
	f.r.probe = nil
	deep := f.src("skills", "deep")
	for len(deep) < 300 {
		deep = filepath.Join(deep, "nested-folder-name")
	}
	put(t, filepath.Join(deep, "SKILL.md"), "deep\n")
	put(t, f.src("skills", "naïve-ñandú-技能", "SKILL.md"), "unicode\n")
	put(t, f.src("skills", "trailing.", "SKILL.md"), "dot\n")
	put(t, f.src("skills", "Mixed", "SKILL.md"), "same bytes\n")
	put(t, f.dst("skills", "mixed", "SKILL.md"), "same bytes\n")
	put(t, f.src("skills", "Case", "SKILL.md"), "default\n")
	put(t, f.dst("skills", "case", "SKILL.md"), "work\n")
	eng := newEngine(t)
	before := snapshot(t, f.def, f.target)
	inv := scan(t, f.r)
	it, _ := inv.Item(KindSkills)
	if it.Conflicts != 1 {
		t.Fatalf("case-only names: conflicts = %d (%+v)", it.Conflicts, it.Entries)
	}
	p, err := BuildPlan(inv, inv.Selection(PresetSync))
	must(t, err)
	apply(t, eng, p)
	for _, n := range []string{"deep", "naïve-ñandú-技能", "trailing.", "Mixed", "Case"} {
		if !isJunctionTo(t, f.dst("skills", n), f.src("skills", n)) {
			t.Errorf("%s is not linked", n)
		}
	}
	if get(t, filepath.Join(strings.Replace(deep, f.def, f.target, 1), "SKILL.md")) != "deep\n" {
		t.Error("cannot read a long path through the link")
	}
	if get(t, f.dst("skills", "case.from-work", "SKILL.md")) != "work\n" {
		t.Error("the case-only conflict was not kept beside")
	}
	if again, _ := BuildPlan(scan(t, f.r), inv.Selection(PresetSync)); !again.Empty() {
		t.Fatalf("running twice: %v", again.Preview())
	}
	stop, err := PlanStopSharing(f.r, KindSkills, "deep")
	must(t, err)
	apply(t, eng, stop)
	if li, _ := readLink(f.dst("skills", "deep")); li.isLink() {
		t.Error("deep is still a link")
	}
	for range 2 {
		_, err := eng.Undo()
		must(t, err)
	}
	sameSnap(t, "after undo", before, snapshot(t, f.def, f.target))
}

// TestAnInterruptionBetweenAnyTwoStepsIsPutRight simulates Devpit dying
// after each step in turn: the journal is left as it was at that moment
// (pending), and the next start rolls the change back to the exact tree.
func TestAnInterruptionBetweenAnyTwoStepsIsPutRight(t *testing.T) {
	f := realFixture(t)
	eng := newEngine(t)
	before := snapshot(t, f.def, f.defJSON, f.target)
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, fullSelection(inv, PolicyKeep))
	must(t, err)
	for k := 0; k <= len(p.Steps); k++ {
		crashAfter(t, eng, p, k, false)
		sameSnap(t, fmt.Sprintf("recovered after %d of %d steps", k, len(p.Steps)), before, snapshot(t, f.def, f.defJSON, f.target))
	}

	// The same for stopping a share, which removes a link in the middle.
	apply(t, eng, p)
	shared := snapshot(t, f.def, f.defJSON, f.target)
	sp, err := PlanStopSharing(f.r, KindAgents, "")
	must(t, err)
	for k := 0; k <= len(sp.Steps); k++ {
		crashAfter(t, eng, sp, k, k < len(sp.Steps) && sp.Steps[k].Op == OpCopy)
		sameSnap(t, fmt.Sprintf("stop sharing recovered after %d steps", k), shared, snapshot(t, f.def, f.defJSON, f.target))
	}
}

// crashAfter runs the first k steps, optionally half of step k+1 when it
// is a copy, and leaves the journal as a crash would.
func crashAfter(t *testing.T, eng *accounts.Engine, p Plan, k int, halfCopy bool) {
	t.Helper()
	txn, err := eng.Begin(p.Summary, nil)
	must(t, err)
	must(t, markUnfinished(txn))
	for i := 0; i < k; i++ {
		_, serr := runStep(txn, p.Roots, p.Steps[i])
		must(t, serr)
	}
	if halfCopy {
		st := p.Steps[k]
		rec := record{Op: st.Op, From: st.From, To: st.To, Tmp: st.To + ".devpit-copy-dead"}
		derr := do(txn, st, rec, func(rc *record) error {
			put(t, filepath.Join(rc.Tmp, "half.txt"), "half a copy")
			return errors.New("the power went out")
		})
		if derr == nil {
			t.Fatal("the simulated crash did not happen")
		}
	}
	journal, err := os.ReadFile(eng.Paths.Journal)
	must(t, err)
	_, err = txn.Commit() // releases the lock, as the dying process would
	must(t, err)
	must(t, os.WriteFile(eng.Paths.Journal, journal, 0o600))

	next := accounts.NewEngine(eng.Paths)
	Register(next)
	rec, err := next.Recover()
	must(t, err)
	if len(rec) != 1 || rec[0].Outcome != "rolled back" {
		t.Fatalf("after %d steps: recovered %+v", k, rec)
	}
}

func TestEnsureSkillLinksOnlyAdds(t *testing.T) {
	f := realFixture(t)
	eng := newEngine(t)
	if res, err := EnsureSkillLinks(f.r); err != nil || res.Skipped == "" {
		t.Fatalf("before sharing: %+v %v", res, err)
	}
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, Selection{Choices: map[Kind]Choice{KindSkills: {Mode: ModeShare}}})
	must(t, err)
	apply(t, eng, p)
	res, err := EnsureSkillLinks(f.r)
	must(t, err)
	if len(res.Created) != 0 || res.Skipped != "" {
		t.Fatalf("nothing was missing: %+v", res)
	}

	put(t, f.src("skills", "newone", "SKILL.md"), "new\n")
	put(t, f.src("skills", "leaky", ".credentials.json"), "{}")
	before := snapshot(t, f.def, f.target)
	res, err = EnsureSkillLinks(f.r)
	must(t, err)
	if len(res.Created) != 1 || res.Created[0] != "newone" || !isJunctionTo(t, f.dst("skills", "newone"), f.src("skills", "newone")) {
		t.Fatalf("res = %+v", res)
	}
	after := snapshot(t, f.def, f.target)
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("EnsureSkillLinks changed %s", k)
		}
	}
	if exists(f.dst("skills", "leaky")) {
		t.Fatal("a skill holding a login file was linked")
	}

	// A skill the account stopped sharing is never linked again.
	sp, err := PlanStopSharing(f.r, KindSkills, "alpha")
	must(t, err)
	apply(t, eng, sp)
	must(t, removeTree(f.dst("skills", "alpha")))
	res, err = EnsureSkillLinks(f.r)
	must(t, err)
	if exists(f.dst("skills", "alpha")) || len(res.Created) != 0 {
		t.Fatalf("a local skill was linked again: %+v", res)
	}

	// While another Devpit is changing accounts, it stays out of the way.
	put(t, f.src("skills", "later", "SKILL.md"), "later\n")
	f.r.Lock = filepath.Join(t.TempDir(), "accounts.lock")
	l, err := accounts.AcquireLock(f.r.Lock, 0)
	must(t, err)
	res, err = EnsureSkillLinks(f.r)
	must(t, err)
	if !strings.Contains(res.Skipped, "changing accounts") || exists(f.dst("skills", "later")) {
		t.Fatalf("with the lock held: %+v", res)
	}
	l.Release()
	res, err = EnsureSkillLinks(f.r)
	must(t, err)
	if len(res.Created) != 1 || res.Created[0] != "later" {
		t.Fatalf("after the lock: %+v", res)
	}
}

// TestLoginFilesNeverLeak plants recognisable fake tokens in both accounts'
// login and account files and checks that no plan, event, journal entry or
// file outside their own account ever holds one, and that no login file is
// ever copied or linked.
func TestLoginFilesNeverLeak(t *testing.T) {
	const srcTok, dstTok = "FAKETOKEN_default_login_marker", "FAKETOKEN_work_login_marker"
	f := realFixture(t)
	put(t, f.src(".credentials.json"), `{"claudeAiOauth":{"accessToken":"`+srcTok+`"}}`)
	put(t, f.src(".credentials.json.lock"), srcTok)
	put(t, f.src(".oauth_refresh.lock"), srcTok)
	put(t, f.src("backups", ".claude.json.backup"), srcTok)
	put(t, f.src("statsig", "id"), srcTok)
	put(t, f.src("sessions", "s.json"), srcTok)
	put(t, f.src("skills", "leaky", "SKILL.md"), "leaky\n")
	put(t, f.src("skills", "leaky", ".credentials.json"), srcTok)
	put(t, f.src("projects", "C--work-app", ".credentials.json"), srcTok)
	cj := get(t, f.defJSON)
	put(t, f.defJSON, strings.Replace(cj, `"numStartups": 40,`, `"numStartups": 40, "primaryApiKey": "`+srcTok+`",`, 1))
	put(t, f.dst(".credentials.json"), `{"claudeAiOauth":{"accessToken":"`+dstTok+`"}}`)
	put(t, f.dst(".claude.json"), "{\n  \"primaryApiKey\": \""+dstTok+"\"\n}\n")
	creds := get(t, f.dst(".credentials.json"))

	eng := newEngine(t)
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, fullSelection(inv, PolicyKeep))
	must(t, err)
	evs, _ := apply(t, eng, p)

	var seen strings.Builder
	for _, l := range p.Preview() {
		seen.WriteString(l + "\n")
	}
	pj, _ := json.Marshal(p.Steps)
	seen.Write(pj)
	for _, ev := range evs {
		fmt.Fprintf(&seen, "%+v\n", ev)
	}
	journal, err := os.ReadFile(eng.Paths.Journal)
	must(t, err)
	seen.Write(journal)
	if s := seen.String(); strings.Contains(s, srcTok) || strings.Contains(s, dstTok) {
		t.Fatal("a token reached a plan, an event or the journal")
	}

	scanFor := func(root, tok string) []string {
		var hits []string
		ents, err := walk(root)
		must(t, err)
		for _, e := range ents {
			p := filepath.Join(root, e.rel)
			if e.link {
				li, _ := readLink(p)
				if containsDenied(li.target) || deniedAnywhere(filepath.Base(li.target)) {
					hits = append(hits, "link to a login file: "+p)
				}
				continue
			}
			if !e.fi.IsDir() && strings.Contains(get(t, p), tok) {
				hits = append(hits, p)
			}
		}
		return hits
	}
	if h := scanFor(f.target, srcTok); len(h) > 0 {
		t.Fatalf("the default's token reached the account: %v", h)
	}
	if h := scanFor(f.def, dstTok); len(h) > 0 {
		t.Fatalf("the account's token reached the default: %v", h)
	}
	if exists(f.dst("skills", "leaky")) {
		t.Fatal("a skill holding a login file was shared")
	}
	if get(t, f.dst(".credentials.json")) != creds {
		t.Fatal("the account's login file changed")
	}
	for _, d := range []string{"backups", "statsig", "sessions", ".credentials.json.lock", ".oauth_refresh.lock"} {
		if exists(f.dst(d)) {
			t.Fatalf("%s reached the account", d)
		}
	}
	if !strings.Contains(get(t, f.dst(".claude.json")), dstTok) {
		t.Fatal("the account's own .claude.json lost its identity")
	}
}

func TestApplyStreamEndsWithTheEntry(t *testing.T) {
	f := realFixture(t)
	eng := newEngine(t)
	inv := scan(t, f.r)
	p, err := BuildPlan(inv, Selection{Choices: map[Kind]Choice{KindCommands: {Mode: ModeShare}}})
	must(t, err)
	var last accounts.Event
	n := 0
	for ev := range ApplyStream(context.Background(), eng, p) {
		last = ev
		n++
	}
	if !last.Final || last.EntryID == "" || last.State != accounts.StepDone || n < 4 {
		t.Fatalf("%d events, last %+v", n, last)
	}
	// A preview made before something changed is refused.
	put(t, f.src("skills", "zeta", "SKILL.md"), "z\n")
	stale, err := BuildPlan(scan(t, f.r), Selection{Choices: map[Kind]Choice{KindSkills: {Mode: ModeShare}}})
	must(t, err)
	put(t, f.src("skills", "eta", "SKILL.md"), "e\n")
	if _, err := Apply(context.Background(), eng, stale, nil); !errors.Is(err, ErrStalePlan) {
		t.Fatalf("stale plan: %v", err)
	}
	if _, err := Apply(context.Background(), eng, Plan{}, nil); !errors.Is(err, ErrNothingToDo) {
		t.Fatalf("empty plan: %v", err)
	}
}
