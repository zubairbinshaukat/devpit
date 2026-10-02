package adapters

import (
	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// Row is one line of the Accounts page: which account a tool uses here,
// why, and who that account is as far as Devpit knows.
type Row struct {
	Tool accounts.Tool
	// Resolution is the account, the reason and the rule chain.
	Resolution accounts.Resolution
	// Identity is the Cached identity: what Devpit recorded at sign-in,
	// import or the last Verify. Never a live answer.
	Identity accounts.Identity
	// Installed and Path: the tool was found on PATH, outside the shim
	// folder. It was not run.
	Installed bool
	Path      string
	// LiveRisk, when set, is the sentence a screen shows before a live
	// check (WhoAmI) of an account that is not the one active here.
	LiveRisk string
	// Problems are the resolution's (a rule naming a missing account) and
	// the environment's (CheckEnv: a stray CLAUDE_CONFIG_DIR, a GH_TOKEN).
	Problems []accounts.Problem
}

// Overview builds the Accounts page for folder without running any tool:
// resolution from the store, identities from what was recorded, and
// "installed" from PATH alone. A live check is a separate, explicit step
// (Adapter.WhoAmI) because for Claude Code polling can sign an idle account
// out (Claude Code issue #95822).
func Overview(d Deps, s *accounts.Store, folder string, opt accounts.ResolveOptions) ([]Row, error) {
	if s == nil {
		s = accounts.NewStore()
	}
	all := All(d)
	rows := make([]Row, 0, len(all))
	for _, a := range all {
		t := a.Tool()
		res, err := accounts.Resolve(s, t, folder, opt)
		if err != nil {
			return nil, err
		}
		row := Row{Tool: t, Resolution: res, Identity: a.Cached(s, res.Account)}
		bins := []string{t.Binary()}
		if t == accounts.ToolConvex {
			bins = append(bins, "npx")
		}
		for _, b := range bins {
			if p, err := d.lookPath(b); err == nil {
				row.Installed, row.Path = true, p
				break
			}
		}
		if risky, why := a.LiveCheckRisk(); risky {
			row.LiveRisk = why
		}
		row.Problems = append(append(row.Problems, res.Problems...), CheckEnv(t, d.getenv)...)
		rows = append(rows, row)
	}
	return rows, nil
}
