// Package adapters is where each developer tool answers the same questions
// for the Accounts engine: is it installed, what can Devpit do with it, who
// is signed in, how to sign in another account, what a change will write,
// and what the launcher must set for one process to use an account.
//
// One file per tool: claude.go, git.go, github.go, vercel.go, firebase.go,
// supabase.go, cloudflare.go, convex.go. Shared helpers (the command runner,
// version parsing, JSON and text helpers, the fake runner for tests) live in
// common.go, runner.go and fake.go.
//
// Rules every adapter keeps, pinned by tests:
//
//   - every external command goes through [Runner], with a timeout, and the
//     real tool is found outside Devpit's shim folder;
//   - tool output reaches an event, a log line, an error or a returned
//     struct only through accounts.Scrub; [accounts.Identity] cannot hold a
//     token at all;
//   - a command that would print a token (firebase login:list --json, gh
//     --show-token) or carry one on its command line is refused by the
//     runner before it starts;
//   - no adapter reads a tool's login file (.credentials.json, hosts.yml,
//     access-token…).
//
// No Bubble Tea here: long operations return a channel of accounts.Event.
package adapters

import (
	"context"
	"io"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// Adapter is one tool. Methods that run the tool take a context; every
// command they run also has its own timeout.
type Adapter interface {
	// Tool is which tool this is.
	Tool() accounts.Tool

	// Installed finds the tool (outside the shim folder) and its version.
	// It never fails: a missing tool is Install{Found: false}.
	Installed(ctx context.Context) Install

	// Capabilities says what Devpit can do with this tool on this PC, with
	// a plain-words reason for anything it cannot. It may probe the tool at
	// run time (a missing subcommand, a missing feature) rather than trust a
	// version number; adapters cache what they learn.
	Capabilities(ctx context.Context) Caps

	// Accounts lists the accounts Devpit can use for this tool: "default"
	// first, then the store's, then any the tool itself knows of that the
	// store does not have yet (ImportedFrom "detected").
	Accounts(ctx context.Context, s *accounts.Store) ([]accounts.Account, error)

	// Cached is who the account is as Devpit recorded it at sign-in,
	// import or the last Verify (accounts.toml), with State
	// StateRecorded. It never runs the tool: screens fill their rows with
	// it, and [Overview] uses nothing else.
	Cached(s *accounts.Store, acct accounts.Account) accounts.Identity

	// WhoAmI is the live check: it runs the tool with the account applied
	// and asks who is signed in. It never returns or logs a token. It is
	// for an explicit request only (Verify, Sign in again): never on a
	// timer, never from the shim, and never for an account other than the
	// one active in the current folder without the person's say-so after
	// reading LiveCheckRisk.
	WhoAmI(ctx context.Context, acct accounts.Account) (accounts.Identity, error)

	// LiveCheckRisk says whether WhoAmI can harm an idle account, and why,
	// in a sentence a screen shows before running it.
	LiveCheckRisk() (risky bool, reason string)

	// EnvOverrides lists the environment variables that decide or override
	// this tool's account, so Verify can flag one left in a terminal (see
	// [CheckEnv]).
	EnvOverrides() []EnvVar

	// Login runs the tool's own sign-in for a new account called req.Name.
	// The channel's last event (Final) carries the Account to save on
	// success; nothing is saved by Login itself, and a sign-in that does
	// not finish leaves nothing behind.
	Login(ctx context.Context, req LoginRequest) (<-chan accounts.Event, error)

	// Plan builds the preview for a change: the plain-words sentences from
	// accounts.BuildPreview plus this tool's own file edits and warnings.
	Plan(in accounts.PreviewInput) (accounts.Preview, error)

	// Apply makes this tool's side effects for a previewed change, each one
	// recorded in txn before it happens. The store itself is saved by the
	// caller ([Apply] in this package) after Apply returns nil. emit
	// reports live steps.
	Apply(ctx context.Context, txn *accounts.Txn, p accounts.Preview, emit func(accounts.Event)) error

	// Launch is what the launcher applies to one process for acct: the
	// environment to set and to clear, and the arguments to put before the
	// person's own. It is only asked when Devpit manages the tool
	// (Store.Manages); then Devpit's answer wins, so the default account
	// clears the Managed variables (a stray CLAUDE_CONFIG_DIR) and starts
	// the tool otherwise untouched. A tool Devpit does not manage is never
	// touched at all.
	Launch(acct accounts.Account) (Launch, error)
}

// EnvVar is an environment variable that decides or overrides a tool's
// account.
type EnvVar struct {
	Name string
	// Managed: Devpit sets it for a named account and removes it for the
	// default account whenever it manages the tool. A variable that is not
	// Managed is only reported, never cleared.
	Managed bool
	// Effect says, in plain words, what it does when set.
	Effect string
}

// Install is what Installed found.
type Install struct {
	Found bool
	// Path is the program that runs (after skipping the shim folder).
	Path string
	// Version is the version the tool reports, or "".
	Version string
	// Note is a sentence for a person ("used through npx; the version
	// depends on the project").
	Note string
}

// Caps is what Devpit can do with a tool.
type Caps struct {
	// FolderRules: a folder rule can pick this tool's account.
	FolderRules bool
	// Everywhere: the account used everywhere else can be changed.
	Everywhere bool
	// JustOnce: one command can run with another account.
	JustOnce bool
	// AddAccount: Devpit can run the tool's sign-in for a new account.
	AddAccount bool
	// ShowOnly: Devpit shows and verifies the account but never switches
	// it (Convex).
	ShowOnly bool
	// Beta marks a tool whose switching relies on a feature the tool itself
	// calls experimental (Wrangler profiles).
	Beta bool
	// Why says, in plain words, why something above is false.
	Why string
}

// Supports reports whether a change in scope is allowed.
func (c Caps) Supports(k accounts.ScopeKind) bool {
	switch k {
	case accounts.ScopeFolder:
		return c.FolderRules
	case accounts.ScopeEverywhere:
		return c.Everywhere
	case accounts.ScopeOnce:
		return c.JustOnce
	}
	return false
}

// Launch is what the launcher applies to one child process.
type Launch struct {
	// Env is KEY=VALUE pairs set for the child.
	Env []string
	// Unset is keys removed from the child's environment (Supabase:
	// SUPABASE_ACCESS_TOKEN, which would win over the account folder).
	Unset []string
	// Args go before the person's own arguments (Vercel: --global-config
	// <dir>; Firebase: --account <email>).
	Args []string
}

// IsZero reports whether the launch changes nothing.
func (l Launch) IsZero() bool {
	return len(l.Env) == 0 && len(l.Unset) == 0 && len(l.Args) == 0
}

// LoginRequest is what Login needs.
type LoginRequest struct {
	// Name is the new account's name. It must pass Store.CheckNewName.
	Name string
	// Store is the current store, for the name check and to warn when the
	// same email is already signed in under another name.
	Store *accounts.Store
	// DefaultEmail is who the default account is, when known, for the same
	// warning.
	DefaultEmail string
	// Email pre-fills the sign-in page, where the tool supports it. For Git
	// it is the identity's address (Git has no sign-in).
	Email string
	// DisplayName is Git's user.name for a new identity; "" uses the name
	// the person's global Git config already has. Other tools ignore it.
	DisplayName string
	// Console signs Claude Code in with an Anthropic Console account
	// (API billing) instead of a subscription.
	Console bool
	// Stdin, Stdout and Stderr, when set, give the tool's own sign-in the
	// terminal (the screen suspends itself first). When nil the output is
	// captured, scrubbed and sent as events.
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}
