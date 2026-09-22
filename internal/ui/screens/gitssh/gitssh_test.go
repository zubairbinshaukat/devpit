package gitssh

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/gitssh"
	"github.com/zubairbinshaukat/devpit/internal/ui/icons"
	"github.com/zubairbinshaukat/devpit/internal/ui/theme"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// testCtx is the render context every screen test in this package uses: the
// dark theme, the unicode icon tier, default config, at the plan's 100×30
// reference size.
func testCtx() uictx.Context {
	return uictx.Context{
		Theme:      theme.For(true),
		Icons:      icons.Unicode(),
		Config:     config.Default(),
		Width:      100,
		Height:     30,
		BodyHeight: 26,
	}
}

func press(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: rune(s[0]), Text: s} }
func enterKey() tea.KeyPressMsg      { return tea.KeyPressMsg{Code: 13} }
func tabKey() tea.KeyPressMsg        { return tea.KeyPressMsg{Code: '\t'} }
func backspaceKey() tea.KeyPressMsg  { return tea.KeyPressMsg{Code: 127} }

// TestExistingKeyRequiresTypedOverwrite is safety rule 15 (docs/safety.md):
// when a key already exists, keygenFn must never be called with Force set
// before the user has typed the overwrite word, and must never be called at
// all if the user answers No.
func TestExistingKeyRequiresTypedOverwrite(t *testing.T) {
	t.Run("typed word gates the overwrite", func(t *testing.T) {
		var calls []gitssh.KeygenOptions
		m := newKeygenScreen()
		m.existsFn = func(string) bool { return true }
		m.emailFn = func(context.Context) (string, error) { return "me@example.com", nil }
		m.keygenFn = func(_ context.Context, opts gitssh.KeygenOptions, _ gitssh.Options) (gitssh.KeygenResult, error) {
			calls = append(calls, opts)
			return gitssh.KeygenResult{PublicKey: "ssh-ed25519 AAAA test", Path: opts.Path}, nil
		}

		ctx := testCtx()
		var scr uictx.Screen = m

		scr, _ = scr.Update(m.Init()(), ctx)

		view := scr.View(ctx)
		if !strings.Contains(view, "already exists") {
			t.Fatalf("expected the overwrite warning, got:\n%s", view)
		}

		// A lower-case word must not unlock the confirmation.
		for _, r := range "overwrite" {
			scr, _ = scr.Update(press(string(r)), ctx)
		}
		scr, _ = scr.Update(enterKey(), ctx) // answers No (word not satisfied); cmd deliberately unread
		if len(calls) != 0 {
			t.Fatalf("keygenFn called %d times after a lower-case word, want 0", len(calls))
		}

		// Clear it and type the exact word.
		for range overwriteWord {
			scr, _ = scr.Update(backspaceKey(), ctx)
		}
		for _, r := range overwriteWord {
			scr, _ = scr.Update(press(string(r)), ctx)
		}

		next, cmd := scr.Update(enterKey(), ctx)
		scr = next
		if cmd == nil {
			t.Fatal("expected the confirm dialog to answer once the word matched")
		}
		ansMsg := cmd()
		next, cmd = scr.Update(ansMsg, ctx)
		scr = next
		if cmd == nil {
			t.Fatal("expected a generate command after answering yes")
		}
		resultMsg := cmd()
		_, _ = scr.Update(resultMsg, ctx)

		if len(calls) != 1 {
			t.Fatalf("keygenFn called %d times after the typed word, want 1", len(calls))
		}
		if !calls[0].Force {
			t.Error("keygenFn was called with Force=false after a confirmed overwrite")
		}
	})

	t.Run("answering no never calls keygen", func(t *testing.T) {
		calls := 0
		m := newKeygenScreen()
		m.existsFn = func(string) bool { return true }
		m.emailFn = func(context.Context) (string, error) { return "", nil }
		m.keygenFn = func(context.Context, gitssh.KeygenOptions, gitssh.Options) (gitssh.KeygenResult, error) {
			calls++
			return gitssh.KeygenResult{}, nil
		}

		ctx := testCtx()
		var scr uictx.Screen = m

		scr, _ = scr.Update(m.Init()(), ctx)

		// Enter on a fresh typed-word dialog answers No without any input.
		next, cmd := scr.Update(enterKey(), ctx)
		scr = next
		if cmd == nil {
			t.Fatal("expected the confirm dialog to answer")
		}
		ansMsg := cmd()
		_, _ = scr.Update(ansMsg, ctx)

		if calls != 0 {
			t.Fatalf("keygenFn called %d times after answering no, want 0", calls)
		}
	})
}

// TestSetIdentityDefaultsToNo is safety rule 2 pinned at the screen level:
// the set-identity screen reviews the change in a confirm dialog before
// writing anything, and that dialog defaults to No.
func TestSetIdentityDefaultsToNo(t *testing.T) {
	calls := 0
	m := newSetIdentityScreen()
	m.setFn = func(context.Context, string, string) error {
		calls++
		return nil
	}
	m.loadFn = func(context.Context) (gitssh.Identity, error) { return gitssh.Identity{}, nil }

	ctx := testCtx()
	var scr uictx.Screen = m

	for _, r := range "Jane Doe" {
		scr, _ = scr.Update(press(string(r)), ctx)
	}
	scr, _ = scr.Update(tabKey(), ctx)
	for _, r := range "jane@example.com" {
		scr, _ = scr.Update(press(string(r)), ctx)
	}

	// Enter moves from the fields to the review dialog.
	scr, _ = scr.Update(enterKey(), ctx)
	view := scr.View(ctx)
	if !strings.Contains(view, "Jane Doe") {
		t.Fatalf("expected the review dialog to name the new identity, got:\n%s", view)
	}

	// Enter again, without an explicit yes, must answer No and never save.
	next, cmd := scr.Update(enterKey(), ctx)
	scr = next
	if cmd == nil {
		t.Fatal("expected the confirm dialog to answer")
	}
	ansMsg := cmd()
	_, _ = scr.Update(ansMsg, ctx)

	if calls != 0 {
		t.Fatalf("SetGitConfig invoked %d times before an explicit yes, want 0", calls)
	}
}

// TestCopyPublicKeyShowsStatus asserts that pressing "c" on a generated key
// copies it through the injected clipboard function (never the real
// clipboard) and reports success both in the view and via uictx.Status.
func TestCopyPublicKeyShowsStatus(t *testing.T) {
	const pub = "ssh-ed25519 AAAA test me@example.com"

	m := newKeygenScreen()
	m.existsFn = func(string) bool { return false }
	m.emailFn = func(context.Context) (string, error) { return "me@example.com", nil }
	m.keygenFn = func(_ context.Context, opts gitssh.KeygenOptions, _ gitssh.Options) (gitssh.KeygenResult, error) {
		return gitssh.KeygenResult{PublicKey: pub, Path: opts.Path}, nil
	}
	var copiedWith string
	m.copyWin32 = func(text string) error {
		copiedWith = text
		return nil
	}

	ctx := testCtx()
	var scr uictx.Screen = m

	next, cmd := scr.Update(m.Init()(), ctx) // no existing key -> generates immediately
	scr = next
	if cmd == nil {
		t.Fatal("expected a generate command")
	}
	scr, _ = scr.Update(cmd(), ctx)

	view := scr.View(ctx)
	if !strings.Contains(view, pub) {
		t.Fatalf("expected the public key in the view, got:\n%s", view)
	}

	next, cmd = scr.Update(press("c"), ctx)
	scr = next
	if cmd == nil {
		t.Fatal("pressing c produced no command")
	}
	next, cmd = scr.Update(cmd(), ctx)
	scr = next

	if copiedWith != pub {
		t.Fatalf("copyWin32 called with %q, want %q", copiedWith, pub)
	}

	view = scr.View(ctx)
	if !strings.Contains(view, "Copied") {
		t.Fatalf("expected a copied confirmation in the view, got:\n%s", view)
	}
	if cmd == nil {
		t.Fatal("expected a status command after copying")
	}
	statusMsg, ok := cmd().(uictx.StatusMsg)
	if !ok {
		t.Fatalf("expected uictx.StatusMsg, got %T", cmd())
	}
	if !strings.Contains(statusMsg.Text, "Copied") {
		t.Fatalf("status text = %q, want it to mention copying", statusMsg.Text)
	}
}

// TestGitMissingShowsInstallHint asserts that the show-identity screen
// surfaces a plain "git is not installed" hint when GitIdentity returns
// *gitssh.NotInstalledError, rather than a raw error string.
func TestGitMissingShowsInstallHint(t *testing.T) {
	m := newIdentityScreen()
	m.identityFn = func(context.Context) (gitssh.Identity, error) {
		return gitssh.Identity{}, &gitssh.NotInstalledError{Tool: "git", Err: &exec.Error{Name: "git", Err: exec.ErrNotFound}}
	}

	ctx := testCtx()
	var scr uictx.Screen = m

	scr, _ = scr.Update(scr.Init()(), ctx)

	view := scr.View(ctx)
	if !strings.Contains(view, "not installed") {
		t.Fatalf("expected an install hint, got:\n%s", view)
	}
}
