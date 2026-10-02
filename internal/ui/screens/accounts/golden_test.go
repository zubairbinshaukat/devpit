package accounts

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/claudeshare"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/screens/accounts/demo"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// goldenState is one named state of an Accounts screen, reached by driving
// the screens with keys from the page, the way a person gets there. The
// names are what the documentation screenshots are keyed by.
type goldenState struct {
	name  string
	build func(t *testing.T) uictx.Screen
}

// open is the page over svc.
func open(t *testing.T, svc *demo.Service) *harness { return newHarness(t, svc) }

// toolPage opens the page of the tool in row i (0 is Claude Code).
func toolPage(t *testing.T, svc *demo.Service, row int) *harness {
	h := open(t, svc)
	for range row {
		h.keys("down")
	}
	return h.keys("enter")
}

// expiredSvc is the demo with the Claude Code login here expired, as a
// verify found it.
func expiredSvc() *demo.Service {
	s := demo.New()
	s.Identities = map[string]accounts.Identity{"claude/work": accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateExpired, Note: "The sign-in has expired. Sign in again."})}
	return s
}

func goldenStates() []goldenState {
	top := func(h *harness) uictx.Screen { return h.top() }
	return []goldenState{
		// The page.
		{"page", func(t *testing.T) uictx.Screen { return top(open(t, demo.New())) }},
		{"page_opening", func(t *testing.T) uictx.Screen { return NewWith(testOptions(demo.New(), new([]string))) }},
		{"page_empty", func(t *testing.T) uictx.Screen { return top(open(t, demo.Empty())) }},
		{"page_notices", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.ShimRepairs = []accounts.Event{accounts.NewEvent("Added Devpit's shim for Claude Code", accounts.StepDone, "")}
			s.Recovered = []accounts.Recovered{{Entry: accounts.Entry{Summary: "Claude Code uses work in C:\\Projects"}, Outcome: "finished"}}
			s.Warning = "Your accounts file could not be read, so it was moved to accounts.toml.broken-1759400000 and Devpit started with no rules."
			return top(open(t, s))
		}},
		{"page_read_only", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.Locked = true
			return top(open(t, s))
		}},
		{"page_home_folder", func(t *testing.T) uictx.Screen {
			s := demo.New()
			o := testOptions(s, new([]string))
			o.Folder = demo.Home
			h := &harness{t: t, svc: s, ctx: testCtx(80, 24, icons.TierUnicode)}
			p := NewWith(o)
			h.stack = []uictx.Screen{p}
			h.settle(p.Init())
			return h.top()
		}},
		{"page_folder_gone", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.Gone = map[string]bool{strings.ToLower(demo.Folder): true}
			return top(open(t, s))
		}},
		{"page_problem", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.Problems = map[accounts.Tool][]service.Problem{accounts.ToolClaude: {{
				Kind: string(accounts.ProblemEnvOverride), Tool: "claude",
				Message: "CLAUDE_CONFIG_DIR is set in this terminal by something other than Devpit (to C:\\Users\\you\\.claude-switch\\accounts\\client). Claude Code started through Devpit uses Devpit's rule instead.",
				Fix:     "If you did not mean to set it, remove it here with `Remove-Item Env:CLAUDE_CONFIG_DIR` (PowerShell), and from wherever sets it, then open a new terminal.",
			}}}
			return top(open(t, s))
		}},
		{"page_stale_rule", func(t *testing.T) uictx.Screen {
			s := demo.New()
			if err := s.Store.SetRule(`E:\usb\old-client`, accounts.ToolVercel, "client"); err != nil {
				t.Fatal(err)
			}
			s.Stale = map[string]accounts.StaleKind{`e:\usb\old-client`: accounts.StaleDriveNotConnected}
			return top(open(t, s).keys("down", "down", "down"))
		}},
		{"page_after_verify", func(t *testing.T) uictx.Screen { return top(open(t, expiredSvc()).keys("v").keys("enter")) }},
		{"page_import_hint", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.Found = demo.SampleFound()
			_ = s.DismissImport(s.Found)
			return top(open(t, s))
		}},
		{"page_long_paths", func(t *testing.T) uictx.Screen {
			s := demo.New()
			long := `C:\Users\you\Documents\Clients\Northwind Traders\2026 engagement\web-platform-monorepo`
			if err := s.Store.SetRule(long, accounts.ToolClaude, "work"); err != nil {
				t.Fatal(err)
			}
			if err := s.Store.AddAccount(accounts.Account{Tool: accounts.ToolVercel, Name: "northwind", Email: "firstname.lastname.contractor@northwind-traders.example"}); err != nil {
				t.Fatal(err)
			}
			if err := s.Store.SetRule(long, accounts.ToolVercel, "northwind"); err != nil {
				t.Fatal(err)
			}
			o := testOptions(s, new([]string))
			o.Folder = long + `\packages\checkout`
			h := &harness{t: t, svc: s, ctx: testCtx(80, 24, icons.TierUnicode)}
			p := NewWith(o)
			h.stack = []uictx.Screen{p}
			h.settle(p.Init())
			return h.top()
		}},
		{"page_change_folder", func(t *testing.T) uictx.Screen { return top(open(t, demo.New()).keys("f")) }},

		// Tool pages.
		{"tool_claude", func(t *testing.T) uictx.Screen { return top(toolPage(t, demo.New(), 0)) }},
		{"tool_claude_expired", func(t *testing.T) uictx.Screen {
			return top(open(t, expiredSvc()).keys("v").keys("enter").keys("enter"))
		}},
		{"tool_github", func(t *testing.T) uictx.Screen { return top(toolPage(t, demo.New(), 2)) }},
		{"tool_cloudflare", func(t *testing.T) uictx.Screen { return top(toolPage(t, demo.New(), 6)) }},
		{"tool_convex", func(t *testing.T) uictx.Screen { return top(toolPage(t, demo.New(), 7)) }},
		{"tool_not_installed", func(t *testing.T) uictx.Screen { return top(toolPage(t, demo.New(), 4)) }},
		{"tool_nested_rules", func(t *testing.T) uictx.Screen {
			s := demo.New()
			if err := s.Store.AddAccount(accounts.Account{Tool: accounts.ToolClaude, Name: "client", Email: "you@client.example", Dir: `C:\x`}); err != nil {
				t.Fatal(err)
			}
			if err := s.Store.SetRule(demo.Folder, accounts.ToolClaude, "client"); err != nil {
				t.Fatal(err)
			}
			return top(toolPage(t, s, 0))
		}},

		// The change flow.
		{"picker", func(t *testing.T) uictx.Screen { return top(toolPage(t, demo.New(), 0).keys("enter")) }},
		{"picker_filtered", func(t *testing.T) uictx.Screen { return top(toolPage(t, demo.New(), 0).keys("enter").typed("wo")) }},
		{"signin_intro", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).keys("down", "down", "down", "enter"))
		}},
		{"signin_name", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).keys("down", "down", "down", "enter", "enter"))
		}},
		{"signin_name_taken", func(t *testing.T) uictx.Screen {
			h := toolPage(t, demo.New(), 0).keys("down", "down", "down", "enter", "enter")
			for range 8 {
				h.keys("backspace")
			}
			return top(h.typed("work"))
		}},
		{"signin_same_login", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.SignInEmail = "you@work.example"
			s.SignInWarnings = []accounts.Event{accounts.NewEvent("Same login twice", accounts.StepWarning,
				"new is signed in as you@work.example, like work. Two folders signed in to the same account can sign each other out when the login refreshes.")}
			return top(toolPage(t, s, 0).keys("down", "down", "down", "enter", "enter"))
		}},
		{"signin_cancelled", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.SignInEmail = ""
			return top(toolPage(t, s, 0).keys("down", "down", "down", "enter", "enter"))
		}},
		{"signin_discard", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).keys("down", "down", "down", "enter", "enter", "esc"))
		}},
		{"signin_cloudflare_name_first", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 6).keys("down", "enter").typed("acme"))
		}},
		{"scope", func(t *testing.T) uictx.Screen { return top(toolPage(t, demo.New(), 0).keys("enter", "enter")) }},
		{"scope_home_folder", func(t *testing.T) uictx.Screen {
			s := demo.New()
			o := testOptions(s, new([]string))
			o.Folder = demo.Home
			h := &harness{t: t, svc: s, ctx: testCtx(80, 24, icons.TierUnicode)}
			p := NewWith(o)
			h.stack = []uictx.Screen{p}
			h.settle(p.Init())
			return top(h.keys("enter", "enter", "enter"))
		}},
		{"scope_another_folder", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).keys("enter", "enter", "down", "down", "down", "enter"))
		}},
		{"preview_folder", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).keys("enter", "up", "enter", "enter"))
		}},
		{"preview_everywhere_kept", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).keys("down", "enter", "enter", "enter"))
		}},
		{"preview_git", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 1).keys("enter", "down", "enter", "enter"))
		}},
		{"preview_no_change", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).keys("enter", "up", "enter", "down", "enter"))
		}},
		{"preview_read_only", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.Locked = true
			return top(toolPage(t, s, 0).keys("enter", "up", "enter", "enter"))
		}},
		{"preview_drive_root_second", func(t *testing.T) uictx.Screen {
			h := toolPage(t, demo.New(), 0).keys("enter", "up", "enter", "down", "down", "down", "enter", "down", "enter")
			return top(h.typed(`D:\`).keys("enter", "y"))
		}},
		{"applying", func(t *testing.T) uictx.Screen {
			h := toolPage(t, demo.New(), 0).keys("enter", "up", "enter", "enter")
			f := h.top().(flow)
			f.stage = stApplying
			f.live = liveRun{id: 99}.add(accounts.NewEvent("Recording the change so it can be undone", accounts.StepDone, ""), true).
				add(accounts.NewEvent("Nothing to change in Claude Code itself", accounts.StepRunning, ""), true)
			return f
		}},
		{"done", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).keys("enter", "up", "enter", "enter", "y"))
		}},
		{"failed_midway", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.FailApplyAt = 2
			return top(toolPage(t, s, 0).keys("enter", "up", "enter", "enter", "y"))
		}},
		{"just_this_once", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).keys("down", "down", "enter", "enter"))
		}},
		{"remove_rule", func(t *testing.T) uictx.Screen {
			h := toolPage(t, demo.New(), 0)
			return top(h.clickText("Forget this folder's choice"))
		}},

		// Verify.
		{"verify", func(t *testing.T) uictx.Screen { return top(open(t, demo.New()).keys("v")) }},
		{"verify_expired", func(t *testing.T) uictx.Screen { return top(open(t, expiredSvc()).keys("v")) }},
		{"verify_mismatch", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.Identities = map[string]accounts.Identity{"claude/work": accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Email: "someone-else@work.example"})}
			return top(open(t, s).keys("v"))
		}},
		{"verify_ask_risky", func(t *testing.T) uictx.Screen { return top(open(t, demo.New()).keys("v", "a")) }},
		{"verify_idle_skipped", func(t *testing.T) uictx.Screen { return top(open(t, demo.New()).keys("v", "a", "n")) }},

		// Undo.
		{"undo_nothing", func(t *testing.T) uictx.Screen { return top(open(t, demo.New()).keys("u")) }},
		{"undo_ask", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).keys("enter", "up", "enter", "enter", "y", "u"))
		}},
		{"undo_done", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).keys("enter", "up", "enter", "enter", "y", "u", "y"))
		}},
		{"undo_changed_by_hand", func(t *testing.T) uictx.Screen {
			s := demo.New()
			h := toolPage(t, s, 0).keys("enter", "up", "enter", "enter", "y")
			s.UndoByHand = `C:\Users\you\.gitconfig`
			return top(h.keys("u"))
		}},

		// Browse folders.
		{"browse", func(t *testing.T) uictx.Screen { return top(open(t, demo.New()).keys("b")) }},
		{"browse_stale", func(t *testing.T) uictx.Screen { return top(staleBrowse(t)) }},
		{"fix_old_rules", func(t *testing.T) uictx.Screen { return top(staleBrowse(t).keys("x")) }},
		{"fix_old_rules_preview", func(t *testing.T) uictx.Screen { return top(staleBrowse(t).keys("x", "d")) }},

		// Git.
		{"git_page", func(t *testing.T) uictx.Screen { return top(toolPage(t, demo.New(), 1)) }},
		{"git_page_ssh_remote", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.Pushes.Via, s.Pushes.SSHKey = "the SSH key", demo.Home+`\.ssh\id_ed25519_devpit_work`
			s.Pushes.Remote = adapters.ParseRemoteURL("git@github.com:you/quiz-slayer.git")
			s.Commits.Repo.IsRepo = false
			s.Commits.Notes = []string{"This folder is not a Git repo yet. The rule applies once it is one (git init or git clone); until then Git would use you@personal.example."}
			return top(toolPage(t, s, 1))
		}},
		{"git_identity_form", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 1).keys("down", "down", "enter").typed("oss").keys("tab").typed("You").keys("tab", "tab"))
		}},
		{"ssh_choose", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 1).keys("down", "down", "down", "down", "enter"))
		}},
		{"ssh_made", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 1).keys("down", "down", "down", "down", "enter", "enter"))
		}},
		{"ssh_key_exists", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.KeyExists = true
			return top(toolPage(t, s, 1).keys("down", "down", "down", "down", "enter", "enter"))
		}},
		{"ssh_preview", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 1).keys("down", "down", "down", "down", "enter", "enter", "enter"))
		}},

		// Claude Code setup.
		{"claude_ask", func(t *testing.T) uictx.Screen { return top(claudeSetup(t, demo.New())) }},
		{"claude_items", func(t *testing.T) uictx.Screen {
			return top(claudeSetup(t, demo.New()).keys("down", "down", "down", "enter"))
		}},
		{"claude_careful_first", func(t *testing.T) uictx.Screen { return top(carefulRow(t, demo.New())) }},
		{"claude_careful_second", func(t *testing.T) uictx.Screen { return top(carefulRow(t, demo.New()).keys("y")) }},
		{"claude_conflict", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.Inventory.Items[0].Conflicts = 2
			return top(claudeSetup(t, s).keys("enter"))
		}},
		{"claude_preview", func(t *testing.T) uictx.Screen { return top(claudeSetup(t, demo.New()).keys("enter")) }},
		{"claude_done", func(t *testing.T) uictx.Screen { return top(claudeSetup(t, demo.New()).keys("enter", "y")) }},
		{"claude_link_refused", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.LinkRefused = true
			return top(claudeSetup(t, s).keys("enter", "y"))
		}},
		{"claude_in_use", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.InUse = true
			return top(claudeSetup(t, s).keys("enter", "y"))
		}},
		{"claude_status", func(t *testing.T) uictx.Screen {
			s := demo.New()
			s.Inventory.Items[0].Entries = []claudeshare.Entry{
				{Name: "review-pr", State: claudeshare.EntryLinked}, {Name: "deploy-notes", State: claudeshare.EntryLinked}, {Name: "my-new-skill", State: claudeshare.EntryTargetOnly},
			}
			s.Inventory.Items[1].Entries = []claudeshare.Entry{{Name: "agents", State: claudeshare.EntryBroken, LinkTarget: demo.Home + `\.claude\agents`}}
			return top(toolPage(t, s, 0).clickText("Bring your Claude Code setup over"))
		}},

		// Accounts already on this PC.
		{"import_card", func(t *testing.T) uictx.Screen { return top(importOpen(t)) }},
		{"import_preview", func(t *testing.T) uictx.Screen { return top(importOpen(t).keys("enter")) }},
		{"import_guide", func(t *testing.T) uictx.Screen { return top(importOpen(t).keys("enter", "y", "enter")) }},

		// Manage accounts and sign in again.
		{"manage", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).clickText("Rename or remove an account"))
		}},
		{"manage_remove", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).clickText("Rename or remove an account").keys("d"))
		}},
		{"manage_rename", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).clickText("Rename or remove an account").keys("r", "backspace", "backspace", "backspace", "backspace").typed("office").keys("enter"))
		}},
		{"sign_in_again", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).clickText("Sign in again"))
		}},
		{"sign_in_again_done", func(t *testing.T) uictx.Screen {
			return top(toolPage(t, demo.New(), 0).clickText("Sign in again").keys("enter"))
		}},
	}
}

// staleBrowse is Browse folders with one rule on a drive that is not
// connected.
func staleBrowse(t *testing.T) *harness {
	s := demo.New()
	if err := s.Store.SetRule(`E:\usb\old-client`, accounts.ToolVercel, "client"); err != nil {
		t.Fatal(err)
	}
	s.Stale = map[string]accounts.StaleKind{`e:\usb\old-client`: accounts.StaleDriveNotConnected}
	return open(t, s).keys("b")
}

// claudeSetup opens the Claude setup question for the work account.
func claudeSetup(t *testing.T, s *demo.Service) *harness {
	return toolPage(t, s, 0).clickText("Bring your Claude Code setup over")
}

// carefulRow turns MCP servers on in the item list, which asks.
func carefulRow(t *testing.T, s *demo.Service) *harness {
	h := claudeSetup(t, s).keys("down", "down", "down", "enter")
	for range 7 {
		h.keys("down")
	}
	return h.keys("left")
}

// importOpen opens the page on a PC with claude-acc.
func importOpen(t *testing.T) *harness {
	s := demo.New()
	s.Found = demo.SampleFound()
	return open(t, s)
}

// TestGoldenFrames pins every state at both supported sizes, on the unicode
// and the ascii tier. Every frame is also checked for lines wider than the
// terminal and for a body taller than the room it has.
func TestGoldenFrames(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {100, 30}}
	seen := map[string]bool{}
	for _, st := range goldenStates() {
		if seen[st.name] {
			t.Fatalf("duplicate state %q", st.name)
		}
		seen[st.name] = true
		t.Run(st.name, func(t *testing.T) {
			scr := st.build(t)
			for _, size := range sizes {
				for _, tier := range []icons.Tier{icons.TierUnicode, icons.TierASCII} {
					ctx := testCtx(size.w, size.h, tier)
					name := fmt.Sprintf("accounts_%s_%dx%d_%s_nocolor", st.name, size.w, size.h, tier)
					checkFits(t, name, scr, ctx)
					requireGolden(t, name, frame(scr, ctx))
				}
			}
		})
	}
}

// TestLightThemeRendersEveryState draws every state on a light background
// with the colour left in, so a style that only works on dark (or a glyph
// that only renders with colour) shows up as a failure here.
func TestLightThemeRendersEveryState(t *testing.T) {
	for _, st := range goldenStates() {
		t.Run(st.name, func(t *testing.T) {
			scr := st.build(t)
			ctx := testCtx(80, 24, icons.TierUnicode)
			ctx.Theme = theme.For(false)
			checkFits(t, st.name+" light", scr, ctx)
			if !strings.Contains(scr.View(ctx), "\x1b[") && st.name != "page_opening" {
				t.Errorf("%s: no colour at all on the light theme", st.name)
			}
		})
	}
}

// checkFits fails for a line wider than the terminal or a body taller than
// the room it is given (the app would cut it off).
func checkFits(t *testing.T, name string, scr uictx.Screen, ctx uictx.Context) {
	t.Helper()
	lines := strings.Split(ansi.Strip(scr.View(ctx)), "\n")
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > ctx.Width {
			t.Errorf("%s: line %d is %d cells wide, the terminal %d:\n%q", name, i, w, ctx.Width, l)
		}
	}
	if len(lines) > ctx.BodyHeight {
		t.Errorf("%s: the body is %d lines, the room %d; the end is cut off:\n%s", name, len(lines), ctx.BodyHeight, strings.Join(lines, "\n"))
	}
}
