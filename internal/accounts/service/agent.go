package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// agentMarkerStem is the start of the line that marks a SKILL.md Devpit
// wrote. Every Devpit-written skill carries it, whatever its version (the
// first ones carried exactly this line and " -->"), so Devpit can tell its
// own skill from someone else's. Devpit never overwrites or removes a devpit
// skill without it.
const agentMarkerStem = "<!-- written by devpit agent install; devpit agent remove takes it out"

// AgentSkillVersion is the version of the skill this Devpit writes. It goes
// up whenever AgentSkill changes, so `devpit agent install` (and the
// installer's update step) can tell an older Devpit skill from the current
// one. A marked skill with no version is version 1.
const AgentSkillVersion = 2

// agentSkillVersionText is AgentSkillVersion as the marker spells it; a test
// keeps the two equal.
const agentSkillVersionText = "2"

// AgentMarker is the marker line of the current skill.
const AgentMarker = agentMarkerStem + "; skill version " + agentSkillVersionText + " -->"

// The sentences a test pins: the skill must keep telling agents to ask first
// and never to read account folders.
const (
	agentAskFirst = "Ask the user before any change, and only after they agree run it with `--yes`."
	agentNoRead   = "Never read account folders or login files"
)

// AgentSkill is the SKILL.md `devpit agent install` writes: when to use
// Devpit, the read commands, check before switching, ask the person first,
// how to answer "why" with the docs, the exit codes, and what never to read.
// The frontmatter holds only the standard name and description.
const AgentSkill = `---
name: devpit
description: Which account a developer tool (Claude Code, Git, GitHub, Vercel, Firebase, Supabase, Cloudflare, Convex) uses in a folder on this Windows PC, why, and how to change it, with Devpit. Use before pushing, deploying or committing when the account matters; when a push, commit or deploy went to the wrong account; when the user asks which account is active here or wants another one; and when the user asks why Devpit did something (a folder rule, a shim, a PATH change, a warning it printed).
---
` + AgentMarker + `

# Devpit accounts

Devpit picks the account each developer tool uses per folder. Use it to check
which account is active before you push, deploy or commit, to explain why a
tool used the account it did, and to switch accounts when the user asks.

## Read first (safe, changes nothing)

- ` + "`devpit accounts --json`" + ` — every tool's account in this folder, and why.
- ` + "`devpit <tool> --json`" + ` — one tool (claude, git, github, vercel, firebase, supabase, cloudflare, convex).
- ` + "`devpit <tool> list --json`" + ` — the accounts a tool has.
- ` + "`devpit accounts verify --json`" + ` — expected against actual; exit code 4 means a mismatch.

## When the user asks why something happened

1. Look at this PC first, with the read commands: ` + "`devpit accounts verify --json`" + `
   and ` + "`devpit <tool> --json`" + `. Each problem has a ` + "`fix`" + ` and, when a page explains
   it, a ` + "`docs`" + ` link.
2. Fetch https://devpit.zubyr.dev/llms.txt and open the ONE page that matches (the
   problem's ` + "`docs`" + ` link when there is one). Every page is also available as
   Markdown: add ` + "`.md`" + ` to the page URL.
3. Offline? Use ` + "`devpit <command> --help`" + `; every command's help ends with examples.
4. Explain in plain words what happened and what would fix it.
   ` + agentAskFirst + `

## Changing an account

1. Check first with the read commands above.
2. Tell the user exactly what would change and ask them. Never switch on your own.
3. Only after they agree, run the change with ` + "`--yes`" + `, for example
   ` + "`devpit github use work --yes`" + ` (this folder and inside it) or
   ` + "`devpit claude use work --everywhere --yes`" + `.
4. ` + "`devpit undo --yes`" + ` takes the last change back (again only when the user asks).

For one command with another account, nothing saved: ` + "`devpit <tool> run <name> -- <command>`" + `.

## Exit codes

0 done · 1 failed · 2 wrong usage · 3 needs --yes (nothing changed; ask the user, then re-run with --yes) · 4 verify found a mismatch.

## Never

- ` + agentNoRead + ` (` + "`.credentials.json`, `.claude.json`, `hosts.yml`, `access-token`" + `, anything under ` + "`~/.devpit/accounts`" + `).
- Never print, copy or pass on a token.
- Never pass ` + "`--yes`" + ` before the user agreed to that exact change.
`

// AgentsSnippet is what `devpit agent install` prints for agents that read
// AGENTS.md: the person pastes it into their own repository if they want.
const AgentsSnippet = `## Accounts (Devpit)

This PC uses Devpit to pick the account each tool uses per folder.
- Check before pushing or deploying: ` + "`devpit accounts --json`" + ` (or ` + "`devpit <tool> --json`" + `).
- To explain why something happened: ` + "`devpit accounts verify --json`" + `, then the matching page from https://devpit.zubyr.dev/llms.txt (add .md to a page URL for Markdown).
- Never switch accounts on your own. Ask the user; only after they agree run ` + "`devpit <tool> use <name> --yes`" + `.
- Exit codes: 0 done, 1 failed, 2 wrong usage, 3 needs --yes, 4 verify mismatch.
- Never read account folders or credential files.
`

// AgentTargetState says what is in one place the skill goes, and so what
// install would do there.
type AgentTargetState string

const (
	// AgentCreate: no devpit skill there (not installed); install writes it.
	AgentCreate AgentTargetState = "create"
	// AgentUpdate: Devpit's own skill is there but older (or edited); install
	// rewrites it.
	AgentUpdate AgentTargetState = "update"
	// AgentCurrent: Devpit's own skill is there and up to date (installed).
	AgentCurrent AgentTargetState = "up to date"
	// AgentNewer: Devpit's own skill from a newer Devpit is there; left
	// alone (this Devpit would write an older one).
	AgentNewer AgentTargetState = "newer"
	// AgentForeign: a devpit skill Devpit did not write is in the way; left
	// alone.
	AgentForeign AgentTargetState = "not Devpit's"
	// AgentShared: the account reaches an earlier target's skill folder
	// through a link (a shared skills folder or a shared devpit skill);
	// nothing is written through the link.
	AgentShared AgentTargetState = "shared"
)

// AgentTarget is one Claude Code config folder the skill goes into.
type AgentTarget struct {
	// Account is "default" or the Devpit account name.
	Account string
	// File is the SKILL.md path.
	File  string
	State AgentTargetState
	// Version is the skill version found there: 0 when there is no Devpit
	// skill, 1 for a marked skill without a version.
	Version int
	// SharedWith is, for AgentShared, the account whose folder this one
	// reaches through the link ("" when the link leads to a Devpit skill
	// outside every target).
	SharedWith string
	// ThroughLink: the skills folder, the devpit folder or SKILL.md itself
	// is a link, so the skill lives somewhere else.
	ThroughLink bool
	// Note says why, in words, for a foreign or unreadable place.
	Note string
}

// Installed reports whether Devpit's skill is there (any version), written
// in place or reached through a shared link.
func (t AgentTarget) Installed() bool {
	return t.State == AgentCurrent || t.State == AgentUpdate || t.State == AgentNewer || t.State == AgentShared
}

// AgentOverall is the one-word summary of the skill on this PC, for a
// settings row.
type AgentOverall string

const (
	// AgentNoClaude: Claude Code was not found; there is nowhere to put it.
	AgentNoClaude AgentOverall = "Claude Code not found"
	// AgentNotInstalled: no Devpit skill anywhere.
	AgentNotInstalled AgentOverall = "not installed"
	// AgentInstalled: the current skill everywhere it belongs.
	AgentInstalled AgentOverall = "installed"
	// AgentViaLink: installed, and the default account gets it through a
	// link (its skills folder is a link to somewhere else).
	AgentViaLink AgentOverall = "installed through a shared link"
	// AgentOlder: a Devpit skill is older than this Devpit's, or missing in
	// some account; install brings it up to date.
	AgentOlder AgentOverall = "update available"
	// AgentInTheWay: a devpit skill Devpit did not write is there, and
	// nowhere else has Devpit's.
	AgentInTheWay AgentOverall = "a different devpit skill is in the way"
)

// AgentStatus is the skill on this PC: whether Claude Code is there at all,
// and each place the skill goes.
type AgentStatus struct {
	// ClaudeCode: Claude Code was found (claude on PATH outside Devpit's
	// shim folder, a ~/.claude folder, or a Claude Code account in Devpit).
	// claude is never run to find out.
	ClaudeCode bool
	// ClaudeCodeWhy says how it was found, or why not.
	ClaudeCodeWhy string
	// Version is the skill version this Devpit writes (AgentSkillVersion).
	Version int
	// Targets are the places, the default account first.
	Targets []AgentTarget
}

// Overall sums the targets up in one state.
func (a AgentStatus) Overall() AgentOverall {
	if !a.ClaudeCode {
		return AgentNoClaude
	}
	var create, update, current, foreign bool
	for _, t := range a.Targets {
		switch t.State {
		case AgentCreate:
			create = true
		case AgentUpdate:
			update = true
		case AgentCurrent, AgentNewer:
			current = true
		case AgentForeign:
			foreign = true
		}
	}
	switch {
	case update || (create && current):
		return AgentOlder
	case current:
		if len(a.Targets) > 0 && a.Targets[0].ThroughLink {
			return AgentViaLink
		}
		return AgentInstalled
	case foreign && !create:
		return AgentInTheWay
	}
	return AgentNotInstalled
}

// CanInstall reports whether `devpit agent install` would write anything.
func (a AgentStatus) CanInstall() bool {
	if !a.ClaudeCode {
		return false
	}
	for _, t := range a.Targets {
		if t.State == AgentCreate || t.State == AgentUpdate {
			return true
		}
	}
	return false
}

// Removable lists the SKILL.md files `devpit agent remove` would take out:
// Devpit's own, written in place (never one reached through a shared link).
func (a AgentStatus) Removable() []string {
	var out []string
	for _, t := range a.Targets {
		if t.State == AgentCurrent || t.State == AgentUpdate || t.State == AgentNewer {
			out = append(out, t.File)
		}
	}
	return out
}

// ErrNoClaudeCode is returned by AgentInstall when Claude Code is not on this
// PC: there is no skills folder to write into.
var ErrNoClaudeCode = errors.New("Claude Code was not found on this PC (not on PATH, no ~/.claude folder), so there is nowhere to put the skill") //nolint:revive,staticcheck // a product name

// AgentStatus says whether Claude Code is installed and, for every place the
// skill goes, whether Devpit's skill is there, older, newer, in the way of
// someone else's, or shared through a link. It reads only the SKILL.md files
// and folder links; it never runs claude.
func (s *Service) AgentStatus(_ context.Context) (AgentStatus, error) {
	ts, err := s.AgentTargets()
	if err != nil {
		return AgentStatus{}, err
	}
	out := AgentStatus{Version: AgentSkillVersion, Targets: ts}
	out.ClaudeCode, out.ClaudeCodeWhy = s.claudeCodeFound()
	return out, nil
}

// claudeCodeFound looks for Claude Code without running it.
func (s *Service) claudeCodeFound() (bool, string) {
	if p, err := s.lookPath("claude"); err == nil {
		return true, "claude is on PATH at " + p
	}
	if fi, err := os.Stat(filepath.Join(s.Deps.Home, ".claude")); err == nil && fi.IsDir() {
		return true, s.DisplayPath(filepath.Join(s.Deps.Home, ".claude")) + " exists"
	}
	if st, _, err := s.Load(); err == nil && len(st.AccountsFor(accounts.ToolClaude)) > 0 {
		return true, "Devpit has Claude Code accounts"
	}
	return false, "claude is not on PATH and there is no ~/.claude folder"
}

// AgentTargets lists where `devpit agent install` writes: the default
// account's ~/.claude\skills\devpit and each Devpit-managed Claude Code
// account, each with its state. A place whose devpit folder really is an
// earlier place's (through a junction or link) is AgentShared, so one copy
// serves both and nothing is written through the link.
func (s *Service) AgentTargets() ([]AgentTarget, error) {
	st, _, err := s.Load()
	if err != nil {
		return nil, err
	}
	dirs := []AgentTarget{{Account: accounts.DefaultName, File: filepath.Join(s.Deps.Home, ".claude", "skills", "devpit", "SKILL.md")}}
	for _, a := range st.AccountsFor(accounts.ToolClaude) {
		if a.Dir != "" {
			dirs = append(dirs, AgentTarget{Account: a.Name, File: filepath.Join(a.Dir, "skills", "devpit", "SKILL.md")})
		}
	}
	seen := map[string]string{} // real devpit folder → account
	for i := range dirs {
		t := &dirs[i]
		skillDir := filepath.Dir(t.File)
		t.ThroughLink = isLink(skillDir) || isLink(filepath.Dir(skillDir)) || isLink(t.File)
		where := realFolder(skillDir)
		if owner, ok := seen[where]; ok {
			t.State, t.SharedWith = AgentShared, owner
			t.Version = skillVersionOf(t.File)
			continue
		}
		seen[where] = t.Account
		t.State, t.Version, t.Note = agentState(t.File)
	}
	return dirs, nil
}

// realFolder is where a folder really is, for comparing two places.
func realFolder(p string) string {
	r, err := accounts.RealPath(p)
	if err != nil {
		r = p
	}
	return strings.ToLower(filepath.Clean(r))
}

func isLink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && (fi.Mode()&os.ModeSymlink != 0 || fi.Mode()&os.ModeIrregular != 0)
}

var skillVersionRe = regexp.MustCompile(`; skill version (\d+) -->`)

// markedVersion is the version of a Devpit-marked skill, or 0 when data has
// no Devpit marker.
func markedVersion(data []byte) int {
	if !bytes.Contains(data, []byte(agentMarkerStem)) {
		return 0
	}
	if m := skillVersionRe.FindSubmatch(data); m != nil {
		if v, err := strconv.Atoi(string(m[1])); err == nil && v > 0 {
			return v
		}
	}
	return 1
}

func skillVersionOf(file string) int {
	data, err := os.ReadFile(file) // #nosec G304 -- a SKILL.md, never a login file
	if err != nil {
		return 0
	}
	return markedVersion(data)
}

// agentState looks at one place.
func agentState(file string) (AgentTargetState, int, string) {
	skillDir := filepath.Dir(file)
	if isLink(file) {
		// Replacing a linked SKILL.md with a file would cut the link.
		if v := skillVersionOf(file); v > 0 {
			return AgentShared, v, "SKILL.md is a link to a Devpit skill elsewhere."
		}
		return AgentForeign, 0, "SKILL.md is a link made by something else."
	}
	data, err := os.ReadFile(file) // #nosec G304 -- a SKILL.md, never a login file
	if errors.Is(err, os.ErrNotExist) {
		entries, derr := os.ReadDir(skillDir)
		switch {
		case errors.Is(derr, os.ErrNotExist):
			if _, lerr := os.Lstat(skillDir); lerr == nil {
				return AgentForeign, 0, "the devpit folder is a link that leads nowhere."
			}
			return AgentCreate, 0, ""
		case derr != nil:
			return AgentForeign, 0, "the devpit folder could not be read: " + accounts.Scrub(derr.Error())
		}
		for _, e := range entries {
			if !isTempSkill(e.Name()) {
				return AgentForeign, 0, "a devpit folder without Devpit's SKILL.md, holding other files."
			}
		}
		return AgentCreate, 0, "" // empty, or only a write a crash cut short
	}
	if err != nil {
		return AgentForeign, 0, "SKILL.md could not be read: " + accounts.Scrub(err.Error())
	}
	v := markedVersion(data)
	switch {
	case v == 0:
		return AgentForeign, 0, "a devpit skill Devpit did not write."
	case v > AgentSkillVersion:
		return AgentNewer, v, "written by a newer Devpit."
	case string(data) == AgentSkill:
		return AgentCurrent, v, ""
	}
	return AgentUpdate, v, ""
}

// isTempSkill is a temporary file writeFileAtomic left behind when Devpit
// stopped halfway through writing SKILL.md.
func isTempSkill(name string) bool { return strings.HasPrefix(name, "SKILL.md.tmp-") }

// sweepTempSkills removes those leftovers from dir.
func sweepTempSkills(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if isTempSkill(e.Name()) && e.Type().IsRegular() {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// readOnlyError says a SKILL.md is read-only and was left as it is.
func readOnlyError(file string) error {
	return fmt.Errorf("%s is read-only, so Devpit left it as it is; make it writable and run the command again", file)
}

func isReadOnly(file string) bool {
	fi, err := os.Stat(file)
	return err == nil && fi.Mode().Perm()&0o200 == 0
}

// AgentInstall writes the skill into every target whose state is create or
// update (refreshing Devpit's own older skill), and returns the files
// written. A foreign skill, a newer Devpit's skill and a place reached
// through a shared link are never touched. A place that changed since the
// targets were read, or a read-only SKILL.md, is skipped with an error while
// the other places are still done. It refuses when Claude Code is not on
// this PC (ErrNoClaudeCode).
func (s *Service) AgentInstall(targets []AgentTarget) ([]string, error) {
	if ok, _ := s.claudeCodeFound(); !ok {
		return nil, ErrNoClaudeCode
	}
	var done []string
	var errs []error
	for _, t := range targets {
		if t.State != AgentCreate && t.State != AgentUpdate {
			continue
		}
		if now, _, _ := agentState(t.File); now != t.State {
			errs = append(errs, fmt.Errorf("%s changed since the preview; run the command again", t.File))
			continue
		}
		if t.State == AgentUpdate && isReadOnly(t.File) {
			errs = append(errs, readOnlyError(t.File))
			continue
		}
		dir := filepath.Dir(t.File)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			errs = append(errs, err)
			continue
		}
		sweepTempSkills(dir)
		if err := writeFileAtomic(t.File, []byte(AgentSkill)); err != nil {
			errs = append(errs, err)
			continue
		}
		done = append(done, t.File)
	}
	return done, errors.Join(errs...)
}

// AgentInstalled lists the SKILL.md files Devpit wrote (marked, any
// version, in place), which `devpit agent remove` and cleanup take out.
func (s *Service) AgentInstalled() ([]string, error) {
	ts, err := s.AgentTargets()
	if err != nil {
		return nil, err
	}
	return AgentStatus{Targets: ts}.Removable(), nil
}

// AgentRemove removes the given files when they are Devpit's own skill
// written in place (one AgentInstalled lists, still carrying the marker),
// then any temporary leftovers beside them and the devpit folder when it is
// then empty. A file without the marker, or reached through a shared link,
// is never touched; a read-only one is left with an error.
func (s *Service) AgentRemove(files []string) ([]string, error) {
	mine, err := s.AgentInstalled()
	if err != nil {
		return nil, err
	}
	ok := map[string]bool{}
	for _, f := range mine {
		ok[strings.ToLower(filepath.Clean(f))] = true
	}
	var done []string
	var errs []error
	for _, f := range files {
		if !ok[strings.ToLower(filepath.Clean(f))] || skillVersionOf(f) == 0 {
			continue
		}
		if isReadOnly(f) {
			errs = append(errs, readOnlyError(f))
			continue
		}
		if err := os.Remove(f); err != nil {
			errs = append(errs, err)
			continue
		}
		dir := filepath.Dir(f)
		sweepTempSkills(dir)
		if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 && !isLink(dir) {
			_ = os.Remove(dir)
		}
		done = append(done, f)
	}
	return done, errors.Join(errs...)
}

// AgentLines says what install would do, one line per place.
func AgentLines(ts []AgentTarget) []string {
	var out []string
	for _, t := range ts {
		var what string
		switch t.State {
		case AgentCreate:
			what = "will write"
		case AgentUpdate:
			what = fmt.Sprintf("will update Devpit's skill (version %d → %d) in", t.Version, AgentSkillVersion)
		case AgentCurrent:
			what = "already has Devpit's skill:"
		case AgentNewer:
			what = fmt.Sprintf("has a newer Devpit's skill (version %d), left alone:", t.Version)
		case AgentForeign:
			what = "has a devpit skill Devpit did not write, left alone:"
		case AgentShared:
			what = "gets it through the shared link:"
			if t.SharedWith != "" {
				what = "gets " + t.SharedWith + "'s through the shared link:"
			}
		}
		out = append(out, fmt.Sprintf("%-10s %s %s", t.Account, what, t.File))
	}
	return out
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// trimLines drops trailing blank lines.
func trimLines(ls []string) []string {
	for len(ls) > 0 && strings.TrimSpace(ls[len(ls)-1]) == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}
