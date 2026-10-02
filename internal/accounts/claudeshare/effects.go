package claudeshare

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// Each step is one journal effect of kind "claudeshare.<op>". What a handler
// needs to check and undo it is a small JSON record in Effect.Before: paths,
// names and hashes, never file contents (except Devpit's own sharing record)
// and never a value that may be a secret. Effect.Data is left empty: the
// journal refuses Data that merely looks like a token, and long skill names
// and temporary folder names can.

const effectPrefix = "claudeshare."

func kindOf(op Op) accounts.EffectKind { return accounts.EffectKind(effectPrefix + string(op)) }

// opPending is the marker that keeps an unfinished change from looking
// finished (see markUnfinished).
const opPending Op = "pending"

func handledOps() []Op {
	return []Op{OpLink, OpUnlink, OpMove, OpCopy, OpRmdir, OpMergeJSON, OpImport, OpWriteFile, OpState, opPending}
}

// Handlers returns the effect handlers for this package's steps.
func Handlers() map[accounts.EffectKind]accounts.EffectHandler {
	m := map[accounts.EffectKind]accounts.EffectHandler{}
	for _, op := range handledOps() {
		m[kindOf(op)] = handler{}
	}
	return m
}

// Register adds this package's effect handlers to e. The app must call it
// at start-up, before Recover or Undo, so an interrupted or finished share
// can be put back. Call it before the engine is used from more than one
// goroutine.
func Register(e *accounts.Engine) {
	if e.Handlers == nil {
		e.Handlers = map[accounts.EffectKind]accounts.EffectHandler{}
	}
	for k, h := range Handlers() {
		e.Handlers[k] = h
	}
}

// record is what a step needs for its undo.
type record struct {
	Op          Op       `json:"op"`
	From        string   `json:"from,omitempty"`
	To          string   `json:"to,omitempty"`
	Tmp         string   `json:"tmp,omitempty"`
	Fingerprint string   `json:"fp,omitempty"`
	Inserted    string   `json:"inserted,omitempty"`
	Created     bool     `json:"created,omitempty"`
	Hash        string   `json:"hash,omitempty"`
	Prev        []byte   `json:"prev,omitempty"`
	PrevExisted bool     `json:"prev_existed,omitempty"`
	JSON        *jsonRec `json:"json,omitempty"`
}

func decode(e accounts.Effect) (record, error) {
	var r record
	if err := json.Unmarshal(e.Before, &r); err != nil {
		return r, fmt.Errorf("the undo record for %s could not be read: %w", e.Target, err)
	}
	if string(e.Kind) != effectPrefix+string(r.Op) {
		return r, fmt.Errorf("the undo record for %s does not match its kind", e.Target)
	}
	return r, nil
}

type handler struct{}

func toEngine(s int) accounts.EffectState {
	switch s {
	case stAfter:
		return accounts.AtAfter
	case stBefore:
		return accounts.AtBefore
	}
	return accounts.Changed
}

// State says where a step's target is now. Each answer allows for the steps
// that came after it in the same change (a link made where a folder was
// moved away, a copy moved into place), because undo checks every step
// before putting any back, then puts them back newest first.
func (handler) State(e accounts.Effect) (accounts.EffectState, error) {
	r, err := decode(e)
	if err != nil {
		return accounts.Changed, err
	}
	switch r.Op {
	case opPending:
		return accounts.AtBefore, nil // nothing to put back
	case OpLink:
		li, err := readLink(r.To)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return accounts.AtBefore, nil
		case err != nil:
			return accounts.Changed, err
		case li.kind == linkJunction && samePath(li.target, r.From):
			return accounts.AtAfter, nil
		case !li.isLink() && isEmptyRealDir(r.To):
			return accounts.AtAfter, nil // the folder was made, the link not yet
		}
		return accounts.Changed, nil
	case OpUnlink:
		li, err := readLink(r.To)
		if err == nil && li.kind == linkJunction && samePath(li.target, r.From) {
			return accounts.AtBefore, nil
		}
		// Gone, or replaced by what a later step put there (the copy moved
		// into place, a repaired link); those are put back first, and the
		// revert refuses if anything is still in the way.
		return accounts.AtAfter, nil
	case OpMove:
		switch {
		case exists(r.To):
			return accounts.AtAfter, nil
		case exists(r.From):
			return accounts.AtBefore, nil
		}
		return accounts.Changed, nil
	case OpCopy:
		if r.Tmp != "" && exists(r.Tmp) {
			return accounts.AtAfter, nil
		}
		if !exists(r.To) {
			return accounts.AtBefore, nil
		}
		if r.Fingerprint != "" {
			if fp, err := fingerprint(r.To); err == nil && fp == r.Fingerprint {
				return accounts.AtAfter, nil
			}
			return accounts.Changed, nil
		}
		if same, err := sameContent(r.From, r.To); err == nil && same {
			return accounts.AtAfter, nil
		}
		return accounts.Changed, nil
	case OpRmdir:
		fi, err := lstat(r.To)
		switch {
		case errors.Is(err, os.ErrNotExist), err == nil && isReparse(fi):
			return accounts.AtAfter, nil
		case err != nil:
			return accounts.Changed, err
		case isEmptyRealDir(r.To):
			return accounts.AtBefore, nil
		}
		return accounts.Changed, nil
	case OpMergeJSON:
		if r.JSON == nil {
			return accounts.Changed, errors.New("the undo record has no JSON part")
		}
		s, err := jsonState(*r.JSON)
		return toEngine(s), err
	case OpImport:
		b, err := readFile(r.To)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return accounts.AtBefore, nil
		case err != nil:
			return accounts.Changed, err
		case strings.Contains(string(b), r.Inserted):
			return accounts.AtAfter, nil
		case strings.Contains(string(b), mdBegin):
			return accounts.Changed, nil
		}
		return accounts.AtBefore, nil
	case OpWriteFile:
		b, err := readFile(r.To)
		switch {
		case errors.Is(err, os.ErrNotExist):
			return accounts.AtBefore, nil
		case err != nil:
			return accounts.Changed, err
		case hashBytes(b) == r.Hash:
			return accounts.AtAfter, nil
		}
		return accounts.Changed, nil
	case OpState:
		b, err := readFile(r.To)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if r.PrevExisted {
				return accounts.Changed, nil
			}
			return accounts.AtBefore, nil
		case err != nil:
			return accounts.Changed, err
		case hashBytes(b) == r.Hash:
			return accounts.AtAfter, nil
		case r.PrevExisted && hashBytes(b) == hashBytes(r.Prev):
			return accounts.AtBefore, nil
		}
		return accounts.Changed, nil
	}
	return accounts.Changed, fmt.Errorf("unknown step %q", r.Op)
}

// Revert puts one step back. It is called only in state AtAfter, newest
// step first.
func (handler) Revert(e accounts.Effect) (string, error) {
	r, err := decode(e)
	if err != nil {
		return "", err
	}
	switch r.Op {
	case opPending:
		return "", nil
	case OpLink:
		li, err := readLink(r.To)
		if err != nil {
			return "", err
		}
		if li.isLink() {
			return "", removeLink(r.To, r.From)
		}
		return "", removeEmptyDir(r.To)
	case OpUnlink:
		if exists(r.To) {
			return "", fmt.Errorf("%s is in the way of putting the link back", r.To)
		}
		return "", createJunction(r.To, r.From)
	case OpMove:
		if exists(r.From) {
			return "", fmt.Errorf("%s is in the way of moving %s back", r.From, r.To)
		}
		return "", explainInUse(moveNoReplace(r.To, r.From), r.To)
	case OpCopy:
		if r.Tmp != "" {
			if err := removeTree(r.Tmp); err != nil {
				return "", err
			}
		}
		return "", removeTree(r.To)
	case OpRmdir:
		if exists(r.To) {
			return "", nil
		}
		return "", os.Mkdir(fsPath(r.To), 0o700)
	case OpMergeJSON:
		out, remove, err := revertMerge(*r.JSON)
		if err != nil {
			return "", err
		}
		switch {
		case remove:
			if err := os.Remove(fsPath(r.JSON.File)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		case out != nil:
			if err := writeAtomic(r.JSON.File, out); err != nil {
				return "", err
			}
		}
		if r.JSON.Sidecar != "" {
			if err := os.Remove(fsPath(r.JSON.Sidecar)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		}
		return "", nil
	case OpImport:
		b, err := readFile(r.To)
		if err != nil {
			return "", err
		}
		out, _ := removeImport(b, r.Inserted)
		if r.Created && len(out) == 0 {
			return "", os.Remove(fsPath(r.To))
		}
		return "", writeAtomic(r.To, out)
	case OpWriteFile:
		return "", os.Remove(fsPath(r.To))
	case OpState:
		if r.PrevExisted {
			return "", writeAtomic(r.To, r.Prev)
		}
		return "", os.Remove(fsPath(r.To))
	}
	return "", fmt.Errorf("unknown step %q", r.Op)
}

func isEmptyRealDir(p string) bool {
	if !isRealDir(p) {
		return false
	}
	ents, err := readDir(p)
	return err == nil && len(ents) == 0
}

// explainInUse turns a sharing violation into a sentence a person can act
// on.
func explainInUse(err error, path string) error {
	if err != nil && inUse(err) {
		return &InUseError{Path: path, Err: err}
	}
	return err
}
