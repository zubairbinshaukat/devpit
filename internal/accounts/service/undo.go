package service

import (
	"context"
	"fmt"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// UndoInfo is what an undo would take back, checked and said in plain
// words. Entries are newest first.
type UndoInfo struct {
	Entries []accounts.Entry
	Lines   []string
}

// UndoPreview says what `devpit undo` (or `u` on the done card) would take
// back. It changes nothing. It fails with accounts.ErrNothingToUndo, or
// with an error matching accounts.ErrChangedByHand that names the file
// changed by hand since Devpit wrote it.
func (s *Service) UndoPreview() (UndoInfo, error) {
	entries, err := s.Engine.UndoPreview()
	if err != nil {
		return UndoInfo{}, err
	}
	info := UndoInfo{Entries: entries}
	if len(entries) > 1 {
		info.Lines = append(info.Lines, fmt.Sprintf("Undo these %d changes, made together:", len(entries)))
	} else {
		info.Lines = append(info.Lines, "Undo this change:")
	}
	for _, e := range entries {
		info.Lines = append(info.Lines, fmt.Sprintf("  %s (%s)", e.Summary, e.Time.Local().Format("2006-01-02 15:04")))
		for i := len(e.Effects) - 1; i >= 0; i-- {
			eff := e.Effects[i]
			if !eff.Done {
				continue
			}
			switch eff.Kind {
			case accounts.EffectDir:
				info.Lines = append(info.Lines, "    keeps "+eff.Target+" if it holds anything (a sign-in is never deleted)")
			default:
				info.Lines = append(info.Lines, "    puts back: "+eff.Summary)
			}
		}
	}
	info.Lines = append(info.Lines, "accounts.toml goes back to how it was before.")
	return info, nil
}

// Undo takes back the latest change (and the changes made together with
// it), then brings the shims in line. Notes are sentences about anything
// left in place on purpose.
func (s *Service) Undo(_ context.Context, emit func(accounts.Event)) (accounts.UndoResult, error) {
	res, err := s.Engine.Undo()
	if err != nil {
		return res, err
	}
	if emit == nil {
		emit = func(accounts.Event) {}
	}
	s.syncShims(emit)
	return res, nil
}
