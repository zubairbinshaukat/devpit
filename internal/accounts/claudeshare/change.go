package claudeshare

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// ErrNotShared is returned when stopping the share of something that is not
// shared.
var ErrNotShared = errors.New("it is not shared")

func findEntry(it Item, name string) (Entry, bool) {
	for _, e := range it.Entries {
		if strings.EqualFold(e.Name, name) {
			return e, true
		}
	}
	return Entry{}, false
}

// PlanStopSharing plans turning one shared thing into the account's own
// real copy of what it shows now: a skill (by name), agents\, commands\ or
// CLAUDE.md (name ignored). A folder is copied to a temporary sibling, the
// link alone is removed, and the copy is renamed into place; each step is
// journalled, so an interruption anywhere is put right on the next run.
func PlanStopSharing(r Roots, k Kind, name string) (Plan, error) {
	inv, err := Scan(r)
	if err != nil {
		return Plan{}, err
	}
	it, ok := inv.Item(k)
	if !ok || !shareable(k) {
		return Plan{}, fmt.Errorf("%s is never shared by a link", k.Title())
	}
	pl := newPlanner(inv, Selection{})
	label := k.Title()
	switch k {
	case KindSkills:
		e, ok := findEntry(it, name)
		if !ok || e.State != EntryLinked {
			return Plan{}, fmt.Errorf("the skill %s: %w", name, ErrNotShared)
		}
		pl.materialise(k, e.TargetPath, e.SourcePath)
		pl.st.setLocal(e.Name, true)
		pl.st.setCopied("skills/"+e.Name, true)
		label = "the skill " + e.Name
	case KindAgents, KindCommands:
		if len(it.Entries) == 0 || it.Entries[0].State != EntryLinked {
			return Plan{}, fmt.Errorf("%s: %w", label, ErrNotShared)
		}
		pl.materialise(k, it.Target.Path, it.Source.Path)
		pl.st.setShared(k, false)
		pl.st.setCopied(copiedKey(k, ""), true)
	case KindClaudeMD:
		if len(it.Entries) == 0 || it.Entries[0].State != EntryLinked {
			return Plan{}, fmt.Errorf("CLAUDE.md: %w", ErrNotShared)
		}
		pl.inlineClaudeMD(it.Source.Path, it.Target.Path)
		pl.st.setShared(k, false)
	}
	p := pl.finish("Stopped sharing " + label + " in " + inv.Roots.TargetName)
	p.build = func() (Plan, error) { return PlanStopSharing(r, k, name) }
	return p, nil
}

// PlanShareInstead plans sharing what the account has as a copy or as its
// own: skills by name (a skill made in this account moves into the default
// account and is linked back), or the whole of agents\, commands\ or
// CLAUDE.md. Conflicts follow policy, as in the first setup.
func PlanShareInstead(r Roots, k Kind, names []string, policy Policy) (Plan, error) {
	if !shareable(k) {
		return Plan{}, fmt.Errorf("%s is never shared by a link", k.Title())
	}
	inv, err := Scan(r)
	if err != nil {
		return Plan{}, err
	}
	sel := Selection{Choices: map[Kind]Choice{k: {Mode: ModeShare, Policy: policy}}}
	if k == KindSkills && len(names) > 0 {
		sel.Names = map[Kind][]string{k: names}
	}
	p, err := BuildPlan(inv, sel)
	if err != nil {
		return Plan{}, err
	}
	p.Summary = "Shared " + k.Title() + " in " + inv.Roots.TargetName
	return p, nil
}

// PlanRepair plans fixing a broken link: pointing it at the default
// account's current copy when there is one, or removing the dead link (the
// link only; there is nothing behind it) when the default account no longer
// has that item.
func PlanRepair(r Roots, k Kind, name string) (Plan, error) {
	inv, err := Scan(r)
	if err != nil {
		return Plan{}, err
	}
	it, ok := inv.Item(k)
	if !ok || !shareable(k) {
		return Plan{}, fmt.Errorf("%s has no links to repair", k.Title())
	}
	pl := newPlanner(inv, Selection{})
	var e Entry
	switch k {
	case KindSkills:
		e, ok = findEntry(it, name)
	default:
		ok = len(it.Entries) > 0
		if ok {
			e = it.Entries[0]
		}
	}
	if !ok || e.State != EntryBroken {
		return Plan{}, fmt.Errorf("nothing broken to repair in %s", k.Title())
	}
	if k == KindClaudeMD {
		pl.repairClaudeMD(it)
	} else {
		pl.repairLink(k, e)
	}
	p := pl.finish("Repaired " + k.Title() + " in " + inv.Roots.TargetName)
	p.build = func() (Plan, error) { return PlanRepair(r, k, name) }
	return p, nil
}

func (pl *planner) repairLink(k Kind, e Entry) {
	pl.add(Step{
		Op: OpUnlink, Kind: k, From: e.LinkTarget, To: e.TargetPath,
		Text: "Remove the broken link " + pl.say(e.TargetPath) + " (it points at " + e.LinkTarget + ", which is gone)",
	})
	if exists(e.SourcePath) {
		pl.link(k, e.TargetPath, e.SourcePath)
		return
	}
	if k == KindSkills {
		pl.p.Notes = append(pl.p.Notes, "The default account no longer has "+e.Name+", so the link is removed and nothing replaces it.")
	} else {
		pl.st.setShared(k, false)
	}
}

func (pl *planner) repairClaudeMD(it Item) {
	src, dst := it.Source.Path, it.Target.Path
	own, _ := readFile(dst)
	body := withoutBlock(own)
	pl.move(KindClaudeMD, dst, pl.backupPath(KindClaudeMD, "CLAUDE.md", true), "Move "+pl.say(dst)+" to the backup folder")
	if exists(src) {
		nl := lineEnding(own)
		content := []byte(importBlock(src, nl))
		if len(body) > 0 {
			content = append(append(content, nl...), body...)
		}
		pl.add(Step{
			Op: OpWriteFile, Kind: KindClaudeMD, To: dst, Content: content,
			Text: "Write " + pl.say(dst) + " bringing in the default account's CLAUDE.md, with its own text below",
		})
		return
	}
	pl.st.setShared(KindClaudeMD, false)
	if len(body) > 0 {
		pl.add(Step{
			Op: OpWriteFile, Kind: KindClaudeMD, To: dst, Content: body,
			Text: "Write " + pl.say(dst) + " with its own text only (the default account has no CLAUDE.md)",
		})
	}
}

// PlanMaterialiseAll plans turning every link in the account back into the
// account's own real copy (for `devpit accounts cleanup`): skills, agents\,
// commands\ and the CLAUDE.md import. Broken links have nothing behind
// them and are removed (the link only). Links made by something else are
// left alone.
func PlanMaterialiseAll(r Roots) (Plan, error) {
	inv, err := Scan(r)
	if err != nil {
		return Plan{}, err
	}
	pl := newPlanner(inv, Selection{})
	for _, it := range inv.Items {
		switch it.Kind {
		case KindSkills, KindAgents, KindCommands:
			for _, e := range it.Entries {
				src, dst := e.SourcePath, e.TargetPath
				if it.Kind != KindSkills {
					src, dst = it.Source.Path, it.Target.Path
				}
				switch e.State {
				case EntryLinked:
					pl.materialise(it.Kind, dst, src)
				case EntryBroken:
					pl.add(Step{
						Op: OpUnlink, Kind: it.Kind, From: e.LinkTarget, To: dst,
						Text: "Remove the broken link " + pl.say(dst) + " (there is nothing behind it)",
					})
				case EntryForeignLink:
					pl.p.Warnings = append(pl.p.Warnings, dst+" is a link made by something else. Devpit leaves it alone.")
				}
			}
		case KindClaudeMD:
			if len(it.Entries) > 0 && (it.Entries[0].State == EntryLinked || it.Entries[0].State == EntryBroken) {
				pl.inlineClaudeMD(it.Source.Path, it.Target.Path)
			}
		}
	}
	pl.st.Shared = nil
	p := pl.finish("Turned every link in " + inv.Roots.TargetName + " into its own copy")
	p.build = func() (Plan, error) { return PlanMaterialiseAll(r) }
	return p, nil
}

// MaterialiseAll plans and applies [PlanMaterialiseAll] as one undoable
// change. It does nothing (and returns ErrNothingToDo) when the account has
// no links.
func MaterialiseAll(ctx context.Context, eng *accounts.Engine, r Roots, emit func(accounts.Event)) (accounts.Entry, error) {
	p, err := PlanMaterialiseAll(r)
	if err != nil {
		return accounts.Entry{}, err
	}
	return Apply(ctx, eng, p, emit)
}

// EnsureResult is what EnsureSkillLinks did.
type EnsureResult struct {
	// Created names the skills linked now.
	Created []string
	// Skipped says, in plain words, why nothing was linked ("" when the
	// links were all there or were made).
	Skipped string
	// Failed lists skills whose link could not be made, with the reason.
	Failed []string
}

// EnsureSkillLinks adds the missing skill links of an account that shares
// its skills: a skill that appeared in the default account since gets its
// junction. It only adds: it never moves, renames or deletes anything, and
// it never links over a name the account already has or keeps as its own.
// It is safe to call at every launch (the shim does) and cheap when nothing
// is missing: one small file read and two folder listings. It is not
// journalled; the links it adds are removed again by MaterialiseAll or by
// hand without loss. It stays out of the way while another Devpit holds the
// accounts lock (r.Lock).
func EnsureSkillLinks(r Roots) (EnsureResult, error) {
	var res EnsureResult
	if r.Target == "" || r.DefaultHome == "" || !filepath.IsAbs(r.Target) || !filepath.IsAbs(r.DefaultHome) {
		return res, errors.New("both account folders are needed, as full paths")
	}
	st, err := readState(r.Target)
	if err != nil {
		return res, err
	}
	if !st.shares(KindSkills) {
		res.Skipped = "this account does not share skills"
		return res, nil
	}
	if missingSkills(r, st) == nil {
		return res, nil
	}
	r, err = r.check()
	if err != nil {
		return res, err
	}
	if r.Lock != "" {
		l, err := accounts.AcquireLock(r.Lock, 0)
		if errors.Is(err, accounts.ErrLocked) {
			res.Skipped = "Devpit is changing accounts right now; the links are added next time"
			return res, nil
		}
		if err != nil {
			return res, err
		}
		defer l.Release()
	}
	src, dst := r.srcPath("skills"), r.dstPath("skills")
	if fi, err := lstat(dst); err == nil && (isReparse(fi) || !fi.IsDir()) {
		res.Skipped = dst + " is not a plain folder, so Devpit leaves it alone"
		return res, nil
	}
	if ok, why := r.linkable(src, nearestExisting(dst)); !ok {
		res.Skipped = why
		return res, nil
	}
	if !exists(dst) {
		if err := os.Mkdir(fsPath(dst), 0o700); err != nil {
			return res, err
		}
	}
	for _, name := range missingSkills(r, st) {
		s, t := filepath.Join(src, name), filepath.Join(dst, name)
		if exists(t) || containsDenied(s) || r.checkDenied(t) != nil {
			continue
		}
		if err := createJunction(t, s); err != nil {
			res.Failed = append(res.Failed, name+": "+err.Error())
			continue
		}
		res.Created = append(res.Created, name)
	}
	return res, nil
}

// missingSkills lists the default account's skills the account has no
// entry for and does not keep as its own.
func missingSkills(r Roots, st State) []string {
	srcEnts, err := readDir(r.srcPath("skills"))
	if err != nil || len(srcEnts) == 0 {
		return nil
	}
	dstEnts, _ := readDir(r.dstPath("skills"))
	have := map[string]bool{}
	for _, e := range dstEnts {
		have[strings.ToUpper(e.Name())] = true
	}
	var out []string
	for _, e := range srcEnts {
		n := e.Name()
		if strings.EqualFold(n, skillsSynced) || deniedAnywhere(n) || have[strings.ToUpper(n)] || st.isLocal(n) {
			continue
		}
		if !e.IsDir() && e.Type()&os.ModeType == 0 {
			continue // a plain file in skills\ is not a skill
		}
		out = append(out, n)
	}
	return out
}
