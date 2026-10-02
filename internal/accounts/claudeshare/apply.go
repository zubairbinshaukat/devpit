package claudeshare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// ErrNothingToDo is returned by Apply for a plan with no steps.
var ErrNothingToDo = errors.New("nothing to change: this account already matches")

// Apply runs a plan as one change in the accounts journal. It first builds
// the plan again from the folders as they are now and refuses with
// [ErrStalePlan] if anything differs from the preview. Every step is
// recorded before it happens; if one fails, everything done so far is put
// back and the error says why. emit (may be nil) gets one running and one
// done event per step, and a final event carrying the journal entry for
// undo. Apply registers this package's undo handlers on eng.
func Apply(ctx context.Context, eng *accounts.Engine, p Plan, emit func(accounts.Event)) (accounts.Entry, error) {
	if emit == nil {
		emit = func(accounts.Event) {}
	}
	if p.Empty() {
		return accounts.Entry{}, ErrNothingToDo
	}
	if p.build == nil {
		return accounts.Entry{}, errors.New("this plan was not made by Devpit's planner, so it cannot be applied")
	}
	Register(eng)

	const checking = "Checking nothing changed since the preview"
	emit(accounts.NewEvent(checking, accounts.StepRunning, ""))
	fresh, err := p.build()
	if err != nil {
		emit(accounts.Failed(checking, err))
		return accounts.Entry{}, err
	}
	if !sameSteps(fresh.Steps, p.Steps) {
		emit(accounts.Failed(checking, ErrStalePlan))
		return accounts.Entry{}, ErrStalePlan
	}
	emit(accounts.NewEvent(checking, accounts.StepDone, ""))

	if perr := probeLinks(p); perr != nil {
		emit(accounts.Failed("Checking that links can be made here", perr))
		return accounts.Entry{}, perr
	}

	txn, err := eng.Begin(p.Summary, nil)
	if err != nil {
		emit(accounts.Failed("Starting the change", err))
		return accounts.Entry{}, err
	}
	if merr := markUnfinished(txn); merr != nil {
		return accounts.Entry{}, fail(txn, emit, "Starting the change", merr)
	}
	for _, st := range p.Steps {
		if cerr := ctx.Err(); cerr != nil {
			return accounts.Entry{}, fail(txn, emit, "Stopped", cerr)
		}
		emit(accounts.NewEvent(st.Text, accounts.StepRunning, ""))
		notes, serr := runStep(txn, p.Roots, st)
		if serr != nil {
			return accounts.Entry{}, fail(txn, emit, st.Text, serr)
		}
		for _, n := range notes {
			emit(accounts.NewEvent(st.Text, accounts.StepWarning, n))
		}
		emit(accounts.NewEvent(st.Text, accounts.StepDone, ""))
	}
	ent, err := txn.Commit()
	if err != nil {
		emit(accounts.Failed("Saving the change", err))
		return ent, err
	}
	final := accounts.NewEvent("Done", accounts.StepDone, fmt.Sprintf("%d step(s). Press u to undo.", len(p.Steps)))
	final.Final = true
	final.EntryID = ent.ID
	emit(final)
	return ent, nil
}

// ApplyStream is Apply with its events on a channel, for a screen. The
// channel closes after the final event.
func ApplyStream(ctx context.Context, eng *accounts.Engine, p Plan) <-chan accounts.Event {
	ch := make(chan accounts.Event, 16)
	go func() {
		defer close(ch)
		sawFinal := false
		_, err := Apply(ctx, eng, p, func(ev accounts.Event) {
			sawFinal = sawFinal || ev.Final
			ch <- ev
		})
		if err != nil && !sawFinal {
			ch <- accounts.Failed("Applying", err)
		}
	}()
	return ch
}

// fail puts back what was done and reports both errors, if putting back
// failed too.
func fail(txn *accounts.Txn, emit func(accounts.Event), step string, cause error) error {
	err := fmt.Errorf("%w. Everything done so far was put back", cause)
	if rb := txn.Rollback(); rb != nil {
		err = fmt.Errorf("%w. Putting back what was done also failed (%v); run Devpit again and it finishes putting things back", cause, rb) //nolint:errorlint // the second error is only described
	}
	emit(accounts.Failed(step, err))
	return err
}

func sameSteps(a, b []Step) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Op != y.Op || x.Kind != y.Kind || !strings.EqualFold(x.From, y.From) || !strings.EqualFold(x.To, y.To) ||
			hashBytes(x.Content) != hashBytes(y.Content) {
			return false
		}
		if (x.JSON == nil) != (y.JSON == nil) {
			return false
		}
		if x.JSON != nil {
			ja, _ := json.Marshal(x.JSON)
			jb, _ := json.Marshal(y.JSON)
			if string(ja) != string(jb) {
				return false
			}
		}
	}
	return true
}

// checkStep is the structural guard every step passes right before it
// runs: nothing in the default home is ever moved, removed, merged into or
// overwritten (it only gains folders and moved-in items), links and copies
// land only in the account, and login files are never touched.
func (r Roots) checkStep(st Step) error {
	inT, inS := r.inTarget, r.inSource
	refuse := func(why string) error {
		return fmt.Errorf("refusing %q: %s", st.Text, why)
	}
	switch st.Op {
	case OpMergeJSON:
		if st.JSON == nil {
			return refuse("no merge")
		}
		ok := samePlace(st.JSON.File, r.dstPath(settingsFile)) || samePlace(st.JSON.File, r.TargetClaudeJSON())
		if !ok || !samePlace(st.JSON.File, st.To) {
			return refuse("Devpit merges only into the account's settings.json or .claude.json")
		}
		for _, op := range st.JSON.Ops {
			if err := guardClaudeJSON(st.JSON.File, op.Path, op.targetPath()); err != nil {
				return err
			}
		}
		if st.JSON.Sidecar != "" && (!inT(st.JSON.Sidecar) || deniedAnywhere(filepath.Base(st.JSON.Sidecar))) {
			return refuse("the kept values must go in the account's backup folder")
		}
		return nil
	case OpState:
		if !samePlace(st.To, statePath(r.Target)) {
			return refuse("the sharing record lives only in the account folder")
		}
		return nil
	}
	for _, p := range []string{st.From, st.To} {
		if err := r.checkDenied(p); err != nil {
			return err
		}
	}
	switch st.Op {
	case OpMkdir:
		if !inT(st.To) && !inS(st.To) {
			return refuse("outside both account folders")
		}
	case OpLink:
		if !inT(st.To) || !inS(st.From) {
			return refuse("links are made only in the account, to the default account's folder")
		}
	case OpUnlink, OpRmdir, OpImport, OpWriteFile:
		if !inT(st.To) {
			return refuse("only the account's own folder is changed")
		}
	case OpMove:
		if !inT(st.From) || (!inT(st.To) && !inS(st.To)) {
			return refuse("nothing in the default account is ever moved")
		}
	case OpCopy:
		if !inT(st.To) || (!inT(st.From) && !inS(st.From)) {
			return refuse("copies land only in the account")
		}
	default:
		return refuse("unknown step")
	}
	return nil
}

// runStep checks a step's starting point, then records and performs it.
func runStep(t *accounts.Txn, r Roots, st Step) ([]string, error) {
	if err := r.checkStep(st); err != nil {
		return nil, err
	}
	stale := func(what string) error { return fmt.Errorf("%s: %w", what, ErrStalePlan) }
	rec := record{Op: st.Op, From: st.From, To: st.To}
	var notes []string
	switch st.Op {
	case OpMkdir:
		return nil, t.MakeDir(st.To, st.Text)
	case OpLink:
		if exists(st.To) {
			return nil, stale(st.To + " exists now")
		}
		if !exists(st.From) {
			return nil, stale(st.From + " is gone")
		}
		return nil, do(t, st, rec, func(*record) error { return createJunction(st.To, st.From) })
	case OpUnlink:
		li, err := readLink(st.To)
		if err != nil || li.kind != linkJunction || !samePath(li.target, st.From) {
			return nil, stale(st.To + " is no longer the link it was")
		}
		return nil, do(t, st, rec, func(*record) error { return removeLink(st.To, st.From) })
	case OpMove:
		fi, err := lstat(st.From)
		if err != nil || isReparse(fi) {
			return nil, stale(st.From + " is gone or became a link")
		}
		if exists(st.To) {
			return nil, stale(st.To + " exists now")
		}
		return nil, do(t, st, rec, func(*record) error {
			return explainInUse(moveNoReplace(st.From, st.To), st.From)
		})
	case OpCopy:
		if exists(st.To) {
			return nil, stale(st.To + " exists now")
		}
		rec.Tmp = filepath.Join(filepath.Dir(st.To), filepath.Base(st.To)+".devpit-copy-"+randHex(4))
		err := do(t, st, rec, func(rc *record) error {
			rep, err := copyTree(st.From, rc.Tmp)
			if err != nil {
				_ = removeTree(rc.Tmp)
				return err
			}
			if rerr := moveNoReplace(rc.Tmp, st.To); rerr != nil {
				_ = removeTree(rc.Tmp)
				return explainInUse(rerr, st.To)
			}
			fp, err := fingerprint(st.To)
			if err != nil {
				return err
			}
			rc.Fingerprint = fp
			for _, l := range rep.SkippedLinks {
				notes = append(notes, "Left out "+l+": it is a link, and links are never copied.")
			}
			for _, d := range rep.SkippedDenied {
				notes = append(notes, "Left out "+d+": login files are never copied.")
			}
			return nil
		})
		return notes, err
	case OpRmdir:
		if !isEmptyRealDir(st.To) {
			return nil, stale(st.To + " is not an empty folder")
		}
		return nil, do(t, st, rec, func(*record) error { return explainInUse(removeEmptyDir(st.To), st.To) })
	case OpMergeJSON:
		out, jrec, side, err := prepareMerge(*st.JSON)
		if err != nil {
			return nil, err
		}
		rec.JSON = &jrec
		return nil, do(t, st, rec, func(*record) error {
			if side != nil {
				if err := writeNew(jrec.Sidecar, side); err != nil {
					return err
				}
			}
			return writeAtomic(st.JSON.File, out)
		})
	case OpImport:
		b, err := readFile(st.To)
		created := errors.Is(err, os.ErrNotExist)
		if err != nil && !created {
			return nil, err
		}
		if ok, _ := hasImportBlock(b); ok {
			return nil, stale(st.To + " already brings in a CLAUDE.md")
		}
		out, ins := addImport(b, st.From)
		rec.Inserted, rec.Created = ins, created
		return nil, do(t, st, rec, func(*record) error {
			if created {
				return writeNew(st.To, out)
			}
			return writeAtomic(st.To, out)
		})
	case OpWriteFile:
		if exists(st.To) {
			return nil, stale(st.To + " exists now")
		}
		rec.Hash = hashBytes(st.Content)
		return nil, do(t, st, rec, func(*record) error { return writeNew(st.To, st.Content) })
	case OpState:
		prev, err := readFile(st.To)
		switch {
		case err == nil:
			rec.Prev, rec.PrevExisted = prev, true
		case !errors.Is(err, os.ErrNotExist):
			return nil, err
		}
		rec.Hash = hashBytes(st.Content)
		return nil, do(t, st, rec, func(*record) error { return writeAtomic(st.To, st.Content) })
	}
	return nil, fmt.Errorf("unknown step %q", st.Op)
}

// LinkError is returned by Apply, before anything is changed, when the plan
// has links but Windows refuses to make one in the account folder (a
// policy, a file system the drive check did not catch). Build the plan again
// with Selection.CopyInsteadOfLinks set and show the new preview.
type LinkError struct {
	Reason string
}

func (e *LinkError) Error() string {
	return "links cannot be made in this account folder (" + e.Reason + "). Nothing was changed; Devpit can copy instead"
}

// probeLinks makes one junction in the account folder and removes it again,
// when the plan has links, so a refusal is found before anything changes.
func probeLinks(p Plan) error {
	for _, s := range p.Steps {
		if s.Op != OpLink {
			continue
		}
		// The probe points at nothing, so even a crash right here leaves only
		// a harmless dangling link.
		probe := filepath.Join(p.Roots.Target, ".devpit-linkprobe-"+randHex(4))
		to := probe + ".nothing"
		if err := createJunction(probe, to); err != nil {
			return &LinkError{Reason: err.Error()}
		}
		if err := removeLink(probe, to); err != nil {
			return fmt.Errorf("removing the test link %s: %w", probe, err)
		}
		return nil
	}
	return nil
}

// errUnfinished keeps the marker effect not-done; see markUnfinished.
var errUnfinished = errors.New("marker: the change is in progress")

// markUnfinished records a marker effect that is never marked done. The
// engine's recovery treats a pending change whose effects are all done and
// whose accounts.toml matches as finished; a share leaves accounts.toml as
// it is, so without the marker a crash between two steps would look
// finished. With it, recovery rolls the half-made change back. The marker
// has nothing to undo, and a committed change with it undoes normally.
func markUnfinished(t *accounts.Txn) error {
	b, _ := json.Marshal(record{Op: opPending})
	eff := accounts.Effect{
		Kind: kindOf(opPending), Tool: accounts.ToolClaude, Target: "-",
		Summary: "A Claude Code setup change started", Before: b,
	}
	err := t.Do(eff, func() (accounts.Effect, error) { return eff, errUnfinished })
	if errors.Is(err, errUnfinished) {
		return nil
	}
	if err == nil {
		return errors.New("the change could not be marked as in progress")
	}
	return err
}

// do records the step, runs fn, and records what fn learnt.
func do(t *accounts.Txn, st Step, rec record, fn func(*record) error) error {
	before, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	eff := accounts.Effect{Kind: kindOf(st.Op), Tool: accounts.ToolClaude, Target: st.To, Summary: st.Text, Before: before}
	return t.Do(eff, func() (accounts.Effect, error) {
		if err := fn(&rec); err != nil {
			return eff, err
		}
		b, err := json.Marshal(rec)
		if err != nil {
			return eff, err
		}
		eff.Before = b
		return eff, nil
	})
}
