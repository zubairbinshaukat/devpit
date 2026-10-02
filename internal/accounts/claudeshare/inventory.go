package claudeshare

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tailscale/hujson"
)

// skillsSynced is the per-account skills folder that is never shared.
const skillsSynced = "synced"

// settingsFile is settings.json in an account folder.
const settingsFile = "settings.json"

// Keys of settings.json that belong to other rows.
var (
	pluginSections = []string{"enabledPlugins", "extraKnownMarketplaces"}
	hooksKey       = "hooks"
)

// secretKeys are settings keys that may hold or produce a secret. Their
// names are shown; they are copied only on the Careful path.
func secretKeys() []string {
	return []string{"env", "apiKeyHelper", "awsAuthRefresh", "awsCredentialExport", "otelHeadersHelper"}
}

func isSecretKey(k string) bool {
	for _, s := range secretKeys() {
		if strings.EqualFold(s, k) {
			return true
		}
	}
	return false
}

// Scan builds the item list for bringing the default account's setup to
// the account in r. It reads; it changes nothing. It never opens a login
// file: the locked rows only check whether one is there.
func Scan(r Roots) (*Inventory, error) {
	r, err := r.check()
	if err != nil {
		return nil, err
	}
	st, err := readState(r.Target)
	inv := &Inventory{Roots: r, state: st}
	if err != nil {
		inv.Warnings = append(inv.Warnings, err.Error())
	}
	if !isRealDir(r.DefaultHome) {
		inv.Warnings = append(inv.Warnings, "The default account's folder "+r.DefaultHome+" is missing, so there is nothing to bring over yet.")
	}
	inv.Items = []Item{
		inv.scanSkills(),
		inv.scanFolder(KindAgents, "agents"),
		inv.scanFolder(KindCommands, "commands"),
		inv.scanClaudeMD(),
		inv.scanPlugins(),
		inv.scanSettings(),
		inv.scanHooks(),
		inv.scanMCP(),
		inv.scanHistory(),
		inv.lockedRow(KindLogin, ".credentials.json",
			"Each account keeps its own sign-in. Devpit never copies, links or opens it."),
		inv.lockedRow(KindAccountInfo, ".claude.json",
			"The account's identity stays its own. Only MCP server entries are ever copied out of it, when you pick that row."),
	}
	return inv, nil
}

func baseItem(k Kind, label Label, def Mode, modes ...Mode) Item {
	it := Item{Kind: k, Title: k.Title(), Label: label, Default: def, Modes: modes, Asks: 1}
	if label == LabelCareful {
		it.Asks = 2
	}
	return it
}

func (inv *Inventory) lockedRow(k Kind, file, note string) Item {
	it := baseItem(k, LabelLocked, ModeLocked, ModeLocked)
	it.Locked, it.Asks = true, 0
	it.Note = note
	src := inv.Roots.srcPath(file)
	if k == KindAccountInfo {
		src = inv.Roots.DefaultClaudeJSON
	}
	// Only whether the file is there: it is never opened.
	_, serr := os.Lstat(fsPath(src))
	_, terr := os.Lstat(fsPath(inv.Roots.dstPath(file)))
	it.Source = Side{Exists: serr == nil, Path: src}
	it.Target = Side{Exists: terr == nil, Path: inv.Roots.dstPath(file)}
	return it
}

// dirEntries lists a folder's entries by name, with what each is.
type dirEntry struct {
	name string
	path string
	fi   os.FileInfo
	link linkInfo
}

func listEntries(dir string, keep func(name string, fi os.FileInfo) bool) []dirEntry {
	ents, _ := readDir(dir)
	var out []dirEntry
	for _, e := range ents {
		p := filepath.Join(dir, e.Name())
		fi, err := lstat(p)
		if err != nil {
			continue
		}
		if keep != nil && !keep(e.Name(), fi) {
			continue
		}
		de := dirEntry{name: e.Name(), path: p, fi: fi}
		if isReparse(fi) {
			de.link, _ = readLink(p)
			if !de.link.isLink() {
				de.link.kind = linkOther
			}
		}
		out = append(out, de)
	}
	return out
}

// pair joins two listings by name, ignoring case as NTFS does.
type pairEntry struct {
	name     string
	src, dst *dirEntry
}

func pairUp(src, dst []dirEntry) []pairEntry {
	idx := map[string]int{}
	var out []pairEntry
	for i := range src {
		k := strings.ToUpper(src[i].name)
		idx[k] = len(out)
		out = append(out, pairEntry{name: src[i].name, src: &src[i]})
	}
	for i := range dst {
		k := strings.ToUpper(dst[i].name)
		if j, ok := idx[k]; ok {
			out[j].dst = &dst[i]
			continue
		}
		out = append(out, pairEntry{name: dst[i].name, dst: &dst[i]})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToUpper(out[i].name) < strings.ToUpper(out[j].name) })
	return out
}

// classify says where one named entry stands. srcPath is where the default's
// copy is (or would be).
func (inv *Inventory) classify(p pairEntry, srcPath string, local bool) Entry {
	e := Entry{Name: p.name, SourcePath: srcPath}
	if p.dst != nil {
		e.TargetPath = p.dst.path
	}
	if p.src != nil {
		e.Size = measure(p.src.path).Bytes
	}
	if p.dst != nil && p.dst.link.isLink() {
		e.LinkTarget = p.dst.link.target
		switch {
		case p.dst.link.kind == linkJunction && samePath(p.dst.link.target, srcPath):
			e.State = EntryLinked
			if p.src == nil || !exists(srcPath) {
				e.State = EntryBroken
			}
		case p.dst.link.kind == linkJunction && !exists(p.dst.link.target) &&
			strings.EqualFold(filepath.Base(p.dst.link.target), filepath.Base(srcPath)):
			// A link of ours to a default home that has moved.
			e.State = EntryBroken
		default:
			e.State = EntryForeignLink
		}
		return e
	}
	switch {
	case p.src != nil && p.dst != nil:
		e.State = EntryDiffers
		if p.src.link.isLink() {
			break
		}
		if same, err := sameContent(p.src.path, p.dst.path); err == nil && same {
			e.State = EntrySame
		}
	case p.src != nil:
		e.State = EntrySourceOnly
	default:
		e.State = EntryTargetOnly
		e.Size = measure(p.dst.path).Bytes
		if local {
			e.State = EntryKeptLocal
		}
	}
	return e
}

func (inv *Inventory) linkCheck(it *Item, sub string) {
	it.LinkOK, it.LinkWhy = inv.Roots.linkable(nearestExisting(inv.Roots.srcPath(sub)), nearestExisting(inv.Roots.dstPath(sub)))
}

func (inv *Inventory) scanSkills() Item {
	r := inv.Roots
	it := baseItem(KindSkills, LabelSafe, ModeShare, ModeShare, ModeCopy, ModeSkip)
	it.Note = "One link per skill folder. skills\\synced stays per account."
	src, dst := r.srcPath("skills"), r.dstPath("skills")
	it.Source = Side{Exists: isRealDir(src) || exists(src), Path: src}
	it.Target = Side{Exists: exists(dst), Path: dst}
	inv.linkCheck(&it, "skills")
	if li, err := readLink(dst); err == nil && li.isLink() {
		it.Note = "skills\\ in this account is itself a link to " + li.target + ", made by something else. Devpit leaves it alone."
		it.Modes = []Mode{ModeSkip}
		it.Default = ModeSkip
		inv.Warnings = append(inv.Warnings, it.Note)
		return it
	}
	keep := func(name string, fi os.FileInfo) bool {
		if strings.EqualFold(name, skillsSynced) || deniedAnywhere(name) {
			return false
		}
		return fi.IsDir() || isReparse(fi)
	}
	pairs := pairUp(listEntries(src, keep), listEntries(dst, keep))
	for _, p := range pairs {
		e := inv.classify(p, filepath.Join(src, p.name), inv.state.isLocal(p.name))
		inv.tally(&it, e, p.src != nil)
	}
	it.Shared = inv.state.shares(KindSkills) || it.Target.Links > 0
	return it
}

// tally adds an entry to the row's counts. A link counts as a link and
// zero bytes in the account, so shared content is never counted twice.
func (inv *Inventory) tally(it *Item, e Entry, inSource bool) {
	it.Entries = append(it.Entries, e)
	if inSource {
		it.Source.Count++
		it.Source.Size += e.Size
	}
	switch e.State {
	case EntryLinked, EntryBroken, EntryForeignLink:
		it.Target.Links++
		it.Target.Count++
	case EntrySame, EntryDiffers:
		it.Target.Count++
		it.Target.Size += measure(e.TargetPath).Bytes
	case EntryTargetOnly, EntryKeptLocal:
		it.Target.Count++
		it.Target.Size += e.Size
	}
	if e.State == EntryDiffers {
		it.Conflicts++
	}
}

// scanFolder is agents\ or commands\: shared as one link to the whole folder.
func (inv *Inventory) scanFolder(k Kind, sub string) Item {
	r := inv.Roots
	it := baseItem(k, LabelSafe, ModeShare, ModeShare, ModeCopy, ModeSkip)
	it.Note = "One link for the whole " + sub + "\\ folder."
	src, dst := r.srcPath(sub), r.dstPath(sub)
	it.Source = Side{Exists: exists(src), Path: src}
	it.Target = Side{Exists: exists(dst), Path: dst}
	inv.linkCheck(&it, sub)
	keep := func(name string, _ os.FileInfo) bool { return !deniedAnywhere(name) }
	if li, err := readLink(dst); err == nil && li.isLink() {
		e := inv.classify(pairEntry{name: sub, src: &dirEntry{name: sub, path: src}, dst: &dirEntry{name: sub, path: dst, link: li}}, src, false)
		if !exists(src) {
			e.State = EntryBroken
			if li.kind != linkJunction || !samePath(li.target, src) {
				e.State = EntryForeignLink
			}
		}
		it.Entries = append(it.Entries, e)
		it.Target.Links = 1
		it.Source.Count = len(listEntries(src, keep))
		it.Source.Size = measure(src).Bytes
		switch e.State {
		case EntryLinked:
			it.Shared = true
			it.Target.Count = it.Source.Count
		case EntryForeignLink:
			it.Note = sub + "\\ in this account is a link to " + li.target + ", made by something else. Devpit leaves it alone."
			it.Modes, it.Default = []Mode{ModeSkip}, ModeSkip
			inv.Warnings = append(inv.Warnings, it.Note)
		}
		return it
	}
	pairs := pairUp(listEntries(src, keep), listEntries(dst, keep))
	for _, p := range pairs {
		inv.tally(&it, inv.classify(p, filepath.Join(src, p.name), false), p.src != nil)
	}
	return it
}

func (inv *Inventory) scanClaudeMD() Item {
	r := inv.Roots
	it := baseItem(KindClaudeMD, LabelSafe, ModeShare, ModeShare, ModeCopy, ModeSkip)
	it.Note = "Shared with one import line at the top of this account's CLAUDE.md; its own text stays below."
	it.LinkOK = true
	src, dst := r.srcPath("CLAUDE.md"), r.dstPath("CLAUDE.md")
	sfi, serr := lstat(src)
	dfi, derr := lstat(dst)
	it.Source = Side{Exists: serr == nil, Path: src}
	it.Target = Side{Exists: derr == nil, Path: dst}
	e := Entry{Name: "CLAUDE.md", SourcePath: src, TargetPath: dst}
	if serr == nil {
		e.Size = sfi.Size()
		it.Source.Count, it.Source.Size = 1, sfi.Size()
	}
	if derr == nil {
		it.Target.Count, it.Target.Size = 1, dfi.Size()
	}
	switch {
	case derr == nil && isReparse(dfi):
		e.State = EntryForeignLink
		e.LinkTarget = func() string { li, _ := readLink(dst); return li.target }()
		it.Modes, it.Default = []Mode{ModeSkip}, ModeSkip
	case derr == nil:
		b, err := readFile(dst)
		if err != nil {
			e.State = EntryDiffers
			break
		}
		if ok, path := hasImportBlock(b); ok {
			e.State = EntryLinked
			e.LinkTarget = path
			it.Shared = true
			if serr != nil || !samePath(path, src) {
				e.State = EntryBroken
			}
			it.Target.Links = 1
			break
		}
		switch {
		case serr != nil:
			e.State = EntryTargetOnly
		default:
			e.State = EntryDiffers
			if same, err := equalFiles(src, dst); err == nil && same {
				e.State = EntrySame
			}
		}
	case serr == nil:
		e.State = EntrySourceOnly
	default:
		return it
	}
	if e.State == EntryDiffers {
		it.Conflicts++
	}
	it.Entries = append(it.Entries, e)
	return it
}

// jsonSides loads the default's and the account's JSON files for a row.
func (inv *Inventory) jsonSides(it *Item, src, dst string) (*jsonDoc, *jsonDoc, bool) {
	it.Source.Path, it.Target.Path = src, dst
	sd, err := loadDoc(src)
	if err != nil {
		it.Note = "The default account's " + filepath.Base(src) + " could not be read, so Devpit will not use it: " + err.Error()
		it.Modes, it.Default = []Mode{ModeSkip}, ModeSkip
		return nil, nil, false
	}
	dd, err := loadDoc(dst)
	if err != nil {
		it.Note = "This account's " + filepath.Base(dst) + " could not be read, so Devpit will not change it: " + err.Error()
		it.Modes, it.Default = []Mode{ModeSkip}, ModeSkip
		return nil, nil, false
	}
	it.Source.Exists, it.Target.Exists = !sd.missing, !dd.missing
	return sd, dd, true
}

// jsonEntries compares the members of one object in two files.
func jsonEntries(sd, dd *jsonDoc, path []string, section string, skip func(string) bool, secret func(string, *hujson.Value) bool) ([]Entry, error) {
	snames, svals, err := sd.members(path)
	if err != nil {
		return nil, err
	}
	dnames, dvals, err := dd.members(path)
	if err != nil {
		return nil, err
	}
	var out []Entry
	seen := map[string]bool{}
	for _, n := range snames {
		if skip != nil && skip(n) {
			continue
		}
		seen[n] = true
		e := Entry{Name: n, Section: section}
		if secret != nil {
			e.Secret = secret(n, svals[n])
		}
		e.Size = int64(len(trimmed(*svals[n])))
		switch dv, ok := dvals[n]; {
		case !ok:
			e.State = EntrySourceOnly
		case sameValue(*svals[n], *dv):
			e.State = EntrySame
		default:
			e.State = EntryDiffers
		}
		out = append(out, e)
	}
	for _, n := range dnames {
		if seen[n] || (skip != nil && skip(n)) {
			continue
		}
		out = append(out, Entry{Name: n, Section: section, State: EntryTargetOnly})
	}
	return out, nil
}

func (inv *Inventory) addJSONEntries(it *Item, es []Entry) {
	for _, e := range es {
		it.Entries = append(it.Entries, e)
		if e.State != EntryTargetOnly {
			it.Source.Count++
			it.Source.Size += e.Size
		}
		if e.State != EntrySourceOnly {
			it.Target.Count++
		}
		if e.State == EntryDiffers {
			it.Conflicts++
		}
		if e.Secret {
			it.Secrets = append(it.Secrets, e.Name)
		}
	}
}

func (inv *Inventory) scanPlugins() Item {
	r := inv.Roots
	it := baseItem(KindPlugins, LabelSafe, ModeSameList, ModeSameList, ModeSkip)
	it.Note = "The same enabled plugins and marketplaces; each account installs its own copy. A shared plugins folder hits an open Claude Code bug (#82272) whose fix deletes plugins."
	sd, dd, ok := inv.jsonSides(&it, r.srcPath(settingsFile), r.dstPath(settingsFile))
	if !ok {
		return it
	}
	for _, sec := range pluginSections {
		es, err := jsonEntries(sd, dd, []string{sec}, sec, nil, nil)
		if err != nil {
			it.Note = "settings.json has an unexpected " + sec + ": " + err.Error()
			it.Modes, it.Default = []Mode{ModeSkip}, ModeSkip
			return it
		}
		inv.addJSONEntries(&it, es)
	}
	return it
}

func (inv *Inventory) scanSettings() Item {
	r := inv.Roots
	it := baseItem(KindSettings, LabelReview, ModeCopy, ModeCopy, ModeSkip)
	it.Note = "Copies settings.json, key by key. Keys that may hold secrets (env, apiKeyHelper...) are only copied on the Careful path."
	sd, dd, ok := inv.jsonSides(&it, r.srcPath(settingsFile), r.dstPath(settingsFile))
	if !ok {
		return it
	}
	skip := func(k string) bool { return k == hooksKey || k == pluginSections[0] || k == pluginSections[1] }
	es, err := jsonEntries(sd, dd, nil, "", skip, func(k string, _ *hujson.Value) bool { return isSecretKey(k) })
	if err != nil {
		it.Note = err.Error()
		return it
	}
	inv.addJSONEntries(&it, es)
	return it
}

func (inv *Inventory) scanHooks() Item {
	r := inv.Roots
	it := baseItem(KindHooks, LabelReview, ModeCopy, ModeCopy, ModeSkip)
	it.Note = "The hooks part of settings.json. Hooks run commands on this PC, so look at them first."
	sd, dd, ok := inv.jsonSides(&it, r.srcPath(settingsFile), r.dstPath(settingsFile))
	if !ok {
		return it
	}
	es, err := jsonEntries(sd, dd, []string{hooksKey}, hooksKey, nil, nil)
	if err != nil {
		it.Note = "settings.json has unexpected hooks: " + err.Error()
		it.Modes, it.Default = []Mode{ModeSkip}, ModeSkip
		return it
	}
	inv.addJSONEntries(&it, es)
	return it
}

// mcpSecret reports whether a server entry carries env or headers values.
func mcpSecret(_ string, v *hujson.Value) bool {
	obj, ok := v.Value.(*hujson.Object)
	if !ok {
		return false
	}
	for _, key := range []string{"env", "headers"} {
		idx := findMember(obj, key)
		if idx < 0 {
			continue
		}
		if o, ok := obj.Members[idx].Value.Value.(*hujson.Object); ok && len(o.Members) > 0 {
			return true
		}
	}
	return false
}

func (inv *Inventory) scanMCP() Item {
	r := inv.Roots
	it := baseItem(KindMCP, LabelCareful, ModeCopy, ModeCopy, ModeSkip)
	it.Note = "Only the MCP server entries of .claude.json, never the rest of that file. They may hold API keys."
	sd, dd, ok := inv.jsonSides(&it, r.DefaultClaudeJSON, r.TargetClaudeJSON())
	if !ok {
		it.Note = "An account file could not be read, so Devpit will not touch MCP servers."
		return it
	}
	es, err := jsonEntries(sd, dd, []string{"mcpServers"}, "mcpServers", nil, mcpSecret)
	if err != nil {
		it.Note = ".claude.json has unexpected mcpServers: " + err.Error()
		it.Modes, it.Default = []Mode{ModeSkip}, ModeSkip
		return it
	}
	inv.addJSONEntries(&it, es)
	if len(it.Secrets) > 0 {
		it.Note += " These carry env or headers values: " + strings.Join(it.Secrets, ", ") + "."
	}
	return it
}

func (inv *Inventory) scanHistory() Item {
	r := inv.Roots
	it := baseItem(KindHistory, LabelReview, ModeSkip, ModeCopy, ModeSkip)
	it.Note = "Big and private. Copied once, never shared; nothing in this account is replaced."
	sp, dp := r.srcPath("projects"), r.dstPath("projects")
	sh, dh := r.srcPath("history.jsonl"), r.dstPath("history.jsonl")
	ss, ds := measure(sp), measure(dp)
	it.Source = Side{Exists: exists(sp) || exists(sh), Path: sp, Count: ss.Files, Size: ss.Bytes}
	it.Target = Side{Exists: exists(dp) || exists(dh), Path: dp, Count: ds.Files, Size: ds.Bytes, Links: ds.Links}
	if fi, err := lstat(sh); err == nil {
		it.Source.Count++
		it.Source.Size += fi.Size()
	}
	if fi, err := lstat(dh); err == nil {
		it.Target.Count++
		it.Target.Size += fi.Size()
	}
	for _, name := range []string{"projects", "history.jsonl"} {
		s, d := r.srcPath(name), r.dstPath(name)
		e := Entry{Name: name, SourcePath: s, TargetPath: d, Size: measure(s).Bytes}
		switch {
		case !exists(s) && !exists(d):
			continue
		case !exists(s):
			e.State = EntryTargetOnly
		case !exists(d):
			e.State = EntrySourceOnly
		default:
			e.State = EntryDiffers
			if name == "history.jsonl" {
				if same, err := equalFiles(s, d); err == nil && same {
					e.State = EntrySame
				}
			}
		}
		it.Entries = append(it.Entries, e)
	}
	return it
}

// errLockedRow is returned for a selection that picks a locked row.
var errLockedRow = errors.New("the login and account rows can never be selected")

func lockedErr(k Kind) error { return fmt.Errorf("%s: %w", k.Title(), errLockedRow) }
