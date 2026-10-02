package claudeshare

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Op is one kind of step.
type Op string

// Steps. Every one is recorded in the journal before it happens and undone
// by this package's effect handlers.
const (
	// OpMkdir creates the folder To.
	OpMkdir Op = "mkdir"
	// OpLink makes To a junction to From.
	OpLink Op = "link"
	// OpUnlink removes the junction To (which points at From), and only it.
	OpUnlink Op = "unlink"
	// OpMove renames From to To; it never replaces anything.
	OpMove Op = "move"
	// OpCopy copies From to To, which must not exist.
	OpCopy Op = "copy"
	// OpRmdir removes the empty folder To.
	OpRmdir Op = "rmdir"
	// OpMergeJSON merges members into a JSON file (see JSON).
	OpMergeJSON Op = "merge-json"
	// OpImport puts the managed import line for From at the top of the
	// CLAUDE.md To.
	OpImport Op = "import"
	// OpWriteFile creates the file To with Content.
	OpWriteFile Op = "write-file"
	// OpState saves Devpit's sharing record (Content) in the account.
	OpState Op = "state"
)

// Step is one thing a plan will do, in plain words.
type Step struct {
	Op   Op
	Kind Kind
	From string
	To   string
	// Text is the step in plain words, as the preview and the live rows
	// show it.
	Text string
	// AddsToDefault is set when the step adds files to the default
	// account's folder.
	AddsToDefault bool
	// JSON is the merge for OpMergeJSON.
	JSON *JSONMerge
	// Content is the file for OpWriteFile and OpState.
	Content []byte
}

// Plan is everything a change will do. Nothing is applied without one.
type Plan struct {
	Roots Roots
	Steps []Step
	// AddsToDefault lists, in plain words, what the plan adds to the
	// default account's folder. The preview must show it.
	AddsToDefault []string
	// Fallbacks say where a link could not be made and a copy is made
	// instead, and why.
	Fallbacks []string
	// Notes are things left as they are on purpose ("kept work's own
	// model setting").
	Notes []string
	// Warnings are things Devpit will not touch (a link made by something
	// else, a broken link to repair).
	Warnings []string
	// BackupDir is the dated backup folder the plan creates in the account,
	// if any.
	BackupDir string
	// Summary is the change's name in the undo history.
	Summary string
	// Secrets names values the plan copies that may hold secrets (names
	// only). Non-empty only on the Careful path.
	Secrets []string

	build func() (Plan, error)
}

// Empty reports whether the plan changes nothing.
func (p Plan) Empty() bool { return len(p.Steps) == 0 }

// Preview is the plan in plain words, for the preview screen and --json.
func (p Plan) Preview() []string {
	if p.Empty() {
		return []string{"Nothing to change: this account already matches."}
	}
	var out []string
	for _, s := range p.Steps {
		out = append(out, s.Text)
	}
	if len(p.AddsToDefault) > 0 {
		out = append(out, fmt.Sprintf("This adds %d item(s) to the default account's folder %s.", len(p.AddsToDefault), p.Roots.DefaultHome))
	}
	if p.BackupDir != "" {
		out = append(out, "Anything moved aside goes to "+p.BackupDir+". Nothing is deleted or overwritten.")
	}
	out = append(out, p.Fallbacks...)
	out = append(out, p.Notes...)
	out = append(out, p.Warnings...)
	return out
}

// ErrStalePlan is returned when the folders changed since the preview was
// built: look at the preview again.
var ErrStalePlan = errors.New("the folders changed since the preview was made; look at the preview again")

// planner builds a plan. It tracks which paths the steps so far create and
// remove, so names chosen later never collide.
type planner struct {
	inv   *Inventory
	r     Roots
	sel   Selection
	p     Plan
	st    State
	taken map[string]bool
	gone  map[string]bool
}

func newPlanner(inv *Inventory, sel Selection) *planner {
	return &planner{
		inv: inv, r: inv.Roots, sel: sel, st: inv.state.clone(),
		p:     Plan{Roots: inv.Roots},
		taken: map[string]bool{}, gone: map[string]bool{},
	}
}

// BuildPlan turns the item list as picked into a plan. It reads; it changes
// nothing.
func BuildPlan(inv *Inventory, sel Selection) (Plan, error) {
	if inv == nil {
		return Plan{}, errors.New("no item list")
	}
	for k, c := range sel.Choices {
		it, ok := inv.Item(k)
		if !ok {
			return Plan{}, fmt.Errorf("unknown row %q", k)
		}
		if it.Locked {
			return Plan{}, lockedErr(k)
		}
		if c.Mode != ModeSkip && c.Mode != "" && !it.Offers(c.Mode) {
			return Plan{}, fmt.Errorf("%s cannot be set to %s", it.Title, c.Mode.Title())
		}
	}
	pl := newPlanner(inv, sel)
	for _, k := range Kinds() {
		c, ok := sel.Choices[k]
		if !ok || c.Mode == ModeSkip || c.Mode == "" {
			continue
		}
		if c.Policy == "" {
			c.Policy = PolicyKeep
		}
		switch k {
		case KindSkills:
			pl.planSkills(c)
		case KindAgents:
			pl.planFolder(KindAgents, "agents", c)
		case KindCommands:
			pl.planFolder(KindCommands, "commands", c)
		case KindClaudeMD:
			pl.planClaudeMD(c)
		case KindPlugins, KindSettings, KindHooks, KindMCP:
			pl.planJSON(k, c)
		case KindHistory:
			pl.planHistory(c)
		}
	}
	p := pl.finish("Brought the Claude Code setup to " + inv.Roots.TargetName)
	r := inv.Roots
	p.build = func() (Plan, error) {
		fresh, err := Scan(r)
		if err != nil {
			return Plan{}, err
		}
		return BuildPlan(fresh, sel)
	}
	return p, nil
}

// finish adds the sharing record if it changed and names the plan.
func (pl *planner) finish(summary string) Plan {
	pl.st.Source = pl.r.DefaultHome
	nb := pl.st.encode()
	old, err := readFile(statePath(pl.r.Target))
	had := err == nil
	empty := len(pl.st.Shared)+len(pl.st.Local)+len(pl.st.Copied) == 0
	if !bytes.Equal(nb, old) && (had || !empty) {
		pl.add(Step{
			Op: OpState, To: statePath(pl.r.Target), Content: nb,
			Text: "Save what " + pl.r.TargetName + " shares in " + stateFileName,
		})
	}
	pl.p.Summary = summary
	return pl.p
}

func pathKey(p string) string { return strings.ToUpper(filepath.Clean(p)) }

// occupied reports whether p will exist once the steps so far have run.
func (pl *planner) occupied(p string) bool {
	k := pathKey(p)
	if pl.taken[k] {
		return true
	}
	if pl.gone[k] {
		return false
	}
	return exists(p)
}

// freeName returns name, or name-2, name-3... the first that is free in dir.
func (pl *planner) freeName(dir, name string, isFile bool) string {
	for n := 1; ; n++ {
		c := numbered(name, n, isFile)
		if !pl.occupied(filepath.Join(dir, c)) {
			return c
		}
	}
}

func (pl *planner) add(st Step) {
	switch st.Op {
	case OpMove:
		pl.gone[pathKey(st.From)] = true
		delete(pl.taken, pathKey(st.From))
		pl.taken[pathKey(st.To)] = true
		delete(pl.gone, pathKey(st.To))
	case OpUnlink, OpRmdir:
		pl.gone[pathKey(st.To)] = true
		delete(pl.taken, pathKey(st.To))
	default:
		if st.To != "" {
			pl.taken[pathKey(st.To)] = true
			delete(pl.gone, pathKey(st.To))
		}
	}
	if st.AddsToDefault {
		pl.p.AddsToDefault = append(pl.p.AddsToDefault, st.Text)
	}
	pl.p.Steps = append(pl.p.Steps, st)
}

// say names a path the way the preview does: "work's skills\foo" or "the
// default account's skills\foo".
func (pl *planner) say(p string) string {
	if rel, ok := relInside(pl.r.Target, p); ok && rel != "" {
		return pl.r.TargetName + "'s " + rel
	}
	if rel, ok := relInside(pl.r.DefaultHome, p); ok && rel != "" {
		return "the default account's " + rel
	}
	return p
}

func (pl *planner) mkdir(dir string, k Kind) {
	if pl.occupied(dir) {
		return
	}
	adds := pl.r.inSource(dir)
	pl.add(Step{Op: OpMkdir, Kind: k, To: dir, AddsToDefault: adds, Text: "Create the folder " + pl.say(dir)})
}

// backupDir is the dated backup folder of this plan, created on first use.
func (pl *planner) backupDir() string {
	if pl.p.BackupDir == "" {
		name := pl.freeName(pl.r.Target, "devpit-backup-"+pl.r.Now().Format("2006-01-02"), false)
		pl.p.BackupDir = filepath.Join(pl.r.Target, name)
		pl.add(Step{Op: OpMkdir, To: pl.p.BackupDir, Text: "Create the backup folder " + pl.say(pl.p.BackupDir)})
	}
	return pl.p.BackupDir
}

// backupPath is where rel (relative to the account) goes in the backup
// folder, with its folders created.
func (pl *planner) backupPath(k Kind, rel string, isFile bool) string {
	dir := pl.backupDir()
	if parent := filepath.Dir(rel); parent != "." {
		for _, part := range strings.Split(parent, string(filepath.Separator)) {
			dir = filepath.Join(dir, part)
			pl.mkdir(dir, k)
		}
	}
	return filepath.Join(dir, pl.freeName(dir, filepath.Base(rel), isFile))
}

func (pl *planner) wanted(k Kind, name string) bool {
	names := pl.sel.Names[k]
	if len(names) == 0 {
		return true
	}
	for _, n := range names {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

func (pl *planner) named(k Kind, name string) bool {
	for _, n := range pl.sel.Names[k] {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

func (pl *planner) move(k Kind, from, to, text string) {
	pl.add(Step{Op: OpMove, Kind: k, From: from, To: to, Text: text, AddsToDefault: pl.r.inSource(to)})
}

func (pl *planner) link(k Kind, at, to string) {
	pl.add(Step{
		Op: OpLink, Kind: k, From: to, To: at,
		Text: "Link " + pl.say(at) + " to " + pl.say(to) + " (one shared copy)",
	})
}

func (pl *planner) copyTo(k Kind, from, to string) {
	pl.add(Step{Op: OpCopy, Kind: k, From: from, To: to, Text: "Copy " + pl.say(from) + " to " + pl.say(to)})
}

// materialise turns the link at link (to src) into a real copy of what it
// shows now: copy to a temporary sibling, remove the link only, rename the
// copy into place. Each step is journalled, so an interruption anywhere is
// put right on the next run.
func (pl *planner) materialise(k Kind, link, src string) {
	dir := filepath.Dir(link)
	tmp := filepath.Join(dir, pl.freeName(dir, filepath.Base(link)+".devpit-unshare", false))
	pl.add(Step{
		Op: OpCopy, Kind: k, From: src, To: tmp,
		Text: "Copy what " + pl.say(link) + " shows now to " + pl.say(tmp),
	})
	pl.add(Step{
		Op: OpUnlink, Kind: k, From: src, To: link,
		Text: "Remove the link " + pl.say(link) + " (the link only; the default account's copy stays)",
	})
	pl.add(Step{
		Op: OpMove, Kind: k, From: tmp, To: link,
		Text: "Put the copy in place as " + pl.say(link),
	})
}

// sourceHasLogin refuses to share or move a tree that holds a login file.
func (pl *planner) refuseLogin(p string) bool {
	if containsDenied(p) {
		pl.p.Warnings = append(pl.p.Warnings, p+" holds a login or account file, so Devpit will not share or move it.")
		return true
	}
	return false
}

func isFileAt(p string) bool {
	fi, err := lstat(p)
	return err == nil && !fi.IsDir()
}

// shareMode returns the mode to plan for a row: Share falls back to Copy
// when no link can be made.
func (pl *planner) shareMode(it Item, c Choice) Mode {
	if c.Mode == ModeShare && pl.sel.CopyInsteadOfLinks && it.Kind != KindClaudeMD {
		pl.p.Fallbacks = append(pl.p.Fallbacks, it.Title+" will be copied, not linked: Windows would not make a link in this account folder.")
		return ModeCopy
	}
	if c.Mode == ModeShare && !it.LinkOK {
		pl.p.Fallbacks = append(pl.p.Fallbacks, it.Title+" will be copied, not linked: "+it.LinkWhy+".")
		return ModeCopy
	}
	return c.Mode
}

func (pl *planner) planSkills(c Choice) {
	it, _ := pl.inv.Item(KindSkills)
	if len(it.Modes) == 1 {
		return
	}
	mode := pl.shareMode(it, c)
	src, dst := pl.r.srcPath("skills"), pl.r.dstPath("skills")
	acct := pl.r.TargetName
	for _, e := range it.Entries {
		if !pl.wanted(KindSkills, e.Name) {
			continue
		}
		s := filepath.Join(src, e.Name)
		t := e.TargetPath
		state := e.State
		if state == EntryKeptLocal && pl.named(KindSkills, e.Name) {
			state = EntryTargetOnly // "Share instead" on a skill kept local
		}
		switch state {
		case EntryBroken:
			pl.p.Warnings = append(pl.p.Warnings, "skills\\"+e.Name+" links to "+e.LinkTarget+", which is gone. Repair it from the account page.")
			continue
		case EntryForeignLink:
			pl.p.Warnings = append(pl.p.Warnings, "skills\\"+e.Name+" is a link made by something else (to "+e.LinkTarget+"). Devpit leaves it alone.")
			continue
		}
		if mode == ModeCopy {
			pl.copySkill(e, state, s, t, dst, c.Policy)
			continue
		}
		switch state {
		case EntrySourceOnly:
			if pl.refuseLogin(s) {
				continue
			}
			pl.mkdir(dst, KindSkills)
			pl.link(KindSkills, filepath.Join(dst, e.Name), s)
			pl.st.setLocal(e.Name, false)
		case EntrySame:
			if pl.refuseLogin(s) {
				continue
			}
			pl.move(KindSkills, t, pl.backupPath(KindSkills, filepath.Join("skills", filepath.Base(t)), false),
				"Move "+pl.say(t)+" (the same as the default's) to the backup folder")
			pl.link(KindSkills, filepath.Join(dst, e.Name), s)
		case EntryDiffers:
			if pl.refuseLogin(s) {
				continue
			}
			switch c.Policy {
			case PolicyReplace:
				pl.move(KindSkills, t, pl.backupPath(KindSkills, filepath.Join("skills", filepath.Base(t)), false),
					"Move "+pl.say(t)+" to the backup folder (the shared one replaces it)")
				pl.link(KindSkills, filepath.Join(dst, e.Name), s)
			case PolicyKeepBoth:
				if pl.refuseLogin(t) {
					continue
				}
				nn := pl.freeBoth(src, dst, withTag(e.Name, "-", acct, false))
				pl.move(KindSkills, t, filepath.Join(src, nn),
					"Move "+pl.say(t)+" into the default account as skills\\"+nn+" (shared from now on)")
				pl.link(KindSkills, filepath.Join(dst, e.Name), s)
				pl.link(KindSkills, filepath.Join(dst, nn), filepath.Join(src, nn))
			default:
				nn := pl.freeName(dst, withTag(e.Name, ".from-", acct, false), false)
				pl.move(KindSkills, t, filepath.Join(dst, nn), "Rename "+pl.say(t)+" to skills\\"+nn+" (kept in "+acct+", not shared)")
				pl.st.setLocal(nn, true)
				pl.link(KindSkills, filepath.Join(dst, e.Name), s)
			}
		case EntryTargetOnly:
			if c.Policy == PolicyReplace && !pl.named(KindSkills, e.Name) {
				pl.st.setLocal(e.Name, true)
				pl.p.Notes = append(pl.p.Notes, "skills\\"+e.Name+" stays in "+acct+" only.")
				continue
			}
			if pl.refuseLogin(t) {
				continue
			}
			pl.mkdir(src, KindSkills)
			pl.move(KindSkills, t, filepath.Join(src, e.Name),
				"Move "+pl.say(t)+" into the default account (shared from now on)")
			pl.link(KindSkills, filepath.Join(dst, e.Name), filepath.Join(src, e.Name))
			pl.st.setLocal(e.Name, false)
		case EntryLinked:
			pl.st.setLocal(e.Name, false)
		}
	}
	if len(pl.sel.Names[KindSkills]) == 0 {
		// The whole row was picked: from now on EnsureSkillLinks follows it.
		pl.st.setShared(KindSkills, mode == ModeShare)
	}
}

// freeBoth finds a name free in both folders.
func (pl *planner) freeBoth(a, b, name string) string {
	for n := 1; ; n++ {
		c := numbered(name, n, false)
		if !pl.occupied(filepath.Join(a, c)) && !pl.occupied(filepath.Join(b, c)) {
			return c
		}
	}
}

func (pl *planner) copySkill(e Entry, state EntryState, s, t, dst string, pol Policy) {
	switch state {
	case EntrySourceOnly:
		pl.mkdir(dst, KindSkills)
		pl.copyTo(KindSkills, s, filepath.Join(dst, e.Name))
		pl.st.setCopied("skills/"+e.Name, true)
	case EntryLinked:
		pl.materialise(KindSkills, t, s)
		pl.st.setCopied("skills/"+e.Name, true)
	case EntryDiffers:
		pl.copyConflict(KindSkills, dst, filepath.Base(t), s, t, filepath.Join("skills", filepath.Base(t)), pol, false)
	}
}

// copyConflict copies s over a differing t without overwriting anything.
func (pl *planner) copyConflict(k Kind, dir, name, s, t, rel string, pol Policy, isFile bool) {
	switch pol {
	case PolicyReplace:
		pl.move(k, t, pl.backupPath(k, rel, isFile), "Move "+pl.say(t)+" to the backup folder")
		pl.copyTo(k, s, t)
	case PolicyKeepBoth:
		nn := pl.freeName(dir, withTag(name, "-", pl.r.TargetName, isFile), isFile)
		pl.move(k, t, filepath.Join(dir, nn), "Rename "+pl.say(t)+" to "+nn)
		pl.copyTo(k, s, t)
	default:
		if pl.besideSame(dir, withTag(name, ".from-", SourceName, isFile), s, isFile) {
			return
		}
		nn := pl.freeName(dir, withTag(name, ".from-", SourceName, isFile), isFile)
		pl.add(Step{
			Op: OpCopy, Kind: k, From: s, To: filepath.Join(dir, nn),
			Text: "Keep " + pl.say(t) + " as it is and put the default's copy beside it as " + nn,
		})
	}
}

// besideSame reports whether a copy of s already sits beside under base (or
// base-2, base-3...), so running twice changes nothing.
func (pl *planner) besideSame(dir, base, s string, isFile bool) bool {
	for n := 1; n < 50; n++ {
		p := filepath.Join(dir, numbered(base, n, isFile))
		if !exists(p) {
			if n > 1 {
				return false
			}
			continue
		}
		if same, err := sameContent(s, p); err == nil && same {
			return true
		}
	}
	return false
}

// copyMerge copies src into an existing dst entry by entry.
func (pl *planner) copyMerge(k Kind, src, dst, rel string, pol Policy) {
	keep := func(name string, _ os.FileInfo) bool { return !deniedAnywhere(name) }
	dents := map[string]dirEntry{}
	for _, d := range listEntries(dst, keep) {
		dents[strings.ToUpper(d.name)] = d
	}
	for _, s := range listEntries(src, keep) {
		if rel == "" && deniedTop(s.name) {
			continue
		}
		if s.link.isLink() {
			pl.p.Warnings = append(pl.p.Warnings, s.path+" is a link; links are never copied.")
			continue
		}
		d, ok := dents[strings.ToUpper(s.name)]
		switch {
		case !ok:
			pl.copyTo(k, s.path, filepath.Join(dst, s.name))
		case d.link.isLink():
			pl.p.Warnings = append(pl.p.Warnings, d.path+" is a link made by something else. Devpit leaves it alone.")
		case s.fi.IsDir() && d.fi.IsDir():
			pl.copyMerge(k, s.path, d.path, filepath.Join(rel, d.name), pol)
		default:
			if same, err := sameContent(s.path, d.path); err == nil && same {
				continue
			}
			pl.copyConflict(k, dst, d.name, s.path, d.path, filepath.Join(rel, d.name), pol, !d.fi.IsDir())
		}
	}
}

// planFolder plans agents\ or commands\: one link for the whole folder.
func (pl *planner) planFolder(k Kind, sub string, c Choice) {
	it, _ := pl.inv.Item(k)
	if len(it.Modes) == 1 {
		return
	}
	src, dst := pl.r.srcPath(sub), pl.r.dstPath(sub)
	mode := pl.shareMode(it, c)
	if li, err := readLink(dst); err == nil && li.isLink() {
		e := it.Entries[0]
		switch {
		case e.State == EntryLinked && mode == ModeCopy:
			pl.materialise(k, dst, src)
			pl.st.setCopied(sub, true)
		case e.State == EntryLinked:
		case e.State == EntryBroken:
			pl.p.Warnings = append(pl.p.Warnings, sub+"\\ links to "+e.LinkTarget+", which is gone. Repair it from the account page.")
		default:
			pl.p.Warnings = append(pl.p.Warnings, sub+"\\ is a link made by something else. Devpit leaves it alone.")
		}
		pl.st.setShared(k, mode == ModeShare && e.State == EntryLinked)
		return
	}
	if mode == ModeShare {
		if why := pl.folderBlocker(dst); why != "" {
			pl.p.Fallbacks = append(pl.p.Fallbacks, it.Title+" will be copied, not linked: "+why+".")
			mode = ModeCopy
		}
	}
	if mode == ModeCopy {
		switch {
		case !exists(src):
		case !exists(dst):
			pl.copyTo(k, src, dst)
			pl.st.setCopied(sub, true)
		default:
			pl.copyMerge(k, src, dst, sub, c.Policy)
			pl.st.setCopied(sub, true)
		}
		pl.st.setShared(k, false)
		return
	}
	if pl.refuseLogin(src) {
		return
	}
	pl.mkdir(src, k)
	if isRealDir(dst) {
		acct := pl.r.TargetName
		for _, e := range it.Entries {
			t := e.TargetPath
			if t == "" {
				continue
			}
			isFile := isFileAt(t)
			switch e.State {
			case EntrySame:
				pl.move(k, t, pl.backupPath(k, filepath.Join(sub, filepath.Base(t)), isFile),
					"Move "+pl.say(t)+" (the same as the default's) to the backup folder")
			case EntryDiffers, EntryTargetOnly:
				if c.Policy == PolicyReplace {
					pl.move(k, t, pl.backupPath(k, filepath.Join(sub, filepath.Base(t)), isFile),
						"Move "+pl.say(t)+" to the backup folder (the shared folder replaces it)")
					continue
				}
				name := filepath.Base(t)
				if e.State == EntryDiffers {
					sep := ".from-"
					if c.Policy == PolicyKeepBoth {
						sep = "-"
					}
					name = withTag(name, sep, acct, isFile)
				}
				nn := pl.freeName(src, name, isFile)
				pl.move(k, t, filepath.Join(src, nn),
					"Move "+pl.say(t)+" into the default account as "+sub+"\\"+nn+" (shared from now on)")
			}
		}
		pl.add(Step{Op: OpRmdir, Kind: k, To: dst, Text: "Remove the now empty folder " + pl.say(dst)})
	}
	pl.link(k, dst, src)
	pl.st.setShared(k, true)
}

// folderBlocker says why a real agents\ or commands\ folder cannot be
// replaced by a link: something in it Devpit will not move.
func (pl *planner) folderBlocker(dst string) string {
	if !isRealDir(dst) {
		return ""
	}
	ents, err := readDir(dst)
	if err != nil {
		return "Devpit could not read " + dst
	}
	for _, e := range ents {
		p := filepath.Join(dst, e.Name())
		fi, err := lstat(p)
		if err != nil {
			return "Devpit could not read " + p
		}
		if isReparse(fi) {
			return p + " is a link made by something else"
		}
		if deniedAnywhere(e.Name()) || containsDenied(p) {
			return p + " holds a login or account file"
		}
	}
	return ""
}

func (pl *planner) planClaudeMD(c Choice) {
	it, _ := pl.inv.Item(KindClaudeMD)
	if len(it.Entries) == 0 {
		return // neither account has a CLAUDE.md
	}
	src, dst := pl.r.srcPath("CLAUDE.md"), pl.r.dstPath("CLAUDE.md")
	state := EntrySourceOnly
	if len(it.Entries) > 0 {
		state = it.Entries[0].State
	}
	if state == EntryForeignLink {
		pl.p.Warnings = append(pl.p.Warnings, "CLAUDE.md in "+pl.r.TargetName+" is a link made by something else. Devpit leaves it alone.")
		return
	}
	imp := func(create bool) {
		text := "Add one line at the top of " + pl.say(dst) + " that brings in the default account's CLAUDE.md; its own text stays below"
		if create {
			text = "Create " + pl.say(dst) + " with one line that brings in the default account's CLAUDE.md"
		}
		pl.add(Step{Op: OpImport, Kind: KindClaudeMD, From: src, To: dst, Text: text})
	}
	if c.Mode == ModeShare {
		if !exists(src) {
			pl.p.Notes = append(pl.p.Notes, "The default account has no CLAUDE.md yet; once it has one, "+pl.r.TargetName+" picks it up.")
		}
		switch state {
		case EntryLinked:
		case EntryBroken:
			pl.p.Warnings = append(pl.p.Warnings, "CLAUDE.md in "+pl.r.TargetName+" brings in a file that is gone. Repair it from the account page.")
		case EntrySourceOnly:
			imp(true)
		case EntrySame:
			pl.move(KindClaudeMD, dst, pl.backupPath(KindClaudeMD, "CLAUDE.md", true), "Move "+pl.say(dst)+" (the same as the default's) to the backup folder")
			imp(true)
		case EntryDiffers, EntryTargetOnly:
			if c.Policy == PolicyReplace {
				pl.move(KindClaudeMD, dst, pl.backupPath(KindClaudeMD, "CLAUDE.md", true), "Move "+pl.say(dst)+" to the backup folder")
				imp(true)
			} else {
				imp(false)
			}
		}
		pl.st.setShared(KindClaudeMD, state != EntryBroken)
		return
	}
	switch state {
	case EntryLinked, EntryBroken:
		pl.inlineClaudeMD(src, dst)
	case EntrySourceOnly:
		pl.copyTo(KindClaudeMD, src, dst)
		pl.st.setCopied("claude-md", true)
	case EntryDiffers:
		pl.copyConflict(KindClaudeMD, pl.r.Target, "CLAUDE.md", src, dst, "CLAUDE.md", c.Policy, true)
		pl.st.setCopied("claude-md", true)
	}
	pl.st.setShared(KindClaudeMD, false)
}

// inlineClaudeMD turns a shared CLAUDE.md into the account's own: the old
// file goes to the backup folder, and a new one holds the default's text
// followed by the account's own.
func (pl *planner) inlineClaudeMD(src, dst string) {
	own, _ := readFile(dst)
	def, _ := readFile(src)
	body := withoutBlock(own)
	var content []byte
	content = append(content, def...)
	if len(def) > 0 && len(body) > 0 {
		nl := lineEnding(def)
		if !bytes.HasSuffix(def, []byte(nl)) {
			content = append(content, nl...)
		}
		content = append(content, nl...)
	}
	content = append(content, bytes.TrimPrefix(body, utf8BOM)...)
	pl.move(KindClaudeMD, dst, pl.backupPath(KindClaudeMD, "CLAUDE.md", true), "Move "+pl.say(dst)+" to the backup folder")
	pl.add(Step{
		Op: OpWriteFile, Kind: KindClaudeMD, To: dst, Content: content,
		Text: "Write " + pl.say(dst) + " with the default account's CLAUDE.md text, then " + pl.r.TargetName + "'s own",
	})
	pl.st.setCopied("claude-md", true)
}

// container is where a JSON row's members live.
func jsonContainer(k Kind, section string) []string {
	switch k {
	case KindSettings:
		return nil
	case KindHooks:
		return []string{hooksKey}
	case KindMCP:
		return []string{"mcpServers"}
	}
	return []string{section}
}

func (pl *planner) planJSON(k Kind, c Choice) {
	it, _ := pl.inv.Item(k)
	if len(it.Modes) == 1 {
		return
	}
	file, source := pl.r.dstPath(settingsFile), pl.r.srcPath(settingsFile)
	if k == KindMCP {
		file, source = pl.r.TargetClaudeJSON(), pl.r.DefaultClaudeJSON
	}
	acct := pl.r.TargetName
	var ops []MemberOp
	var names, secrets []string
	targetNames := map[string]bool{}
	for _, e := range it.Entries {
		targetNames[e.Section+"\x00"+e.Name] = e.State != EntrySourceOnly
	}
	for _, e := range it.Entries {
		if !pl.wanted(k, e.Name) {
			continue
		}
		path := append(append([]string(nil), jsonContainer(k, e.Section)...), e.Name)
		if e.Secret && !pl.sel.AllowSecrets {
			if e.State == EntrySourceOnly || e.State == EntryDiffers {
				pl.p.Notes = append(pl.p.Notes, e.Name+" was left out: it may hold a secret. It is copied only on the Careful path.")
			}
			continue
		}
		var op *MemberOp
		switch e.State {
		case EntrySourceOnly:
			op = &MemberOp{Path: path, Action: ActAdd, Secret: e.Secret}
		case EntryDiffers:
			switch {
			case c.Policy == PolicyReplace:
				op = &MemberOp{Path: path, Action: ActReplace, Secret: e.Secret}
			case c.Policy == PolicyKeepBoth && k == KindHooks:
				op = &MemberOp{Path: path, Action: ActAppend}
			case c.Policy == PolicyKeepBoth && k == KindMCP:
				nn := e.Name + "-from-" + SourceName
				for n := 2; targetNames[e.Section+"\x00"+nn]; n++ {
					nn = fmt.Sprintf("%s-from-%s-%d", e.Name, SourceName, n)
				}
				targetNames[e.Section+"\x00"+nn] = true
				op = &MemberOp{Path: path, Action: ActAddAs, NewName: nn, Secret: e.Secret}
			default:
				pl.p.Notes = append(pl.p.Notes, "Kept "+acct+"'s own "+e.Name+".")
			}
		}
		if op == nil {
			continue
		}
		ops = append(ops, *op)
		label := e.Name
		if op.Action == ActAddAs {
			label += " (as " + op.NewName + ")"
		}
		names = append(names, label)
		if e.Secret {
			secrets = append(secrets, e.Name)
		}
	}
	if len(ops) == 0 {
		return
	}
	m := &JSONMerge{File: file, Source: source, Ops: ops}
	for _, op := range ops {
		if op.Action == ActReplace {
			dir := pl.backupDir()
			m.Sidecar = filepath.Join(dir, pl.freeName(dir, "replaced-"+string(k)+".json", true))
			pl.taken[pathKey(m.Sidecar)] = true
			break
		}
	}
	what := map[Kind]string{
		KindPlugins: "the same plugins and marketplaces", KindSettings: "settings",
		KindHooks: "hooks", KindMCP: "MCP servers",
	}[k]
	text := fmt.Sprintf("Copy %s into %s: %s", what, pl.say(file), strings.Join(names, ", "))
	if k == KindPlugins {
		text += " (each account installs its own copy)"
	}
	if m.Sidecar != "" {
		text += "; the values replaced are kept in " + pl.say(m.Sidecar)
	}
	pl.add(Step{Op: OpMergeJSON, Kind: k, From: source, To: file, JSON: m, Text: text})
	pl.p.Secrets = append(pl.p.Secrets, secrets...)
	pl.st.setCopied(string(k), true)
}

func (pl *planner) planHistory(c Choice) {
	if c.Mode != ModeCopy {
		return
	}
	sp, dp := pl.r.srcPath("projects"), pl.r.dstPath("projects")
	switch {
	case !isRealDir(sp):
	case !exists(dp):
		pl.copyTo(KindHistory, sp, dp)
	case isRealDir(dp):
		pl.copyMerge(KindHistory, sp, dp, "projects", c.Policy)
	default:
		pl.p.Warnings = append(pl.p.Warnings, dp+" is a link made by something else. Devpit leaves it alone.")
	}
	sh, dh := pl.r.srcPath("history.jsonl"), pl.r.dstPath("history.jsonl")
	if isFileAt(sh) {
		switch {
		case !exists(dh):
			pl.copyTo(KindHistory, sh, dh)
		default:
			if same, err := equalFiles(sh, dh); err != nil || !same {
				pl.copyConflict(KindHistory, pl.r.Target, "history.jsonl", sh, dh, "history.jsonl", c.Policy, true)
			}
		}
	}
	pl.st.setCopied("history", true)
}
