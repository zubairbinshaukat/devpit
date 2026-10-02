package service

import (
	"context"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
)

// The --json shapes. Field names are part of the command line's contract:
// they are only ever added to, never renamed or removed. No token can be in
// any of them: identities are built through accounts.NewIdentity, which
// scrubs every field, and nothing else here comes from a tool.

// AccountJSON is one account.
type AccountJSON struct {
	Name    string `json:"name"`
	Email   string `json:"email,omitempty"`
	Login   string `json:"login,omitempty"`
	Display string `json:"display"`
}

// RuleJSON is one rule of a chain.
type RuleJSON struct {
	Folder  string `json:"folder"`
	Account string `json:"account"`
	Won     bool   `json:"won"`
	Skipped bool   `json:"skipped"`
}

// CapsJSON is what Devpit can do with a tool (the static table).
type CapsJSON struct {
	FolderRules bool   `json:"folder_rules"`
	Everywhere  bool   `json:"everywhere"`
	JustOnce    bool   `json:"just_once"`
	AddAccount  bool   `json:"add_account"`
	ShowOnly    bool   `json:"show_only"`
	Beta        bool   `json:"beta"`
	Why         string `json:"why,omitempty"`
}

// StatusJSON is `devpit <tool> --json` and one entry of `devpit accounts
// --json`.
type StatusJSON struct {
	Tool       string      `json:"tool"`
	ToolName   string      `json:"tool_name"`
	Folder     string      `json:"folder"`
	Account    AccountJSON `json:"account"`
	Why        string      `json:"why"`
	Reason     string      `json:"reason"`
	RuleFolder string      `json:"rule_folder,omitempty"`
	Everywhere string      `json:"everywhere"`
	Chain      []RuleJSON  `json:"chain"`
	Installed  bool        `json:"installed"`
	Managed    bool        `json:"managed"`
	Supports   CapsJSON    `json:"supports"`
	Problems   []Problem   `json:"problems"`
}

func capsJSON(c adapters.Caps) CapsJSON {
	return CapsJSON{FolderRules: c.FolderRules, Everywhere: c.Everywhere, JustOnce: c.JustOnce, AddAccount: c.AddAccount, ShowOnly: c.ShowOnly, Beta: c.Beta, Why: c.Why}
}

// JSON is the stable --json shape of ts.
func (ts ToolStatus) JSON() StatusJSON {
	f := ts.Identity.Fields()
	out := StatusJSON{
		Tool: string(ts.Tool), ToolName: ts.Tool.DisplayName(), Folder: ts.Folder,
		Account:    AccountJSON{Name: ts.Account.Name, Email: f.Email, Login: f.Login, Display: ts.Display},
		Why:        ts.Why,
		Reason:     string(ts.Resolution.Reason),
		RuleFolder: ts.Resolution.RuleFolder,
		Everywhere: ts.EverywhereDisplay,
		Chain:      []RuleJSON{},
		Installed:  ts.Installed, Managed: ts.Managed, Supports: capsJSON(ts.Supports),
		Problems: append([]Problem{}, ts.Problems...),
	}
	if ts.Tool == accounts.ToolConvex {
		out.Account = AccountJSON{Name: accounts.DefaultName, Display: ts.Display}
		out.Reason = "project"
	}
	for _, l := range ts.Resolution.Chain {
		out.Chain = append(out.Chain, RuleJSON{Folder: l.Folder, Account: l.Account, Won: l.Won, Skipped: l.Skipped})
	}
	return out
}

// OverviewJSON is `devpit accounts --json`.
type OverviewJSON struct {
	Folder  string       `json:"folder"`
	Tools   []StatusJSON `json:"tools"`
	Warning string       `json:"warning,omitempty"`
}

// JSON is the stable --json shape of o.
func (o Overview) JSON() OverviewJSON {
	out := OverviewJSON{Folder: o.Folder, Tools: []StatusJSON{}, Warning: o.Warning}
	for _, t := range o.Tools {
		out.Tools = append(out.Tools, t.JSON())
	}
	return out
}

// ListEntryJSON is one account of `devpit <tool> list --json`.
type ListEntryJSON struct {
	Name         string   `json:"name"`
	Email        string   `json:"email,omitempty"`
	Login        string   `json:"login,omitempty"`
	Display      string   `json:"display"`
	Folder       string   `json:"folder,omitempty"`
	ImportedFrom string   `json:"imported_from,omitempty"`
	Detected     bool     `json:"detected"`
	HereNow      bool     `json:"here"`
	Everywhere   bool     `json:"everywhere"`
	Rules        []string `json:"rules"`
}

// ListJSON is `devpit <tool> list --json`.
type ListJSON struct {
	Tool     string          `json:"tool"`
	Folder   string          `json:"folder"`
	Accounts []ListEntryJSON `json:"accounts"`
}

// VerifyCheckJSON is one check of `devpit accounts verify --json`.
type VerifyCheckJSON struct {
	Tool     string             `json:"tool"`
	Account  string             `json:"account"`
	Here     bool               `json:"here"`
	Expected string             `json:"expected"`
	Actual   *accounts.Identity `json:"actual,omitempty"`
	Says     string             `json:"says,omitempty"`
	How      string             `json:"how"`
	Status   VerifyStatus       `json:"status"`
	Notes    []string           `json:"notes"`
}

// VerifyJSON is `devpit accounts verify --json`. mismatch is true exactly
// when the command exits 4.
type VerifyJSON struct {
	Folder   string            `json:"folder"`
	Mismatch bool              `json:"mismatch"`
	Checks   []VerifyCheckJSON `json:"checks"`
	Problems []Problem         `json:"problems"`
	Skipped  []string          `json:"skipped"`
}

// JSON is the stable --json shape of r.
func (r VerifyReport) JSON() VerifyJSON {
	out := VerifyJSON{Folder: r.Folder, Mismatch: r.Mismatch, Checks: []VerifyCheckJSON{}, Problems: append([]Problem{}, r.Problems...), Skipped: append([]string{}, r.Skipped...)}
	for _, c := range r.Checks {
		out.Checks = append(out.Checks, VerifyCheckJSON{
			Tool: string(c.Tool), Account: c.Account, Here: c.Here, Expected: c.Expected, Actual: c.Actual,
			Says: c.ActualDisplay, How: c.How, Status: c.Status, Notes: append([]string{}, c.Notes...),
		})
	}
	return out
}

// List returns the accounts of tool: default, the store's, then any the
// tool itself knows of that Devpit does not have yet (detected; this may
// run the tool's own account list, never a sign-in check of Claude Code).
// folder marks the one in use there.
func (s *Service) List(ctx context.Context, tool accounts.Tool, folder string) (ListJSON, error) {
	st, _, err := s.Load()
	if err != nil {
		return ListJSON{}, err
	}
	a, err := s.Adapter(tool)
	if err != nil {
		return ListJSON{}, err
	}
	res, err := accounts.Resolve(st, tool, folder, accounts.ResolveOptions{RealPath: accounts.RealPath})
	if err != nil {
		return ListJSON{}, err
	}
	list, lerr := a.Accounts(ctx, st)
	if len(list) == 0 && lerr != nil {
		return ListJSON{}, lerr
	}
	out := ListJSON{Tool: string(tool), Folder: res.Folder, Accounts: []ListEntryJSON{}}
	every := accounts.DefaultName
	if v, ok := st.Everywhere[tool]; ok {
		every = v
	}
	for _, acct := range list {
		f := a.Cached(st, acct).Fields()
		e := ListEntryJSON{
			Name: acct.Name, Email: f.Email, Login: f.Login, Display: s.display(st, acct, a.Cached(st, acct)),
			Folder: acct.Dir, ImportedFrom: acct.ImportedFrom, Detected: acct.ImportedFrom == "detected" && !storeHas(st, acct),
			HereNow: strings.EqualFold(acct.Name, res.Account.Name), Everywhere: strings.EqualFold(acct.Name, every), Rules: []string{},
		}
		if e.Detected {
			e.Display = acct.Name + " (" + firstNonEmpty(acct.Label, acct.Email, "found in "+tool.DisplayName()) + ", not in Devpit yet)"
		}
		for _, r := range st.RulesUsing(tool, acct.Name) {
			e.Rules = append(e.Rules, r.Folder)
		}
		out.Accounts = append(out.Accounts, e)
	}
	return out, nil
}

func storeHas(st *accounts.Store, a accounts.Account) bool {
	got, ok := st.Account(a.Tool, a.Name)
	return ok && !got.IsDefault() && got.ImportedFrom == a.ImportedFrom
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// ListLines is the list as `devpit <tool> list` prints it.
func ListLines(l ListJSON) []string {
	var out []string
	for _, a := range l.Accounts {
		mark := "  "
		if a.HereNow {
			mark = "* "
		}
		line := mark + a.Display
		var tags []string
		if a.Everywhere {
			tags = append(tags, "everywhere")
		}
		if len(a.Rules) > 0 {
			tags = append(tags, "rules: "+strings.Join(a.Rules, ", "))
		}
		if len(tags) > 0 {
			line += "   " + strings.Join(tags, " · ")
		}
		out = append(out, line)
	}
	out = append(out, "", "* is the account used in "+l.Folder+".")
	return out
}
