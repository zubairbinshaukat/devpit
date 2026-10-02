package accounts

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// JournalLimit is how many finished changes the journal keeps. Changes
// still in progress are never dropped.
const JournalLimit = 20

// EntryState is where a journalled change is.
type EntryState string

const (
	// EntryPending: the intent is written and side effects may have
	// started. Found at startup, it means Devpit stopped half way; the next
	// start finishes it or rolls it back.
	EntryPending EntryState = "pending"
	// EntryDone: applied. The latest done entry is what Undo reverts.
	EntryDone EntryState = "done"
	// EntryUndoing: an undo started and did not finish.
	EntryUndoing EntryState = "undoing"
	// EntryUndone: reverted by Undo.
	EntryUndone EntryState = "undone"
	// EntryRolledBack: a change that never finished, put back at startup.
	EntryRolledBack EntryState = "rolled back"
)

// EffectKind names a kind of side effect. The engine knows how to undo
// EffectFile and EffectDir; other kinds need an [EffectHandler] passed to
// the [Engine].
type EffectKind string

const (
	// EffectFile: a file was written (or created). Undo puts the old
	// content back, or removes a file that did not exist, but only if the
	// file still holds exactly what Devpit wrote.
	EffectFile EffectKind = "file"
	// EffectDir: a folder was created. Undo removes it only if it is still
	// empty, and otherwise leaves it and says so: an account folder holds a
	// sign-in and is never deleted by an undo.
	EffectDir EffectKind = "dir"
)

// maxBefore is the largest file whose old content the journal keeps.
const maxBefore = 1 << 20

// Effect is one side effect of a change, recorded before it happens.
type Effect struct {
	Kind    EffectKind `json:"kind"`
	Tool    Tool       `json:"tool,omitempty"`
	Target  string     `json:"target"`
	Summary string     `json:"summary"`
	// BeforeExists and Before are the target's state before: for a file,
	// whether it existed and its content.
	BeforeExists bool   `json:"before_exists"`
	Before       []byte `json:"before,omitempty"`
	BeforeHash   string `json:"before_hash,omitempty"`
	// AfterHash is the hash of what Devpit wrote. Undo refuses to touch a
	// target whose hash is neither this nor BeforeHash.
	AfterHash string `json:"after_hash,omitempty"`
	// Data carries what a custom handler needs: identifiers Devpit made
	// (paths, profile names, hashes), never a value read from a tool. Keys
	// that name a secret ("token", "password"…) are refused, and each value
	// is checked with the identifier rule ([PathLooksSecret]: a known token
	// prefix), the same rule folder paths get, so a long folder or skill
	// name is never mistaken for a token.
	//
	// Before, for a custom kind, is free-form for its handler (claudeshare
	// keeps a small JSON record there): paths, names and hashes, never a
	// file's secret content. Only EffectFile's Before holds a file's old
	// content, and Txn.WriteFile refuses one that looks like it holds a
	// secret.
	Data map[string]string `json:"data,omitempty"`
	// Done is set once the side effect completed.
	Done bool `json:"done"`
}

// Entry is one journalled change.
type Entry struct {
	// V is the entry format: 2 for entries that carry Committed. Entries
	// written before it (V 0) are still read, and recovered the old way.
	V          int        `json:"v,omitempty"`
	ID         string     `json:"id"`
	Time       time.Time  `json:"time"`
	Summary    string     `json:"summary"`
	State      EntryState `json:"state"`
	Before     *Store     `json:"before"`
	After      *Store     `json:"after"`
	BeforeHash string     `json:"before_hash"`
	AfterHash  string     `json:"after_hash"`
	Effects    []Effect   `json:"effects,omitempty"`
	// Committed is written by Commit, after every side effect and before
	// accounts.toml is saved. A pending entry without it was never finished
	// and is rolled back at the next start; one with it is finished. It is
	// what tells the two apart, even for a change that leaves accounts.toml
	// as it was (sharing Claude Code's setup).
	Committed bool `json:"committed,omitempty"`
	// Group ties changes made by one command together (`devpit use work`
	// switching three tools): Undo takes the whole group back.
	Group string `json:"group,omitempty"`
}

// entryFormat is the V written on new entries.
const entryFormat = 2

// EffectState is where an effect's target is now.
type EffectState int

const (
	// AtBefore: the target is as it was before the change (or the effect
	// never happened).
	AtBefore EffectState = iota
	// AtAfter: the target holds exactly what the change wrote.
	AtAfter
	// Changed: the target was changed by something else since.
	Changed
)

// EffectHandler checks and reverts one kind of effect.
type EffectHandler interface {
	// State says where the effect's target is now.
	State(e Effect) (EffectState, error)
	// Revert puts the target back. It is only called in state AtAfter. A
	// note is a sentence for the person about something left in place.
	Revert(e Effect) (note string, err error)
}

// ErrNothingToUndo is returned when there is no change left to undo.
var ErrNothingToUndo = errors.New("there is no change to undo")

// ErrChangedByHand is matched (errors.Is) by [*ChangedError].
var ErrChangedByHand = errors.New("a file was changed by hand since Devpit wrote it")

// ChangedError says which file was changed by hand, so an undo or a
// recovery stopped without changing anything.
type ChangedError struct {
	Path string
	What string
}

func (e *ChangedError) Error() string {
	return fmt.Sprintf("%s was changed after Devpit %s, so Devpit stopped and changed nothing. Put it back the way it was, or make the change by hand", e.Path, e.What)
}

// Is makes errors.Is(err, ErrChangedByHand) true.
func (e *ChangedError) Is(target error) bool { return target == ErrChangedByHand }

// readJournal loads every entry, oldest first. A journal that will not parse
// is moved aside like a broken accounts.toml and reported once.
func readJournal(path string) ([]Entry, string, error) {
	data, err := readShared(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("reading %s: %w", path, err)
	}
	var out []Entry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return quarantineJournal(path, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return quarantineJournal(path, err)
	}
	return out, "", nil
}

func quarantineJournal(path string, cause error) ([]Entry, string, error) {
	broken, err := Quarantine(path)
	if err != nil {
		return nil, "", fmt.Errorf("the undo history at %s is damaged (%w) and could not be moved aside: %w", path, cause, err)
	}
	return nil, fmt.Sprintf("The undo history could not be read, so it was moved to %s. Your accounts and rules were not touched; only older changes can no longer be undone.",
		filepath.Base(broken)), nil
}

// writeJournal saves entries atomically, keeping every unfinished entry and
// the newest JournalLimit finished ones.
func writeJournal(path string, entries []Entry) error {
	finished := 0
	for _, e := range entries {
		if e.State != EntryPending && e.State != EntryUndoing {
			finished++
		}
	}
	var buf bytes.Buffer
	drop := finished - JournalLimit
	for _, e := range entries {
		if drop > 0 && e.State != EntryPending && e.State != EntryUndoing {
			drop--
			continue
		}
		b, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("encoding the undo history: %w", err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return writeAtomic(path, buf.Bytes())
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fileHandler undoes EffectFile.
type fileHandler struct{}

func (fileHandler) State(e Effect) (EffectState, error) {
	data, err := os.ReadFile(e.Target)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Changed, fmt.Errorf("reading %s: %w", e.Target, err)
	}
	h := ""
	if exists {
		h = hashBytes(data)
	}
	switch {
	case exists && h == e.AfterHash:
		return AtAfter, nil
	case !exists && !e.BeforeExists, exists && e.BeforeExists && h == e.BeforeHash:
		return AtBefore, nil
	case !exists && e.AfterHash == "":
		return AtAfter, nil // the effect removed the file
	}
	return Changed, nil
}

func (fileHandler) Revert(e Effect) (string, error) {
	if e.BeforeExists {
		return "", writeAtomic(e.Target, e.Before)
	}
	if err := os.Remove(e.Target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return "", nil
}

// dirHandler undoes EffectDir.
type dirHandler struct{}

func (dirHandler) State(e Effect) (EffectState, error) {
	fi, err := os.Lstat(e.Target)
	if errors.Is(err, os.ErrNotExist) {
		return AtBefore, nil
	}
	if err != nil {
		return Changed, err
	}
	if !fi.IsDir() {
		return Changed, nil
	}
	return AtAfter, nil
}

func (dirHandler) Revert(e Effect) (string, error) {
	entries, err := os.ReadDir(e.Target)
	if err != nil {
		return "", err
	}
	if len(entries) > 0 {
		return fmt.Sprintf("%s was kept: it holds files (a sign-in, most likely). Remove the account from Accounts to move it to the Recycle Bin.", e.Target), nil
	}
	return "", os.Remove(e.Target)
}

// credentialFile reports whether a path is a login file Devpit must never
// write, whatever an adapter asks.
func credentialFile(path string) bool {
	switch strings.ToLower(filepath.Base(path)) {
	case ".credentials.json", ".claude.json", "credentials.json", "hosts.yml",
		"auth.json", ".netrc", "_netrc", ".git-credentials", "access-token":
		return true
	}
	return false
}
