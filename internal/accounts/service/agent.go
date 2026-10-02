package service

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// AgentMarker is the line that marks a SKILL.md Devpit wrote. Devpit never
// overwrites or removes a devpit skill without it.
const AgentMarker = "<!-- written by devpit agent install; devpit agent remove takes it out -->"

// AgentSkill is the SKILL.md `devpit agent install` writes: when to use
// Devpit, the read commands, check before switching, ask the person first,
// the exit codes, and what never to read.
const AgentSkill = `---
name: devpit
description: Check or change which account a developer tool (Claude Code, Git, GitHub, Vercel, Firebase, Supabase, Cloudflare, Convex) uses in a folder on this Windows PC, with Devpit. Use before pushing, deploying or committing when the account matters, or when the user asks which account is active or wants another one here.
---
` + AgentMarker + `

# Devpit accounts

Devpit picks the account each developer tool uses per folder. Use it to check
which account is active before you push, deploy or commit, and to switch
accounts when the user asks.

## Read first (safe, changes nothing)

- ` + "`devpit accounts --json`" + ` — every tool's account in this folder, and why.
- ` + "`devpit <tool> --json`" + ` — one tool (claude, git, github, vercel, firebase, supabase, cloudflare, convex).
- ` + "`devpit <tool> list --json`" + ` — the accounts a tool has.
- ` + "`devpit accounts verify --json`" + ` — expected against actual; exit code 4 means a mismatch.

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

- Never read account folders or login files (` + "`.credentials.json`, `.claude.json`, `hosts.yml`, `access-token`" + `, anything under ` + "`~/.devpit/accounts`" + `).
- Never print, copy or pass on a token.
- Never pass ` + "`--yes`" + ` before the user agreed to that exact change.
`

// AgentsSnippet is what `devpit agent install` prints for agents that read
// AGENTS.md: the person pastes it into their own repository if they want.
const AgentsSnippet = `## Accounts (Devpit)

This PC uses Devpit to pick the account each tool uses per folder.
- Check before pushing or deploying: ` + "`devpit accounts --json`" + ` (or ` + "`devpit <tool> --json`" + `).
- Never switch accounts on your own. Ask the user; only after they agree run ` + "`devpit <tool> use <name> --yes`" + `.
- Exit codes: 0 done, 1 failed, 2 wrong usage, 3 needs --yes, 4 verify mismatch.
- Never read account folders or credential files.
`

// AgentTargetState says what install would do with one place.
type AgentTargetState string

const (
	// AgentCreate: no devpit skill there; it is written.
	AgentCreate AgentTargetState = "create"
	// AgentUpdate: Devpit's own skill is there; it is rewritten if it differs.
	AgentUpdate AgentTargetState = "update"
	// AgentCurrent: Devpit's own skill is there and up to date.
	AgentCurrent AgentTargetState = "up to date"
	// AgentForeign: a devpit skill Devpit did not write is there; left alone.
	AgentForeign AgentTargetState = "not Devpit's"
	// AgentShared: the account gets the default account's skill through a
	// shared link; nothing to write.
	AgentShared AgentTargetState = "shared"
)

// AgentTarget is one Claude Code config folder the skill goes into.
type AgentTarget struct {
	// Account is "default" or the Devpit account name.
	Account string
	// File is the SKILL.md path.
	File  string
	State AgentTargetState
}

// AgentTargets lists where `devpit agent install` writes: the default
// account's ~/.claude\skills\devpit and each Devpit-managed Claude Code
// account that does not get it through a shared link.
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
	for i := range dirs {
		dirs[i].State = agentState(dirs[i].File)
	}
	return dirs, nil
}

func isLink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && (fi.Mode()&os.ModeSymlink != 0 || fi.Mode()&os.ModeIrregular != 0)
}

func agentState(file string) AgentTargetState {
	skillDir := filepath.Dir(file)
	if isLink(skillDir) || isLink(filepath.Dir(skillDir)) {
		return AgentShared
	}
	data, err := os.ReadFile(file) // #nosec G304 -- a SKILL.md, never a login file
	if errors.Is(err, os.ErrNotExist) {
		if _, derr := os.Lstat(skillDir); derr == nil {
			return AgentForeign // a devpit skill folder of someone else's
		}
		return AgentCreate
	}
	if err != nil || !bytes.Contains(data, []byte(AgentMarker)) {
		return AgentForeign
	}
	if string(data) == AgentSkill {
		return AgentCurrent
	}
	return AgentUpdate
}

// AgentInstall writes the skill into every target whose state is create or
// update, and returns the files written.
func (s *Service) AgentInstall(targets []AgentTarget) ([]string, error) {
	var done []string
	for _, t := range targets {
		if t.State != AgentCreate && t.State != AgentUpdate {
			continue
		}
		if agentState(t.File) != t.State {
			return done, fmt.Errorf("%s changed since the preview; run the command again", t.File)
		}
		if err := os.MkdirAll(filepath.Dir(t.File), 0o700); err != nil {
			return done, err
		}
		if err := writeFileAtomic(t.File, []byte(AgentSkill)); err != nil {
			return done, err
		}
		done = append(done, t.File)
	}
	return done, nil
}

// AgentInstalled lists the SKILL.md files Devpit wrote (marked), which
// `devpit agent remove` and cleanup take out.
func (s *Service) AgentInstalled() ([]string, error) {
	ts, err := s.AgentTargets()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range ts {
		if t.State == AgentUpdate || t.State == AgentCurrent {
			out = append(out, t.File)
		}
	}
	return out, nil
}

// AgentRemove removes the marked files (and their devpit folder when it is
// then empty). A file without the marker is never touched.
func (s *Service) AgentRemove(files []string) ([]string, error) {
	var done []string
	for _, f := range files {
		data, err := os.ReadFile(f) // #nosec G304 -- a SKILL.md Devpit wrote
		if err != nil || !bytes.Contains(data, []byte(AgentMarker)) || isLink(filepath.Dir(f)) {
			continue
		}
		if err := os.Remove(f); err != nil {
			return done, err
		}
		if entries, err := os.ReadDir(filepath.Dir(f)); err == nil && len(entries) == 0 {
			_ = os.Remove(filepath.Dir(f))
		}
		done = append(done, f)
	}
	return done, nil
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
			what = "will update Devpit's skill in"
		case AgentCurrent:
			what = "already has Devpit's skill:"
		case AgentForeign:
			what = "has a devpit skill Devpit did not write, left alone:"
		case AgentShared:
			what = "gets it through the shared link:"
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
