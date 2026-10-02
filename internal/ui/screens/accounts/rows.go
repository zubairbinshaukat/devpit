package accounts

import (
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/acctable"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// toolIcon is the nerd-tier glyph key for each tool's row.
func toolIcon(t accounts.Tool) string {
	switch t {
	case accounts.ToolGit, accounts.ToolGitHub:
		return "git"
	case accounts.ToolVercel, accounts.ToolFirebase, accounts.ToolCloudflare:
		return "globe"
	case accounts.ToolSupabase, accounts.ToolConvex:
		return "package"
	}
	return "key"
}

// splitDisplay splits the engine's "who" into the strong name and the quiet
// detail the table draws: "work (z@work.com)" → "work", "(z@work.com)";
// "Zubair <z@x.com>" → "Zubair", "<z@x.com>"; "project: quiz-slayer" →
// "project:", "quiz-slayer".
func splitDisplay(d string) (name, detail string) {
	for _, sep := range []string{" (", " <"} {
		if i := strings.Index(d, sep); i > 0 {
			return d[:i], d[i+1:]
		}
	}
	if strings.HasPrefix(d, "<") {
		return "", d
	}
	if i := strings.Index(d, ": "); i > 0 {
		return d[:i+1], d[i+2:]
	}
	return d, ""
}

// rowFor turns one tool's status into its table row. check, when set, is
// the latest live check of the account active here, which can say more than
// the offline overview (an expired login, a mismatch).
func rowFor(ctx uictx.Context, ts service.ToolStatus, check *service.VerifyCheck, fresh bool) acctable.Row {
	r := acctable.Row{
		ID: string(ts.Tool), Tool: ts.Tool.DisplayName(), Icon: toolIcon(ts.Tool), Fresh: fresh,
	}
	if !ts.Installed {
		r.State = acctable.StateNotInstalled
		if ts.Managed {
			r.Why, r.WhyKind, r.Note = "", acctable.WhyNone, "rules kept"
		}
		return r
	}
	r.Name, r.Detail = splitDisplay(ts.Display)
	why := ts.Why
	if strings.HasSuffix(why, " · beta") {
		why, r.Note = strings.TrimSuffix(why, " · beta"), "beta"
	}
	r.Why = why
	switch {
	case ts.Tool == accounts.ToolConvex:
		r.WhyKind = acctable.WhyProjectFile
	case ts.Resolution.Reason == accounts.ReasonFolderRule:
		r.WhyKind = acctable.WhyFolderRule
		r.Why, r.WhyPath = "folder rule:", ts.Resolution.RuleFolder
		if ts.Resolution.Via == accounts.ViaResolved && ts.Resolution.ResolvedFolder != "" {
			r.Note = "through " + ts.Resolution.ResolvedFolder
		}
	default:
		r.WhyKind = acctable.WhyEverywhere
	}
	switch ts.Identity.State() {
	case accounts.StateExpired:
		r.State, r.Note = acctable.StateExpired, "sign in again"
	case accounts.StateNotSignedIn:
		r.State = acctable.StateSignedOut
	}
	if check != nil {
		switch check.Status {
		case service.VerifyMismatch:
			r.State = acctable.StateMismatch
			if check.Actual != nil {
				switch check.Actual.State() {
				case accounts.StateExpired:
					r.State, r.Note = acctable.StateExpired, "sign in again"
				case accounts.StateNotSignedIn:
					r.State, r.Note = acctable.StateSignedOut, "sign in again"
				}
			}
		case service.VerifyError:
			r.State = acctable.StateProblem
		}
	}
	if p, ok := firstHere(ts.Problems); ok && r.State == acctable.StateOK {
		r.State, r.Note = acctable.StateProblem, problemWord(p)
	} else if len(ts.Problems) > 0 && r.Note == "" {
		// Only an old rule on another folder: worth a word, not a cross,
		// since nothing is wrong here.
		r.Note = problemWord(ts.Problems[0])
	}
	if c := chainCaption(ctx, ts.Resolution); c != "" {
		r.Caption = c
	}
	return r
}

// firstHere is the first problem that is about this folder, not an old rule
// on another one.
func firstHere(ps []service.Problem) (service.Problem, bool) {
	for _, p := range ps {
		switch p.Kind {
		case string(accounts.StaleFolderNotFound), string(accounts.StaleDriveNotConnected):
			continue
		}
		return p, true
	}
	return service.Problem{}, false
}

// problemWord is the short note a problem puts on its row.
func problemWord(p service.Problem) string {
	switch p.Kind {
	case string(accounts.StaleFolderNotFound), string(accounts.StaleDriveNotConnected):
		return "old rule: " + p.Kind
	}
	return "see below"
}

// chainCaption is the rule chain under a selected row when more than one
// rule covers the folder ("C:\Work → C:\Work\client (won)"), or when the
// rule matched only through a junction. "" otherwise.
func chainCaption(ctx uictx.Context, r accounts.Resolution) string {
	if len(r.Chain) < 2 && r.Via != accounts.ViaResolved {
		return ""
	}
	var parts []string
	for _, l := range r.Chain {
		p := l.Folder
		switch {
		case l.Won:
			p += " (won)"
		case l.Skipped:
			p += " (skipped: " + l.Account + " is gone)"
		}
		parts = append(parts, p)
	}
	s := strings.Join(parts, " "+ctx.Icons.Arrow+" ")
	if r.Via == accounts.ViaResolved && r.ResolvedFolder != "" {
		s += sep(ctx) + "matched through " + r.ResolvedFolder
	}
	return s
}

// rowsFor builds every row of the page.
func rowsFor(ctx uictx.Context, s *session) []acctable.Row {
	if s == nil {
		return nil
	}
	out := make([]acctable.Row, 0, len(s.ov.Tools))
	for _, ts := range s.ov.Tools {
		var check *service.VerifyCheck
		if c, ok := s.checks[ts.Tool]; ok {
			check = &c
		}
		out = append(out, rowFor(ctx, ts, check, s.fresh == ts.Tool))
	}
	return out
}

// managesAnything reports whether Devpit has any account or rule at all:
// the page teaches the first step when it does not.
func managesAnything(s *session) bool {
	if s == nil {
		return false
	}
	for _, ts := range s.ov.Tools {
		if ts.Managed {
			return true
		}
	}
	return false
}
