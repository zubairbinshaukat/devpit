package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/importer"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
)

// session is what every Accounts screen shares while the section is open:
// the engine, the folder the page is about, and the latest overview of it.
// The page holds it and hands it to every screen it opens, so a change made
// three screens deep is on the table the moment the person comes back.
//
// It is a pointer inside value screens, like Share Files' run handle, and
// it is only ever changed from a screen's Update, which Bubble Tea runs on
// one goroutine; the commands that work in the background return messages
// and never touch it.
type session struct {
	svc  Service
	opts Options

	// folder is the folder the page is about, as the engine spells it.
	folder string
	// ov is the latest overview of folder; ovErr is why there is none.
	ov    service.Overview
	ovErr error
	// gone is set when folder no longer exists.
	gone bool
	// folderNote is the engine's reason "this folder" needs a second look
	// (the home folder, a drive root); "" when it is fine.
	folderNote string
	// readOnly is set when another Devpit window holds the accounts lock.
	readOnly string
	// notices are shown once, at the top of the page: shims repaired, a
	// change a crash left half done, a quarantined accounts file.
	notices []notice
	// fresh is the tool whose row just changed, flagged once on the page.
	fresh accounts.Tool
	// checks are the latest live checks, per tool, of the account active in
	// folder, so a verify that found an expired login marks its row.
	checks map[accounts.Tool]service.VerifyCheck
	// found is what the first-open look found on this PC.
	found     importer.Found
	hasFound  bool
	dismissed bool
	// runs numbers the long operations, so a late event from one that was
	// left behind is never taken for the current one's.
	runs int
}

// notice is one line at the top of the page.
type notice struct {
	level string // "", "success", "warning", "danger"
	text  string
}

// timeout bounds every engine call a screen makes that runs no sign-in.
const timeout = 2 * time.Minute

// engineCtx is a context for one engine call.
func engineCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), timeout)
}

// status returns the overview row of tool, and whether there is one.
func (s *session) status(t accounts.Tool) (service.ToolStatus, bool) {
	if s == nil {
		return service.ToolStatus{}, false
	}
	for _, ts := range s.ov.Tools {
		if ts.Tool == t {
			return ts, true
		}
	}
	return service.ToolStatus{}, false
}

// Messages every Accounts screen understands.
type (
	// openedMsg is the engine opened and the first overview built.
	openedMsg struct {
		svc        Service
		err        error
		folder     string
		ov         service.Overview
		ovErr      error
		gone       bool
		folderNote string
		readOnly   string
		notices    []notice
		found      importer.Found
		hasFound   bool
		dismissed  bool
	}
	// overviewMsg is a fresh overview after a change, a verify or a new
	// folder.
	overviewMsg struct {
		folder string
		ov     service.Overview
		err    error
		gone   bool
		fresh  accounts.Tool
	}
)

// openCmd opens the engine off the first frame: crash recovery, the lock,
// the shims, the import look and the offline overview. Nothing in it runs
// Claude Code or any other tool's sign-in check.
func openCmd(opts Options) tea.Cmd {
	return func() tea.Msg {
		folder := opts.Folder
		if folder == "" {
			wd, err := opts.Getwd()
			if err != nil {
				return openedMsg{err: fmt.Errorf("finding the folder Devpit was started in: %w", err)}
			}
			folder = wd
		}
		svc, err := opts.Open()
		if err != nil {
			return openedMsg{err: err}
		}
		msg := openedMsg{svc: svc, folder: folder}
		if rec, rerr := svc.RecoveryReport(); len(rec) > 0 || rerr != nil {
			for _, r := range rec {
				msg.notices = append(msg.notices, notice{"warning", "Finished a change that was interrupted: " + r.Entry.Summary + " (" + r.Outcome + ")."})
			}
			if rerr != nil {
				msg.notices = append(msg.notices, notice{"danger", "A change that was interrupted could not be finished: " + accounts.Scrub(rerr.Error())})
			}
		}
		if herr := svc.Hold(); herr != nil {
			if errors.Is(herr, accounts.ErrLocked) {
				msg.readOnly = "Another Devpit window is changing accounts, so this one can look but not change anything. Close Accounts there, then press r here."
			} else {
				msg.readOnly = "Devpit could not take the accounts lock, so this window can look but not change anything: " + accounts.Scrub(herr.Error())
			}
		}
		if msg.readOnly == "" {
			// Opening the page repairs a missing or out-of-date shim; each
			// repair is one line at the top, so it never happens unseen.
			svc.SyncShims(func(ev accounts.Event) {
				switch ev.State {
				case accounts.StepDone:
					msg.notices = append(msg.notices, notice{"success", ev.Step + " (it was missing or out of date)."})
				case accounts.StepWarning:
					msg.notices = append(msg.notices, notice{"warning", ev.Detail})
				}
			})
		}
		ctx, cancel := engineCtx()
		defer cancel()
		if f, _, derr := svc.DetectClaudeAcc(ctx); derr == nil && !f.Empty() {
			msg.found, msg.hasFound = f, true
			msg.dismissed = svc.ImportDismissed(f)
		}
		msg.folderNote = svc.FolderCheck(folder)
		msg.ov, msg.ovErr = svc.Overview(ctx, folder)
		if msg.ovErr == nil {
			msg.folder = msg.ov.Folder
			if msg.ov.Warning != "" {
				msg.notices = append(msg.notices, notice{"warning", msg.ov.Warning})
			}
		}
		msg.gone = folderGone(opts, msg.folder)
		return msg
	}
}

// folderGone reports whether folder is missing right now.
func folderGone(opts Options, folder string) bool {
	if opts.Stat == nil || folder == "" {
		return false
	}
	fi, err := opts.Stat(folder)
	return err != nil || !fi.IsDir()
}

// refreshCmd builds a fresh overview of folder. fresh names the tool whose
// row just changed, so the page can flag it.
func (s *session) refreshCmd(folder string, fresh accounts.Tool) tea.Cmd {
	if s == nil || s.svc == nil {
		return nil
	}
	svc, opts := s.svc, s.opts
	return func() tea.Msg {
		ctx, cancel := engineCtx()
		defer cancel()
		ov, err := svc.Overview(ctx, folder)
		out := overviewMsg{folder: folder, ov: ov, err: err, fresh: fresh}
		if err == nil {
			out.folder = ov.Folder
		}
		out.gone = folderGone(opts, out.folder)
		return out
	}
}

// absorb takes a message every screen must handle the same way: a fresh
// overview. It reports whether msg was one.
func (s *session) absorb(msg tea.Msg) bool {
	m, ok := msg.(overviewMsg)
	if !ok || s == nil {
		return false
	}
	if m.err != nil {
		s.ovErr = m.err
		return true
	}
	if !strings.EqualFold(s.folder, m.folder) {
		s.checks = nil
		s.folderNote = s.svc.FolderCheck(m.folder)
	}
	s.folder, s.ov, s.ovErr, s.gone = m.folder, m.ov, nil, m.gone
	if m.fresh != "" {
		s.fresh = m.fresh
		delete(s.checks, m.fresh)
	}
	return true
}
