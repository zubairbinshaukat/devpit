package accounts

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// Engine changes accounts: it holds the lock, journals every change before
// its side effects, and undoes or recovers changes. One Engine per process;
// it keeps no state but the lock it may be holding.
type Engine struct {
	// Paths are the files it works on.
	Paths Paths
	// Handlers undo effect kinds other than EffectFile and EffectDir
	// (internal/accounts/shims registers its own).
	Handlers map[EffectKind]EffectHandler
	// Now is time.Now unless a test sets it.
	Now func() time.Time
	// LockWait is how long a change waits for another Devpit window.
	LockWait time.Duration

	mu   sync.Mutex
	held *Lock
}

// NewEngine returns an Engine for p.
func NewEngine(p Paths) *Engine {
	return &Engine{Paths: p, Now: time.Now, LockWait: 3 * time.Second}
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) handler(k EffectKind) (EffectHandler, error) {
	switch k {
	case EffectFile:
		return fileHandler{}, nil
	case EffectDir:
		return dirHandler{}, nil
	}
	if h, ok := e.Handlers[k]; ok {
		return h, nil
	}
	return nil, fmt.Errorf("no way to undo a %q change is known; the undo history may come from a newer Devpit", k)
}

// Load reads accounts.toml for the app (see [LoadFile]).
func (e *Engine) Load() (LoadResult, error) {
	return LoadFile(e.Paths.Store)
}

// Hold takes the lock for as long as a screen may change accounts, so a
// second Devpit window becomes read-only for Accounts. It returns ErrLocked
// when another window holds it. Changes made through this Engine while it is
// held reuse it.
func (e *Engine) Hold() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.held != nil {
		return nil
	}
	l, err := AcquireLock(e.Paths.Lock, 0)
	if err != nil {
		return err
	}
	e.held = l
	return nil
}

// Release lets go of a lock taken by Hold.
func (e *Engine) Release() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.held.Release()
	e.held = nil
}

// lock takes the lock for one operation, or reuses the held one.
func (e *Engine) lock() (func(), error) {
	e.mu.Lock()
	held := e.held != nil
	e.mu.Unlock()
	if held {
		return func() {}, nil
	}
	wait := e.LockWait
	l, err := AcquireLock(e.Paths.Lock, wait)
	if err != nil {
		return nil, err
	}
	return l.Release, nil
}

// History returns the journal, newest first.
func (e *Engine) History() ([]Entry, string, error) {
	entries, warn, err := readJournal(e.Paths.Journal)
	slices.Reverse(entries)
	return entries, warn, err
}

// Txn is one change in progress: the lock is held, the intent is in the
// journal, and every side effect is recorded before it is made. It ends
// with Commit or Rollback.
type Txn struct {
	e       *Engine
	release func()
	entries []Entry
	idx     int
	ended   bool
	// Warning is set when accounts.toml had to be moved aside to begin.
	Warning string
}

// Begin starts a change. It takes the lock, finishes or rolls back anything
// a crash left behind, loads the store, applies mutate to a copy, and writes
// the intent (before and after) to the journal. Nothing else has happened
// yet when it returns.
func (e *Engine) Begin(summary string, mutate func(*Store) error) (*Txn, error) {
	release, err := e.lock()
	if err != nil {
		return nil, err
	}
	t, err := e.begin(summary, mutate)
	if err != nil {
		release()
		return nil, err
	}
	t.release = release
	return t, nil
}

func (e *Engine) begin(summary string, mutate func(*Store) error) (*Txn, error) {
	if _, err := e.recoverLocked(); err != nil {
		return nil, err
	}
	res, err := LoadFile(e.Paths.Store)
	if err != nil {
		return nil, err
	}
	before := res.Store
	after := before.Clone()
	if mutate != nil {
		if err = mutate(after); err != nil {
			return nil, err
		}
	}
	if err = after.Validate(); err != nil {
		return nil, err
	}
	entries, _, err := readJournal(e.Paths.Journal)
	if err != nil {
		return nil, err
	}
	entry := Entry{
		V: entryFormat, ID: newEntryID(e.now()), Time: e.now().UTC(), Summary: ScrubKeepingPaths(summary), State: EntryPending,
		Before: before, After: after, BeforeHash: StoreHash(before), AfterHash: StoreHash(after),
	}
	entries = append(entries, entry)
	if err := writeJournal(e.Paths.Journal, entries); err != nil {
		return nil, err
	}
	return &Txn{e: e, entries: entries, idx: len(entries) - 1, Warning: res.Warning}, nil
}

// BeginPreview starts the change a preview describes. It fails with
// ErrStalePreview if the store is no longer the one the preview was built
// from, and refuses a once change (nothing is saved for one) and a no-op.
func (e *Engine) BeginPreview(p Preview) (*Txn, error) {
	if p.Change.Scope.Kind == ScopeOnce {
		return nil, errors.New("a just-this-once change is never saved; start the tool through the launcher instead")
	}
	if p.NoChange {
		return nil, errors.New("this change would change nothing")
	}
	t, err := e.Begin(p.Summary, func(s *Store) error {
		if StoreHash(s) != p.BeforeHash {
			return ErrStalePreview
		}
		after, err := p.Change.ApplyTo(s)
		if err != nil {
			return err
		}
		*s = *after
		return nil
	})
	if err != nil {
		return nil, err
	}
	if p.Group != "" {
		t.SetGroup(p.Group)
	}
	return t, nil
}

// NewGroup returns an id for [Preview.Group] / [Txn.SetGroup]: changes made
// with the same id are undone together.
func NewGroup() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// SetGroup ties this change to others made by the same command, so Undo
// takes them all back at once. It is saved with the change.
func (t *Txn) SetGroup(g string) {
	if !t.ended {
		t.entries[t.idx].Group = g
	}
}

// Edit makes a store-only change in one step: Begin, then Commit.
func (e *Engine) Edit(summary string, mutate func(*Store) error) (Entry, error) {
	t, err := e.Begin(summary, mutate)
	if err != nil {
		return Entry{}, err
	}
	return t.Commit()
}

// AddAccount saves a new account, journalled so Undo can take it out again.
// If the account's folder is inside Devpit's accounts folder it is recorded
// too: an undo removes it only if it is empty, which it never is after a
// sign-in, so the sign-in is never lost.
func (e *Engine) AddAccount(a Account) (Entry, error) {
	t, err := e.Begin(fmt.Sprintf("Added %s account %s", a.Tool.DisplayName(), a.Name), func(s *Store) error {
		return s.AddAccount(a)
	})
	if err != nil {
		return Entry{}, err
	}
	if a.Dir != "" && e.Paths.AccountsDir != "" && FolderContains(e.Paths.AccountsDir, a.Dir) {
		if err := t.record(Effect{
			Kind: EffectDir, Tool: a.Tool, Target: a.Dir,
			Summary: "Account folder " + a.Dir, AfterHash: "dir", Done: true,
		}); err != nil {
			_ = t.Rollback()
			return Entry{}, err
		}
	}
	return t.Commit()
}

// ID is the journal entry's id.
func (t *Txn) ID() string { return t.entries[t.idx].ID }

// Before is the store as it was. Do not modify it.
func (t *Txn) Before() *Store { return t.entries[t.idx].Before.Clone() }

// After is the store as it will be saved.
func (t *Txn) After() *Store { return t.entries[t.idx].After.Clone() }

func (t *Txn) save() error {
	return writeJournal(t.e.Paths.Journal, t.entries)
}

// record appends an effect to the journal entry and saves the journal.
func (t *Txn) record(eff Effect) error {
	if t.ended {
		return errors.New("this change has already ended")
	}
	// Data holds identifiers Devpit made (see Effect.Data): the key must not
	// name a secret, and the value gets the identifier rule.
	for k, v := range eff.Data {
		if secretKey(strings.ToLower(k)) || LooksSecret(k) || PathLooksSecret(v) {
			return fmt.Errorf("refusing to record %q in the undo history: it looks like a secret", k)
		}
	}
	if PathLooksSecret(eff.Target) {
		return fmt.Errorf("refusing to record %s in the undo history: the path looks like it holds a secret", eff.Target)
	}
	eff.Summary = ScrubKeepingPaths(eff.Summary)
	t.entries[t.idx].Effects = append(t.entries[t.idx].Effects, eff)
	return t.save()
}

func (t *Txn) markDone() error {
	effs := t.entries[t.idx].Effects
	effs[len(effs)-1].Done = true
	return t.save()
}

// WriteFile writes data to target as a journalled side effect: the old
// content and the new hash go into the journal first, then the file is
// written atomically, then the effect is marked done. It refuses a login
// file and content that looks like it holds a secret, so neither can ever
// reach the journal. Content is checked with [ContentLooksSecret]: a long
// folder name inside a path in a config file is a folder name, while a
// token anywhere else in the text is still refused.
func (t *Txn) WriteFile(target, summary string, data []byte) error {
	if credentialFile(target) {
		return fmt.Errorf("refusing to write %s: Devpit never writes a tool's login file", target)
	}
	if ContentLooksSecret(string(data)) {
		return fmt.Errorf("refusing to write %s: the content looks like it holds a secret", target)
	}
	eff := Effect{Kind: EffectFile, Target: target, Summary: summary, AfterHash: hashBytes(data)}
	old, err := os.ReadFile(target) // #nosec G304 -- a file an adapter owns
	switch {
	case err == nil:
		if len(old) > maxBefore {
			return fmt.Errorf("%s is too large to change safely (%d bytes)", target, len(old))
		}
		if ContentLooksSecret(string(old)) {
			return fmt.Errorf("refusing to change %s: it holds something that looks like a secret, which Devpit will not copy into its undo history", target)
		}
		eff.BeforeExists, eff.Before, eff.BeforeHash = true, old, hashBytes(old)
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("reading %s: %w", target, err)
	}
	if err := t.record(eff); err != nil {
		return err
	}
	if err := writeAtomic(target, data); err != nil {
		return err
	}
	return t.markDone()
}

// MakeDir creates target (and its parents) as a journalled side effect.
// Undo removes it only while it is empty.
func (t *Txn) MakeDir(target, summary string) error {
	if _, err := os.Lstat(target); err == nil {
		return nil // already there: not ours to undo
	}
	if err := t.record(Effect{Kind: EffectDir, Target: target, Summary: summary, AfterHash: "dir"}); err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	return t.markDone()
}

// Do runs a custom side effect whose kind has a handler on the Engine. eff
// is recorded first; do performs it and returns the effect as it ended up
// (with AfterHash or Data filled in), which replaces the record.
func (t *Txn) Do(eff Effect, do func() (Effect, error)) error {
	if _, err := t.e.handler(eff.Kind); err != nil {
		return err
	}
	if err := t.record(eff); err != nil {
		return err
	}
	done, err := do()
	if err != nil {
		return err
	}
	done.Done = true
	effs := t.entries[t.idx].Effects
	effs[len(effs)-1] = done
	return t.save()
}

// Commit saves the store and marks the change done. The lock is released.
func (t *Txn) Commit() (Entry, error) {
	if t.ended {
		return Entry{}, errors.New("this change has already ended")
	}
	defer t.end()
	ent := &t.entries[t.idx]
	// Committed goes into the journal first: from here on the change is
	// finished, never rolled back, even if Devpit stops before the store is
	// saved (the next start saves it).
	ent.Committed = true
	if err := t.save(); err != nil {
		ent.Committed = false
		return Entry{}, err
	}
	if err := SaveFile(t.e.Paths.Store, ent.After.Clone()); err != nil {
		// Not saved, but committed: the next start finishes it. Say so.
		return Entry{}, fmt.Errorf("the change is made but accounts.toml could not be saved (%w); Devpit finishes saving it the next time it starts", err)
	}
	ent.State = EntryDone
	if err := t.save(); err != nil {
		// The store is saved; the next start sees a committed entry and
		// marks it done.
		return *ent, err
	}
	return *ent, nil
}

// Rollback undoes the side effects made so far, leaves the store as it was
// and marks the change rolled back. The lock is released.
func (t *Txn) Rollback() error {
	if t.ended {
		return nil
	}
	defer t.end()
	ent := &t.entries[t.idx]
	if _, err := t.e.revertEffects(ent); err != nil {
		return err
	}
	ent.State = EntryRolledBack
	return t.save()
}

func (t *Txn) end() {
	t.ended = true
	if t.release != nil {
		t.release()
	}
}

// UndoResult is what Undo did.
type UndoResult struct {
	// Entry is the change undone first (the newest).
	Entry Entry
	// Entries are every change undone, newest first: more than one when a
	// command made several changes together (see Txn.SetGroup).
	Entries []Entry
	// Notes are sentences about anything left in place on purpose.
	Notes []string
}

// storeHashNow is the hash of accounts.toml as it is now. A missing file is
// an empty store, exactly as LoadFile reads it, so a change that never got
// as far as creating the file is not mistaken for one edited by hand.
func (e *Engine) storeHashNow() (string, error) {
	cur, err := ReadFile(e.Paths.Store)
	if err != nil {
		return "", err
	}
	return StoreHash(cur), nil
}

// undoTarget returns the index of the latest change Undo would revert.
func undoTarget(entries []Entry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].State == EntryDone {
			return i
		}
	}
	return -1
}

// UndoPreview is what Undo would take back, checked: Entries newest first.
// It changes nothing. It fails with ErrNothingToUndo, or with a
// [*ChangedError] when a file the latest change wrote was changed by hand
// (then Undo would stop the same way).
func (e *Engine) UndoPreview() ([]Entry, error) {
	entries, _, err := readJournal(e.Paths.Journal)
	if err != nil {
		return nil, err
	}
	for _, ent := range entries {
		if ent.State == EntryPending || ent.State == EntryUndoing {
			return nil, errors.New("a change was left unfinished; open Devpit or run the command again so it can be finished or put back first")
		}
	}
	idx := undoTarget(entries)
	if idx < 0 {
		return nil, ErrNothingToUndo
	}
	ent := entries[idx]
	if err := e.checkUndoable(&ent); err != nil {
		return nil, err
	}
	out := []Entry{ent}
	if ent.Group != "" {
		for i := idx - 1; i >= 0; i-- {
			if entries[i].State != EntryDone || entries[i].Group != ent.Group {
				break
			}
			out = append(out, entries[i])
		}
	}
	return out, nil
}

// checkUndoable fails if accounts.toml or any file ent wrote was changed
// since.
func (e *Engine) checkUndoable(ent *Entry) error {
	h, err := e.storeHashNow()
	if err != nil || h != ent.AfterHash {
		return &ChangedError{Path: e.Paths.Store, What: "made the change \"" + ent.Summary + "\""}
	}
	return e.checkEffects(ent)
}

// Undo reverts the latest change that is not undone yet, and the changes
// made together with it (the same Group). Before touching anything it
// checks that accounts.toml and every file the change wrote still hold
// exactly what Devpit wrote; if any was changed by hand it stops with a
// [*ChangedError] and changes nothing. In a group, each change is checked
// again right before it is undone; if one was changed by hand, the ones
// already undone stay undone and the error says so.
func (e *Engine) Undo() (UndoResult, error) {
	release, err := e.lock()
	if err != nil {
		return UndoResult{}, err
	}
	defer release()
	if _, err = e.recoverLocked(); err != nil {
		return UndoResult{}, err
	}
	var res UndoResult
	group := ""
	for {
		entries, _, err := readJournal(e.Paths.Journal)
		if err != nil {
			return res, err
		}
		idx := undoTarget(entries)
		if idx < 0 || (len(res.Entries) > 0 && (group == "" || entries[idx].Group != group)) {
			if len(res.Entries) == 0 {
				return res, ErrNothingToUndo
			}
			return res, nil
		}
		ent := &entries[idx]
		if cerr := e.checkUndoable(ent); cerr != nil {
			if len(res.Entries) > 0 {
				return res, fmt.Errorf("%d of the changes made together were undone, then Devpit stopped: %w", len(res.Entries), cerr)
			}
			return res, cerr
		}
		ent.State = EntryUndoing
		if err = writeJournal(e.Paths.Journal, entries); err != nil {
			return res, err
		}
		notes, err := e.revertEffects(ent)
		if err != nil {
			return res, err
		}
		if err := SaveFile(e.Paths.Store, ent.Before.Clone()); err != nil {
			return res, err
		}
		ent.State = EntryUndone
		if err := writeJournal(e.Paths.Journal, entries); err != nil {
			return res, err
		}
		if len(res.Entries) == 0 {
			res.Entry = *ent
			group = ent.Group
		}
		res.Entries = append(res.Entries, *ent)
		res.Notes = append(res.Notes, notes...)
		if group == "" {
			return res, nil
		}
	}
}

// checkEffects fails if any effect's target was changed by hand.
func (e *Engine) checkEffects(ent *Entry) error {
	for _, eff := range ent.Effects {
		h, err := e.handler(eff.Kind)
		if err != nil {
			return err
		}
		st, err := h.State(eff)
		if err != nil {
			return err
		}
		if st == Changed {
			return &ChangedError{Path: eff.Target, What: "wrote it"}
		}
	}
	return nil
}

// revertEffects reverts the effects in reverse order, skipping any already
// back where they were. It checks every effect first.
func (e *Engine) revertEffects(ent *Entry) ([]string, error) {
	if err := e.checkEffects(ent); err != nil {
		return nil, err
	}
	var notes []string
	for i := len(ent.Effects) - 1; i >= 0; i-- {
		eff := ent.Effects[i]
		h, _ := e.handler(eff.Kind)
		st, err := h.State(eff)
		if err != nil {
			return notes, err
		}
		if st != AtAfter {
			continue
		}
		note, err := h.Revert(eff)
		if err != nil {
			return notes, fmt.Errorf("putting back %s: %w", eff.Target, err)
		}
		if note != "" {
			notes = append(notes, note)
		}
	}
	return notes, nil
}

// Recovered is one unfinished change found and dealt with by Recover.
type Recovered struct {
	Entry Entry
	// Outcome is "finished", "rolled back" or "undo finished".
	Outcome string
}

// Recover deals with changes a crash left unfinished. A change that was
// committed (every side effect done, Committed written) is finished: its
// store is saved if it was not yet, and it is marked done. Any other
// pending change is rolled back, however far it got; an interrupted undo is
// finished. A missing accounts.toml counts as an empty store. If a file
// involved was changed by hand since, it stops with a [*ChangedError] and
// leaves that entry as it is. Begin and Undo call it first; the app calls it
// at startup to tell the person what happened.
func (e *Engine) Recover() ([]Recovered, error) {
	release, err := e.lock()
	if err != nil {
		return nil, err
	}
	defer release()
	return e.recoverLocked()
}

func (e *Engine) recoverLocked() ([]Recovered, error) {
	entries, _, err := readJournal(e.Paths.Journal)
	if err != nil {
		return nil, err
	}
	var out []Recovered
	changed := false
	for i := range entries {
		ent := &entries[i]
		if ent.State != EntryPending && ent.State != EntryUndoing {
			continue
		}
		curHash, herr := e.storeHashNow()
		if herr != nil {
			curHash = "" // unreadable: treated as changed by hand below
		}

		if ent.State == EntryPending && e.finished(ent, curHash) {
			if curHash != ent.AfterHash {
				if err := SaveFile(e.Paths.Store, ent.After.Clone()); err != nil {
					return out, err
				}
			}
			ent.State = EntryDone
			out = append(out, Recovered{Entry: *ent, Outcome: "finished"})
			changed = true
			continue
		}
		if curHash != ent.AfterHash && curHash != ent.BeforeHash {
			if changed {
				_ = writeJournal(e.Paths.Journal, entries)
			}
			return out, &ChangedError{Path: e.Paths.Store, What: "started the change \"" + ent.Summary + "\""}
		}
		if _, err := e.revertEffects(ent); err != nil {
			if changed {
				_ = writeJournal(e.Paths.Journal, entries)
			}
			return out, err
		}
		if curHash == ent.AfterHash {
			if err := SaveFile(e.Paths.Store, ent.Before.Clone()); err != nil {
				return out, err
			}
		}
		outcome := "rolled back"
		if ent.State == EntryUndoing {
			ent.State = EntryUndone
			outcome = "undo finished"
		} else {
			ent.State = EntryRolledBack
		}
		out = append(out, Recovered{Entry: *ent, Outcome: outcome})
		changed = true
	}
	if changed {
		if err := writeJournal(e.Paths.Journal, entries); err != nil {
			return out, err
		}
	}
	return out, nil
}

// finished reports whether a pending entry is to be finished rather than
// rolled back. A current entry says so itself (Committed), and is finished
// when the store is either side of the save Commit was making. An entry
// from before Committed existed (V 0) is finished the old way: every effect
// done and the store saved, provided the change did change the store (an
// entry whose store is the same before and after cannot prove it got to
// Commit, so it is rolled back).
func (e *Engine) finished(ent *Entry, curHash string) bool {
	if ent.V >= entryFormat {
		return ent.Committed && (curHash == ent.AfterHash || curHash == ent.BeforeHash)
	}
	return allDone(ent.Effects) && curHash == ent.AfterHash && ent.AfterHash != ent.BeforeHash
}

func allDone(effs []Effect) bool {
	for _, e := range effs {
		if !e.Done {
			return false
		}
	}
	return true
}

// newEntryID is a sortable id: the time plus four random bytes.
func newEntryID(t time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return t.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}
