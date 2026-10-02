package accounts

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/accounts/demo"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The acceptance criterion of the release: a person can add a second
// Claude Code account, name it, and use it in one folder without typing a
// single command. This drives the whole path with the mouse and Enter, from
// the Accounts page to the row on it that says the new account is used
// here.
func TestAddASecondClaudeAccountAndUseItInOneFolder(t *testing.T) {
	svc := demo.New()
	h := newHarness(t, svc)

	// The page: click the Claude Code row (it is selected; a click on the
	// selected row opens it).
	h.mustSee("Claude Code", "work (you@work.example)").clickText("Claude Code")
	if _, ok := h.top().(toolScreen); !ok {
		t.Fatalf("clicking the selected row did not open the tool page:\n%s", h.view())
	}

	// The tool page: click "Sign in with another account…".
	h.clickText("Sign in with another account")
	h.mustSee("Devpit hands this window to Claude Code's own sign-in", "claude auth login")

	// The sign-in: Enter hands the terminal over; the demo signs in as
	// you@side.example and comes back.
	h.keys("enter")
	h.mustSee("Signed in as you@side.example", "What should Devpit call this account?", "side")
	if svc.Called("signin claude new-") != 1 {
		t.Fatalf("the sign-in ran under a placeholder name %d times, want 1: %v", svc.Called("signin claude new-"), svc.Calls)
	}

	// Name it: the suggestion is good, Enter saves it.
	h.keys("enter")
	if svc.Called("save claude side") != 1 {
		t.Fatalf("the account was not saved as side: %v", svc.Calls)
	}
	// A new Claude Code account is offered its setup first; not now.
	h.mustSee("Bring your Claude Code setup to side?").clickText("Not now")

	// Where: "This folder" is picked already.
	h.mustSee("Where should side be used?", "This folder").keys("enter")

	// The preview, in the engine's words, and its No-first card: click Yes.
	h.mustSee("In "+demo.Folder+" and every folder inside it, Claude Code will use side (you@side.example).", "[ No ]")
	h.clickText("[ Yes ]")
	h.mustSee("Claude Code uses side in "+demo.Folder, "[v] verify", "[u] undo")

	// The rule is there, and the store says so.
	acct, ok := svc.Store.Account(accounts.ToolClaude, "side")
	if !ok || acct.Email != "you@side.example" {
		t.Fatalf("the account was not added: %+v", acct)
	}
	r, ok := svc.Store.Rule(demo.Folder)
	if !ok || r.Accounts[accounts.ToolClaude] != "side" {
		t.Fatalf("no rule for side on %s: %+v", demo.Folder, svc.Store.Rules)
	}

	// Back to the page: the row says side, flagged as just changed.
	h.keys("enter")
	if _, ok := h.top().(Model); !ok {
		t.Fatalf("Enter on the done card did not go back to the page: %T", h.top())
	}
	h.mustSee("✓ side", "updated")
}

// "Everywhere" lists the folder rules that keep winning.
func TestEverywhereListsTheFolderRulesThatStillWin(t *testing.T) {
	h := newHarness(t, demo.New()).keys("enter")
	h.clickText("Use another account everywhere")
	h.keys("up", "enter") // default, everywhere
	h.mustSee("Everywhere", "Wherever no folder rule says otherwise")
	if _, ok := h.top().(flow); !ok {
		t.Fatalf("not in the flow: %T", h.top())
	}
	h = newHarness(t, demo.New()).keys("enter")
	h.clickText("Use another account everywhere").keys("enter", "enter")
	h.mustSee("Everywhere, Claude Code will use work (you@work.example).",
		"1 folder keeps its own rule and will not change:", `C:\Projects   work (you@work.example)`)
}

// Undo from the done card puts the store back.
func TestUndoFromTheDoneCard(t *testing.T) {
	svc := demo.New()
	before := accounts.StoreHash(svc.Store)
	h := newHarness(t, svc).keys("enter", "enter", "up", "enter", "enter", "y")
	if accounts.StoreHash(svc.Store) == before {
		t.Fatal("the change was not made")
	}
	h.keys("u").mustSee("Undo this change:", "Claude Code uses default in "+demo.Folder)
	// The card starts on No: Enter alone changes nothing.
	h.keys("enter")
	if accounts.StoreHash(svc.Store) == before {
		t.Fatal("undo ran without a yes")
	}
	h.keys("u", "y").mustSee("Undone: Claude Code uses default")
	if accounts.StoreHash(svc.Store) != before {
		t.Fatal("undo did not put the store back")
	}
	h.keys("enter")
	if _, ok := h.top().(Model); !ok {
		t.Fatalf("Enter after undo did not go back to the page: %T", h.top())
	}
}

// Verify checks the account active here, shows a mismatch with a one-key
// fix, and asks before a risky check of idle accounts, starting on No.
func TestVerifyMismatchAndTheRiskyCheckPrompt(t *testing.T) {
	svc := demo.New()
	svc.Identities = map[string]accounts.Identity{"claude/work": accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Email: "someone-else@work.example"})}
	h := newHarness(t, svc).keys("v")
	h.mustSee("mismatch", "Devpit expects work to be you@work.example", "[f] fix this")
	if svc.Called("verify all=false risky=false") != 1 {
		t.Fatalf("verify ran %v", svc.Calls)
	}
	h.keys("a").mustSee("Also ask Claude Code about its idle accounts?", "can sign an idle", "[ No ]")
	// Enter on the card is No: the idle Claude Code account is not asked.
	h.keys("enter")
	if svc.Called("verify all=true risky=false") != 1 || svc.Called("verify all=true risky=true") != 0 {
		t.Fatalf("the risky check ran without a yes: %v", svc.Calls)
	}
	h.mustSee("Claude Code accounts not checked: default")

	// Yes, asked again from a fresh verify, runs it.
	h2 := newHarness(t, svc).keys("v", "a", "y")
	if svc.Called("verify all=true risky=true") != 1 {
		t.Fatalf("yes did not run the risky check: %v", svc.Calls)
	}
	_ = h2

	// f on the mismatch opens what fixes it: the tool page.
	h.keys("up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "f")
	if _, ok := h.top().(toolScreen); !ok {
		t.Fatalf("f did not open the fix: %T\n%s", h.top(), h.view())
	}
}

// The page marks a row the latest verify found expired, and the tool page
// leads with "Sign in again".
func TestExpiredLoginLeadsWithSignInAgain(t *testing.T) {
	h := newHarness(t, expiredSvc()).keys("v", "enter")
	h.mustSee("expired", "sign in again")
	h.keys("enter").mustSee("expired, sign in again")
	h.keys("enter").mustSee("Sign work in again", "claude auth login")
	h.keys("enter").mustSee("sign-in finished")
	if expiredSvc().Called("sign in again") != 0 {
		t.Fatal("a fresh demo has calls")
	}
}

// A Careful row is asked about twice before anything that may hold a secret
// is copied, and No at either question leaves it on Skip.
func TestCarefulRowAsksTwice(t *testing.T) {
	svc := demo.New()
	h := carefulRow(t, svc)
	h.mustSee("Copy MCP servers?", "API keys")
	h.keys("n").mustSee("[Skip]")
	h.keys("left").keys("y").mustSee("Are you sure?")
	h.keys("n")
	h.keys("left", "y", "y")
	h.mustSee("MCP servers", "[Copy]")
	h.keys("enter")
	if svc.Called("plan claude secrets=true") != 1 {
		t.Fatalf("secrets were not allowed after two yeses: %v", svc.Calls)
	}

	// Without the two yeses, secrets are never allowed.
	svc2 := demo.New()
	h2 := claudeSetup(t, svc2).keys("down", "down", "down", "enter", "enter")
	_ = h2
	if svc2.Called("plan claude secrets=false") != 1 || svc2.Called("plan claude secrets=true") != 0 {
		t.Fatalf("the plan allowed secrets without asking: %v", svc2.Calls)
	}
}

// When Windows refuses links, the setup offers copies instead and shows the
// new preview before anything changes.
func TestLinkRefusedOffersCopyInstead(t *testing.T) {
	svc := demo.New()
	svc.LinkRefused = true
	h := claudeSetup(t, svc).keys("enter", "y")
	h.mustSee("Windows will not make links here", "copy instead")
	h.keys("c")
	h.mustSee("Copy skills into work")
	if svc.Called("plan claude secrets=false copy=true") != 1 {
		t.Fatalf("the plan was not rebuilt with copies: %v", svc.Calls)
	}
}

// The first open on a PC with claude-acc offers to use its accounts; using
// them is a preview and one change, then the removal steps.
func TestImportFromTheFirstOpenCard(t *testing.T) {
	svc := demo.New()
	svc.Found = demo.SampleFound()
	h := newHarness(t, svc)
	h.mustSee("Found accounts already on this PC", "Found 1 Claude account and 6 folder links from claude-acc.")
	h.clickText("Use them in Devpit")
	h.mustSee("Add the Claude Code account client", "Bring these into Devpit?")
	h.keys("y").mustSee("Imported from claude-acc")
	h.keys("enter").mustSee("To retire claude-acc safely", "1. Check that Devpit picks the same accounts")
	h.keys("enter")
	if _, ok := svc.Store.Account(accounts.ToolClaude, "client"); !ok || len(svc.Store.RulesUsing(accounts.ToolClaude, "client")) != 5 {
		t.Fatalf("the import did not add the account and its rules: %+v", svc.Store)
	}
	if _, ok := h.top().(Model); !ok {
		t.Fatalf("not back on the page: %T", h.top())
	}
}

// Ignore is remembered, and i brings the card back.
func TestIgnoreImportIsRememberedAndIReopensIt(t *testing.T) {
	svc := demo.New()
	svc.Found = demo.SampleFound()
	h := newHarness(t, svc).clickText("Ignore")
	if svc.Called("dismiss import") != 1 {
		t.Fatalf("ignore was not remembered: %v", svc.Calls)
	}
	h.mustSee("Press i to bring them into Devpit")
	h2 := newHarness(t, svc)
	if _, ok := h2.top().(Model); !ok {
		t.Fatalf("the ignored card opened again: %T", h2.top())
	}
	h2.keys("i").mustSee("Found accounts already on this PC")
}

// Esc after signing in, before naming, asks whether to throw the sign-in
// away, and Yes removes what it left.
func TestEscBeforeNamingAsksToThrowTheSignInAway(t *testing.T) {
	svc := demo.New()
	h := newHarness(t, svc).keys("enter")
	h.clickText("Sign in with another account").keys("enter")
	if !uictx.Busy(h.top()) {
		t.Fatal("a sign-in waiting for its name must own Esc")
	}
	h.keys("esc").mustSee("Throw away this sign-in?", "[ No ]")
	h.keys("esc").mustSee("What should Devpit call this account?")
	h.keys("esc", "y")
	if svc.Called("discard claude new-") != 1 {
		t.Fatalf("the sign-in was not thrown away: %v", svc.Calls)
	}
	if _, ok := h.top().(toolScreen); !ok {
		t.Fatalf("not back on the tool page: %T", h.top())
	}
}

// Ctrl+C while a sign-in waits for its name throws it away too.
func TestCtrlCBeforeNamingThrowsTheSignInAway(t *testing.T) {
	svc := demo.New()
	h := newHarness(t, svc).keys("enter")
	h.clickText("Sign in with another account").keys("enter")
	uictx.Stop(h.top())
	deadline := time.Now().Add(2 * time.Second)
	for svc.Called("discard claude new-") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if svc.Called("discard claude new-") != 1 {
		t.Fatalf("Ctrl+C left the sign-in behind: %v", svc.Calls)
	}
	if uictx.Busy(h.top()) {
		t.Fatal("still busy after Ctrl+C, so the app would wait")
	}
}

// Esc cannot leave a change half applied.
func TestEscDuringApplyIsIgnored(t *testing.T) {
	h := newHarness(t, demo.New()).keys("enter", "enter", "up", "enter", "enter")
	f := h.top().(flow)
	f.stage = stApplying
	f.live = liveRun{id: 42}
	h.stack[len(h.stack)-1] = f
	if !uictx.Busy(h.top()) {
		t.Fatal("applying must be busy")
	}
	h.keys("esc")
	if got, ok := h.top().(flow); !ok || got.stage != stApplying {
		t.Fatalf("Esc left the change: %T", h.top())
	}
	if f.TerminalProgress() == nil {
		t.Fatal("no taskbar progress while applying")
	}
}

// A change that fails midway says so and that everything was put back; the
// store is unchanged.
func TestApplyThatFailsMidwayIsPutBack(t *testing.T) {
	svc := demo.New()
	svc.FailApplyAt = 2
	before := accounts.StoreHash(svc.Store)
	h := newHarness(t, svc).keys("enter", "enter", "up", "enter", "enter", "y")
	h.mustSee("The change did not finish", "Everything", "put back")
	if accounts.StoreHash(svc.Store) != before {
		t.Fatal("a failed change changed the store")
	}
}

// A second Devpit window holding the lock makes this one read-only: the
// preview says why and there is nothing to say yes to.
func TestReadOnlyWindowCannotApply(t *testing.T) {
	svc := demo.New()
	svc.Locked = true
	h := newHarness(t, svc).mustSee("can look but not change anything")
	h.keys("enter", "enter", "up", "enter", "enter").mustSee("can look but not change anything")
	h.keys("y")
	if svc.Called("apply") != 0 {
		t.Fatalf("a read-only window applied a change: %v", svc.Calls)
	}
}

// Fix old rules removes a rule on a drive that is not connected, after a
// preview, and never by itself.
func TestFixOldRulesRemovesAfterAPreview(t *testing.T) {
	h := staleBrowse(t)
	svc := h.svc
	h.mustSee("drive not connected").keys("x")
	if _, ok := svc.Store.Rule(`E:\usb\old-client`); !ok {
		t.Fatal("the old rule went away on its own")
	}
	h.keys("d").mustSee(`E:\usb\old-client will no longer have its own Vercel rule.`)
	h.keys("y", "enter")
	if _, ok := svc.Store.Rule(`E:\usb\old-client`); ok {
		t.Fatal("the old rule is still there")
	}
}

// The page works with the mouse: the wheel moves the selection, a click
// selects, a click on the selected row opens it.
func TestPageMouse(t *testing.T) {
	h := newHarness(t, demo.New())
	h.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown, Y: 6})
	m := h.top().(Model)
	if r, _ := m.sized(h.ctx).Selected(); r.ID != string(accounts.ToolGit) {
		t.Fatalf("the wheel did not move to Git: %s", r.ID)
	}
	h.clickText("Vercel")
	if _, ok := h.top().(Model); !ok {
		t.Fatal("a click on another row opened it instead of selecting it")
	}
	h.clickText("Vercel")
	if ts, ok := h.top().(toolScreen); !ok || ts.tool != accounts.ToolVercel {
		t.Fatalf("a click on the selected row did not open it: %T", h.top())
	}
}

// Leaving Accounts lets go of the lock.
func TestLeavingReleasesTheLock(t *testing.T) {
	svc := demo.New()
	h := newHarness(t, svc)
	if !svc.Held() {
		t.Fatal("the page did not take the lock")
	}
	uictx.Stop(h.top())
	if svc.Held() {
		t.Fatal("leaving did not release the lock")
	}
}

// A tool that is not installed opens without errors and says so.
func TestNotInstalledToolOpensQuietly(t *testing.T) {
	h := toolPage(t, demo.New(), 4)
	h.mustSee("Firebase is not installed")
	if strings.Contains(h.view(), "Something went wrong") {
		t.Fatal("a missing tool is an error")
	}
}

// No sentence about a change is written by the screens: the preview is the
// engine's own.
func TestPreviewIsTheEnginesWords(t *testing.T) {
	svc := demo.New()
	h := newHarness(t, svc).keys("enter", "enter", "up", "enter", "enter")
	p, err := svc.Plan(t.Context(), accounts.Change{Tool: accounts.ToolClaude, Account: "default", Scope: accounts.FolderScope(demo.Folder)})
	if err != nil {
		t.Fatal(err)
	}
	v := strings.Join(strings.Fields(h.view()), " ")
	for _, s := range p.Sentences {
		if !strings.Contains(v, strings.Join(strings.Fields(s), " ")) {
			t.Errorf("the preview does not show the engine's sentence %q:\n%s", s, h.view())
		}
	}
}
