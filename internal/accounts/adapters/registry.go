package adapters

import (
	"context"
	"fmt"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
)

// All returns one adapter per tool, in display order.
func All(d Deps) []Adapter {
	return []Adapter{
		newClaude(d), newGit(d), newGitHub(d), newVercel(d),
		newFirebase(d), newSupabase(d), newCloudflare(d), newConvex(d),
	}
}

// For returns the adapter for one tool.
func For(d Deps, t accounts.Tool) (Adapter, bool) {
	for _, a := range All(d) {
		if a.Tool() == t {
			return a, true
		}
	}
	return nil, false
}

// Handlers returns the undo handlers for every effect kind an adapter can
// record, beyond the engine's own file and folder kinds. Whoever builds an
// accounts.Engine adds them (package service does, for the app and the
// command line), so an undo or a recovery never meets a kind it cannot put
// back. Git and GitHub record only file and folder effects.
func Handlers(d Deps) map[accounts.EffectKind]accounts.EffectHandler {
	out := map[accounts.EffectKind]accounts.EffectHandler{}
	for k, h := range CloudflareHandlers(d) {
		out[k] = h
	}
	return out
}

// EffectKinds lists every custom effect kind the adapters record.
func EffectKinds() []accounts.EffectKind {
	return []accounts.EffectKind{EffectCloudflareBinding}
}

// Supports is what Devpit can do with a tool when it is installed and new
// enough: the static answer for help text and documentation, which never
// runs anything. Adapter.Capabilities is the real answer on this PC, and may
// say less (not installed, too old) with the reason.
func Supports(t accounts.Tool) Caps {
	full := Caps{FolderRules: true, Everywhere: true, JustOnce: true, AddAccount: true}
	switch t {
	case accounts.ToolGit:
		return Caps{FolderRules: true, Everywhere: true, AddAccount: true, Why: gitNoOnce}
	case accounts.ToolCloudflare:
		return Caps{FolderRules: true, JustOnce: true, AddAccount: true, Beta: true, Why: cloudflareEverywhereWhy}
	case accounts.ToolConvex:
		return Caps{ShowOnly: true, Why: convexWhy}
	case accounts.ToolClaude, accounts.ToolGitHub, accounts.ToolVercel, accounts.ToolFirebase, accounts.ToolSupabase:
		return full
	}
	return Caps{Why: "unknown tool"}
}

// LaunchFor is what the shim applies for acct: it builds no adapter and runs
// nothing, so a shimmed call costs no more than this switch. Call it only
// for a tool Devpit manages (Store.Manages): then Devpit's answer wins, and
// the default account clears the tool's Managed variables (a
// CLAUDE_CONFIG_DIR left in the terminal by something else) and changes
// nothing more.
func LaunchFor(acct accounts.Account) (Launch, error) {
	switch acct.Tool {
	case accounts.ToolClaude:
		return claudeLaunch(acct)
	case accounts.ToolGit:
		return gitLaunch(acct)
	case accounts.ToolGitHub:
		return githubLaunch(acct)
	case accounts.ToolVercel:
		return vercelLaunch(acct)
	case accounts.ToolFirebase:
		return firebaseLaunch(acct)
	case accounts.ToolSupabase:
		return supabaseLaunch(acct)
	case accounts.ToolCloudflare:
		return cloudflareLaunch(acct)
	case accounts.ToolConvex:
		return convexLaunch(acct)
	}
	return Launch{}, fmt.Errorf("unknown tool %q", acct.Tool)
}

// EnvOverridesFor lists the variables that decide or override t's account,
// without building an adapter.
func EnvOverridesFor(t accounts.Tool) []EnvVar {
	switch t {
	case accounts.ToolClaude:
		return claudeEnv()
	case accounts.ToolGit:
		return gitEnv()
	case accounts.ToolGitHub:
		return githubEnv()
	case accounts.ToolVercel:
		return vercelEnv()
	case accounts.ToolFirebase:
		return firebaseEnv()
	case accounts.ToolSupabase:
		return supabaseEnv()
	case accounts.ToolCloudflare:
		return cloudflareEnv()
	case accounts.ToolConvex:
		return convexEnv()
	}
	return nil
}

// Apply runs a previewed change from start to finish and reports each step:
// the journal records the intent, the adapter makes its side effects (each
// recorded before it happens), the store is saved, and the final event
// carries the journal entry id for undo. A failure rolls back whatever was
// done. There is no way to apply a change without a Preview.
func Apply(ctx context.Context, eng *accounts.Engine, a Adapter, p accounts.Preview) <-chan accounts.Event {
	return Stream("Saving the change", func(emit func(accounts.Event)) error {
		if !a.Capabilities(ctx).Supports(p.Change.Scope.Kind) {
			return NotSupported(a.Tool(), "this kind of change")
		}
		emit(accounts.NewEvent("Recording the change so it can be undone", accounts.StepRunning, ""))
		txn, err := eng.BeginPreview(p)
		if err != nil {
			return err
		}
		emit(accounts.NewEvent("Recording the change so it can be undone", accounts.StepDone, ""))
		if err = a.Apply(ctx, txn, p, emit); err != nil {
			if rerr := txn.Rollback(); rerr != nil {
				return fmt.Errorf("%w; putting things back also failed: %w", err, rerr)
			}
			return err
		}
		emit(accounts.NewEvent("Saving the rule", accounts.StepRunning, ""))
		entry, err := txn.Commit()
		if err != nil {
			return err
		}
		ev := accounts.NewEvent("Saving the rule", accounts.StepDone, entry.Summary)
		ev.Final = true
		ev.EntryID = entry.ID
		emit(ev)
		return nil
	})
}
