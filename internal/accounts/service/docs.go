package service

import (
	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/shims"
)

// DocsBase is where the troubleshooting pages live. A page's Markdown twin
// is the same address with ".md" added.
const DocsBase = "https://devpit.zubyr.dev/docs/troubleshooting/"

// docsPages is the one table from a problem kind to the troubleshooting page
// that explains it (the slug of a file in
// web/docs-site/src/content/docs/troubleshooting). A test walks every kind
// and fails if one is missing here or its page does not exist.
var docsPages = map[string]string{
	string(accounts.ProblemMissingAccount):    "accounts-wrong-account-used",
	string(accounts.ProblemEverywhereMissing): "accounts-wrong-account-used",
	string(accounts.ProblemEnvOverride):       "accounts-wrong-account-used",
	string(accounts.StaleFolderNotFound):      "accounts-folder-rule-not-applying",
	string(accounts.StaleDriveNotConnected):   "accounts-folder-rule-not-applying",
	string(shims.IssueShimMissing):            "accounts-folder-rule-not-applying",
	string(shims.IssueStaleShim):              "accounts-folder-rule-not-applying",
	string(shims.IssueShimProgramMissing):     "accounts-folder-rule-not-applying",
	string(shims.IssueShadowed):               "accounts-shim-not-first-on-path",
	string(shims.IssueNotOnPath):              "accounts-shim-not-first-on-path",
	string(shims.IssueNewTerminal):            "accounts-shim-not-first-on-path",
	string(adapters.ProblemWranglerDrift):     "accounts-folder-rule-not-applying",
}

// docsPagesForTool are the kinds whose page depends on the tool: a variable
// set in the terminal has its own page for Claude Code (CLAUDE_CONFIG_DIR).
var docsPagesForTool = map[string]string{
	string(accounts.ProblemEnvOverride) + "/" + string(accounts.ToolClaude): "claude-config-dir-set-by-something-else",
}

// verifyPages are the Verify outcomes that have a page: a sign-in that is
// not the expected account, and a sign-in that expired or was signed out.
var verifyPages = map[string]string{
	"mismatch":   "accounts-wrong-account-used",
	"signed out": "accounts-signed-out-or-expired",
	"push":       "git-push-asks-for-password-or-wrong-account",
}

// DocsSlug is the troubleshooting page for a problem kind (and tool), or ""
// when no page explains it.
func DocsSlug(kind, tool string) string {
	if s, ok := docsPagesForTool[kind+"/"+tool]; ok {
		return s
	}
	return docsPages[kind]
}

// DocsURL is the full address of a troubleshooting page, or "".
func DocsURL(slug string) string {
	if slug == "" {
		return ""
	}
	return DocsBase + slug
}

// DocsSlugs lists every page the table points at, for the test that checks
// each one exists.
func DocsSlugs() map[string]bool {
	out := map[string]bool{}
	for _, m := range []map[string]string{docsPages, docsPagesForTool, verifyPages} {
		for _, s := range m {
			out[s] = true
		}
	}
	return out
}

// withDocs fills in the problem's docs link from the table when it has none.
func withDocs(p Problem) Problem {
	if p.Docs == "" {
		p.Docs = DocsURL(DocsSlug(p.Kind, p.Tool))
	}
	return p
}
